package compose

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

func DecodeFile(path string) (*yaml.Node, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("compose: read %s: %w", path, err)
	}
	var n yaml.Node
	if err := yaml.Unmarshal(b, &n); err != nil {
		return nil, fmt.Errorf("compose: parse %s: %w", path, err)
	}
	return &n, nil
}

func Encode(n *yaml.Node) ([]byte, error) {
	if n == nil {
		return nil, fmt.Errorf("compose: cannot encode nil node")
	}
	return yaml.Marshal(n)
}

func WriteFile(path string, n *yaml.Node) error {
	b, err := Encode(n)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0644); err != nil {
		return fmt.Errorf("compose: write %s: %w", path, err)
	}
	return nil
}
