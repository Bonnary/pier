package config

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFullWithPorts(t *testing.T) {
	cfg, err := Load(filepath.Join("testdata", "full-ports.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Dev.Ports["laravel"]; got != 8080 {
		t.Errorf("Dev.Ports[laravel] = %d, want 8080", got)
	}
	if got := cfg.Dev.Ports["vite"]; got != 5174 {
		t.Errorf("Dev.Ports[vite] = %d, want 5174", got)
	}
	prod := cfg.Deploy["production"]
	if got := prod.Ports["laravel"]; got != 8383 {
		t.Errorf("Deploy[production].Ports[laravel] = %d, want 8383", got)
	}
}

func TestLoadMinimal(t *testing.T) {
	cfg, err := Load(filepath.Join("testdata", "minimal.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Project.Name != "myapp" {
		t.Errorf("Project.Name = %q, want myapp", cfg.Project.Name)
	}
	if cfg.Stack.PHP != "8.3" {
		t.Errorf("Stack.PHP = %q, want 8.3", cfg.Stack.PHP)
	}
	if cfg.Stack.Node != "22" {
		t.Errorf("Stack.Node = %q, want 22", cfg.Stack.Node)
	}
	if len(cfg.Stack.Services) != 0 {
		t.Errorf("Stack.Services = %v, want []", cfg.Stack.Services)
	}
}

func TestLoadFull(t *testing.T) {
	cfg, err := Load(filepath.Join("testdata", "full.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Stack.Services; len(got) != 3 || got[0] != "redis" || got[1] != "mailpit" || got[2] != "s3" {
		t.Errorf("Stack.Services = %v, want [redis mailpit s3]", got)
	}
	prod, ok := cfg.Deploy["production"]
	if !ok {
		t.Fatal(`Deploy["production"] missing`)
	}
	if prod.Host != "prod.example.com" || prod.User != "deploy" || prod.Path != "/srv/myapp" || prod.Branch != "main" {
		t.Errorf("production = %+v", prod)
	}
	staging, ok := cfg.Deploy["staging"]
	if !ok {
		t.Fatal(`Deploy["staging"] missing`)
	}
	if staging.Branch != "develop" {
		t.Errorf("staging.Branch = %q, want develop", staging.Branch)
	}
}

func TestLoadInvalid(t *testing.T) {
	_, err := Load(filepath.Join("testdata", "invalid.toml"))
	if err == nil {
		t.Fatal("Load(invalid) = nil error, want ErrConfigInvalid")
	}
	if !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("err = %v, want errors.Is(err, ErrConfigInvalid)", err)
	}
}

func TestLoadMissing(t *testing.T) {
	_, err := Load(filepath.Join("testdata", "does-not-exist.toml"))
	if err == nil {
		t.Fatal("Load(missing) = nil error, want non-nil")
	}
}

func TestValidatePHPVersion(t *testing.T) {
	c := &Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "7.4", Node: "22"},
	}
	if err := c.Validate(); !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("Validate = %v, want ErrConfigInvalid", err)
	}
}

func TestValidateStackType(t *testing.T) {
	c := &Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "rails", PHP: "8.3", Node: "22"},
	}
	if err := c.Validate(); !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("Validate = %v, want ErrConfigInvalid", err)
	}
}

func TestValidateDevPortOutOfRange(t *testing.T) {
	c := &Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Dev:     DevConfig{Ports: map[string]int{"laravel": -1}},
	}
	if err := c.Validate(); !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("Validate = %v, want ErrConfigInvalid (laravel=-1 out of range)", err)
	}
}

func TestValidateDevPortTooLarge(t *testing.T) {
	c := &Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Dev:     DevConfig{Ports: map[string]int{"laravel": 70000}},
	}
	if err := c.Validate(); !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("Validate = %v, want ErrConfigInvalid (laravel=70000 out of range)", err)
	}
}

func TestValidateDevPortZeroAccepted(t *testing.T) {
	c := &Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Dev:     DevConfig{Ports: map[string]int{"laravel": 0}},
	}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate(laravel=0) = %v, want nil (0 means 'don't expose')", err)
	}
}

func TestValidateDevPortRejectsWebserverHTTP(t *testing.T) {
	c := &Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Dev:     DevConfig{Ports: map[string]int{"webserver_http": 80}},
	}
	err := c.Validate()
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("Validate = %v, want ErrConfigInvalid", err)
	}
	if !strings.Contains(err.Error(), "webserver_http") {
		t.Errorf("err = %v, want it to mention 'webserver_http'", err)
	}
}

func TestValidateDeployPortRejectsVite(t *testing.T) {
	c := &Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]DeployConfig{
			"production": {Host: "h", User: "u", Path: "p", Branch: "b", Ports: map[string]int{"vite": 5173}},
		},
	}
	err := c.Validate()
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("Validate = %v, want ErrConfigInvalid", err)
	}
	if !strings.Contains(err.Error(), "vite") {
		t.Errorf("err = %v, want it to mention 'vite'", err)
	}
}

func TestValidateDeployPortRejectsMailpitSMTP(t *testing.T) {
	c := &Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]DeployConfig{
			"production": {Host: "h", User: "u", Path: "p", Branch: "b", Ports: map[string]int{"mailpit_smtp": 1025}},
		},
	}
	if err := c.Validate(); !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("Validate = %v, want ErrConfigInvalid (mailpit_smtp not valid in production)", err)
	}
}

func TestValidateDeployPortAcceptsWebserverHTTP(t *testing.T) {
	c := &Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]DeployConfig{
			"production": {Host: "h", User: "u", Path: "p", Branch: "b", Ports: map[string]int{"webserver_http": 8080}},
		},
	}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate(webserver_http=8080 in production) = %v, want nil", err)
	}
}

func TestDevBindDefaults(t *testing.T) {
	c := &Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if c.Dev.Bind != "127.0.0.1" {
		t.Errorf("Dev.Bind = %q, want %q (default when absent)", c.Dev.Bind, "127.0.0.1")
	}
}

func TestDevBindEmptyStringTreatedAsAbsent(t *testing.T) {
	c := &Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Dev:     DevConfig{Bind: ""},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if c.Dev.Bind != "127.0.0.1" {
		t.Errorf("Dev.Bind = %q, want %q (empty string == absent, apply default)", c.Dev.Bind, "127.0.0.1")
	}
}

func TestDevBindLoopbackAccepted(t *testing.T) {
	c := &Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Dev:     DevConfig{Bind: "127.0.0.1"},
	}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate(bind=127.0.0.1) = %v, want nil", err)
	}
}

func TestDevBindAllInterfacesAccepted(t *testing.T) {
	c := &Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Dev:     DevConfig{Bind: "0.0.0.0"},
	}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate(bind=0.0.0.0) = %v, want nil", err)
	}
}

func TestDevBindRejectsUnknown(t *testing.T) {
	cases := []string{"::", "localhost", "192.168.1.1", "0", "10.0.0.1", "::1", "1.2.3.4"}
	for _, v := range cases {
		c := &Config{
			Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
			Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
			Dev:     DevConfig{Bind: v},
		}
		err := c.Validate()
		if !errors.Is(err, ErrConfigInvalid) {
			t.Errorf("Validate(bind=%q) = %v, want ErrConfigInvalid", v, err)
			continue
		}
		if !strings.Contains(err.Error(), v) {
			t.Errorf("Validate(bind=%q) err = %q, want it to mention the bad value", v, err)
		}
	}
}
