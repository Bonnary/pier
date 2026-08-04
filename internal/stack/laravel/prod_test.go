package laravel

import (
	"strconv"
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
	if findFile(files, ".env.production") == nil {
		t.Error(".env.production missing")
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

func TestGenerateProdFilesAppBuildArgs(t *testing.T) {
	s := New()
	files, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
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
			Build *struct {
				Args map[string]string `yaml:"args"`
			} `yaml:"build"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(got.Contents, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	app, ok := doc.Services["app"]
	if !ok {
		t.Fatal("app service missing")
	}
	if app.Build == nil {
		t.Fatal("app build block missing")
	}
	for _, key := range []string{"WWWUSER", "WWWGROUP"} {
		v, ok := app.Build.Args[key]
		if !ok {
			t.Errorf("app build args missing %q; got %v (the runtime Dockerfile's `groupadd -g $WWWGROUP` fails with 'invalid group ID' when the arg is absent)", key, app.Build.Args)
			continue
		}
		if _, err := strconv.Atoi(v); err != nil {
			t.Errorf("app build arg %s = %q, want a numeric UID/GID (groupadd rejects non-numeric)", key, v)
		}
	}
}

func TestGenerateProdFilesAppBuildsFromProdDockerfile(t *testing.T) {
	s := New()
	files, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
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
			Build *struct {
				Context    string `yaml:"context"`
				Dockerfile string `yaml:"dockerfile"`
			} `yaml:"build"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(got.Contents, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	app, ok := doc.Services["app"]
	if !ok {
		t.Fatal("app service missing")
	}
	if app.Build == nil {
		t.Fatal("app build block missing")
	}
	if app.Build.Context != "." {
		t.Errorf("app build context = %q, want %q (the app code must be in the build context so Dockerfile.prod can COPY it; a ./docker/<php> context contains only the runtime and the image has no application code)", app.Build.Context, ".")
	}
	if app.Build.Dockerfile != "docker/8.3/Dockerfile.prod" {
		t.Errorf("app build dockerfile = %q, want %q (the runtime Dockerfile never COPYs the app — dev bind-mounts ./:/var/www/html, prod must bake it in)", app.Build.Dockerfile, "docker/8.3/Dockerfile.prod")
	}
}

func TestGenerateProdFilesRendersProdDockerfile(t *testing.T) {
	s := New()
	files, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
	}, "production")
	if err != nil {
		t.Fatalf("GenerateProdFiles: %v", err)
	}
	df := findFile(files, "docker/8.3/Dockerfile.prod")
	if df == nil {
		t.Fatal("docker/8.3/Dockerfile.prod missing (the prod image must bake the application; without it containers fail with 'Could not open input file: /var/www/html/artisan')")
	}
	body := string(df.Contents)
	checks := []struct {
		want string
		msg  string
	}{
		{"FROM ubuntu:24.04 AS runtime", "prod Dockerfile must name the runtime stage so the app stage can derive from it"},
		{"FROM runtime AS app", "prod Dockerfile must bake the app into a final stage derived from the runtime"},
		{"COPY . /var/www/html", "prod Dockerfile must COPY the project into /var/www/html (build context is the project root)"},
		{"composer install --no-dev", "prod Dockerfile must install prod composer dependencies (vendor/ is excluded from the deploy sync)"},
		{"npm run build", "prod Dockerfile must build frontend assets (node_modules/ is excluded from the deploy sync)"},
		{"chown", "prod Dockerfile must hand storage and bootstrap/cache to the sail user (COPY preserves root ownership)"},
		{"COPY docker/8.3/start-container /usr/local/bin/start-container", "runtime COPYs must be rewritten for the project-root build context (a plain `COPY start-container` now resolves to /start-container and the build fails with 'not found')"},
		{"COPY docker/8.3/supervisord.conf /etc/supervisor/conf.d/supervisord.conf", "runtime COPYs must be rewritten for the project-root build context"},
		{"COPY docker/8.3/php.ini /etc/php/8.3/cli/conf.d/99-sail.ini", "runtime COPYs must be rewritten for the project-root build context"},
	}
	for _, c := range checks {
		if !strings.Contains(body, c.want) {
			t.Errorf("Dockerfile.prod missing %q: %s\n%s", c.want, c.msg, body)
		}
	}
	if !strings.Contains(body, "COPY docker/8.3/start-container") {
		t.Errorf("Dockerfile.prod must keep the runtime base (start-container, supervisord) — got:\n%s", body)
	}
}

func TestGenerateProdFilesNginxProxiesToAppHTTPServer(t *testing.T) {
	s := New()
	files, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
	}, "production")
	if err != nil {
		t.Fatalf("GenerateProdFiles: %v", err)
	}
	conf := findFile(files, "docker/nginx/default.conf")
	if conf == nil {
		t.Fatal("docker/nginx/default.conf missing")
	}
	body := string(conf.Contents)
	if strings.Contains(body, "fastcgi_pass") {
		t.Errorf("nginx conf must not use fastcgi_pass: the app container runs `artisan serve` (PHP built-in server on port 80), not php-fpm — there is no php-fpm binary in the runtime image (apt installs php8.x-cli only), so every request 502s:\n%s", body)
	}
	if strings.Contains(body, "try_files") {
		t.Errorf("nginx conf must not rewrite to /index.php via try_files: the built-in server executes an existing public file directly (bypassing the Laravel router) and returns 500 'headers already sent' for /index.php — every non-file request like /up 500s:\n%s", body)
	}
	if !strings.Contains(body, "proxy_pass http://app:80") {
		t.Errorf("nginx conf must proxy requests verbatim to the app's HTTP listener (proxy_pass http://app:80), matching the runtime's `artisan serve --host=0.0.0.0 --port=80`:\n%s", body)
	}
}

