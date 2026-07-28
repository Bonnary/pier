package laravel

import (
	"sort"
	"strings"
)

type Service struct {
	Name        string
	Image       string
	DevOnly     string
	Ports       []string
	Env         map[string]string
	Volumes     []string
	Healthcheck *Healthcheck
	DependsOn   []string
}

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
			Name:  "mysql",
			Image: "mysql:8.0",
			Ports: []string{"3306:3306"},
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
			Name:  "postgres",
			Image: "postgres:16-alpine",
			Ports: []string{"5432:5432"},
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
			Name: "redis", Image: "redis:7-alpine", Ports: []string{"6379:6379"},
			Volumes: []string{"redis_data:/data"},
			Healthcheck: &Healthcheck{
				Test:     []string{"CMD", "redis-cli", "ping"},
				Interval: "10s", Timeout: "5s", Retries: "5", StartPeriod: "10s",
			},
		},
		"meilisearch": {
			Name: "meilisearch", Image: "getmeili/meilisearch:v1.10",
			Ports:   []string{"7700:7700"},
			Env:     map[string]string{"MEILI_ENV": "development"},
			Volumes: []string{"meili_data:/meili_data"},
			Healthcheck: &Healthcheck{
				Test:     []string{"CMD", "wget", "--spider", "-q", "http://localhost:7700/health"},
				Interval: "10s", Timeout: "5s", Retries: "5", StartPeriod: "10s",
			},
		},
		"mailpit": {
			Name: "mailpit", Image: "axllent/mailpit:latest",
			Ports: []string{"1025:1025", "8025:8025"}, DevOnly: "true",
			Healthcheck: &Healthcheck{
				Test:     []string{"CMD", "wget", "--spider", "-q", "http://localhost:8025/"},
				Interval: "10s", Timeout: "5s", Retries: "5", StartPeriod: "10s",
			},
		},
		"reverb": {
			Name: "reverb", Image: "serversideup/reverb:latest",
			Ports: []string{"8080:8080"},
			Env:   map[string]string{"REVERB_SERVER_PORT": "8080"},
			Healthcheck: &Healthcheck{
				Test:     []string{"CMD", "wget", "--spider", "-q", "http://localhost:8080/"},
				Interval: "10s", Timeout: "5s", Retries: "5", StartPeriod: "20s",
			},
		},
		"queue": {
			Name: "queue", Image: "${APP_IMAGE:-myapp:latest}",
			Env:       map[string]string{"CONTAINER_ROLE": "queue"},
			DependsOn: []string{"app"},
			Healthcheck: &Healthcheck{
				Test:     []string{"CMD-SHELL", "ps aux | grep -v grep | grep -q 'artisan queue:work'"},
				Interval: "30s", Timeout: "10s", Retries: "3",
			},
		},
		"scheduler": {
			Name: "scheduler", Image: "${APP_IMAGE:-myapp:latest}",
			Env:       map[string]string{"CONTAINER_ROLE": "scheduler"},
			DependsOn: []string{"app"},
			Healthcheck: &Healthcheck{
				Test:     []string{"CMD-SHELL", "ps aux | grep -v grep | grep -q 'artisan schedule:work'"},
				Interval: "30s", Timeout: "10s", Retries: "3",
			},
		},
		"log-viewer": {
			Name: "log-viewer", Image: "opcodesio/log-viewer:latest",
			Ports: []string{"8081:8080"}, DevOnly: "true",
		},
		"dumps": {
			Name: "dumps", Image: "nicolasbissig/laravel-dumps:latest",
			Ports: []string{"9191:9191"}, DevOnly: "true",
		},
		"s3": {
			Name: "s3", Image: "chrislusf/seaweedfs:latest",
			Ports:   []string{"8333", "8888", "9333"},
			Volumes: []string{"s3_data:/data"},
			Healthcheck: &Healthcheck{
				Test:     []string{"CMD-SHELL", "echo 's3' | nc -w 1 localhost 8333 | grep -q s3"},
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
