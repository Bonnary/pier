package laravel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const inertiaViteConfig = `import { defineConfig } from 'vite';
import laravel from 'laravel-vite-plugin';

export default defineConfig({
    plugins: [
        laravel({
            input: ['resources/css/app.css', 'resources/js/app.js'],
            refresh: true,
        }),
    ],
});
`

func TestEnsureViteHost_NoConfigFile(t *testing.T) {
	dir := t.TempDir()
	changed, err := EnsureViteHost(dir)
	if err != nil {
		t.Fatalf("EnsureViteHost: %v", err)
	}
	if changed {
		t.Errorf("changed = true, want false (no vite config exists)")
	}
	for _, name := range []string{"vite.config.ts", "vite.config.js"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("EnsureViteHost created %s unexpectedly", name)
		}
	}
}

func TestEnsureViteHost_FallsBackToJS(t *testing.T) {
	dir := t.TempDir()
	jsPath := filepath.Join(dir, "vite.config.js")
	if err := os.WriteFile(jsPath, []byte(inertiaViteConfig), 0644); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureViteHost(dir)
	if err != nil {
		t.Fatalf("EnsureViteHost: %v", err)
	}
	if !changed {
		t.Errorf("changed = false, want true")
	}
	got, err := os.ReadFile(jsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "server: { host: true }") {
		t.Errorf("vite.config.js missing server: { host: true }:\n%s", got)
	}
}

func TestEnsureViteHost_PatchesInertiaShape(t *testing.T) {
	dir := t.TempDir()
	tsPath := filepath.Join(dir, "vite.config.ts")
	if err := os.WriteFile(tsPath, []byte(inertiaViteConfig), 0644); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureViteHost(dir)
	if err != nil {
		t.Fatalf("EnsureViteHost: %v", err)
	}
	if !changed {
		t.Errorf("changed = false, want true")
	}
	got, err := os.ReadFile(tsPath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, "server: { host: true },") {
		t.Errorf("vite.config.ts missing server: { host: true } as a sibling of plugins:\n%s", s)
	}
	if !strings.Contains(s, "plugins:") {
		t.Errorf("plugins: section was clobbered:\n%s", s)
	}
}