func TestGenerateProdFilesDeclaresNamedVolumes(t *testing.T) {
	s := New()
	files, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"mysql", "postgres", "redis", "meilisearch", "s3"}},
	}, "production")
	if err != nil {
		t.Fatalf("GenerateProdFiles: %v", err)
	}
	got := findFile(files, "docker-compose.prod.yml")
	if got == nil {
		t.Fatal("docker-compose.prod.yml missing")
	}
	var doc struct {
		Volumes map[string]struct{} `yaml:"volumes"`
	}
	if err := yaml.Unmarshal(got.Contents, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]string{
		"mysql":       "mysql_data",
		"postgres":    "postgres_data",
		"redis":       "redis_data",
		"meilisearch": "meili_data",
		"s3":          "s3_data",
	}
	for svc, vol := range want {
		if _, ok := doc.Volumes[vol]; !ok {
			t.Errorf("prod compose service %q mounts named volume %q but top-level volumes: does not declare it; docker compose rejects this with 'invalid compose project'. Got volumes: %v",
				svc, vol, doc.Volumes)
		}
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

func TestGenerateProdFilesInterpolatesSecretsFromEnvFile(t *testing.T) {
	s := New()
	files, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack: config.StackConfig{
			Type: "laravel", PHP: "8.3", Node: "22",
			Services: []string{"postgres", "mysql", "redis"},
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
			Env map[string]string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(got.Contents, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Secrets must interpolate from the deploy host's .env.production
	// (compose is run with --env-file), never be hardcoded into the
	// committed compose file, and never be absent: a blank DB_PASSWORD
	// makes the app 500 with fe_sendauth, and a missing APP_KEY breaks
	// session encryption.
	for _, key := range []string{"DB_PASSWORD", "APP_KEY"} {
		v, ok := doc.Services["app"].Env[key]
		if !ok {
			t.Errorf("prod app env missing %q; got %v", key, doc.Services["app"].Env)
			continue
		}
		if !strings.Contains(v, "${") {
			t.Errorf("prod app env %s = %q, want a ${...} interpolation so the value comes from .env.production at runtime", key, v)
		}
	}
	for svc, key := range map[string]string{"postgres": "POSTGRES_PASSWORD", "mysql": "MYSQL_ROOT_PASSWORD"} {
		v, ok := doc.Services[svc].Env[key]
		if !ok {
			t.Errorf("prod %s env missing %q", svc, key)
			continue
		}
		if !strings.Contains(v, "${DB_PASSWORD}") {
			t.Errorf("prod %s env %s = %q, want ${DB_PASSWORD} so the DB server and the app share one password from .env.production (a hardcoded value silently mismatches when the user changes DB_PASSWORD)", svc, key, v)
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
	httpEnv := findFile(httpFiles, ".env.production")
	if httpEnv == nil {
		t.Fatal(".env.production missing (http)")
	}
	if contains(string(httpEnv.Contents), "Copy to") {
		t.Errorf(".env.production should not contain copy instructions:\n%s", httpEnv.Contents)
	}
	if !contains(string(httpEnv.Contents), "APP_URL=http://myapp.example.com") {
		t.Errorf("env missing plain-HTTP APP_URL:\n%s", httpEnv.Contents)
	}

	httpsFiles, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Host: "h", User: "u", Path: "p", Branch: "b", TLS: true}},
	}, "production")
	if err != nil {
		t.Fatalf("GenerateProdFiles (https): %v", err)
	}
	httpsEnv := findFile(httpsFiles, ".env.production")
	if httpsEnv == nil {
		t.Fatal(".env.production missing (https)")
	}
	if !contains(string(httpsEnv.Contents), "APP_URL=https://myapp.example.com") {
		t.Errorf("env missing HTTPS APP_URL:\n%s", httpsEnv.Contents)
	}
}
