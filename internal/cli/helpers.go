package cli

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

func cliError(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

func osGetenv(k string) string       { return os.Getenv(k) }
func osUserHomeDir() (string, error) { return os.UserHomeDir() }

// readSudoPassword prompts on stderr (so --json stdout stays clean)
// with echo disabled and returns the entered password. The prompt
// goes to stderr because it is not part of the command's output.
func readSudoPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return string(b), nil
}
