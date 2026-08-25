package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// stateFile is the per-project deploy record, relative to the remote
// project root. Carries the active and previous image tags so
// Rollback knows what to retag.
const stateFile = ".pier/state.json"

// State is the parsed .pier/state.json from a remote project. The
// JSON tags are the wire format; do not rename without a migration.
type State struct {
	Current    string `json:"current"`
	Previous   string `json:"previous"`
	DeployedAt string `json:"deployed_at"`
	DeployedBy string `json:"deployed_by"`
}

// HasPrevious reports whether State carries a non-empty Previous
// image tag. Rollback short-circuits to "no previous deploy" when
// this is false.
func (s *State) HasPrevious() bool { return s.Previous != "" }

// LoadState reads .pier/state.json from dir and decodes it. Returns
// (nil, nil) when the file does not exist (a fresh project, no
// deploys yet) so the caller can treat "no state" as a normal
// condition.
func LoadState(dir string) (*State, error) {
	path := filepath.Join(dir, stateFile)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("deploy: read state: %w", err)
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("deploy: parse state: %w", err)
	}
	return &s, nil
}

// SaveState atomically writes s to .pier/state.json in dir. The
// .pier/ directory is created with 0755 if missing; the file is
// written to a sibling .tmp first and renamed into place so a crash
// mid-write can't corrupt the deploy record.
func SaveState(dir string, s *State) error {
	dirPath := filepath.Join(dir, ".pier")
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return fmt.Errorf("deploy: mkdir .pier: %w", err)
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dirPath, "state.json.tmp")
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return fmt.Errorf("deploy: write tmp state: %w", err)
	}
	return os.Rename(tmp, filepath.Join(dirPath, "state.json"))
}

// stateStore abstracts .pier/state.json access on a deploy host. The
// real pipeline uses sftpStateStore over the deploy SSH connection;
// tests use localStateStore against a temp dir.
type stateStore interface {
	ReadState(ctx context.Context, dir string) (*State, error)
	WriteState(ctx context.Context, dir string, s *State) error
}

// localStateStore reads and writes .pier/state.json with local os
// calls (unit tests).
type localStateStore struct{}

func (localStateStore) ReadState(ctx context.Context, dir string) (*State, error) {
	return LoadState(dir)
}

func (localStateStore) WriteState(ctx context.Context, dir string, s *State) error {
	return SaveState(dir, s)
}

// SFTPStateStore reads and writes .pier/state.json over SFTP on the
// deploy SSH connection, so the deploy record lives on the remote
// host where Rollback and `pier status <env>` can find it. Client is
// the already-dialed deploy connection.
type SFTPStateStore struct {
	Client *Client
}

func (s SFTPStateStore) ReadState(ctx context.Context, dir string) (*State, error) {
	b, err := s.Client.ReadFile(ctx, filepath.ToSlash(filepath.Join(dir, stateFile)))
	if err != nil || b == nil {
		return nil, err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("deploy: parse state: %w", err)
	}
	return &st, nil
}

func (s SFTPStateStore) WriteState(ctx context.Context, dir string, st *State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return s.Client.WriteFile(ctx, filepath.ToSlash(filepath.Join(dir, stateFile)), b)
}
