package config

import (
	"fmt"
	"strings"
)

// SplitCommand tokenizes a command line into arguments. Whitespace
// separates arguments; single quotes, double quotes, and backslash
// escapes preserve literal whitespace inside an argument (there is no
// environment expansion). An unterminated quote or a trailing
// backslash returns an error. Empty input returns an empty slice.
func SplitCommand(line string) ([]string, error) {
	var (
		args    []string
		cur     strings.Builder
		quote   rune
		escaped bool
		hasCur  bool
	)
	flush := func() {
		args = append(args, cur.String())
		cur.Reset()
		hasCur = false
	}
	for _, ch := range line {
		if escaped {
			cur.WriteRune(ch)
			hasCur = true
			escaped = false
			continue
		}
		switch {
		case ch == '\\' && quote != '\'':
			escaped = true
			hasCur = true
		case quote != 0:
			if ch == quote {
				quote = 0
			} else {
				cur.WriteRune(ch)
				hasCur = true
			}
		case ch == '\'' || ch == '"':
			quote = ch
			hasCur = true
		case ch == ' ' || ch == '\t':
			if hasCur {
				flush()
			}
		default:
			cur.WriteRune(ch)
			hasCur = true
		}
	}
	if escaped {
		return nil, fmt.Errorf("trailing backslash")
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	if hasCur {
		flush()
	}
	return args, nil
}
