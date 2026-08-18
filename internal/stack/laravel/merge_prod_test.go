package laravel

import (
	"testing"

	"github.com/Bonnary/pier/internal/config"
)

func keep(MergeWarning) Decision { return DecisionKeep }

func TestMergeProdEmptyExistingReturnsFresh(t *testing.T) {
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "myapp"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"redis"}},
	}
	merged, warns, err := MergeProd("", cfg, "production", keep)
	if err != nil {
		t.Fatalf("MergeProd: %v", err)
	}
	if len(warns) != 0 {
		t.Errorf("warns = %v, want none for empty existing", warns)
	}
	if !contains(merged, "redis:") {
		t.Errorf("fresh render missing redis:\n%s", merged)
	}
}

func TestMergeProdPreservesUserServiceAndAppEnv(t *testing.T) {
	existing := `services:
    app:
        image: myapp:latest
        environment:
            AWS_ACCESS_KEY_ID: ${AWS_ACCESS_KEY_ID}
            AWS_SECRET_ACCESS_KEY: ${AWS_SECRET_ACCESS_KEY}
    webserver:
        image: nginx:alpine
    redis:
        image: redis:7-alpine
    custom:
        image: custom/sidecar:1
networks:
    pier:
        driver: bridge
`
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "myapp"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"redis"}},
	}
	merged, _, err := MergeProd(existing, cfg, "production", keep)
	if err != nil {
		t.Fatalf("MergeProd: %v", err)
	}
	for _, want := range []string{"AWS_ACCESS_KEY_ID: ${AWS_ACCESS_KEY_ID}", "custom/sidecar:1"} {
		if !contains(merged, want) {
			t.Errorf("merged compose missing preserved content %q:\n%s", want, merged)
		}
	}
}

func TestMergeProdDropsRemovedPierService(t *testing.T) {
	existing := `services:
    app:
        image: myapp:latest
    webserver:
        image: nginx:alpine
    redis:
        image: redis:7-alpine
`
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "myapp"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Services: []string{}}}, // explicit: no sidecars
	}
	merged, _, err := MergeProd(existing, cfg, "production", keep)
	if err != nil {
		t.Fatalf("MergeProd: %v", err)
	}
	if contains(merged, "redis:") {
		t.Errorf("merged compose still has removed redis service:\n%s", merged)
	}
	if !contains(merged, "app:") || !contains(merged, "webserver:") {
		t.Errorf("merged compose missing app/webserver:\n%s", merged)
	}
}

func TestMergeProdAddsNewPierService(t *testing.T) {
	existing := `services:
    app:
        image: myapp:latest
    webserver:
        image: nginx:alpine
`
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "myapp"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Services: []string{"postgres"}}},
	}
	merged, _, err := MergeProd(existing, cfg, "production", keep)
	if err != nil {
		t.Fatalf("MergeProd: %v", err)
	}
	if !contains(merged, "postgres:") {
		t.Errorf("merged compose missing new postgres service:\n%s", merged)
	}
}

func TestMergeEnvFilePreservesValuesAndAddsMissing(t *testing.T) {
	existing := "# production environment\nAPP_KEY=real-secret\nDB_PASSWORD=supersecret\nAWS_ENDPOINT=http://s3:8333\n"
	fresh := []byte("APP_NAME=x\nAPP_ENV=production\nAPP_KEY=\nDB_CONNECTION=pgsql\nDB_HOST=postgres\nDB_PORT=5432\nDB_DATABASE=laravel\nDB_USERNAME=laravel\nDB_PASSWORD=changeme\nREDIS_HOST=redis\nREDIS_PORT=6379\n")
	got := MergeEnvFile(existing, fresh)
	for _, want := range []string{"APP_KEY=real-secret", "DB_PASSWORD=supersecret", "AWS_ENDPOINT=http://s3:8333"} {
		if !contains(got, want) {
			t.Errorf("MergeEnvFile lost existing line %q:\n%s", want, got)
		}
	}
	for _, want := range []string{"DB_CONNECTION=pgsql", "REDIS_HOST=redis"} {
		if !contains(got, want) {
			t.Errorf("MergeEnvFile missing fresh key %q:\n%s", want, got)
		}
	}
	if contains(got, "DB_PASSWORD=changeme") {
		t.Errorf("MergeEnvFile clobbered DB_PASSWORD with placeholder:\n%s", got)
	}
}

func TestMergeProdAddsDomainPublishes443(t *testing.T) {
	// A domain-less deploy ships webserver ports [80:80]. Adding a
	// domain re-renders them as [443:443, 80:80]; the merge must
	// publish 443 or Caddy never answers HTTPS and the health probe
	// (https://<domain>/up) fails.
	existing := `services:
    app:
        image: x:latest
    webserver:
        image: caddy:2-alpine
        ports:
            - 80:80
        volumes:
            - ./docker/caddy/Caddyfile:/etc/caddy/Caddyfile:ro
            - caddy_data:/data
            - caddy_config:/config
    redis:
        image: redis:7-alpine
networks:
    pier:
        driver: bridge
`
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "x"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"redis"}},
		Deploy:  map[string]config.DeployConfig{"production": {Domain: "myapp.example.com"}},
	}
	merged, _, err := MergeProd(existing, cfg, "production", keep)
	if err != nil {
		t.Fatalf("MergeProd: %v", err)
	}
	if !contains(merged, "443:443") {
		t.Errorf("merged compose must publish 443:443 once a domain is set (webserver ports are pier-owned):\n%s", merged)
	}
	if !contains(merged, "80:80") {
		t.Errorf("merged compose must keep the 80:80 HTTP-to-HTTPS redirect port:\n%s", merged)
	}
}

func TestMergeEnvFileUpdatesAPPURLWhenDomainChanges(t *testing.T) {
	existing := "# production environment\nAPP_KEY=real-secret\nAPP_URL=http://1.2.3.4:80\nDB_PASSWORD=supersecret\n"
	fresh := []byte("APP_NAME=x\nAPP_ENV=production\nAPP_KEY=\nAPP_URL=https://myapp.example.com\nDB_PASSWORD=changeme\n")
	got := MergeEnvFile(existing, fresh)
	if !contains(got, "APP_URL=https://myapp.example.com") {
		t.Errorf("APP_URL must track the env's domain (it is pier-derived):\n%s", got)
	}
	if contains(got, "APP_URL=http://1.2.3.4:80") {
		t.Errorf("stale APP_URL must not survive the merge:\n%s", got)
	}
	for _, want := range []string{"APP_KEY=real-secret", "DB_PASSWORD=supersecret"} {
		if !contains(got, want) {
			t.Errorf("MergeEnvFile lost secret %q:\n%s", want, got)
		}
	}
}

func TestMergeEnvFileEmptyExistingReturnsFresh(t *testing.T) {
	fresh := []byte("APP_KEY=\nDB_PASSWORD=changeme\n")
	got := MergeEnvFile("", fresh)
	if string(got) != string(fresh) {
		t.Errorf("MergeEnvFile(\"\") = %q, want fresh %q", got, fresh)
	}
}
