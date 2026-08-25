package config

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

var devPortKeys = map[string]bool{
	"laravel":      true,
	"vite":         true,
	"mysql":        true,
	"postgres":     true,
	"redis":        true,
	"meilisearch":  true,
	"mailpit_smtp": true,
	"mailpit_ui":   true,
	"s3_api":       true,
	"s3_filer":     true,
	"s3_master":    true,
}

var deployPortKeys = map[string]bool{
	"laravel":        true,
	"webserver_http": true,
	"mysql":          true,
	"postgres":       true,
	"redis":          true,
	"meilisearch":    true,
	"s3_api":         true,
	"s3_filer":       true,
	"s3_master":      true,
}

// Load reads path as a pier.toml file, decodes it into a Config, runs
// Validate, and returns the result. Errors from the file system are
// returned directly; decode and validation failures wrap
// ErrConfigInvalid so callers can use errors.Is.
func Load(path string) (*Config, error) {
	var c Config
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return nil, fmt.Errorf("%w: decode %s: %v", ErrConfigInvalid, path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate checks every required field, every enum-style value
// (stack type, PHP version, Node version, dev bind), and every
// per-port override. It applies DefaultDevBind when [dev] bind is
// absent. It returns nil on success, otherwise wraps ErrConfigInvalid
// with the specific field that failed.
func (c *Config) Validate() error {
	if c.Project.Name == "" {
		return fmt.Errorf("%w: project.name is required", ErrConfigInvalid)
	}
	if !validStackType[c.Stack.Type] {
		return fmt.Errorf("%w: stack.type %q not supported (valid: laravel)", ErrConfigInvalid, c.Stack.Type)
	}
	if !validPHP[c.Stack.PHP] {
		return fmt.Errorf("%w: stack.php %q not in [8.2 8.3 8.4 8.5]", ErrConfigInvalid, c.Stack.PHP)
	}
	if !validNode[c.Stack.Node] {
		return fmt.Errorf("%w: stack.node %q not in [20 22]", ErrConfigInvalid, c.Stack.Node)
	}
	if c.Stack.QueueWorkers < 0 || c.Stack.QueueWorkers > MaxQueueWorkers {
		return fmt.Errorf("%w: stack.queue_workers = %d, must be in 0..%d (0 = default %d)", ErrConfigInvalid, c.Stack.QueueWorkers, MaxQueueWorkers, DefaultQueueWorkers)
	}
	if c.Dev.Bind == "" {
		c.Dev.Bind = DefaultDevBind
	}
	if !validDevBind[c.Dev.Bind] {
		return fmt.Errorf("%w: [dev] bind = %q must be %q or %q", ErrConfigInvalid, c.Dev.Bind, "127.0.0.1", "0.0.0.0")
	}
	for env, dc := range c.Deploy {
		if err := c.validateDeployEnv(env, dc); err != nil {
			return err
		}
	}
	for key, port := range c.Dev.Ports {
		if !devPortKeys[key] {
			return fmt.Errorf("%w: [dev.ports] has unknown key %q (valid: laravel, vite, mysql, postgres, redis, meilisearch, mailpit_smtp, mailpit_ui, s3_api, s3_filer, s3_master)", ErrConfigInvalid, key)
		}
		if port < 0 || port > 65535 {
			return fmt.Errorf("%w: [dev.ports.%s] = %d, must be in 0..65535 (0 = don't expose)", ErrConfigInvalid, key, port)
		}
	}
	for env, dc := range c.Deploy {
		for key, port := range dc.Ports {
			if !deployPortKeys[key] {
				return fmt.Errorf("%w: [deploy.%s.ports] has unknown key %q (valid: laravel, webserver_http, mysql, postgres, redis, meilisearch, s3_api, s3_filer, s3_master)", ErrConfigInvalid, env, key)
			}
			if port < 0 || port > 65535 {
				return fmt.Errorf("%w: [deploy.%s.ports.%s] = %d, must be in 0..65535 (0 = don't expose)", ErrConfigInvalid, env, key, port)
			}
		}
	}
	return nil
}

// validateHookList checks that every entry in a before_deploy /
// after_deploy list tokenizes to at least one argument, so a typo
// surfaces at config load instead of mid-deploy.
func validateHookList(env, key string, list []string) error {
	for i, entry := range list {
		args, err := SplitCommand(entry)
		if err != nil || len(args) == 0 {
			return fmt.Errorf("%w: deploy.%s.%s[%d] %q is not a valid non-empty command", ErrConfigInvalid, env, key, i, entry)
		}
	}
	return nil
}

// validateDeployEnv checks every required field and enum-style value
// of one [deploy.<env>] section, plus the domain and redirect_domains
// hostname syntax. Extracted from Validate so the per-env rule set
// stays reviewable and Validate's complexity stays in check.
func (c *Config) validateDeployEnv(env string, dc DeployConfig) error {
	configured := dc.Host != "" || dc.User != "" || dc.Path != "" || dc.Branch != ""
	if configured && (dc.Host == "" || dc.User == "" || dc.Path == "" || dc.Branch == "") {
		return fmt.Errorf("%w: deploy.%s requires host, user, path, branch (leave all empty to scaffold)", ErrConfigInvalid, env)
	}
	if dc.Domain != "" && !validHostname(dc.Domain) {
		return fmt.Errorf("%w: deploy.%s.domain %q is not a valid hostname (no scheme, port, path, whitespace, @, or bare IP)", ErrConfigInvalid, env, dc.Domain)
	}
	seen := map[string]bool{}
	for _, d := range dc.RedirectDomains {
		if !validHostname(d) {
			return fmt.Errorf("%w: deploy.%s.redirect_domains entry %q is not a valid hostname (no scheme, port, path, whitespace, @, or bare IP)", ErrConfigInvalid, env, d)
		}
		lowered := strings.ToLower(d)
		if seen[lowered] {
			return fmt.Errorf("%w: deploy.%s.redirect_domains has duplicate %q", ErrConfigInvalid, env, d)
		}
		seen[lowered] = true
	}
	if seen[strings.ToLower(dc.Domain)] {
		return fmt.Errorf("%w: deploy.%s.redirect_domains must not contain the domain %q", ErrConfigInvalid, env, dc.Domain)
	}
	if err := validateHookList(env, "before_deploy", dc.BeforeDeploy); err != nil {
		return err
	}
	if err := validateHookList(env, "after_deploy", dc.AfterDeploy); err != nil {
		return err
	}
	if dc.Builder != "" && !validBuilder[dc.Builder] {
		return fmt.Errorf("%w: deploy.%s.builder %q must be host_server, local_machine, or build_server", ErrConfigInvalid, env, dc.Builder)
	}
	if dc.QueueWorkers < 0 || dc.QueueWorkers > MaxQueueWorkers {
		return fmt.Errorf("%w: deploy.%s.queue_workers = %d, must be in 0..%d (0 = inherit)", ErrConfigInvalid, env, dc.QueueWorkers, MaxQueueWorkers)
	}
	if dc.BuilderMode() == "build_server" && (dc.BuildHost == "" || dc.BuildUser == "" || dc.BuildPath == "") {
		return fmt.Errorf("%w: deploy.%s.builder = \"build_server\" requires build_host, build_user, and build_path", ErrConfigInvalid, env)
	}
	return nil
}

// validHostname reports whether s is a bare hostname for Caddy HTTPS:
// no scheme, port, path, whitespace, userinfo, or bare IP — a bare IP
// belongs in the deploy host field, where Caddy would only ever
// present a self-signed certificate for it. Used to validate deploy
// domains so a pasted URL fails fast at config load instead of
// rendering a broken Caddyfile.
func validHostname(s string) bool {
	if s == "" || strings.ContainsAny(s, " /:@\t\r\n") || net.ParseIP(s) != nil {
		return false
	}
	return true
}
