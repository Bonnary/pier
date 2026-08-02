package deploy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Bonnary/pier/internal/config"
)

// fakeStatusRunner answers each RemoteStatus command by substring
// match against resp keys, so tests don't need the exact interpolated
// command strings. Commands matching no key fail, mirroring a
// non-zero exit on the remote host (a missing state.json makes
// `cat` fail, yielding a nil State).
type fakeStatusRunner struct {
	cmds []string
	resp map[string]string
	err  error
}

func (f *fakeStatusRunner) Run(ctx context.Context, cmd string) ([]byte, []byte, error) {
	f.cmds = append(f.cmds, cmd)
	if f.err != nil {
		return nil, nil, f.err
	}
	for key, out := range f.resp {
		if strings.Contains(cmd, key) {
			return []byte(out), nil, nil
		}
	}
	return nil, nil, errors.New("unmatched command: " + cmd)
}

func (f *fakeStatusRunner) Close() error { return nil }

func healthServer(code int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
	}))
}

func TestRemoteStatusHappyPath(t *testing.T) {
	srv := healthServer(200)
	defer srv.Close()
	f := &fakeStatusRunner{resp: map[string]string{
		"docker compose": "abc  app  Up 2 hours",
		"df -h":          "/dev/sda  20G  15G",
		"system df":      "Images  5  3",
		"state.json":     `{"current":"sha1","previous":"sha0","deployed_at":"2026-08-02T05:00:00Z","deployed_by":"u@h"}`,
	}}
	rep, err := RemoteStatus(context.Background(), config.DeployConfig{Path: "/srv/x"},
		HealthConfig{URL: srv.URL, Timeout: 5 * time.Second, Interval: 10 * time.Millisecond, MaxAttempts: 1}, f)
	if err != nil {
		t.Fatalf("RemoteStatus: %v", err)
	}
	if !strings.Contains(rep.Containers, "app") {
		t.Errorf("Containers = %q, want to contain app", rep.Containers)
	}
	if !strings.Contains(rep.Disk, "20G") {
		t.Errorf("Disk = %q, want to contain 20G", rep.Disk)
	}
	if !strings.Contains(rep.DockerDisk, "Images") {
		t.Errorf("DockerDisk = %q, want to contain Images", rep.DockerDisk)
	}
	if rep.State == nil || rep.State.Current != "sha1" || rep.State.DeployedAt == "" {
		t.Errorf("State = %+v, want current=sha1 with deployed_at", rep.State)
	}
	if !rep.Healthy {
		t.Errorf("Healthy = false, want true")
	}
}

func TestRemoteStatusMissingState(t *testing.T) {
	srv := healthServer(200)
	defer srv.Close()
	f := &fakeStatusRunner{resp: map[string]string{
		"docker compose": "abc  app  Up",
		"df -h":          "Filesystem  Size  Used",
		"system df":      "Images  5  3",
	}}
	rep, err := RemoteStatus(context.Background(), config.DeployConfig{Path: "/srv/x"},
		HealthConfig{URL: srv.URL, Timeout: 5 * time.Second, Interval: 10 * time.Millisecond, MaxAttempts: 1}, f)
	if err != nil {
		t.Fatalf("RemoteStatus: %v", err)
	}
	if rep.State != nil {
		t.Errorf("State = %+v, want nil for missing state.json", rep.State)
	}
}

func TestRemoteStatusHealthDown(t *testing.T) {
	srv := healthServer(500)
	defer srv.Close()
	f := &fakeStatusRunner{resp: map[string]string{"docker compose": "x", "df -h": "", "system df": ""}}
	rep, err := RemoteStatus(context.Background(), config.DeployConfig{Path: "/srv/x"},
		HealthConfig{URL: srv.URL, Timeout: 5 * time.Second, Interval: 10 * time.Millisecond, MaxAttempts: 1}, f)
	if err != nil {
		t.Fatalf("RemoteStatus: %v", err)
	}
	if rep.Healthy {
		t.Errorf("Healthy = true, want false for 500 response")
	}
}

func TestRemoteStatusUnparseableState(t *testing.T) {
	srv := healthServer(200)
	defer srv.Close()
	f := &fakeStatusRunner{resp: map[string]string{
		"docker compose": "abc  app  Up",
		"df -h":          "Filesystem  Size  Used",
		"system df":      "Images  5  3",
		"state.json":     "not json",
	}}
	_, err := RemoteStatus(context.Background(), config.DeployConfig{Path: "/srv/x"},
		HealthConfig{URL: srv.URL, Timeout: 5 * time.Second, Interval: 10 * time.Millisecond, MaxAttempts: 1}, f)
	if err == nil {
		t.Fatal("RemoteStatus = nil error, want non-nil for unparseable state.json")
	}
	if !strings.Contains(err.Error(), "state.json parse") {
		t.Errorf("error = %q, want mention of state.json parse", err)
	}
}

func TestRemoteStatusCommandFailure(t *testing.T) {
	f := &fakeStatusRunner{err: errors.New("boom")}
	_, err := RemoteStatus(context.Background(), config.DeployConfig{Path: "/srv/x"},
		HealthConfig{URL: "http://127.0.0.1:1", Timeout: time.Second, Interval: 10 * time.Millisecond, MaxAttempts: 1}, f)
	if err == nil {
		t.Fatal("RemoteStatus = nil error, want non-nil on command failure")
	}
	if !strings.Contains(err.Error(), "docker compose ps") {
		t.Errorf("error = %q, want mention of the failing command", err)
	}
}
