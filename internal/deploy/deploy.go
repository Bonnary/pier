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
	"net"
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

// Pipeline is the top-level deploy driver. One Pipeline is constructed
// per `pier deploy <env>` invocation and Run is called exactly once.
type Pipeline struct {
	Config    *config.Config
	Env       string
	DeployEnv config.DeployConfig
	Logger    Logger
	SSH       SSHConfig
	Health    HealthConfig
	Now       func() time.Time
}

// Run executes the full deploy pipeline: preflight, render, sync,
// build, before_deploy hooks, up, after_deploy hooks, health probe,
// commit. On any up- or health-stage failure
// the previous image is retagged and re-deployed (Rollback) before the
// error is returned. Now is set to time.Now if nil so tests can pin
// the timestamp written to state.json.
func (p *Pipeline) Run(ctx context.Context) error {
	if p.Now == nil {
		p.Now = time.Now
	}

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

	// Phase 2: render (local) — re-render docker-compose.prod.yml.
	p.Logger.PhaseStart("render")
	stackMod, err := p.render()
	if err != nil {
		p.Logger.PhaseEnd("render", err)
		return err
	}
	p.Logger.PhaseEnd("render", nil)
	_ = stackMod

	// Phase 3: sync.
	p.Logger.PhaseStart("sync")
	if err := client.SyncDir(ctx, ".", p.DeployEnv.Path, rsyncExcludes); err != nil {
		p.Logger.PhaseEnd("sync", err)
		return PreflightError(err)
	}
	p.Logger.PhaseEnd("sync", nil)

	// Phase 4: build.
	p.Logger.PhaseStart("build")
	if err := Build(ctx, client, p.DeployEnv.Path, p.Config.Project.Name, "gitsha", func(l string) {
		p.Logger.Log("build", "%s", l)
	}); err != nil {
		p.Logger.PhaseEnd("build", err)
		return RemoteBuildError(p.SSH.Host, err)
	}
	p.Logger.PhaseEnd("build", nil)

	// Phase 5: before_deploy — run user hooks in the app container
	// while the old release is still serving (after the build, before
	// up). Failures warn and continue; they never abort the deploy.
	p.runHooks(ctx, client, "before_deploy", p.DeployEnv.BeforeDeploy)

	// Phase 6: up.
	p.Logger.PhaseStart("up")
	if err := Up(ctx, client, p.DeployEnv.Path); err != nil {
		p.Logger.PhaseEnd("up", err)
		return p.rollback(ctx, client)
	}
	p.Logger.PhaseEnd("up", nil)

	// Phase 7: after_deploy — run user hooks in the app container
	// against the new release (after up and the nginx reload, before
	// the health probe). Failures warn and continue; they never abort
	// the deploy.
	p.runHooks(ctx, client, "after_deploy", p.DeployEnv.AfterDeploy)

	// Phase 8: health.
	p.Logger.PhaseStart("health")
	if err := Probe(ctx, p.Health); err != nil {
		p.Logger.PhaseEnd("health", err)
		return p.rollback(ctx, client)
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
		return nil, fmt.Errorf("deploy.%s.host is empty", p.Env)
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
	return client, nil
}

func (p *Pipeline) render() (any, error) {
	// Re-render docker-compose.prod.yml from pier.toml. Full implementation:
	// 1. Read pier.toml (already in p.Config).
	// 2. Call stack.ForName(...).GenerateProdFiles.
	// 3. Write docker-compose.prod.yml and the runtime to a temp dir.
	// 4. Sync to remote as part of the sync phase.
	// Skeleton: returns a placeholder.
	return nil, nil
}

func (p *Pipeline) rollback(ctx context.Context, c *Client) error {
	if err := Rollback(ctx, SFTPStateStore{Client: c}, c, p.DeployEnv.Path, p.Config.Project.Name); err != nil {
		return RemoteUpError(p.SSH.Host, err)
	}
	return RemoteUpError(p.SSH.Host, fmt.Errorf("health check failed; rolled back"))
}

func (p *Pipeline) commit(ctx context.Context, c *Client) error {
	st := SFTPStateStore{Client: c}
	prev, err := st.ReadState(ctx, p.DeployEnv.Path)
	if err != nil {
		return fmt.Errorf("deploy: read state: %w", err)
	}
	s := &State{
		Current:    "gitsha",
		DeployedAt: p.Now().UTC().Format(time.RFC3339),
		DeployedBy: p.SSH.User + "@" + p.SSH.Host,
	}
	if prev != nil && prev.Current != "" {
		s.Previous = prev.Current
	}
	return st.WriteState(ctx, p.DeployEnv.Path, s)
}

// ResolvedURL returns the public URL for the deployed env: scheme
// and port resolved from [deploy.<env>].tls and the "laravel" port
// (default 443 when TLS is enabled, 80 for plain HTTP, or the
// per-env override from [deploy.<env>.ports.laravel]). The host is
// the project domain when it resolves (DNS or /etc/hosts); otherwise
// it falls back to the deploy host IP, so the printed URL is usable
// before DNS entries point the domain at the server.
func ResolvedURL(cfg config.Config, env string) string {
	host := cfg.Project.Domain
	if !hostResolvable(host) {
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

// HealthURL returns the URL the health probe GETs for env: the
// deploy host IP from [deploy.<env>].host with the resolved scheme
// and "laravel" port, plus "/up". Probing the host IP instead of the
// public domain means health checks pass before DNS/hosts entries
// point the domain at the server.
func HealthURL(cfg config.Config, env string) string {
	deployCfg, ok := cfg.Deploy[env]
	if !ok {
		deployCfg = config.DeployConfig{}
	}
	return fmt.Sprintf("%s://%s:%d/up", laravelpkg.WebScheme(cfg, env), deployCfg.Host, laravelpkg.WebPort(cfg, env))
}
