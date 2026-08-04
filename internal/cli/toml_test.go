package cli

import (
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
