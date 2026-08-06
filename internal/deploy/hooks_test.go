package deploy

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bonnary/pier/internal/config"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// recordingLogger captures phase transitions and log lines so tests
// can assert what the pipeline told the user.
type recordingLogger struct {
	mu     sync.Mutex
	phases []string
	logs   []string
}

func (r *recordingLogger) Emit(Event) {}
func (r *recordingLogger) PhaseStart(name string) {
	r.mu.Lock()
	r.phases = append(r.phases, "start:"+name)
	r.mu.Unlock()
}
func (r *recordingLogger) PhaseEnd(name string, _ error) {
	r.mu.Lock()
	r.phases = append(r.phases, "end:"+name)
	r.mu.Unlock()
}
func (r *recordingLogger) Log(_ string, format string, args ...any) {
	r.mu.Lock()
	r.logs = append(r.logs, fmt.Sprintf(format, args...))
	r.mu.Unlock()
}
func (r *recordingLogger) JSON() bool        { return false }
func (r *recordingLogger) Writer() io.Writer { return io.Discard }

func TestRunHooksRunsCommandsInOrder(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("ok\n"), status: 0}
	host, port := testAddr(t, startFakeSession(t, keyOnlyServer(pub), fs))
	client := dialTestClient(t, SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath})
	defer client.Close()

	logger := &recordingLogger{}
	p := &Pipeline{DeployEnv: config.DeployConfig{Path: "/srv/x"}, Logger: logger}
	if err := p.runHooks(context.Background(), client, "before_deploy",
		[]string{"php artisan down", "php artisan cache:clear"}); err != nil {
		t.Fatalf("runHooks() = %v, want nil", err)
	}

	want := []string{
		"cd '/srv/x' && docker compose --env-file .env.production -f docker-compose.prod.yml exec -T app 'php' 'artisan' 'down'",
		"cd '/srv/x' && docker compose --env-file .env.production -f docker-compose.prod.yml exec -T app 'php' 'artisan' 'cache:clear'",
	}
	if len(fs.cmds) != len(want) {
		t.Fatalf("recorded commands = %q, want %d", fs.cmds, len(want))
	}
	for i, w := range want {
		if fs.cmds[i] != w {
			t.Errorf("command %d = %q, want %q", i, fs.cmds[i], w)
		}
	}
	if len(logger.phases) != 2 || logger.phases[0] != "start:before_deploy" || logger.phases[1] != "end:before_deploy" {
		t.Errorf("phases = %q, want [start:before_deploy end:before_deploy]", logger.phases)
	}
	if len(logger.logs) < 2 {
		t.Errorf("logs = %q, want an ok line per command", logger.logs)
	}
}

func TestRunHooksAbortsOnFirstFailure(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("boom\n"), status: 1}
	host, port := testAddr(t, startFakeSession(t, keyOnlyServer(pub), fs))
	client := dialTestClient(t, SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath})
	defer client.Close()

	logger := &recordingLogger{}
	p := &Pipeline{DeployEnv: config.DeployConfig{Path: "/srv/x"}, Logger: logger}
	err := p.runHooks(context.Background(), client, "after_deploy",
		[]string{"php artisan migrate --force", "php artisan cache:clear"})

	// The first command failed (exit status 1), so the returned error
	// must fail the deploy and the second command must never run.
	if err == nil {
		t.Fatal("runHooks() = nil, want an error (a failing hook must abort the deploy)")
	}
	if len(fs.cmds) != 1 {
		t.Fatalf("recorded commands = %q, want exactly 1 (stop at first failure)", fs.cmds)
	}
	var errLines int
	for _, l := range logger.logs {
		if strings.Contains(l, "error:") {
			errLines++
		}
	}
	if errLines != 1 {
		t.Errorf("error lines = %d, want 1; logs = %q", errLines, logger.logs)
	}
	if len(logger.phases) != 2 || logger.phases[1] != "end:after_deploy" {
		t.Errorf("phases = %q, want [start:after_deploy end:after_deploy]", logger.phases)
	}
}

func TestRunHooksEmptyListSkipsPhase(t *testing.T) {
	logger := &recordingLogger{}
	p := &Pipeline{Logger: logger}
	if err := p.runHooks(context.Background(), nil, "before_deploy", nil); err != nil {
		t.Fatalf("runHooks() = %v, want nil (empty list)", err)
	}
	if len(logger.phases) != 0 {
		t.Errorf("phases = %q, want none (empty list skips the phase)", logger.phases)
	}
}

