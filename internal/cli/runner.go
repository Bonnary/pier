package cli

import "github.com/pcnerd/pier/internal/docker"

var dockerRunner docker.Runner = docker.ExecRunner{}
