package deploy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Bonnary/pier/internal/config"
)

func TestProbeSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	cfg := HealthConfig{URL: srv.URL, Timeout: 2 * time.Second, Interval: 100 * time.Millisecond, MaxAttempts: 3}
	if err := Probe(context.Background(), cfg); err != nil {
		t.Errorf("Probe: %v", err)
	}
}

func TestProbeFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	cfg := HealthConfig{URL: srv.URL, Timeout: 1 * time.Second, Interval: 100 * time.Millisecond, MaxAttempts: 2}
	if err := Probe(context.Background(), cfg); err == nil {
		t.Error("Probe = nil error, want non-nil")
	}
}

func TestProbeBackoff(t *testing.T) {
	count := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if count < 2 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	cfg := HealthConfig{URL: srv.URL, Timeout: 3 * time.Second, Interval: 100 * time.Millisecond, MaxAttempts: 5}
	if err := Probe(context.Background(), cfg); err != nil {
		t.Errorf("Probe with retry: %v", err)
	}
	if count < 2 {
		t.Errorf("count = %d, want >= 2", count)
	}
}

func TestDefaultHealthConfig(t *testing.T) {
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "x"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Host: "192.168.1.10", User: "u", Path: "p", Branch: "b", Domain: "x.example.com"}},
	}
	h := DefaultHealthConfig(cfg, "production")
	if h.URL != "https://x.example.com:443/up" {
		t.Errorf("URL = %q, want https://x.example.com:443/up (domain set: probe the domain over https)", h.URL)
	}
	if h.Timeout != 60*time.Second || h.Interval != 2*time.Second || h.MaxAttempts != 30 {
		t.Errorf("DefaultHealthConfig = %+v, want 60s timeout / 2s interval / 30 attempts", h)
	}
}
