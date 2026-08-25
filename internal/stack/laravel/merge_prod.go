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

// userOwnedEnvKeys are the .env.production keys pier never touches:
// credentials the user fills in (APP_KEY, DB_PASSWORD, AWS access
// keys) and values the compose file interpolates as ${...} so users
// can override them from .env.production (TRUSTED_PROXIES,
// CACHE_STORE, QUEUE_CONNECTION). Every other key renderProdEnv emits
// is pier-derived from pier.toml (APP_URL from the env's domain, DB_*
// and REDIS_* from the service list, ...) and is updated when the
// render changes.
var userOwnedEnvKeys = map[string]bool{
	"APP_KEY":               true,
	"DB_PASSWORD":           true,
	"AWS_ACCESS_KEY_ID":     true,
	"AWS_SECRET_ACCESS_KEY": true,
	"TRUSTED_PROXIES":       true,
	"CACHE_STORE":           true,
	"QUEUE_CONNECTION":      true,
}

// MergeEnvFile merges a fresh render of .env.production into the
// existing file. Existing user-owned lines (secrets and overridable
// keys, see userOwnedEnvKeys) are kept verbatim; pier-derived keys
// whose fresh value changed are updated to the fresh value; keys
// present in fresh but missing from existing are appended; user-added
// keys absent from fresh are kept. An empty existing file yields the
// fresh render unchanged.
func MergeEnvFile(existing string, fresh []byte) string {
	if existing == "" {
		return string(fresh)
	}
	freshValue := map[string]string{}
	for _, ln := range strings.Split(string(fresh), "\n") {
		if k, v, ok := strings.Cut(ln, "="); ok {
			freshValue[strings.TrimSpace(k)] = v
		}
	}
	have := map[string]bool{}
	var b strings.Builder
	for _, ln := range strings.Split(existing, "\n") {
		k, _, ok := strings.Cut(ln, "=")
		if !ok {
			b.WriteString(ln)
			b.WriteString("\n")
			continue
		}
		key := strings.TrimSpace(k)
		have[key] = true
		if !userOwnedEnvKeys[key] {
			if v, ok := freshValue[key]; ok && v != valueOf(ln) {
				b.WriteString(key + "=" + v)
				b.WriteString("\n")
				continue
			}
		}
		b.WriteString(ln)
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

// valueOf returns the part after the first '=' of a KEY=VALUE line.
func valueOf(ln string) string {
	_, v, ok := strings.Cut(ln, "=")
	if !ok {
		return ""
	}
	return v
}
