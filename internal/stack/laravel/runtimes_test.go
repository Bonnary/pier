package laravel

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeVersionsPresent(t *testing.T) {
	for _, v := range []string{"8.2", "8.3", "8.4", "8.5"} {
		dir := filepath.Join("runtimes", v)
		for _, f := range []string{"Dockerfile", "php.ini", "supervisord.conf"} {
			p := filepath.Join(dir, f)
			if _, err := os.Stat(p); err != nil {
				t.Errorf("missing %s: %v", p, err)
			}
		}
	}
}

func TestRuntime(t *testing.T) {
	for _, v := range []string{"8.2", "8.3", "8.4", "8.5"} {
		got, err := Runtime(v)
		if err != nil {
			t.Errorf("Runtime(%q): %v", v, err)
		}
		if filepath.Base(got) != v {
			t.Errorf("Runtime(%q) = %q, base = %q", v, got, filepath.Base(got))
		}
	}
}

func TestRuntimeUnknown(t *testing.T) {
	_, err := Runtime("7.4")
	if err == nil {
		t.Error("Runtime(7.4) = nil error, want non-nil")
	}
}
