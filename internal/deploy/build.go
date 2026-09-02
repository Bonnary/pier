package deploy

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
)

// Build runs `docker compose --env-file .env.production -f
// docker-compose.prod.yml build --pull` on the remote host, streaming
// each output line to onLine. Used as stage 4 of the deploy pipeline.
// The --env-file is passed so ${...} interpolation in the compose file
// resolves instead of warning; --pull keeps the image fresh.
func Build(ctx context.Context, r runner, dir, project, sha string, onLine func(string)) error {
	cmd := fmt.Sprintf("%sdocker compose --env-file %s -f %s build --pull", remotePrefix(dir), remoteEnvFile, remoteComposeFile)
	return r.RunStream(ctx, cmd, onLine)
}

// Tag retags the just-built <project>:latest image to <project>:<sha>
// (the immutable deploy record) and to <project>:current (the live
// alias that Rollback overwrites).
func Tag(ctx context.Context, r runner, project, sha string) error {
	tag := fmt.Sprintf("docker tag %s:%s %s:%s && docker tag %s:%s %s:%s", quoteShell(project), "latest", quoteShell(project), quoteShell(sha), quoteShell(project), "latest", quoteShell(project), "current")
	_, _, err := r.Run(ctx, tag)
	return err
}

// imageBuildArgs returns the `docker build` arguments for the
// production image, shared by the local and remote build paths. The
// context is the project root and the Dockerfile is the per-PHP
// Dockerfile.prod; the WWWUSER/WWWGROUP args are required because the
// runtime Dockerfile's ARG WWWGROUP has no default.
func imageBuildArgs(php, project, sha string) []string {
	return []string{"build", "--pull",
		"-f", "docker/" + php + "/Dockerfile.prod",
		"--build-arg", "WWWUSER=1337", "--build-arg", "WWWGROUP=1337",
		"-t", project + ":" + sha, "."}
}

// BuildLocalImage runs plain `docker build` on the local machine in
// dir (the project root), streaming stdout/stderr lines to onLine.
// Used as the build stage of the local_machine builder mode.
func BuildLocalImage(ctx context.Context, dir, php, project, sha string, onLine func(string)) error {
	cmd := exec.CommandContext(ctx, "docker", imageBuildArgs(php, project, sha)...)
	cmd.Dir = dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var stderrErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			onLine(sc.Text())
		}
		stderrErr = sc.Err()
	}()
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		onLine(sc.Text())
	}
	<-done
	if err := sc.Err(); err != nil {
		return err
	}
	if stderrErr != nil {
		return stderrErr
	}
	return cmd.Wait()
}

// RemoteBuildImage runs plain `docker build` on a remote build server
// inside dir (the synced project root), streaming output lines to
// onLine. Used as the build stage of the build_server builder mode.
// dir, php, project, and sha are shell-quoted so a hostile value in
// pier.toml cannot inject commands into the remote shell (F2).
func RemoteBuildImage(ctx context.Context, r runner, dir, php, project, sha string, onLine func(string)) error {
	cmd := remotePrefix(dir) + "docker build --pull -f " +
		quoteShell("docker/"+php+"/Dockerfile.prod") +
		" --build-arg WWWUSER=1337 --build-arg WWWGROUP=1337 -t " +
		quoteShell(project+":"+sha) + " ."
	return r.RunStream(ctx, cmd, onLine)
}
