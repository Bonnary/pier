package deploy

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestDialPasswordAuthSucceeds(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	addr := startSSHServer(t, passwordOnlyServer())
	host, port := testAddr(t, addr)
	c, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath:  keyPath,
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("Dial(key rejected + password) = %v, want success", err)
	}
	c.Close()
}

func TestDialPasswordWrongPasswordFails(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	addr := startSSHServer(t, passwordOnlyServer())
	host, port := testAddr(t, addr)
	_, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath:  keyPath,
		Password: "wrong",
	})
	if !errors.Is(err, ErrPreflight) {
		t.Fatalf("Dial(wrong password) = %v, want ErrPreflight", err)
	}
}

func TestDialNoPasswordSourceFails(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	addr := startSSHServer(t, passwordOnlyServer())
	host, port := testAddr(t, addr)
	_, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath: keyPath,
	})
	if !errors.Is(err, ErrPreflight) {
		t.Fatalf("Dial(no password source) = %v, want ErrPreflight", err)
	}
}

func TestDialMissingKeyFileUsesPassword(t *testing.T) {
	addr := startSSHServer(t, passwordOnlyServer())
	host, port := testAddr(t, addr)
	c, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath:  "/nonexistent/key",
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("Dial(missing key + password) = %v, want success", err)
	}
	c.Close()
}

func TestDialUnreadableKeyFailsFast(t *testing.T) {
	addr := startSSHServer(t, passwordOnlyServer())
	host, port := testAddr(t, addr)
	// A directory as the key path makes os.ReadFile return EISDIR
	// deterministically (0o000 perms are still readable as root).
	_, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath:  t.TempDir(),
		Password: "secret",
	})
	if !errors.Is(err, ErrPreflight) {
		t.Fatalf("Dial(unreadable key + password) = %v, want ErrPreflight, no password fallback", err)
	}
}

func TestDialKeyAuthStillWorks(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	addr := startSSHServer(t, keyOnlyServer(pub))
	host, port := testAddr(t, addr)
	c, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath: keyPath,
	})
	if err != nil {
		t.Fatalf("Dial(key) = %v, want success", err)
	}
	c.Close()
}

func TestDialPromptFallback(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	addr := startSSHServer(t, passwordOnlyServer())
	host, port := testAddr(t, addr)
	called := 0
	c, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath: keyPath,
		PasswordPrompt: func() (string, error) {
			called++
			return "secret", nil
		},
	})
	if err != nil {
		t.Fatalf("Dial(prompt) = %v, want success", err)
	}
	c.Close()
	if called != 1 {
		t.Errorf("prompt called %d times, want 1", called)
	}
}

func TestDialPromptNotCalledWhenKeyWorks(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	addr := startSSHServer(t, keyOnlyServer(pub))
	host, port := testAddr(t, addr)
	called := 0
	c, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath: keyPath,
		PasswordPrompt: func() (string, error) {
			called++
			return "secret", nil
		},
	})
	if err != nil {
		t.Fatalf("Dial(key) = %v, want success", err)
	}
	c.Close()
	if called != 0 {
		t.Errorf("prompt called %d times, want 0", called)
	}
}

func TestDialPromptAbort(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	addr := startSSHServer(t, passwordOnlyServer())
	host, port := testAddr(t, addr)
	_, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath: keyPath,
		PasswordPrompt: func() (string, error) {
			return "", errors.New("interrupted")
		},
	})
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("Dial(aborted prompt) = %v, want ErrAborted", err)
	}
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != ExitAborted {
		t.Fatalf("Dial(aborted prompt) = %T, want *ExitError with code %d", err, ExitAborted)
	}
}

// keyboardOnlyServer returns a ServerConfig that only accepts
// keyboard-interactive auth for user "deploy" with password "secret".
func keyboardOnlyServer() *ssh.ServerConfig {
	sc := &ssh.ServerConfig{
		KeyboardInteractiveCallback: func(c ssh.ConnMetadata, challenges ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			if c.User() != "deploy" {
				return nil, errAuthFailed
			}
			answers, err := challenges("", "", []string{"Password: "}, []bool{false})
			if err != nil || len(answers) != 1 || answers[0] != "secret" {
				return nil, errAuthFailed
			}
			return nil, nil
		},
	}
	sc.SetDefaults()
	return sc
}

func TestDialKeyboardInteractive(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	addr := startSSHServer(t, keyboardOnlyServer())
	host, port := testAddr(t, addr)
	c, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath:  keyPath,
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("Dial(keyboard-interactive) = %v, want success", err)
	}
	c.Close()
}
