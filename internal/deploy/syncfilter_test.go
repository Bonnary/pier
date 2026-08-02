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
