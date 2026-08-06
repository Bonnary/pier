# Build Server Modes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let `pier deploy <env>` build the production image on the dev machine or a dedicated build server instead of always on the deploy host, streaming the image to the host over SSH.

**Architecture:** A per-env `builder` setting (`host_server` / `local_machine` / `build_server`) drives the pipeline: which machines preflight dials, which files sync where, where the build runs, and whether a `transfer` phase pipes `docker save` → `docker load` between the build side and the host over pier's own SSH connections. The host compose file renders an image-only variant (`image: project:current`, no `build:`) in the image modes. Real git SHA tags replace the hardcoded `"gitsha"` and the never-called `Tag()` is wired in, fixing rollback in every mode.

**Tech Stack:** Go 1.25+, cobra, Bubble Tea, `golang.org/x/crypto/ssh`, `github.com/pkg/sftp`, `BurntSushi/toml`, `gopkg.in/yaml.v3`.

## Global Constraints

- `builder` values are exactly `"host_server"`, `"local_machine"`, `"build_server"` (spec §1).
- Absent `builder` means `host_server` (existing configs unchanged).
- `build_host` / `build_user` / `build_path` required iff `builder = "build_server"` (spec §1).
- Image tag = `git rev-parse --short HEAD`, fallback `time.Now().UTC().Format("20060102150405")` (spec §2).
- Image transfer is streamed (binary-safe, no `bufio.Scanner` on the tar path), no temp files, no cross-server keys (spec §4).
- Host compose variant in image modes: app service `image: project:current`, no `build:` key (spec §5).
- `deployFilesOnly` ships exactly `docker-compose.prod.yml`, `.env.production`, `docker/nginx/default.conf` (spec §5).
- Hooks, up, health, state.json, rollback always target the host (spec §3).
- Boundary rules (README): `cli` never calls Docker directly; `stack/laravel` never imports SSH/Docker; `deploy` never knows about Laravel.
- Run `go test -race ./...` and `golangci-lint run` before each commit.

---

### Task 1: Config model — builder fields and validation

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/parse.go`
- Test: `internal/config/parse_test.go`

**Interfaces:**
- Consumes: existing `DeployConfig` struct, `Validate()` method, `ErrConfigInvalid`.
- Produces: `DeployConfig.Builder`, `DeployConfig.BuildHost`, `DeployConfig.BuildUser`, `DeployConfig.BuildPath` (TOML keys `builder`, `build_host`, `build_user`, `build_path`); method `BuilderMode() string`; validation errors for bad `builder` and missing `build_*` fields.

- [ ] **Step 1: Write the failing tests**

Add to `internal/config/parse_test.go`:

```go
func TestValidateBuilderModes(t *testing.T) {
	base := &Config{
		Project: ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]DeployConfig{
			"production": {Host: "h", User: "u", Path: "/srv/x", Branch: "main"},
		},
	}
	// Every valid builder value passes.
	for _, b := range []string{"host_server", "local_machine", "build_server"} {
		c := *base
		dc := c.Deploy["production"]
		dc.Builder = b
		if b == "build_server" {
			dc.BuildHost, dc.BuildUser, dc.BuildPath = "bh", "bu", "/srv/build"
		}
		c.Deploy["production"] = dc
		if err := c.Validate(); err != nil {
			t.Errorf("builder %q: Validate() = %v, want nil", b, err)
		}
	}
	// Unknown builder value is rejected.
	c := *base
	dc := c.Deploy["production"]
	dc.Builder = "spaceship"
	c.Deploy["production"] = dc
	if err := c.Validate(); err == nil {
		t.Error("builder = spaceship: Validate() = nil, want invalid-config error")
	}
	// build_server without build_* fields is rejected.
	c = *base
	dc = c.Deploy["production"]
	dc.Builder = "build_server"
	c.Deploy["production"] = dc
	if err := c.Validate(); err == nil {
		t.Error("builder = build_server with no build_* fields: Validate() = nil, want invalid-config error")
	}
	// Absent builder defaults to host_server and stays valid.
	if err := base.Validate(); err != nil {
		t.Errorf("absent builder: Validate() = %v, want nil", err)
	}
}

