package deploy

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/stack"
	laravelpkg "github.com/Bonnary/pier/internal/stack/laravel"
)

// trustedProxiesContents extracts the generated config/trustedproxy.php
// from the stack's files. The stack always ships it, so a missing entry
// is a programming error, not a user condition.
func trustedProxiesContents(files []stack.File) []byte {
	for _, f := range files {
		if f.Path == "config/trustedproxy.php" {
			return f.Contents
		}
	}
	panic("render: generated files lack config/trustedproxy.php")
}

// renderProdFiles re-renders docker-compose.prod.yml, the caddy
// Caddyfile, and .env.production in dir from pier.toml's per-env
// effective services. The compose file is merged (MergeProd) so
// user-owned services and keys survive while pier services dropped from
// the effective list are removed from the file. The Caddyfile is
// pier-rendered from the env's domain, so it is replaced wholesale.
// .env.production is merged with MergeEnvFile so existing values
// (secrets) are never clobbered or deleted. The sync phase that follows
// ships these files to the deploy host.
func renderProdFiles(dir string, cfg *config.Config, env string) error {
	stackMod, err := stack.ForName(cfg.Stack.Type)
	if err != nil {
		return fmt.Errorf("render: resolve stack: %w", err)
	}
	files, err := stackMod.GenerateProdFiles(*cfg, env)
	if err != nil {
		return fmt.Errorf("render: generate prod files: %w", err)
	}

	composePath := filepath.Join(dir, "docker-compose.prod.yml")
	existing, err := os.ReadFile(composePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("render: read %s: %w", composePath, err)
	}
	merged, _, err := laravelpkg.MergeProd(string(existing), *cfg, env, func(laravelpkg.MergeWarning) laravelpkg.Decision {
		return laravelpkg.DecisionKeep
	})
	if err != nil {
		return fmt.Errorf("render: merge docker-compose.prod.yml: %w", err)
	}
	if err := os.WriteFile(composePath, []byte(merged), 0644); err != nil {
		return fmt.Errorf("render: write %s: %w", composePath, err)
	}

	// config/trustedproxy.php must exist in the local build context: the
	// prod Dockerfile bakes the project into the image (COPY .), and
	// without it Laravel 13 renders http:// asset URLs behind the Caddy
	// reverse proxy (white screen on https). pier init refuses to re-run
	// over an existing pier.toml, so render creates it for projects that
	// predate the fix. An existing file is preserved — users may pin a
	// specific proxy instead of the '*' default.
	proxyPath := filepath.Join(dir, "config", "trustedproxy.php")
	if _, err := os.Stat(proxyPath); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(proxyPath), 0755); err != nil {
			return fmt.Errorf("render: mkdir %s: %w", filepath.Dir(proxyPath), err)
		}
		if err := os.WriteFile(proxyPath, trustedProxiesContents(files), 0644); err != nil {
			return fmt.Errorf("render: write %s: %w", proxyPath, err)
		}
	} else if err != nil {
		return fmt.Errorf("render: stat %s: %w", proxyPath, err)
	}

	// The Caddyfile is pier-rendered from the env's domain (and
	// redirect_domains), so it is always replaced: a stale file from a
	// domain-less `pier init` would otherwise keep serving plain HTTP
	// after the user sets a domain in pier.toml.
	caddyPath := filepath.Join(dir, "docker", "caddy", "Caddyfile")
	var freshCaddy []byte
	for _, f := range files {
		if f.Path == "docker/caddy/Caddyfile" {
			freshCaddy = f.Contents
			break
		}
	}
	if freshCaddy == nil {
		return fmt.Errorf("render: generated files lack docker/caddy/Caddyfile")
	}
	if err := os.MkdirAll(filepath.Dir(caddyPath), 0755); err != nil {
		return fmt.Errorf("render: mkdir %s: %w", filepath.Dir(caddyPath), err)
	}
	if err := os.WriteFile(caddyPath, freshCaddy, 0644); err != nil {
		return fmt.Errorf("render: write %s: %w", caddyPath, err)
	}

	var freshEnv []byte
	for _, f := range files {
		if f.Path == ".env.production" {
			freshEnv = f.Contents
			break
		}
	}
	if freshEnv == nil {
		return fmt.Errorf("render: generated files lack .env.production")
	}
	envPath := filepath.Join(dir, ".env.production")
	existingEnv, err := os.ReadFile(envPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("render: read %s: %w", envPath, err)
	}
	mergedEnv := laravelpkg.MergeEnvFile(string(existingEnv), freshEnv)
	if err := os.WriteFile(envPath, []byte(mergedEnv), 0644); err != nil {
		return fmt.Errorf("render: write %s: %w", envPath, err)
	}
	return nil
}
