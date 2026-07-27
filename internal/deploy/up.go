package deploy

import (
	"context"
	"fmt"
)

func Up(ctx context.Context, r runner, dir string) error {
	cmd := fmt.Sprintf("cd %s && docker compose -f docker-compose.prod.yml up -d", dir)
	_, _, err := r.Run(ctx, cmd)
	return err
}
