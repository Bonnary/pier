package laravel

import (
	"os"
	"path/filepath"
	"strings"
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

func TestSupportedPHPRuntimes(t *testing.T) {
	got := SupportedPHPRuntimes()
	if len(got) < 1 {
		t.Fatal("SupportedPHPRuntimes() empty")
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] >= got[i] {
			t.Errorf("not ascending: %v", got)
		}
	}
	for _, v := range got {
		if _, err := Runtime(v); err != nil {
			t.Errorf("SupportedPHPRuntimes contains %q which Runtime() rejects: %v", v, err)
		}
	}
}

func TestSupportedNodeVersions(t *testing.T) {
	got := SupportedNodeVersions()
	if len(got) < 1 {
		t.Fatal("SupportedNodeVersions() empty")
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] >= got[i] {
			t.Errorf("not ascending: %v", got)
		}
	}
}

func TestRuntimeSupervisordNumprocs(t *testing.T) {
	for _, v := range []string{"8.2", "8.3", "8.4", "8.5"} {
		sv, err := os.ReadFile(filepath.Join("runtimes", v, "supervisord.conf"))
		if err != nil {
			t.Fatalf("read supervisord.conf: %v", err)
		}
		for _, want := range []string{"process_name=%(program_name)s_%(process_num)02d", "numprocs=%(ENV_SUPERVISOR_NUMPROCS)s"} {
			if !strings.Contains(string(sv), want) {
				t.Errorf("runtimes/%s/supervisord.conf missing %q (numprocs needs a %%(process_num) process_name; the env var lets the same file run 1 (app/scheduler) or N (queue) processes)", v, want)
			}
		}
		df, err := os.ReadFile(filepath.Join("runtimes", v, "Dockerfile"))
		if err != nil {
			t.Fatalf("read Dockerfile: %v", err)
		}
		if !strings.Contains(string(df), `ENV SUPERVISOR_NUMPROCS="1"`) {
			t.Errorf("runtimes/%s/Dockerfile missing ENV SUPERVISOR_NUMPROCS default (without it the env expansion is empty and supervisord fails to parse numprocs)", v)
		}
	}
}
