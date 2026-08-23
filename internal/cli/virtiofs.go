package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// minWSLVirtioFS is the first WSL release with VirtioFS support for Windows
// drive mounts.
const minWSLVirtioFS = "2.7.1"

// Test seams — overridable from *_test.go.
var (
	virtiofsIsWindows = func() bool { return runtime.GOOS == "windows" }
	virtiofsVersionCmd = func() (string, error) {
		out, err := exec.Command("wsl", "--version").Output()
		return string(out), err
	}
	virtiofsUpdateCmd = func() error { return exec.Command("wsl", "--update").Run() }
	virtiofsConfigPath = func() string {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, ".wslconfig")
	}
	virtiofsReadFile  = os.ReadFile
	virtiofsWriteFile = os.WriteFile
)

// parseWSLVersion extracts the "WSL version:" line from `wsl --version`
// output (e.g. "WSL version: 2.7.1.0") and returns its major/minor/patch.
func parseWSLVersion(output string) (major, minor, patch int, ok bool) {
	for _, line := range strings.Split(output, "\n") {
		rest, found := strings.CutPrefix(strings.TrimSpace(line), "WSL version:")
		if !found {
			continue
		}
		parts := strings.Split(strings.TrimSpace(rest), ".")
		if len(parts) < 3 {
			continue
		}
		var v [3]int
		for i, p := range parts[:3] {
			n, err := strconv.Atoi(strings.TrimSpace(p))
			if err != nil {
				break
			}
			v[i] = n
			if i == 2 {
				return v[0], v[1], v[2], true
			}
		}
	}
	return 0, 0, 0, false
}

// wslSupportsVirtioFS reports whether the version is >= 2.7.1.
func wslSupportsVirtioFS(major, minor, patch int) bool {
	if major != 2 {
		return major > 2
	}
	if minor != 7 {
		return minor > 7
	}
	return patch >= 1
}

// wslVersionString formats a parsed version back to x.y.z.
func wslVersionString(major, minor, patch int) string {
	return fmt.Sprintf("%d.%d.%d", major, minor, patch)
}

// virtiofsEnabled reports whether the [wsl2] section has virtiofs=true.
func virtiofsEnabled(content []byte) bool {
	section := ""
	for _, line := range strings.Split(strings.TrimSuffix(string(content), "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = strings.ToLower(strings.Trim(trimmed, "[]"))
			continue
		}
		if section != "wsl2" {
			continue
		}
		key, val, found := strings.Cut(trimmed, "=")
		if !found {
			continue
		}
		if strings.ToLower(strings.TrimSpace(key)) == "virtiofs" && strings.TrimSpace(val) == "true" {
			return true
		}
	}
	return false
}

// mergeVirtioFSConfig adds [wsl2] virtio=true and virtiofs=true when the
// keys are missing, preserving all existing content and existing keys.
func mergeVirtioFSConfig(content []byte) []byte {
	if len(content) == 0 {
		return []byte("[wsl2]\nvirtio=true\nvirtiofs=true\n")
	}
	var out []string
	wantVirtio, wantVirtiofs := true, true
	sawWSL2, inWSL2 := false, false
	missing := func() []string {
		var keys []string
		if wantVirtio {
			keys = append(keys, "virtio=true")
		}
		if wantVirtiofs {
			keys = append(keys, "virtiofs=true")
		}
		return keys
	}
	lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inWSL2 {
				out = append(out, missing()...)
			}
			inWSL2 = strings.EqualFold(trimmed, "[wsl2]")
			sawWSL2 = sawWSL2 || inWSL2
			out = append(out, line)
			continue
		}
		if inWSL2 {
			key, _, found := strings.Cut(trimmed, "=")
			if found {
				switch strings.ToLower(strings.TrimSpace(key)) {
				case "virtio":
					wantVirtio = false
				case "virtiofs":
					wantVirtiofs = false
				}
			}
		}
		out = append(out, line)
	}
	if inWSL2 {
		out = append(out, missing()...)
	}
	if !sawWSL2 {
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, "[wsl2]", "virtio=true", "virtiofs=true")
	}
	joined := strings.Join(out, "\n")
	if strings.HasSuffix(string(content), "\n") {
		joined += "\n"
	}
	return []byte(joined)
}

// isWSLPath reports whether p lives on a WSL filesystem (\\wsl$\ or
// \\wsl.localhost\), where mounts are already native.
func isWSLPath(p string) bool {
	lp := strings.ToLower(filepath.ToSlash(p))
	return strings.HasPrefix(lp, "//wsl$/") || strings.HasPrefix(lp, "//wsl.localhost/")
}

// askYesNo asks a [Y/n] question via prompt; an empty answer means no.
func askYesNo(stdout io.Writer, stdin io.Reader, label string) bool {
	return strings.EqualFold(strings.TrimSpace(prompt(stdout, stdin, label, "n")), "y")
}
