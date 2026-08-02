package deploy

import (
	"context"
	"errors"
	"testing"
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
