package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteDevcontainer(t *testing.T) {
	dir := t.TempDir()
	if err := writeDevcontainer(dir); err != nil {
		t.Fatalf("writeDevcontainer: %v", err)
	}
	path := filepath.Join(dir, ".devcontainer", "devcontainer.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got["service"] != "laravel.test" {
		t.Errorf("service = %v, want laravel.test", got["service"])
	}
	if got["workspaceFolder"] != "/var/www/html" {
		t.Errorf("workspaceFolder = %v, want /var/www/html", got["workspaceFolder"])
	}
}
