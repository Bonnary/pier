package deploy

import "testing"

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
