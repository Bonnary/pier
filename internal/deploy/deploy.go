// Package deploy runs the production deploy pipeline over SSH:
// preflight, render, sync, build, before_deploy hooks, up,
// after_deploy hooks, health probe, and commit (the .pier/state.json
// write that records the active image tag for `pier rollback`). The
// package owns the typed error contract (ExitError, Kind) and the SSH
// client used for every remote command.
package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/Bonnary/pier/internal/config"
	laravelpkg "github.com/Bonnary/pier/internal/stack/laravel"
)

// pipelineDial is a seam for tests to inject a fake Dial into the
// deploy pipeline's preflight phase. It returns a bootstrapConn so
// tests can observe Close.
var pipelineDial = func(ctx context.Context, cfg SSHConfig) (bootstrapConn, error) {
	return Dial(ctx, cfg)
}

// pipelineProbe is a seam for tests to inject a fake bootstrap probe
// into the deploy pipeline's preflight phase.
var pipelineProbe = ProbeBootstrap

// pipelineEnsurePath is a seam for tests to inject a fake path-ensure
// step into the deploy pipeline's preflight phase.
var pipelineEnsurePath = func(ctx context.Context, c *Client, path string) error {
	return EnsureDeployPath(ctx, c, path)
}

// pipelineCheckDNS is a seam for tests to inject a fake domain-DNS
// preflight step into the deploy pipeline's preflight phase.
var pipelineCheckDNS = func(cfg config.Config, env string) error {
	return checkDomainDNS(cfg, env)
}

// Pipeline is the top-level deploy driver. One Pipeline is constructed
// per `pier deploy <env>` invocation and Run is called exactly once.
type Pipeline struct {
	Config    *config.Config
	Env       string
	DeployEnv config.DeployConfig
	Logger    Logger
	SSH       SSHConfig
	// BuildSSH is the SSH config of the dedicated build server, used
	// only when [deploy.<env>].builder is "build_server". Dialed in
	// preflight like the host connection.
	BuildSSH SSHConfig
	// buildClient is the dialed build-server connection, set by
	// preflight in build_server mode.
	buildClient *Client
	// saveLocal, when set, replaces the local `docker save` invocation
	// in the transfer phase (tests script the docker side).
	saveLocal func(ctx context.Context, dir, image string, sink io.Writer, onLine func(string)) error
	Health    HealthConfig
	Now       func() time.Time
	// tag is the immutable image tag for this deploy (git SHA or
	// timestamp fallback), computed at the top of Run and used for
	// the build output, the transferred image, and state.json.
	tag string
}

