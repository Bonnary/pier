package deploy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSyncDirUploadsTree(t *testing.T) {
	addr := startSSHServer(t, passwordOnlyServer())
	host, port := testAddr(t, addr)
	remote := t.TempDir()
	local := t.TempDir()

	files := map[string]struct {
		mode os.FileMode
		body string
	}{
		"composer.json":              {0o644, `{"name":"x"}`},
		"app/Http/routes.php":        {0o755, "<?php\n"},
		".env.production":            {0o600, "APP_KEY=secret\n"},
		"sub/.env.staging":           {0o600, "APP_KEY=staging\n"},
		".git/HEAD":                  {0o644, "ref: refs/heads/main\n"},
		"storage/logs/laravel.log":   {0o644, "log line\n"},
		"node_modules/pkg/index.js":  {0o644, "module.exports = 1\n"},
		"note.swp":                   {0o644, "swap\n"},
	}
	for rel, f := range files {
		p := filepath.Join(local, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f.body), f.mode); err != nil {
			t.Fatal(err)
		}
	}

	c, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath:  writeTestKeyPath(t),
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	if err := c.SyncDir(context.Background(), local, remote, rsyncExcludes); err != nil {
		t.Fatalf("SyncDir: %v", err)
	}

	for _, rel := range []string{"composer.json", "app/Http/routes.php", ".env.production"} {
		if _, err := os.Stat(filepath.Join(remote, rel)); err != nil {
			t.Errorf("expected %s on remote: %v", rel, err)
		}
	}
	for _, rel := range []string{
		"sub/.env.staging", ".git/HEAD", "storage/logs/laravel.log",
		"node_modules/pkg/index.js", "note.swp",
	} {
		if _, err := os.Stat(filepath.Join(remote, rel)); err == nil {
			t.Errorf("unexpected file %s on remote", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(remote, ".git")); err == nil {
		t.Error("unexpected .git dir on remote")
	}
}

func TestSyncDirPreservesMode(t *testing.T) {
	addr := startSSHServer(t, passwordOnlyServer())
	host, port := testAddr(t, addr)
	remote := t.TempDir()
	local := t.TempDir()

	rel := "script.sh"
	if err := os.WriteFile(filepath.Join(local, rel), []byte("#!/bin/sh\n"), 0o754); err != nil {
		t.Fatal(err)
	}

	c, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath:  writeTestKeyPath(t),
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	if err := c.SyncDir(context.Background(), local, remote, rsyncExcludes); err != nil {
		t.Fatalf("SyncDir: %v", err)
	}
	info, err := os.Stat(filepath.Join(remote, rel))
	if err != nil {
		t.Fatalf("stat remote script: %v", err)
	}
	if info.Mode().Perm() != 0o754 {
		t.Errorf("mode = %v, want 0754", info.Mode().Perm())
	}
}

func TestSyncDirEmptyLocalKeepsNothing(t *testing.T) {
	addr := startSSHServer(t, passwordOnlyServer())
	host, port := testAddr(t, addr)
	remote := t.TempDir()

	c, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath:  writeTestKeyPath(t),
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	if err := c.SyncDir(context.Background(), t.TempDir(), remote, rsyncExcludes); err != nil {
		t.Fatalf("SyncDir: %v", err)
	}
	entries, err := os.ReadDir(remote)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("remote has %d entries, want 0", len(entries))
	}
}
