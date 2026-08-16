package deploy

import (
	"fmt"

	"github.com/Bonnary/pier/internal/config"
)

// checkDomainDNS verifies that the env's domain resolves
// to the deploy host — the precondition for Caddy's ACME HTTP-01
// challenge, which proves ownership by answering a token on port 80
// of the domain (a request that only reaches the server when the
// domain's A record points at it). A missing or mismatched record
// fails preflight with an actionable hint instead of a confusing
// certificate error minutes into the deploy. Envs without a domain
// (plain HTTP) are skipped. The host itself resolving is optional
// (it may live only in the SSH config); the health probe surfaces
// any real reachability problem.
func checkDomainDNS(cfg config.Config, env string) error {
	dc := cfg.Deploy[env]
	if dc.Domain == "" {
		return nil
	}
	domain := dc.Domain
	domainIPs, err := lookupHost(domain)
	if err != nil || len(domainIPs) == 0 {
		return fmt.Errorf(
			"domain %s does not resolve — point an A record for %s at the deploy host IP (%s), wait for DNS propagation, then re-deploy",
			domain, domain, dc.Host)
	}
	hostIPs, err := lookupHost(dc.Host)
	if err != nil || len(hostIPs) == 0 {
		return nil
	}
	for _, dip := range domainIPs {
		for _, hip := range hostIPs {
			if dip == hip {
				return nil
			}
		}
	}
	return fmt.Errorf(
		"domain %s resolves to %v, but the deploy host %s resolves to %v — point an A record for %s at the deploy host IP, wait for DNS propagation, then re-deploy",
		domain, domainIPs, dc.Host, hostIPs, domain)
}
