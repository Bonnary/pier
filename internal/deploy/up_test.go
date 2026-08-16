package deploy

import (
	"context"
	"strings"
	"testing"
)

type recordingUpRunner struct {
	cmds *[]string
}

func (r *recordingUpRunner) Run(ctx context.Context, cmd string) ([]byte, []byte, error) {
	*r.cmds = append(*r.cmds, cmd)
	return nil, nil, nil
}

func (r *recordingUpRunner) RunStream(ctx context.Context, cmd string, onLine func(string)) error {
	*r.cmds = append(*r.cmds, cmd)
	return nil
}

func TestUpReloadsWebserverCaddy(t *testing.T) {
	var cmds []string
	r := &recordingUpRunner{cmds: &cmds}
	if err := Up(context.Background(), r, "/srv/myapp"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if len(cmds) != 2 {
		t.Fatalf("Up ran %d commands, want 2 (compose up + caddy reload); got: %v", len(cmds), cmds)
	}
	if !strings.Contains(cmds[0], "docker compose --env-file .env.production -f docker-compose.prod.yml up -d --wait --wait-timeout 120 --remove-orphans") {
		t.Errorf("up command = %q, want `docker compose --env-file .env.production -f docker-compose.prod.yml up -d --wait --wait-timeout 120 --remove-orphans` (--remove-orphans stops and removes containers of services dropped from the compose file — the per-env teardown contract — while preserving named volumes)", cmds[0])
	}
	if !strings.Contains(cmds[1], "--env-file .env.production") {
		t.Errorf("reload command = %q, want it to pass --env-file .env.production so compose interpolation does not warn", cmds[1])
	}
	if !strings.Contains(cmds[1], "exec -T webserver caddy reload --config /etc/caddy/Caddyfile") {
		t.Errorf("reload command = %q, want it to reload the webserver's caddy so bind-mounted Caddyfile changes (the sync rewrites files in place, preserving the inode) take effect without a container recreate", cmds[1])
	}
}
