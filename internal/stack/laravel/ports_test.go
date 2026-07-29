package laravel

import (
	"testing"

	"github.com/pcnerd/pier/internal/config"
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

func TestBindAddrDev(t *testing.T) {
	if got := BindAddr("dev"); got != "127.0.0.1" {
		t.Errorf("BindAddr(dev) = %q, want 127.0.0.1", got)
	}
}

func TestBindAddrProd(t *testing.T) {
	if got := BindAddr("production"); got != "" {
		t.Errorf("BindAddr(production) = %q, want \"\" (no bind prefix = 0.0.0.0)", got)
	}
}

func TestBindAddrStaging(t *testing.T) {
	if got := BindAddr("staging"); got != "" {
		t.Errorf("BindAddr(staging) = %q, want \"\"", got)
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
