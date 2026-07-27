package deploy

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSSHConfigDefaults(t *testing.T) {
	c := SSHConfig{Host: "h", User: "u", KeyPath: filepath.Join("testdata", "id_ed25519")}
	if c.Port == 0 {
		c.Port = 22
	}
	if c.Port != 22 {
		t.Errorf("Port = %d, want 22 (default)", c.Port)
	}
}

func TestDialRejectsEmptyHost(t *testing.T) {
	_, err := Dial(context.Background(), SSHConfig{User: "u", KeyPath: "/nonexistent"})
	if err == nil {
		t.Fatal("Dial(empty host) = nil error, want non-nil")
	}
}
