package laravel

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// EnsureViteHost walks the project root looking for vite.config.ts
// (falling back to vite.config.js) and, if the file exists and does
// not already set server.host, adds server: { host: true } to the
// defineConfig() call. Returns (true, nil) when the file was
// modified, (false, nil) when no change was needed (file missing
// or already configured), and a non-nil error only for I/O failures.
func EnsureViteHost(projectPath string) (bool, error) {
	candidates := []string{
		filepath.Join(projectPath, "vite.config.ts"),
		filepath.Join(projectPath, "vite.config.js"),
	}
	var path string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			path = c
			break
		}
	}
	if path == "" {
		return false, nil
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	src := string(contents)

	hasHost := regexp.MustCompile(`(?s)server\s*:\s*\{[^}]*?\bhost\b\s*:`).MatchString(src)
	if hasHost {
		return false, nil
	}

	serverRe := regexp.MustCompile(`(?s)server\s*:\s*\{`)
	if serverRe.MatchString(src) {
		updated := serverRe.ReplaceAllStringFunc(src, func(match string) string {
			return match + " host: true,"
		})
		if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
			return false, fmt.Errorf("write %s: %w", path, err)
		}
		return true, nil
	}

	defineConfigRe := regexp.MustCompile(`defineConfig\(\s*\{`)
	loc := defineConfigRe.FindStringIndex(src)
	if loc == nil {
		return false, nil
	}
	insertAt := loc[1]
	updated := src[:insertAt] + "\n    server: { host: true }," + src[insertAt:]
	if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}
