package deploy

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

type HealthConfig struct {
	URL         string
	Timeout     time.Duration
	Interval    time.Duration
	MaxAttempts int
}

func DefaultHealthConfig(domain string) HealthConfig {
	return HealthConfig{
		URL:         fmt.Sprintf("https://%s/up", domain),
		Timeout:     60 * time.Second,
		Interval:    2 * time.Second,
		MaxAttempts: 30,
	}
}

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
