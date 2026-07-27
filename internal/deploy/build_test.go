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

func TestBuildStreamsOutput(t *testing.T) {
	f := &fakeSSHClient{}
	var lines []string
	if err := Build(context.Background(), f, "/srv/app", "myapp", "abc123", func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(lines) < 3 {
		t.Errorf("lines = %v, want >= 3", lines)
	}
	if !contains(f.cmds[0], "docker compose -f docker-compose.prod.yml build --pull") {
		t.Errorf("build command = %q", f.cmds[0])
	}
}
