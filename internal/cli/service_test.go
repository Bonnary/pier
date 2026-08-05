package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bonnary/pier/internal/docker"
	"github.com/Bonnary/pier/internal/tui"
)

func writeServiceToml(t *testing.T, dir, extra string) {
	t.Helper()
	toml := "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\nservices=[]\n" + extra
	if err := os.WriteFile(filepath.Join(dir, "pier.toml"), []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}
}

func stubServicePicker(t *testing.T, picked []string, err error) {
	t.Helper()
	origTTY := tuiForTest
	tuiForTest = func() bool { return true }
	t.Cleanup(func() { tuiForTest = origTTY })
	origPick := pickServicesTUI
	pickServicesTUI = func(available, current []string) ([]string, error) { return picked, err }
	t.Cleanup(func() { pickServicesTUI = origPick })
}

func TestServiceDevPickerWritesConfigRerendersAndUps(t *testing.T) {
	dir := t.TempDir()
	writeServiceToml(t, dir, "")
	stubServicePicker(t, []string{"redis"}, nil)
	runner := &capturingRunner{}
	orig := dockerRunner
	dockerRunner = docker.Runner(runner)
	defer func() { dockerRunner = orig }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "service"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte(`"redis"`)) {
		t.Errorf("redis not in pier.toml:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "docker-compose.yml")); err != nil {
		t.Errorf("docker-compose.yml not re-rendered: %v", err)
	}
	if len(runner.calls) == 0 || !strings.Contains(runner.calls[0], "up") || !strings.Contains(runner.calls[0], "redis") {
		t.Errorf("docker up not invoked with the added service; calls = %v", runner.calls)
	}
}

func TestServiceDevNoChangesSkipsWrite(t *testing.T) {
	dir := t.TempDir()
	writeServiceToml(t, dir, "")
	stubServicePicker(t, []string{}, nil)

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "service"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "no changes") {
		t.Errorf("output = %q, want %q", buf.String(), "no changes")
	}
}

func TestServiceDevTUIAbort(t *testing.T) {
	dir := t.TempDir()
	writeServiceToml(t, dir, "")
	stubServicePicker(t, nil, tui.ErrAborted)

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "service"})
	err := root.Execute()
	if err == nil {
		t.Fatal("Execute returned nil error; want abort")
	}
	if !errors.Is(err, ErrAborted) {
		t.Errorf("errors.Is(err, ErrAborted) = false; want true; err = %v", err)
	}
}

func TestServiceNonTTYFails(t *testing.T) {
	dir := t.TempDir()
	writeServiceToml(t, dir, "")
	origTTY := tuiForTest
	tuiForTest = func() bool { return false }
	defer func() { tuiForTest = origTTY }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "service"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "interactive") {
		t.Errorf("err = %v, want non-TTY interactive error", err)
	}
}

func TestServiceEnvCreatesServicesOverride(t *testing.T) {
	dir := t.TempDir()
	writeServiceToml(t, dir, "[deploy.production]\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\nbranch=\"main\"\n")
	stubServicePicker(t, []string{"postgres", "redis"}, nil)

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "service", "production"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte(`services = ["postgres", "redis"]`)) {
		t.Errorf("deploy.production.services not written:\n%s", got)
	}
}

func TestServiceEnvInheritsStackAndMaterializes(t *testing.T) {
	dir := t.TempDir()
	writeServiceToml(t, dir, "[deploy.production]\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\nbranch=\"main\"\n")
	// stack.services = [] in writeServiceToml; picker adds redis and s3:
	stubServicePicker(t, []string{"redis", "s3"}, nil)

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "service", "production"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	got, _ := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if !bytes.Contains(got, []byte(`services = ["redis", "s3"]`)) {
		t.Errorf("deploy.production.services not materialized:\n%s", got)
	}
}

func TestServiceEnvNoChangesSkipsWrite(t *testing.T) {
	dir := t.TempDir()
	writeServiceToml(t, dir, "[deploy.production]\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\nbranch=\"main\"\nservices=[\"redis\"]\n")
	stubServicePicker(t, []string{"redis"}, nil)
	before, _ := os.ReadFile(filepath.Join(dir, "pier.toml"))

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "service", "production"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	after, _ := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if !bytes.Equal(before, after) {
		t.Errorf("pier.toml changed on no-op pick:\nbefore: %s\nafter: %s", before, after)
	}
	if !strings.Contains(buf.String(), "no changes") {
		t.Errorf("output = %q, want %q", buf.String(), "no changes")
	}
}

func TestServiceEnvEmptyPickWritesEmptyList(t *testing.T) {
	dir := t.TempDir()
	writeServiceToml(t, dir, "[deploy.production]\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\nbranch=\"main\"\nservices=[\"redis\"]\n")
	stubServicePicker(t, []string{}, nil)

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "service", "production"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	got, _ := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if !bytes.Contains(got, []byte(`services = []`)) {
		t.Errorf("empty pick should write explicit empty list:\n%s", got)
	}
}

func TestServiceEnvUnknownEnvFails(t *testing.T) {
	dir := t.TempDir()
	writeServiceToml(t, dir, "")
	stubServicePicker(t, []string{"redis"}, nil)

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "service", "production"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "no [deploy.production] section") {
		t.Errorf("err = %v, want missing deploy-section error", err)
	}
}

func TestServiceEnvWorksOnScaffoldedTable(t *testing.T) {
	dir := t.TempDir()
	writeServiceToml(t, dir, "[deploy.production]\nservices=[\"redis\"]\n")
	stubServicePicker(t, []string{"postgres"}, nil)

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "service", "production"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s (scaffolded table must load and update)", err, buf.String())
	}
	got, _ := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if !bytes.Contains(got, []byte(`services = ["postgres"]`)) {
		t.Errorf("scaffolded deploy.production.services not updated:\n%s", got)
	}
}
