package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tui "github.com/pcnerd/pier/internal/tui"
)

func TestInitWritesPierToml(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "artisan"), []byte("#!/usr/bin/env php\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"require":{"laravel/framework":"^11.0"}}`), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "init", dir, "--php", "8.3", "--node", "22"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	for _, want := range []string{"pier.toml", "docker-compose.yml", "docker-compose.prod.yml", "docker/8.3/Dockerfile"} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Errorf("expected %s after init: %v", want, err)
		}
	}
}

func TestInitFailsOnExistingPierToml(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "artisan"), []byte("#!/usr/bin/env php\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"require":{"laravel/framework":"^11.0"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pier.toml"), []byte("[project]\nname=\"x\"\ndomain=\"x.example.com\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"init", dir, "--php", "8.3", "--node", "22"})
	root.SilenceUsage = true
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "pier.toml exists") {
		t.Errorf("err = %v, want pier.toml-exists error", err)
	}
}

func TestInitFailsOnNonLaravel(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"init", dir})
	root.SilenceUsage = true
	err := root.Execute()
	if err == nil {
		t.Fatal("Execute = nil error, want non-nil (not a Laravel project)")
	}
}

func TestInitTUIInvokedWhenTTYAndNoFlags(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "artisan"), []byte("#!/usr/bin/env php\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"require":{"laravel/framework":"^11.0"}}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Force ShouldRun() = true for the duration of this test.
	origTTY := tuiForTest
	tuiForTest = func() bool { return true }
	defer func() { tuiForTest = origTTY }()

	// Stub RunInit by swapping a package-level var — see step 3 for the seam.
	called := false
	origRun := runInitTUI
	runInitTUI = func(phpVersions, nodeVersions, services []string) (tui.InitResult, error) {
		called = true
		return tui.InitResult{
			PHP:      "8.3",
			Node:     "22",
			Services: []string{"redis"},
		}, nil
	}
	defer func() { runInitTUI = origRun }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"init", dir})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	if !called {
		t.Error("RunInit was not invoked when TTY and no flags were set")
	}
	got, _ := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if !bytes.Contains(got, []byte(`php = "8.3"`)) {
		t.Errorf("php = 8.3 not in pier.toml:\n%s", got)
	}
	if !bytes.Contains(got, []byte(`node = "22"`)) {
		t.Errorf("node = 22 not in pier.toml:\n%s", got)
	}
	if !bytes.Contains(got, []byte(`"redis"`)) {
		t.Errorf("redis not in pier.toml:\n%s", got)
	}
}
