package cli

import (
	"testing"

	"github.com/Bonnary/pier/internal/config"
)

func TestNewSSHConfigPorts(t *testing.T) {
	cases := []struct {
		name string
		dc   config.DeployConfig
		want int
	}{
		{"unset port stays 0 (Dial defaults to 22)", config.DeployConfig{Host: "h", User: "u"}, 0},
		{"explicit port", config.DeployConfig{Host: "h", User: "u", Port: 8282}, 8282},
	}
	for _, tc := range cases {
		cfg := newSSHConfig(tc.dc)
		if cfg.Port != tc.want {
			t.Errorf("%s: newSSHConfig.Port = %d, want %d", tc.name, cfg.Port, tc.want)
		}
	}

	buildCases := []struct {
		name string
		dc   config.DeployConfig
		want int
	}{
		{"unset build_port stays 0 (Dial defaults to 22)", config.DeployConfig{}, 0},
		{"explicit build_port", config.DeployConfig{BuildPort: 8822}, 8822},
	}
	for _, tc := range buildCases {
		cfg := newBuildSSHConfig(tc.dc)
		if cfg.Port != tc.want {
			t.Errorf("%s: newBuildSSHConfig.Port = %d, want %d", tc.name, cfg.Port, tc.want)
		}
	}
}
