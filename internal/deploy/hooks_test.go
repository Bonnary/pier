package deploy

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
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
	p.runHooks(context.Background(), client, "before_deploy",
		[]string{"php artisan down", "php artisan cache:clear"})

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

func TestRunHooksWarnsAndContinuesOnFailure(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("boom\n"), status: 1}
	host, port := testAddr(t, startFakeSession(t, keyOnlyServer(pub), fs))
	client := dialTestClient(t, SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath})
	defer client.Close()

	logger := &recordingLogger{}
	p := &Pipeline{DeployEnv: config.DeployConfig{Path: "/srv/x"}, Logger: logger}
	p.runHooks(context.Background(), client, "after_deploy",
		[]string{"php artisan migrate --force", "php artisan cache:clear"})

	// Both commands still ran despite both failing (exit status 1).
	if len(fs.cmds) != 2 {
		t.Fatalf("recorded commands = %q, want 2 (continue after failure)", fs.cmds)
	}
	var warnings int
	for _, l := range logger.logs {
		if strings.Contains(l, "warning:") {
			warnings++
		}
	}
	if warnings != 2 {
		t.Errorf("warning lines = %d, want 2; logs = %q", warnings, logger.logs)
	}
}

func TestRunHooksEmptyListSkipsPhase(t *testing.T) {
	logger := &recordingLogger{}
	p := &Pipeline{Logger: logger}
	p.runHooks(context.Background(), nil, "before_deploy", nil)
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
	p.runHooks(context.Background(), client, "before_deploy", []string{`php artisan "migrate --force"`})

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
	p.runHooks(ctx, client, "before_deploy",
		[]string{"php artisan down", "php artisan cache:clear"})
	<-recorded

	// The first command ran (it was already in flight when the context
	// was cancelled) but the second one never does.
	if len(fs.cmds) != 1 {
		t.Fatalf("recorded commands = %q, want exactly 1 (the second must not run after cancel)", fs.cmds)
	}
	// Only the in-flight first command's failure warning appears.
	var warnings int
	for _, l := range logger.logs {
		if strings.Contains(l, "warning:") {
			warnings++
		}
	}
	if warnings != 1 {
		t.Errorf("warning lines = %d, want 1; logs = %q", warnings, logger.logs)
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
			fs.addCmd(string(req.Payload[4:]))
			_ = req.Reply(true, nil)
			_, _ = channel.Write(fs.output)
			finishFakeSession(channel, fs.status)
			return
		}
	}
}

// TestPipelineRunsHooksAtCorrectStages drives the full pipeline
// against an in-process SSH server (sftp + exec) and asserts the
// recorded remote commands are ordered build → before_deploy → up →
// nginx reload → after_deploy. The health probe targets a dead port,
// so the run ends in the rollback path (up-phase error) after the
// hook commands were recorded.
func TestPipelineRunsHooksAtCorrectStages(t *testing.T) {
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

	// Health probe fails against port 1 → rollback → no previous
	// deploy on record → up-phase error. The commands recorded before
	// that are what we assert.
	if !errors.Is(err, ErrUp) {
		t.Fatalf("Run() = %v, want ErrUp (health failed, rollback path)", err)
	}
	if len(fs.cmds) < 5 {
		t.Fatalf("recorded commands = %q, want at least 5", fs.cmds)
	}
	if !strings.Contains(fs.cmds[0], "build --pull") {
		t.Errorf("command 0 = %q, want the build command", fs.cmds[0])
	}
	wantBefore := "cd '" + remote + "' && docker compose --env-file .env.production -f docker-compose.prod.yml exec -T app 'php' 'artisan' 'down'"
	if fs.cmds[1] != wantBefore {
		t.Errorf("command 1 = %q, want before_deploy hook %q", fs.cmds[1], wantBefore)
	}
	if !strings.Contains(fs.cmds[2], "up -d") {
		t.Errorf("command 2 = %q, want the up command", fs.cmds[2])
	}
	if !strings.Contains(fs.cmds[3], "nginx -s reload") {
		t.Errorf("command 3 = %q, want the nginx reload", fs.cmds[3])
	}
	wantAfter := "cd '" + remote + "' && docker compose --env-file .env.production -f docker-compose.prod.yml exec -T app 'php' 'artisan' 'migrate' '--force'"
	if fs.cmds[4] != wantAfter {
		t.Errorf("command 4 = %q, want after_deploy hook %q", fs.cmds[4], wantAfter)
	}
}

// TestPipelineSkipsHooksWhenListsEmpty asserts that an env without
// hook lists records no hook commands: exactly build, up, reload.
func TestPipelineSkipsHooksWhenListsEmpty(t *testing.T) {
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
	if len(fs.cmds) != 3 {
		t.Fatalf("recorded commands = %q, want exactly 3 (build, up, reload) with no hooks", fs.cmds)
	}
}
