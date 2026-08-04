# Pre/Post Deploy Commands Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let users declare per-env command lists in pier.toml that run inside the app container before and after a production deploy (e.g. `php artisan down` before, `php artisan migrate --force` after).

**Architecture:** `[deploy.<env>]` gains `before_deploy` / `after_deploy` string arrays. A shellwords-style tokenizer in `internal/config` splits each entry; the deploy pipeline runs them through the existing `docker compose exec -T app` machinery (`remoteExecCommand`) with warn-and-continue failure semantics. `pier init` renders both keys commented out, bind-style.

**Tech Stack:** Go 1.25, BurntSushi/toml, cobra, x/crypto/ssh, pkg/sftp (tests), no new dependencies.

## Global Constraints

- Commands run **in the app container** on the deploy host: `docker compose --env-file .env.production -f docker-compose.prod.yml exec -T app <args>`.
- Placement: `before_deploy` between **build** and **up** (old release still serving); `after_deploy` between **up** (incl. nginx reload) and **health probe**.
- A failed hook logs a warning and the deploy continues; hooks never abort the deploy. Hooks never run during `pier rollback`.
- Empty list ⇒ phase skipped entirely (no `PhaseStart`).
- Tokenizer lives in `internal/config` (deploy imports config; config must not import deploy).
- Validation rejects entries that tokenize to zero args or fail to tokenize, naming `deploy.<env>.<key>[<i>]`.
- `pier init` renders `# before_deploy = ["php artisan down"]` and `# after_deploy = ["php artisan migrate --force"]` commented out per `[deploy.<env>]`; real values render uncommented when set.
- No env-var interpolation, no per-command flags, no dev-flow hooks.

## File Structure

| File | Responsibility |
|---|---|
| `internal/config/split.go` (new) | `SplitCommand` shellwords tokenizer |
| `internal/config/split_test.go` (new) | Tokenizer unit tests |
| `internal/config/config.go` (modify) | `DeployConfig.BeforeDeploy` / `.AfterDeploy` fields |
| `internal/config/parse.go` (modify) | `validateHookList` + wiring in `Validate` |
| `internal/config/parse_test.go` (modify) | Load/validate tests for hook lists |
| `internal/config/testdata/hooks.toml` (new) | Fixture with hook lists |
| `internal/deploy/hooks.go` (new) | `(*Pipeline).runHooks` executor |
| `internal/deploy/hooks_test.go` (new) | `runHooks` tests + recording logger + combined SSH test server + pipeline placement test |
| `internal/deploy/deploy.go` (modify) | Phase placement in `Run` |
| `internal/cli/toml.go` (modify) | Render hook keys (real or commented) in generated pier.toml |
| `internal/cli/toml_test.go` (new) | `tomlEncode` hook rendering tests |
| `README.md`, `CHANGELOG.md` (modify) | Docs |

---

### Task 1: Shellwords tokenizer `SplitCommand`

**Files:**
- Create: `internal/config/split.go`
- Test: `internal/config/split_test.go`

**Interfaces:**
- Produces: `func SplitCommand(line string) ([]string, error)` — splits on whitespace honoring single/double quotes and backslash escapes; returns `([], nil)` for empty/whitespace-only input; errors on unterminated quote or trailing backslash. Used by Task 2 (validation) and Task 3 (execution).

- [ ] **Step 1: Write the failing tests**

Create `internal/config/split_test.go`:

```go
package config

import "testing"

func TestSplitCommand(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"plain args", "php artisan migrate", []string{"php", "artisan", "migrate"}},
		{"flag with value", "php artisan migrate --force", []string{"php", "artisan", "migrate", "--force"}},
		{"double-quoted space", `php artisan "migrate --force"`, []string{"php", "artisan", "migrate --force"}},
		{"single-quoted space", "php artisan 'migrate --force'", []string{"php", "artisan", "migrate --force"}},
		{"escaped space", `php artisan migrate\ --force`, []string{"php", "artisan", "migrate --force"}},
		{"collapsed whitespace", "  php   artisan\tmigrate  ", []string{"php", "artisan", "migrate"}},
		{"empty string", "", nil},
		{"only whitespace", "   \t ", nil},
		{"empty quoted arg", `php ''`, []string{"php", ""}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := SplitCommand(c.in)
			if err != nil {
				t.Fatalf("SplitCommand(%q): %v", c.in, err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("SplitCommand(%q) = %q, want %q", c.in, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("SplitCommand(%q) = %q, want %q", c.in, got, c.want)
				}
			}
		})
	}
}

func TestSplitCommandErrors(t *testing.T) {
	for _, in := range []string{`php "unterminated`, `php 'unterminated`, `php \`} {
		if _, err := SplitCommand(in); err == nil {
			t.Errorf("SplitCommand(%q) = nil error, want error", in)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -run TestSplitCommand -v`
Expected: FAIL — `undefined: SplitCommand`

- [ ] **Step 3: Implement the tokenizer**

Create `internal/config/split.go`:

```go
package config

import (
	"fmt"
	"strings"
)

// SplitCommand tokenizes a command line into arguments. Whitespace
// separates arguments; single quotes, double quotes, and backslash
// escapes preserve literal whitespace inside an argument (there is no
// environment expansion). An unterminated quote or a trailing
// backslash returns an error. Empty input returns an empty slice.
func SplitCommand(line string) ([]string, error) {
	var (
		args    []string
		cur     strings.Builder
		quote   rune
		escaped bool
		hasCur  bool
	)
	flush := func() {
		args = append(args, cur.String())
		cur.Reset()
		hasCur = false
	}
	for _, ch := range line {
		if escaped {
			cur.WriteRune(ch)
			hasCur = true
			escaped = false
			continue
		}
		switch {
		case ch == '\\' && quote != '\'':
			escaped = true
			hasCur = true
		case quote != 0:
			if ch == quote {
				quote = 0
			} else {
				cur.WriteRune(ch)
				hasCur = true
			}
		case ch == '\'' || ch == '"':
			quote = ch
			hasCur = true
		case ch == ' ' || ch == '\t':
			if hasCur {
				flush()
			}
		default:
			cur.WriteRune(ch)
			hasCur = true
		}
	}
	if escaped {
		return nil, fmt.Errorf("trailing backslash")
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	if hasCur {
		flush()
	}
	return args, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -run TestSplitCommand -v`
Expected: PASS (9 sub-tests + 1 error test)

- [ ] **Step 5: Commit**

```bash
git add internal/config/split.go internal/config/split_test.go
git commit -m "feat(config): shellwords tokenizer for hook commands"
```

---

### Task 2: Config fields and validation

**Files:**
- Modify: `internal/config/config.go` (`DeployConfig`, lines 81-88)
- Modify: `internal/config/parse.go` (`Validate`, lines 81-85; add helper)
- Test: `internal/config/parse_test.go`
- Create: `internal/config/testdata/hooks.toml`

**Interfaces:**
- Consumes: `SplitCommand` from Task 1.
- Produces: `config.DeployConfig{... BeforeDeploy []string; AfterDeploy []string}` (TOML keys `before_deploy`, `after_deploy`). Consumed by Tasks 3 and 5.

- [ ] **Step 1: Write the failing tests**

Create `internal/config/testdata/hooks.toml`:

```toml
[project]
name = "myapp"
domain = "myapp.example.com"

[stack]
type = "laravel"
php = "8.3"
node = "22"

[deploy.production]
host = "prod.example.com"
user = "deploy"
path = "/srv/myapp"
branch = "main"
before_deploy = ["php artisan down"]
after_deploy = ["php artisan migrate --force", "php artisan cache:clear"]
```

Append to `internal/config/parse_test.go`:

```go
func TestLoadHookLists(t *testing.T) {
	cfg, err := Load(filepath.Join("testdata", "hooks.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	prod := cfg.Deploy["production"]
	if len(prod.BeforeDeploy) != 1 || prod.BeforeDeploy[0] != "php artisan down" {
		t.Errorf("BeforeDeploy = %q, want [php artisan down]", prod.BeforeDeploy)
	}
	if len(prod.AfterDeploy) != 2 || prod.AfterDeploy[0] != "php artisan migrate --force" || prod.AfterDeploy[1] != "php artisan cache:clear" {
		t.Errorf("AfterDeploy = %q, want [php artisan migrate --force php artisan cache:clear]", prod.AfterDeploy)
	}
}

func TestValidateHookListAcceptsValid(t *testing.T) {
	c := &Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]DeployConfig{
			"production": {
				Host: "h", User: "u", Path: "p", Branch: "b",
				BeforeDeploy: []string{"php artisan down"},
				AfterDeploy:  []string{"php artisan migrate --force"},
			},
		},
	}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate = %v, want nil", err)
	}
}

func TestValidateHookListRejectsEmptyEntry(t *testing.T) {
	c := &Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]DeployConfig{
			"production": {Host: "h", User: "u", Path: "p", Branch: "b", BeforeDeploy: []string{""}},
		},
	}
	err := c.Validate()
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("Validate = %v, want ErrConfigInvalid", err)
	}
	if !strings.Contains(err.Error(), "before_deploy") {
		t.Errorf("err = %v, want it to mention before_deploy", err)
	}
}