func TestRunHooksQuotedEntryBecomesOneArg(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("ok\n"), status: 0}
	host, port := testAddr(t, startFakeSession(t, keyOnlyServer(pub), fs))
	client := dialTestClient(t, SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath})
	defer client.Close()

	p := &Pipeline{DeployEnv: config.DeployConfig{Path: "/srv/x"}, Logger: &recordingLogger{}}
	if err := p.runHooks(context.Background(), client, "before_deploy", []string{`php artisan "migrate --force"`}); err != nil {
		t.Fatalf("runHooks() = %v, want nil", err)
	}

	want := "cd '/srv/x' && docker compose --env-file .env.production -f docker-compose.prod.yml exec -T app 'php' 'artisan' 'migrate --force'"
	if len(fs.cmds) != 1 || fs.cmds[0] != want {
		t.Errorf("command = %q, want %q", fs.cmds, want)
	}
}

func TestRunHooksStopsOnCancelledContext(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("boom\n"), status: 1}
	host, port := testAddr(t, startFakeSession(t, keyOnlyServer(pub), fs))
	client := dialTestClient(t, SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath})
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Cancel while the first command is still in flight, mirroring a
	// Ctrl+C mid-deploy: the remaining hooks must not run.
	recorded := make(chan struct{})
	go func() {
		for {
			fs.mu.Lock()
			n := len(fs.cmds)
			fs.mu.Unlock()
			if n >= 1 {
				cancel()
				close(recorded)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	logger := &recordingLogger{}
	p := &Pipeline{DeployEnv: config.DeployConfig{Path: "/srv/x"}, Logger: logger}
	err := p.runHooks(ctx, client, "before_deploy",
		[]string{"php artisan down", "php artisan cache:clear"})
	<-recorded

	// The first command ran (it was already in flight when the context
	// was cancelled) but the second one never does, and the returned
	// error fails the deploy.
	if err == nil {
		t.Fatal("runHooks() = nil, want an error after cancellation aborted the in-flight command")
	}
	if len(fs.cmds) != 1 {
		t.Fatalf("recorded commands = %q, want exactly 1 (the second must not run after cancel)", fs.cmds)
	}
	// Only the in-flight first command's failure line appears.
	var errLines int
	for _, l := range logger.logs {
		if strings.Contains(l, "error:") {
			errLines++
		}
	}
	if errLines != 1 {
		t.Errorf("error lines = %d, want 1; logs = %q", errLines, logger.logs)
	}
}

// startPipelineServer starts an SSH server that serves both the sftp
// subsystem (used by the sync phase) and session exec requests
// (recorded on fs, used by the build/hooks/up phases) — the full
// command surface the deploy pipeline uses.
func startPipelineServer(t *testing.T, scfg *ssh.ServerConfig, fs *fakeSession) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("host key signer: %v", err)
	}
	scfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go servePipelineConn(nc, scfg, fs)
		}
	}()
	return ln.Addr().String()
}

func servePipelineConn(nc net.Conn, scfg *ssh.ServerConfig, fs *fakeSession) {
	conn, chans, reqs, err := ssh.NewServerConn(nc, scfg)
	if err != nil {
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs)
	for ch := range chans {
		go servePipelineChannel(ch, fs)
	}
}

