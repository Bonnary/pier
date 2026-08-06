package deploy

import "strings"

// rsyncExcludes is the default set of paths pier skips when syncing
// the project tree to the deploy host: version control, build
// artifacts, secrets, editor state, and macOS metadata. .env.production
// is allowed through; everything else starting with .env is dropped.
// The list keeps the rsync CLI shape (--include= / --exclude=) so it
// stays recognizable; pathExcluded interprets it directly.
var rsyncExcludes = []string{
	"--exclude=.git",
	"--exclude=node_modules",
	"--exclude=vendor",
	"--exclude=.env",
	"--exclude=.env.*",
	"--include=.env.production",
	"--exclude=storage/logs/*",
	"--exclude=.idea",
	"--exclude=.vscode",
	"--exclude=*.swp",
	"--exclude=.DS_Store",
}

// deployFilesOnly is the filter for image-mode host syncs: exactly
// the files the host needs to run the stack (the compose file, the
// env file with secrets, and the bind-mounted nginx conf). Everything
// else is excluded — the host never receives the source tree when the
// image is built elsewhere.
var deployFilesOnly = []string{
	"--include=docker-compose.prod.yml",
	"--include=.env.production",
	"--include=docker/nginx/default.conf",
	"--exclude=*",
}

// pathExcluded reports whether the relative path rel should be
// skipped when syncing. Include rules are checked first, so an
// include pattern always wins over an earlier exclude pattern —
// rsync's first-match-wins would let --exclude=.env.* drop
// .env.production, so the SFTP sync fixes that by design.
func pathExcluded(rel string, excludes []string) bool {
	for _, rule := range excludes {
		if !strings.HasPrefix(rule, "--include=") {
			continue
		}
		if matchPattern(rel, strings.TrimPrefix(rule, "--include=")) {
			return false
		}
	}
	for _, rule := range excludes {
		if !strings.HasPrefix(rule, "--exclude=") {
			continue
		}
		if matchPattern(rel, strings.TrimPrefix(rule, "--exclude=")) {
			// An excluded directory may still hold an included file
			// (--exclude=* matches "docker" while
			// docker/nginx/default.conf is included beneath it);
			// WalkDir must descend into it or the include never ships.
			if dirHoldsIncludedFile(rel, excludes) {
				return false
			}
			return true
		}
	}
	return false
}

// dirHoldsIncludedFile reports whether any include rule is anchored
// under rel (e.g. rel "docker" and include "docker/nginx/default.conf").
func dirHoldsIncludedFile(rel string, excludes []string) bool {
	prefix := rel + "/"
	for _, rule := range excludes {
		if !strings.HasPrefix(rule, "--include=") {
			continue
		}
		p := strings.TrimPrefix(rule, "--include=")
		if strings.Contains(p, "/") && strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// matchPattern matches rel against a single rsync-style pattern.
// Patterns containing a slash are anchored at the root (rsync
// semantics); patterns without a slash match any path component.
func matchPattern(rel, pattern string) bool {
	if strings.Contains(pattern, "/") {
		// Anchored: "storage/logs/*" → any path under "storage/logs/".
		return strings.HasPrefix(rel, strings.TrimSuffix(pattern, "*"))
	}
	for _, comp := range strings.Split(rel, "/") {
		if globMatch(comp, pattern) {
			return true
		}
	}
	return false
}

// globMatch reports whether s matches pattern, where '*' matches any
// run of characters (like rsync's *, which never matches '/'; the
// component split guarantees no '/' in s).
func globMatch(s, pattern string) bool {
	for len(pattern) > 0 {
		star := strings.IndexByte(pattern, '*')
		if star < 0 {
			return s == pattern
		}
		if !strings.HasPrefix(s, pattern[:star]) {
			return false
		}
		s = s[star:]
		pattern = pattern[star+1:]
		end := strings.IndexByte(pattern, '*')
		if end < 0 {
			return strings.HasSuffix(s, pattern)
		}
		mid := pattern[:end]
		idx := strings.Index(s, mid)
		if idx < 0 {
			return false
		}
		s = s[idx+len(mid):]
		pattern = pattern[end:]
	}
	return true
}