func TestValidateHookListRejectsWhitespaceOnlyEntry(t *testing.T) {
	c := &Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]DeployConfig{
			"production": {Host: "h", User: "u", Path: "p", Branch: "b", AfterDeploy: []string{"   "}},
		},
	}
	err := c.Validate()
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("Validate = %v, want ErrConfigInvalid", err)
	}
	if !strings.Contains(err.Error(), "after_deploy") {
		t.Errorf("err = %v, want it to mention after_deploy", err)
	}
}

func TestValidateHookListRejectsUnterminatedQuote(t *testing.T) {
	c := &Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]DeployConfig{
			"production": {Host: "h", User: "u", Path: "p", Branch: "b", BeforeDeploy: []string{`php "unterminated`}},
		},
	}
	if err := c.Validate(); !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("Validate = %v, want ErrConfigInvalid", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -run 'TestLoadHookLists|TestValidateHookList' -v`
Expected: FAIL — fields do not exist (unknown `prod.BeforeDeploy`, etc.)

- [ ] **Step 3: Add the fields**

In `internal/config/config.go`, extend `DeployConfig`:

```go
type DeployConfig struct {
	Host   string         `toml:"host"`
	User   string         `toml:"user"`
	Path   string         `toml:"path"`
	Branch string         `toml:"branch"`
	Ports  map[string]int `toml:"ports"`
	TLS    bool           `toml:"tls"`
	// BeforeDeploy runs inside the app container on the deploy host
	// after the image build, while the old release is still serving.
	// A failing command logs a warning and the deploy continues.
	BeforeDeploy []string `toml:"before_deploy"`
	// AfterDeploy runs inside the app container on the deploy host
	// after `docker compose up` (and the nginx reload), before the
	// health probe. A failing command logs a warning and the deploy
	// continues.
	AfterDeploy []string `toml:"after_deploy"`
}
```

Also update the `DeployConfig` doc comment (above the struct) to mention the hook lists.

- [ ] **Step 4: Add validation**

In `internal/config/parse.go`, replace the first deploy-validation loop (lines 81-85) with:

```go
	for env, dc := range c.Deploy {
		if dc.Host == "" || dc.User == "" || dc.Path == "" || dc.Branch == "" {
			return fmt.Errorf("%w: deploy.%s requires host, user, path, branch", ErrConfigInvalid, env)
		}
		if err := validateHookList(env, "before_deploy", dc.BeforeDeploy); err != nil {
			return err
		}
		if err := validateHookList(env, "after_deploy", dc.AfterDeploy); err != nil {
			return err
		}
	}
```

And add the helper after `Validate` (before `devPortKeys` or after it — either is fine):

```go
// validateHookList checks that every entry in a before_deploy /
// after_deploy list tokenizes to at least one argument, so a typo
// surfaces at config load instead of mid-deploy.
func validateHookList(env, key string, list []string) error {
	for i, entry := range list {
		args, err := SplitCommand(entry)
		if err != nil || len(args) == 0 {
			return fmt.Errorf("%w: deploy.%s.%s[%d] %q is not a valid non-empty command", ErrConfigInvalid, env, key, i, entry)
		}
	}
	return nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/config/`
Expected: PASS (all existing + new tests)

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/parse.go internal/config/parse_test.go internal/config/testdata/hooks.toml
git commit -m "feat(config): before_deploy/after_deploy fields with validation"
```

---

### Task 3: `runHooks` executor

**Files:**
- Create: `internal/deploy/hooks.go`
- Test: `internal/deploy/hooks_test.go`

**Interfaces:**
- Consumes: `config.SplitCommand` (Task 1); `remoteExecCommand(dir, args)` and the `runner`-surface `Client.RunStream(ctx, cmd, onLine)` (both already in `internal/deploy`); `pipelineLogger` methods via `p.Logger`.
- Produces: `func (p *Pipeline) runHooks(ctx context.Context, c *Client, name string, cmds []string)` — never returns an error; used by Task 4.

- [ ] **Step 1: Write the failing tests**

Create `internal/deploy/hooks_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/deploy/ -run TestRunHooks -v`
Expected: FAIL — `p.runHooks undefined`

- [ ] **Step 3: Implement the executor**

Create `internal/deploy/hooks.go`:

```go
package deploy

import (
	"context"
	"errors"

	"golang.org/x/crypto/ssh"

	"github.com/Bonnary/pier/internal/config"
)

// runHooks runs each command in cmds inside the app container on the
// remote deploy host, streaming output through the logger. Commands
// run in order; a failing command logs a warning and the remaining
// commands still run. runHooks never fails the deploy — a pre/post
// deploy hook cannot abort a release. An empty list skips the phase
// entirely.
func (p *Pipeline) runHooks(ctx context.Context, c *Client, name string, cmds []string) {
	if len(cmds) == 0 {
		return
	}
	p.Logger.PhaseStart(name)
	for _, line := range cmds {
		args, err := config.SplitCommand(line)
		if err != nil {
			p.Logger.Log(name, "warning: skip %q: %v", line, err)
			continue
		}
		cmd := remoteExecCommand(p.DeployEnv.Path, args)
		err = c.RunStream(ctx, cmd, func(l string) {
			p.Logger.Log(name, "%s", l)
		})
		if err != nil {
			var exitErr *ssh.ExitError
			if errors.As(err, &exitErr) {
				p.Logger.Log(name, "warning: %q exited with status %d", line, exitErr.ExitStatus())
			} else {
				p.Logger.Log(name, "warning: %q failed: %v", line, err)
			}
			continue
		}
		p.Logger.Log(name, "ok: %q", line)
	}
	p.Logger.PhaseEnd(name, nil)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/deploy/ -run TestRunHooks -v`
Expected: PASS (4 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/hooks.go internal/deploy/hooks_test.go
git commit -m "feat(deploy): run pre/post deploy hooks in the app container"
```

---

### Task 4: Pipeline placement

**Files:**
- Modify: `internal/deploy/deploy.go` (`Run`, lines 48-131; package comment, line 1-5)
- Test: `internal/deploy/hooks_test.go` (append placement test + combined test server)

**Interfaces:**
- Consumes: `(*Pipeline).runHooks` from Task 3; `config.DeployConfig.BeforeDeploy` / `.AfterDeploy` from Task 2.
- Produces: the pipeline sequence preflight → render → sync → build → `before_deploy` → up → `after_deploy` → health → commit.

- [ ] **Step 1: Write the failing placement test**

Append to `internal/deploy/hooks_test.go` (add `"crypto/ed25519"`, `"crypto/rand"`, `"net"`, `"time"` to the imports and `"github.com/pkg/sftp"`, `"golang.org/x/crypto/ssh"`):

```go
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
	wantAfter := "cd '" + remote + "' && docker compose --env-file .env.production -f docker-compose.prod.yml exec -T app 'php' 'artisan' 'migrate --force'"
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
```

Note: Task 3's `hooks_test.go` imports are `context`, `fmt`, `io`,
`strings`, `sync`, `testing`, and `github.com/Bonnary/pier/internal/config`.
The Task 4 code below additionally needs `errors`, `time`, `net`,
`crypto/ed25519`, `crypto/rand`, `github.com/pkg/sftp`, and
`golang.org/x/crypto/ssh` — add all of them to the import block when
appending these tests.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/deploy/ -run 'TestPipelineRunsHooksAtCorrectStages|TestPipelineSkipsHooksWhenListsEmpty' -v`
Expected: FAIL — commands 1 and 4 are absent (hooks never run; `fs.cmds` has only build, up, reload)

- [ ] **Step 3: Wire the phases into `Run`**

In `internal/deploy/deploy.go`:

1. Update the package doc comment (lines 1-5):

```go
// Package deploy runs the production deploy pipeline over SSH:
// preflight, render, sync, build, before_deploy hooks, up,
// after_deploy hooks, health probe, and commit (the .pier/state.json
// write that records the active image tag for `pier rollback`). The
// package owns the typed error contract (ExitError, Kind) and the SSH
// client used for every remote command.
```

2. In `Run`, after the build phase (after line 99 `p.Logger.PhaseEnd("build", nil)`), insert:

```go
	// Phase 5: before_deploy — run user hooks in the app container
	// while the old release is still serving (after the build, before
	// up). Failures warn and continue; they never abort the deploy.
	p.runHooks(ctx, client, "before_deploy", p.DeployEnv.BeforeDeploy)
```

3. Renumber the existing phases 5→6 (up), 6→7 (health), 7→8 (commit) in their comments.

4. After the up phase (after line 107 `p.Logger.PhaseEnd("up", nil)`), insert:

```go
	// Phase 7: after_deploy — run user hooks in the app container
	// against the new release (after up and the nginx reload, before
	// the health probe). Failures warn and continue; they never abort
	// the deploy.
	p.runHooks(ctx, client, "after_deploy", p.DeployEnv.AfterDeploy)
```

5. Update `Run`'s doc comment (line 48-52) from "preflight, render, sync, build, up, health probe, commit" to "preflight, render, sync, build, before_deploy hooks, up, after_deploy hooks, health probe, commit".

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/deploy/ -run 'TestPipelineRunsHooksAtCorrectStages|TestPipelineSkipsHooksWhenListsEmpty|TestRunHooks' -v`
Expected: PASS (6 tests)

Then run the whole package to confirm nothing regressed:
Run: `go test ./internal/deploy/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/deploy.go internal/deploy/hooks_test.go
git commit -m "feat(deploy): run before/after deploy hooks between pipeline stages"
```

---

### Task 5: Generated pier.toml rendering

**Files:**
- Modify: `internal/cli/toml.go` (whole file; it is 33 lines)
- Test: `internal/cli/toml_test.go` (new)

**Interfaces:**
- Consumes: `config.DeployConfig.BeforeDeploy` / `.AfterDeploy` (Task 2).
- Produces: commented template lines (empty lists) or real values (non-empty lists) in `tomlEncode` output.

- [ ] **Step 1: Write the failing tests**

Create `internal/cli/toml_test.go`:

```go
package cli

import (
	"strings"
	"testing"

	"github.com/Bonnary/pier/internal/config"
)

func TestTomlEncodeRendersCommentedHookExamples(t *testing.T) {
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "/srv/x", Branch: "main"},
		},
	}
	b, err := tomlEncode(cfg)
	if err != nil {
		t.Fatalf("tomlEncode: %v", err)
	}
	got := string(b)
	for _, want := range []string{
		`# before_deploy = ["php artisan down"]`,
		`# after_deploy = ["php artisan migrate --force"]`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("tomlEncode output missing %q; got:\n%s", want, got)
		}
	}
}

