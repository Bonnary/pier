package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

func newRollbackCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "rollback <env>",
		Short: "Roll back <env> to the previously deployed image",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("not implemented")
		},
	}
}