func TestBuilderModeDefaultsToHostServer(t *testing.T) {
	var dc DeployConfig
	if got := dc.BuilderMode(); got != "host_server" {
		t.Errorf("BuilderMode() = %q, want host_server", got)
	}
	dc.Builder = "build_server"
	if got := dc.BuilderMode(); got != "build_server" {
		t.Errorf("BuilderMode() = %q, want build_server", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -run 'TestValidateBuilderModes|TestBuilderModeDefaults' -v`
Expected: FAIL — `dc.Builder` and `BuilderMode` undefined (no compile).

- [ ] **Step 3: Implement the config fields and validation**

In `internal/config/config.go`, add to `DeployConfig` (after the `Branch` field, before `Services`):

```go
	// Builder selects where the production image is built for this
	// env: "host_server" (default, empty means this) builds on the
	// deploy host itself, "local_machine" builds on the machine
	// running pier, "build_server" builds on a dedicated remote
	// machine. The image-mode values stream the finished image to the
	// host over SSH; host_server builds in place.
	Builder string `toml:"builder"`
	// BuildHost, BuildUser, and BuildPath configure the build server
	// used when Builder is "build_server": SSH target and the path
	// where the source tree is synced and built.
	BuildHost string `toml:"build_host"`
	BuildUser string `toml:"build_user"`
	BuildPath string `toml:"build_path"`
```

Add after the `DeployConfig` struct:

```go
// validBuilder lists the accepted [deploy.<env>].builder values.
var validBuilder = map[string]bool{
	"host_server":  true,
	"local_machine": true,
	"build_server": true,
}

// BuilderMode returns the effective builder for the env: the
// configured value, or "host_server" when absent (the historical
// behavior: build and host on the same machine).
func (d DeployConfig) BuilderMode() string {
	if d.Builder == "" {
		return "host_server"
	}
	return d.Builder
}
```

In `internal/config/parse.go`, inside the `for env, dc := range c.Deploy` loop (after the existing `configured` check, before `validateHookList`):

```go
		if dc.Builder != "" && !validBuilder[dc.Builder] {
			return fmt.Errorf("%w: deploy.%s.builder %q must be host_server, local_machine, or build_server", ErrConfigInvalid, env, dc.Builder)
		}
		if dc.BuilderMode() == "build_server" && (dc.BuildHost == "" || dc.BuildUser == "" || dc.BuildPath == "") {
			return fmt.Errorf("%w: deploy.%s.builder = \"build_server\" requires build_host, build_user, and build_path", ErrConfigInvalid, env)
		}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/config/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/parse.go internal/config/parse_test.go
git commit -m "feat(config): builder field and validation for build server modes"
```

---

### Task 2: Real git SHA image tag + wire in the missing Tag() call

**Files:**
- Create: `internal/deploy/tag.go`
- Modify: `internal/deploy/deploy.go`
- Modify: `internal/deploy/build.go`
- Test: `internal/deploy/tag_test.go`, `internal/deploy/build_test.go`

**Interfaces:**
- Consumes: existing `Pipeline.Run`, `Build`, `Tag`.
- Produces: `deployTag() string` (package-level, computes `<sha>`); seam `gitShortSHA func(dir string) (string, error)`; `Pipeline.tag` field (string, set at the top of `Run`).

- [ ] **Step 1: Write the failing tests**

Create `internal/deploy/tag_test.go`:

```go
package deploy

import (
	"regexp"
	"testing"
)

func TestDeployTagUsesGitSHA(t *testing.T) {
	old := gitShortSHA
	gitShortSHA = func(dir string) (string, error) { return "abc1234", nil }
	defer func() { gitShortSHA = old }()
	if got := deployTag(); got != "abc1234" {
		t.Errorf("deployTag() = %q, want abc1234", got)
	}
}

func TestDeployTagFallsBackToTimestamp(t *testing.T) {
	old := gitShortSHA
	gitShortSHA = func(dir string) (string, error) { return "", errNoHEAD }
	defer func() { gitShortSHA = old }()
	got := deployTag()
	re := regexp.MustCompile(`^[0-9]{14}$`)
	if !re.MatchString(got) {
		t.Errorf("deployTag() = %q, want 14-digit timestamp", got)
	}
}

var errNoHEAD = &noHEADErr{}

type noHEADErr struct{}

func (*noHEADErr) Error() string { return "no HEAD" }
```

Add to `internal/deploy/build_test.go`:

```go
func TestTagRetagsLatestToSHAAndCurrent(t *testing.T) {
	f := &fakeSSHClient{}
	if err := Tag(context.Background(), f, "myapp", "abc1234"); err != nil {
		t.Fatalf("Tag: %v", err)
	}
	if len(f.cmds) != 1 {
		t.Fatalf("Tag ran %d commands, want 1", len(f.cmds))
	}
	want := "docker tag myapp:latest myapp:abc1234 && docker tag myapp:latest myapp:current"
	if f.cmds[0] != want {
		t.Errorf("Tag command = %q, want %q", f.cmds[0], want)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race ./internal/deploy/ -run 'TestDeployTag|TestTagRetags'`
Expected: FAIL — `gitShortSHA`, `deployTag` undefined.

- [ ] **Step 3: Implement the tag helpers**

Create `internal/deploy/tag.go`:

```go
package deploy

import (
	"os/exec"
	"strings"
	"time"
)

// gitShortSHA returns the short SHA of HEAD in dir. It is a var so
// tests can pin the value without invoking git.
var gitShortSHA = func(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD").Output()
	return strings.TrimSpace(string(out)), err
}

// deployTag computes the immutable image tag for this deploy: the
// short git SHA of HEAD when the project is a git repo with a HEAD,
// else a UTC timestamp (no git repo, no HEAD, or git unavailable).
func deployTag() string {
	if sha, err := gitShortSHA("."); err == nil && sha != "" {
		return sha
	}
	return time.Now().UTC().Format("20060102150405")
}
```

- [ ] **Step 4: Wire the tag through the pipeline**

In `internal/deploy/deploy.go`, inside `Run`, after the `p.Now` guard:

```go
	p.tag = deployTag()
```

Add the field to the `Pipeline` struct:

```go
	// tag is the immutable image tag for this deploy (git SHA or
	// timestamp fallback), computed at the top of Run and used for
	// the build output, the transferred image, and state.json.
	tag string
```

Replace the build phase (phase 4) with a mode switch that also calls Tag in host_server mode:

```go
	// Phase 4: build.
	p.Logger.PhaseStart("build")
	switch p.DeployEnv.BuilderMode() {
	case "host_server":
		if err := Build(ctx, client, p.DeployEnv.Path, p.Config.Project.Name, p.tag, func(l string) {
			p.Logger.Log("build", "%s", l)
		}); err != nil {
			p.Logger.PhaseEnd("build", err)
			return RemoteBuildError(p.SSH.Host, err)
		}
		// Tag the fresh :latest as the immutable sha record and the
		// :current alias that Rollback overwrites. This is what makes
		// `pier rollback` able to retag the previous image.
		if err := Tag(ctx, client, p.Config.Project.Name, p.tag); err != nil {
			p.Logger.PhaseEnd("build", err)
			return RemoteBuildError(p.SSH.Host, err)
		}
	case "local_machine":
		if err := BuildLocalImage(ctx, ".", p.Config.Stack.PHP, p.Config.Project.Name, p.tag, func(l string) {
			p.Logger.Log("build", "%s", l)
		}); err != nil {
			p.Logger.PhaseEnd("build", err)
			return BuildError(err)
		}
	case "build_server":
		if err := RemoteBuildImage(ctx, p.buildClient, p.DeployEnv.BuildPath, p.Config.Stack.PHP, p.Config.Project.Name, p.tag, func(l string) {
			p.Logger.Log("build", "%s", l)
		}); err != nil {
			p.Logger.PhaseEnd("build", err)
			return RemoteBuildError(p.buildClient.Config.Host, err)
		}
	}
	p.Logger.PhaseEnd("build", nil)
```

Replace the hardcoded `"gitsha"` in `commit`:

```go
	s := &State{
		Current:    p.tag,
		DeployedAt: p.Now().UTC().Format(time.RFC3339),
		DeployedBy: p.SSH.User + "@" + p.SSH.Host,
	}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test -race ./internal/deploy/`
Expected: PASS (all existing pipeline tests still pass; `TestTagRetagsLatestToSHAAndCurrent` passes).

- [ ] **Step 6: Commit**

```bash
git add internal/deploy/tag.go internal/deploy/tag_test.go internal/deploy/deploy.go internal/deploy/build.go internal/deploy/build_test.go
git commit -m "feat(deploy): real git SHA image tags and wired-in Tag call"
```

---

### Task 3: Compose render variant per builder mode

**Files:**
- Modify: `internal/stack/laravel/prod.go`
- Test: `internal/stack/laravel/prod_test.go`

**Interfaces:**
- Consumes: `config.DeployConfig.BuilderMode()` from Task 1.
- Produces: `renderProdCompose` renders the app service with `build:` + `image: project:latest` for `host_server`; `image: project:current` with no `build:` key otherwise.

- [ ] **Step 1: Write the failing test**

Add to `internal/stack/laravel/prod_test.go` (following the inline-config + `findFile` pattern used by the existing tests in that file):

```go
func TestGenerateProdFilesImageModeOmitsBuild(t *testing.T) {
	s := New()
	files, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {Host: "h", User: "u", Path: "/srv/x", Branch: "main", Builder: "local_machine"},
		},
	}, "production")
	if err != nil {
		t.Fatalf("GenerateProdFiles: %v", err)
	}
	body := string(findFile(files, "docker-compose.prod.yml").Contents)
	if contains(body, "build:") {
		t.Errorf("image-mode prod compose still has a build key:\n%s", body)
	}
	if !contains(body, "image: myapp:current") {
		t.Errorf("image-mode prod compose must reference myapp:current:\n%s", body)
	}
}

func TestGenerateProdFilesHostServerKeepsBuild(t *testing.T) {
	s := New()
	files, err := s.GenerateProdFiles(config.Config{
		Project: config.ProjectConfig{Name: "myapp", Domain: "myapp.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
	}, "production")
	if err != nil {
		t.Fatalf("GenerateProdFiles: %v", err)
	}
	body := string(findFile(files, "docker-compose.prod.yml").Contents)
	if !contains(body, "build:") {
		t.Errorf("host_server prod compose must keep the build key:\n%s", body)
	}
	if !contains(body, "image: myapp:latest") {
		t.Errorf("host_server prod compose must keep image myapp:latest:\n%s", body)
	}
}
```

`findFile` and `contains` already exist in the laravel test package (`findFile` used by prod_test.go, `contains` in testhelpers_test.go).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race ./internal/stack/laravel/ -run 'TestGenerateProdFilesImageMode|TestGenerateProdFilesHostServer'`
Expected: FAIL — image mode still renders the build key.

- [ ] **Step 3: Implement the variant**

In `internal/stack/laravel/prod.go`, inside `renderProdCompose`, replace the app service literal:

```go
		Services: map[string]composeService{
			"app": {
				Image:       cfg.Project.Name + ":current",
				Restart:     "unless-stopped",
				Environment: prodEnvForServices(services),
				Networks:    []string{"pier"},
			},
```

and immediately after the `composeFile` literal's closing `}`, before the service loop, insert:

```go
	// host_server builds the image in place, so the compose file keeps
	// the build context (the synced project root) and the :latest
	// tag. The image modes ship a prebuilt image, so the app service
	// references the mutable :current tag instead and has no build
	// key — docker compose up must never try to build or pull.
	if deployCfg.BuilderMode() == "host_server" {
		app := cf.Services["app"]
		app.Image = cfg.Project.Name + ":latest"
		app.Build = &composeBuild{
			// Context is the project root, not ./docker/<php>:
			// the prod Dockerfile (Dockerfile.prod) bakes the
			// application into the image, while the dev runtime
			// Dockerfile gets the code from the ./:/var/www/html
			// bind mount and so never COPYs it.
			Context:    ".",
			Dockerfile: fmt.Sprintf("docker/%s/Dockerfile.prod", cfg.Stack.PHP),
			Args: map[string]string{
				// The runtime Dockerfile's ARG WWWGROUP has no
				// default, so the build fails with
				// `groupadd: invalid group ID 'sail'` when the
				// arg is absent. Prod has no host bind-mount,
				// so a fixed UID/GID (matching the Dockerfile's
				// ARG WWWUSER=1337 default) is fine.
				"WWWUSER":  "1337",
				"WWWGROUP": "1337",
			},
		}
		cf.Services["app"] = app
	}
```

Move the `Build` field and its comment out of the struct literal (the struct literal above now starts with `Image:` only). The `deployCfg` variable is already defined at the top of `renderProdCompose`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/stack/laravel/`
Expected: PASS — the existing build-key assertions (`TestGenerateProdFilesAppBuildArgs`, `TestGenerateProdFilesAppBuildsFromProdDockerfile`) still pass because their configs have no `builder` set (defaults to `host_server`), and the two new tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/stack/laravel/prod.go internal/stack/laravel/prod_test.go
git commit -m "feat(compose): per-builder prod compose variant (image-only vs build)"
```

---

### Task 4: deployFilesOnly sync filter

**Files:**
- Modify: `internal/deploy/syncfilter.go`
- Test: `internal/deploy/syncfilter_test.go`

**Interfaces:**
- Consumes: `pathExcluded(rel string, excludes []string) bool`.
- Produces: `deployFilesOnly []string` — include the three deploy files, exclude everything else; ancestor directories of included files are descended.

- [ ] **Step 1: Write the failing tests**

Add to `internal/deploy/syncfilter_test.go`:

```go
func TestDeployFilesOnly(t *testing.T) {
	for _, rel := range []string{
		"docker-compose.prod.yml",
		".env.production",
		"docker/nginx/default.conf",
	} {
		if pathExcluded(rel, deployFilesOnly) {
			t.Errorf("pathExcluded(%q, deployFilesOnly) = true, want false (must be shipped)", rel)
		}
	}
	for _, rel := range []string{
		"docker-compose.yml",
		"docker/8.3/Dockerfile.prod",
		"app/Models/User.php",
		"marker.txt",
		".git/config",
		".env",
		".env.example",
	} {
		if !pathExcluded(rel, deployFilesOnly) {
			t.Errorf("pathExcluded(%q, deployFilesOnly) = false, want true (must be skipped)", rel)
		}
	}
}

// TestDeployFilesOnlyDescendsAncestorDirs guards the WalkDir pruning
// interaction: an excluded directory holding an included file must be
// descended (--exclude=* matches "docker", but
// docker/nginx/default.conf is included under it).
func TestDeployFilesOnlyDescendsAncestorDirs(t *testing.T) {
	for _, rel := range []string{"docker", "docker/nginx"} {
		if pathExcluded(rel, deployFilesOnly) {
			t.Errorf("pathExcluded(%q, deployFilesOnly) = true, want false (directory holds an included file)", rel)
		}
	}
	for _, rel := range []string{"docker/php", "app"} {
		if !pathExcluded(rel, deployFilesOnly) {
			t.Errorf("pathExcluded(%q, deployFilesOnly) = false, want true (no included file beneath)", rel)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race ./internal/deploy/ -run TestDeployFilesOnly`
Expected: FAIL — `deployFilesOnly` undefined.

- [ ] **Step 3: Implement the filter and the directory-descend fix**

In `internal/deploy/syncfilter.go`, after `rsyncExcludes`:

```go
// deployFilesOnly is the filter for image-mode host syncs: exactly
// the files the host needs to run the stack (the compose file, the
// env file with secrets, and the bind-mounted nginx conf). Everything
// else is excluded — the host never receives the source tree when the
// image is built elsewhere.
var deployFilesOnly = []string{
	"--include=docker-compose.prod.yml",
	"--include=.env.production",
	"--include=docker/nginx/default.conf",
	"--exclude=*",
}
```

Modify `pathExcluded`'s exclude loop so a directory that holds an included file is not pruned:

```go
	for _, rule := range excludes {
		if !strings.HasPrefix(rule, "--exclude=") {
			continue
		}
		if matchPattern(rel, strings.TrimPrefix(rule, "--exclude=")) {
			// An excluded directory may still hold an included file
			// (--exclude=* matches "docker" while
			// docker/nginx/default.conf is included beneath it);
			// WalkDir must descend into it or the include never ships.
			if dirHoldsIncludedFile(rel, excludes) {
				return false
			}
			return true
		}
	}
	return false
}

// dirHoldsIncludedFile reports whether any include rule is anchored
// under rel (e.g. rel "docker" and include "docker/nginx/default.conf").
func dirHoldsIncludedFile(rel string, excludes []string) bool {
	prefix := rel + "/"
	for _, rule := range excludes {
		if !strings.HasPrefix(rule, "--include=") {
			continue
		}
		p := strings.TrimPrefix(rule, "--include=")
		if strings.Contains(p, "/") && strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/deploy/`
Expected: PASS — the existing `TestPathExcluded` still passes (no include patterns there, so `dirHoldsIncludedFile` never fires).

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/syncfilter.go internal/deploy/syncfilter_test.go
git commit -m "feat(deploy): deployFilesOnly sync filter for image-mode host syncs"
```

---

### Task 5: SSH streaming primitives (StreamIn / StreamOut)

**Files:**
- Modify: `internal/deploy/ssh.go`
- Modify: `internal/deploy/hooks_test.go` (test server stdin capture)
- Modify: `internal/deploy/shell_test.go` (fakeSession fields)
- Test: `internal/deploy/stream_test.go`

**Interfaces:**
- Consumes: `*Client.conn (*ssh.Client)`, `outputTail` (defined in ssh.go), `runStreamTailSize`.
- Produces:
  - `func (c *Client) StreamIn(ctx context.Context, cmd string, in io.Reader, onLine func(string)) error` — runs cmd with stdin from `in` (binary-safe input path), streams stderr lines to `onLine`.
  - `func (c *Client) StreamOut(ctx context.Context, cmd string, out io.Writer, onLine func(string)) error` — runs cmd, pipes stdout into `out` (binary-safe), streams stderr lines to `onLine`.
  - Test seam: `fakeSession.captureStdin bool`, `fakeSession.stdinData []byte`, `fakeSession.setStdin([]byte)`; `startPipelineServer` exec handler reads stdin when `captureStdin` is set.

- [ ] **Step 1: Extend the test server to capture stdin**

In `internal/deploy/shell_test.go`, add to `fakeSession`:

```go
	// captureStdin, when set, makes the exec handler drain the
	// session's stdin before writing output, so tests can assert what
	// a client streamed (e.g. a docker save tar).
	captureStdin bool
	// stdinData holds the last captured stdin stream.
	stdinData []byte
```

Add the method:

```go
func (f *fakeSession) setStdin(b []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stdinData = b
}

func (f *fakeSession) stdin() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stdinData
}
```

In `internal/deploy/hooks_test.go`, modify the `case "exec":` handler in `servePipelineChannel` so it drains stdin before writing output, only when opted in:

```go
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
				if err == nil {
					fs.setStdin(b)
				}
			}
			_, _ = channel.Write(fs.output)
			st := fs.status
			if fs.statusFn != nil {
				st = fs.statusFn(cmd)
			}
			finishFakeSession(channel, st)
			return
```

- [ ] **Step 2: Write the failing tests**

Create `internal/deploy/stream_test.go`:

```go
package deploy

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestStreamInFeedsStdinAndStreamsStderr(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("Loaded image: myapp:abc1234\n"), status: 0, captureStdin: true}
	host, port := testAddr(t, startPipelineServer(t, keyOnlyServer(pub), fs))
	client := dialTestClient(t, SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath})
	defer client.Close()

	var lines []string
	tar := "TARBYTES\x00BINARY"
	if err := client.StreamIn(context.Background(), "docker load", strings.NewReader(tar), func(l string) {
		lines = append(lines, l)
	}); err != nil {
		t.Fatalf("StreamIn: %v", err)
	}
	if len(fs.cmds) != 1 || fs.cmds[0] != "docker load" {
		t.Errorf("cmds = %q, want [docker load]", fs.cmds)
	}
	if got := string(fs.stdin()); got != tar {
		t.Errorf("stdin received = %q, want %q", got, tar)
	}
	if len(lines) == 0 {
		t.Error("onLine never called; stderr should have streamed docker load output")
	}
}

