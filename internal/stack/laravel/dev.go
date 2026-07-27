package laravel

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pcnerd/pier/internal/config"
	"github.com/pcnerd/pier/internal/stack"
)

type composeFile struct {
	Services map[string]composeService `yaml:"services"`
	Networks map[string]composeNetwork `yaml:"networks,omitempty"`
	Volumes  map[string]composeVolume  `yaml:"volumes,omitempty"`
}

type composeService struct {
	Build       *composeBuild       `yaml:"build,omitempty"`
	Image       string              `yaml:"image,omitempty"`
	Ports       []string            `yaml:"ports,omitempty"`
	Environment map[string]string   `yaml:"environment,omitempty"`
	Volumes     []string            `yaml:"volumes,omitempty"`
	ExtraHosts  []string            `yaml:"extra_hosts,omitempty"`
	Networks    []string            `yaml:"networks,omitempty"`
	DependsOn   []string            `yaml:"depends_on,omitempty"`
	Healthcheck *composeHealthcheck `yaml:"healthcheck,omitempty"`
	Command     []string            `yaml:"command,omitempty"`
	Restart     string              `yaml:"restart,omitempty"`
}

type composeBuild struct {
	Context    string            `yaml:"context"`
	Dockerfile string            `yaml:"dockerfile"`
	Args       map[string]string `yaml:"args,omitempty"`
}

type composeHealthcheck struct {
	Test        []string `yaml:"test"`
	Interval    string   `yaml:"interval,omitempty"`
	Timeout     string   `yaml:"timeout,omitempty"`
	Retries     string   `yaml:"retries,omitempty"`
	StartPeriod string   `yaml:"start_period,omitempty"`
}

type composeNetwork struct {
	Driver string `yaml:"driver"`
}

type composeVolume struct {
	Driver string `yaml:"driver"`
}

func (s *Stack) GenerateDevCompose(cfg config.Config) (stack.Files, error) {
	for _, name := range cfg.Stack.Services {
		if _, ok := lookup(name); !ok {
			return nil, fmt.Errorf("laravel: unknown service %q in [stack].services", name)
		}
	}

	runtimeDir, err := Runtime(cfg.Stack.PHP)
	if err != nil {
		return nil, err
	}
	var files stack.Files
	for _, name := range []string{"Dockerfile", "php.ini", "supervisord.conf"} {
		src := filepath.Join(runtimeDir, name)
		b, err := os.ReadFile(src)
		if err != nil {
			return nil, fmt.Errorf("laravel: read runtime %s: %w", src, err)
		}
		files = append(files, stack.File{
			Path: filepath.Join("docker", cfg.Stack.PHP, name), Contents: b, Mode: 0644,
		})
	}

	compose, err := renderDevCompose(cfg)
	if err != nil {
		return nil, err
	}
	files = append(files, stack.File{Path: "docker-compose.yml", Contents: compose, Mode: 0644})

	env, err := renderDevEnv(cfg)
	if err != nil {
		return nil, err
	}
	files = append(files, stack.File{Path: ".env", Contents: env, Mode: 0644})

	return files, nil
}

