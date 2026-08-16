package laravel

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
	caddyfile := renderCaddyfile(cfg, env)

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
		{Path: "docker/caddy/Caddyfile", Contents: caddyfile, Mode: 0644},
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
				Image:   "caddy:2-alpine",
				Restart: "unless-stopped",
				Ports:   webserverPorts("", deployCfg.Ports, deployCfg.Domain != ""),
				Volumes: []string{
					"./docker/caddy/Caddyfile:/etc/caddy/Caddyfile:ro",
					"caddy_data:/data",
					"caddy_config:/config",
				},
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
			// The app-image sidecars (queue, scheduler) must match the
			// tag the pipeline actually ships: host_server builds and
			// tags :latest in place; the image modes retag the
			// transferred image only as :current, and a :latest
			// reference would make compose pull it from the registry
			// (first deploys fail with "pull access denied").
			tag := prodImageTag
			if deployCfg.BuilderMode() != "host_server" {
				tag = ":current"
			}
			cs.Image = appImageFor(cfg, tag)
			// They also need the same connection env as the app.
			// Dev gets it from the bound ./:/var/www/html .env; prod
			// has no bind mount, and the image may carry the dev .env
			// (no .dockerignore), so without these the worker
			// authenticates with dev credentials against the deploy
			// host's database and crash-loops with "password
			// authentication failed".
			// svcEnv (not env) so the function parameter env — the
			// deploy env name — stays reachable for QueueWorkersForEnv.
			svcEnv := prodEnvForServices(services)
			for k, v := range s.Env {
				svcEnv[k] = v
			}
			if name == "queue" {
				svcEnv["SUPERVISOR_NUMPROCS"] = strconv.Itoa(cfg.QueueWorkersForEnv(env))
			}
			cs.Environment = svcEnv
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

// webserverPorts assembles the `ports:` slice for the webserver
// service. domain reports whether the env's domain is set: then Caddy
// serves HTTPS (container 443, port key "laravel") plus
// the HTTP→HTTPS redirect listener (container 80, port key
// "webserver_http"); without a domain it serves plain HTTP on
// container 80 under the "laravel" key. Either key may be 0 in the
// override to opt out. bind is the host-side bind prefix ("" = no
// prefix, host firewall restricts access; the deploy path always
// passes "").
func webserverPorts(bind string, override map[string]int, domain bool) []string {
	laravelDefault, laravelContainer := 80, 80
	if domain {
		laravelDefault, laravelContainer = 443, 443
	}
	defaults := map[string]int{"laravel": laravelDefault, "webserver_http": 80}
	var out []string
	if host, ok := ResolvePort("laravel", override, defaults); ok {
		out = append(out, PortBinding(bind, host, laravelContainer))
	}
	if domain {
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
	env := map[string]string{"APP_ENV": "production", "APP_DEBUG": "false", "APP_KEY": "${APP_KEY}", "SUPERVISOR_NUMPROCS": "1"}
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
		// Default the cache to redis when the stack ships it: the
		// database-cache default makes app-image workers boot-check
		// the `cache` table, which only exists after after_deploy
		// migrations — but up --wait demands they be healthy first,
		// so a first deploy can never pass. The value is interpolated
		// from .env.production (CACHE_STORE=redis) so the user can
		// override it.
		env["CACHE_STORE"] = "${CACHE_STORE}"
		// Same for the queue: the database-driver default makes
		// queue:work query the `jobs` table (and require a live
		// database) before after_deploy migrations run — and a
		// refused connection at boot makes the worker exit 0, which
		// supervisord's default autorestart=unexpected does not
		// restart, so the queue container stays unhealthy forever and
		// up --wait fails the deploy. Redis is in the stack, so point
		// the queue at it; the value is interpolated from
		// .env.production (QUEUE_CONNECTION=redis) so the user can
		// override it.
		env["QUEUE_CONNECTION"] = "${QUEUE_CONNECTION}"
	}
	return env
}

// renderProdEnv renders the .env.production file written by init. It
// carries placeholder values (APP_KEY is empty, DB_PASSWORD is
// changeme) so the file is safe to write on a fresh project; fill in
// real values before deploying.
func renderProdEnv(cfg config.Config, env string, services []string) []byte {
	var b bytes.Buffer
	deployCfg, ok := cfg.Deploy[env]
	if !ok {
		deployCfg = config.DeployConfig{}
	}
	fmt.Fprintf(&b, "# %s production environment\n", cfg.Project.Name)
	fmt.Fprintf(&b, "# Fill in real values before deploying.\n\n")
	fmt.Fprintln(&b, "APP_NAME="+cfg.Project.Name)
	fmt.Fprintln(&b, "APP_ENV=production")
	fmt.Fprintln(&b, "APP_KEY=")
	fmt.Fprintln(&b, "APP_DEBUG=false")
	if deployCfg.Domain != "" {
		if WebPort(cfg, env) == 443 {
			fmt.Fprintf(&b, "APP_URL=%s://%s\n\n", WebScheme(cfg, env), deployCfg.Domain)
		} else {
			fmt.Fprintf(&b, "APP_URL=%s://%s:%d\n\n", WebScheme(cfg, env), deployCfg.Domain, WebPort(cfg, env))
		}
	} else {
		host := "localhost"
		if deployCfg.Host != "" {
			host = deployCfg.Host
		}
		fmt.Fprintf(&b, "APP_URL=http://%s:%d\n\n", host, WebPort(cfg, env))
	}
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
		// Redis is in the stack: default the cache to redis so
		// queue/scheduler workers don't need the `cache` table before
		// the first after_deploy migrate (see prodEnvForServices).
		fmt.Fprintln(&b, "CACHE_STORE=redis")
		// And the queue to redis: the database-driver default makes
		// queue:work exit 0 on a refused boot-time connection (see
		// prodEnvForServices), which supervisord never restarts.
		fmt.Fprintln(&b, "QUEUE_CONNECTION=redis")
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

// renderCaddyfile renders the production Caddyfile for env. With a
// domain, Caddy serves HTTPS with an automatic Let's Encrypt
// certificate — ownership is proven by the ACME HTTP-01 challenge on
// ports 80/443, so the domain's A record must point at the deploy
// host — and every redirect_domains entry redirects to the env's
// domain. Without a domain it serves plain HTTP on container port 80.
func renderCaddyfile(cfg config.Config, env string) []byte {
	dc, ok := cfg.Deploy[env]
	if !ok {
		dc = config.DeployConfig{}
	}
	if dc.Domain == "" {
		return []byte(":80 {\n    encode gzip\n    reverse_proxy app:80\n}\n")
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s {\n    encode gzip\n    reverse_proxy app:80\n}\n", dc.Domain)
	for _, extra := range dc.RedirectDomains {
		fmt.Fprintf(&b, "\n%s {\n    redir https://%s{uri}\n}\n", extra, dc.Domain)
	}
	return b.Bytes()
}

func ternary[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}
