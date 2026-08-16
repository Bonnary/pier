package deploy

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Bonnary/pier/internal/config"
)

// HealthConfig drives Probe: the URL to GET, the overall Timeout, the
// Interval between attempts, and the MaxAttempts safety cap. The
// defaults come from DefaultHealthConfig.
type HealthConfig struct {
	URL         string
	Timeout     time.Duration
	Interval    time.Duration
	MaxAttempts int
}

// DefaultHealthConfig returns a sensible default HealthConfig for a
// deploy env: GET to the deploy host's web endpoint (scheme and port
// resolved from the env's domain (see HealthURL)), 60 s
// total timeout, 2 s base interval, 30 attempts (interval doubles
// each attempt up to a 10 s cap).
func DefaultHealthConfig(cfg config.Config, env string) HealthConfig {
	return HealthConfig{
		URL:         HealthURL(cfg, env),
		Timeout:     60 * time.Second,
		Interval:    2 * time.Second,
		MaxAttempts: 30,
	}
}

// Probe repeatedly GETs cfg.URL until it returns a 2xx, the Timeout
// elapses, or MaxAttempts is reached. The interval doubles each
// attempt, capped at 10 s. Used as stage 6 of the deploy pipeline; a
// failed probe triggers Rollback.
func Probe(ctx context.Context, cfg HealthConfig) error {
	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(cfg.Timeout)
	backoff := cfg.Interval
	attempt := 0
	for {
		attempt++
		if time.Now().After(deadline) || attempt > cfg.MaxAttempts {
			return fmt.Errorf("deploy: health probe failed after %d attempts", attempt-1)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.URL, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
		backoff *= 2
		if backoff > 10*time.Second {
			backoff = 10 * time.Second
		}
	}
}
