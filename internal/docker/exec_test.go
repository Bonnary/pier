package docker

import (
	"context"
	"testing"
)

func TestExecService(t *testing.T) {
	f := &fakeRunner{ok: true}
	c := &Compose{Workdir: "/tmp", File: "docker-compose.yml", Runner: f}
	if err := c.Exec(context.Background(), ExecOpts{Service: "laravel.test", User: "www-data"}, "php", "artisan", "--version"); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	want := "docker compose -f /tmp/docker-compose.yml --project-directory /tmp exec -T -u www-data laravel.test php artisan --version"
	if f.calls[0] != want {
		t.Errorf("got: %s\nwant: %s", f.calls[0], want)
	}
}

func TestExecTTYAddsI(t *testing.T) {
	f := &fakeRunner{ok: true}
	c := &Compose{Workdir: "/tmp", File: "docker-compose.yml", Runner: f}
	if err := c.Exec(context.Background(), ExecOpts{Service: "laravel.test", User: "www-data", TTY: true}, "bash"); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	want := "docker compose -f /tmp/docker-compose.yml --project-directory /tmp exec -i -u www-data laravel.test bash"
	if f.calls[0] != want {
		t.Errorf("got: %s\nwant: %s", f.calls[0], want)
	}
}

func TestExecRequiresService(t *testing.T) {
	f := &fakeRunner{ok: true}
	c := &Compose{Workdir: "/tmp", File: "docker-compose.yml", Runner: f}
	err := c.Exec(context.Background(), ExecOpts{User: "www-data"}, "bash")
	if err == nil {
		t.Fatal("Exec = nil error, want non-nil (service required)")
	}
}
