package deploy

import (
	"errors"
	"strings"
	"testing"

	"github.com/Bonnary/pier/internal/config"
)

// pinDNS makes the DNS seam deterministic for a test: resolve maps
// hostnames to IP sets; unknown hosts fail resolution.
func pinDNS(t *testing.T, resolve map[string][]string) {
	t.Helper()
	old := lookupHost
	lookupHost = func(host string) ([]string, error) {
		if ips, ok := resolve[host]; ok {
			return ips, nil
		}
		return nil, errors.New("no such host")
	}
	t.Cleanup(func() { lookupHost = old })
}

func TestCheckDomainDNSSkipsWithoutDomain(t *testing.T) {
	pinDNS(t, map[string][]string{})
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "x"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Host: "1.2.3.4", User: "u", Path: "p", Branch: "b"}},
	}
	if err := checkDomainDNS(cfg, "production"); err != nil {
		t.Errorf("checkDomainDNS(no domain) = %v, want nil (plain HTTP needs no DNS)", err)
	}
}

func TestCheckDomainDNSFailsWhenDomainDoesNotResolve(t *testing.T) {
	pinDNS(t, map[string][]string{"1.2.3.4": {"1.2.3.4"}})
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Host: "1.2.3.4", User: "u", Path: "p", Branch: "b"}},
	}
	err := checkDomainDNS(cfg, "production")
	if err == nil {
		t.Fatal("checkDomainDNS = nil, want DNS hint error (ACME HTTP-01 needs the A record)")
	}
	if !strings.Contains(err.Error(), "point an A record") {
		t.Errorf("err = %q, want the actionable A-record hint", err)
	}
	if !strings.Contains(err.Error(), "myapp.example.com") {
		t.Errorf("err = %q, want it to name the domain", err)
	}
}

func TestCheckDomainDNSFailsOnMismatch(t *testing.T) {
	pinDNS(t, map[string][]string{
		"myapp.example.com": {"5.6.7.8"},
		"1.2.3.4":           {"1.2.3.4"},
	})
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Host: "1.2.3.4", User: "u", Path: "p", Branch: "b"}},
	}
	err := checkDomainDNS(cfg, "production")
	if err == nil || !strings.Contains(err.Error(), "resolves to") {
		t.Fatalf("checkDomainDNS = %v, want mismatch error naming both IP sets", err)
	}
}

func TestCheckDomainDNSPassesOnMatch(t *testing.T) {
	pinDNS(t, map[string][]string{
		"myapp.example.com": {"1.2.3.4"},
		"1.2.3.4":           {"1.2.3.4"},
	})
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": {Host: "1.2.3.4", User: "u", Path: "p", Branch: "b"}},
	}
	if err := checkDomainDNS(cfg, "production"); err != nil {
		t.Errorf("checkDomainDNS(match) = %v, want nil", err)
	}
}
