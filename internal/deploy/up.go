package deploy

import (
	"context"
	"fmt"
)

// Up runs `docker compose --env-file .env.production -f
// docker-compose.prod.yml up -d --wait --wait-timeout 120
// --remove-orphans` on the remote host and then reloads the
// webserver's caddy. Used as stage 5
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
// (inode preserved), so a changed Caddyfile is visible to the
// webserver container, but caddy only reads config at start/reload
// and compose does not recreate a service whose spec is unchanged.
// Reloading unconditionally is harmless when nothing changed; the
// error is ignored because a freshly created container already loaded
// the current config. --remove-orphans stops and removes containers
// whose service is no longer in the compose file — exactly the
// sidecars the per-env render dropped — while named volumes
// (mysql_data, s3_data, ...) are preserved.
func Up(ctx context.Context, r runner, dir string) error {
	cmd := fmt.Sprintf("%sdocker compose --env-file %s -f %s up -d --wait --wait-timeout 120 --remove-orphans", remotePrefix(dir), remoteEnvFile, remoteComposeFile)
	if _, _, err := r.Run(ctx, cmd); err != nil {
		return err
	}
	reload := fmt.Sprintf("%sdocker compose --env-file %s -f %s exec -T webserver caddy reload --config /etc/caddy/Caddyfile || true", remotePrefix(dir), remoteEnvFile, remoteComposeFile)
	_, _, err := r.Run(ctx, reload)
	return err
}
