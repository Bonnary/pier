package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

func newExecCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "exec <cmd...>",
		Short: "Run a one-off command in the laravel.test container",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("not implemented")
		},
	}
}
