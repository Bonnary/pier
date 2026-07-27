package laravel

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func detect(path string) bool {
	composerPath := filepath.Join(path, "composer.json")
	b, err := os.ReadFile(composerPath)
	if err != nil {
		return false
	}
	var c struct {
		Require map[string]string `json:"require"`
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return false
	}
	if c.Require["laravel/framework"] == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(path, "artisan")); err != nil {
		return false
	}
	return true
}
