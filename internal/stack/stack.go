// Package stack is the registry for pier's stack modules. Each
// module (currently only "laravel") knows how to detect a project,
// fill in a default pier.toml stack block, render the dev and
// production compose files for a Config, and list the project
// directories it expects to find after init. Modules are
// constructor-registered via init() in their package.
package stack

import (
	"fmt"
	"os"
	"sync"

	"github.com/Bonnary/pier/internal/config"
)

// File is one pier-owned file a stack module wants written to the
// project tree (compose, env example, runtime Dockerfile, etc.).
type File struct {
	Path     string
	Contents []byte
	Mode     os.FileMode
}

// Files is a list of File, used as the return type for
// GenerateDevCompose and GenerateProdFiles.
type Files []File

// MergeWarning is raised when a stack's smart-merge logic finds
// user-owned content in the existing compose file that the user
// must explicitly accept or drop. The Decision callback returns
// DecisionKeep or DecisionDrop.
type MergeWarning struct {
	Service    string
	Key        string
	SourceFile string
}

// Stack is the contract every pier stack module implements.
type Stack interface {
	// Name returns the [stack] type string ("laravel").
	Name() string
	// Detect returns true when projectPath looks like a project of
	// this stack.
	Detect(projectPath string) bool
	// DefaultConfig returns the [stack] block pier writes into
	// pier.toml during init when the user accepts all defaults.
	DefaultConfig() config.StackConfig
	// GenerateDevCompose renders docker-compose.yml, .env, and the
	// runtime Dockerfile/php.ini/supervisord.conf files for local
	// development.
	GenerateDevCompose(cfg config.Config) (Files, error)
	// GenerateProdFiles renders docker-compose.prod.yml,
	// .env.production.example, the caddy Caddyfile, and the
	// production runtime Dockerfile for the named env.
	GenerateProdFiles(cfg config.Config, env string) (Files, error)
	// RequiredDirs is the list of project-tree directories this
	// stack expects to be present after init (e.g. "docker",
	// ".devcontainer"). Used by `pier init` to fail fast when
	// something is unexpectedly missing.
	RequiredDirs() []string
}

var (
	regMu sync.RWMutex
	reg   = map[string]Stack{}
)

// Register adds s to the registry under name. Panics on empty name,
// nil stack, or duplicate registration. Called from init() in each
// stack module.
func Register(name string, s Stack) {
	if name == "" || s == nil {
		panic("stack: Register: empty name or nil stack")
	}
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := reg[name]; dup {
		panic("stack: Register: duplicate registration for " + name)
	}
	reg[name] = s
}

// Registry returns a snapshot of the registered stack modules,
// keyed by [stack] type string. The returned map is owned by the
// caller.
func Registry() map[string]Stack {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make(map[string]Stack, len(reg))
	for k, v := range reg {
		out[k] = v
	}
	return out
}

// ForName returns the stack module registered under name, or an
// error listing the known names. The error message intentionally
// hard-codes the current set so the message stays stable when
// Registry() grows.
func ForName(name string) (Stack, error) {
	regMu.RLock()
	s, ok := reg[name]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("stack: %q not registered (known: laravel)", name)
	}
	return s, nil
}
