package laravel

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Bonnary/pier/internal/config"
)

func TestGenerateDevComposeRendersDevServices(t *testing.T) {
	s := New()
	files, err := s.GenerateDevCompose(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Dev: config.DevConfig{
			Services: map[string]config.DevService{
				"log-viewer": {
					Image: "ghcr.io/example/log-viewer:1.0",
					Ports: []string{"8081:8080"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("GenerateDevCompose: %v", err)
	}
	got := findFile(files, "docker-compose.yml")
	if got == nil {
		t.Fatal("docker-compose.yml missing")
	}

	var doc struct {
		Services map[string]struct {
			Image    string            `yaml:"image"`
			Ports    []string          `yaml:"ports"`
			Networks []string          `yaml:"networks"`
			Env      map[string]string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(got.Contents, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	lv, ok := doc.Services["log-viewer"]
	if !ok {
		t.Fatalf("log-viewer service not in dev compose:\n%s", got.Contents)
	}
	if lv.Image != "ghcr.io/example/log-viewer:1.0" {
		t.Errorf("log-viewer image = %q, want ghcr.io/example/log-viewer:1.0", lv.Image)
	}
	if len(lv.Ports) != 1 || lv.Ports[0] != "8081:8080" {
		t.Errorf("log-viewer ports = %v, want [8081:8080]", lv.Ports)
	}
}

func TestGenerateDevComposeRendersDevServiceEnvAndDeps(t *testing.T) {
	s := New()
	files, err := s.GenerateDevCompose(config.Config{
		Stack: config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"redis"}},
		Dev: config.DevConfig{
			Services: map[string]config.DevService{
				"reverb": {
					Image:     "example/reverb:1",
					Ports:     []string{"8080:8080"},
					Env:       map[string]string{"REVERB_APP_ID": "1"},
					DependsOn: []string{"redis"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("GenerateDevCompose: %v", err)
	}
	got := findFile(files, "docker-compose.yml")
	if got == nil {
		t.Fatal("docker-compose.yml missing")
	}
	text := string(got.Contents)
	for _, want := range []string{"reverb:", "example/reverb:1", "8080:8080", "REVERB_APP_ID", "redis"} {
		if !strings.Contains(text, want) {
			t.Errorf("dev compose missing %q:\n%s", want, text)
		}
	}
}

func TestGenerateDevComposeRejectsDevServiceWithoutImage(t *testing.T) {
	s := New()
	_, err := s.GenerateDevCompose(config.Config{
		Stack: config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Dev: config.DevConfig{
			Services: map[string]config.DevService{
				"log-viewer": {Ports: []string{"8081:8080"}},
			},
		},
	})
	if err == nil {
		t.Fatal("GenerateDevCompose = nil error, want non-nil (image required)")
	}
}

func TestGenerateProdFilesExcludesDevServices(t *testing.T) {
	s := New()
	files, err := s.GenerateProdFiles(config.Config{
		Stack: config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Dev: config.DevConfig{
			Services: map[string]config.DevService{
				"log-viewer": {Image: "x/y:1"},
			},
		},
	}, "production")
	if err != nil {
		t.Fatalf("GenerateProdFiles: %v", err)
	}
	compose := string(findFile(files, "docker-compose.prod.yml").Contents)
	if strings.Contains(compose, "log-viewer:") {
		t.Errorf("prod compose must not include dev-only log-viewer:\n%s", compose)
	}
}
