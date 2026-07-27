package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

func newDeployCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "deploy <env>",
		Short: "Deploy the project to <env>",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("not implemented")
		},
	}
}
