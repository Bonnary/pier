package docker

import (
	"context"
	"errors"
	"testing"
)

type fakeRunner struct {
	calls    []string
	ok       bool
	stdout   []byte
	stderr   []byte
	failWith error
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	call := name
	for _, a := range args {
		call += " " + a
	}
	f.calls = append(f.calls, call)
	if f.failWith != nil {
		return nil, nil, f.failWith
	}
	if !f.ok {
		return nil, nil, errors.New("fakeRunner: not ok")
	}
	return f.stdout, f.stderr, nil
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
