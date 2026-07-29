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

func TestNewConstructorsSetKind(t *testing.T) {
	base := errors.New("base")
	cases := []struct {
		name string
		got  error
		want Kind
	}{
		{"ConfigError", ConfigError(base), KindConfig},
		{"DockerError", DockerError(base), KindDocker},
		{"SSHError", SSHError(base), KindSSH},
		{"NetworkError", NetworkError(base), KindNetwork},
		{"UserError", UserError(base), KindUser},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var ee *ExitError
			if !errors.As(c.got, &ee) {
				t.Fatalf("errors.As failed: not *ExitError")
			}
			if ee.Kind != c.want {
				t.Errorf("Kind = %v, want %v", ee.Kind, c.want)
			}
			if !errors.Is(c.got, base) {
				t.Errorf("errors.Is(base) = false, want true")
			}
		})
	}
}