func servePipelineChannel(ch ssh.NewChannel, fs *fakeSession) {
	if ch.ChannelType() != "session" {
		_ = ch.Reject(ssh.UnknownChannelType, "unsupported channel type")
		return
	}
	channel, reqs, err := ch.Accept()
	if err != nil {
		return
	}
	defer channel.Close()
	for req := range reqs {
		switch req.Type {
		case "subsystem":
			if string(req.Payload[4:]) == "sftp" {
				_ = req.Reply(true, nil)
				srv, err := sftp.NewServer(channel)
				if err != nil {
					return
				}
				_ = srv.Serve()
				return
			}
			_ = req.Reply(false, nil)
		case "exec":
			if fs.reject {
				_ = req.Reply(false, nil)
				return
			}
			cmd := string(req.Payload[4:])
			fs.addCmd(cmd)
			_ = req.Reply(true, nil)
			if fs.captureStdin {
				b, err := io.ReadAll(channel)
				// Only record non-empty captures: commands that never
				// set stdin (e.g. the docker tag Run after a docker
				// load StreamIn) send an immediate CloseWrite, and
				// their empty drain must not overwrite the streamed
				// payload of the command under test.
				if err == nil && len(b) > 0 {
					fs.setStdin(b)
				}
			}
			_, _ = channel.Write(fs.output)
			// The stream tests read stderr (StreamIn) and stdout
			// (StreamOut) separately; mirror the output on the stderr
			// extended-data stream (code 1, the SSH spec's
			// SSH_EXTENDED_DATA_STDERR) so both paths see it.
			if ext, ok := channel.(interface {
				WriteExtended([]byte, uint32) (int, error)
			}); ok {
				_, _ = ext.WriteExtended(fs.output, 1)
			}
			st := fs.status
			if fs.statusFn != nil {
				st = fs.statusFn(cmd)
			}
			finishFakeSession(channel, st)
			return
		}
	}
}

// writeRemoteState records a deploy state on a (temp-dir) deploy
// host, so the pipeline treats it as an existing deployment rather
// than a first deploy. Current only — no Previous — so a rollback
// reports "no previous deploy" (and an after_deploy or health
// failure surfaces the cause directly instead of the rollback error).
func writeRemoteState(t *testing.T, remote string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(remote, ".pier"), 0755); err != nil {
		t.Fatalf("mkdir .pier: %v", err)
	}
	if err := SaveState(remote, &State{Current: "sha1"}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
}

// seedEnvFile writes a minimal local .env.production into the cwd so
// the pipeline's render phase treats the env file as user-owned and
// does not abort the deploy with the fresh-template guard.
func seedEnvFile(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(".env.production", []byte("APP_KEY=test\n"), 0644); err != nil {
		t.Fatalf("write .env.production: %v", err)
	}
}

// TestPipelineRunsHooksAtCorrectStages drives the full pipeline
// against an in-process SSH server (sftp + exec) and asserts the
// recorded remote commands are ordered build → before_deploy → up →
// nginx reload → after_deploy. The health probe targets a dead port,
// so the run ends in the rollback path (up-phase error) after the
// hook commands were recorded.
func TestPipelineRunsHooksAtCorrectStages(t *testing.T) {
	t.Chdir(t.TempDir())
	seedEnvFile(t)
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("ok\n"), status: 0}
	host, port := testAddr(t, startPipelineServer(t, keyOnlyServer(pub), fs))
	remote := t.TempDir()
	writeRemoteState(t, remote)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {
				Host: host, User: "deploy", Path: remote, Branch: "main",
				BeforeDeploy: []string{"php artisan down"},
				AfterDeploy:  []string{"php artisan migrate --force"},
			},
		},
	}

	origProbe, origEnsure := pipelineProbe, pipelineEnsurePath
	pipelineProbe = func(ctx context.Context, r stdinRunner) (bool, error) { return true, nil }
	pipelineEnsurePath = func(ctx context.Context, c *Client, path string) error { return nil }
	defer func() { pipelineProbe, pipelineEnsurePath = origProbe, origEnsure }()

	p := &Pipeline{
		Config:    cfg,
		Env:       "production",
		DeployEnv: cfg.Deploy["production"],
		Logger:    discardLogger{},
		SSH:       SSHConfig{Host: host, User: "deploy", Port: port, KeyPath: keyPath},
		Health:    HealthConfig{URL: "http://127.0.0.1:1/up", Timeout: time.Second, Interval: 50 * time.Millisecond, MaxAttempts: 1},
		Now:       time.Now,
	}
	err := p.Run(context.Background())

	// Health probe fails against port 1 → rollback → no previous
	// deploy on record → up-phase error. The commands recorded before
	// that are what we assert: build, tag, before_deploy, up, reload,
	// after_deploy.
	if !errors.Is(err, ErrUp) {
		t.Fatalf("Run() = %v, want ErrUp (health failed, rollback path)", err)
	}
	if len(fs.cmds) < 6 {
		t.Fatalf("recorded commands = %q, want at least 6", fs.cmds)
	}
	if !strings.Contains(fs.cmds[0], "build --pull") {
		t.Errorf("command 0 = %q, want the build command", fs.cmds[0])
	}
	if !strings.Contains(fs.cmds[1], "docker tag x:latest x:") {
		t.Errorf("command 1 = %q, want the docker tag command", fs.cmds[1])
	}
	wantBefore := "cd '" + remote + "' && docker compose --env-file .env.production -f docker-compose.prod.yml exec -T app 'php' 'artisan' 'down'"
	if fs.cmds[2] != wantBefore {
		t.Errorf("command 2 = %q, want before_deploy hook %q", fs.cmds[2], wantBefore)
	}
	if !strings.Contains(fs.cmds[3], "up -d") {
		t.Errorf("command 3 = %q, want the up command", fs.cmds[3])
	}
	if !strings.Contains(fs.cmds[4], "nginx -s reload") {
		t.Errorf("command 4 = %q, want the nginx reload", fs.cmds[4])
	}
	wantAfter := "cd '" + remote + "' && docker compose --env-file .env.production -f docker-compose.prod.yml exec -T app 'php' 'artisan' 'migrate' '--force'"
	if fs.cmds[5] != wantAfter {
		t.Errorf("command 5 = %q, want after_deploy hook %q", fs.cmds[5], wantAfter)
	}
}

