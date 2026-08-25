package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/docker"
)

func writeFile(path string, contents []byte) error {
	return os.WriteFile(path, contents, 0644)
}

type fakeRunnerCLI struct {
	calls []string
}

func (f *fakeRunnerCLI) Run(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
	call := name
	for _, a := range args {
		call += " " + a
	}
	f.calls = append(f.calls, call)
	stdout.Write([]byte("name\timage\tstate\n"))
	return nil
}

func TestDevCommand(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\nservices=[]\n[dev.ports]\nlaravel=18300\nvite=18301\n"
	if err := writeFile(filepath.Join(dir, "pier.toml"), []byte(toml)); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunnerCLI{}
	origRunner := dockerRunner
	dockerRunner = docker.Runner(runner)
	defer func() { dockerRunner = origRunner }()
	origWait := devAppTimeout
	devAppTimeout = 50 * time.Millisecond
	defer func() { devAppTimeout = origWait }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "dev"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	if len(runner.calls) < 2 {
		t.Errorf("expected >=2 docker calls, got: %v", runner.calls)
	}
	if runner.calls[0] != "docker compose -f "+filepath.Join(dir, "docker-compose.yml")+" --project-directory "+dir+" build" {
		t.Errorf("first call = %q", runner.calls[0])
	}
	if len(runner.calls) < 2 || runner.calls[1] != "docker compose -f "+filepath.Join(dir, "docker-compose.yml")+" --project-directory "+dir+" up -d" {
		t.Errorf("second call = %v", runner.calls)
	}
}

func TestDevCommandPortInUse(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer l.Close()
	conflictPort := l.Addr().(*net.TCPAddr).Port

	dir := t.TempDir()
	toml := fmt.Sprintf("[project]\nname=\"x\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\nservices=[]\n[dev.ports]\nlaravel=%d\n", conflictPort)
	if err := writeFile(filepath.Join(dir, "pier.toml"), []byte(toml)); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunnerCLI{}
	origRunner := dockerRunner
	dockerRunner = docker.Runner(runner)
	defer func() { dockerRunner = origRunner }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "dev"})
	err = root.Execute()
	if err == nil {
		t.Fatal("Execute = nil, want error (port in use)")
	}
	if ExitCode(err) != ExitPortInUse {
		t.Errorf("ExitCode(err) = %d, want %d", ExitCode(err), ExitPortInUse)
	}
	if len(runner.calls) != 0 {
		t.Errorf("docker should not be called when port is in use; got calls: %v", runner.calls)
	}
	if !strings.Contains(buf.String(), fmt.Sprintf("port %d", conflictPort)) {
		t.Errorf("stdout/stderr = %q, want it to mention 'port %d'", buf.String(), conflictPort)
	}
}

func TestPrintReadyBlockUsesConfiguredBind(t *testing.T) {
	cases := []struct {
		bind         string
		mustContain  []string
		mustNotMatch []string
	}{
		{
			bind:         "127.0.0.1",
			mustContain:  []string{"http://127.0.0.1:8000"},
			mustNotMatch: []string{"http://0.0.0.0:8000"},
		},
		{
			bind:         "0.0.0.0",
			mustContain:  []string{"http://0.0.0.0:8000"},
			mustNotMatch: []string{"http://127.0.0.1:8000"},
		},
	}
	for _, c := range cases {
		cfg := &config.Config{
			Project: config.ProjectConfig{Name: "x"},
			Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
			Dev:     config.DevConfig{Bind: c.bind},
		}
		var buf bytes.Buffer
		printReadyBlock(&buf, cfg)
		got := buf.String()
		for _, want := range c.mustContain {
			if !strings.Contains(got, want) {
				t.Errorf("bind=%q: ready block = %q, want it to contain %q", c.bind, got, want)
			}
		}
		for _, dont := range c.mustNotMatch {
			if strings.Contains(got, dont) {
				t.Errorf("bind=%q: ready block = %q, want it to NOT contain %q", c.bind, got, dont)
			}
		}
	}
}