func TestStreamInPropagatesExitError(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("open /var/run/docker.sock: permission denied\n"), status: 1}
	host, port := testAddr(t, startPipelineServer(t, keyOnlyServer(pub), fs))
	client := dialTestClient(t, SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath})
	defer client.Close()

	err := client.StreamIn(context.Background(), "docker load", strings.NewReader("data"), nil)
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("StreamIn() = %v, want failure carrying the stderr tail", err)
	}
}

func TestStreamOutPipesBinaryStdout(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("TAR\x00BINARY\x01data"), status: 0}
	host, port := testAddr(t, startPipelineServer(t, keyOnlyServer(pub), fs))
	client := dialTestClient(t, SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath})
	defer client.Close()

	var out bytes.Buffer
	if err := client.StreamOut(context.Background(), "docker save myapp:abc1234", &out, nil); err != nil {
		t.Fatalf("StreamOut: %v", err)
	}
	if !bytes.Equal(out.Bytes(), []byte("TAR\x00BINARY\x01data")) {
		t.Errorf("StreamOut stdout = %q, want binary tar preserved byte-for-byte", out.Bytes())
	}
}
```

Note: `dialTestClient` already exists in the test package (used by hooks_test.go). If `strings` is already imported in stream_test.go, use it without the extra import.

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test -race ./internal/deploy/ -run 'TestStreamIn|TestStreamOut'`
Expected: FAIL — `StreamIn`/`StreamOut` undefined.

- [ ] **Step 4: Implement StreamIn and StreamOut**

In `internal/deploy/ssh.go`, after `RunStream` (before `Close`):

```go
// StreamIn executes cmd on the remote host with stdin fed from in and
// invokes onLine for each stderr line as it arrives. The stdin path is
// binary-safe (no line scanning on the input side): it pipes `docker
// save` output into a remote `docker load`. stderr is line-streamed
// because docker load writes its progress and errors as lines. On a
// non-zero exit the returned error carries the last
// runStreamTailSize stderr lines.
func (c *Client) StreamIn(ctx context.Context, cmd string, in io.Reader, onLine func(string)) error {
	sess, err := c.conn.NewSession()
	if err != nil {
		return fmt.Errorf("ssh: new session: %w", err)
	}
	defer sess.Close()
	sess.Stdin = in
	stderr, err := sess.StderrPipe()
	if err != nil {
		return err
	}
	if err := sess.Start(cmd); err != nil {
		return err
	}
	tail := &outputTail{max: runStreamTailSize}
	var stderrErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			line := sc.Text()
			tail.add(line)
			if onLine != nil {
				onLine(line)
			}
		}
		stderrErr = sc.Err()
	}()
	err = sess.Wait()
	<-done
	if err != nil {
		return fmt.Errorf("remote command failed: %w (last output: %s)", err, tail.String())
	}
	if stderrErr != nil {
		return stderrErr
	}
	return nil
}

