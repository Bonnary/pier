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
	if got := prod.Domain; got != "myapp.example.com" {
		t.Errorf("Deploy[production].Domain = %q, want myapp.example.com (domain = ... in full-ports.toml)", got)
	}
	if got := prod.ExtraDomains; len(got) != 1 || got[0] != "www.myapp.example.com" {
		t.Errorf("Deploy[production].ExtraDomains = %v, want [www.myapp.example.com]", got)
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
	if got := cfg.DomainForEnv("staging"); got != "myapp.example.com" {
		t.Errorf("DomainForEnv(staging) = %q, want myapp.example.com (no [deploy.staging].domain → inherit [project].domain)", got)
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

func TestLoadHookLists(t *testing.T) {
	cfg, err := Load(filepath.Join("testdata", "hooks.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	prod := cfg.Deploy["production"]
	if len(prod.BeforeDeploy) != 1 || prod.BeforeDeploy[0] != "php artisan down" {
		t.Errorf("BeforeDeploy = %q, want [php artisan down]", prod.BeforeDeploy)
	}
	if len(prod.AfterDeploy) != 2 || prod.AfterDeploy[0] != "php artisan migrate --force" || prod.AfterDeploy[1] != "php artisan cache:clear" {
		t.Errorf("AfterDeploy = %q, want [php artisan migrate --force php artisan cache:clear]", prod.AfterDeploy)
	}
}

func TestValidateHookListAcceptsValid(t *testing.T) {
	c := &Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]DeployConfig{
			"production": {
				Host: "h", User: "u", Path: "p", Branch: "b",
				BeforeDeploy: []string{"php artisan down"},
				AfterDeploy:  []string{"php artisan migrate --force"},
			},
		},
	}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate = %v, want nil", err)
	}
}

func TestValidateHookListRejectsEmptyEntry(t *testing.T) {
	c := &Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]DeployConfig{
			"production": {Host: "h", User: "u", Path: "p", Branch: "b", BeforeDeploy: []string{""}},
		},
	}
	err := c.Validate()
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("Validate = %v, want ErrConfigInvalid", err)
	}
	if !strings.Contains(err.Error(), "before_deploy") {
		t.Errorf("err = %v, want it to mention before_deploy", err)
	}
}

func TestValidateHookListRejectsWhitespaceOnlyEntry(t *testing.T) {
	c := &Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]DeployConfig{
			"production": {Host: "h", User: "u", Path: "p", Branch: "b", AfterDeploy: []string{"   "}},
		},
	}
	err := c.Validate()
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("Validate = %v, want ErrConfigInvalid", err)
	}
	if !strings.Contains(err.Error(), "after_deploy") {
		t.Errorf("err = %v, want it to mention after_deploy", err)
	}
}

func TestValidateHookListRejectsUnterminatedQuote(t *testing.T) {
	c := &Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]DeployConfig{
			"production": {Host: "h", User: "u", Path: "p", Branch: "b", BeforeDeploy: []string{`php "unterminated`}},
		},
	}
	if err := c.Validate(); !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("Validate = %v, want ErrConfigInvalid", err)
	}
}

func TestValidateDeployScaffoldAllowsEmptyHostUserPathBranch(t *testing.T) {
	cfg := Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]DeployConfig{"production": {Services: []string{"redis"}}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate = %v, want nil (unconfigured scaffold)", err)
	}
}

func TestValidateDeployPartialConfigStillRejected(t *testing.T) {
	cfg := Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]DeployConfig{"production": {Host: "h"}},
	}
	err := cfg.Validate()
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("Validate = %v, want ErrConfigInvalid", err)
	}
	if !strings.Contains(err.Error(), "requires host, user, path, branch") {
		t.Errorf("err = %v, want substring %q", err, "requires host, user, path, branch")
	}
}

func TestServicesForEnv(t *testing.T) {
	cfg := Config{
		Stack: StackConfig{Services: []string{"redis", "mailpit"}},
		Deploy: map[string]DeployConfig{
			"prod":  {Services: []string{"postgres"}},
			"stage": {}, // no services key → inherit
		},
	}
	if got := cfg.ServicesForEnv("prod"); len(got) != 1 || got[0] != "postgres" {
		t.Errorf(`ServicesForEnv("prod") = %v, want ["postgres"]`, got)
	}
	if got := cfg.ServicesForEnv("stage"); len(got) != 2 || got[0] != "redis" {
		t.Errorf(`ServicesForEnv("stage") = %v, want ["redis" "mailpit"]`, got)
	}
	if got := cfg.ServicesForEnv("nonexistent"); len(got) != 2 {
		t.Errorf(`ServicesForEnv("nonexistent") = %v, want stack services`, got)
	}
}

