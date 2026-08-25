package laravel

import (
	"bytes"
	"fmt"
	"path/filepath"
	"runtime"
)

// leanLF converts CRLF to LF. Runtime files are copied into projects and
// run inside Linux containers, where CRLF (e.g. from a `core.autocrlf`
// Windows checkout of the pier repo) breaks script shebangs.
func leanLF(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

// Runtime returns the absolute path to the runtimes/<php>
// directory bundled with this module, which contains the
// Dockerfile, php.ini, supervisord.conf, and start-container that
// `pier init` copies into docker/<php>/ in the project tree.
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
