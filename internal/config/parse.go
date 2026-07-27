package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

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
	return nil
}
