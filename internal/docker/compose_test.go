package docker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls    []string
	ok       bool
	stdout   []byte
	stderr   []byte
	stdin    io.Reader
	failWith error
}

func (f *fakeRunner) Run(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
	call := name
	for _, a := range args {
		call += " " + a
	}
	f.calls = append(f.calls, call)
	f.stdin = stdin
	if f.stdout != nil {
		stdout.Write(f.stdout)
	}
	if f.stderr != nil {
		stderr.Write(f.stderr)
	}
	if f.failWith != nil {
		return f.failWith
	}
	if !f.ok {
		return errors.New("fakeRunner: not ok")
	}
	return nil
}

func TestComposeUp(t *testing.T) {
	f := &fakeRunner{ok: true}
	c := &Compose{Workdir: "/tmp", File: "docker-compose.yml", Runner: f}
	if err := c.Up(context.Background()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if len(f.calls) != 1 || f.calls[0] != "docker compose -f /tmp/docker-compose.yml --project-directory /tmp up -d" {
		t.Errorf("calls = %v", f.calls)
	}
}

func TestComposeUpWithServices(t *testing.T) {
	f := &fakeRunner{ok: true}
	c := &Compose{Workdir: "/tmp", File: "docker-compose.yml", Runner: f}
	if err := c.Up(context.Background(), "redis", "mysql"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if f.calls[0] != "docker compose -f /tmp/docker-compose.yml --project-directory /tmp up -d redis mysql" {
		t.Errorf("calls = %v", f.calls)
	}
}

func TestComposeUpWrapsStderrInError(t *testing.T) {
	f := &fakeRunner{ok: false, stderr: []byte("Error response from daemon: pull access denied for x\n")}
	c := &Compose{Workdir: "/tmp", File: "docker-compose.yml", Runner: f}
	err := c.Up(context.Background())
	if err == nil {
		t.Fatal("Up: expected error, got nil")
	}
	if !contains(err.Error(), "pull access denied") {
		t.Errorf("error message did not include stderr: %v", err)
	}
}

func TestComposeBuildWrapsStderrInError(t *testing.T) {
	f := &fakeRunner{ok: false, stderr: []byte("failed to solve: not found\n")}
	c := &Compose{Workdir: "/tmp", File: "docker-compose.yml", Runner: f}
	err := c.Build(context.Background())
	if err == nil {
		t.Fatal("Build: expected error, got nil")
	}
	if !contains(err.Error(), "failed to solve") {
		t.Errorf("error message did not include stderr: %v", err)
	}
}

func TestComposeDown(t *testing.T) {
	f := &fakeRunner{ok: true}
	c := &Compose{Workdir: "/tmp", File: "docker-compose.yml", Runner: f}
	if err := c.Down(context.Background()); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if f.calls[0] != "docker compose -f /tmp/docker-compose.yml --project-directory /tmp down" {
		t.Errorf("calls = %v", f.calls)
	}
}

func TestComposeBuild(t *testing.T) {
	f := &fakeRunner{ok: true}
	c := &Compose{Workdir: "/tmp", File: "docker-compose.yml", Runner: f}
	if err := c.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if f.calls[0] != "docker compose -f /tmp/docker-compose.yml --project-directory /tmp build" {
		t.Errorf("calls = %v", f.calls)
	}
}

func TestComposePS(t *testing.T) {
	f := &fakeRunner{ok: true, stdout: []byte("name\timage\tstate\n")}
	c := &Compose{Workdir: "/tmp", File: "docker-compose.yml", Runner: f}
	out, err := c.PS(context.Background())
	if err != nil {
		t.Fatalf("PS: %v", err)
	}
	if string(out) != "name\timage\tstate\n" {
		t.Errorf("PS out = %q", out)
	}
}

func TestComposeConfig(t *testing.T) {
	f := &fakeRunner{ok: true, stdout: []byte("ok")}
	c := &Compose{Workdir: "/tmp", File: "docker-compose.yml", Runner: f}
	_, err := c.Config(context.Background())
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if f.calls[0] != "docker compose -f /tmp/docker-compose.yml --project-directory /tmp config" {
		t.Errorf("calls = %v", f.calls)
	}
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}

func TestRunRawPassesOSStdin(t *testing.T) {
	f := &fakeRunner{ok: true}
	c := &Compose{Workdir: "/tmp", File: "docker-compose.yml", Runner: f}
	if err := c.runRaw(context.Background(), "ps"); err != nil {
		t.Fatalf("runRaw: %v", err)
	}
	if f.stdin != os.Stdin {
		t.Errorf("runRaw did not pass os.Stdin to Runner: got %T, want *os.File", f.stdin)
	}
}

func TestRunStreamingPassesOSStdin(t *testing.T) {
	f := &fakeRunner{ok: true}
	c := &Compose{Workdir: "/tmp", File: "docker-compose.yml", Runner: f}
	if err := c.Up(context.Background()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if f.stdin != os.Stdin {
		t.Errorf("runStreaming did not pass os.Stdin to Runner: got %T, want *os.File", f.stdin)
	}
}

func TestRunCapturedPassesOSStdin(t *testing.T) {
	f := &fakeRunner{ok: true, stdout: []byte("ok")}
	c := &Compose{Workdir: "/tmp", File: "docker-compose.yml", Runner: f}
	if _, err := c.PS(context.Background()); err != nil {
		t.Fatalf("PS: %v", err)
	}
	if f.stdin != os.Stdin {
		t.Errorf("runCaptured did not pass os.Stdin to Runner: got %T, want *os.File", f.stdin)
	}
}

func TestExecRunnerForwardsStdinToChild(t *testing.T) {
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat not available on PATH; skipping stdin forwarding test")
	}
	input := "pier-stdin-forwarding-test\n"
	var out bytes.Buffer
	err := ExecRunner{}.Run(context.Background(), strings.NewReader(input), &out, nil, "cat")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.String() != input {
		t.Errorf("cat output = %q, want %q", out.String(), input)
	}
}

func TestExecRunnerDefaultsToOSStdin(t *testing.T) {
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat not available on PATH; skipping os.Stdin default test")
	}
	runner := ExecRunner{}
	var out bytes.Buffer
	if err := runner.Run(context.Background(), nil, &out, nil, "cat"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := out.String(); got != "" {
		t.Errorf("cat output = %q, want empty (nil stdin should not block)", got)
	}
}
