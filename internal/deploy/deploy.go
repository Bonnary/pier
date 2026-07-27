package deploy

import (
	"context"
	"fmt"
	"time"

	"github.com/pcnerd/pier/internal/config"
)

type Pipeline struct {
	Config    *config.Config
	Env       string
	DeployEnv config.DeployConfig
	Logger    Logger
	SSH       SSHConfig
	Health    HealthConfig
	Now       func() time.Time
}

func (p *Pipeline) Run(ctx context.Context) error {
	if p.Now == nil {
		p.Now = time.Now
	}

	// Phase 1: preflight (local + remote).
	p.Logger.PhaseStart("preflight")
	client, err := p.preflight(ctx)
	if err != nil {
		p.Logger.PhaseEnd("preflight", err)
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
	if err := Sync(ctx, defaultRunner, ".", p.sshAddr()); err != nil {
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
		return BuildError(err)
	}
	p.Logger.PhaseEnd("build", nil)

	// Phase 5: up.
	p.Logger.PhaseStart("up")
	if err := Up(ctx, client, p.DeployEnv.Path); err != nil {
		p.Logger.PhaseEnd("up", err)
		return p.rollback(ctx, client)
	}
	p.Logger.PhaseEnd("up", nil)

	// Phase 6: health.
	p.Logger.PhaseStart("health")
	if err := Probe(ctx, p.Health); err != nil {
		p.Logger.PhaseEnd("health", err)
		return p.rollback(ctx, client)
	}
	p.Logger.PhaseEnd("health", nil)

	// Phase 7: commit.
	p.Logger.PhaseStart("commit")
	if err := p.commit(); err != nil {
		p.Logger.PhaseEnd("commit", err)
		return err
	}
	p.Logger.PhaseEnd("commit", nil)
	return nil
}

func (p *Pipeline) preflight(ctx context.Context) (*Client, error) {
	if p.SSH.Host == "" {
		return nil, fmt.Errorf("deploy.%s.host is empty", p.Env)
	}
	if p.SSH.KeyPath == "" {
		return nil, fmt.Errorf("ssh key path is empty (set --ssh-key or DEPLOY_SSH_KEY)")
	}
	return Dial(ctx, p.SSH)
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

func (p *Pipeline) sshAddr() string {
	return fmt.Sprintf("%s@%s:%s", p.SSH.User, p.SSH.Host, p.DeployEnv.Path)
}

func (p *Pipeline) rollback(ctx context.Context, c *Client) error {
	if err := Rollback(ctx, c, p.DeployEnv.Path, p.Config.Project.Name); err != nil {
		return UpError(err)
	}
	return UpError(fmt.Errorf("health check failed; rolled back"))
}

func (p *Pipeline) commit() error {
	dir := p.DeployEnv.Path
	prev, _ := LoadState(dir)
	s := &State{
		Current:    "gitsha",
		DeployedAt: p.Now().UTC().Format(time.RFC3339),
		DeployedBy: p.SSH.User + "@" + p.SSH.Host,
	}
	if prev != nil && prev.Current != "" {
		s.Previous = prev.Current
	}
	return SaveState(dir, s)
}
