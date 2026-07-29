package config

import "errors"

var ErrConfigInvalid = errors.New("invalid pier.toml")

var validPHP = map[string]bool{"8.2": true, "8.3": true, "8.4": true, "8.5": true}
var validNode = map[string]bool{"20": true, "22": true}
var validStackType = map[string]bool{"laravel": true}

type Config struct {
	Project ProjectConfig           `toml:"project"`
	Stack   StackConfig             `toml:"stack"`
	Dev     DevConfig               `toml:"dev"`
	Deploy  map[string]DeployConfig `toml:"deploy"`
}

type ProjectConfig struct {
	Name   string `toml:"name"`
	Domain string `toml:"domain"`
}

type StackConfig struct {
	Type     string   `toml:"type"`
	PHP      string   `toml:"php"`
	Node     string   `toml:"node"`
	Services []string `toml:"services"`
}

type DevConfig struct {
	Services map[string]DevService `toml:"services"`
	Ports    map[string]int       `toml:"ports"`
}

type DevService struct {
	Image     string            `toml:"image"`
	Ports     []string          `toml:"ports"`
	Env       map[string]string `toml:"environment"`
	Volumes   []string          `toml:"volumes"`
	DependsOn []string          `toml:"depends_on"`
	Restart   string            `toml:"restart"`
}

type DeployConfig struct {
	Host   string         `toml:"host"`
	User   string         `toml:"user"`
	Path   string         `toml:"path"`
	Branch string         `toml:"branch"`
	Ports  map[string]int `toml:"ports"`
}
