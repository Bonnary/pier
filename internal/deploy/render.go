package deploy

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/stack"
	laravelpkg "github.com/Bonnary/pier/internal/stack/laravel"
)

// renderProdFiles re-renders docker-compose.prod.yml and
// .env.production in dir from pier.toml's per-env effective
// services. The compose file is merged (MergeProd) so user-owned
// services and keys survive while pier services dropped from the
// effective list are removed from the file. .env.production is
// merged with MergeEnvFile so existing values (secrets) are never
// clobbered or deleted. The sync phase that follows ships both files
// to the deploy host.
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
