package laravel

import (
	"fmt"
	"path/filepath"
	"runtime"
)

func Runtime(php string) (string, error) {
	switch php {
	case "8.2", "8.3", "8.4", "8.5":
		_, thisFile, _, ok := runtime.Caller(0)
		if !ok {
			return "", fmt.Errorf("laravel: cannot resolve package directory")
		}
		return filepath.Join(filepath.Dir(thisFile), "runtimes", php), nil
	default:
		return "", fmt.Errorf("laravel: PHP %q not supported (valid: 8.2 8.3 8.4 8.5)", php)
	}
}

// SupportedPHPRuntimes returns the PHP runtime versions pier ships, in
// ascending order. The last element is treated as the latest and is the
// default cursor position in the init TUI. Keep this list in sync with
// the switch in Runtime() and the runtimes/ directory layout.
func SupportedPHPRuntimes() []string {
	return []string{"8.2", "8.3", "8.4", "8.5"}
}

// SupportedNodeVersions returns the Node major versions pier's Dockerfiles
// default to, in ascending order. The last element is treated as the
// latest and is the default cursor position in the init TUI.
func SupportedNodeVersions() []string {
	return []string{"20", "22"}
}
