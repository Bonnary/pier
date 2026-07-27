package stack_test

import (
	"sort"
	"testing"

	"github.com/pcnerd/pier/internal/stack"
	_ "github.com/pcnerd/pier/internal/stack/laravel"
	"github.com/pcnerd/pier/internal/config"
)

func TestRegistryHasLaravel(t *testing.T) {
	reg := stack.Registry()
	if _, ok := reg["laravel"]; !ok {
		t.Fatal(`Registry()["laravel"] missing`)
	}
}

func TestForName(t *testing.T) {
	s, err := stack.ForName("laravel")
	if err != nil {
		t.Fatalf("ForName(laravel): %v", err)
	}
	if s.Name() != "laravel" {
		t.Errorf("Name = %q, want laravel", s.Name())
	}
}

func TestForNameMissing(t *testing.T) {
	_, err := stack.ForName("python")
	if err == nil {
		t.Fatal("ForName(python) = nil error, want non-nil")
	}
}

func TestStackInterfaceSatisfied(t *testing.T) {
	cfg := config.Config{Stack: config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"}}
	s, _ := stack.ForName(cfg.Stack.Type)
	def := s.DefaultConfig()
	if def.PHP != "8.3" {
		t.Errorf("DefaultConfig().PHP = %q, want 8.3", def.PHP)
	}
}

func TestRegistryDeterministic(t *testing.T) {
	reg := stack.Registry()
	keys := make([]string, 0, len(reg))
	for k := range reg {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) != 1 || keys[0] != "laravel" {
		t.Errorf("registry keys = %v, want [laravel]", keys)
	}
}
