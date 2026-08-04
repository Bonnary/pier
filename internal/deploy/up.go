package deploy

import (
	"context"
	"fmt"
)

// Up runs `docker compose --env-file .env.production -f
// docker-compose.prod.yml up -d --wait --wait-timeout 120` on the
// remote host and then reloads the webserver's nginx. Used as stage 5
// of the deploy pipeline and by Rollback. The --env-file is required:
// the compose file interpolates ${DB_PASSWORD}/${APP_KEY} from it
// (without it compose warns and the app container gets blank secrets
// and 500s). The --wait makes compose return only once every service
// with a healthcheck (postgres, redis, the sidecars) is healthy —
// `up -d` alone returns as soon as containers start, so a fresh
// postgres volume still initializing would refuse connections from
// the very first after_deploy hook (`SQLSTATE[08006] Connection
// refused`), while the same hook succeeds minutes later in an
// interactive shell. --wait-timeout bounds that wait so a never-
// healthy service fails the deploy instead of hanging it. The reload
// is needed because the sync writes bind-mounted files in place
// (inode preserved), so a changed nginx conf is visible to the
// webserver container, but nginx only reads config at start/reload
// and compose does not recreate a service whose spec is unchanged.
// Reloading unconditionally is harmless when nothing changed; the
// error is ignored because a freshly created container already loaded
// the current config.
func Up(ctx context.Context, r runner, dir string) error {
	cmd := fmt.Sprintf("cd %s && docker compose --env-file %s -f %s up -d --wait --wait-timeout 120", dir, remoteEnvFile, remoteComposeFile)
	if _, _, err := r.Run(ctx, cmd); err != nil {
		return err
	}
	reload := fmt.Sprintf("cd %s && docker compose --env-file %s -f %s exec -T webserver nginx -s reload || true", dir, remoteEnvFile, remoteComposeFile)
	_, _, err := r.Run(ctx, reload)
	return err
}
