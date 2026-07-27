package cli

import (
	"fmt"
	"os"
)

func cliError(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

func osGetenv(k string) string       { return os.Getenv(k) }
func osUserHomeDir() (string, error) { return os.UserHomeDir() }
