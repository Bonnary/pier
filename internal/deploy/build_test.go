package deploy

import (
	"context"
	"testing"
)

type fakeSSHClient struct {
	cmds []string
}

func (f *fakeSSHClient) Run(ctx context.Context, cmd string) ([]byte, []byte, error) {
	f.cmds = append(f.cmds, cmd)
	return nil, nil, nil
}

func (f *fakeSSHClient) RunStream(ctx context.Context, cmd string, onLine func(string)) error {
	f.cmds = append(f.cmds, cmd)
	for _, l := range []string{"Step 1/2", "Step 2/2", "Successfully tagged"} {
		onLine(l)
	}
	return nil
}

func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	if len(s) < len(sub) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestTagRetagsLatestToSHAAndCurrent(t *testing.T) {
	f := &fakeSSHClient{}
	if err := Tag(context.Background(), f, "myapp", "abc1234"); err != nil {
		t.Fatalf("Tag: %v", err)
	}
	if len(f.cmds) != 1 {
		t.Fatalf("Tag ran %d commands, want 1", len(f.cmds))
	}
	want := "docker tag 'myapp':latest 'myapp':'abc1234' && docker tag 'myapp':latest 'myapp':current"
	if f.cmds[0] != want {
		t.Errorf("Tag command = %q, want %q", f.cmds[0], want)
	}
}

func TestBuildStreamsOutput(t *testing.T) {
	f := &fakeSSHClient{}
	var lines []string
	if err := Build(context.Background(), f, "/srv/app", "myapp", "abc123", func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(lines) < 3 {
		t.Errorf("lines = %v, want >= 3", lines)
	}
	if !contains(f.cmds[0], "docker compose --env-file .env.production -f docker-compose.prod.yml build --pull") {
		t.Errorf("build command = %q, want it to pass --env-file .env.production so ${...} interpolation does not warn", f.cmds[0])
	}
}