// TestPipelineSkipsHooksWhenListsEmpty asserts that an env without
// hook lists records no hook commands: exactly build, up, reload.
func TestPipelineSkipsHooksWhenListsEmpty(t *testing.T) {
	t.Chdir(t.TempDir())
	seedEnvFile(t)
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("ok\n"), status: 0}
	host, port := testAddr(t, startPipelineServer(t, keyOnlyServer(pub), fs))
	remote := t.TempDir()

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: host, User: "deploy", Path: remote, Branch: "main"},
		},
	}

	origProbe, origEnsure := pipelineProbe, pipelineEnsurePath
	pipelineProbe = func(ctx context.Context, r stdinRunner) (bool, error) { return true, nil }
	pipelineEnsurePath = func(ctx context.Context, c *Client, path string) error { return nil }
	defer func() { pipelineProbe, pipelineEnsurePath = origProbe, origEnsure }()

	p := &Pipeline{
		Config:    cfg,
		Env:       "production",
		DeployEnv: cfg.Deploy["production"],
		Logger:    discardLogger{},
		SSH:       SSHConfig{Host: host, User: "deploy", Port: port, KeyPath: keyPath},
		Health:    HealthConfig{URL: "http://127.0.0.1:1/up", Timeout: time.Second, Interval: 50 * time.Millisecond, MaxAttempts: 1},
		Now:       time.Now,
	}
	err := p.Run(context.Background())
	if !errors.Is(err, ErrUp) {
		t.Fatalf("Run() = %v, want ErrUp (health failed, rollback path)", err)
	}
	if len(fs.cmds) != 4 {
		t.Fatalf("recorded commands = %q, want exactly 4 (build, tag, up, reload) with no hooks", fs.cmds)
	}
}

// TestPipelineBeforeDeployFailureAborts asserts a failing
// before_deploy hook fails the deploy (ErrHooks) before the new
// release is brought up: only the build and the failing hook command
// are recorded.
func TestPipelineBeforeDeployFailureAborts(t *testing.T) {
	t.Chdir(t.TempDir())
	seedEnvFile(t)
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("ok\n"), status: 0, statusFn: func(cmd string) int {
		if strings.Contains(cmd, "'artisan' 'down'") {
			return 1
		}
		return 0
	}}
	host, port := testAddr(t, startPipelineServer(t, keyOnlyServer(pub), fs))
	remote := t.TempDir()
	writeRemoteState(t, remote)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {
				Host: host, User: "deploy", Path: remote, Branch: "main",
				BeforeDeploy: []string{"php artisan down"},
			},
		},
	}

	origProbe, origEnsure := pipelineProbe, pipelineEnsurePath
	pipelineProbe = func(ctx context.Context, r stdinRunner) (bool, error) { return true, nil }
	pipelineEnsurePath = func(ctx context.Context, c *Client, path string) error { return nil }
	defer func() { pipelineProbe, pipelineEnsurePath = origProbe, origEnsure }()

	p := &Pipeline{
		Config:    cfg,
		Env:       "production",
		DeployEnv: cfg.Deploy["production"],
		Logger:    discardLogger{},
		SSH:       SSHConfig{Host: host, User: "deploy", Port: port, KeyPath: keyPath},
		Health:    HealthConfig{URL: "http://127.0.0.1:1/up", Timeout: time.Second, Interval: 50 * time.Millisecond, MaxAttempts: 1},
		Now:       time.Now,
	}
	err := p.Run(context.Background())

	if !errors.Is(err, ErrHooks) {
		t.Fatalf("Run() = %v, want ErrHooks (before_deploy hook failed)", err)
	}
	if len(fs.cmds) != 3 {
		t.Fatalf("recorded commands = %q, want exactly 3 (build, tag, failing before_deploy hook; up must not run)", fs.cmds)
	}
	if !strings.Contains(fs.cmds[2], "'artisan' 'down'") {
		t.Errorf("command 2 = %q, want the before_deploy hook command", fs.cmds[2])
	}
}

