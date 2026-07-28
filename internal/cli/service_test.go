package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/pcnerd/pier/internal/docker"
)

func TestServiceAdd(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\nservices=[]\n"
	if err := os.WriteFile(filepath.Join(dir, "pier.toml"), []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}
	runner := &capturingRunner{}
	orig := dockerRunner
	dockerRunner = docker.Runner(runner)
	defer func() { dockerRunner = orig }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "service", "add", "redis", "--no-up"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(got), "redis") {
		t.Errorf("redis not in pier.toml:\n%s", got)
	}
	if !contains(string(got), "docker-compose.yml") {
	}
}

func TestServiceAddIdempotent(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\nservices=[\"redis\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "pier.toml"), []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}
	runner := &capturingRunner{}
	orig := dockerRunner
	dockerRunner = docker.Runner(runner)
	defer func() { dockerRunner = orig }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "service", "add", "redis", "--no-up"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "pier.toml"))
	count := bytes.Count(got, []byte("\"redis\""))
	if count != 1 {
		t.Errorf("redis count = %d, want 1 (idempotent):\n%s", count, got)
	}
}

func TestServiceAddTUIPickerInvoked(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\nservices=[]\n"
	if err := os.WriteFile(filepath.Join(dir, "pier.toml"), []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}
	runner := &capturingRunner{}
	orig := dockerRunner
	dockerRunner = docker.Runner(runner)
	defer func() { dockerRunner = orig }()

	// Force TTY + stub picker.
	origTTY := tuiForTest
	tuiForTest = func() bool { return true }
	defer func() { tuiForTest = origTTY }()
	called := false
	origPick := pickAddTUI
	pickAddTUI = func(available, installed []string) ([]string, error) {
		called = true
		return []string{"redis"}, nil
	}
	defer func() { pickAddTUI = origPick }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "service", "add", "--no-up"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	if !called {
		t.Error("PickServicesToAdd was not invoked when TTY and no args")
	}
	got, _ := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if !bytes.Contains(got, []byte(`"redis"`)) {
		t.Errorf("redis not in pier.toml:\n%s", got)
	}
}

func TestServiceRemoveTUIPickerInvoked(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\nservices=[\"redis\",\"mailpit\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "pier.toml"), []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}
	runner := &capturingRunner{}
	orig := dockerRunner
	dockerRunner = docker.Runner(runner)
	defer func() { dockerRunner = orig }()

	origTTY := tuiForTest
	tuiForTest = func() bool { return true }
	defer func() { tuiForTest = origTTY }()
	called := false
	origPick := pickRemoveTUI
	pickRemoveTUI = func(installed []string) ([]string, error) {
		called = true
		return []string{"redis"}, nil
	}
	defer func() { pickRemoveTUI = origPick }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "service", "remove"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	if !called {
		t.Error("PickServicesToRemove was not invoked when TTY and no args")
	}
	got, _ := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if bytes.Contains(got, []byte(`"redis"`)) {
		t.Errorf("redis still in pier.toml after remove:\n%s", got)
	}
	if !bytes.Contains(got, []byte(`"mailpit"`)) {
		t.Errorf("mailpit missing from pier.toml after partial remove:\n%s", got)
	}
}
