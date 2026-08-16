package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bonnary/pier/internal/config"
)

func TestTomlEncodeRendersCommentedHookExamples(t *testing.T) {
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "/srv/x", Branch: "main"},
		},
	}
	b, err := tomlEncode(cfg)
	if err != nil {
		t.Fatalf("tomlEncode: %v", err)
	}
	got := string(b)
	for _, want := range []string{
		`queue_workers = 1`,
		`# before_deploy = ["php artisan down"]`,
		`# after_deploy = ["php artisan migrate --force"]`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("tomlEncode output missing %q; got:\n%s", want, got)
		}
	}
}

func TestTomlEncodeRendersRealHookValues(t *testing.T) {
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {
				Host: "h", User: "u", Path: "/srv/x", Branch: "main",
				BeforeDeploy: []string{"php artisan down"},
				AfterDeploy:  []string{"php artisan migrate --force", "php artisan cache:clear"},
			},
		},
	}
	b, err := tomlEncode(cfg)
	if err != nil {
		t.Fatalf("tomlEncode: %v", err)
	}
	got := string(b)
	if !strings.Contains(got, `before_deploy = ["php artisan down"]`) {
		t.Errorf("tomlEncode output missing real before_deploy; got:\n%s", got)
	}
	if !strings.Contains(got, `after_deploy = ["php artisan migrate --force", "php artisan cache:clear"]`) {
		t.Errorf("tomlEncode output missing real after_deploy; got:\n%s", got)
	}
	if strings.Contains(got, "# before_deploy") || strings.Contains(got, "# after_deploy") {
		t.Errorf("tomlEncode output has commented template next to real values; got:\n%s", got)
	}
}

func TestTomlEncodeRoundTripsDeployQueueWorkers(t *testing.T) {
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "/srv/x", Branch: "main", QueueWorkers: 5},
			"staging":    {Host: "s", User: "u", Path: "/srv/x", Branch: "main"},
		},
	}
	b, err := tomlEncode(cfg)
	if err != nil {
		t.Fatalf("tomlEncode: %v", err)
	}
	path := filepath.Join(t.TempDir(), "pier.toml")
	if err := os.WriteFile(path, b, 0644); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load: %v\nencoded:\n%s", err, b)
	}
	if n := got.QueueWorkersForEnv("production"); n != 5 {
		t.Errorf("QueueWorkersForEnv(production) = %d, want 5 (explicit override must survive the rewrite)\nencoded:\n%s", n, b)
	}
	if n := got.QueueWorkersForEnv("staging"); n != config.DefaultQueueWorkers {
		t.Errorf("QueueWorkersForEnv(staging) = %d, want %d (absent override must stay inherit)\nencoded:\n%s", n, config.DefaultQueueWorkers, b)
	}
	for _, sec := range strings.Split(string(b), "\n[deploy.") {
		if strings.HasPrefix(sec, "staging]") && strings.Contains(sec, "queue_workers") {
			t.Errorf("staging section must not emit queue_workers (0 = inherit); got:\n%s", b)
		}
	}
}

func TestTomlEncodeRendersDeployDomain(t *testing.T) {
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {
				Host: "h", User: "u", Path: "/srv/x", Branch: "main",
				Domain: "prod.example.com", ExtraDomains: []string{"www.prod.example.com"},
			},
			"staging": {Host: "s", User: "u", Path: "/srv/x", Branch: "main"},
		},
	}
	b, err := tomlEncode(cfg)
	if err != nil {
		t.Fatalf("tomlEncode: %v", err)
	}
	got := string(b)
	for _, want := range []string{
		`domain = "prod.example.com"`,
		`extra_domains = ["www.prod.example.com"]`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("tomlEncode output missing %q; got:\n%s", want, got)
		}
	}
	for _, sec := range strings.Split(got, "\n[deploy.") {
		if strings.HasPrefix(sec, "staging]") && (strings.Contains(sec, "domain") || strings.Contains(sec, "extra_domains")) {
			t.Errorf("staging section must not emit domain keys (empty = inherit); got:\n%s", sec)
		}
	}
	path := filepath.Join(t.TempDir(), "pier.toml")
	if err := os.WriteFile(path, b, 0644); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load: %v\nencoded:\n%s", err, b)
	}
	if got := loaded.DomainForEnv("production"); got != "prod.example.com" {
		t.Errorf("DomainForEnv(production) = %q, want prod.example.com after round trip", got)
	}
	if got := loaded.DomainForEnv("staging"); got != "x.example.com" {
		t.Errorf("DomainForEnv(staging) = %q, want x.example.com (inherit) after round trip", got)
	}
}
