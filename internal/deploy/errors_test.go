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

func TestExistingConstructorsDefaultKind(t *testing.T) {
	base := errors.New("base")
	cases := []struct {
		name string
		got  error
		code int
		kind Kind
	}{
		{"PreflightError", PreflightError(base), ExitPreflight, KindConfig},
		{"BuildError", BuildError(base), ExitBuild, KindDocker},
		{"UpError", UpError(base), ExitUp, KindDocker},
		{"ExecDownError", ExecDownError(), ExitExecDown, KindDocker},
		{"PortInUseError", PortInUseError([]int{8000}), ExitPortInUse, KindUser},
		{"AbortedError", AbortedError(), ExitAborted, KindUser},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var ee *ExitError
			if !errors.As(c.got, &ee) {
				t.Fatalf("not *ExitError")
			}
			if ee.Code != c.code {
				t.Errorf("Code = %d, want %d", ee.Code, c.code)
			}
			if ee.Kind != c.kind {
				t.Errorf("Kind = %v, want %v", ee.Kind, c.kind)
			}
		})
	}
}

func TestPortInUseErrorCode(t *testing.T) {
	e := PortInUseError([]int{8000, 6379})
	var exitErr *ExitError
	if !errors.As(e, &exitErr) {
		t.Fatalf("PortInUseError did not return *ExitError, got %T", e)
	}
	if exitErr.Code != ExitPortInUse {
		t.Errorf("Code = %d, want %d", exitErr.Code, ExitPortInUse)
	}
	if exitErr.Kind != KindUser {
		t.Errorf("Kind = %v, want KindUser (user needs to edit pier.toml)", exitErr.Kind)
	}
	if !errors.Is(e, ErrPortInUse) {
		t.Errorf("err does not match ErrPortInUse sentinel")
	}
}

func TestKindHint(t *testing.T) {
	cases := []struct {
		k    Kind
		want string
	}{
		{KindConfig, "see docs/superpowers/specs/2026-07-26-pier-design.md#configuration or run 'cat pier.toml'"},
		{KindDocker, "run 'pier status' to see container state, then 'pier dev' to (re)start the stack"},
		{KindSSH, "verify ssh access: 'ssh deploy@<host>', check ~/.ssh/id_ed25519 perms (chmod 600)"},
		{KindNetwork, "check internet/VPN; 'docker pull <image>' manually to isolate registry vs DNS"},
		{KindUser, ""},
		{KindUnknown, ""},
	}
	for _, c := range cases {
		if got := c.k.Hint(); got != c.want {
			t.Errorf("Kind(%v).Hint() = %q, want %q", c.k, got, c.want)
		}
	}
}
