package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bonnary/pier/internal/config"
	tui "github.com/Bonnary/pier/internal/tui"
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
	for _, want := range []string{"pier.toml", "docker-compose.yml", "docker-compose.prod.yml", "docker/8.3/Dockerfile", ".env", ".env.production"} {
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

func TestInitEmitsDevBindHint(t *testing.T) {
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
	got, err := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if err != nil {
		t.Fatalf("read pier.toml: %v", err)
	}
	contents := string(got)
	wantSubstrings := []string{
		"[dev]",
		"# bind",
		"0.0.0.0",
		"127.0.0.1",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(contents, want) {
			t.Errorf("init pier.toml missing %q; got:\n%s", want, contents)
		}
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
	runInitTUI = func(phpVersions, nodeVersions, services, builders []string) (tui.InitResult, error) {
		called = true
		_ = builders
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

const inertiaViteConfig = `import { defineConfig } from 'vite';
import laravel from 'laravel-vite-plugin';

export default defineConfig({
    plugins: [
        laravel({
            input: ['resources/css/app.css', 'resources/js/app.js'],
            refresh: true,
        }),
    ],
});
`

func TestInit_PatchesViteConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "artisan"), []byte("#!/usr/bin/env php\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"require":{"laravel/framework":"^11.0"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vite.config.ts"), []byte(inertiaViteConfig), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "init", dir, "--php", "8.3", "--node", "22"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "patched vite.config.ts: set server.host=true") {
		t.Errorf("stdout missing patch message; got:\n%s", buf.String())
	}
	ts, err := os.ReadFile(filepath.Join(dir, "vite.config.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ts), "server: { host: true },") {
		t.Errorf("vite.config.ts missing server: { host: true },:\n%s", ts)
	}
}

func TestInit_NoPatchWhenAlreadyConfigured(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "artisan"), []byte("#!/usr/bin/env php\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"require":{"laravel/framework":"^11.0"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	prePatched := `import { defineConfig } from 'vite';
export default defineConfig({
    server: { host: true },
});
`
	if err := os.WriteFile(filepath.Join(dir, "vite.config.ts"), []byte(prePatched), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "init", dir, "--php", "8.3", "--node", "22"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	if strings.Contains(buf.String(), "patched vite.config.ts") {
		t.Errorf("stdout should not mention patch when already configured; got:\n%s", buf.String())
	}
}

func TestInit_NoPatchWhenNoViteConfig(t *testing.T) {
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
	if strings.Contains(buf.String(), "patched vite.config.ts") {
		t.Errorf("stdout should not mention patch when no vite config exists; got:\n%s", buf.String())
	}
}

func TestInitScaffoldsDeployProductionServices(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "artisan"), []byte("#!/usr/bin/env php\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"require":{"laravel/framework":"^11.0"}}`), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "init", dir, "--php", "8.3", "--node", "22", "--services", "redis,mailpit"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if err != nil {
		t.Fatal(err)
	}
	contents := string(got)
	if !strings.Contains(contents, "[deploy.production]") {
		t.Errorf("init pier.toml missing [deploy.production]:\n%s", contents)
	}
	for _, want := range []string{"\"redis\"", "\"mailpit\""} {
		if !strings.Contains(contents, want) {
			t.Errorf("init pier.toml missing %s in services lists:\n%s", want, contents)
		}
	}
	if _, err := config.Load(filepath.Join(dir, "pier.toml")); err != nil {
		t.Errorf("init pier.toml must pass validation: %v", err)
	}
}

func TestInitWithoutServicesNoDeployScaffold(t *testing.T) {
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
	got, _ := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if strings.Contains(string(got), "[deploy.") {
		t.Errorf("init without services must not scaffold a deploy table:\n%s", got)
	}
}
