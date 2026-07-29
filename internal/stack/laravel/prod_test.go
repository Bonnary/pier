package laravel

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

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

func TestGenerateProdFilesQueueSchedulerReuseAppImage(t *testing.T) {
	s := New()
	files, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"queue", "scheduler"}},
	})
	if err != nil {
		t.Fatalf("GenerateProdFiles: %v", err)
	}
	got := findFile(files, "docker-compose.prod.yml")
	if got == nil {
		t.Fatal("docker-compose.prod.yml missing")
	}
	var doc struct {
		Services map[string]struct {
			Image string `yaml:"image"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(got.Contents, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, name := range []string{"queue", "scheduler"} {
		img, ok := doc.Services[name]
		if !ok {
			t.Errorf("service %q missing from prod compose:\n%s", name, got.Contents)
			continue
		}
		if img.Image != "myapp:latest" {
			t.Errorf("prod %s image = %q, want %q (queue/scheduler must reuse the built app image, not the unresolvable myapp:latest fallback)", name, img.Image, "myapp:latest")
		}
	}
	if strings.Contains(string(got.Contents), "${APP_IMAGE") {
		t.Errorf("prod compose still contains the unresolvable ${APP_IMAGE:-myapp:latest} fallback:\n%s", got.Contents)
	}
}

func TestGenerateProdFilesQueueSchedulerSetSupervisorCommand(t *testing.T) {
	s := New()
	files, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"queue", "scheduler"}},
	})
	if err != nil {
		t.Fatalf("GenerateProdFiles: %v", err)
	}
	got := findFile(files, "docker-compose.prod.yml")
	if got == nil {
		t.Fatal("docker-compose.prod.yml missing")
	}
	var doc struct {
		Services map[string]struct {
			Env map[string]string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(got.Contents, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, name := range []string{"queue", "scheduler"} {
		svc, ok := doc.Services[name]
		if !ok {
			t.Errorf("service %q missing from prod compose", name)
			continue
		}
		cmd, ok := svc.Env["SUPERVISOR_PHP_COMMAND"]
		if !ok {
			t.Errorf("prod %s env missing SUPERVISOR_PHP_COMMAND", name)
			continue
		}
		switch name {
		case "queue":
			if !strings.Contains(cmd, "queue:work") {
				t.Errorf("prod queue SUPERVISOR_PHP_COMMAND = %q, want it to invoke 'artisan queue:work'", cmd)
			}
		case "scheduler":
			if !strings.Contains(cmd, "schedule:work") {
				t.Errorf("prod scheduler SUPERVISOR_PHP_COMMAND = %q, want it to invoke 'artisan schedule:work'", cmd)
			}
		}
	}
}
