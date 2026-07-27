package laravel

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"

	"github.com/pcnerd/pier/internal/config"
	"github.com/pcnerd/pier/internal/stack"
)

var update = flag.Bool("update", false, "update golden files")

func TestGenerateDevComposeNoServices(t *testing.T) {
	s := New()
	files, err := s.GenerateDevCompose(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
	})
	if err != nil {
		t.Fatalf("GenerateDevCompose: %v", err)
	}
	got := findFile(files, "docker-compose.yml")
	if got == nil {
		t.Fatal("docker-compose.yml missing")
	}
	if *update {
		writeGolden(t, "testdata/golden/compose-no-services.yml", got.Contents)
	}
	want := readGolden(t, "testdata/golden/compose-no-services.yml")
	assertYAMLEqual(t, got.Contents, want)
}

func TestGenerateDevComposeWithServices(t *testing.T) {
	s := New()
	files, err := s.GenerateDevCompose(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"redis", "mailpit"}},
	})
	if err != nil {
		t.Fatalf("GenerateDevCompose: %v", err)
	}
	got := findFile(files, "docker-compose.yml")
	if got == nil {
		t.Fatal("docker-compose.yml missing")
	}
	if *update {
		writeGolden(t, "testdata/golden/compose-with-services.yml", got.Contents)
	}
	want := readGolden(t, "testdata/golden/compose-with-services.yml")
	assertYAMLEqual(t, got.Contents, want)
}

func TestGenerateDevComposeRejectsUnknownService(t *testing.T) {
	s := New()
	_, err := s.GenerateDevCompose(config.Config{
		Stack: config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"oracle"}},
	})
	if err == nil {
		t.Fatal("GenerateDevCompose = nil error, want non-nil")
	}
}

func TestGenerateDevComposeCopiesRuntime(t *testing.T) {
	s := New()
	files, err := s.GenerateDevCompose(config.Config{
		Stack: config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
	})
	if err != nil {
		t.Fatalf("GenerateDevCompose: %v", err)
	}
	for _, name := range []string{"docker/8.3/Dockerfile", "docker/8.3/php.ini", "docker/8.3/supervisord.conf"} {
		if findFile(files, name) == nil {
			t.Errorf("expected file %q in result", name)
		}
	}
}

func findFile(files stack.Files, name string) *stack.File {
	for i, f := range files {
		if f.Path == name {
			return &files[i]
		}
	}
	return nil
}

func writeGolden(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, contents, 0644); err != nil {
		t.Fatalf("write golden: %v", err)
	}
}

func readGolden(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v", path, err)
	}
	return b
}

func assertYAMLEqual(t *testing.T, got, want []byte) {
	t.Helper()
	var g, w interface{}
	if err := yaml.Unmarshal(got, &g); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	if err := yaml.Unmarshal(want, &w); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	if diff := cmp.Diff(g, w); diff != "" {
		t.Errorf("compose mismatch (-got +want):\n%s", diff)
	}
}