func TestServicesForEnvExplicitEmpty(t *testing.T) {
	cfg := Config{
		Stack:  StackConfig{Services: []string{"redis"}},
		Deploy: map[string]DeployConfig{"prod": {Services: []string{}}},
	}
	if got := cfg.ServicesForEnv("prod"); len(got) != 0 {
		t.Errorf(`ServicesForEnv("prod") = %v, want [] (explicit empty overrides inherit)`, got)
	}
}

func TestValidateBuilderModes(t *testing.T) {
	base := &Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]DeployConfig{
			"production": {Host: "h", User: "u", Path: "/srv/x", Branch: "main"},
		},
	}
	// Every valid builder value passes.
	for _, b := range []string{"host_server", "local_machine", "build_server"} {
		c := *base
		dc := c.Deploy["production"]
		dc.Builder = b
		if b == "build_server" {
			dc.BuildHost, dc.BuildUser, dc.BuildPath = "bh", "bu", "/srv/build"
		}
		c.Deploy["production"] = dc
		if err := c.Validate(); err != nil {
			t.Errorf("builder %q: Validate() = %v, want nil", b, err)
		}
	}
	// *base shares the Deploy map with the loop copies above, so
	// restore the pristine entry before the negative checks below.
	base.Deploy["production"] = DeployConfig{Host: "h", User: "u", Path: "/srv/x", Branch: "main"}
	// Unknown builder value is rejected.
	c := *base
	dc := c.Deploy["production"]
	dc.Builder = "spaceship"
	c.Deploy["production"] = dc
	if err := c.Validate(); err == nil {
		t.Error("builder = spaceship: Validate() = nil, want invalid-config error")
	}
	// build_server without build_* fields is rejected.
	c = *base
	dc = c.Deploy["production"]
	dc.Builder = "build_server"
	c.Deploy["production"] = dc
	if err := c.Validate(); err == nil {
		t.Error("builder = build_server with no build_* fields: Validate() = nil, want invalid-config error")
	}
	// Absent builder defaults to host_server and stays valid.
	base.Deploy["production"] = DeployConfig{Host: "h", User: "u", Path: "/srv/x", Branch: "main"}
	if err := base.Validate(); err != nil {
		t.Errorf("absent builder: Validate() = %v, want nil", err)
	}
}

func TestBuilderModeDefaultsToHostServer(t *testing.T) {
	var dc DeployConfig
	if got := dc.BuilderMode(); got != "host_server" {
		t.Errorf("BuilderMode() = %q, want host_server", got)
	}
	dc.Builder = "build_server"
	if got := dc.BuilderMode(); got != "build_server" {
		t.Errorf("BuilderMode() = %q, want build_server", got)
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

func TestQueueWorkersDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join("testdata", "minimal.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.QueueWorkers(); got != DefaultQueueWorkers {
		t.Errorf("QueueWorkers() = %d, want %d (absent key must default)", got, DefaultQueueWorkers)
	}
	if got := cfg.QueueWorkersForEnv("production"); got != DefaultQueueWorkers {
		t.Errorf("QueueWorkersForEnv(production) = %d, want %d (absent key must default)", got, DefaultQueueWorkers)
	}
}

func TestQueueWorkersEnvOverride(t *testing.T) {
	c := &Config{
		Stack: StackConfig{Type: "laravel", PHP: "8.3", Node: "22", QueueWorkers: 4},
		Deploy: map[string]DeployConfig{
			"production": {QueueWorkers: 8},
			"staging":    {},
		},
	}
	if got := c.QueueWorkersForEnv("production"); got != 8 {
		t.Errorf("QueueWorkersForEnv(production) = %d, want 8 (explicit env override wins)", got)
	}
	if got := c.QueueWorkersForEnv("staging"); got != 4 {
		t.Errorf("QueueWorkersForEnv(staging) = %d, want 4 (absent env override inherits stack)", got)
	}
	if got := c.QueueWorkers(); got != 4 {
		t.Errorf("QueueWorkers() = %d, want 4", got)
	}
}

func TestValidateQueueWorkers(t *testing.T) {
	valid := func(qw int) *Config {
		return &Config{
			Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
			Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22", QueueWorkers: qw},
		}
	}
	if err := valid(0).Validate(); err != nil {
		t.Errorf("Validate(queue_workers=0) = %v, want nil (0 = default)", err)
	}
	if err := valid(MaxQueueWorkers).Validate(); err != nil {
		t.Errorf("Validate(queue_workers=%d) = %v, want nil", MaxQueueWorkers, err)
	}
	for _, bad := range []int{-1, MaxQueueWorkers + 1} {
		if err := valid(bad).Validate(); !errors.Is(err, ErrConfigInvalid) {
			t.Errorf("Validate(stack queue_workers=%d) = %v, want ErrConfigInvalid", bad, err)
		}
	}
	c := valid(1)
	c.Deploy = map[string]DeployConfig{"production": {QueueWorkers: MaxQueueWorkers + 1}}
	if err := c.Validate(); !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("Validate(deploy queue_workers=%d) = %v, want ErrConfigInvalid", MaxQueueWorkers+1, err)
	}
}

