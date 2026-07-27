package laravel

import (
	"testing"

	"github.com/pcnerd/pier/internal/config"
)

func TestGenerateProdFilesNoServices(t *testing.T) {
	s := New()
	files, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
	})
	if err != nil {
		t.Fatalf("GenerateProdFiles: %v", err)
	}
	if findFile(files, "docker-compose.prod.yml") == nil {
		t.Error("docker-compose.prod.yml missing")
	}
	if findFile(files, ".env.production.example") == nil {
		t.Error(".env.production.example missing")
	}
	if findFile(files, "docker/nginx/default.conf") == nil {
		t.Error("docker/nginx/default.conf missing")
	}
	compose := string(findFile(files, "docker-compose.prod.yml").Contents)
	if contains(compose, ":/var/www/html") {
		t.Errorf("prod compose should not contain bind mount /var/www/html:\n%s", compose)
	}
}

func TestGenerateProdFilesWithServices(t *testing.T) {
	s := New()
	files, err := s.GenerateProdFiles(config.Config{
		Stack: config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"redis"}},
	})
	if err != nil {
		t.Fatalf("GenerateProdFiles: %v", err)
	}
	compose := string(findFile(files, "docker-compose.prod.yml").Contents)
	if !contains(compose, "redis:") {
		t.Errorf("prod compose missing redis service:\n%s", compose)
	}
}

func TestGenerateProdFilesDevOnlyExcluded(t *testing.T) {
	s := New()
	files, err := s.GenerateProdFiles(config.Config{
		Stack: config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"redis", "mailpit"}},
	})
	if err != nil {
		t.Fatalf("GenerateProdFiles: %v", err)
	}
	compose := string(findFile(files, "docker-compose.prod.yml").Contents)
	if contains(compose, "mailpit:") {
		t.Errorf("prod compose must not include dev-only mailpit:\n%s", compose)
	}
}
