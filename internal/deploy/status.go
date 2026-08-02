package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Bonnary/pier/internal/config"
)

// StatusRunner is the subset of *Client that RemoteStatus needs:
// Run to capture command output and Close to release the connection.
// The CLI dials a real client and passes it; tests inject a fake.
type StatusRunner interface {
	Run(ctx context.Context, cmd string) ([]byte, []byte, error)
	Close() error
}

var _ StatusRunner = (*Client)(nil)

// StatusReport is the result of a `pier status <env>` probe of a
// remote host: raw command output for containers and disk, the last
// deploy record, and a single HTTP health verdict.
type StatusReport struct {
	Containers string // `docker compose -f docker-compose.prod.yml ps` output
	Disk       string // `df -h <path>` output
	DockerDisk string // `docker system df` output
	State      *State // parsed .pier/state.json; nil when the file is absent
	Healthy    bool   // single HTTP GET of health.URL returned 2xx
}

// RemoteStatus probes the remote host behind r: container state,
// deploy-path and docker disk usage, the last deploy record, and one
// HTTP health check against health.URL. A missing state file is
// normal (a project with no deploys yet) and yields a nil State. A
// failed health probe sets Healthy=false instead of failing the
// probe; a failed probe command (compose, df, or docker system df)
// returns an error. For the state file, a failed `cat` (e.g. no
// deploys yet) yields a nil State; output that cannot be parsed
// returns an error.
func RemoteStatus(ctx context.Context, de config.DeployConfig, health HealthConfig, r StatusRunner) (*StatusReport, error) {
	rep := &StatusReport{}

	out, _, err := r.Run(ctx, fmt.Sprintf("cd %s && docker compose -f docker-compose.prod.yml ps", de.Path))
	if err != nil {
		return nil, fmt.Errorf("remote `docker compose ps` failed: %w", err)
	}
	rep.Containers = strings.TrimRight(string(out), "\n")

	out, _, err = r.Run(ctx, fmt.Sprintf("df -h %s", de.Path))
	if err != nil {
		return nil, fmt.Errorf("remote `df -h` failed: %w", err)
	}
	rep.Disk = strings.TrimRight(string(out), "\n")

	out, _, err = r.Run(ctx, "docker system df")
	if err != nil {
		return nil, fmt.Errorf("remote `docker system df` failed: %w", err)
	}
	rep.DockerDisk = strings.TrimRight(string(out), "\n")

	out, _, err = r.Run(ctx, fmt.Sprintf("cat %s", filepath.Join(de.Path, stateFile)))
	if err == nil {
		var s State
		if jerr := json.Unmarshal(out, &s); jerr != nil {
			return nil, fmt.Errorf("remote state.json parse: %w", jerr)
		}
		rep.State = &s
	}

	rep.Healthy = Probe(ctx, health) == nil
	return rep, nil
}