// StreamOut executes cmd on the remote host piping its stdout into
// out and invoking onLine for each stderr line as it arrives. The
// stdout path is binary-safe (io.Copy, no line scanning): it streams
// `docker save` output from a build server into a sink. On a non-zero
// exit the returned error carries the last runStreamTailSize stderr
// lines.
func (c *Client) StreamOut(ctx context.Context, cmd string, out io.Writer, onLine func(string)) error {
	sess, err := c.conn.NewSession()
	if err != nil {
		return fmt.Errorf("ssh: new session: %w", err)
	}
	defer sess.Close()
	stdout, err := sess.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := sess.StderrPipe()
	if err != nil {
		return err
	}
	if err := sess.Start(cmd); err != nil {
		return err
	}
	tail := &outputTail{max: runStreamTailSize}
	var stderrErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			line := sc.Text()
			tail.add(line)
			if onLine != nil {
				onLine(line)
			}
		}
		stderrErr = sc.Err()
	}()
	if _, err := io.Copy(out, stdout); err != nil {
		return err
	}
	<-done
	if err := sess.Wait(); err != nil {
		return fmt.Errorf("remote command failed: %w (last output: %s)", err, tail.String())
	}
	if stderrErr != nil {
		return stderrErr
	}
	return nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test -race ./internal/deploy/ -run 'TestStreamIn|TestStreamOut|TestPipelineRunsHooksAtCorrectStages'`
Expected: PASS — the hooks pipeline test confirms the exec-handler change is inert for tests that do not set `captureStdin`.

- [ ] **Step 6: Commit**

```bash
git add internal/deploy/ssh.go internal/deploy/shell_test.go internal/deploy/hooks_test.go internal/deploy/stream_test.go
git commit -m "feat(deploy): binary-safe StreamIn/StreamOut SSH primitives"
```

---

### Task 6: Pipeline modes — preflight, sync, build, transfer

**Files:**
- Create: `internal/deploy/transfer.go`
- Modify: `internal/deploy/build.go` (BuildLocalImage, RemoteBuildImage, imageBuildArgs)
- Modify: `internal/deploy/deploy.go` (BuildSSH field, preflight dual dial, per-mode sync)
- Test: `internal/deploy/transfer_test.go`, `internal/deploy/deploy_unit_test.go`

**Interfaces:**
- Consumes: `Pipeline.tag` (Task 2), `deployFilesOnly` (Task 4), `StreamIn`/`StreamOut` (Task 5), `pipelineDial`/`pipelineProbe`/`pipelineEnsurePath` seams, `config.DeployConfig.BuilderMode()`.
- Produces:
  - `Pipeline.BuildSSH SSHConfig` (field; dialed when `build_server`), `Pipeline.buildClient *Client` (set by preflight).
  - `BuildLocalImage(ctx, dir, php, project, sha string, onLine func(string)) error`
  - `RemoteBuildImage(ctx, r runner, dir, php, project, sha string, onLine func(string)) error`
  - `TransferImage(ctx, saver imageSaver, dst *Client, image string, onLine func(string)) (int64, error)`
  - `imageSaver` interface: `Save(ctx context.Context, image string, sink io.Writer, onLine func(string)) error`; `localSaver{dir string}`; `remoteSaver{c *Client}`.
  - `Pipeline.transfer(ctx, hostClient *Client) error` — logs a `transfer` phase, retags `project:current` on the host after a successful load.

- [ ] **Step 1: Write the failing tests**

Add to `internal/deploy/deploy_unit_test.go`:

```go
// TestPipelineSyncTargetsPerBuilder drives Run against two in-process
// SSH/SFTP servers (host + build server) and asserts each builder
// mode syncs the right set to the right machine: full source to the
// host in host_server mode, deploy files only to the host in the
// image modes, and full source to the build server in build_server
// mode. Run cannot complete past the build phase (no real docker), so
// success is asserted on the synced files.
func TestPipelineSyncTargetsPerBuilder(t *testing.T) {
	cases := []struct {
		name    string
		builder string
		hostSet bool // marker.txt expected on the host
		build   bool // build server configured
	}{
		{"host_server", "host_server", true, false},
		{"local_machine", "local_machine", false, false},
		{"build_server", "build_server", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			seedEnvFile(t)
			// The render phase does not write docker/nginx/default.conf
			// (it exists in a real project from `pier init`); seed it
			// so the image-mode host sync has something to ship.
			if err := os.MkdirAll(filepath.Join("docker", "nginx"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join("docker", "nginx", "default.conf"), []byte("server {}\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile("marker.txt", []byte("sync me"), 0644); err != nil {
				t.Fatal(err)
			}
			keyPath, pub := writeTestKey(t)
			hostFs := &fakeSession{output: []byte("ok\n"), status: 0}
			buildFs := &fakeSession{output: []byte("ok\n"), status: 0}
			hostAddr := startPipelineServer(t, keyOnlyServer(pub), hostFs)
			buildAddr := startPipelineServer(t, keyOnlyServer(pub), buildFs)
			host, hostPort := testAddr(t, hostAddr)
			build, buildPort := testAddr(t, buildAddr)
			remoteHost := t.TempDir()
			remoteBuild := t.TempDir()

			origProbe, origEnsure := pipelineProbe, pipelineEnsurePath
			pipelineProbe = func(ctx context.Context, r stdinRunner) (bool, error) { return true, nil }
			pipelineEnsurePath = func(ctx context.Context, c *Client, path string) error { return nil }
			defer func() { pipelineProbe, pipelineEnsurePath = origProbe, origEnsure }()

			dc := config.DeployConfig{
				Host: host, User: "deploy", Path: remoteHost, Branch: "main", Builder: tc.builder,
			}
			buildSSH := SSHConfig{}
			if tc.build {
				dc.BuildHost, dc.BuildUser, dc.BuildPath = build, "deploy", remoteBuild
				buildSSH = SSHConfig{Host: build, User: "deploy", Port: buildPort, KeyPath: keyPath}
			}
			cfg := &config.Config{
				Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
				Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
				Deploy:  map[string]config.DeployConfig{"production": dc},
			}
			p := &Pipeline{
				Config: cfg, Env: "production", DeployEnv: dc,
				Logger: discardLogger{},
				SSH:    SSHConfig{Host: host, User: "deploy", Port: hostPort, KeyPath: keyPath},
				BuildSSH: buildSSH,
				Now:    time.Now,
			}
			_ = p.Run(context.Background()) // ends at the build phase: no real docker

			_, hostMarkerErr := os.Stat(filepath.Join(remoteHost, "marker.txt"))
			if tc.hostSet && hostMarkerErr != nil {
				t.Fatalf("marker.txt must be synced to the host, got %v", hostMarkerErr)
			}
			if !tc.hostSet && hostMarkerErr == nil {
				t.Fatal("image mode: marker.txt must NOT be synced to the host")
			}
			if tc.build {
				if _, err := os.Stat(filepath.Join(remoteBuild, "marker.txt")); err != nil {
					t.Fatalf("build_server: marker.txt must be synced to the build server, got %v", err)
				}
			}
			// The deploy files always land on the host.
			for _, f := range []string{"docker-compose.prod.yml", ".env.production", filepath.Join("docker", "nginx", "default.conf")} {
				if _, err := os.Stat(filepath.Join(remoteHost, f)); err != nil {
					t.Errorf("host missing %s: %v", f, err)
				}
			}
		})
	}
}

// TestPipelineBuildServerPreflightDialsBoth asserts build_server mode
// dials, probes, and ensures paths on both the host and the build
// server, host first.
func TestPipelineBuildServerPreflightDialsBoth(t *testing.T) {
	t.Chdir(t.TempDir())
	seedEnvFile(t)
	keyPath, pub := writeTestKey(t)
	hostFs := &fakeSession{output: []byte("ok\n"), status: 0}
	buildFs := &fakeSession{output: []byte("ok\n"), status: 0}
	hostAddr := startPipelineServer(t, keyOnlyServer(pub), hostFs)
	buildAddr := startPipelineServer(t, keyOnlyServer(pub), buildFs)
	host, hostPort := testAddr(t, hostAddr)
	build, buildPort := testAddr(t, buildAddr)

	origDial, origProbe, origEnsure := pipelineDial, pipelineProbe, pipelineEnsurePath
	var dialed []string
	pipelineDial = func(ctx context.Context, cfg SSHConfig) (bootstrapConn, error) {
		dialed = append(dialed, cfg.Host)
		return origDial(ctx, cfg)
	}
	probes := 0
	pipelineProbe = func(ctx context.Context, r stdinRunner) (bool, error) { probes++; return true, nil }
	pipelineEnsurePath = func(ctx context.Context, c *Client, path string) error { return nil }
	defer func() { pipelineDial, pipelineProbe, pipelineEnsurePath = origDial, origProbe, origEnsure }()

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "x", Domain: "x.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy: map[string]config.DeployConfig{
			"production": {
				Host: host, User: "deploy", Path: t.TempDir(), Branch: "main",
				Builder: "build_server", BuildHost: build, BuildUser: "deploy", BuildPath: t.TempDir(),
			},
		},
	}
	p := &Pipeline{
		Config: cfg, Env: "production", DeployEnv: cfg.Deploy["production"],
		Logger: discardLogger{},
		SSH:    SSHConfig{Host: host, User: "deploy", Port: hostPort, KeyPath: keyPath},
		BuildSSH: SSHConfig{Host: build, User: "deploy", Port: buildPort, KeyPath: keyPath},
		Now:    time.Now,
	}
	_ = p.Run(context.Background()) // ends at the build phase: no real docker

	want := []string{host, build}
	if len(dialed) != 2 || dialed[0] != want[0] || dialed[1] != want[1] {
		t.Errorf("dialed = %v, want %v (host first, then build server)", dialed, want)
	}
	if probes != 2 {
		t.Errorf("probe calls = %d, want 2 (host + build server)", probes)
	}
}
```

Note: these tests rely on preflight's `conn.(*Client)` type assertion, so the fake dial must return real `*Client`s (real `Dial` to the in-process servers) — the `recordingSyncClient`/fake-conn approach cannot pass preflight.

Create `internal/deploy/transfer_test.go`:

```go
package deploy

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// scriptedSaver is an imageSaver that writes canned bytes to sink and
// optionally fails.
type scriptedSaver struct {
	data string
	err  error
}

func (s scriptedSaver) Save(ctx context.Context, image string, sink io.Writer, onLine func(string)) error {
	if s.err != nil {
		return s.err
	}
	_, err := io.WriteString(sink, s.data)
	return err
}

func TestTransferImageStreamsSaveIntoLoad(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("ok\n"), status: 0, captureStdin: true}
	host, port := testAddr(t, startPipelineServer(t, keyOnlyServer(pub), fs))
	client := dialTestClient(t, SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath})
	defer client.Close()

	saver := scriptedSaver{data: "TARBYTES"}
	var lines []string
	n, err := TransferImage(context.Background(), saver, client, "myapp:abc1234", func(l string) { lines = append(lines, l) })
	if err != nil {
		t.Fatalf("TransferImage: %v", err)
	}
	if n != int64(len("TARBYTES")) {
		t.Errorf("TransferImage bytes = %d, want %d", n, len("TARBYTES"))
	}
	if got := string(fs.stdin()); got != "TARBYTES" {
		t.Errorf("docker load stdin = %q, want the save stream", got)
	}
	if len(fs.cmds) != 1 || fs.cmds[0] != "docker load" {
		t.Errorf("cmds = %q, want [docker load]", fs.cmds)
	}
}

