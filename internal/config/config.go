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

// DefaultQueueWorkers is the number of queue:work processes the
// queue service runs when queue_workers is not configured anywhere.
const DefaultQueueWorkers = 1

// MaxQueueWorkers is the largest queue_workers value pier accepts.
// All workers share one container; 32 keeps a single host sane.
const MaxQueueWorkers = 32

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

// ProjectConfig is the [project] table: the project's friendly name.
// Public domains are configured per deploy env in [deploy.<env>].domain.
type ProjectConfig struct {
	Name string `toml:"name"`
}

// StackConfig is the [stack] table: stack type, PHP and Node major
// versions, and the list of pre-registered sidecar services the user
// wants (mysql, redis, etc.).
type StackConfig struct {
	Type     string   `toml:"type"`
	PHP      string   `toml:"php"`
	Node     string   `toml:"node"`
	Services []string `toml:"services"`
	// QueueWorkers is the number of queue:work processes the queue
	// service runs (dev and, unless overridden per env, prod).
	// 0 means "not set": dev uses DefaultQueueWorkers and prod
	// envs fall back to it.
	QueueWorkers int `toml:"queue_workers"`
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
// branch to build from, per-env host-port overrides, the public
// domain(s) Caddy serves, and the pre/post deploy hook commands.
// A non-empty env domain means Caddy serves HTTPS with an
// automatic Let's Encrypt certificate; an empty one means plain
// HTTP by IP.
type DeployConfig struct {
	Host   string `toml:"host"`
	User   string `toml:"user"`
	Path   string `toml:"path"`
	Branch string `toml:"branch"`
	// Domain is the env's public domain. A non-empty domain means
	// Caddy serves HTTPS with an automatic Let's Encrypt certificate;
	// an empty one means plain HTTP by IP.
	Domain string `toml:"domain"`
	// RedirectDomains lists additional domains Caddy serves,
	// redirecting each to this env's domain (e.g. www.example.com for
	// example.com).
	RedirectDomains []string `toml:"redirect_domains"`
	// Builder selects where the production image is built for this
	// env: "host_server" (default, empty means this) builds on the
	// deploy host itself, "local_machine" builds on the machine
	// running pier, "build_server" builds on a dedicated remote
	// machine. The image-mode values stream the finished image to the
	// host over SSH; host_server builds in place.
	Builder string `toml:"builder"`
	// BuildHost, BuildUser, and BuildPath configure the build server
	// used when Builder is "build_server": SSH target and the path
	// where the source tree is synced and built.
	BuildHost string `toml:"build_host"`
	BuildUser string `toml:"build_user"`
	BuildPath string `toml:"build_path"`
	// Services, when present, is the full list of sidecar services
	// for this env, overriding [stack].services. When absent the env
	// inherits [stack].services. An explicitly empty list means the
	// env runs no sidecars.
	Services []string       `toml:"services"`
	Ports    map[string]int `toml:"ports"`
	// QueueWorkers overrides [stack].queue_workers for this env.
	// 0 means inherit the stack value.
	QueueWorkers int `toml:"queue_workers"`
	// BeforeDeploy runs inside the app container on the deploy host
	// after the image build, while the old release is still serving.
	// Commands run in order and stop at the first failure; a failing
	// command aborts the deploy (the old release keeps serving). The
	// phase is skipped on a first deploy, when no app container
	// exists yet.
	BeforeDeploy []string `toml:"before_deploy"`
	// AfterDeploy runs inside the app container on the deploy host
	// after `docker compose up` (and the caddy reload), before the
	// health probe. Commands run in order and stop at the first
	// failure; a failing command aborts the deploy and rolls back to
	// the previous image.
	AfterDeploy []string `toml:"after_deploy"`
}

// validBuilder lists the accepted [deploy.<env>].builder values.
var validBuilder = map[string]bool{
	"host_server":   true,
	"local_machine": true,
	"build_server":  true,
}

// BuilderMode returns the effective builder for the env: the
// configured value, or "host_server" when absent (the historical
// behavior: build and host on the same machine).
func (d DeployConfig) BuilderMode() string {
	if d.Builder == "" {
		return "host_server"
	}
	return d.Builder
}

// ServicesForEnv returns the effective sidecar service list for env:
// [deploy.<env>].services when present (nil distinguishes "absent"
// from an explicit empty list), else [stack].services.
func (c *Config) ServicesForEnv(env string) []string {
	if dc, ok := c.Deploy[env]; ok && dc.Services != nil {
		return dc.Services
	}
	return c.Stack.Services
}

// QueueWorkers returns the effective [stack] queue worker count:
// the configured value, or DefaultQueueWorkers when absent.
func (c *Config) QueueWorkers() int {
	if c.Stack.QueueWorkers > 0 {
		return c.Stack.QueueWorkers
	}
	return DefaultQueueWorkers
}

// QueueWorkersForEnv returns the effective queue worker count for
// env: [deploy.<env>].queue_workers when set, else the stack value.
func (c *Config) QueueWorkersForEnv(env string) int {
	if dc, ok := c.Deploy[env]; ok && dc.QueueWorkers > 0 {
		return dc.QueueWorkers
	}
	return c.QueueWorkers()
}