func TestDomainForEnv(t *testing.T) {
	cfg := Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Deploy: map[string]DeployConfig{
			"prod":  {Domain: "prod.example.com"},
			"stage": {},
		},
	}
	if got := cfg.DomainForEnv("prod"); got != "prod.example.com" {
		t.Errorf(`DomainForEnv("prod") = %q, want prod.example.com (env override wins)`, got)
	}
	if got := cfg.DomainForEnv("stage"); got != "x.example.com" {
		t.Errorf(`DomainForEnv("stage") = %q, want x.example.com (inherit project domain)`, got)
	}
	if got := cfg.DomainForEnv("missing"); got != "x.example.com" {
		t.Errorf(`DomainForEnv("missing") = %q, want x.example.com`, got)
	}
	empty := Config{Project: ProjectConfig{Name: "x"}}
	if got := empty.DomainForEnv("prod"); got != "" {
		t.Errorf("DomainForEnv(prod) = %q, want \"\" (no domain anywhere = plain HTTP)", got)
	}
}

func TestValidateEmptyProjectDomainAllowed(t *testing.T) {
	c := &Config{
		Project: ProjectConfig{Name: "x"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
	}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate(empty domain) = %v, want nil (empty domain = plain HTTP by IP)", err)
	}
}

func TestValidateDomainSyntax(t *testing.T) {
	cases := []struct {
		name  string
		field string
	}{
		{"project domain with scheme", "https://myapp.example.com"},
		{"project domain with port", "myapp.example.com:8443"},
		{"project domain with path", "myapp.example.com/app"},
	}
	for _, c := range cases {
		cfg := &Config{
			Project: ProjectConfig{Name: "x", Domain: c.field},
			Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		}
		err := cfg.Validate()
		if !errors.Is(err, ErrConfigInvalid) {
			t.Errorf("%s: Validate = %v, want ErrConfigInvalid", c.name, err)
			continue
		}
		if !strings.Contains(err.Error(), c.field) {
			t.Errorf("%s: err = %q, want it to mention the bad domain", c.name, err)
		}
	}
}

func TestValidateDeployDomainSyntax(t *testing.T) {
	c := &Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]DeployConfig{
			"production": {Host: "h", User: "u", Path: "p", Branch: "b", Domain: "https://prod.example.com"},
		},
	}
	err := c.Validate()
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("Validate = %v, want ErrConfigInvalid", err)
	}
	if !strings.Contains(err.Error(), "deploy.production.domain") {
		t.Errorf("err = %q, want it to mention deploy.production.domain", err)
	}
}

func TestValidateExtraDomains(t *testing.T) {
	base := func(extra []string) *Config {
		return &Config{
			Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
			Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
			Deploy: map[string]DeployConfig{
				"production": {Host: "h", User: "u", Path: "p", Branch: "b", ExtraDomains: extra},
			},
		}
	}
	if err := base([]string{"www.x.example.com"}).Validate(); err != nil {
		t.Errorf("Validate(valid extra_domains) = %v, want nil", err)
	}
	if err := base([]string{"bad domain"}).Validate(); !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("Validate(extra_domains with space) = %v, want ErrConfigInvalid", err)
	}
	if err := base([]string{"www.x.example.com", "www.x.example.com"}).Validate(); !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("Validate(duplicate extra_domains) = %v, want ErrConfigInvalid", err)
	}
	if err := base([]string{"x.example.com"}).Validate(); !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("Validate(extra_domains containing primary) = %v, want ErrConfigInvalid", err)
	}
}