// TestPipelineAfterDeployFailureFirstDeployReportsHook asserts a
// failing after_deploy hook on a first deploy (no previous image on
// record) fails the deploy with the hook's own error: rollback is
// skipped because there is nothing to roll back to, so the user sees
// the actual hook failure (exit code 7) instead of a dead-end "no
// previous deploy to roll back to" message.
func TestPipelineAfterDeployFailureFirstDeployReportsHook(t *testing.T) {
	t.Chdir(t.TempDir())
	seedEnvFile(t)
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("ok\n"), status: 0, statusFn: func(cmd string) int {
		if strings.Contains(cmd, "'migrate'") {
			return 1
		}
		return 0
	}}
	host, port := testAddr(t, startPipelineServer(t, keyOnlyServer(pub), fs))
	remote := t.TempDir()
	writeRemoteState(t, remote)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {
				Host: host, User: "deploy", Path: remote, Branch: "main",
				AfterDeploy: []string{"php artisan migrate --force"},
			},
		},
	}

	origProbe, origEnsure := pipelineProbe, pipelineEnsurePath
	pipelineProbe = func(ctx context.Context, r stdinRunner) (bool, error) { return true, nil }
	pipelineEnsurePath = func(ctx context.Context, c *Client, path string) error { return nil }
	defer func() { pipelineProbe, pipelineEnsurePath = origProbe, origEnsure }()

	p := &Pipeline{
		Config:    cfg,
		Env:       "production",
		DeployEnv: cfg.Deploy["production"],
		Logger:    discardLogger{},
		SSH:       SSHConfig{Host: host, User: "deploy", Port: port, KeyPath: keyPath},
		Health:    HealthConfig{URL: "http://127.0.0.1:1/up", Timeout: time.Second, Interval: 50 * time.Millisecond, MaxAttempts: 1},
		Now:       time.Now,
	}
	err := p.Run(context.Background())

	if !errors.Is(err, ErrHooks) {
		t.Fatalf("Run() = %v, want ErrHooks (after_deploy hook failed; no previous deploy to roll back to, so the hook error must surface)", err)
	}
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != ExitHooks {
		t.Fatalf("Run() error = %T, want *ExitError with code %d", err, ExitHooks)
	}
	if len(fs.cmds) != 5 {
		t.Fatalf("recorded commands = %q, want exactly 5 (build, tag, up, reload, failing after_deploy hook; rollback must be skipped on a first deploy)", fs.cmds)
	}
	if !strings.Contains(fs.cmds[4], "'migrate'") {
		t.Errorf("command 4 = %q, want the failing after_deploy hook command", fs.cmds[4])
	}
}

