package laravel

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Bonnary/pier/internal/config"
)

func TestGenerateProdFilesNoServices(t *testing.T) {
	s := New()
	files, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
	}, "production")
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
	}, "production")
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
	}, "production")
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
	}, "production")
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
	}, "production")
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

func TestGenerateProdFilesWebserverDefaultPorts(t *testing.T) {
	s := New()
	files, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "p", Branch: "b"},
		},
	}, "production")
	if err != nil {
		t.Fatalf("GenerateProdFiles: %v", err)
	}
	got := findFile(files, "docker-compose.prod.yml")
	if got == nil {
		t.Fatal("docker-compose.prod.yml missing")
	}
	var doc struct {
		Services map[string]struct {
			Ports []string `yaml:"ports"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(got.Contents, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	web, ok := doc.Services["webserver"]
	if !ok {
		t.Fatal("webserver missing")
	}
	found80, found443 := false, false
	for _, p := range web.Ports {
		if p == "80:80" {
			found80 = true
		}
		if p == "443:443" {
			found443 = true
		}
	}
	if !found80 {
		t.Errorf("webserver ports = %v, want it to include 80:80 (plain-HTTP default: laravel → container 80)", web.Ports)
	}
	if found443 {
		t.Errorf("webserver ports = %v, must not include 443:443 when tls is off (nginx serves HTTP on 80 only)", web.Ports)
	}
}

func TestGenerateProdFilesPortPartialOverride(t *testing.T) {
	s := New()
	files, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack: config.StackConfig{
			Type: "laravel", PHP: "8.3", Node: "22",
			Services: []string{"redis"},
		},
		Deploy: map[string]config.DeployConfig{
			"production": {
				Host: "h", User: "u", Path: "p", Branch: "b",
				Ports: map[string]int{"laravel": 8383},
			},
		},
	}, "production")
	if err != nil {
		t.Fatalf("GenerateProdFiles: %v", err)
	}
	got := findFile(files, "docker-compose.prod.yml")
	if got == nil {
		t.Fatal("docker-compose.prod.yml missing")
	}
	var doc struct {
		Services map[string]struct {
			Ports []string `yaml:"ports"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(got.Contents, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	web := doc.Services["webserver"]
	found8383, found80 := false, false
	for _, p := range web.Ports {
		if p == "8383:80" {
			found8383 = true
		}
		if p == "80:80" {
			found80 = true
		}
	}
	if !found8383 {
		t.Errorf("webserver ports = %v, want it to include 8383:80 (laravel override → container 80 when tls is off)", web.Ports)
	}
	if found80 {
		t.Errorf("webserver ports = %v, must not publish the webserver_http default 80:80 when tls is off", web.Ports)
	}
	redis := doc.Services["redis"]
	foundRedis := false
	for _, p := range redis.Ports {
		if p == "6379:6379" {
			foundRedis = true
		}
	}
	if !foundRedis {
		t.Errorf("redis ports = %v, want it to include 6379:6379 (prod default, not overridden)", redis.Ports)
	}
}

func TestGenerateProdFilesWebserverTLSPorts(t *testing.T) {
	s := New()
	files, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "p", Branch: "b", TLS: true},
		},
	}, "production")
	if err != nil {
		t.Fatalf("GenerateProdFiles: %v", err)
	}
	got := findFile(files, "docker-compose.prod.yml")
	if got == nil {
		t.Fatal("docker-compose.prod.yml missing")
	}
	var doc struct {
		Services map[string]struct {
			Ports []string `yaml:"ports"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(got.Contents, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	web, ok := doc.Services["webserver"]
	if !ok {
		t.Fatal("webserver missing")
	}
	wantPorts := map[string]bool{
		"443:443": false,
		"80:80":   false,
	}
	for _, p := range web.Ports {
		if _, ok := wantPorts[p]; ok {
			wantPorts[p] = true
		}
	}
	for p, found := range wantPorts {
		if !found {
			t.Errorf("webserver ports missing %q; got %v (tls on: laravel=443, webserver_http=80)", p, web.Ports)
		}
	}
}

func TestGenerateProdFilesWebserverHTTPOverrideWhenNoTLS(t *testing.T) {
	s := New()
	files, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "p", Branch: "b", Ports: map[string]int{"webserver_http": 8080}},
		},
	}, "production")
	if err != nil {
		t.Fatalf("GenerateProdFiles: %v", err)
	}
	got := findFile(files, "docker-compose.prod.yml")
	if got == nil {
		t.Fatal("docker-compose.prod.yml missing")
	}
	var doc struct {
		Services map[string]struct {
			Ports []string `yaml:"ports"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(got.Contents, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	web, ok := doc.Services["webserver"]
	if !ok {
		t.Fatal("webserver missing")
	}
	wantPorts := map[string]bool{
		"80:80":   false,
		"8080:80": false,
	}
	for _, p := range web.Ports {
		if _, ok := wantPorts[p]; ok {
			wantPorts[p] = true
		}
	}
	for p, found := range wantPorts {
		if !found {
			t.Errorf("webserver ports missing %q; got %v (tls off: laravel default 80:80 + explicit webserver_http 8080:80)", p, web.Ports)
		}
	}
}

func TestGenerateProdEnvExampleAPPURL(t *testing.T) {
	s := New()
	httpFiles, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Host: "h", User: "u", Path: "p", Branch: "b"}},
	}, "production")
	if err != nil {
		t.Fatalf("GenerateProdFiles (http): %v", err)
	}
	httpEnv := findFile(httpFiles, ".env.production.example")
	if httpEnv == nil {
		t.Fatal(".env.production.example missing (http)")
	}
	if !contains(string(httpEnv.Contents), "APP_URL=http://myapp.example.com") {
		t.Errorf("env example missing plain-HTTP APP_URL:\n%s", httpEnv.Contents)
	}

	httpsFiles, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Host: "h", User: "u", Path: "p", Branch: "b", TLS: true}},
	}, "production")
	if err != nil {
		t.Fatalf("GenerateProdFiles (https): %v", err)
	}
	httpsEnv := findFile(httpsFiles, ".env.production.example")
	if httpsEnv == nil {
		t.Fatal(".env.production.example missing (https)")
	}
	if !contains(string(httpsEnv.Contents), "APP_URL=https://myapp.example.com") {
		t.Errorf("env example missing HTTPS APP_URL:\n%s", httpsEnv.Contents)
	}
}
