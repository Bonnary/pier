package config

import "testing"

func TestSplitCommand(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"plain args", "php artisan migrate", []string{"php", "artisan", "migrate"}},
		{"flag with value", "php artisan migrate --force", []string{"php", "artisan", "migrate", "--force"}},
		{"double-quoted space", `php artisan "migrate --force"`, []string{"php", "artisan", "migrate --force"}},
		{"single-quoted space", "php artisan 'migrate --force'", []string{"php", "artisan", "migrate --force"}},
		{"escaped space", `php artisan migrate\ --force`, []string{"php", "artisan", "migrate --force"}},
		{"collapsed whitespace", "  php   artisan\tmigrate  ", []string{"php", "artisan", "migrate"}},
		{"empty string", "", nil},
		{"only whitespace", "   \t ", nil},
		{"empty quoted arg", `php ''`, []string{"php", ""}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := SplitCommand(c.in)
			if err != nil {
				t.Fatalf("SplitCommand(%q): %v", c.in, err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("SplitCommand(%q) = %q, want %q", c.in, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("SplitCommand(%q) = %q, want %q", c.in, got, c.want)
				}
			}
		})
	}
}

func TestSplitCommandErrors(t *testing.T) {
	for _, in := range []string{`php "unterminated`, `php 'unterminated`, `php \`} {
		if _, err := SplitCommand(in); err == nil {
			t.Errorf("SplitCommand(%q) = nil error, want error", in)
		}
	}
}
