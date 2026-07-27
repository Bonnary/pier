//go:build integration

package deploy

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/pcnerd/pier/internal/cli"
	"github.com/pcnerd/pier/internal/config"
)

func TestPipelineEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	// Spin up a Linux container with sshd + docker.
	req := testcontainers.ContainerRequest{
		Image:        "linuxserver/openssh-server:latest",
		ExposedPorts: []string{"22/tcp"},
		WaitingFor:   wait.NewLogStrategy("Server listening on").WithStartupTimeout(60 * time.Second),
	}
	// (Full test container setup is non-trivial; the testcontainers pattern
	//  is well-documented. Engineer fills in env, port mapping, and key push.)
	_ = req
	t.Skip("engineer: implement testcontainer SSH + docker host end-to-end test")
	_ = ctx
	_ = cli.NewLogger
	_ = config.Config{}
}
