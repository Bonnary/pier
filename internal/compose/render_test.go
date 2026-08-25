package compose

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDecodeFile(t *testing.T) {
	n, err := DecodeFile(filepath.Join("testdata", "services.yml"))
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	if n.Kind != yaml.DocumentNode {
		t.Errorf("root Kind = %v, want DocumentNode", n.Kind)
	}
}

func TestDecodeFileMissing(t *testing.T) {
	_, err := DecodeFile(filepath.Join("testdata", "does-not-exist.yml"))
	if err == nil {
		t.Fatal("DecodeFile(missing) = nil error, want non-nil")
	}
}

func TestEncode(t *testing.T) {
	n := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "name"},
			{Kind: yaml.ScalarNode, Value: "myapp"},
		},
	}
	b, err := Encode(n)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(b) == 0 {
		t.Error("Encode returned empty bytes")
	}
}

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.yml")
	n := &yaml.Node{
		Kind: yaml.DocumentNode,
		Content: []*yaml.Node{
			{Kind: yaml.MappingNode, Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Value: "name"},
				{Kind: yaml.ScalarNode, Value: "myapp"},
			}},
		},
	}
	if err := WriteFile(path, n); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(b) == 0 {
		t.Error("written file is empty")
	}
}