// Run executes the full deploy pipeline: preflight, render, sync,
// build, before_deploy hooks, up, after_deploy hooks, health probe,
// commit. A failing before_deploy hook aborts the deploy (before the
// new release starts); a failing after_deploy hook, up, or health
// probe retags the previous image and re-deploys it (Rollback) before
// the error is returned. Now is set to time.Now if nil so tests can
// pin the timestamp written to state.json.
func (p *Pipeline) Run(ctx context.Context) error {
	if p.Now == nil {
		p.Now = time.Now
	}
	p.tag = deployTag()

	// Phase 1: preflight (local + remote).
	p.Logger.PhaseStart("preflight")
	client, err := p.preflight(ctx)
	if err != nil {
		p.Logger.PhaseEnd("preflight", err)
		// An interactive abort (Ctrl+C on the password prompt) is
		// not a preflight failure: it must exit 130, not 2.
		if errors.Is(err, ErrAborted) {
			return err
		}
		return PreflightError(err)
	}
	p.Logger.PhaseEnd("preflight", nil)
	defer client.Close()
	if p.buildClient != nil {
		defer p.buildClient.Close()
	}

	// Phase 2: render (local) — re-render docker-compose.prod.yml and
	// .env.production from pier.toml so the sync ships per-env
	// services and port overrides.
	p.Logger.PhaseStart("render")
	if err := p.render(); err != nil {
		p.Logger.PhaseEnd("render", err)
		return err
	}
	p.Logger.PhaseEnd("render", nil)

	// Phase 3: sync.
	p.Logger.PhaseStart("sync")
	var syncErr error
	switch p.DeployEnv.BuilderMode() {
	case "host_server":
		syncErr = client.SyncDir(ctx, ".", p.DeployEnv.Path, rsyncExcludes)
	case "local_machine":
		// The host only needs the deploy files; the build runs from
		// the local working tree.
		syncErr = client.SyncDir(ctx, ".", p.DeployEnv.Path, deployFilesOnly)
	case "build_server":
		if syncErr = p.buildClient.SyncDir(ctx, ".", p.DeployEnv.BuildPath, rsyncExcludes); syncErr == nil {
			syncErr = client.SyncDir(ctx, ".", p.DeployEnv.Path, deployFilesOnly)
		}
	}
	if syncErr != nil {
		p.Logger.PhaseEnd("sync", syncErr)
		return PreflightError(syncErr)
	}
	p.Logger.PhaseEnd("sync", nil)

	// Phase 4: build.
	p.Logger.PhaseStart("build")
	switch p.DeployEnv.BuilderMode() {
	case "host_server":
		if err := Build(ctx, client, p.DeployEnv.Path, p.Config.Project.Name, p.tag, func(l string) {
			p.Logger.Log("build", "%s", l)
		}); err != nil {
			p.Logger.PhaseEnd("build", err)
			return RemoteBuildError(p.SSH.Host, err)
		}
		// Tag the fresh :latest as the immutable sha record and the
		// :current alias that Rollback overwrites. This is what makes
		// `pier rollback` able to retag the previous image.
		if err := Tag(ctx, client, p.Config.Project.Name, p.tag); err != nil {
			p.Logger.PhaseEnd("build", err)
			return RemoteBuildError(p.SSH.Host, err)
		}
	case "local_machine":
		if err := BuildLocalImage(ctx, ".", p.Config.Stack.PHP, p.Config.Project.Name, p.tag, func(l string) {
			p.Logger.Log("build", "%s", l)
		}); err != nil {
			p.Logger.PhaseEnd("build", err)
			return BuildError(err)
		}
	case "build_server":
		if err := RemoteBuildImage(ctx, p.buildClient, p.DeployEnv.BuildPath, p.Config.Stack.PHP, p.Config.Project.Name, p.tag, func(l string) {
			p.Logger.Log("build", "%s", l)
		}); err != nil {
			p.Logger.PhaseEnd("build", err)
			return RemoteBuildError(p.buildClient.Config.Host, err)
		}
	}
	p.Logger.PhaseEnd("build", nil)

	// Phase 4b: transfer — image modes stream the just-built image
	// into the host's docker daemon and retag it as :current.
	if p.DeployEnv.BuilderMode() != "host_server" {
		if err := p.transfer(ctx, client); err != nil {
			return err
		}
	}

	// Phase 5: before_deploy — run user hooks in the app container
	// while the old release is still serving (after the build, before
	// up). On a first deploy there is no app container yet (and no
	// old release to prepare), so the phase is skipped entirely. A
	// failing hook aborts the deploy; the old release is still
	// serving, so no rollback runs.
	if len(p.DeployEnv.BeforeDeploy) > 0 {
		st := SFTPStateStore{Client: client}
		prev, err := st.ReadState(ctx, p.DeployEnv.Path)
		if err != nil {
			return RemoteHookError(p.SSH.Host, fmt.Errorf("before_deploy: read deploy state: %w", err))
		}
		if prev == nil {
			p.Logger.Log("before_deploy", "skipped %d command(s): first deploy, no app container yet", len(p.DeployEnv.BeforeDeploy))
		} else if err := p.runHooks(ctx, client, "before_deploy", p.DeployEnv.BeforeDeploy); err != nil {
			return RemoteHookError(p.SSH.Host, err)
		}
	}

	// Phase 6: up.
	p.Logger.PhaseStart("up")
	if err := Up(ctx, client, p.DeployEnv.Path); err != nil {
		p.Logger.PhaseEnd("up", err)
		return p.rollback(ctx, client, err, p.upError)
	}
	p.Logger.PhaseEnd("up", nil)

	// Phase 7: after_deploy — run user hooks in the app container
	// against the new release (after up and the caddy reload, before
	// the health probe). A failing hook aborts the deploy and rolls
	// back to the previous image, like a failed health probe.
	if err := p.runHooks(ctx, client, "after_deploy", p.DeployEnv.AfterDeploy); err != nil {
		return p.rollback(ctx, client, err, p.hookError)
	}

	// Phase 8: health.
	p.Logger.PhaseStart("health")
	if err := Probe(ctx, p.Health); err != nil {
		p.Logger.PhaseEnd("health", err)
		return p.rollback(ctx, client, err, p.upError)
	}
	p.Logger.PhaseEnd("health", nil)

	// Phase 9: commit.
	p.Logger.PhaseStart("commit")
	if err := p.commit(ctx, client); err != nil {
		p.Logger.PhaseEnd("commit", err)
		return err
	}
	p.Logger.PhaseEnd("commit", nil)

	p.Logger.Emit(Event{
		Phase:   "done",
		Message: "URL: " + ResolvedURL(*p.Config, p.Env),
		Data:    map[string]any{"url": ResolvedURL(*p.Config, p.Env)},
	})
	return nil
}

