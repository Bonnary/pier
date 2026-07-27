package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pcnerd/pier/internal/docker"
)

type capturingRunner struct {
	calls []string
}

func (c *capturingRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	call := name
	for _, a := range args {
		call += " " + a
	}
	c.calls = append(c.calls, call)
	return []byte("name\timage\tstate\nlaravel.test\tmyapp\tUp\n"), nil, nil
}

func TestExecBuildsCommand(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\nservices=[]\n"
	if err := writeFile(filepath.Join(dir, "pier.toml"), []byte(toml)); err != nil {
		t.Fatal(err)
	}
	runner := &capturingRunner{}
	orig := dockerRunner
	dockerRunner = docker.Runner(runner)
	defer func() { dockerRunner = orig }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "exec", "--", "php", "artisan", "--version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls = %v", runner.calls)
	}
	last := runner.calls[len(runner.calls)-1]
	if !strings.Contains(last, "laravel.test") || !strings.HasSuffix(last, "php artisan --version") {
		t.Errorf("call = %q", last)
	}
}
