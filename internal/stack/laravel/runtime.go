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
