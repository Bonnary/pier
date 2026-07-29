package laravel

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"

	"github.com/pcnerd/pier/internal/config"
	"github.com/pcnerd/pier/internal/stack"
)

var update = flag.Bool("update", false, "update golden files")

func TestGenerateDevComposeNoServices(t *testing.T) {
	s := New()
	files, err := s.GenerateDevCompose(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
	})
	if err != nil {
		t.Fatalf("GenerateDevCompose: %v", err)
	}
	got := findFile(files, "docker-compose.yml")
	if got == nil {
		t.Fatal("docker-compose.yml missing")
	}
	if *update {
		writeGolden(t, "testdata/golden/compose-no-services.yml", got.Contents)
	}
	want := readGolden(t, "testdata/golden/compose-no-services.yml")
	assertYAMLEqual(t, got.Contents, want)
}

func TestGenerateDevComposeWithServices(t *testing.T) {
	s := New()
	files, err := s.GenerateDevCompose(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"redis", "mailpit"}},
	})
	if err != nil {
		t.Fatalf("GenerateDevCompose: %v", err)
	}
	got := findFile(files, "docker-compose.yml")
	if got == nil {
		t.Fatal("docker-compose.yml missing")
	}
	if *update {
		writeGolden(t, "testdata/golden/compose-with-services.yml", got.Contents)
	}
	want := readGolden(t, "testdata/golden/compose-with-services.yml")
	assertYAMLEqual(t, got.Contents, want)
}

func TestGenerateDevComposeRejectsUnknownService(t *testing.T) {
	s := New()
	_, err := s.GenerateDevCompose(config.Config{
		Stack: config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"oracle"}},
	})
	if err == nil {
		t.Fatal("GenerateDevCompose = nil error, want non-nil")
	}
}

func TestGenerateDevComposeQueueSchedulerReuseAppImage(t *testing.T) {
	s := New()
	files, err := s.GenerateDevCompose(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"queue", "scheduler"}},
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
			Image string `yaml:"image"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(got.Contents, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, name := range []string{"queue", "scheduler"} {
		img, ok := doc.Services[name]
		if !ok {
			t.Errorf("service %q missing from dev compose:\n%s", name, got.Contents)
			continue
		}
		if img.Image != "myapp/test:latest" {
			t.Errorf("dev %s image = %q, want %q (queue/scheduler must reuse the built laravel.test image, not the unresolvable myapp:latest fallback)", name, img.Image, "myapp/test:latest")
		}
	}
	if strings.Contains(string(got.Contents), "myapp:latest") {
		t.Errorf("dev compose still contains the broken myapp:latest fallback:\n%s", got.Contents)
	}
}

func TestGenerateDevComposeQueueSchedulerBindMountApp(t *testing.T) {
	s := New()
	files, err := s.GenerateDevCompose(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"queue", "scheduler"}},
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
			Volumes []string          `yaml:"volumes"`
			Env     map[string]string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(got.Contents, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, name := range []string{"queue", "scheduler"} {
		svc, ok := doc.Services[name]
		if !ok {
			t.Errorf("service %q missing from dev compose", name)
			continue
		}
		hasBind := false
		for _, v := range svc.Volumes {
			if strings.Contains(v, ":/var/www/html") {
				hasBind = true
				break
			}
		}
		if !hasBind {
			t.Errorf("dev %s volumes = %v, want a bind mount of the project into /var/www/html so artisan exists (without it, supervisord's php program exits 1 with 'Could not open input file: /var/www/html/artisan')", name, svc.Volumes)
		}
		cmd, ok := svc.Env["SUPERVISOR_PHP_COMMAND"]
		if !ok {
			t.Errorf("dev %s env missing SUPERVISOR_PHP_COMMAND (supervisord would fall back to Dockerfile default 'artisan serve' and the healthcheck would never see the expected process)", name)
			continue
		}
		switch name {
		case "queue":
			if !strings.Contains(cmd, "queue:work") {
				t.Errorf("dev queue SUPERVISOR_PHP_COMMAND = %q, want it to invoke 'artisan queue:work'", cmd)
			}
		case "scheduler":
			if !strings.Contains(cmd, "schedule:work") {
				t.Errorf("dev scheduler SUPERVISOR_PHP_COMMAND = %q, want it to invoke 'artisan schedule:work'", cmd)
			}
		}
	}
}

func TestGenerateDevComposeCopiesRuntime(t *testing.T) {
	s := New()
	files, err := s.GenerateDevCompose(config.Config{
		Stack: config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
	})
	if err != nil {
		t.Fatalf("GenerateDevCompose: %v", err)
	}
	for _, name := range []string{"docker/8.3/Dockerfile", "docker/8.3/php.ini", "docker/8.3/supervisord.conf", "docker/8.3/start-container"} {
		if findFile(files, name) == nil {
			t.Errorf("expected file %q in result", name)
		}
	}
}

func TestGenerateDevComposeLaravelTestHasPorts(t *testing.T) {
	s := New()
	files, err := s.GenerateDevCompose(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
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
			Ports []string `yaml:"ports"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(got.Contents, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	lt, ok := doc.Services["laravel.test"]
	if !ok {
		t.Fatal("laravel.test missing")
	}
	wantPorts := map[string]bool{
		"127.0.0.1:8000:8000": false,
		"127.0.0.1:5173:5173": false,
	}
	for _, p := range lt.Ports {
		if _, ok := wantPorts[p]; ok {
			wantPorts[p] = true
		}
	}
	for p, found := range wantPorts {
		if !found {
			t.Errorf("laravel.test ports missing %q; got %v", p, lt.Ports)
		}
	}
}

