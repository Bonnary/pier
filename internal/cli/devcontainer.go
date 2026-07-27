package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func writeDevcontainer(projectPath string) error {
	dc := map[string]any{
		"name":              "pier",
		"dockerComposeFile": "../docker-compose.yml",
		"service":           "laravel.test",
		"workspaceFolder":   "/var/www/html",
		"customizations": map[string]any{
			"vscode": map[string]any{
				"extensions": []string{
					"bmewburn.vscode-intelephense-client",
					"laravel.vscode-laravel",
				},
			},
		},
	}
	b, err := json.MarshalIndent(dc, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Join(projectPath, ".devcontainer")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "devcontainer.json"), b, 0644)
}
