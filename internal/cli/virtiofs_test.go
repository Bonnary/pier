package cli

import (
	"bytes"
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
