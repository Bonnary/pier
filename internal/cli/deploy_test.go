package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Bonnary/pier/internal/deploy"
)

func TestDeployMissingEnv(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pier.toml"), []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "deploy", "staging"})
	root.SilenceUsage = true
	err := root.Execute()
	if err == nil || !contains(err.Error(), "no [deploy.staging]") {
		t.Errorf("err = %v, want no-deploy-staging error", err)
	}
	_ = deploy.Pipeline{}
	_ = context.Background
}
