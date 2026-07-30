package laravel

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Bonnary/pier/internal/config"
)

func TestMergeDevEmpty(t *testing.T) {
	out, warns, err := MergeDev("", config.Config{
		Stack: config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
	}, func(MergeWarning) Decision { return DecisionKeep })
	if err != nil {
		t.Fatalf("MergeDev: %v", err)
	}
	if len(warns) != 0 {
		t.Errorf("warns = %v, want []", warns)
	}
	if !contains(out, "laravel.test:") {
		t.Errorf("output missing laravel.test:\n%s", out)
	}
}

func TestMergeDevPreservesUserSidecar(t *testing.T) {
	existing, _ := os.ReadFile(filepath.Join("testdata", "merge", "user-sidecar.yml"))
	out, _, err := MergeDev(string(existing), config.Config{
		Stack: config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"redis"}},
	}, func(MergeWarning) Decision { return DecisionKeep })
	if err != nil {
		t.Fatalf("MergeDev: %v", err)
	}
	if !contains(out, "user-sidecar:") {
		t.Errorf("user-sidecar was dropped:\n%s", out)
	}
	if !contains(out, "redis:") {
		t.Errorf("redis missing:\n%s", out)
	}
}

func TestMergeDevWarnsUnknownKey(t *testing.T) {
	existing, _ := os.ReadFile(filepath.Join("testdata", "merge", "unknown-key.yml"))
	var warned []MergeWarning
	_, warns, err := MergeDev(string(existing), config.Config{
		Stack: config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
	}, func(w MergeWarning) Decision {
		warned = append(warned, w)
		return DecisionKeep
	})
	if err != nil {
		t.Fatalf("MergeDev: %v", err)
	}
	if len(warns) == 0 {
		t.Error("expected at least one warning for unknown top-level key")
	}
}

func TestMergeDevPreservesExtraHostsOnOwnedService(t *testing.T) {
	existing, _ := os.ReadFile(filepath.Join("testdata", "merge", "extra-hosts.yml"))
	out, _, err := MergeDev(string(existing), config.Config{
		Stack: config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
	}, func(MergeWarning) Decision { return DecisionKeep })
	if err != nil {
		t.Fatalf("MergeDev: %v", err)
	}
	if !contains(out, "myhost.local:192.168.1.1") {
		t.Errorf("extra_hosts dropped:\n%s", out)
	}
}

func TestMergeDevIdempotent(t *testing.T) {
	existing, _ := os.ReadFile(filepath.Join("testdata", "merge", "user-sidecar.yml"))
	cfg := config.Config{Stack: config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"redis"}}}
	first, _, err := MergeDev(string(existing), cfg, func(MergeWarning) Decision { return DecisionKeep })
	if err != nil {
		t.Fatalf("MergeDev first: %v", err)
	}
	second, _, err := MergeDev(first, cfg, func(MergeWarning) Decision { return DecisionKeep })
	if err != nil {
		t.Fatalf("MergeDev second: %v", err)
	}
	if first != second {
		t.Errorf("MergeDev not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestMergeDevDecisionDropRemovesKey(t *testing.T) {
	existing, _ := os.ReadFile(filepath.Join("testdata", "merge", "unknown-key.yml"))
	out, _, err := MergeDev(string(existing), config.Config{
		Stack: config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
	}, func(w MergeWarning) Decision {
		if w.Key == "version" {
			return DecisionDrop
		}
		return DecisionKeep
	})
	if err != nil {
		t.Fatalf("MergeDev: %v", err)
	}
	if contains(out, "version:") {
		t.Errorf("DecisionDrop should have removed 'version':\n%s", out)
	}
}

func TestMergeDevDevServiceEditsPropagate(t *testing.T) {
	existing, _ := os.ReadFile(filepath.Join("testdata", "merge", "dev-service-stale.yml"))
	cfg := config.Config{
		Stack: config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Dev: config.DevConfig{
			Services: map[string]config.DevService{
				"log-viewer": {
					Image: "new/image:2",
					Ports: []string{"9090:8080"},
				},
			},
		},
	}
	out, _, err := MergeDev(string(existing), cfg, func(MergeWarning) Decision { return DecisionKeep })
	if err != nil {
		t.Fatalf("MergeDev: %v", err)
	}
	if contains(out, "old/image:1") {
		t.Errorf("dev service image was treated as a user sidecar; expected overlay to win for owned services:\n%s", out)
	}
	if !contains(out, "new/image:2") {
		t.Errorf("new image missing after merge:\n%s", out)
	}
}