// TestPipelineAfterDeployFailureRollsBackToPrevious asserts a failing
// after_deploy hook with a previous image on record takes the
// rollback path: the previous image is retagged and re-upped before
// the hook error (ErrHooks) is reported.
func TestPipelineAfterDeployFailureRollsBackToPrevious(t *testing.T) {
	t.Chdir(t.TempDir())
	seedEnvFile(t)
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("ok\n"), status: 0, statusFn: func(cmd string) int {
		if strings.Contains(cmd, "'migrate'") {
			return 1
		}
		return 0
	}}
	host, port := testAddr(t, startPipelineServer(t, keyOnlyServer(pub), fs))
	remote := t.TempDir()
	if err := SaveState(remote, &State{Current: "new", Previous: "old"}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {
				Host: host, User: "deploy", Path: remote, Branch: "main",
				AfterDeploy: []string{"php artisan migrate --force"},
			},
		},
	}

	origProbe, origEnsure := pipelineProbe, pipelineEnsurePath
	pipelineProbe = func(ctx context.Context, r stdinRunner) (bool, error) { return true, nil }
	pipelineEnsurePath = func(ctx context.Context, c *Client, path string) error { return nil }
	defer func() { pipelineProbe, pipelineEnsurePath = origProbe, origEnsure }()

	p := &Pipeline{
		Config:    cfg,
		Env:       "production",
		DeployEnv: cfg.Deploy["production"],
		Logger:    discardLogger{},
		SSH:       SSHConfig{Host: host, User: "deploy", Port: port, KeyPath: keyPath},
		Health:    HealthConfig{URL: "http://127.0.0.1:1/up", Timeout: time.Second, Interval: 50 * time.Millisecond, MaxAttempts: 1},
		Now:       time.Now,
	}
	err := p.Run(context.Background())

	if !errors.Is(err, ErrHooks) {
		t.Fatalf("Run() = %v, want ErrHooks (after_deploy hook failed)", err)
	}
	// build, tag, up, nginx reload, failing after_deploy hook, then
	// the rollback: retag previous image, up again, nginx reload.
	if len(fs.cmds) != 8 {
		t.Fatalf("recorded commands = %q, want exactly 8 (build, tag, up, reload, failing hook, rollback tag, up, reload)", fs.cmds)
	}
	if !strings.Contains(fs.cmds[5], "docker tag x:old x:current") {
		t.Errorf("command 5 = %q, want the rollback retag of the previous image", fs.cmds[5])
	}
	if !strings.Contains(fs.cmds[6], "up -d --wait") {
		t.Errorf("command 6 = %q, want the rollback up", fs.cmds[6])
	}
	if !strings.Contains(fs.cmds[7], "nginx -s reload") {
		t.Errorf("command 7 = %q, want the rollback nginx reload", fs.cmds[7])
	}
}

// TestPipelineBeforeDeploySkippedOnFirstDeploy asserts before_deploy
// never runs when the remote host has no deploy state yet (no app
// container exists): only build, up, and the nginx reload run, and
// after_deploy still executes against the fresh release.
func TestPipelineBeforeDeploySkippedOnFirstDeploy(t *testing.T) {
	t.Chdir(t.TempDir())
	seedEnvFile(t)
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("ok\n"), status: 0}
	host, port := testAddr(t, startPipelineServer(t, keyOnlyServer(pub), fs))
	remote := t.TempDir()

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {
				Host: host, User: "deploy", Path: remote, Branch: "main",
				BeforeDeploy: []string{"php artisan down"},
				AfterDeploy:  []string{"php artisan migrate --force"},
			},
		},
	}

	origProbe, origEnsure := pipelineProbe, pipelineEnsurePath
	pipelineProbe = func(ctx context.Context, r stdinRunner) (bool, error) { return true, nil }
	pipelineEnsurePath = func(ctx context.Context, c *Client, path string) error { return nil }
	defer func() { pipelineProbe, pipelineEnsurePath = origProbe, origEnsure }()

	p := &Pipeline{
		Config:    cfg,
		Env:       "production",
		DeployEnv: cfg.Deploy["production"],
		Logger:    discardLogger{},
		SSH:       SSHConfig{Host: host, User: "deploy", Port: port, KeyPath: keyPath},
		Health:    HealthConfig{URL: "http://127.0.0.1:1/up", Timeout: time.Second, Interval: 50 * time.Millisecond, MaxAttempts: 1},
		Now:       time.Now,
	}
	err := p.Run(context.Background())
	if !errors.Is(err, ErrUp) {
		t.Fatalf("Run() = %v, want ErrUp (health failed, rollback path)", err)
	}

	// build, tag, up, nginx reload, after_deploy — no before_deploy
	// command.
	if len(fs.cmds) != 5 {
		t.Fatalf("recorded commands = %q, want exactly 5 (build, tag, up, reload, after_deploy)", fs.cmds)
	}
	for _, cmd := range fs.cmds {
		if strings.Contains(cmd, "'artisan' 'down'") {
			t.Errorf("before_deploy hook ran on first deploy: %q", cmd)
		}
	}
	if !strings.Contains(fs.cmds[4], "'migrate'") {
		t.Errorf("command 4 = %q, want the after_deploy hook command", fs.cmds[4])
	}
}