func TestTransferImageSaveFailureAborts(t *testing.T) {
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("ok\n"), status: 0}
	host, port := testAddr(t, startPipelineServer(t, keyOnlyServer(pub), fs))
	client := dialTestClient(t, SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath})
	defer client.Close()

	_, err := TransferImage(context.Background(), scriptedSaver{err: errors.New("save boom")}, client, "myapp:abc1234", nil)
	if err == nil || !strings.Contains(err.Error(), "save boom") {
		t.Errorf("TransferImage() = %v, want the save error propagated", err)
	}
}

func TestPipelineTransferRetagsCurrentOnHost(t *testing.T) {
	t.Chdir(t.TempDir())
	keyPath, pub := writeTestKey(t)
	fs := &fakeSession{output: []byte("ok\n"), status: 0, captureStdin: true}
	host, port := testAddr(t, startPipelineServer(t, keyOnlyServer(pub), fs))
	client := dialTestClient(t, SSHConfig{User: "deploy", Host: host, Port: port, KeyPath: keyPath})
	defer client.Close()

	logger := &recordingLogger{}
	p := &Pipeline{
		Config: &config.Config{Project: config.ProjectConfig{Name: "myapp"}},
		DeployEnv: config.DeployConfig{Builder: "local_machine"},
		Logger: logger, SSH: SSHConfig{Host: host, User: "deploy", Port: port, KeyPath: keyPath},
		tag: "abc1234",
	}
	p.saveLocal = func(ctx context.Context, dir, image string, sink io.Writer, onLine func(string)) error {
		_, err := io.WriteString(sink, "TAR")
		return err
	}
	if err := p.transfer(context.Background(), client); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if got := string(fs.stdin()); got != "TAR" {
		t.Errorf("docker load stdin = %q, want the save stream", got)
	}
	if len(fs.cmds) != 2 || fs.cmds[1] != "docker tag myapp:abc1234 myapp:current" {
		t.Errorf("cmds = %q, want [docker load docker tag myapp:abc1234 myapp:current]", fs.cmds)
	}
}
```

Note: `TestPipelineTransferRetagsCurrentOnHost` references `p.saveLocal` — a seam field on Pipeline defined in the implementation step below. `recordingLogger` is already defined in hooks_test.go; `config` import must be added to transfer_test.go.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race ./internal/deploy/ -run 'TestPipelineSyncTargets|TestPipelineBuildServerPreflight|TestTransferImage|TestPipelineTransferRetags'`
Expected: FAIL — `BuildSSH`, `p.saveLocal`, `BuildLocalImage`, `RemoteBuildImage`, `TransferImage` undefined.

- [ ] **Step 3: Implement build-side image builders**

In `internal/deploy/build.go`, add after `Tag`:

```go
// imageBuildArgs returns the `docker build` arguments for the
// production image, shared by the local and remote build paths. The
// context is the project root and the Dockerfile is the per-PHP
// Dockerfile.prod; the WWWUSER/WWWGROUP args are required because the
// runtime Dockerfile's ARG WWWGROUP has no default.
func imageBuildArgs(php, project, sha string) []string {
	return []string{"build", "--pull",
		"-f", "docker/" + php + "/Dockerfile.prod",
		"--build-arg", "WWWUSER=1337", "--build-arg", "WWWGROUP=1337",
		"-t", project + ":" + sha, "."}
}

// BuildLocalImage runs plain `docker build` on the local machine in
// dir (the project root), streaming stdout/stderr lines to onLine.
// Used as the build stage of the local_machine builder mode.
func BuildLocalImage(ctx context.Context, dir, php, project, sha string, onLine func(string)) error {
	cmd := exec.CommandContext(ctx, "docker", imageBuildArgs(php, project, sha)...)
	cmd.Dir = dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var stderrErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			onLine(sc.Text())
		}
		stderrErr = sc.Err()
	}()
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		onLine(sc.Text())
	}
	<-done
	if err := sc.Err(); err != nil {
		return err
	}
	if stderrErr != nil {
		return stderrErr
	}
	return cmd.Wait()
}

// RemoteBuildImage runs plain `docker build` on a remote build server
// inside dir (the synced project root), streaming output lines to
// onLine. Used as the build stage of the build_server builder mode.
func RemoteBuildImage(ctx context.Context, r runner, dir, php, project, sha string, onLine func(string)) error {
	cmd := "cd " + dir + " && docker " + strings.Join(imageBuildArgs(php, project, sha), " ")
	return r.RunStream(ctx, cmd, onLine)
}
```

Add the imports `bufio`, `os/exec`, `strings` to `build.go`.

- [ ] **Step 4: Implement transfer.go**

Create `internal/deploy/transfer.go`:

```go
package deploy

import (
	"context"
	"fmt"
	"io"
)

// imageSaver produces a docker save tar stream for image, either from
// the local docker daemon (localSaver) or over an SSH connection to a
// build server (remoteSaver).
type imageSaver interface {
	Save(ctx context.Context, image string, sink io.Writer, onLine func(string)) error
}

// localSaver saves images from the local docker daemon. dir is the
// working directory for the docker invocation.
type localSaver struct {
	dir string
}

func (s localSaver) Save(ctx context.Context, image string, sink io.Writer, onLine func(string)) error {
	return saveLocal(ctx, s.dir, image, sink, onLine)
}

// remoteSaver saves images over an SSH connection to a build server.
type remoteSaver struct {
	c *Client
}

func (s remoteSaver) Save(ctx context.Context, image string, sink io.Writer, onLine func(string)) error {
	return s.c.StreamOut(ctx, "docker save "+image, sink, onLine)
}

// saveLocal runs `docker save <image>` locally in dir, streaming the
// tar into sink. It is a var so tests can script the docker side.
var saveLocal = func(ctx context.Context, dir, image string, sink io.Writer, onLine func(string)) error {
	cmd := exec.CommandContext(ctx, "docker", "save", image)
	cmd.Dir = dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	var stderrErr error
	go func() {
		defer close(done)
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			if onLine != nil {
				onLine(sc.Text())
			}
		}
		stderrErr = sc.Err()
	}()
	_, copyErr := io.Copy(sink, stdout)
	<-done
	if err := cmd.Wait(); err != nil {
		return err
	}
	if copyErr != nil {
		return copyErr
	}
	return stderrErr
}

// countingReader counts the bytes read through it.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// TransferImage streams image from saver into dst's docker daemon via
// `docker load` and returns the number of bytes streamed. Both sides
// run concurrently: the saver's output feeds an io.Pipe whose read
// side is the docker load session's stdin. A failure on either side
// aborts the transfer; the saver's error takes precedence so the
// reported cause matches where the image came from.
func TransferImage(ctx context.Context, saver imageSaver, dst *Client, image string, onLine func(string)) (int64, error) {
	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		err := saver.Save(ctx, image, pw, onLine)
		_ = pw.CloseWithError(err)
		errCh <- err
	}()
	counter := &countingReader{r: pr}
	loadErr := dst.StreamIn(ctx, "docker load", counter, onLine)
	saveErr := <-errCh
	if saveErr != nil {
		return counter.n, fmt.Errorf("docker save %s: %w", image, saveErr)
	}
	if loadErr != nil {
		return counter.n, fmt.Errorf("docker load: %w", loadErr)
	}
	return counter.n, nil
}
```

Add imports `bufio`, `os/exec` to transfer.go. `remoteSaver`'s zero-value `c` is never used with a nil `c` (the pipeline only builds it after preflight).

- [ ] **Step 5: Implement the pipeline changes**

In `internal/deploy/deploy.go`:

1. Add fields to `Pipeline`:

```go
	// BuildSSH is the SSH config of the dedicated build server, used
	// only when [deploy.<env>].builder is "build_server". Dialed in
	// preflight like the host connection.
	BuildSSH SSHConfig
	// buildClient is the dialed build-server connection, set by
	// preflight in build_server mode.
	buildClient *Client
```

2. In `Run`, after the host `defer client.Close()` and before the render phase, close the build client too:

```go
	defer client.Close()
	if p.buildClient != nil {
		defer p.buildClient.Close()
	}
```

3. Replace the sync phase with a per-mode switch (use a fresh variable
   name — `err` is already declared at function scope by
   `client, err := p.preflight(ctx)`):

```go
	// Phase 3: sync.
	p.Logger.PhaseStart("sync")
	var syncErr error
	switch p.DeployEnv.BuilderMode() {
	case "host_server":
		syncErr = client.SyncDir(ctx, ".", p.DeployEnv.Path, rsyncExcludes)
	case "local_machine":
		// The host only needs the deploy files; the build runs from
		// the local working tree.
		syncErr = client.SyncDir(ctx, ".", p.DeployEnv.Path, deployFilesOnly)
	case "build_server":
		if syncErr = p.buildClient.SyncDir(ctx, ".", p.DeployEnv.BuildPath, rsyncExcludes); syncErr == nil {
			syncErr = client.SyncDir(ctx, ".", p.DeployEnv.Path, deployFilesOnly)
		}
	}
	if syncErr != nil {
		p.Logger.PhaseEnd("sync", syncErr)
		return PreflightError(syncErr)
	}
	p.Logger.PhaseEnd("sync", nil)
```

4. In `preflight`, after the existing host ensure-path block, add the build-server dial:

