package deploy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const stateFile = ".pier/state.json"

type State struct {
	Current    string `json:"current"`
	Previous   string `json:"previous"`
	DeployedAt string `json:"deployed_at"`
	DeployedBy string `json:"deployed_by"`
}

func (s *State) HasPrevious() bool { return s.Previous != "" }

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
