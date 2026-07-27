package laravel

import (
	"fmt"
	"path/filepath"
)

func Runtime(php string) (string, error) {
	switch php {
	case "8.2", "8.3", "8.4", "8.5":
		return filepath.Join("internal", "stack", "laravel", "runtimes", php), nil
	default:
		return "", fmt.Errorf("laravel: PHP %q not supported (valid: 8.2 8.3 8.4 8.5)", php)
	}
}