// preflight validates SSH config, dials the host, probes for a
// bootstrapped server (docker accessible without sudo), and ensures
// the deploy path exists. Unbootstrapped hosts fail fast with
// NotBootstrappedError instead of hanging on a hidden sudo prompt
// during the build phase; an unwritable deploy path fails with an
// actionable error naming the exact commands to fix it.
func (p *Pipeline) preflight(ctx context.Context) (*Client, error) {
	if p.SSH.Host == "" {
		return nil, fmt.Errorf("deploy.%s is unconfigured: set host, user, path, and branch in pier.toml (pier init scaffolds the section)", p.Env)
	}
	if p.SSH.KeyPath == "" {
		return nil, fmt.Errorf("ssh key path is empty (set --ssh-key or DEPLOY_SSH_KEY)")
	}
	conn, err := pipelineDial(ctx, p.SSH)
	if err != nil {
		return nil, err
	}
	ok, err := pipelineProbe(ctx, conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if !ok {
		conn.Close()
		return nil, NotBootstrappedError(p.Env)
	}
	client, ok := conn.(*Client)
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("internal: dial returned %T, want *Client", conn)
	}
	if err := pipelineEnsurePath(ctx, client, p.DeployEnv.Path); err != nil {
		client.Close()
		return nil, fmt.Errorf(
			"deploy path %s on %s is not writable for %s.\nCreate it once with:\n  sudo mkdir -p %s\n  sudo chown %s:%s %s\n(or re-run `pier bootstrap %s` to create it automatically.)",
			p.DeployEnv.Path, p.SSH.Host, p.SSH.User,
			p.DeployEnv.Path, p.SSH.User, p.SSH.User, p.DeployEnv.Path, p.Env)
	}
	if p.Config != nil {
		if err := pipelineCheckDNS(*p.Config, p.Env); err != nil {
			client.Close()
			return nil, err
		}
	}
	if p.DeployEnv.BuilderMode() == "build_server" {
		if p.BuildSSH.Host == "" {
			client.Close()
			return nil, fmt.Errorf("deploy.%s.builder = \"build_server\" requires build_host, build_user, and build_path in pier.toml", p.Env)
		}
		conn, err := pipelineDial(ctx, p.BuildSSH)
		if err != nil {
			client.Close()
			return nil, err
		}
		ok, err := pipelineProbe(ctx, conn)
		if err != nil {
			conn.Close()
			client.Close()
			return nil, err
		}
		if !ok {
			conn.Close()
			client.Close()
			return nil, NotBootstrappedError(p.Env + " build server")
		}
		buildClient, ok := conn.(*Client)
		if !ok {
			conn.Close()
			client.Close()
			return nil, fmt.Errorf("internal: dial returned %T, want *Client", conn)
		}
		if err := pipelineEnsurePath(ctx, buildClient, p.DeployEnv.BuildPath); err != nil {
			buildClient.Close()
			client.Close()
			return nil, fmt.Errorf(
				"build path %s on %s is not writable for %s.\nCreate it once with:\n  sudo mkdir -p %s\n  sudo chown %s:%s %s\n(or re-run `pier bootstrap %s` to create it automatically.)",
				p.DeployEnv.BuildPath, p.BuildSSH.Host, p.BuildSSH.User,
				p.DeployEnv.BuildPath, p.BuildSSH.User, p.BuildSSH.User, p.DeployEnv.BuildPath, p.Env)
		}
		p.buildClient = buildClient
	}
	return client, nil
}

