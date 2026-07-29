package laravel

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pcnerd/pier/internal/config"
	"github.com/pcnerd/pier/internal/stack"
)

func (s *Stack) GenerateProdFiles(cfg config.Config) (stack.Files, error) {
	prodServices := []string{}
	for _, name := range cfg.Stack.Services {
		svc, ok := lookup(name)
		if !ok {
			return nil, fmt.Errorf("laravel: unknown service %q in [stack].services", name)
		}
		if svc.DevOnly == "true" {
			continue
		}
		prodServices = append(prodServices, name)
	}

	compose, err := renderProdCompose(cfg, prodServices)
	if err != nil {
		return nil, err
	}
	envExample := renderProdEnvExample(cfg, prodServices)
	nginx := renderNginx(cfg)

	runtimeDir, err := Runtime(cfg.Stack.PHP)
	if err != nil {
		return nil, err
	}
	dockerfile, err := os.ReadFile(filepath.Join(runtimeDir, "Dockerfile"))
	if err != nil {
		return nil, fmt.Errorf("laravel: read runtime Dockerfile: %w", err)
	}

	return stack.Files{
		{Path: "docker-compose.prod.yml", Contents: compose, Mode: 0644},
		{Path: ".env.production.example", Contents: envExample, Mode: 0644},
		{Path: "docker/nginx/default.conf", Contents: nginx, Mode: 0644},
		{Path: filepath.Join("docker", cfg.Stack.PHP, "Dockerfile"), Contents: dockerfile, Mode: 0644},
	}, nil
}

func renderProdCompose(cfg config.Config, services []string) ([]byte, error) {
	cf := composeFile{
		Services: map[string]composeService{
			"app": {
				Build: &composeBuild{
					Context: fmt.Sprintf("./docker/%s", cfg.Stack.PHP), Dockerfile: "Dockerfile",
				},
				Image:       cfg.Project.Name + ":latest",
				Restart:     "unless-stopped",
				Environment: prodEnvForServices(services),
				Networks:    []string{"pier"},
			},
			"webserver": {
				Image:     "nginx:alpine",
				Restart:   "unless-stopped",
				Ports:     []string{"80:80", "443:443"},
				Volumes:   []string{"./docker/nginx/default.conf:/etc/nginx/conf.d/default.conf:ro"},
				Networks:  []string{"pier"},
				DependsOn: []string{"app"},
			},
		},
		Networks: map[string]composeNetwork{"pier": {Driver: "bridge"}},
	}

	for _, n := range services {
		switch n {
		case "mysql", "postgres", "redis":
			appSvc := cf.Services["app"]
			appSvc.DependsOn = append(appSvc.DependsOn, n)
			cf.Services["app"] = appSvc
		}
	}

	for _, name := range services {
		s, ok := lookup(name)
		if !ok {
			return nil, fmt.Errorf("laravel: unknown service %q", name)
		}
		cs := composeService{
			Image: s.Image, Ports: s.Ports, Environment: s.Env, Volumes: s.Volumes,
			Restart:  "unless-stopped",
			Networks: []string{"pier"},
		}
		if s.Healthcheck != nil {
			cs.Healthcheck = &composeHealthcheck{
				Test: s.Healthcheck.Test, Interval: s.Healthcheck.Interval,
				Timeout: s.Healthcheck.Timeout, Retries: s.Healthcheck.Retries, StartPeriod: s.Healthcheck.StartPeriod,
			}
		}
		if cs.Image == "" {
			cs.Image = appImageFor(cfg, prodImageTag)
		}
		cf.Services[name] = cs
	}

	return yamlMarshal(cf)
}

func prodEnvForServices(services []string) map[string]string {
	env := map[string]string{"APP_ENV": "production", "APP_DEBUG": "false"}
	set := map[string]bool{}
	for _, s := range services {
		set[s] = true
	}
	if set["mysql"] {
		env["DB_CONNECTION"] = "mysql"
		env["DB_HOST"] = "mysql"
		env["DB_PORT"] = "3306"
		env["DB_DATABASE"] = "laravel"
		env["DB_USERNAME"] = "laravel"
		env["DB_PASSWORD"] = "${DB_PASSWORD}"
	}
	if set["postgres"] {
		env["DB_CONNECTION"] = "pgsql"
		env["DB_HOST"] = "postgres"
		env["DB_PORT"] = "5432"
		env["DB_DATABASE"] = "laravel"
		env["DB_USERNAME"] = "laravel"
		env["DB_PASSWORD"] = "${DB_PASSWORD}"
	}
	if set["redis"] {
		env["REDIS_HOST"] = "redis"
		env["REDIS_PORT"] = "6379"
	}
	return env
}

func renderProdEnvExample(cfg config.Config, services []string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# %s production environment\n", cfg.Project.Name)
	fmt.Fprintf(&b, "# Copy to .env.production and fill in real values.\n\n")
	fmt.Fprintln(&b, "APP_NAME="+cfg.Project.Name)
	fmt.Fprintln(&b, "APP_ENV=production")
	fmt.Fprintln(&b, "APP_KEY=")
	fmt.Fprintln(&b, "APP_DEBUG=false")
	fmt.Fprintf(&b, "APP_URL=https://%s\n\n", cfg.Project.Domain)
	set := map[string]bool{}
	for _, s := range services {
		set[s] = true
	}
	if set["mysql"] || set["postgres"] {
		fmt.Fprintln(&b, "DB_CONNECTION="+ternary(set["mysql"], "mysql", "pgsql"))
		fmt.Fprintln(&b, "DB_HOST="+ternary(set["mysql"], "mysql", "postgres"))
		fmt.Fprintln(&b, "DB_PORT="+ternary(set["mysql"], "3306", "5432"))
		fmt.Fprintln(&b, "DB_DATABASE=laravel")
		fmt.Fprintln(&b, "DB_USERNAME=laravel")
		fmt.Fprintln(&b, "DB_PASSWORD=changeme")
	}
	if set["redis"] {
		fmt.Fprintln(&b, "\nREDIS_HOST=redis")
		fmt.Fprintln(&b, "REDIS_PORT=6379")
	}
	if set["s3"] {
		fmt.Fprintln(&b, "\nAWS_ENDPOINT=http://s3:8333")
		fmt.Fprintln(&b, "AWS_ACCESS_KEY_ID=somekey")
		fmt.Fprintln(&b, "AWS_SECRET_ACCESS_KEY=somesecret")
		fmt.Fprintln(&b, "AWS_BUCKET=app")
	}
	return b.Bytes()
}

func renderNginx(cfg config.Config) []byte {
	return []byte(fmt.Sprintf(`server {
    listen 80;
    server_name %s;
    root /var/www/html/public;
    index index.php;

    gzip on;
    gzip_types text/plain text/css application/json application/javascript text/xml application/xml application/xml+rss text/javascript;
    gzip_min_length 256;

    location / {
        try_files $uri $uri/ /index.php?$query_string;
    }

    location ~ \.php$ {
        fastcgi_pass app:9000;
        fastcgi_index index.php;
        fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;
        include fastcgi_params;
    }

    location ~ /\.ht {
        deny all;
    }

    location ~* \.(?:css|js|jpg|jpeg|gif|png|ico|svg|woff|woff2)$ {
        expires 30d;
        add_header Cache-Control "public, max-age=2592000";
    }
}
`, cfg.Project.Domain))
}

func ternary[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}
