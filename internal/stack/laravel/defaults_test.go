package laravel

import (
	"testing"

	"github.com/Bonnary/pier/internal/config"
)

func TestDefaultConfig(t *testing.T) {
	s := New()
	_ = config.StackConfig{}
	d := s.DefaultConfig()
	if d.Type != "laravel" {
		t.Errorf("Type = %q, want laravel", d.Type)
	}
	if d.PHP != "8.3" {
		t.Errorf("PHP = %q, want 8.3", d.PHP)
	}
	if d.Node != "22" {
		t.Errorf("Node = %q, want 22", d.Node)
	}
	if len(d.Services) != 0 {
		t.Errorf("Services = %v, want []", d.Services)
	}
}

func TestRequiredDirs(t *testing.T) {
	s := New()
	dirs := s.RequiredDirs()
	if len(dirs) == 0 {
		t.Error("RequiredDirs = [], want at least one entry")
	}
}