```go
	if p.DeployEnv.BuilderMode() == "build_server" {
		if p.BuildSSH.Host == "" {
			client.Close()
			return nil, fmt.Errorf("deploy.%s.builder = \"build_server\" requires build_host, build_user, and build_path in pier.toml", p.Env)
		}
		conn, err := pipelineDial(ctx, p.BuildSSH)
		if err != nil {
			client.Close()
			return nil, err
		}
		ok, err := pipelineProbe(ctx, conn)
		if err != nil {
			conn.Close()
			client.Close()
			return nil, err
		}
		if !ok {
			conn.Close()
			client.Close()
			return nil, NotBootstrappedError(p.Env + " build server")
		}
		buildClient, ok := conn.(*Client)
		if !ok {
			conn.Close()
			client.Close()
			return nil, fmt.Errorf("internal: dial returned %T, want *Client", conn)
		}
		if err := pipelineEnsurePath(ctx, buildClient, p.DeployEnv.BuildPath); err != nil {
			buildClient.Close()
			client.Close()
			return nil, fmt.Errorf(
				"build path %s on %s is not writable for %s.\nCreate it once with:\n  sudo mkdir -p %s\n  sudo chown %s:%s %s\n(or re-run `pier bootstrap %s` to create it automatically.)",
				p.DeployEnv.BuildPath, p.BuildSSH.Host, p.BuildSSH.User,
				p.DeployEnv.BuildPath, p.BuildSSH.User, p.BuildSSH.User, p.DeployEnv.BuildPath, p.Env)
		}
		p.buildClient = buildClient
	}
	return client, nil
```

5. Insert the transfer phase after the build phase's `p.Logger.PhaseEnd("build", nil)` and before the before_deploy block:

```go
	// Phase 4b: transfer — image modes stream the just-built image
	// into the host's docker daemon and retag it as :current.
	if p.DeployEnv.BuilderMode() != "host_server" {
		if err := p.transfer(ctx, client); err != nil {
			return err
		}
	}
```

6. Add the `transfer` method (in deploy.go, after `runHooks` usage — put it near `rollback`):

```go
// transfer streams the just-built image from the build side (local
// docker or the build server) into the host's docker daemon and
// retags it as <project>:current, so the image-variant compose file's
// `image: <project>:current` reference resolves. A failed save or
// load leaves the old :current image untouched — the deploy aborts
// with the old release still serving, so no rollback is needed.
func (p *Pipeline) transfer(ctx context.Context, hostClient *Client) error {
	p.Logger.PhaseStart("transfer")
	image := p.Config.Project.Name + ":" + p.tag
	var saver imageSaver
	if p.DeployEnv.BuilderMode() == "local_machine" {
		saver = localSaver{dir: "."}
	} else {
		saver = remoteSaver{c: p.buildClient}
	}
	n, err := TransferImage(ctx, saver, hostClient, image, func(l string) {
		p.Logger.Log("transfer", "%s", l)
	})
	if err != nil {
		p.Logger.PhaseEnd("transfer", err)
		return err
	}
	p.Logger.Log("transfer", "image %s (%d bytes) loaded on %s", image, n, p.SSH.Host)
	if _, _, err := hostClient.Run(ctx, "docker tag "+image+" "+p.Config.Project.Name+":current"); err != nil {
		p.Logger.PhaseEnd("transfer", err)
		return fmt.Errorf("transfer: retag current: %w", err)
	}
	p.Logger.PhaseEnd("transfer", nil)
	return nil
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test -race ./internal/deploy/`
Expected: PASS. The existing pipeline tests (`TestPipelineSyncsFilesToRemote`, `TestPipelineRunsHooksAtCorrectStages`) use `host_server` (no builder set) and must still pass unchanged.

- [ ] **Step 7: Fix the transfer_test's saveLocal seam on Pipeline**

`TestPipelineTransferRetagsCurrentOnHost` overrides a seam on `p`. Make the pipeline use the seam:

In `transfer`, replace `saver = localSaver{dir: "."}` with:

```go
	if p.DeployEnv.BuilderMode() == "local_machine" {
		saver = localSaver{dir: ".", save: p.saveLocal}
	}
```

Extend `localSaver`:

```go
// localSaver saves images from the local docker daemon. dir is the
// working directory for the docker invocation; save defaults to the
// package saveLocal when nil.
type localSaver struct {
	dir  string
	save func(ctx context.Context, dir, image string, sink io.Writer, onLine func(string)) error
}

func (s localSaver) Save(ctx context.Context, image string, sink io.Writer, onLine func(string)) error {
	save := s.save
	if save == nil {
		save = saveLocal
	}
	return save(ctx, s.dir, image, sink, onLine)
}
```

And add the seam field to `Pipeline`:

```go
	// saveLocal, when set, replaces the local `docker save` invocation
	// in the transfer phase (tests script the docker side).
	saveLocal func(ctx context.Context, dir, image string, sink io.Writer, onLine func(string)) error
```

- [ ] **Step 8: Run all tests and lint**

Run: `go test -race ./... && golangci-lint run`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/deploy/build.go internal/deploy/transfer.go internal/deploy/deploy.go internal/deploy/transfer_test.go internal/deploy/deploy_unit_test.go
git commit -m "feat(deploy): per-builder pipeline (dual preflight, split sync, streamed transfer)"
```

---

### Task 7: CLI — buildmode command, build-server SSH config, bootstrap both machines

**Files:**
- Create: `internal/cli/buildmode.go`
- Modify: `internal/cli/helpers.go`
- Modify: `internal/cli/deploy.go`
- Modify: `internal/cli/bootstrap.go`
- Modify: `internal/cli/toml.go`
- Modify: `internal/cli/root.go`
- Test: `internal/cli/buildmode_test.go`, `internal/cli/bootstrap_test.go`

**Interfaces:**
- Consumes: `config.DeployConfig.BuilderMode()`, `tui.PickEnv(labels []string, start int)` (single-select picker, start index), `deploy.BootstrapEnv`, `deploy.ProbeEnv`, `deploy.SSHConfig`.
- Produces:
  - `newBuildSSHConfig(dc config.DeployConfig) deploy.SSHConfig`
  - `pier buildmode <env>` command (`newBuildmodeCmd`), `runBuildmode(cmd, env string) error`, seam `pickBuilderTUI func(labels []string, current string) (string, error)`
  - `Pipeline.BuildSSH` populated in `runDeploy`
  - `provisionOne(ctx, cmd, sshCfg, user, path string, force bool) error` — extracted bootstrap body, returns `errAlreadyBootstrapped` sentinel when skipped
  - tomlEncode writes `builder = "..."` plus `build_host`/`build_user`/`build_path` when non-empty
  - `tui.PickEnv` gains a `start int` parameter (pre-tick)

- [ ] **Step 1: Extend tui.PickEnv with a start index (pre-tick)**

The builder picker must show the current value pre-ticked. `PickEnv`
currently hardcodes start index 0; give it a `start` parameter
(`NewSinglePicker` already accepts one):

In `internal/tui/env.go`:

```go
// PickEnv opens a single-select Picker over the given labels and
// returns the chosen index. start is the initially highlighted (and
// pre-ticked) label index. Returns -1 with a nil error when labels
// is empty, and ErrAborted when the user hits q / Ctrl+C.
func PickEnv(labels []string, start int) (int, error) {
	if len(labels) == 0 {
		return -1, nil
	}
	if start < 0 || start >= len(labels) {
		start = 0
	}
	p := NewSinglePicker("Env to bootstrap", labels, start)
	res, err := p.Run()
	if err != nil {
		return -1, err
	}
	if res.Aborted {
		return -1, ErrAborted
	}
	return res.Indices[0], nil
}
```

Update the call site in `internal/cli/bootstrap.go` (the `pickEnvTUI`
seam var already points at `tui.PickEnv`, so only the call changes):

```go
			idx, err := pickEnvTUI(labels, 0)
```

Update `internal/tui/env_test.go` to pass the start index (existing
tests exercise the empty case and picker construction):

```go
func TestPickEnvBuildsSinglePicker(t *testing.T) {
	idx, err := PickEnv([]string{"stage (s.example.com)", "production (p.example.com)"}, 1)
	if err != nil {
		t.Fatalf("PickEnv() = %v, want nil", err)
	}
	_ = idx
}
```

- [ ] **Step 2: Write the failing buildmode tests**

Create `internal/cli/buildmode_test.go`:

```go
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
	cmd.SetArgs([]string{"production"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("buildmode: %v", err)
	}
	if !strings.Contains(out.String(), "no changes") {
		t.Errorf("output = %q, want no-changes message", out.String())
	}
}
```

Note: `writeServiceToml` already exists in service_test.go. The picker seam signature is `func(labels []string, current string) (string, error)` — matches the new `pickBuilderTUI` seam defined in the implementation step.

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test -race ./internal/cli/ -run TestBuildmode`
Expected: FAIL — `pickBuilderTUI`, `newBuildmodeCmd` undefined.

- [ ] **Step 4: Implement the buildmode command**

Create `internal/cli/buildmode.go`:

```go
package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Bonnary/pier/internal/config"
	"github.com/Bonnary/pier/internal/tui"
)

// pickBuilderTUI is a test seam for the builder picker (the shared
// single-select picker, with the current value pre-ticked).
var pickBuilderTUI = func(labels []string, current string) (string, error) {
	idx := 0
	for i, l := range labels {
		if l == current {
			idx = i
		}
	}
	got, err := tui.PickEnv(labels, idx)
	if err != nil {
		return "", err
	}
	return labels[got], nil
}

func newBuildmodeCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "buildmode <env>",
		Short: "Choose where the production image is built",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBuildmode(cmd, args[0])
		},
	}
}

// runBuildmode edits [deploy.<env>].builder with an interactive
// picker. Choosing build_server additionally prompts for the build
// server's host, user, and path.
func runBuildmode(cmd *cobra.Command, env string) error {
	if !tuiForTest() {
		return cliError("pier buildmode is interactive; run it in a terminal or edit pier.toml directly")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	dc, ok := cfg.Deploy[env]
	if !ok {
		return cliError("no [deploy.%s] section in pier.toml", env)
	}
	labels := []string{"host_server", "local_machine", "build_server"}
	picked, err := pickBuilderTUI(labels, dc.BuilderMode())
	if err != nil {
		if errors.Is(err, tui.ErrAborted) {
			return AbortedError()
		}
		return err
	}
	if picked == dc.BuilderMode() {
		fmt.Fprintln(cmd.OutOrStdout(), "no changes")
		return nil
	}
	dc.Builder = picked
	if picked == "build_server" {
		dc.BuildHost = promptString("build server host: ", dc.BuildHost)
		dc.BuildUser = promptString("build server user: ", dc.BuildUser)
		dc.BuildPath = promptString("build server path: ", dc.BuildPath)
	}
	cfg.Deploy[env] = dc
	if err := writeConfig(cfgPath, *cfg); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "builder: %s\n", picked)
	return nil
}

// promptString reads one line from stdin, trimming whitespace. def is
// returned when the input is empty. Prompts go to stderr so --json
// stdout stays clean.
func promptString(prompt, def string) string {
	fmt.Fprintf(os.Stderr, "%s", prompt)
	r := bufio.NewReader(os.Stdin)
	s, _ := r.ReadString('\n')
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	return s
}
```

