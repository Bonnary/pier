package laravel

import (
	"sort"
	"strings"

	"github.com/Bonnary/pier/internal/config"
)

const (
	devImageTag  = "test:latest"
	prodImageTag = ":latest"
)

// appImageFor returns the image reference for sidecar services that reuse the
// project's built app image (currently queue and scheduler). The static
// services() registry leaves Image empty for these; the render code calls
// appImageFor to substitute the concrete reference using the project name from
// pier.toml, so the generated compose never falls back to a non-existent tag
// like "myapp:latest".
//
// image is the tag portion of the reference and must already include its
// separator, e.g. "test:latest" (dev) or ":latest" (prod).
func appImageFor(cfg config.Config, image string) string {
	return cfg.Project.Name + image
}

// Service is one registered sidecar in the laravel stack (mysql,
// redis, mailpit, etc.). The render code (renderDevCompose /
// renderProdCompose) reads Image, Ports, Env, Volumes, and
// Healthcheck verbatim into the compose service. PortKeys is a
// parallel slice to Ports and is the set of keys looked up in
// DevPortDefaults / ProdPortDefaults for the host-side port.
// DevOnly is "true" for services that must not appear in
// docker-compose.prod.yml (e.g. mailpit).
type Service struct {
	Name        string
	Image       string
	DevOnly     string
	Ports       []string
	PortKeys    []string
	Env         map[string]string
	Volumes     []string
	Healthcheck *Healthcheck
	DependsOn   []string
}

// Healthcheck is the type-safe shape of a compose `healthcheck:`
// block. The fields map 1:1 to compose's hyphenated YAML keys and
// are written verbatim.
type Healthcheck struct {
	Test        []string
	Interval    string
	Timeout     string
	Retries     string
	StartPeriod string
}

func services() map[string]Service {
	return map[string]Service{
		"mysql": {
			Name:     "mysql",
			Image:    "mysql:8.0",
			Ports:    []string{"3306:3306"},
			PortKeys: []string{"mysql"},
			Env: map[string]string{
				"MYSQL_ROOT_PASSWORD": "root",
				"MYSQL_DATABASE":      "laravel",
			},
			Volumes: []string{"mysql_data:/var/lib/mysql"},
			Healthcheck: &Healthcheck{
				Test:     []string{"CMD", "mysqladmin", "ping", "-h", "localhost"},
				Interval: "10s", Timeout: "5s", Retries: "5", StartPeriod: "30s",
			},
		},
		"postgres": {
			Name:     "postgres",
			Image:    "postgres:16-alpine",
			Ports:    []string{"5432:5432"},
			PortKeys: []string{"postgres"},
			Env: map[string]string{
				"POSTGRES_USER": "laravel", "POSTGRES_PASSWORD": "secret", "POSTGRES_DB": "laravel",
			},
			Volumes: []string{"postgres_data:/var/lib/postgresql/data"},
			Healthcheck: &Healthcheck{
				Test:     []string{"CMD-SHELL", "pg_isready -U laravel"},
				Interval: "10s", Timeout: "5s", Retries: "5", StartPeriod: "30s",
			},
		},
		"redis": {
			Name: "redis", Image: "redis:7-alpine", Ports: []string{"6379:6379"}, PortKeys: []string{"redis"},
			Volumes: []string{"redis_data:/data"},
			Healthcheck: &Healthcheck{
				Test:     []string{"CMD", "redis-cli", "ping"},
				Interval: "10s", Timeout: "5s", Retries: "5", StartPeriod: "10s",
			},
		},
		"meilisearch": {
			Name: "meilisearch", Image: "getmeili/meilisearch:v1.10",
			Ports:    []string{"7700:7700"},
			PortKeys: []string{"meilisearch"},
			Env:      map[string]string{"MEILI_ENV": "development"},
			Volumes:  []string{"meili_data:/meili_data"},
			Healthcheck: &Healthcheck{
				Test:     []string{"CMD", "wget", "--spider", "-q", "http://127.0.0.1:7700/health"},
				Interval: "10s", Timeout: "5s", Retries: "5", StartPeriod: "10s",
			},
		},
		"mailpit": {
			Name: "mailpit", Image: "axllent/mailpit:latest",
			Ports: []string{"1025:1025", "8025:8025"}, PortKeys: []string{"mailpit_smtp", "mailpit_ui"}, DevOnly: "true",
			Healthcheck: &Healthcheck{
				Test:     []string{"CMD", "wget", "--spider", "-q", "http://localhost:8025/"},
				Interval: "10s", Timeout: "5s", Retries: "5", StartPeriod: "10s",
			},
		},
		"queue": {
			Name: "queue", Image: "",
			Env: map[string]string{
				"CONTAINER_ROLE":         "queue",
				"SUPERVISOR_PHP_COMMAND": "/usr/bin/php /var/www/html/artisan queue:work",
			},
			DependsOn: []string{"app"},
			Healthcheck: &Healthcheck{
				Test:     []string{"CMD-SHELL", "ps aux | grep -v grep | grep -q 'artisan queue:work'"},
				Interval: "30s", Timeout: "10s", Retries: "3",
			},
		},
		"scheduler": {
			Name: "scheduler", Image: "",
			Env: map[string]string{
				"CONTAINER_ROLE":         "scheduler",
				"SUPERVISOR_PHP_COMMAND": "/usr/bin/php /var/www/html/artisan schedule:work",
			},
			DependsOn: []string{"app"},
			Healthcheck: &Healthcheck{
				Test:     []string{"CMD-SHELL", "ps aux | grep -v grep | grep -q 'artisan schedule:work'"},
				Interval: "30s", Timeout: "10s", Retries: "3",
			},
		},
		"s3": {
			Name: "s3", Image: "chrislusf/seaweedfs:latest",
			Ports:    []string{"8333", "8888", "9333"},
			PortKeys: []string{"s3_api", "s3_filer", "s3_master"},
			Volumes:  []string{"s3_data:/data"},
			Healthcheck: &Healthcheck{
				Test:     []string{"CMD-SHELL", "nc -z 127.0.0.1 8333"},
				Interval: "10s", Timeout: "5s", Retries: "5", StartPeriod: "20s",
			},
		},
	}
}

func lookup(name string) (Service, bool) {
	lower := strings.ToLower(name)
	for k, v := range services() {
		if strings.ToLower(k) == lower {
			return v, true
		}
	}
	return Service{}, false
}

// SupportedServices returns the names of every service registered in
// services(), sorted alphabetically. Used as the picker input by the
// init and service-add TUIs so the TUI shows a stable order regardless
// of map iteration.
func SupportedServices() []string {
	out := make([]string, 0, len(services()))
	for k := range services() {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
