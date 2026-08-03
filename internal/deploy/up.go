package deploy

import (
	"context"
	"fmt"
)

// Up runs `docker compose -f docker-compose.prod.yml up -d` on the
// remote host and then reloads the webserver's nginx. Used as stage 5
// of the deploy pipeline and by Rollback. The reload is needed
// because the sync writes bind-mounted files in place (inode
// preserved), so a changed nginx conf is visible to the webserver
// container, but nginx only reads config at start/reload and compose
// does not recreate a service whose spec is unchanged. Reloading
// unconditionally is harmless when nothing changed; the error is
// ignored because a freshly created container already loaded the
// current config.
func Up(ctx context.Context, r runner, dir string) error {
	cmd := fmt.Sprintf("cd %s && docker compose -f docker-compose.prod.yml up -d", dir)
	if _, _, err := r.Run(ctx, cmd); err != nil {
		return err
	}
	reload := fmt.Sprintf("cd %s && docker compose -f docker-compose.prod.yml exec -T webserver nginx -s reload || true", dir)
	_, _, err := r.Run(ctx, reload)
	return err
}
