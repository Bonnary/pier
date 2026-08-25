package cli

import "github.com/Bonnary/pier/internal/docker"

var dockerRunner docker.Runner = docker.ExecRunner{}
