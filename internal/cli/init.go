package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

func newInitCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "init [path]",
		Short: "Initialize a new pier project",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("not implemented")
		},
	}
}
