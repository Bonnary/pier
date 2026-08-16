package deploy

import "testing"

func TestPathExcluded(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"composer.json", false},
		{".git/config", true},
		{"vendor/composer/autoload.php", true},
		{"node_modules/foo/index.js", true},
		{"resources/css/app.css", false},
		{".env", true},
		{".env.staging", true},
		{".env.production", false}, // include rule overrides .env.*
		{"storage/logs/laravel.log", true},
		{"storage/logs", false}, // dir itself is kept; children pruned
		{".idea/workspace.xml", true},
		{".vscode/settings.json", true},
		{"note.swp", true},
		{".DS_Store", true},
		{"sub/dir/.env.production", false},
		{"sub/dir/.env.local", true},
	}
	for _, c := range cases {
		if got := pathExcluded(c.path, rsyncExcludes); got != c.want {
			t.Errorf("pathExcluded(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestDeployFilesOnly(t *testing.T) {
	for _, rel := range []string{
		"docker-compose.prod.yml",
		".env.production",
		"docker/caddy/Caddyfile",
	} {
		if pathExcluded(rel, deployFilesOnly) {
			t.Errorf("pathExcluded(%q, deployFilesOnly) = true, want false (must be shipped)", rel)
		}
	}
	for _, rel := range []string{
		"docker-compose.yml",
		"docker/8.3/Dockerfile.prod",
		"app/Models/User.php",
		"marker.txt",
		".git/config",
		".env",
		".env.example",
	} {
		if !pathExcluded(rel, deployFilesOnly) {
			t.Errorf("pathExcluded(%q, deployFilesOnly) = false, want true (must be skipped)", rel)
		}
	}
}

// TestDeployFilesOnlyDescendsAncestorDirs guards the WalkDir pruning
// interaction: an excluded directory holding an included file must be
// descended (--exclude=* matches "docker", but
// docker/caddy/Caddyfile is included under it).
func TestDeployFilesOnlyDescendsAncestorDirs(t *testing.T) {
	for _, rel := range []string{"docker", "docker/caddy"} {
		if pathExcluded(rel, deployFilesOnly) {
			t.Errorf("pathExcluded(%q, deployFilesOnly) = true, want false (directory holds an included file)", rel)
		}
	}
	for _, rel := range []string{"docker/php", "app"} {
		if !pathExcluded(rel, deployFilesOnly) {
			t.Errorf("pathExcluded(%q, deployFilesOnly) = false, want true (no included file beneath)", rel)
		}
	}
}