func TestTomlEncodeRendersRealHookValues(t *testing.T) {
	cfg := config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {
				Host: "h", User: "u", Path: "/srv/x", Branch: "main",
				BeforeDeploy: []string{"php artisan down"},
				AfterDeploy:  []string{"php artisan migrate --force", "php artisan cache:clear"},
			},
		},
	}
	b, err := tomlEncode(cfg)
	if err != nil {
		t.Fatalf("tomlEncode: %v", err)
	}
	got := string(b)
	if !strings.Contains(got, `before_deploy = ["php artisan down"]`) {
		t.Errorf("tomlEncode output missing real before_deploy; got:\n%s", got)
	}
	if !strings.Contains(got, `after_deploy = ["php artisan migrate --force", "php artisan cache:clear"]`) {
		t.Errorf("tomlEncode output missing real after_deploy; got:\n%s", got)
	}
	if strings.Contains(got, "# before_deploy") || strings.Contains(got, "# after_deploy") {
		t.Errorf("tomlEncode output has commented template next to real values; got:\n%s", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run TestTomlEncode -v`
Expected: FAIL — output lacks the hook lines

- [ ] **Step 3: Implement the rendering**

Replace the whole tail of `tomlEncode` — the deploy-env loop and the
dead `_ = strconv.Quote` / `return` lines (lines 24-32) — with:

```go
	for env, dc := range c.Deploy {
		fmt.Fprintf(&b, "\n[deploy.%s]\n", env)
		fmt.Fprintf(&b, "host = %q\n", dc.Host)
		fmt.Fprintf(&b, "user = %q\n", dc.User)
		fmt.Fprintf(&b, "path = %q\n", dc.Path)
		fmt.Fprintf(&b, "branch = %q\n", dc.Branch)
		if len(dc.BeforeDeploy) == 0 {
			fmt.Fprintf(&b, "# before_deploy = [%q]  # uncomment: runs in the app container before the new release starts\n", "php artisan down")
		} else {
			fmt.Fprintf(&b, "before_deploy = %s\n", tomlStringArray(dc.BeforeDeploy))
		}
		if len(dc.AfterDeploy) == 0 {
			fmt.Fprintf(&b, "# after_deploy = [%q]  # uncomment: runs in the app container after the new release is up\n", "php artisan migrate --force")
		} else {
			fmt.Fprintf(&b, "after_deploy = %s\n", tomlStringArray(dc.AfterDeploy))
		}
	}
	return b.Bytes(), nil
}
```

Add the helper at the end of `toml.go`:

```go
// tomlStringArray renders items as a TOML array of quoted strings.
func tomlStringArray(items []string) string {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, s := range items {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q", s)
	}
	b.WriteByte(']')
	return b.String()
}
```

Clean up the dead `strconv` import: remove `"strconv"` from the
import block (the `_ = strconv.Quote` line is gone with the
replacement above).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/ -run TestTomlEncode -v`
Expected: PASS (2 tests)

Also confirm the existing init test still sees the bind line:
Run: `go test ./internal/cli/ -run TestInitEmitsDevBindHint -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cli/toml.go internal/cli/toml_test.go
git commit -m "feat(cli): render before/after deploy hook keys in pier.toml"
```

---

### Task 6: Documentation

**Files:**
- Modify: `README.md` (deploy config section, around lines 274-294)
- Modify: `CHANGELOG.md` (Unreleased → Added, around lines 5-20)

**Interfaces:**
- Consumes: the behavior implemented in Tasks 1-5.

- [ ] **Step 1: Update the README example**

In `README.md`, add the two keys to the `[deploy.production]` example block (lines 274-279), right after `branch = "main"`:

```toml
[deploy.production]
host   = "prod.example.com"
user   = "deploy"
path   = "/srv/myapp"
branch = "main"
tls    = false   # false (default): plain HTTP. true: HTTPS URLs + 443 — requires the upcoming cert feature
# before_deploy = ["php artisan down"]              # uncomment: runs in the app container before the new release starts
# after_deploy = ["php artisan migrate --force"]    # uncomment: runs in the app container after the new release is up
```

- [ ] **Step 2: Add the prose paragraph**

In `README.md`, after the paragraph ending "...keep it `false` for now." (line 294), insert:

```markdown
`[deploy.<env>]` also accepts optional `before_deploy` and
`after_deploy` command lists. Each entry runs inside the app
container on the deploy host (`docker compose exec -T app`, the same
mechanism as `pier exec <env>`). `before_deploy` runs after the image
build while the old release is still serving; `after_deploy` runs
after `docker compose up` (and the nginx reload) and before the
health probe. Commands run in order; a failing command logs a warning
and the remaining commands still run — a hook failure never aborts a
deploy (migrations are best placed in `after_deploy`). `pier init`
writes both keys commented out.
```

- [ ] **Step 3: Update the changelog**

In `CHANGELOG.md`, under `## Unreleased` → `### Added`, append:

```markdown
- `[deploy.<env>].before_deploy` / `after_deploy` command lists: each
  entry runs inside the app container on the deploy host, before the
  new release starts (`before_deploy`, after the image build, while
  the old release still serves) or after it is up (`after_deploy`,
  after `docker compose up` and the nginx reload, before the health
  probe). Failures log a warning and the deploy continues; `pier
  init` writes both keys commented out.
```

- [ ] **Step 4: Verify no code changes are needed for docs and run the full suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages build, vet clean, all tests pass (integration-tagged tests are excluded by the `integration` build tag)

- [ ] **Step 5: Commit**

```bash
git add README.md CHANGELOG.md
git commit -m "docs: document before/after deploy commands"
```
