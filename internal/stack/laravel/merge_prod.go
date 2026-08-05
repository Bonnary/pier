package laravel

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Bonnary/pier/internal/config"
)

// ownedProdServices returns the compose services pier owns in the
// prod file: the app, the webserver, and the env's effective
// sidecars.
func ownedProdServices(cfg config.Config, env string) map[string]bool {
	out := map[string]bool{"app": true, "webserver": true}
	for _, n := range cfg.ServicesForEnv(env) {
		out[n] = true
	}
	for n := range services() {
		out[n] = true
	}
	return out
}

// MergeProd renders the fresh prod compose from cfg for env and
// merges it into existing (the current docker-compose.prod.yml).
// Same ownership semantics as MergeDev: pier-owned services and
// per-key content get fresh values, user-owned services and keys are
// preserved, and pier services absent from the fresh render are
// dropped. When existing is empty the fresh render is returned with
// no warnings.
func MergeProd(existing string, cfg config.Config, env string, decision func(MergeWarning) Decision) (string, []MergeWarning, error) {
	files, err := New().GenerateProdFiles(cfg, env)
	if err != nil {
		return "", nil, err
	}
	var fresh []byte
	for _, f := range files {
		if f.Path == "docker-compose.prod.yml" {
			fresh = f.Contents
			break
		}
	}
	if fresh == nil {
		return "", nil, fmt.Errorf("laravel: fresh prod compose not generated")
	}
	if existing == "" {
		return string(fresh), nil, nil
	}

	var freshNode, existingNode yaml.Node
	if err := yaml.Unmarshal(fresh, &freshNode); err != nil {
		return "", nil, fmt.Errorf("laravel: parse fresh: %w", err)
	}
	if err := yaml.Unmarshal([]byte(existing), &existingNode); err != nil {
		return "", nil, fmt.Errorf("laravel: parse existing: %w", err)
	}

	owned := ownedProdServices(cfg, env)
	warnings, merged := mergeWithOwnership(&existingNode, &freshNode, owned, decision, "docker-compose.prod.yml")

	out, err := yaml.Marshal(merged)
	if err != nil {
		return "", warnings, err
	}
	return string(out), warnings, nil
}

// MergeEnvFile merges a fresh render of .env.production into the
// existing file: every existing KEY=VALUE line is kept verbatim and
// keys present in fresh but missing from existing are appended with
// fresh's values. Existing values (secrets) are never changed and no
// existing key is removed — .env.production is user-owned. An empty
// existing file yields the fresh render unchanged.
func MergeEnvFile(existing string, fresh []byte) string {
	if existing == "" {
		return string(fresh)
	}
	have := map[string]bool{}
	for _, ln := range strings.Split(existing, "\n") {
		if k, _, ok := strings.Cut(ln, "="); ok {
			have[strings.TrimSpace(k)] = true
		}
	}
	var b strings.Builder
	b.WriteString(existing)
	if !strings.HasSuffix(existing, "\n") {
		b.WriteString("\n")
	}
	for _, ln := range strings.Split(string(fresh), "\n") {
		k, _, ok := strings.Cut(ln, "=")
		if !ok {
			continue
		}
		key := strings.TrimSpace(k)
		if key == "" || have[key] {
			continue
		}
		b.WriteString(ln)
		b.WriteString("\n")
		have[key] = true
	}
	return b.String()
}
