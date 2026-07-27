package deploy

import (
	"context"
	"fmt"
)

func Rollback(ctx context.Context, r runner, dir, project string) error {
	state, err := LoadState(dir)
	if err != nil {
		return fmt.Errorf("deploy: rollback: %w", err)
	}
	if state == nil || !state.HasPrevious() {
		return fmt.Errorf("deploy: rollback: no previous deploy to roll back to")
	}
	cmd := fmt.Sprintf("cd %s && docker tag %s:%s %s:current", dir, project, state.Previous, project)
	if _, _, err := r.Run(ctx, cmd); err != nil {
		return fmt.Errorf("deploy: rollback tag: %w", err)
	}
	if err := Up(ctx, r, dir); err != nil {
		return fmt.Errorf("deploy: rollback up: %w", err)
	}
	return nil
}
