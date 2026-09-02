package deploy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type fakeNetAddr struct{}

func (fakeNetAddr) Network() string { return "tcp" }
func (fakeNetAddr) String() string  { return "example.com:22" }

// TestTOFUHostKeyRecordsFirstAndRejectsMismatch drives the TOFU host
// key verifier (F3): an unknown host is accepted and recorded on first
// contact; the same key is accepted thereafter; a different key for the
// same host is rejected (MITM).
func TestTOFUHostKeyRecordsFirstAndRejectsMismatch(t *testing.T) {
	kh := filepath.Join(t.TempDir(), "known_hosts")
	cb, err := tofuHostKeyCallback(kh)
	if err != nil {
		t.Fatalf("tofuHostKeyCallback: %v", err)
	}
	addr := fakeNetAddr{}
	_, key1 := writeTestKey(t)
	_, key2 := writeTestKey(t)
	host := "example.com:22"

	if err := cb(host, addr, key1); err != nil {
		t.Fatalf("first contact (unknown host) = %v, want accept-and-record", err)
	}
	if err := cb(host, addr, key1); err != nil {
		t.Fatalf("reconnect with recorded key = %v, want accepted", err)
	}
	if err := cb(host, addr, key2); err == nil {
		t.Fatal("different key for known host = nil, want rejection (MITM)")
	}
}

// TestDialWithKnownHostsTOFU drives the production path end to end:
// Dial with a KnownHostsPath verifies the remote host trust-on-first-
// use, recording the key on first contact and re-verifying it on a
// second connection to the same server.
func TestDialWithKnownHostsTOFU(t *testing.T) {
	addr := startSSHServer(t, passwordOnlyServer())
	host, port := testAddr(t, addr)
	kh := filepath.Join(t.TempDir(), "known_hosts")

	cfg := SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath:        writeTestKeyPath(t),
		Password:       "secret",
		KnownHostsPath: kh,
	}
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Dial with TOFU: %v", err)
	}
	c.Close()
	data, err := os.ReadFile(kh)
	if err != nil || len(data) == 0 {
		t.Fatalf("known_hosts not recorded: %q (err %v)", data, err)
	}

	c2, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatalf("second Dial with TOFU: %v", err)
	}
	c2.Close()
}
