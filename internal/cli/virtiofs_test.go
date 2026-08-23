package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseWSLVersion(t *testing.T) {
	cases := []struct {
		name                 string
		in                   string
		major, minor, patch  int
		ok                   bool
	}{
		{
			name:  "standard output",
			in:    "WSL version: 2.7.1\nKernel version: 6.6.16.1\nWSLg version: 1.0.61\nWindows version: 10.0.22621.3155\n",
			major: 2, minor: 7, patch: 1, ok: true,
		},
		{
			name:  "four-part version",
			in:    "WSL version: 2.7.1.0\n",
			major: 2, minor: 7, patch: 1, ok: true,
		},
		{
			name:  "old version",
			in:    "WSL version: 1.1.0\n",
			major: 1, minor: 1, patch: 0, ok: true,
		},
		{
			name: "unparseable value",
			in:   "WSL version: nonsense\n",
			ok:   false,
		},
		{
			name: "no version line",
			in:   "usage: wsl [options]\n",
			ok:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			major, minor, patch, ok := parseWSLVersion(c.in)
			if ok != c.ok || major != c.major || minor != c.minor || patch != c.patch {
				t.Errorf("parseWSLVersion(%q) = (%d,%d,%d,%v), want (%d,%d,%d,%v)",
					c.in, major, minor, patch, ok, c.major, c.minor, c.patch, c.ok)
			}
		})
	}
}

func TestWSLSupportsVirtioFS(t *testing.T) {
	cases := []struct {
		major, minor, patch int
		want                bool
	}{
		{2, 7, 0, false},
		{2, 7, 1, true},
		{2, 7, 2, true},
		{2, 8, 0, true},
		{2, 6, 9, false},
		{1, 7, 1, false},
		{3, 0, 0, true},
	}
	for _, c := range cases {
		if got := wslSupportsVirtioFS(c.major, c.minor, c.patch); got != c.want {
			t.Errorf("wslSupportsVirtioFS(%d,%d,%d) = %v, want %v", c.major, c.minor, c.patch, got, c.want)
		}
	}
}

func TestVirtiofsEnabled(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "enabled",
			content: "[wsl2]\nvirtio=true\nvirtiofs=true\n",
			want:    true,
		},
		{
			name:    "disabled",
			content: "[wsl2]\nvirtio=true\nvirtiofs=false\n",
			want:    false,
		},
		{
			name:    "wrong section",
			content: "[experimental]\nvirtiofs=true\n",
			want:    false,
		},
		{
			name:    "spaced value",
			content: "[wsl2]\nvirtiofs = true\n",
			want:    true,
		},
		{
			name:    "empty file",
			content: "",
			want:    false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := virtiofsEnabled([]byte(c.content)); got != c.want {
				t.Errorf("virtiofsEnabled(%q) = %v, want %v", c.content, got, c.want)
			}
		})
	}
}

func TestMergeVirtioFSConfig(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "empty file",
			content: "",
			want:    "[wsl2]\nvirtio=true\nvirtiofs=true\n",
		},
		{
			name:    "existing wsl2 section preserves keys",
			content: "[wsl2]\nmemory=4GB\nswap=8GB\n",
			want:    "[wsl2]\nmemory=4GB\nswap=8GB\nvirtio=true\nvirtiofs=true\n",
		},
		{
			name:    "user virtio=false is not clobbered",
			content: "[wsl2]\nvirtio=false\n",
			want:    "[wsl2]\nvirtio=false\nvirtiofs=true\n",
		},
		{
			name:    "both keys present unchanged",
			content: "[wsl2]\nvirtio=true\nvirtiofs=true\n",
			want:    "[wsl2]\nvirtio=true\nvirtiofs=true\n",
		},
		{
			name:    "section case-insensitive",
			content: "[WSL2]\nmemory=4GB\n",
			want:    "[WSL2]\nmemory=4GB\nvirtio=true\nvirtiofs=true\n",
		},
		{
			name:    "no wsl2 section appends one",
			content: "[network]\ndnsProxy=true\n",
			want:    "[network]\ndnsProxy=true\n\n[wsl2]\nvirtio=true\nvirtiofs=true\n",
		},
		{
			name:    "wsl2 before other sections",
			content: "[wsl2]\nmemory=4GB\n[network]\ndnsProxy=true\n",
			want:    "[wsl2]\nmemory=4GB\nvirtio=true\nvirtiofs=true\n[network]\ndnsProxy=true\n",
		},
		{
			name:    "wsl2 not first",
			content: "[network]\ndnsProxy=true\n[wsl2]\nmemory=4GB\n",
			want:    "[network]\ndnsProxy=true\n[wsl2]\nmemory=4GB\nvirtio=true\nvirtiofs=true\n",
		},
		{
			name:    "commented virtiofs ignored",
			content: "[wsl2]\n# virtiofs=true\n",
			want:    "[wsl2]\n# virtiofs=true\nvirtio=true\nvirtiofs=true\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := string(mergeVirtioFSConfig([]byte(c.content)))
			if got != c.want {
				t.Errorf("mergeVirtioFSConfig(%q) =\n%q\nwant\n%q", c.content, got, c.want)
			}
		})
	}
}

func TestIsWSLPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{`\\wsl$\Ubuntu\home\user\app`, true},
		{`\\WSL$\Ubuntu\home\user\app`, true},
		{`\\wsl.localhost\Ubuntu\home\user`, true},
		{`C:\code\myapp`, false},
		{`/home/user/app`, false},
	}
	for _, c := range cases {
		if got := isWSLPath(c.path); got != c.want {
			t.Errorf("isWSLPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestAskYesNo(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"yes", "y\n", true},
		{"yes upper", "Y\n", true},
		{"no", "n\n", false},
		{"empty defaults no", "\n", false},
		{"whitespace defaults no", " \n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			if got := askYesNo(&buf, strings.NewReader(c.input), "continue? "); got != c.want {
				t.Errorf("askYesNo(%q) = %v, want %v", c.input, got, c.want)
			}
		})
	}
}

// runVirtioFS wires the seams around maybeEnableVirtioFS and runs it.
// configPath is returned verbatim by the config-path seam; versions are
// served one per versionCmd call. Returns (output, updateRan, err).
func runVirtioFS(t *testing.T, isWindows bool, versions []string, versionErr, updateErr error, configPath, input string) (string, bool, error) {
	t.Helper()
	prevWin := virtiofsIsWindows
	virtiofsIsWindows = func() bool { return isWindows }
	t.Cleanup(func() { virtiofsIsWindows = prevWin })

	vi := 0
	prevVer := virtiofsVersionCmd
	virtiofsVersionCmd = func() (string, error) {
		if vi < len(versions) {
			ver := versions[vi]
			vi++
			return ver, versionErr
		}
		return "", versionErr
	}
	t.Cleanup(func() { virtiofsVersionCmd = prevVer })

	updated := false
	prevUpd := virtiofsUpdateCmd
	virtiofsUpdateCmd = func() error { updated = true; return updateErr }
	t.Cleanup(func() { virtiofsUpdateCmd = prevUpd })

	prevPath := virtiofsConfigPath
	virtiofsConfigPath = func() string { return configPath }
	t.Cleanup(func() { virtiofsConfigPath = prevPath })

	var buf bytes.Buffer
	err := maybeEnableVirtioFS(&buf, strings.NewReader(input), `C:\code\myapp`)
	return buf.String(), updated, err
}