func TestGenerateDevComposeSidecarPortsBindLoopback(t *testing.T) {
	s := New()
	files, err := s.GenerateDevCompose(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"redis", "mailpit"}},
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
			Ports []string `yaml:"ports"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(got.Contents, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	redis := doc.Services["redis"]
	wantRedis := map[string]bool{"127.0.0.1:6379:6379": false}
	for _, p := range redis.Ports {
		if _, ok := wantRedis[p]; ok {
			wantRedis[p] = true
		}
	}
	for p, found := range wantRedis {
		if !found {
			t.Errorf("redis ports missing %q; got %v", p, redis.Ports)
		}
	}
	mailpit := doc.Services["mailpit"]
	wantMailpit := map[string]bool{
		"127.0.0.1:1025:1025": false,
		"127.0.0.1:8025:8025": false,
	}
	for _, p := range mailpit.Ports {
		if _, ok := wantMailpit[p]; ok {
			wantMailpit[p] = true
		}
	}
	for p, found := range wantMailpit {
		if !found {
			t.Errorf("mailpit ports missing %q; got %v", p, mailpit.Ports)
		}
	}
}

func TestRuntimeDirHasStartContainer(t *testing.T) {
	for _, v := range SupportedPHPRuntimes() {
		dir, err := Runtime(v)
		if err != nil {
			t.Fatalf("Runtime(%s): %v", v, err)
		}
		path := filepath.Join(dir, "start-container")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("runtime %s missing %s: %v", v, path, err)
		}
	}
}

func findFile(files stack.Files, name string) *stack.File {
	for i, f := range files {
		if f.Path == name {
			return &files[i]
		}
	}
	return nil
}

func writeGolden(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, contents, 0644); err != nil {
		t.Fatalf("write golden: %v", err)
	}
}

func readGolden(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v", path, err)
	}
	return b
}

func assertYAMLEqual(t *testing.T, got, want []byte) {
	t.Helper()
	var g, w interface{}
	if err := yaml.Unmarshal(got, &g); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	if err := yaml.Unmarshal(want, &w); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	if diff := cmp.Diff(g, w); diff != "" {
		t.Errorf("compose mismatch (-got +want):\n%s", diff)
	}
}
