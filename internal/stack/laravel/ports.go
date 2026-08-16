package laravel

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Bonnary/pier/internal/config"
)

// DevPortDefaults maps every dev port key to its default host port.
// Override per-project via [dev.ports] in pier.toml. 0 is not a default —
// use [dev.ports.<key> = 0] to opt out of exposing a port.
var DevPortDefaults = map[string]int{
	"laravel":      8000,
	"vite":         5173,
	"mysql":        3306,
	"postgres":     5432,
	"redis":        6379,
	"meilisearch":  7700,
	"mailpit_smtp": 1025,
	"mailpit_ui":   8025,
	"s3_api":       8333,
	"s3_filer":     8888,
	"s3_master":    9333,
}

// ProdPortDefaults maps every deploy-env port key to its default host port.
// Override per-env via [deploy.<env>.ports] in pier.toml. The `laravel` key
// here is the webserver HTTPS port (the primary visible port in production);
// `webserver_http` is the HTTP→HTTPS redirect on port 80.
var ProdPortDefaults = map[string]int{
	"laravel":        443,
	"webserver_http": 80,
	"mysql":          3306,
	"postgres":       5432,
	"redis":          6379,
	"meilisearch":    7700,
	"s3_api":         8333,
	"s3_filer":       8888,
	"s3_master":      9333,
}

// BindAddr returns the bind-address prefix for compose `ports:` strings.
// bind comes from the user's pier.toml ([dev] bind) or "" for deploy
// compose (no prefix, the host firewall restricts access). An empty bind
// falls back to the config default — callers should pass the validated
// value from cfg.Dev.Bind rather than relying on this fallback.
func BindAddr(bind string) string {
	if bind == "" {
		return config.DefaultDevBind
	}
	return bind
}

// PortBinding formats a compose `ports:` entry. bind comes from BindAddr;
// host is the override-resolved host-side port; container is the fixed
// container-side port. When bind is empty (prod/staging), the entry is
// formatted as "host:container" with no leading colon. When bind is set
// (dev), the entry is "bind:host:container".
func PortBinding(bind string, host, container int) string {
	if bind == "" {
		return fmt.Sprintf("%d:%d", host, container)
	}
	return fmt.Sprintf("%s:%d:%d", bind, host, container)
}

// ResolvePort returns the host port to bind for a given port key, given the
// user's override map and the env's default map. ok is false when the user
// has explicitly set the key to 0 (don't expose) OR when neither the
// override nor the default has the key.
func ResolvePort(key string, override, defaults map[string]int) (host int, ok bool) {
	if v, set := override[key]; set {
		if v == 0 {
			return 0, false
		}
		return v, true
	}
	if v, set := defaults[key]; set {
		return v, true
	}
	return 0, false
}

// WebScheme returns the URL scheme for the env's primary web
// endpoint: "https" when the env has an effective domain (Caddy
// provisions a Let's Encrypt certificate automatically, proving
// ownership via the ACME HTTP-01 challenge on ports 80/443), else
// "http" (no domain, plain HTTP by IP).
func WebScheme(cfg config.Config, env string) string {
	if cfg.DomainForEnv(env) != "" {
		return "https"
	}
	return "http"
}

// WebPort returns the host port for the env's primary web endpoint:
// the [deploy.<env>.ports.laravel] override when set (0 = don't
// expose falls back to the default), else 443 when the env has a
// domain or 80 for the no-domain plain-HTTP default.
func WebPort(cfg config.Config, env string) int {
	deployCfg, ok := cfg.Deploy[env]
	if !ok {
		deployCfg = config.DeployConfig{}
	}
	if v, set := deployCfg.Ports["laravel"]; set && v != 0 {
		return v
	}
	if cfg.DomainForEnv(env) != "" {
		return 443
	}
	return 80
}

// CollectHostPorts parses a rendered docker-compose YAML and returns the
// host-side port number of every <bind>:<host>:<container> binding, plus
// the host-side of every DevService.Ports entry passed in. Used by the
// pre-flight port probe in `pier dev` to catch host-port collisions before
// `docker compose up`.
func CollectHostPorts(composeYAML []byte, userServices map[string]config.DevService) ([]int, error) {
	var doc struct {
		Services map[string]struct {
			Ports []yaml.Node `yaml:"ports"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(composeYAML, &doc); err != nil {
		return nil, err
	}
	var out []int
	for _, svc := range doc.Services {
		for _, p := range svc.Ports {
			host, ok := hostOfPortBinding(p.Value)
			if ok {
				out = append(out, host)
			}
		}
	}
	for _, ds := range userServices {
		for _, p := range ds.Ports {
			host, ok := hostOfPortBinding(p)
			if ok {
				out = append(out, host)
			}
		}
	}
	return out, nil
}

// hostOfPortBinding parses a compose `ports:` entry and returns the host
// port. Supports three forms:
//
//	"8000"                    -> 8000 (host == container)
//	"127.0.0.1:8000:8000"     -> 8000
//	"8000:8000"               -> 8000
//
// Returns ok=false if the entry can't be parsed as a port number.
func hostOfPortBinding(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	parts := strings.Split(s, ":")
	var hostSeg string
	switch len(parts) {
	case 1:
		hostSeg = parts[0]
	case 2:
		hostSeg = parts[0]
	default:
		hostSeg = parts[len(parts)-2]
	}
	n, err := strconv.Atoi(hostSeg)
	if err != nil {
		return 0, false
	}
	return n, true
}
