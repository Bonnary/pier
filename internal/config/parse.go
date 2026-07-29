package config

import (
	"fmt"
	"os"

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

func (c *Config) Validate() error {
	if c.Project.Name == "" {
		return fmt.Errorf("%w: project.name is required", ErrConfigInvalid)
	}
	if c.Project.Domain == "" {
		return fmt.Errorf("%w: project.domain is required", ErrConfigInvalid)
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
	for env, dc := range c.Deploy {
		if dc.Host == "" || dc.User == "" || dc.Path == "" || dc.Branch == "" {
			return fmt.Errorf("%w: deploy.%s requires host, user, path, branch", ErrConfigInvalid, env)
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
