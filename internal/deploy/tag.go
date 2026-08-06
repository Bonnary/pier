package deploy

import (
	"os/exec"
	"strings"
	"time"
)

// gitShortSHA returns the short SHA of HEAD in dir. It is a var so
// tests can pin the value without invoking git.
var gitShortSHA = func(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD").Output()
	return strings.TrimSpace(string(out)), err
}

// deployTag computes the immutable image tag for this deploy: the
// short git SHA of HEAD when the project is a git repo with a HEAD,
// else a UTC timestamp (no git repo, no HEAD, or git unavailable).
func deployTag() string {
	if sha, err := gitShortSHA("."); err == nil && sha != "" {
		return sha
	}
	return time.Now().UTC().Format("20060102150405")
}
