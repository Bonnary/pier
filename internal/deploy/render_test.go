package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bonnary/pier/internal/config"
)

func TestRenderProdFilesWritesMergedComposeAndEnv(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "x"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"mysql"}},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "/srv/x", Branch: "main", Services: []string{"postgres"}},
		},
	}
	if err := renderProdFiles(dir, cfg, "production"); err != nil {
		t.Fatalf("renderProdFiles: %v", err)
	}
	compose, err := os.ReadFile(filepath.Join(dir, "docker-compose.prod.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(compose), "postgres:") || strings.Contains(string(compose), "mysql:") {
		t.Errorf("compose must contain postgres and not mysql:\n%s", compose)
	}
	env, err := os.ReadFile(filepath.Join(dir, ".env.production"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), "DB_CONNECTION=pgsql") {
		t.Errorf("env file missing pgsql connection:\n%s", env)
	}
	// The trustedproxy config is baked into the local build context
	// (Dockerfile.prod COPYs . into the image), so render must ensure it
	// exists on projects that predate the fix (pier init refuses to
	// re-run over an existing pier.toml).
	proxy, err := os.ReadFile(filepath.Join(dir, "config/trustedproxy.php"))
	if err != nil {
		t.Fatalf("render must create config/trustedproxy.php (Laravel 13 behind Caddy renders http:// assets → white screen): %v", err)
	}
	if !strings.Contains(string(proxy), "env('TRUSTED_PROXIES', '*')") {
		t.Errorf("trustedproxy config must trust proxies by default:\n%s", proxy)
	}
}

func TestRenderProdFilesWritesCaddyfileReflectingDomain(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docker", "caddy"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docker", "caddy", "Caddyfile"), []byte(":80 {\n    encode gzip\n    reverse_proxy app:80\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "x"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "/srv/x", Branch: "main", Domain: "hello.monodachi.org"},
		},
	}
	if err := renderProdFiles(dir, cfg, "production"); err != nil {
		t.Fatalf("renderProdFiles: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "docker", "caddy", "Caddyfile"))
	if err != nil {
		t.Fatalf("render must write docker/caddy/Caddyfile (the bind-mounted file Caddy serves from): %v", err)
	}
	if !strings.Contains(string(got), "hello.monodachi.org {") {
		t.Errorf("Caddyfile must serve the env's domain:\n%s", got)
	}
	if strings.Contains(string(got), ":80 {") {
		t.Errorf("stale plain-HTTP :80 block must be replaced by the domain block:\n%s", got)
	}
}

func TestRenderProdFilesWritesHTTPOnlyCaddyfileWithoutDomain(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "x"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "/srv/x", Branch: "main"},
		},
	}
	if err := renderProdFiles(dir, cfg, "production"); err != nil {
		t.Fatalf("renderProdFiles: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "docker", "caddy", "Caddyfile"))
	if err != nil {
		t.Fatalf("render must write docker/caddy/Caddyfile: %v", err)
	}
	if !strings.Contains(string(got), ":80 {") {
		t.Errorf("no-domain Caddyfile must serve plain HTTP on :80:\n%s", got)
	}
}

func TestRenderProdFilesPreservesExistingTrustedProxiesConfig(t *testing.T) {
	dir := t.TempDir()
	existing := "<?php return ['proxies' => '10.0.0.1'];\n"
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config/trustedproxy.php"), []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "x"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
	}
	if err := renderProdFiles(dir, cfg, "production"); err != nil {
		t.Fatalf("renderProdFiles: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "config/trustedproxy.php"))
	if string(got) != existing {
		t.Errorf("render clobbered a user's existing config/trustedproxy.php:\n%s", got)
	}
}

func TestRenderProdFilesPreservesUserContent(t *testing.T) {
	dir := t.TempDir()
	existing := `services:
    app:
        image: x:latest
        environment:
            AWS_ACCESS_KEY_ID: ${AWS_ACCESS_KEY_ID}
    webserver:
        image: nginx:alpine
    custom:
        image: custom/sidecar:1
    redis:
        image: redis:7-alpine
`
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.prod.yml"), []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.production"), []byte("APP_KEY=real-secret\nDB_PASSWORD=supersecret\nAWS_ENDPOINT=http://s3:8333\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "x"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"redis"}},
	}
	if err := renderProdFiles(dir, cfg, "production"); err != nil {
		t.Fatalf("renderProdFiles: %v", err)
	}
	compose, _ := os.ReadFile(filepath.Join(dir, "docker-compose.prod.yml"))
	for _, want := range []string{"AWS_ACCESS_KEY_ID: ${AWS_ACCESS_KEY_ID}", "custom/sidecar:1"} {
		if !strings.Contains(string(compose), want) {
			t.Errorf("user content %q lost from compose:\n%s", want, compose)
		}
	}
	env, _ := os.ReadFile(filepath.Join(dir, ".env.production"))
	for _, want := range []string{"APP_KEY=real-secret", "DB_PASSWORD=supersecret", "AWS_ENDPOINT=http://s3:8333"} {
		if !strings.Contains(string(env), want) {
			t.Errorf("user env line %q lost:\n%s", want, env)
		}
	}
	if !strings.Contains(string(compose), "redis:") {
		t.Errorf("inherited redis service missing from compose:\n%s", compose)
	}
}

func TestRenderProdFilesDropsRemovedService(t *testing.T) {
	dir := t.TempDir()
	existing := `services:
    app:
        image: x:latest
    webserver:
        image: nginx:alpine
    redis:
        image: redis:7-alpine
`
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.prod.yml"), []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "x"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"redis"}},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "/srv/x", Branch: "main", Services: []string{}},
		},
	}
	if err := renderProdFiles(dir, cfg, "production"); err != nil {
		t.Fatalf("renderProdFiles: %v", err)
	}
	compose, _ := os.ReadFile(filepath.Join(dir, "docker-compose.prod.yml"))
	if strings.Contains(string(compose), "redis:") {
		t.Errorf("removed redis service still in compose:\n%s", compose)
	}
	if !strings.Contains(string(compose), "webserver:") {
		t.Errorf("webserver missing from compose:\n%s", compose)
	}
}