In `internal/cli/root.go`, register the command (after `newServiceCmd`):

```go
	root.AddCommand(newBuildmodeCmd(stdout, stderr))
```

- [ ] **Step 5: Implement newBuildSSHConfig and wire Pipeline.BuildSSH**

In `internal/cli/helpers.go`, after `newSSHConfig`:

```go
// newBuildSSHConfig builds the SSHConfig for the env's dedicated
// build server ([deploy.<env>].build_host/build_user). The password
// prompt names the build server so the user knows which machine they
// are authenticating to.
func newBuildSSHConfig(dc config.DeployConfig) deploy.SSHConfig {
	return deploy.SSHConfig{
		Host:    dc.BuildHost,
		User:    dc.BuildUser,
		KeyPath: sshKeyPath(),
		PasswordPrompt: func() (string, error) {
			return readPassword(fmt.Sprintf("SSH password for %s@%s: ", dc.BuildUser, dc.BuildHost))
		},
	}
}
```

In `internal/cli/deploy.go`, add `BuildSSH: newBuildSSHConfig(dc),` to the Pipeline literal.

- [ ] **Step 6: Extend tomlEncode**

In `internal/cli/toml.go`, after the `branch` line:

```go
		fmt.Fprintf(&b, "builder = %q\n", dc.BuilderMode())
		if dc.BuildHost != "" {
			fmt.Fprintf(&b, "build_host = %q\n", dc.BuildHost)
		}
		if dc.BuildUser != "" {
			fmt.Fprintf(&b, "build_user = %q\n", dc.BuildUser)
		}
		if dc.BuildPath != "" {
			fmt.Fprintf(&b, "build_path = %q\n", dc.BuildPath)
		}
```

- [ ] **Step 7: Bootstrap both machines**

In `internal/cli/bootstrap.go`, add the sentinel and extract the per-machine flow:

```go
// errAlreadyBootstrapped marks a skipped machine so the caller can
// print its "already bootstrapped" line.
var errAlreadyBootstrapped = errors.New("already bootstrapped")

// provisionOne runs the full bootstrap flow (skip-check, sudo prompt,
// provision, path creation) for one machine and prints its status.
func provisionOne(ctx context.Context, cmd *cobra.Command, sshCfg deploy.SSHConfig, user, path string, force bool) error {
	if !force {
		ok, err := probeEnvFn(ctx, sshCfg)
		if err != nil {
			return err
		}
		if ok {
			fmt.Fprintf(cmd.OutOrStdout(), "already bootstrapped — skipping\n")
			return errAlreadyBootstrapped
		}
	}
	pw, err := readSudoPwd(fmt.Sprintf("sudo password for %s@%s: ", sshCfg.User, sshCfg.Host))
	if err != nil {
		return err
	}
	bootstrap := func() error {
		return bootstrapEnvFn(ctx, sshCfg, pw, deploy.BootstrapOpts{
			User:     user,
			Force:    force,
			Path:     path,
			OnStdout: func(line string) { fmt.Fprintln(cmd.OutOrStdout(), line) },
			OnStderr: func(line string) { fmt.Fprintln(cmd.ErrOrStderr(), line) },
		})
	}
	err = bootstrap()
	if errors.Is(err, deploy.ErrSudoWrongPassword) {
		pw, err = readSudoPwd("wrong password — try again: ")
		if err != nil {
			return err
		}
		err = bootstrap()
	}
	if errors.Is(err, deploy.ErrSudoNotSudoers) {
		return fmt.Errorf("%w: add %q to sudoers on %s first, or bootstrap as a different user",
			err, sshCfg.User, sshCfg.Host)
	}
	if err != nil {
		return err
	}
	return nil
}
```

Replace the body of the env loop in `runBootstrap`:

```go
	for _, env := range envs {
		dc := cfg.Deploy[env]
		if err := provisionOne(cmd.Context(), cmd, newSSHConfig(dc), dc.User, dc.Path, f.force); err != nil {
			if errors.Is(err, errAlreadyBootstrapped) {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: already bootstrapped — skipping\n", env)
				continue
			}
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s: done\n", env)
		if dc.BuilderMode() == "build_server" {
			buildSSH := newBuildSSHConfig(dc)
			if err := provisionOne(cmd.Context(), cmd, buildSSH, dc.BuildUser, dc.BuildPath, f.force); err != nil {
				if errors.Is(err, errAlreadyBootstrapped) {
					fmt.Fprintf(cmd.OutOrStdout(), "%s (build server): already bootstrapped — skipping\n", env)
					continue
				}
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s (build server): done\n", env)
		}
	}
```

This preserves the existing `"%s: already bootstrapped — skipping"` and `"%s: done"` lines for the host (existing bootstrap_test assertions keep passing) and adds the build-server pair when `build_server` is set.

- [ ] **Step 8: Add a bootstrap both-machines test**

Add to `internal/cli/bootstrap_test.go`:

```go
func TestBootstrapBuildServerModeProvisionsBoth(t *testing.T) {
	dir := t.TempDir()
	writeServiceToml(t, dir, "[deploy.production]\nhost=\"h\"\nuser=\"u\"\npath=\"/srv/x\"\nbranch=\"main\"\nbuilder=\"build_server\"\nbuild_host=\"bh\"\nbuild_user=\"bu\"\nbuild_path=\"/srv/build\"\n")
	oldCfg := cfgPath
	cfgPath = filepath.Join(dir, "pier.toml")
	defer func() { cfgPath = oldCfg }()

	oldProbe, oldBoot, oldPwd := probeEnvFn, bootstrapEnvFn, readSudoPwd
	probeEnvFn = func(ctx context.Context, cfg deploy.SSHConfig) (bool, error) { return false, nil }
	provisioned := map[string]bool{}
	bootstrapEnvFn = func(ctx context.Context, cfg deploy.SSHConfig, pw string, opts deploy.BootstrapOpts) error {
		provisioned[cfg.Host] = true
		if opts.Path == "/srv/build" {
			return nil
		}
		if opts.Path == "/srv/x" {
			return nil
		}
		return fmt.Errorf("unexpected path %q", opts.Path)
	}
	readSudoPwd = func(prompt string) (string, error) { return "pw", nil }
	defer func() { probeEnvFn, bootstrapEnvFn, readSudoPwd = oldProbe, oldBoot, oldPwd }()

	var out, errOut bytes.Buffer
	cmd := newBootstrapCmd(&out, &errOut)
	cmd.SetArgs([]string{"production"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if !provisioned["h"] || !provisioned["bh"] {
		t.Errorf("provisioned = %v, want both h and bh", provisioned)
	}
	if !contains(out.String(), "production: done") {
		t.Errorf("output missing host done line: %q", out.String())
	}
	if !contains(out.String(), "production (build server): done") {
		t.Errorf("output missing build-server done line: %q", out.String())
	}
}
```

Check imports in bootstrap_test.go (`context`, `deploy`, `fmt`, `bytes`, `path/filepath`); add any missing ones.

- [ ] **Step 9: Run tests to verify they pass**

Run: `go test -race ./internal/cli/`
Expected: PASS — existing bootstrap/service/init tests unchanged; new buildmode and bootstrap tests pass.

- [ ] **Step 10: Run all tests and lint**

Run: `go test -race ./... && golangci-lint run`
Expected: PASS.

- [ ] **Step 11: Commit**

```bash
git add internal/cli/buildmode.go internal/cli/buildmode_test.go internal/cli/helpers.go internal/cli/deploy.go internal/cli/bootstrap.go internal/cli/bootstrap_test.go internal/cli/toml.go internal/cli/root.go
git commit -m "feat(cli): buildmode command, build-server SSH config, bootstrap both machines"
```

---

### Task 8: Deploy TUI transfer phase + docs

**Files:**
- Modify: `internal/tui/deploy.go`
- Modify: `README.md`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: `deploy.Pipeline.DeployEnv.BuilderMode()`.
- Produces: TUI phase list includes `transfer` between `build` and `up` in the image modes.

- [ ] **Step 1: Write the failing test**

Add to `internal/tui/deploy_test.go`:

```go
func TestRunPhaseListIncludesTransferInImageModes(t *testing.T) {
	for _, b := range []string{"local_machine", "build_server"} {
		p := &deploy.Pipeline{DeployEnv: config.DeployConfig{Builder: b}}
		phases := deployPhases(p)
		got := []string{}
		for _, ph := range phases {
			got = append(got, ph.Name)
		}
		found := false
		for i, name := range got {
			if name == "transfer" {
				found = true
				if i == 0 || got[i-1] != "build" {
					t.Errorf("%s: transfer not right after build: %v", b, got)
				}
			}
		}
		if !found {
			t.Errorf("%s: phase list %v missing transfer", b, got)
		}
	}
}

func TestRunPhaseListOmitsTransferInHostMode(t *testing.T) {
	p := &deploy.Pipeline{DeployEnv: config.DeployConfig{}}
	for _, ph := range deployPhases(p) {
		if ph.Name == "transfer" {
			t.Fatal("host_server phase list must not include transfer")
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race ./internal/tui/ -run TestRunPhaseList`
Expected: FAIL — `deployPhases` undefined.

