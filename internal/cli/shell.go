package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

func newShellCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "shell",
		Short: "Open an interactive bash in the laravel.test container",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("not implemented")
		},
	}
}