func TestEnsureViteHost_MergesIntoExistingServer(t *testing.T) {
	dir := t.TempDir()
	tsPath := filepath.Join(dir, "vite.config.ts")
	input := `import { defineConfig } from 'vite';

export default defineConfig({
    server: { port: 3000 },
});
`
	if err := os.WriteFile(tsPath, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureViteHost(dir)
	if err != nil {
		t.Fatalf("EnsureViteHost: %v", err)
	}
	if !changed {
		t.Errorf("changed = false, want true")
	}
	got, err := os.ReadFile(tsPath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, "host: true") {
		t.Errorf("missing host: true:\n%s", s)
	}
	if !strings.Contains(s, "port: 3000") {
		t.Errorf("existing port: 3000 was lost:\n%s", s)
	}
}

func TestEnsureViteHost_AlreadyHasHostTrue(t *testing.T) {
	dir := t.TempDir()
	tsPath := filepath.Join(dir, "vite.config.ts")
	input := `import { defineConfig } from 'vite';

export default defineConfig({
    server: { host: true },
});
`
	if err := os.WriteFile(tsPath, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureViteHost(dir)
	if err != nil {
		t.Fatalf("EnsureViteHost: %v", err)
	}
	if changed {
		t.Errorf("changed = true, want false (server.host already true)")
	}
	got, err := os.ReadFile(tsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != input {
		t.Errorf("file was modified; want byte-equal to input.\ninput:\n%s\ngot:\n%s", input, got)
	}
}

func TestEnsureViteHost_AlreadyHasHostString(t *testing.T) {
	dir := t.TempDir()
	tsPath := filepath.Join(dir, "vite.config.ts")
	input := `import { defineConfig } from 'vite';

export default defineConfig({
    server: { host: '0.0.0.0' },
});
`
	if err := os.WriteFile(tsPath, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureViteHost(dir)
	if err != nil {
		t.Fatalf("EnsureViteHost: %v", err)
	}
	if changed {
		t.Errorf("changed = true, want false (server.host already set to string)")
	}
	got, err := os.ReadFile(tsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != input {
		t.Errorf("file was modified; want byte-equal to input.\ninput:\n%s\ngot:\n%s", input, got)
	}
}

func TestEnsureViteHost_AlreadyHasHostFalse(t *testing.T) {
	dir := t.TempDir()
	tsPath := filepath.Join(dir, "vite.config.ts")
	input := `import { defineConfig } from 'vite';

export default defineConfig({
    server: { host: false },
});
`
	if err := os.WriteFile(tsPath, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureViteHost(dir)
	if err != nil {
		t.Fatalf("EnsureViteHost: %v", err)
	}
	if changed {
		t.Errorf("changed = true, want false (server.host explicitly false is user intent)")
	}
	got, err := os.ReadFile(tsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != input {
		t.Errorf("file was modified; want byte-equal to input.\ninput:\n%s\ngot:\n%s", input, got)
	}
}

func TestEnsureViteHost_Idempotent(t *testing.T) {
	dir := t.TempDir()
	tsPath := filepath.Join(dir, "vite.config.ts")
	if err := os.WriteFile(tsPath, []byte(inertiaViteConfig), 0644); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureViteHost(dir)
	if err != nil {
		t.Fatalf("first EnsureViteHost: %v", err)
	}
	if !changed {
		t.Errorf("first call: changed = false, want true")
	}
	afterFirst, err := os.ReadFile(tsPath)
	if err != nil {
		t.Fatal(err)
	}
	changed, err = EnsureViteHost(dir)
	if err != nil {
		t.Fatalf("second EnsureViteHost: %v", err)
	}
	if changed {
		t.Errorf("second call: changed = true, want false (idempotent)")
	}
	afterSecond, err := os.ReadFile(tsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterFirst) != string(afterSecond) {
		t.Errorf("file changed between calls.\nafter first:\n%s\nafter second:\n%s", afterFirst, afterSecond)
	}
}

func TestEnsureViteHost_NoDefineConfig(t *testing.T) {
	dir := t.TempDir()
	tsPath := filepath.Join(dir, "vite.config.ts")
	input := `export const plugins = ['some-plugin'];
export default { plugins };
`
	if err := os.WriteFile(tsPath, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureViteHost(dir)
	if err != nil {
		t.Fatalf("EnsureViteHost: %v", err)
	}
	if changed {
		t.Errorf("changed = true, want false (no defineConfig({ in file)")
	}
	got, err := os.ReadFile(tsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != input {
		t.Errorf("file was modified; want byte-equal to input.\ninput:\n%s\ngot:\n%s", input, got)
	}
}

func TestEnsureViteHost_PreservesSurroundingContent(t *testing.T) {
	dir := t.TempDir()
	tsPath := filepath.Join(dir, "vite.config.ts")
	input := `import { defineConfig } from 'vite';
import laravel from 'laravel-vite-plugin';

// pier should leave this comment alone.
export default defineConfig({
    plugins: [
        laravel({
            input: ['resources/css/app.css', 'resources/js/app.js'],
            refresh: true,
        }),
    ],
});
`
	if err := os.WriteFile(tsPath, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureViteHost(dir)
	if err != nil {
		t.Fatalf("EnsureViteHost: %v", err)
	}
	if !changed {
		t.Errorf("changed = false, want true")
	}
	got, err := os.ReadFile(tsPath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, "// pier should leave this comment alone.") {
		t.Errorf("leading comment was lost:\n%s", s)
	}
	if !strings.Contains(s, "server: { host: true },") {
		t.Errorf("server: { host: true }, not inserted:\n%s", s)
	}
}
