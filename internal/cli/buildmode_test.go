package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pickBuilderTUI is overridden by buildmode_test to script the picker.
func TestBuildmodeWritesBuilder(t *testing.T) {
	origTTY := tuiForTest
	tuiForTest = func() bool { return true }
	defer func() { tuiForTest = origTTY }()
	dir := t.TempDir()
	writeServiceToml(t, dir, "[deploy.production]\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\nbranch=\"main\"\n")
	old := pickBuilderTUI
	pickBuilderTUI = func(labels []string, current string) (string, error) {
		if current != "host_server" {
			t.Fatalf("picker current = %q, want host_server", current)
		}
		return "local_machine", nil
	}
	defer func() { pickBuilderTUI = old }()

	oldCfg := cfgPath
	cfgPath = filepath.Join(dir, "pier.toml")
	defer func() { cfgPath = oldCfg }()

	var out, errOut bytes.Buffer
	cmd := newBuildmodeCmd(&out, &errOut)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"production"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("buildmode: %v", err)
	}
	got, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read pier.toml: %v", err)
	}
	if !strings.Contains(string(got), `builder = "local_machine"`) {
		t.Errorf("pier.toml missing builder = \"local_machine\":\n%s", got)
	}
}

func TestBuildmodeBuildServerPromptsForFields(t *testing.T) {
	origTTY := tuiForTest
	tuiForTest = func() bool { return true }
	defer func() { tuiForTest = origTTY }()
	dir := t.TempDir()
	writeServiceToml(t, dir, "[deploy.production]\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\nbranch=\"main\"\n")
	old := pickBuilderTUI
	pickBuilderTUI = func(labels []string, current string) (string, error) { return "build_server", nil }
	defer func() { pickBuilderTUI = old }()

	oldCfg := cfgPath
	cfgPath = filepath.Join(dir, "pier.toml")
	defer func() { cfgPath = oldCfg }()

	// Script the three prompts (build host, user, path).
	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()
	go func() {
		_, _ = w.WriteString("build.example.com\nbuilder-user\n/srv/build\n")
		_ = w.Close()
	}()

	var out, errOut bytes.Buffer
	cmd := newBuildmodeCmd(&out, &errOut)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"production"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("buildmode: %v", err)
	}
	got, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read pier.toml: %v", err)
	}
	s := string(got)
	for _, want := range []string{
		`builder = "build_server"`,
		`build_host = "build.example.com"`,
		`build_user = "builder-user"`,
		`build_path = "/srv/build"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("pier.toml missing %s:\n%s", want, s)
		}
	}
}

func TestBuildmodeNoChanges(t *testing.T) {
	origTTY := tuiForTest
	tuiForTest = func() bool { return true }
	defer func() { tuiForTest = origTTY }()
	dir := t.TempDir()
	writeServiceToml(t, dir, "[deploy.production]\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\nbranch=\"main\"\n")
	old := pickBuilderTUI
	pickBuilderTUI = func(labels []string, current string) (string, error) { return current, nil }
	defer func() { pickBuilderTUI = old }()

	oldCfg := cfgPath
	cfgPath = filepath.Join(dir, "pier.toml")
	defer func() { cfgPath = oldCfg }()

	var out, errOut bytes.Buffer
	cmd := newBuildmodeCmd(&out, &errOut)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"production"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("buildmode: %v", err)
	}
	if !strings.Contains(out.String(), "no changes") {
		t.Errorf("output = %q, want no-changes message", out.String())
	}
}
