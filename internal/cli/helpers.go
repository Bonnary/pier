package cli

import (
	"fmt"
	"os"

	"golang.org/x/term"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/deploy"
)

func cliError(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

func osGetenv(k string) string       { return os.Getenv(k) }
func osUserHomeDir() (string, error) { return os.UserHomeDir() }

// readPassword prompts on stderr (so --json stdout stays clean)
// with echo disabled and returns the entered password. The prompt
// goes to stderr because it is not part of the command's output.
func readPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return string(b), nil
}

// newSSHConfig builds the SSHConfig for a deploy env: target host
// and user from [deploy.<env>], key path from sshKeyPath, and an
// interactive password prompt that Dial falls back to when the
// server rejects the key. The password is never stored.
func newSSHConfig(dc config.DeployConfig) deploy.SSHConfig {
	return deploy.SSHConfig{
		Host:    dc.Host,
		User:    dc.User,
		KeyPath: sshKeyPath(),
		PasswordPrompt: func() (string, error) {
			return readPassword(fmt.Sprintf("SSH password for %s@%s: ", dc.User, dc.Host))
		},
	}
}
