package cli

import (
	"io"

	"github.com/spf13/cobra"
)

func newServiceCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "service",
		Short: "Add or remove services from pier.toml",
	}
}
