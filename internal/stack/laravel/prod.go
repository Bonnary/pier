package laravel

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/stack"
)

func (s *Stack) GenerateProdFiles(cfg config.Config, env string) (stack.Files, error) {
	prodServices := []string{}
	for _, name := range cfg.ServicesForEnv(env) {
		svc, ok := lookup(name)
		if !ok {
			return nil, fmt.Errorf("laravel: unknown service %q in [stack].services or [deploy.%s].services", name, env)
		}
		if svc.DevOnly == "true" {
			continue
		}
		prodServices = append(prodServices, name)
	}

	compose, err := renderProdCompose(cfg, env, prodServices)
	if err != nil {
		return nil, err
	}
	envExample := renderProdEnvExample(cfg, env, prodServices)
	nginx := renderNginx(cfg)

	runtimeDir, err := Runtime(cfg.Stack.PHP)
	if err != nil {
		return nil, err
	}
	dockerfile, err := os.ReadFile(filepath.Join(runtimeDir, "Dockerfile"))
	if err != nil {
		return nil, fmt.Errorf("laravel: read runtime Dockerfile: %w", err)
	}

	envFile := renderProdEnv(cfg, env, prodServices)

	return stack.Files{
		{Path: "docker-compose.prod.yml", Contents: compose, Mode: 0644},
		{Path: ".env.production", Contents: envFile, Mode: 0644},
		{Path: ".env.production.example", Contents: envExample, Mode: 0644},
		{Path: "docker/nginx/default.conf", Contents: nginx, Mode: 0644},
		{Path: filepath.Join("docker", cfg.Stack.PHP, "Dockerfile"), Contents: dockerfile, Mode: 0644},
		{Path: filepath.Join("docker", cfg.Stack.PHP, "Dockerfile.prod"), Contents: renderProdDockerfile(dockerfile, cfg.Stack.PHP), Mode: 0644},
	}, nil
}

// renderProdDockerfile derives the production Dockerfile from the dev
// runtime Dockerfile. Dev gets the code from the ./:/var/www/html bind
// mount; prod has no bind mount, so the runtime stage is named and a
// final stage COPYs the synced project (build context "."), installs
// production dependencies (vendor/ and node_modules/ are excluded from
// the deploy sync), builds frontend assets, and hands the writable
// Laravel directories to the sail user (COPY preserves root ownership).
// The runtime COPYs of start-container/supervisord.conf/php.ini are
// rewritten to their docker/<php>/ location because the prod build
// context is the project root, not the runtime directory.
func renderProdDockerfile(runtime []byte, php string) []byte {
	base := string(runtime)
	base = strings.Replace(base, "FROM ubuntu:24.04", "FROM ubuntu:24.04 AS runtime", 1)
	for _, f := range []string{"start-container", "supervisord.conf", "php.ini"} {
		base = strings.ReplaceAll(base, "COPY "+f+" ", "COPY docker/"+php+"/"+f+" ")
	}
	return []byte(base + `# prod: bake the application into the image. Dev bind-mounts
# ./:/var/www/html instead, so the dev runtime Dockerfile never COPYs
# the code. Build with the project root as the context
# (docker-compose.prod.yml) so COPY . sees the synced application.
FROM runtime AS app
WORKDIR /var/www/html
COPY . /var/www/html
RUN composer install --no-dev --optimize-autoloader --prefer-dist --no-interaction
RUN if [ -f package.json ]; then npm ci --no-audit --no-fund && npm run build --if-present; fi
RUN chown -R sail:sail /var/www/html/storage /var/www/html/bootstrap/cache
`)
}

