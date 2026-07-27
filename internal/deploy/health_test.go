package deploy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
