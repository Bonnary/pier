package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/pcnerd/pier/internal/docker"
)

func writeFile(path string, contents []byte) error {
	return os.WriteFile(path, contents, 0644)
}

type fakeRunnerCLI struct {
	calls []string
}

func (f *fakeRunnerCLI) Run(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
	call := name
	for _, a := range args {
		call += " " + a
	}
	f.calls = append(f.calls, call)
	stdout.Write([]byte("name\timage\tstate\n"))
	return nil
}

func TestDevCommand(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\nservices=[]\n"
	if err := writeFile(filepath.Join(dir, "pier.toml"), []byte(toml)); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunnerCLI{}
	origRunner := dockerRunner
	dockerRunner = docker.Runner(runner)
	defer func() { dockerRunner = origRunner }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "dev"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	if len(runner.calls) < 2 {
		t.Errorf("expected >=2 docker calls, got: %v", runner.calls)
	}
	if runner.calls[0] != "docker compose -f "+filepath.Join(dir, "docker-compose.yml")+" --project-directory "+dir+" build" {
		t.Errorf("first call = %q", runner.calls[0])
	}
	if len(runner.calls) < 2 || runner.calls[1] != "docker compose -f "+filepath.Join(dir, "docker-compose.yml")+" --project-directory "+dir+" up -d" {
		t.Errorf("second call = %v", runner.calls)
	}
}