// render re-renders the local compose and env files. When no local
// .env.production exists, renderProdFiles creates one from fresh
// placeholders; shipping that to the server would silently overwrite
// a real .env.production holding secrets on the deploy host, so the
// deploy aborts (after the file is created locally for review).
func (p *Pipeline) render() error {
	_, err := os.Stat(".env.production")
	switch {
	case err == nil:
		return renderProdFiles(".", p.Config, p.Env)
	case !os.IsNotExist(err):
		return fmt.Errorf("render: stat .env.production: %w", err)
	}
	if err := renderProdFiles(".", p.Config, p.Env); err != nil {
		return err
	}
	return fmt.Errorf("render: no local .env.production existed — generated a fresh template at .env.production with placeholder values (APP_KEY, DB_PASSWORD, ...). Deploy aborted before sync: review and fill in the real values, then re-run pier deploy; the sync would otherwise overwrite any .env.production already on the server")
}

// rollback retags the previous image as the tag the prod compose file
// references and re-runs Up after an up-, after_deploy-, or
// health-stage failure. The host_server compose variant keeps the
// build context and references <project>:latest, so the rollback
// retag must repoint :latest (Tag only adds tags, so :latest would
// keep serving the broken release); the image modes reference
// <project>:current. cause names the failure that triggered the
// rollback; classify wraps the final error so it carries the right
// sentinel and exit code (up/health failures stay ErrUp, hook
// failures stay ErrHooks). When there is no previous deploy on record
// (first deploy) there is nothing to roll back to, so the cause is
// reported directly instead of a dead-end "no previous deploy"
// rollback error that hides what actually failed. When the rollback
// itself fails, that failure is reported instead.
func (p *Pipeline) rollback(ctx context.Context, c *Client, cause error, classify func(error) error) error {
	st := SFTPStateStore{Client: c}
	state, err := st.ReadState(ctx, p.DeployEnv.Path)
	if err != nil {
		return classify(fmt.Errorf("rollback: read deploy state: %w", err))
	}
	if state == nil || !state.HasPrevious() {
		p.Logger.Log("rollback", "skipped: no previous image to roll back to (first deploy); reporting the failure directly")
		return classify(cause)
	}
	target := "current"
	if p.DeployEnv.BuilderMode() == "host_server" {
		target = "latest"
	}
	if err := Rollback(ctx, st, c, p.DeployEnv.Path, p.Config.Project.Name, target); err != nil {
		return classify(err)
	}
	return classify(fmt.Errorf("%w; rolled back", cause))
}

// upError wraps a failed up/health cause as an up-category remote
// error; hookError wraps a failed hook cause as a hooks-category
// remote error. Both are classify funcs for Pipeline.rollback.
func (p *Pipeline) upError(e error) error   { return RemoteUpError(p.SSH.Host, e) }
func (p *Pipeline) hookError(e error) error { return RemoteHookError(p.SSH.Host, e) }