func TestDevCommandSubsetSkipsAppWait(t *testing.T) {
	dir := t.TempDir()
	toml := "[project]\nname=\"x\"\n[stack]\ntype=\"laravel\"\nphp=\"8.3\"\nnode=\"22\"\nservices=[]\n[dev.ports]\nlaravel=18300\nvite=18301\n"
	if err := writeFile(filepath.Join(dir, "pier.toml"), []byte(toml)); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunnerCLI{}
	origRunner := dockerRunner
	dockerRunner = docker.Runner(runner)
	defer func() { dockerRunner = origRunner }()
	origWait := devAppTimeout
	devAppTimeout = 50 * time.Millisecond
	defer func() { devAppTimeout = origWait }()

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "dev", "redis"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	if strings.Contains(buf.String(), "waiting for app") {
		t.Errorf("subset `dev redis` should not wait for the app; got:\n%s", buf.String())
	}
}

func TestPrintReadyBlockShowsOnlyConfiguredServices(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "x"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22", Services: []string{"mysql", "mailpit"}},
		Dev: config.DevConfig{
			Bind: "127.0.0.1",
			Services: map[string]config.DevService{
				"log-viewer": {Image: "x", Ports: []string{"8081:8080"}},
			},
		},
	}
	var buf bytes.Buffer
	printReadyBlock(&buf, cfg)
	got := buf.String()
	for _, want := range []string{
		"App:    http://127.0.0.1:8000",
		"Vite:   http://127.0.0.1:5173",
		"Mysql: 127.0.0.1:3306",
		"MAILPIT_SMTP: 127.0.0.1:1025",
		"MAILPIT_UI: 127.0.0.1:8025",
		"log-viewer: 127.0.0.1:8081",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ready block missing %q; got:\n%s", want, got)
		}
	}
	for _, dont := range []string{"Postgres", "Redis", "Meilisearch", "S3_"} {
		if strings.Contains(got, dont) {
			t.Errorf("ready block shows %q even though not configured; got:\n%s", dont, got)
		}
	}
}

func TestWaitForApp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	var buf bytes.Buffer
	if !waitForApp(&buf, srv.URL, 2*time.Second) {
		t.Fatal("waitForApp = false, want true (server is up)")
	}
	if strings.Contains(buf.String(), "waiting for app") {
		t.Errorf("instant-ready app should print no waiting message; got %q", buf.String())
	}

	buf.Reset()
	if waitForApp(&buf, "http://127.0.0.1:1", 100*time.Millisecond) {
		t.Fatal("waitForApp = true, want false (nothing listens on port 1)")
	}
	for _, want := range []string{"waiting for app at", "not responding"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("output missing %q; got %q", want, buf.String())
		}
	}
}

func TestMaybeWarnLanExposure(t *testing.T) {
	cases := []struct {
		bind       string
		wantOutput bool
	}{
		{"127.0.0.1", false},
		{"0.0.0.0", true},
	}
	for _, c := range cases {
		cfg := &config.Config{
			Project: config.ProjectConfig{Name: "x"},
			Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
			Dev:     config.DevConfig{Bind: c.bind},
		}
		var buf bytes.Buffer
		maybeWarnLanExposure(&buf, cfg)
		got := buf.String()
		if c.wantOutput {
			if !strings.Contains(got, "LAN") {
				t.Errorf("bind=%q: warning = %q, want it to mention 'LAN'", c.bind, got)
			}
			if !strings.Contains(got, c.bind) {
				t.Errorf("bind=%q: warning = %q, want it to mention the bind value", c.bind, got)
			}
		} else {
			if got != "" {
				t.Errorf("bind=%q: warning = %q, want empty (loopback is the safe default, no warning)", c.bind, got)
			}
		}
	}
}
