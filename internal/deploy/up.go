package deploy

import (
	"context"
	"fmt"
)

// Up runs `docker compose -f docker-compose.prod.yml up -d` on the
// remote host. Used as stage 5 of the deploy pipeline and by
// Rollback.
func Up(ctx context.Context, r runner, dir string) error {
	cmd := fmt.Sprintf("cd %s && docker compose -f docker-compose.prod.yml up -d", dir)
	_, _, err := r.Run(ctx, cmd)
	return err
}
