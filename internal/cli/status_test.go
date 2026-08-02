package cli

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/deploy"
	"github.com/Bonnary/pier/internal/docker"
)

func TestStatusNoConfig(t *testing.T) {
	dir := t.TempDir()
	runner := &capturingRunner{}
	orig := dockerRunner
	dockerRunner = docker.Runner(runner)
	defer func() { dockerRunner = orig }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "no.toml"), "status"})
	root.SilenceUsage = true
	err := root.Execute()
	if err == nil {
		t.Fatal("status on missing config = nil error, want non-nil")
	}
}

func TestStatusReadsConfig(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\nservices=[\"redis\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "pier.toml"), []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}
	runner := &capturingRunner{}
	orig := dockerRunner
	dockerRunner = docker.Runner(runner)
	defer func() { dockerRunner = orig }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "status"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(buf.String(), "x") {
		t.Errorf("output missing project name: %q", buf.String())
	}
}

type fakeStatusRunner struct {
	cmds        []string
	noStateFile bool
}

func (f *fakeStatusRunner) Run(ctx context.Context, cmd string) ([]byte, []byte, error) {
	f.cmds = append(f.cmds, cmd)
	switch {
	case strings.Contains(cmd, "compose"):
		return []byte("abc  app  Up 2 hours"), nil, nil
	case strings.Contains(cmd, "state.json"):
		if f.noStateFile {
			return nil, nil, errors.New("unmatched command: " + cmd)
		}
		return []byte(`{"current":"sha1","deployed_at":"2026-08-02T05:00:00Z","deployed_by":"u@h"}`), nil, nil
	case strings.Contains(cmd, "system df"):
		return []byte("Images  5  3"), nil, nil
	case strings.Contains(cmd, "df -h"):
		return []byte("/dev/sda  20G  15G"), nil, nil
	}
	return []byte("out"), nil, nil
}

func (f *fakeStatusRunner) Close() error { return nil }

func TestStatusRemoteSuccess(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\n[deploy.production]\nbranch=\"main\"\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pier.toml"), []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()

	origDial, origURL := statusDial, statusHealthURL
	statusDial = func(ctx context.Context, cfg deploy.SSHConfig) (deploy.StatusRunner, error) {
		return &fakeStatusRunner{}, nil
	}
	statusHealthURL = func(cfg *config.Config, env string) string { return srv.URL }
	defer func() { statusDial, statusHealthURL = origDial, origURL }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "status", "production"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := buf.String()
	for _, want := range []string{
		"env:     production (host: h)",
		"containers:",
		"app  Up 2 hours",
		"health: OK",
		"last deploy: sha1, 2026-08-02T05:00:00Z by u@h",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, got)
		}
	}
}

func TestStatusRemoteHealthDownNoDeploy(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\n[deploy.production]\nbranch=\"main\"\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pier.toml"), []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()

	origDial, origURL := statusDial, statusHealthURL
	statusDial = func(ctx context.Context, cfg deploy.SSHConfig) (deploy.StatusRunner, error) {
		return &fakeStatusRunner{noStateFile: true}, nil
	}
	statusHealthURL = func(cfg *config.Config, env string) string { return srv.URL }
	defer func() { statusDial, statusHealthURL = origDial, origURL }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "status", "production"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := buf.String()
	for _, want := range []string{
		"health: DOWN (",
		"last deploy: none yet",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, got)
		}
	}
}

func TestStatusRemoteNoEnvSection(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pier.toml"), []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "status", "production"})
	root.SilenceUsage = true
	err := root.Execute()
	if err == nil {
		t.Fatal("status production with no [deploy.production] = nil error, want non-nil")
	}
	if !strings.Contains(err.Error(), "no [deploy.production] section in pier.toml") {
		t.Errorf("error = %q, want missing-section message", err)
	}
}

func TestStatusRemoteDialFailure(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\ndomain=\"x.example.com\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\n[deploy.production]\nbranch=\"main\"\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pier.toml"), []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}
	orig := statusDial
	statusDial = func(ctx context.Context, cfg deploy.SSHConfig) (deploy.StatusRunner, error) {
		return nil, errors.New("boom")
	}
	defer func() { statusDial = orig }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "status", "production"})
	root.SilenceUsage = true
	err := root.Execute()
	if err == nil {
		t.Fatal("dial failure = nil error, want non-nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %q, want to contain boom", err)
	}
}
