package deploy

import (
	"context"
	"fmt"
)

// Build runs `docker compose -f docker-compose.prod.yml build --pull`
// on the remote host, streaming each output line to onLine. Used as
// stage 4 of the deploy pipeline.
func Build(ctx context.Context, r runner, dir, project, sha string, onLine func(string)) error {
	cmd := fmt.Sprintf("cd %s && docker compose -f docker-compose.prod.yml build --pull", dir)
	return r.RunStream(ctx, cmd, onLine)
}

// Tag retags the just-built <project>:latest image to <project>:<sha>
// (the immutable deploy record) and to <project>:current (the live
// alias that Rollback overwrites).
func Tag(ctx context.Context, r runner, project, sha string) error {
	tag := fmt.Sprintf("docker tag %s:latest %s:%s && docker tag %s:latest %s:current", project, project, sha, project, project)
	_, _, err := r.Run(ctx, tag)
	return err
}