- [ ] **Step 3: Implement the phase list builder**

In `internal/tui/deploy.go`, extract the phase list into a function and use it in `Run`:

```go
// deployPhases returns the phase list for the pipeline's builder mode.
// The image modes stream the built image to the host between build and
// up, so they get an extra transfer phase.
func deployPhases(p *deploy.Pipeline) []phase {
	phases := []phase{
		{Name: "preflight"}, {Name: "render"}, {Name: "sync"},
		{Name: "build"},
	}
	if p.DeployEnv.BuilderMode() != "host_server" {
		phases = append(phases, phase{Name: "transfer"})
	}
	return append(phases,
		phase{Name: "up"}, phase{Name: "health"}, phase{Name: "commit"})
}
```

Replace the literal in `Run`:

```go
	phases := deployPhases(p)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/tui/`
Expected: PASS.

- [ ] **Step 5: Update README**

In `README.md`:

1. In the Features list, after the `pier deploy <env>` bullet, add:

```
- **`pier buildmode <env>`** — choose where the production image is
  built: `host_server` (the deploy host builds in place, the
  default), `local_machine` (your machine builds it), or
  `build_server` (a dedicated remote machine). The two image modes
  stream the finished image to the host over SSH (`docker save` →
  `docker load`) in a `transfer` deploy phase — no registry, no
  temp files. The host receives only the files it needs to run the
  stack.
```

2. In the Commands table, after the `pier service` row:

```
| `pier buildmode <env>` | Pick where the production image is built (`host_server` / `local_machine` / `build_server`); prompts for build host/user/path when `build_server` is chosen. |
```

3. In the Configuration section, after the `[deploy.production]` block example, add a paragraph:

```
`[deploy.<env>].builder` chooses where the production image is built.
`"host_server"` (the default when the key is absent) builds on the
deploy host itself. `"local_machine"` builds on the machine running
`pier` (Docker required locally). `"build_server"` builds on a
dedicated machine configured with `build_host`, `build_user`, and
`build_path` (the path the source tree is synced to and built in).
Both image modes sync only the deploy files (`docker-compose.prod.yml`,
`.env.production`, `docker/nginx/default.conf`) to the host, stream
the built image over SSH, and render the prod compose with
`image: <project>:current` instead of a build context. `pier bootstrap
<env>` provisions both the host and the build server when
`build_server` is set.
```

4. In the bootstrap feature bullet, mention the build server:

Change `pier bootstrap <env>` bullet's ending to add: when
`[deploy.<env>].builder = "build_server"`, the same invocation also
provisions the build server and its `build_path`.

5. Update the `pier bootstrap <env>` checklist item in the manual
   verification checklist section:

```
- [ ] `pier bootstrap <env>` with `builder = "build_server"` — host
  and build server both provisioned, two `done` lines printed
```

- [ ] **Step 6: Update CHANGELOG**

Add under an `Unreleased` heading (or the top of the changelog, matching its existing format):

```
## Unreleased

### Added

- `pier buildmode <env>` — choose where the production image is built
  (host_server / local_machine / build_server); image modes stream the
  image to the host over SSH in a new `transfer` deploy phase.
- `[deploy.<env>].builder` / `build_host` / `build_user` / `build_path`
  configuration for build server modes; `pier bootstrap <env>`
  provisions both machines when `build_server` is set.
- Real git SHA image tags (timestamp fallback) replace the hardcoded
  `gitsha` placeholder; `docker tag` wiring fixes `pier rollback` in
  every builder mode.
```

- [ ] **Step 7: Run all tests and lint**

Run: `go test -race ./... && golangci-lint run`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/tui/deploy.go internal/tui/deploy_test.go README.md CHANGELOG.md
git commit -m "feat(tui): transfer phase in image modes; docs for build server modes"
```

---

### Task 9: Integration test — local_machine end-to-end

**Files:**
- Modify: `internal/deploy/deploy_integration_test.go`

**Interfaces:**
- Consumes: the env-gated integration style of `bootstrap_integration_test.go` (`PIER_TEST_SSH_HOST` etc.), the full `Pipeline` in `local_machine` mode with a real docker daemon on both the local machine and the remote host.

- [ ] **Step 1: Write the failing integration test**

Replace the stub body in `internal/deploy/deploy_integration_test.go` (keep the `//go:build integration` tag; drop the testcontainers import and the `t.Skip`):

```go
//go:build integration

package deploy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Bonnary/pier/internal/config"
)

// TestPipelineLocalMachineEndToEnd drives a full local_machine deploy
// against a real SSH host with a real docker daemon: the image is
// built locally, streamed into the remote daemon (docker load), and
// the app comes up. Requires docker locally and on the host.
//
// Run:
//
//	PIER_TEST_SSH_HOST=192.168.122.63 PIER_TEST_SSH_USER=deploy \
//	PIER_TEST_SSH_KEY=~/.ssh/id_ed25519 \
//	go test -tags=integration -run TestPipelineLocalMachineEndToEnd ./internal/deploy/
func TestPipelineLocalMachineEndToEnd(t *testing.T) {
	host := os.Getenv("PIER_TEST_SSH_HOST")
	if host == "" {
		t.Skip("PIER_TEST_SSH_HOST not set")
	}
	user := os.Getenv("PIER_TEST_SSH_USER")
	if user == "" {
		user = "deploy"
	}
	key := os.Getenv("PIER_TEST_SSH_KEY")
	if key == "" {
		key = filepath.Join(os.Getenv("HOME"), ".ssh", "id_ed25519")
	}
	ctx := context.Background()

	t.Chdir(t.TempDir())
	if err := os.WriteFile(".env.production", []byte("APP_KEY=base64:test\nAPP_DEBUG=false\nDB_PASSWORD=test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	remote := os.Getenv("PIER_TEST_DEPLOY_PATH")
	if remote == "" {
		remote = "/tmp/pier-it-" + time.Now().Format("150405")
	}

	dc := config.DeployConfig{
		Host: host, User: user, Path: remote, Branch: "main",
		Builder: "local_machine",
	}
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "pierit", Domain: "pierit.example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: "8.3", Node: "22"},
		Deploy:  map[string]config.DeployConfig{"production": dc},
	}
	p := &Pipeline{
		Config: cfg, Env: "production", DeployEnv: dc,
		Logger: &stdTestLogger{t},
		SSH:    SSHConfig{Host: host, User: user, KeyPath: key},
		Health: HealthConfig{URL: "http://" + host + ":80/up", Timeout: 5 * time.Second, Interval: time.Second, MaxAttempts: 3},
		Now:    time.Now,
	}
	if err := p.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	client, err := Dial(ctx, SSHConfig{Host: host, User: user, KeyPath: key})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()
	images, _, err := client.Run(ctx, "docker images --format '{{.Repository}}:{{.Tag}}'")
	if err != nil {
		t.Fatalf("docker images: %v", err)
	}
	if !contains(string(images), "pierit:current") {
		t.Errorf("remote images missing pierit:current:\n%s", images)
	}
}
```

Add the `stdTestLogger` helper at the bottom of the same file (the pipeline needs a Logger):

```go
// stdTestLogger logs deploy events through t.Logf.
type stdTestLogger struct{ t *testing.T }

func (l *stdTestLogger) Emit(_ Event)        {}
func (l *stdTestLogger) PhaseStart(n string) { l.t.Logf("phase %s", n) }
func (l *stdTestLogger) PhaseEnd(n string, err error) {
	if err != nil {
		l.t.Logf("phase %s failed: %v", n, err)
		return
	}
	l.t.Logf("phase %s ok", n)
}
func (l *stdTestLogger) Log(_ string, format string, args ...any) { l.t.Logf(format, args...) }
func (l *stdTestLogger) JSON() bool                               { return false }
func (l *stdTestLogger) Writer() io.Writer                        { return io.Discard }
```

(The `Writer()` method must match the `Logger` interface in `internal/deploy/logger.go`; add `io` to the imports.)

- [ ] **Step 2: Run the integration test**

Run: `go test -tags=integration -timeout 15m -run TestPipelineLocalMachineEndToEnd ./internal/deploy/`
Expected: SKIP without `PIER_TEST_SSH_HOST`; with a real SSH host + local docker daemon, PASS (the pipeline builds locally, transfers, and the remote daemon holds `pierit:current`).

- [ ] **Step 3: Commit**

```bash
git add internal/deploy/deploy_integration_test.go
git commit -m "test(deploy): local_machine end-to-end integration test"
```

---

### Task 10: Final verification

- [ ] **Step 1: Run the full unit suite**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 2: Run the linter**

Run: `golangci-lint run`
Expected: clean.

- [ ] **Step 3: Manual smoke — buildmode on a real project**

```bash
go build -o pier ./cmd/pier
cd /tmp/some-laravel-app
../pier buildmode production   # pick local_machine; verify pier.toml
../pier buildmode production   # pick build_server; verify prompts + pier.toml
```
Expected: picker shows the current value pre-ticked; switching to build_server writes all three build_* fields.

- [ ] **Step 4: Manual smoke — deploy with local_machine against a local docker daemon**

Follow the README checklist for `pier deploy`, with `builder = "local_machine"` in pier.toml. Expected: phases run through `transfer`, `image <project>:<sha> (N bytes) loaded on <host>` is logged, `docker images` on the host shows `<project>:<sha>` and `<project>:current`.

- [ ] **Step 5: Commit any final fixes**

```bash
git add -A
git commit -m "chore: final verification fixes"
```
(Skip this commit if nothing changed.)
