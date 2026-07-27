package deploy

import (
	"context"
	"fmt"
)

func Build(ctx context.Context, r runner, dir, project, sha string, onLine func(string)) error {
	cmd := fmt.Sprintf("cd %s && docker compose -f docker-compose.prod.yml build --pull", dir)
	return r.RunStream(ctx, cmd, onLine)
}

func Tag(ctx context.Context, r runner, project, sha string) error {
	tag := fmt.Sprintf("docker tag %s:latest %s:%s && docker tag %s:latest %s:current", project, project, sha, project, project)
	_, _, err := r.Run(ctx, tag)
	return err
}