func renderDevCompose(cfg config.Config) ([]byte, error) {
	svcSet := map[string]bool{}
	for _, n := range cfg.Stack.Services {
		svcSet[n] = true
	}

	cf := composeFile{
		Services: map[string]composeService{
			"laravel.test": {
				Build: &composeBuild{
					Context:    fmt.Sprintf("./docker/%s", cfg.Stack.PHP),
					Dockerfile: "Dockerfile",
					Args:       map[string]string{"WWWGROUP": "1000"},
				},
				Image:       cfg.Project.Name + "/test:latest",
				ExtraHosts:  []string{"host.docker.internal:host-gateway"},
				Volumes:     []string{"./:/var/www/html"},
				Environment: devEnvForServices(svcSet),
				Networks:    []string{"pier"},
			},
		},
		Networks: map[string]composeNetwork{"pier": {Driver: "bridge"}},
	}

	var deps []string
	for _, n := range []string{"mysql", "postgres", "redis"} {
		if svcSet[n] {
			deps = append(deps, n)
		}
	}
	laravelTest := cf.Services["laravel.test"]
	laravelTest.DependsOn = deps
	cf.Services["laravel.test"] = laravelTest

	for _, name := range cfg.Stack.Services {
		s, ok := lookup(name)
		if !ok {
			return nil, fmt.Errorf("laravel: unknown service %q", name)
		}
		cs := composeService{
			Image: s.Image, Ports: s.Ports, Environment: s.Env, Volumes: s.Volumes, Networks: []string{"pier"},
		}
		if s.Healthcheck != nil {
			cs.Healthcheck = &composeHealthcheck{
				Test: s.Healthcheck.Test, Interval: s.Healthcheck.Interval,
				Timeout: s.Healthcheck.Timeout, Retries: s.Healthcheck.Retries, StartPeriod: s.Healthcheck.StartPeriod,
			}
		}
		cf.Services[name] = cs
	}

	vols := map[string]bool{}
	for _, name := range cfg.Stack.Services {
		switch name {
		case "mysql":
			vols["mysql_data"] = true
		case "postgres":
			vols["postgres_data"] = true
		case "redis":
			vols["redis_data"] = true
		case "meilisearch":
			vols["meili_data"] = true
		case "s3":
			vols["s3_data"] = true
		}
	}
	if len(vols) > 0 {
		cf.Volumes = map[string]composeVolume{}
		for v := range vols {
			cf.Volumes[v] = composeVolume{Driver: "local"}
		}
	}

	return yamlMarshal(cf)
}

func devEnvForServices(svcSet map[string]bool) map[string]string {
	env := map[string]string{"APP_ENV": "local", "APP_DEBUG": "true"}
	switch {
	case svcSet["mysql"]:
		env["DB_CONNECTION"] = "mysql"
		env["DB_HOST"] = "mysql"
		env["DB_PORT"] = "3306"
		env["DB_DATABASE"] = "laravel"
		env["DB_USERNAME"] = "root"
		env["DB_PASSWORD"] = "root"
	case svcSet["postgres"]:
		env["DB_CONNECTION"] = "pgsql"
		env["DB_HOST"] = "postgres"
		env["DB_PORT"] = "5432"
		env["DB_DATABASE"] = "laravel"
		env["DB_USERNAME"] = "laravel"
		env["DB_PASSWORD"] = "secret"
	default:
		env["DB_CONNECTION"] = "sqlite"
	}
	if svcSet["redis"] {
		env["REDIS_HOST"] = "redis"
		env["REDIS_PORT"] = "6379"
	}
	if svcSet["mailpit"] {
		env["MAIL_MAILER"] = "smtp"
		env["MAIL_HOST"] = "mailpit"
		env["MAIL_PORT"] = "1025"
	}
	return env
}

func renderDevEnv(cfg config.Config) ([]byte, error) {
	svcSet := map[string]bool{}
	for _, n := range cfg.Stack.Services {
		svcSet[n] = true
	}
	var b []byte
	b = append(b, []byte("APP_NAME="+cfg.Project.Name+"\n")...)
	b = append(b, []byte("APP_ENV=local\n")...)
	b = append(b, []byte("APP_KEY=\n")...)
	b = append(b, []byte("APP_DEBUG=true\n")...)
	b = append(b, []byte("APP_URL=http://localhost\n")...)
	switch {
	case svcSet["mysql"]:
		b = append(b, []byte("DB_CONNECTION=mysql\nDB_HOST=mysql\nDB_PORT=3306\nDB_DATABASE=laravel\nDB_USERNAME=root\nDB_PASSWORD=root\n")...)
	case svcSet["postgres"]:
		b = append(b, []byte("DB_CONNECTION=pgsql\nDB_HOST=postgres\nDB_PORT=5432\nDB_DATABASE=laravel\nDB_USERNAME=laravel\nDB_PASSWORD=secret\n")...)
	}
	if svcSet["redis"] {
		b = append(b, []byte("REDIS_HOST=redis\nREDIS_PORT=6379\n")...)
	}
	return b, nil
}
