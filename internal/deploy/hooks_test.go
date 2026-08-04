package deploy

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/Bonnary/pier/internal/config"
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
