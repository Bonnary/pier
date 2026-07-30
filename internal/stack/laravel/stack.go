// Package laravel is pier's Laravel stack module: project
// detection, default pier.toml stack block, dev/prod compose
// rendering, smart-merge with an existing docker-compose.yml, and
// the service / port / runtime registries. It registers itself as
// the "laravel" stack in package stack during init().
package laravel

import (
	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/stack"
)

// Stack is the laravel stack module. Stateless; use New() to get
// a value.
type Stack struct{}

// New returns a fresh laravel Stack value.
func New() *Stack { return &Stack{} }

func init() {
	stack.Register("laravel", New())
}

// Name returns the [stack] type string "laravel".
func (s *Stack) Name() string { return "laravel" }

// Detect returns true when path contains a composer.json that
// requires laravel/framework AND an artisan file at the project
// root.
func (s *Stack) Detect(path string) bool { return detect(path) }

// DefaultConfig returns PHP 8.3, Node 22, no services — the
// defaults the init TUI shows first.
func (s *Stack) DefaultConfig() config.StackConfig {
	return config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{}}
}

// RequiredDirs lists "docker" and ".devcontainer" — the two
// project-tree directories pier writes into during init.
func (s *Stack) RequiredDirs() []string {
	return []string{"docker", ".devcontainer"}
}
