package laravel

import (
	"testing"

	"github.com/Bonnary/pier/internal/config"
)

func TestResolvePortOverride(t *testing.T) {
	host, ok := ResolvePort("laravel", map[string]int{"laravel": 8080}, DevPortDefaults)
	if !ok {
		t.Fatal("ok = false, want true (override != 0)")
	}
	if host != 8080 {
		t.Errorf("host = %d, want 8080", host)
	}
}

func TestResolvePortDefault(t *testing.T) {
	host, ok := ResolvePort("laravel", nil, DevPortDefaults)
	if !ok {
		t.Fatal("ok = false, want true (default != 0)")
	}
	if host != 8000 {
		t.Errorf("host = %d, want 8000 (default)", host)
	}
}

func TestResolvePortZeroOptOut(t *testing.T) {
	_, ok := ResolvePort("laravel", map[string]int{"laravel": 0}, DevPortDefaults)
	if ok {
		t.Error("ok = true, want false (override 0 = don't expose)")
	}
}

func TestResolvePortUnknownKey(t *testing.T) {
	_, ok := ResolvePort("nonexistent", nil, DevPortDefaults)
	if ok {
		t.Error("ok = true, want false (unknown key with no default)")
	}
}

func TestBindAddrReturnsConfig(t *testing.T) {
	cases := []struct {
		bind string
		want string
	}{
		{"127.0.0.1", "127.0.0.1"},
		{"0.0.0.0", "0.0.0.0"},
		{"", config.DefaultDevBind},
	}
	for _, c := range cases {
		if got := BindAddr(c.bind); got != c.want {
			t.Errorf("BindAddr(%q) = %q, want %q", c.bind, got, c.want)
		}
	}
}

func TestPortBinding(t *testing.T) {
	cases := []struct {
		bind      string
		host      int
		container int
		want      string
	}{
		{"", 8000, 8000, "8000:8000"},
		{"127.0.0.1", 8000, 8000, "127.0.0.1:8000:8000"},
		{"0.0.0.0", 8000, 8000, "0.0.0.0:8000:8000"},
		{"127.0.0.1", 5173, 5173, "127.0.0.1:5173:5173"},
		{"0.0.0.0", 6379, 6379, "0.0.0.0:6379:6379"},
	}
	for _, c := range cases {
		if got := PortBinding(c.bind, c.host, c.container); got != c.want {
			t.Errorf("PortBinding(%q, %d, %d) = %q, want %q", c.bind, c.host, c.container, got, c.want)
		}
	}
}

func TestDevPortDefaultsComplete(t *testing.T) {
	want := map[string]int{
		"laravel":      8000,
		"vite":         5173,
		"mysql":        3306,
		"postgres":     5432,
		"redis":        6379,
		"meilisearch":  7700,
		"mailpit_smtp": 1025,
		"mailpit_ui":   8025,
		"s3_api":       8333,
		"s3_filer":     8888,
		"s3_master":    9333,
	}
	for k, v := range want {
		if got := DevPortDefaults[k]; got != v {
			t.Errorf("DevPortDefaults[%s] = %d, want %d", k, got, v)
		}
	}
}

func TestProdPortDefaultsComplete(t *testing.T) {
	want := map[string]int{
		"laravel":        443,
		"webserver_http": 80,
		"mysql":          3306,
		"postgres":       5432,
		"redis":          6379,
		"meilisearch":    7700,
		"s3_api":         8333,
		"s3_filer":       8888,
		"s3_master":      9333,
	}
	for k, v := range want {
		if got := ProdPortDefaults[k]; got != v {
			t.Errorf("ProdPortDefaults[%s] = %d, want %d", k, got, v)
		}
	}
}

func TestCollectHostPortsFromCompose(t *testing.T) {
	yaml := []byte(`
services:
  laravel.test:
    ports:
      - "127.0.0.1:8000:8000"
      - "127.0.0.1:5173:5173"
  redis:
    ports:
      - "127.0.0.1:6379:6379"
`)
	hostPorts, err := CollectHostPorts(yaml, nil)
	if err != nil {
		t.Fatalf("CollectHostPorts: %v", err)
	}
	want := map[int]bool{8000: false, 5173: false, 6379: false}
	for _, p := range hostPorts {
		if _, ok := want[p]; ok {
			want[p] = true
		}
	}
	for p, found := range want {
		if !found {
			t.Errorf("CollectHostPorts missing host port %d; got %v", p, hostPorts)
		}
	}
}

func TestCollectHostPortsIncludesUserDevServices(t *testing.T) {
	yaml := []byte(`
services:
  laravel.test:
    ports:
      - "127.0.0.1:8000:8000"
`)
	users := map[string]config.DevService{
		"log-viewer": {Image: "x", Ports: []string{"127.0.0.1:8081:8080"}},
	}
	hostPorts, err := CollectHostPorts(yaml, users)
	if err != nil {
		t.Fatalf("CollectHostPorts: %v", err)
	}
	found := false
	for _, p := range hostPorts {
		if p == 8081 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("CollectHostPorts = %v, want it to include 8081 from user-defined dev service", hostPorts)
	}
}

func TestWebScheme(t *testing.T) {
	base := func(tls bool) config.Config {
		return config.Config{
			Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
			Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
			Deploy:  map[string]config.DeployConfig{"production": {TLS: tls}},
		}
	}
	if got := WebScheme(base(false), "production"); got != "http" {
		t.Errorf("WebScheme(tls=false) = %q, want http", got)
	}
	if got := WebScheme(base(true), "production"); got != "https" {
		t.Errorf("WebScheme(tls=true) = %q, want https", got)
	}
	if got := WebScheme(config.Config{}, "missing"); got != "http" {
		t.Errorf("WebScheme(missing env) = %q, want http (zero-value default)", got)
	}
}

func TestWebPort(t *testing.T) {
	cfgWith := func(tls bool, ports map[string]int) config.Config {
		return config.Config{
			Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
			Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
			Deploy:  map[string]config.DeployConfig{"production": {TLS: tls, Ports: ports}},
		}
	}
	cases := []struct {
		name string
		cfg  config.Config
		want int
	}{
		{"no tls, no override", cfgWith(false, nil), 80},
		{"no tls, override 8383", cfgWith(false, map[string]int{"laravel": 8383}), 8383},
		{"tls, no override", cfgWith(true, nil), 443},
		{"tls, override 8443", cfgWith(true, map[string]int{"laravel": 8443}), 8443},
		{"laravel=0 falls back to default", cfgWith(false, map[string]int{"laravel": 0}), 80},
	}
	for _, c := range cases {
		if got := WebPort(c.cfg, "production"); got != c.want {
			t.Errorf("%s: WebPort = %d, want %d", c.name, got, c.want)
		}
	}
}
