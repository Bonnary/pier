package deploy

import (
	"errors"
	"testing"
)

func TestKindString(t *testing.T) {
	cases := []struct {
		k    Kind
		want string
	}{
		{KindUnknown, "unknown"},
		{KindConfig, "config"},
		{KindDocker, "docker"},
		{KindSSH, "ssh"},
		{KindNetwork, "network"},
		{KindUser, "user"},
		{Kind(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.k.String(); got != c.want {
			t.Errorf("Kind(%d).String() = %q, want %q", c.k, got, c.want)
		}
	}
}

func TestExitErrorKindZero(t *testing.T) {
	e := &ExitError{Code: ExitGeneral, Err: errors.New("boom")}
	if e.Kind != KindUnknown {
		t.Errorf("zero-value Kind = %v, want KindUnknown", e.Kind)
	}
}

func TestExitErrorErrorString(t *testing.T) {
	e := &ExitError{Code: ExitBuild, Kind: KindDocker, Err: errors.New("docker daemon not running")}
	want := "exit 3: docker daemon not running"
	if got := e.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
