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
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
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
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
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
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
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
