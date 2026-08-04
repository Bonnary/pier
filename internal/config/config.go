// Package config defines the on-disk pier.toml schema, loads it into a
// typed Config, and validates every field. It is the only place the
// rest of pier should look for project name, stack choice, dev-port
// overrides, and per-env deploy targets.
package config

import "errors"

// ErrConfigInvalid is wrapped by every pier.toml validation failure
// (`Load` returns it via fmt.Errorf("%w: ...", ErrConfigInvalid, ...)).
// Callers use errors.Is to distinguish config errors from I/O errors.
var ErrConfigInvalid = errors.New("invalid pier.toml")

// DefaultDevBind is the host-side bind address pier uses for dev ports when
// the user has not opted in to LAN exposure. 127.0.0.1 keeps dev ports
// reachable only from the host — safe on shared networks.
const DefaultDevBind = "127.0.0.1"

var validPHP = map[string]bool{"8.2": true, "8.3": true, "8.4": true, "8.5": true}
var validNode = map[string]bool{"20": true, "22": true}
var validStackType = map[string]bool{"laravel": true}

var validDevBind = map[string]bool{
	"127.0.0.1": true,
	"0.0.0.0":   true,
}

// Config is the in-memory representation of a pier.toml file. Project,
// Stack, and Dev are single blocks; Deploy is keyed by env name
// (matching each [deploy.<env>] table in the TOML).
type Config struct {
	Project ProjectConfig           `toml:"project"`
	Stack   StackConfig             `toml:"stack"`
	Dev     DevConfig               `toml:"dev"`
	Deploy  map[string]DeployConfig `toml:"deploy"`
}

// ProjectConfig is the [project] table: a friendly name and the public
// domain the app will be served from in production.
type ProjectConfig struct {
	Name   string `toml:"name"`
	Domain string `toml:"domain"`
}

// StackConfig is the [stack] table: stack type, PHP and Node major
// versions, and the list of pre-registered sidecar services the user
// wants (mysql, redis, etc.).
type StackConfig struct {
	Type     string   `toml:"type"`
	PHP      string   `toml:"php"`
	Node     string   `toml:"node"`
	Services []string `toml:"services"`
}

// DevConfig is the [dev] table: host-side bind address, opt-in sidecar
// services, and per-port host overrides. The bind default
// (DefaultDevBind) is applied during Validate, not on the zero value.
type DevConfig struct {
	Bind     string                `toml:"bind"`
	Services map[string]DevService `toml:"services"`
	Ports    map[string]int        `toml:"ports"`
}

// DevService is one [dev.services.<name>] table — an opt-in dev-only
// sidecar image (log viewer, Reverb, dump inspector, or anything
// else) that pier merges into docker-compose.yml. Dev services are
// excluded from docker-compose.prod.yml.
type DevService struct {
	Image     string            `toml:"image"`
	Ports     []string          `toml:"ports"`
	Env       map[string]string `toml:"environment"`
	Volumes   []string          `toml:"volumes"`
	DependsOn []string          `toml:"depends_on"`
	Restart   string            `toml:"restart"`
}

// DeployConfig is one [deploy.<env>] table: SSH target, remote path,
// branch to build from, per-env host-port overrides, the TLS toggle,
// and the pre/post deploy hook commands. TLS is false by default
// (plain HTTP; SSL certificate provisioning is not shipped yet).
// BeforeDeploy and AfterDeploy are run in the app container on the
// deploy host, before and after the release is brought up.
type DeployConfig struct {
	Host   string         `toml:"host"`
	User   string         `toml:"user"`
	Path   string         `toml:"path"`
	Branch string         `toml:"branch"`
	Ports  map[string]int `toml:"ports"`
	TLS    bool           `toml:"tls"`
	// BeforeDeploy runs inside the app container on the deploy host
	// after the image build, while the old release is still serving.
	// A failing command logs a warning and the deploy continues.
	BeforeDeploy []string `toml:"before_deploy"`
	// AfterDeploy runs inside the app container on the deploy host
	// after `docker compose up` (and the nginx reload), before the
	// health probe. A failing command logs a warning and the deploy
	// continues.
	AfterDeploy []string `toml:"after_deploy"`
}
