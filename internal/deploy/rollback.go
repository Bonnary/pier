package deploy

import (
	"context"
	"fmt"
)

// Rollback retags the previous deploy's image to the tag the prod
// compose file actually references and re-runs Up. target is that tag
// without the project prefix: "latest" in host_server mode (the
// compose keeps the build context and references <project>:latest)
// and "current" in the image modes (<project>:current). The
// "previous" image comes from .pier/state.json on the deploy host
// (written by Pipeline.commit at the end of every successful deploy).
// Returns an error if state.json is missing or if the previous image
// tag is empty (i.e. there is no prior deploy to roll back to). st
// may be nil, in which case local disk is used (unit tests); the
// pipeline always passes sftpStateStore.
func Rollback(ctx context.Context, st stateStore, r runner, dir, project, target string) error {
	if st == nil {
		st = localStateStore{}
	}
	state, err := st.ReadState(ctx, dir)
	if err != nil {
		return fmt.Errorf("deploy: rollback: %w", err)
	}
	if state == nil || !state.HasPrevious() {
		return fmt.Errorf("deploy: rollback: no previous deploy to roll back to")
	}
	cmd := fmt.Sprintf("cd %s && docker tag %s:%s %s:%s", dir, project, state.Previous, project, target)
	if _, _, err := r.Run(ctx, cmd); err != nil {
		return fmt.Errorf("deploy: rollback tag: %w", err)
	}
	if err := Up(ctx, r, dir); err != nil {
		return fmt.Errorf("deploy: rollback up: %w", err)
	}
	return nil
}
