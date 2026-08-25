package deploy

import (
	"regexp"
	"testing"
)

func TestDeployTagUsesGitSHA(t *testing.T) {
	old := gitShortSHA
	gitShortSHA = func(dir string) (string, error) { return "abc1234", nil }
	defer func() { gitShortSHA = old }()
	if got := deployTag(); got != "abc1234" {
		t.Errorf("deployTag() = %q, want abc1234", got)
	}
}

func TestDeployTagFallsBackToTimestamp(t *testing.T) {
	old := gitShortSHA
	gitShortSHA = func(dir string) (string, error) { return "", errNoHEAD }
	defer func() { gitShortSHA = old }()
	got := deployTag()
	re := regexp.MustCompile(`^[0-9]{14}$`)
	if !re.MatchString(got) {
		t.Errorf("deployTag() = %q, want 14-digit timestamp", got)
	}
}

var errNoHEAD = &noHEADErr{}

type noHEADErr struct{}

func (*noHEADErr) Error() string { return "no HEAD" }
