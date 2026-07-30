package compose

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// DecodeFile reads path and returns the parsed yaml.Node tree. The
// file is treated as a single document; multi-document YAML is
// surfaced as a single DocumentNode whose Content[0] is the actual
// root.
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

// Encode marshals n to YAML bytes. Returns an error on a nil node
// so callers don't accidentally write a nil pointer.
func Encode(n *yaml.Node) ([]byte, error) {
	if n == nil {
		return nil, fmt.Errorf("compose: cannot encode nil node")
	}
	return yaml.Marshal(n)
}

// WriteFile encodes n and writes it to path with mode 0644. Used
// after MergeNodes to drop a merged compose file back to disk.
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
