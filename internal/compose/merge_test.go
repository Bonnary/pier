package compose

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func mustDecode(s string) *yaml.Node {
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(s), &n); err != nil {
		panic(err)
	}
	return &n
}

func TestMergeNodesEmpty(t *testing.T) {
	base := mustDecode("")
	overlay := mustDecode("services:\n  app:\n    image: x\n")
	got := MergeNodes(base, overlay)
	b, _ := yaml.Marshal(got)
	want := "services:\n    app:\n        image: x\n"
	if string(b) != want {
		t.Errorf("got:\n%s\nwant:\n%s", b, want)
	}
}

func TestMergeNodesOverlayScalar(t *testing.T) {
	base := mustDecode("services:\n  app:\n    image: old\n")
	overlay := mustDecode("services:\n  app:\n    image: new\n")
	got := MergeNodes(base, overlay)
	b, _ := yaml.Marshal(got)
	if string(b) != "services:\n    app:\n        image: new\n" {
		t.Errorf("got: %s", b)
	}
}

func TestMergeNodesPreserveUnknownKeys(t *testing.T) {
	base := mustDecode("services:\n  app:\n    image: myapp:1\n    extra_hosts:\n      - host.docker.internal:host-gateway\n")
	overlay := mustDecode("services:\n  app:\n    image: myapp:2\n")
	got := MergeNodes(base, overlay)
	b, _ := yaml.Marshal(got)
	want := "services:\n    app:\n        image: myapp:2\n        extra_hosts:\n            - host.docker.internal:host-gateway\n"
	if string(b) != want {
		t.Errorf("got:\n%s\nwant:\n%s", b, want)
	}
}

func TestMergeNodesNewTopLevelKey(t *testing.T) {
	base := mustDecode("version: '3'\n")
	overlay := mustDecode("networks:\n  default:\n    driver: bridge\n")
	got := MergeNodes(base, overlay)
	b, _ := yaml.Marshal(got)
	if !contains(b, "version: '3'") || !contains(b, "networks:") {
		t.Errorf("got: %s", b)
	}
}

func TestMergeNodesServiceLevel(t *testing.T) {
	base := mustDecode("services:\n  user-sidecar:\n    image: custom:1\n")
	overlay := mustDecode("services:\n  app:\n    image: app:1\n")
	got := MergeNodes(base, overlay)
	b, _ := yaml.Marshal(got)
	if !contains(b, "user-sidecar:") {
		t.Errorf("user-sidecar was dropped: %s", b)
	}
	if !contains(b, "app:") {
		t.Errorf("app missing: %s", b)
	}
}

func TestMergeNodesSequenceBaseWins(t *testing.T) {
	base := mustDecode("services:\n  app:\n    volumes:\n      - ./a:/a\n")
	overlay := mustDecode("services:\n  app:\n    volumes:\n      - ./b:/b\n")
	got := MergeNodes(base, overlay)
	b, _ := yaml.Marshal(got)
	if !contains(b, "./a:/a") {
		t.Errorf("base volume dropped: %s", b)
	}
	if contains(b, "./b:/b") {
		t.Errorf("overlay volume incorrectly applied: %s", b)
	}
}

func TestMergeNodesIdempotent(t *testing.T) {
	merged := MergeNodes(mustDecode("services:\n  app:\n    image: x\n"), mustDecode("services:\n  app:\n    image: y\n"))
	again := MergeNodes(merged, merged)
	m1, _ := yaml.Marshal(merged)
	m2, _ := yaml.Marshal(again)
	if string(m1) != string(m2) {
		t.Errorf("MergeNodes not idempotent:\n%s\nvs\n%s", m1, m2)
	}
}

func contains(b []byte, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	if len(b) < len(sub) {
		return false
	}
	for i := 0; i+len(sub) <= len(b); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			if b[i+j] != sub[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
