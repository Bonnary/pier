package deploy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestSFTPStateStoreRoundTrip drives the remote state store against
// the in-process SSH/SFTP server: commit writes .pier/state.json on
// the remote path, the store reads it back, and a second write chains
// the previous tag.
func TestSFTPStateStoreRoundTrip(t *testing.T) {
	addr := startSSHServer(t, passwordOnlyServer())
	host, port := testAddr(t, addr)
	remote := t.TempDir()

	c, err := Dial(context.Background(), SSHConfig{
		Host: host, User: "deploy", Port: port,
		KeyPath:  writeTestKeyPath(t),
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	st := SFTPStateStore{Client: c}
	ctx := context.Background()

	got, err := st.ReadState(ctx, remote)
	if err != nil {
		t.Fatalf("ReadState (absent): %v", err)
	}
	if got != nil {
		t.Fatalf("ReadState (absent) = %+v, want nil", got)
	}

	if err := st.WriteState(ctx, remote, &State{Current: "abc", DeployedAt: "t1", DeployedBy: "u@h"}); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	got, err = st.ReadState(ctx, remote)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got == nil || got.Current != "abc" || got.DeployedAt != "t1" {
		t.Fatalf("ReadState = %+v, want Current=abc", got)
	}

	if err := st.WriteState(ctx, remote, &State{Current: "def", Previous: "abc", DeployedAt: "t2", DeployedBy: "u@h"}); err != nil {
		t.Fatalf("WriteState (2nd): %v", err)
	}
	got, err = st.ReadState(ctx, remote)
	if err != nil {
		t.Fatalf("ReadState (2nd): %v", err)
	}
	if got == nil || got.Current != "def" || got.Previous != "abc" {
		t.Fatalf("ReadState (2nd) = %+v, want Current=def Previous=abc", got)
	}

	if _, err := os.Stat(filepath.Join(remote, ".pier", "state.json")); err != nil {
		t.Fatalf("state.json missing on remote: %v", err)
	}
	if _, err := os.Stat(filepath.Join(remote, ".pier", "state.json.tmp")); !os.IsNotExist(err) {
		t.Fatalf("state.json.tmp left behind (rename not atomic?): %v", err)
	}
}