// transfer streams the just-built image from the build side (local
// docker or the build server) into the host's docker daemon and
// retags it as <project>:current, so the image-variant compose file's
// `image: <project>:current` reference resolves. A failed save or
// load leaves the old :current image untouched — the deploy aborts
// with the old release still serving, so no rollback is needed.
func (p *Pipeline) transfer(ctx context.Context, hostClient *Client) error {
	p.Logger.PhaseStart("transfer")
	image := p.Config.Project.Name + ":" + p.tag
	var saver imageSaver
	if p.DeployEnv.BuilderMode() == "local_machine" {
		saver = localSaver{dir: ".", save: p.saveLocal}
	} else {
		saver = remoteSaver{c: p.buildClient}
	}
	n, err := TransferImage(ctx, saver, hostClient, image, func(l string) {
		p.Logger.Log("transfer", "%s", l)
	})
	if err != nil {
		p.Logger.PhaseEnd("transfer", err)
		return err
	}
	p.Logger.Log("transfer", "image %s (%d bytes) loaded on %s", image, n, p.SSH.Host)
	if _, _, err := hostClient.Run(ctx, "docker tag "+image+" "+p.Config.Project.Name+":current"); err != nil {
		p.Logger.PhaseEnd("transfer", err)
		return fmt.Errorf("transfer: retag current: %w", err)
	}
	p.Logger.PhaseEnd("transfer", nil)
	return nil
}

func (p *Pipeline) commit(ctx context.Context, c *Client) error {
	st := SFTPStateStore{Client: c}
	prev, err := st.ReadState(ctx, p.DeployEnv.Path)
	if err != nil {
		return fmt.Errorf("deploy: read state: %w", err)
	}
	s := &State{
		Current:    p.tag,
		DeployedAt: p.Now().UTC().Format(time.RFC3339),
		DeployedBy: p.SSH.User + "@" + p.SSH.Host,
	}
	if prev != nil && prev.Current != "" {
		s.Previous = prev.Current
	}
	return st.WriteState(ctx, p.DeployEnv.Path, s)
}

// ResolvedURL returns the public URL for the deployed env: scheme
// and port resolved from the env's effective domain and the
// "laravel" port (default 443 with a domain, 80 for the plain-HTTP
// default, or the per-env override from
// [deploy.<env>.ports.laravel]). The host is the effective domain
// when it resolves (DNS or /etc/hosts); otherwise it falls back to
// the deploy host IP, so the printed URL is usable before DNS
// entries point the domain at the server.
func ResolvedURL(cfg config.Config, env string) string {
	domain := cfg.DomainForEnv(env)
	host := domain
	if host == "" || !hostResolvable(host) {
		if dc, ok := cfg.Deploy[env]; ok && dc.Host != "" {
			host = dc.Host
		}
	}
	return fmt.Sprintf("%s://%s:%d", laravelpkg.WebScheme(cfg, env), host, laravelpkg.WebPort(cfg, env))
}

// lookupHost resolves a hostname to its IP addresses; a seam so tests
// can pin DNS outcomes instead of depending on the ambient resolver.
var lookupHost = net.LookupHost

// hostResolvable reports whether host resolves to at least one IP
// address. Bare IPs pass through (net.LookupHost echoes them back).
func hostResolvable(host string) bool {
	addrs, err := lookupHost(host)
	return err == nil && len(addrs) > 0
}

// HealthURL returns the URL the health probe GETs for env. With an
// effective domain the probe targets https://<domain>/up with normal
// TLS verification — an end-to-end check that exercises the real
// Let's Encrypt certificate (the DNS preflight already verified the
// domain points at the deploy host). Without a domain it probes the
// deploy host IP with the resolved "laravel" port, so health checks
// pass before DNS or /etc/hosts entries exist.
func HealthURL(cfg config.Config, env string) string {
	if domain := cfg.DomainForEnv(env); domain != "" {
		return "https://" + domain + "/up"
	}
	deployCfg, ok := cfg.Deploy[env]
	if !ok {
		deployCfg = config.DeployConfig{}
	}
	return fmt.Sprintf("http://%s:%d/up", deployCfg.Host, laravelpkg.WebPort(cfg, env))
}