func renderProdCompose(cfg config.Config, env string, services []string) ([]byte, error) {
	deployCfg, ok := cfg.Deploy[env]
	if !ok {
		deployCfg = config.DeployConfig{}
	}
	cf := composeFile{
		Services: map[string]composeService{
			"app": {
				Image:       cfg.Project.Name + ":current",
				Restart:     "unless-stopped",
				Environment: prodEnvForServices(services),
				Networks:    []string{"pier"},
			},
			"webserver": {
				Image:     "nginx:alpine",
				Restart:   "unless-stopped",
				Ports:     webserverPorts("", deployCfg.Ports, deployCfg.TLS),
				Volumes:   []string{"./docker/nginx/default.conf:/etc/nginx/conf.d/default.conf:ro"},
				Networks:  []string{"pier"},
				DependsOn: []string{"app"},
			},
		},
		Networks: map[string]composeNetwork{"pier": {Driver: "bridge"}},
	}

	// host_server builds the image in place, so the compose file keeps
	// the build context (the synced project root) and the :latest
	// tag. The image modes ship a prebuilt image, so the app service
	// references the mutable :current tag instead and has no build
	// key — docker compose up must never try to build or pull.
	if deployCfg.BuilderMode() == "host_server" {
		app := cf.Services["app"]
		app.Image = cfg.Project.Name + ":latest"
		app.Build = &composeBuild{
			// Context is the project root, not ./docker/<php>:
			// the prod Dockerfile (Dockerfile.prod) bakes the
			// application into the image, while the dev runtime
			// Dockerfile gets the code from the ./:/var/www/html
			// bind mount and so never COPYs it.
			Context:    ".",
			Dockerfile: fmt.Sprintf("docker/%s/Dockerfile.prod", cfg.Stack.PHP),
			Args: map[string]string{
				// The runtime Dockerfile's ARG WWWGROUP has no
				// default, so the build fails with
				// `groupadd: invalid group ID 'sail'` when the
				// arg is absent. Prod has no host bind-mount,
				// so a fixed UID/GID (matching the Dockerfile's
				// ARG WWWUSER=1337 default) is fine.
				"WWWUSER":  "1337",
				"WWWGROUP": "1337",
			},
		}
		cf.Services["app"] = app
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
			Image: s.Image, Ports: sidecarPorts("", name, s.PortKeys, s.Ports, deployCfg.Ports, ProdPortDefaults),
			Environment: s.Env, Volumes: s.Volumes,
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

	vols := map[string]bool{}
	for _, v := range cf.Services {
		for _, m := range v.Volumes {
			vol := strings.SplitN(m, ":", 2)[0]
			if vol == "" || strings.HasPrefix(vol, ".") || strings.HasPrefix(vol, "/") {
				continue
			}
			vols[vol] = true
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

// webserverPorts assembles the `ports:` slice for the webserver service.
// The "laravel" key is the primary visible port: container 443 when TLS
// is enabled, container 80 for the plain-HTTP default. "webserver_http"
// (the HTTP→HTTPS redirect listener) is only published when TLS is
// enabled, unless the user explicitly set it while TLS is off. Either
// key may be 0 in the override to opt out. bind is the host-side bind
// prefix ("" = no prefix, host firewall restricts access; the deploy
// path always passes "").
func webserverPorts(bind string, override map[string]int, tls bool) []string {
	laravelDefault, laravelContainer := 80, 80
	if tls {
		laravelDefault, laravelContainer = 443, 443
	}
	defaults := map[string]int{"laravel": laravelDefault, "webserver_http": 80}
	var out []string
	if host, ok := ResolvePort("laravel", override, defaults); ok {
		out = append(out, PortBinding(bind, host, laravelContainer))
	}
	if tls {
		if host, ok := ResolvePort("webserver_http", override, defaults); ok {
			out = append(out, PortBinding(bind, host, 80))
		}
	} else if v, set := override["webserver_http"]; set && v != 0 {
		out = append(out, PortBinding(bind, v, 80))
	}
	return out
}

func prodEnvForServices(services []string) map[string]string {
	// Secrets are ${...} interpolations resolved by compose from the
	// deploy host's .env.production (every remote compose invocation
	// passes --env-file .env.production). APP_KEY must be present or
	// session encryption throws; DB_PASSWORD must be present or
	// Postgres answers `fe_sendauth: no password supplied` (500).
	env := map[string]string{"APP_ENV": "production", "APP_DEBUG": "false", "APP_KEY": "${APP_KEY}"}
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

// renderProdEnv renders the .env.production file written by init. It
// carries placeholder values (APP_KEY is empty, DB_PASSWORD is
// changeme) so the file is safe to write on a fresh project; fill in
// real values before deploying.
func renderProdEnv(cfg config.Config, env string, services []string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# %s production environment\n", cfg.Project.Name)
	fmt.Fprintf(&b, "# Fill in real values before deploying.\n\n")
	fmt.Fprintln(&b, "APP_NAME="+cfg.Project.Name)
	fmt.Fprintln(&b, "APP_ENV=production")
	fmt.Fprintln(&b, "APP_KEY=")
	fmt.Fprintln(&b, "APP_DEBUG=false")
	fmt.Fprintf(&b, "APP_URL=%s://%s\n\n", WebScheme(cfg, env), cfg.Project.Domain)
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

// renderProdEnvExample is the reference template for hand-managed
// environments: same keys as .env.production plus a note that the
// active file is created by init.
func renderProdEnvExample(cfg config.Config, env string, services []string) []byte {
	return []byte("# Reference template. pier init writes .env.production with the same keys.\n\n" + string(renderProdEnv(cfg, env, services)))
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
        proxy_pass http://app:80;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location ~ /\.ht {
        deny all;
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