func TestMaybeEnableVirtioFSFlow(t *testing.T) {
	t.Run("non-windows skips", func(t *testing.T) {
		out, updated, err := runVirtioFS(t, false, nil, nil, nil, filepath.Join(t.TempDir(), ".wslconfig"), "")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if updated {
			t.Errorf("must not run wsl --update on non-windows")
		}
		if strings.Contains(out, "VirtioFS") {
			t.Errorf("output = %q, want no virtiofs prompt", out)
		}
	})

	t.Run("wsl-path project skips wsl version call", func(t *testing.T) {
		prevWin := virtiofsIsWindows
		virtiofsIsWindows = func() bool { return true }
		t.Cleanup(func() { virtiofsIsWindows = prevWin })
		versionCalls := 0
		prevVer := virtiofsVersionCmd
		virtiofsVersionCmd = func() (string, error) {
			versionCalls++
			return "WSL version: 2.7.1\n", nil
		}
		t.Cleanup(func() { virtiofsVersionCmd = prevVer })

		var buf bytes.Buffer
		if err := maybeEnableVirtioFS(&buf, strings.NewReader(""), `\\wsl$\Ubuntu\home\user\app`); err != nil {
			t.Fatalf("err: %v", err)
		}
		if versionCalls != 0 {
			t.Errorf("wsl --version must not run for WSL-path projects; calls = %d", versionCalls)
		}
	})

	t.Run("missing wsl skips silently", func(t *testing.T) {
		out, updated, err := runVirtioFS(t, true, nil, errors.New("wsl not found"), nil, filepath.Join(t.TempDir(), ".wslconfig"), "")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if updated {
			t.Errorf("must not run wsl --update when wsl is missing")
		}
		if out != "" {
			t.Errorf("output = %q, want empty (silent skip)", out)
		}
	})

	t.Run("already enabled prints note and skips", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), ".wslconfig")
		if err := os.WriteFile(cfgPath, []byte("[wsl2]\nvirtio=true\nvirtiofs=true\n"), 0644); err != nil {
			t.Fatal(err)
		}
		out, updated, err := runVirtioFS(t, true, []string{"WSL version: 2.7.1\n"}, nil, nil, cfgPath, "")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if updated {
			t.Errorf("must not run wsl --update")
		}
		if !strings.Contains(out, "already enabled") {
			t.Errorf("output = %q, want 'already enabled'", out)
		}
		got, _ := os.ReadFile(cfgPath)
		if string(got) != "[wsl2]\nvirtio=true\nvirtiofs=true\n" {
			t.Errorf("config changed: %q", got)
		}
	})

	t.Run("old wsl offers update, decline = nothing", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), ".wslconfig")
		out, updated, err := runVirtioFS(t, true, []string{"WSL version: 2.6.2\n"}, nil, nil, cfgPath, "\n")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if updated {
			t.Errorf("must not run wsl --update when declined")
		}
		if !strings.Contains(out, "2.7.1") {
			t.Errorf("output = %q, want the 2.7.1 requirement mentioned", out)
		}
		if _, err := os.Stat(cfgPath); err == nil {
			t.Errorf(".wslconfig must not be written when update is declined")
		}
	})

	t.Run("old wsl accepts update then enables", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), ".wslconfig")
		out, updated, err := runVirtioFS(t, true,
			[]string{"WSL version: 2.6.2\n", "WSL version: 2.7.1\n"}, nil, nil, cfgPath, "y\ny\n")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !updated {
			t.Errorf("wsl --update must run when accepted")
		}
		got, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatalf("read .wslconfig: %v", err)
		}
		if string(got) != "[wsl2]\nvirtio=true\nvirtiofs=true\n" {
			t.Errorf("config = %q, want both keys", got)
		}
		if !strings.Contains(out, "wsl --shutdown") {
			t.Errorf("output = %q, want restart instructions", out)
		}
	})

	t.Run("update still old after update prints rerun note", func(t *testing.T) {
		out, updated, err := runVirtioFS(t, true,
			[]string{"WSL version: 2.6.2\n", "WSL version: 2.6.2\n"}, nil, nil, filepath.Join(t.TempDir(), ".wslconfig"), "y\n")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !updated {
			t.Errorf("wsl --update must run")
		}
		if !strings.Contains(out, "rerun pier init") {
			t.Errorf("output = %q, want rerun note", out)
		}
	})

	t.Run("enable declines = no write", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), ".wslconfig")
		out, _, err := runVirtioFS(t, true, []string{"WSL version: 2.7.1\n"}, nil, nil, cfgPath, "n\n")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if _, err := os.Stat(cfgPath); err == nil {
			t.Errorf(".wslconfig must not be written when declined")
		}
		if strings.Contains(out, "wsl --shutdown") {
			t.Errorf("output = %q, want no instructions on decline", out)
		}
	})

	t.Run("enable creates file with both keys", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), ".wslconfig")
		out, updated, err := runVirtioFS(t, true, []string{"WSL version: 2.7.1\n"}, nil, nil, cfgPath, "y\n")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if updated {
			t.Errorf("must not run wsl --update when version is sufficient")
		}
		got, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatalf("read .wslconfig: %v", err)
		}
		if string(got) != "[wsl2]\nvirtio=true\nvirtiofs=true\n" {
			t.Errorf("config = %q, want both keys", got)
		}
		if !strings.Contains(out, "wsl --shutdown") {
			t.Errorf("output = %q, want restart instructions", out)
		}
	})
}
