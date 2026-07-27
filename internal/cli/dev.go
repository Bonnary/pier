package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

func newDevCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "dev [services...]",
		Short: "Bring up the dev Docker stack",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("not implemented")
		},
	}
}
