# pier init Full Deploy Setup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `pier init` walks the user through the full deploy setup — deploy host/user/path/branch and the build machine (builder mode, with build server host/user/path when `build_server`) — and the now-redundant `pier buildmode <env>` command is removed.

**Architecture:** The builder question becomes the 4th state of the existing Bubble Tea init TUI (PHP → Node → Services → Builder, `host_server` pre-ticked). Free-text deploy fields are plain prompts after the TUI (empty host/user/path = skip; branch defaults to `main` but is only written when all three are present, so the config stays valid). `[deploy.production]` is always written now. Non-TUI environments get a numbered text fallback for the builder question. The `buildmode` command, its test file, and its root registration are deleted.

**Tech Stack:** Go 1.25+, cobra, Bubble Tea, BurntSushi/toml (read), custom `tomlEncode` (write).

## Global Constraints

- Builder values are exactly `"host_server"`, `"local_machine"`, `"build_server"`; init pre-ticks `host_server`.
- `[deploy.production]` is always written by init (today only when services > 0); `TestInitWithoutServicesNoDeployScaffold` is replaced, not kept.
- Deploy target prompts: host/user/path accept empty (skip); `branch` prompts with default `main` but is written **only when host AND user AND path are all non-empty** — the config validator rejects any deploy section where some but not all of host/user/path/branch are set.
- `builder = "build_server"` requires non-empty build host/user/path: `promptRequired` reprompts and gives up after 3 consecutive empty answers with an error naming `pier.toml`.
- q/Ctrl+C anywhere in the TUI → `AbortedError()`, pier.toml not written.
- Flags `--builder/--host/--user/--path/--build-host/--build-user/--build-path` skip their prompts; the TUI runs only when `tuiForTest()` is true and `--php/--node/--services/--builder` are all unset (host/path flags do not gate the TUI).
- `tomlEncode` is NOT changed: it already renders `builder`/`build_*` and writes empty `host`/`user`/`path`/`branch` as empty strings — keep that.
- `pier buildmode` is deleted; the only later way to change the builder is editing `pier.toml`.
- `tui.PickEnv` and `newBuildSSHConfig` stay (used by bootstrap/deploy).
- Run `go test -race ./...` and `golangci-lint run` before each commit.

---

### Task 1: TUI — builder picker state

**Files:**
- Modify: `internal/tui/init.go`
- Modify: `internal/tui/init_test.go`
- Modify: `internal/cli/init_test.go:119` (compile-fix the stub signature only — the tree must stay green)

**Interfaces:**
- Consumes: existing `initModel` state machine, `Picker` (`NewSinglePicker(title, items, defaultIdx)`), `InitResult`.
- Produces: `InitResult.Builder string`; `RunInit(phpVersions, nodeVersions, services, builders []string) (InitResult, error)`; `initModel` with 4 pickers and 5 states (`stateBuilder` between `stateServices` and `stateDone`); `newInitModel(phpVersions, nodeVersions, services, builders []string) initModel`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/tui/init_test.go` (and update the five existing `newInitModel(...)` call sites to pass a 4th arg, `[]string{"host_server", "local_machine", "build_server"}`):

```go
func TestInitModelBuilderStateStoresChoice(t *testing.T) {
	builders := []string{"host_server", "local_machine", "build_server"}
	m := newInitModel([]string{"8.2", "8.3"}, []string{"20", "22"}, []string{"redis"}, builders)
	if m.builderPicker.cursor != 0 {
		t.Errorf("builderPicker.cursor = %d, want 0 (host_server default)", m.builderPicker.cursor)
	}
	// Step through PHP, Node, services into the builder state.
	for i := 0; i < 3; i++ {
		upd, _ := m.Update(keyMsg("enter"))
		m = upd.(initModel)
	}
	if m.state != stateBuilder {
		t.Fatalf("state = %d, want %d (stateBuilder)", m.state, stateBuilder)
	}
	upd, _ := m.Update(keyMsg("j")) // down to local_machine
	m = upd.(initModel)
	upd, _ = m.Update(keyMsg("enter"))
	m = upd.(initModel)
	if m.state != stateDone {
		t.Errorf("state = %d, want %d (stateDone)", m.state, stateDone)
	}
	if m.result.Builder != "local_machine" {
		t.Errorf("result.Builder = %q, want local_machine", m.result.Builder)
	}
}

func TestInitModelAbortOnBuilder(t *testing.T) {
	builders := []string{"host_server", "local_machine", "build_server"}
	m := newInitModel([]string{"8.2", "8.3"}, []string{"20", "22"}, []string{"redis"}, builders)
	for i := 0; i < 3; i++ {
		upd, _ := m.Update(keyMsg("enter"))
		m = upd.(initModel)
	}
	upd, _ := m.Update(keyMsg("q"))
	got := upd.(initModel)
	if !got.result.Aborted {
		t.Error("result.Aborted = false after q on builder, want true")
	}
	if got.result.Node != "22" {
		t.Errorf("result.Node = %q after abort on builder, want 22 (carried from prior step)", got.result.Node)
	}
}
```

Update `TestInitModelFlowHappyPath` in the same file: after the services-enter it must now expect `stateBuilder`, then one more `enter` and expect `stateDone` plus `result.Builder == "host_server"`:

```go
	upd, _ = m.Update(keyMsg("enter"))
	m = upd.(initModel)
	if m.state != stateBuilder {
		t.Errorf("after enter on services: state = %d, want %d (stateBuilder)", m.state, stateBuilder)
	}
	upd, _ = m.Update(keyMsg("enter"))
	m = upd.(initModel)
	if m.state != stateDone {
		t.Errorf("after enter on builder: state = %d, want %d (stateDone)", m.state, stateDone)
	}
	if m.result.Builder != "host_server" {
		t.Errorf("result.Builder = %q, want host_server", m.result.Builder)
	}
```

Also update `TestInitModelEmptyServicesOK` (lines 98-110): after its third `enter` (services confirm) the state is now `stateBuilder`, so add one more `enter` before the `stateDone` assertion:

```go
	upd, _ = upd.(initModel).Update(keyMsg("enter")) // services confirm
	upd, _ = upd.(initModel).Update(keyMsg("enter")) // builder confirm
	got := upd.(initModel)
```

In `internal/cli/init_test.go`, fix the `runInitTUI` stub (line 119) so the package compiles — this is a signature-only change, the assertions stay as they are:

```go
	runInitTUI = func(phpVersions, nodeVersions, services, builders []string) (tui.InitResult, error) {
		called = true
		_ = builders
		return tui.InitResult{
			PHP:      "8.3",
			Node:     "22",
			Services: []string{"redis"},
		}, nil
	}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race ./internal/tui/`
Expected: FAIL — `stateBuilder`, `builderPicker`, `result.Builder` undefined.

- [ ] **Step 3: Implement the TUI changes**

In `internal/tui/init.go`:

```go
type initState int

const (
	statePHP initState = iota
	stateNode
	stateServices
	stateBuilder
	stateDone
)

// InitResult is what RunInit returns: the user's PHP, Node, and
// Builder choices, the list of services they ticked in the
// multi-select, and an Aborted flag for q / Ctrl+C.
type InitResult struct {
	PHP      string
	Node     string
	Services []string
	Builder  string
	Aborted  bool
}

type initModel struct {
	state         initState
	phpPicker     *Picker
	nodePicker    *Picker
	svcPicker     *Picker
	builderPicker *Picker
	result        InitResult
}

func newInitModel(phpVersions, nodeVersions, services, builders []string) initModel {
	return initModel{
		state:         statePHP,
		phpPicker:     NewSinglePicker("PHP version", phpVersions, len(phpVersions)-1),
		nodePicker:    NewSinglePicker("Node version", nodeVersions, len(nodeVersions)-1),
		svcPicker:     NewMultiPicker("Services (space to toggle)", services, nil),
		builderPicker: NewSinglePicker("Build machine", builders, 0),
	}
}
```

In `Update`, add the builder state to the navigation switch (after the `stateServices` case):

```go
		case stateBuilder:
			u, _ := m.builderPicker.Update(msg)
			m.builderPicker = u.(*Picker)
```

In the enter switch, change the `stateServices` case to advance into the builder state, and add the `stateBuilder` case:

```go
	case stateServices:
		var picked []string
		for i, on := range m.svcPicker.picked {
			if on {
				picked = append(picked, m.svcPicker.items[i])
			}
		}
		m.result.Services = picked
		m.state = stateBuilder
	case stateBuilder:
		m.result.Builder = m.builderPicker.items[m.builderPicker.cursor]
		m.state = stateDone
		return m, tea.Quit
```

In `View`, add the builder case:

```go
	case stateBuilder:
		return m.builderPicker.View()
```

Change `RunInit`:

```go
// RunInit drives the four-picker init flow (PHP → Node → services →
// build machine). It is a thin wrapper around the internal model; the
// CLI uses it after the ShouldRun check passes.
func RunInit(phpVersions, nodeVersions, services, builders []string) (InitResult, error) {
	m := newInitModel(phpVersions, nodeVersions, services, builders)
	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return InitResult{}, err
	}
	got := final.(initModel)
	return got.result, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/tui/ ./internal/cli/`
Expected: PASS — the tui tests pass and the cli package compiles (its init tests still pass with the stub returning an empty `Builder`).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/init.go internal/tui/init_test.go internal/cli/init_test.go
git commit -m "feat(tui): build machine picker state in the init flow"
```

---

### Task 2: CLI — deploy prompts, flags, always-written deploy section

**Files:**
- Modify: `internal/cli/init.go`
- Modify: `internal/cli/init_test.go`

**Interfaces:**
- Consumes: `tui.RunInit` (new 4-param signature from Task 1), `tui.InitResult.Builder`, `config.DeployConfig` fields (`Host`, `User`, `Path`, `Branch`, `Builder`, `BuildHost`, `BuildUser`, `BuildPath`, `Services`), `cliError` (in `errors.go`).
- Produces: new `initFlags` fields `builder/host/user/path/buildHost/buildUser/buildPath` (flags `--builder/--host/--user/--path/--build-host/--build-user/--build-path`); helpers `validBuilderValue(v string) bool`, `promptBuilder(stdout io.Writer, stdin io.Reader) string`, `promptRequired(stdout io.Writer, stdin io.Reader, label, def string) (string, error)`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/cli/init_test.go`. The seed pattern for a Laravel project (artisan + composer.json) is repeated in every test below; keep using the inline writes from the existing tests:

```go
func TestInitDeployFlagsWriteFullDeploySection(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "artisan"), []byte("#!/usr/bin/env php\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"require":{"laravel/framework":"^11.0"}}`), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "init", dir,
		"--php", "8.3", "--node", "22",
		"--builder", "build_server",
		"--host", "prod.example.com", "--user", "deploy", "--path", "/srv/myapp",
		"--build-host", "build.example.com", "--build-user", "builder", "--build-path", "/srv/build",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`host = "prod.example.com"`,
		`user = "deploy"`,
		`path = "/srv/myapp"`,
		`branch = "main"`,
		`builder = "build_server"`,
		`build_host = "build.example.com"`,
		`build_user = "builder"`,
		`build_path = "/srv/build"`,
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("pier.toml missing %s:\n%s", want, got)
		}
	}
	if _, err := config.Load(filepath.Join(dir, "pier.toml")); err != nil {
		t.Errorf("init pier.toml must pass validation: %v", err)
	}
}

func TestInitPromptsForDeployFields(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "artisan"), []byte("#!/usr/bin/env php\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"require":{"laravel/framework":"^11.0"}}`), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetIn(strings.NewReader(
		"8.3\n22\nredis\n3\nprod.example.com\ndeploy\n/srv/myapp\nbuild.example.com\nbuilder\n/srv/build\n"))
	root.SetArgs([]string{"init", dir})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`builder = "build_server"`,
		`host = "prod.example.com"`,
		`user = "deploy"`,
		`path = "/srv/myapp"`,
		`build_host = "build.example.com"`,
		`build_user = "builder"`,
		`build_path = "/srv/build"`,
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("pier.toml missing %s:\n%s", want, got)
		}
	}
	if _, err := config.Load(filepath.Join(dir, "pier.toml")); err != nil {
		t.Errorf("init pier.toml must pass validation: %v", err)
	}
}

func TestInitEmptyDeployPromptsSkipFields(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "artisan"), []byte("#!/usr/bin/env php\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"require":{"laravel/framework":"^11.0"}}`), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetIn(strings.NewReader("8.3\n22\n\n1\n\n\n\n")) // services: none, builder: 1 (host_server), host/user/path: skip
	root.SetArgs([]string{"init", dir})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if err != nil {
		t.Fatal(err)
	}
	contents := string(got)
	if !strings.Contains(contents, "[deploy.production]") {
		t.Errorf("pier.toml missing [deploy.production]:\n%s", contents)
	}
	if strings.Contains(contents, `branch = "main"`) {
		t.Errorf("branch must not be written when host/user/path are empty:\n%s", contents)
	}
	if _, err := config.Load(filepath.Join(dir, "pier.toml")); err != nil {
		t.Errorf("init pier.toml must pass validation (empty deploy section is valid): %v", err)
	}
}

func TestInitBuildServerEmptyAnswerReprompts(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "artisan"), []byte("#!/usr/bin/env php\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"require":{"laravel/framework":"^11.0"}}`), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	// Prompt order: php, node, services, builder, host, user, path,
	// then (build_server) build host/user/path. Build path gets one
	// empty answer, then a real one — the reprompt must recover.
	root.SetIn(strings.NewReader("8.3\n22\n\n3\n\n\n\nbh\nbu\n\n/srv/build\n"))
	root.SetArgs([]string{"init", dir})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `build_path = "/srv/build"`) {
		t.Errorf("pier.toml missing build_path after reprompt:\n%s", got)
	}
}

func TestInitBuildServerRequiredFieldGivesUp(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "artisan"), []byte("#!/usr/bin/env php\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"require":{"laravel/framework":"^11.0"}}`), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	// build server host: three empty answers then EOF — must give up with an error.
	root.SetIn(strings.NewReader("8.3\n22\n\n3\n\n\n\n"))
	root.SetArgs([]string{"init", dir})
	root.SilenceUsage = true
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "pier.toml") {
		t.Errorf("err = %v, want error naming pier.toml after 3 empty answers", err)
	}
}
```

Update `TestInitTUIInvokedWhenTTYAndNoFlags` (line 102): the stub now returns `Builder: "local_machine"` (so no build-server prompts fire on the test's real stdin) and the test asserts the key landed in pier.toml:

```go
	runInitTUI = func(phpVersions, nodeVersions, services, builders []string) (tui.InitResult, error) {
		called = true
		return tui.InitResult{
			PHP:      "8.3",
			Node:     "22",
			Services: []string{"redis"},
			Builder:  "local_machine",
		}, nil
	}
```

and after the existing `"redis"` assertion add:

```go
	if !bytes.Contains(got, []byte(`builder = "local_machine"`)) {
		t.Errorf("builder = local_machine not in pier.toml:\n%s", got)
	}
```

Replace `TestInitWithoutServicesNoDeployScaffold` (line 274) with `TestInitAlwaysWritesDeploySection` — the deploy section is now unconditional and must stay valid when empty:

```go
func TestInitAlwaysWritesDeploySection(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "artisan"), []byte("#!/usr/bin/env php\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"require":{"laravel/framework":"^11.0"}}`), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	root := NewRootCmd(&buf, &buf)
	root.SetArgs([]string{"--config", filepath.Join(dir, "pier.toml"), "init", dir, "--php", "8.3", "--node", "22"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, buf.String())
	}
	got, _ := os.ReadFile(filepath.Join(dir, "pier.toml"))
	if !strings.Contains(string(got), "[deploy.production]") {
		t.Errorf("init without services must still scaffold [deploy.production]:\n%s", got)
	}
	if _, err := config.Load(filepath.Join(dir, "pier.toml")); err != nil {
		t.Errorf("init pier.toml must pass validation (empty deploy section is valid): %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race ./internal/cli/ -run 'TestInitDeployFlags|TestInitPromptsFor|TestInitEmptyDeploy|TestInitBuildServer|TestInitAlwaysWrites|TestInitTUIInvoked'`
Expected: FAIL — new flags not registered, `promptBuilder`/`promptRequired` undefined, deploy section missing.

- [ ] **Step 3: Implement the CLI changes**

In `internal/cli/init.go`:

Extend `initFlags`:

```go
type initFlags struct {
	php          string
	node         string
	services     []string
	devcontainer bool
	builder      string
	host         string
	user         string
	path         string
	buildHost    string
	buildUser    string
	buildPath    string
}
```

Register the flags in `newInitCmd`, after the `--services` flag:

```go
	cmd.Flags().StringVar(&f.builder, "builder", "", "build machine: host_server, local_machine, or build_server")
	cmd.Flags().StringVar(&f.host, "host", "", "deploy host (SSH target)")
	cmd.Flags().StringVar(&f.user, "user", "", "deploy user")
	cmd.Flags().StringVar(&f.path, "path", "", "deploy path on the host")
	cmd.Flags().StringVar(&f.buildHost, "build-host", "", "build server host (with --builder build_server)")
	cmd.Flags().StringVar(&f.buildUser, "build-user", "", "build server user (with --builder build_server)")
	cmd.Flags().StringVar(&f.buildPath, "build-path", "", "build server path (with --builder build_server)")
```

In `runInit`, replace everything from `php := f.php` through the `cfg := config.Config{...}` literal (currently lines 73-107) with:

```go
	php := f.php
	node := f.node
	services := f.services
	builder := f.builder
	if tuiForTest() && f.php == "" && f.node == "" && len(f.services) == 0 && f.builder == "" {
		res, err := runInitTUI(
			laravelpkg.SupportedPHPRuntimes(),
			laravelpkg.SupportedNodeVersions(),
			laravelpkg.SupportedServices(),
			[]string{"host_server", "local_machine", "build_server"},
		)
		if err != nil {
			return err
		}
		if res.Aborted {
			return AbortedError()
		}
		php = res.PHP
		node = res.Node
		services = res.Services
		builder = res.Builder
	}
	if php == "" {
		php = prompt(cmd.OutOrStdout(), cmd.InOrStdin(), "PHP version [8.3]: ", "8.3")
	}
	if node == "" {
		node = prompt(cmd.OutOrStdout(), cmd.InOrStdin(), "Node version [22]: ", "22")
	}
	if services == nil {
		servicesStr := prompt(cmd.OutOrStdout(), cmd.InOrStdin(), "Services (comma-separated, blank for none) [redis,mailpit,s3]: ", "")
		if servicesStr != "" {
			services = splitCSV(servicesStr)
		}
	}
	if builder == "" {
		builder = promptBuilder(cmd.OutOrStdout(), cmd.InOrStdin())
	}
	if !validBuilderValue(builder) {
		return cliError("--builder %q: must be host_server, local_machine, or build_server", builder)
	}
	host := f.host
	if host == "" {
		host = prompt(cmd.OutOrStdout(), cmd.InOrStdin(), "Deploy host (SSH target, enter to skip): ", "")
	}
	user := f.user
	if user == "" {
		user = prompt(cmd.OutOrStdout(), cmd.InOrStdin(), "Deploy user (enter to skip): ", "")
	}
	path := f.path
	if path == "" {
		path = prompt(cmd.OutOrStdout(), cmd.InOrStdin(), "Deploy path (enter to skip): ", "")
	}
	buildHost, buildUser, buildPath := f.buildHost, f.buildUser, f.buildPath
	if builder == "build_server" {
		if buildHost, err = promptRequired(cmd.OutOrStdout(), cmd.InOrStdin(), "Build server host: ", buildHost); err != nil {
			return err
		}
		if buildUser, err = promptRequired(cmd.OutOrStdout(), cmd.InOrStdin(), "Build server user: ", buildUser); err != nil {
			return err
		}
		if buildPath, err = promptRequired(cmd.OutOrStdout(), cmd.InOrStdin(), "Build server path: ", buildPath); err != nil {
			return err
		}
	}
	dc := config.DeployConfig{
		Services: services,
		Builder:  builder,
	}
	if host != "" {
		dc.Host = host
	}
	if user != "" {
		dc.User = user
	}
	if path != "" {
		dc.Path = path
	}
	if host != "" && user != "" && path != "" {
		dc.Branch = "main"
	}
	if builder == "build_server" {
		dc.BuildHost, dc.BuildUser, dc.BuildPath = buildHost, buildUser, buildPath
	}
	cfg := config.Config{
		Project: config.ProjectConfig{Name: filepath.Base(abs), Domain: filepath.Base(abs) + ".example.com"},
		Stack:   config.StackConfig{Type: "laravel", PHP: php, Node: node, Services: services},
		Deploy:  map[string]config.DeployConfig{"production": dc},
	}
```

(The `err` variable comes from the existing `s, err := laravelpkg.New().DefaultConfig(), error(nil)` line above; `buildHost, err = promptRequired(...)` uses `=` because both are already declared.)

Add the three helpers at the bottom of the file (after `splitCSV`):

```go
// validBuilderValue reports whether v is one of the three builder
// modes config accepts.
func validBuilderValue(v string) bool {
	return v == "host_server" || v == "local_machine" || v == "build_server"
}

// promptBuilder asks the build-machine question as a numbered plain
// prompt — the TUI-free fallback when bubbletea cannot run. Defaults
// to host_server.
func promptBuilder(stdout io.Writer, stdin io.Reader) string {
	fmt.Fprintln(stdout, "Where should the production image be built?")
	fmt.Fprintln(stdout, "  1) host_server — on the deploy host (default)")
	fmt.Fprintln(stdout, "  2) local_machine — on this machine")
	fmt.Fprintln(stdout, "  3) build_server — on a dedicated server")
	ans := prompt(stdout, stdin, "choose [1]: ", "1")
	switch ans {
	case "2", "local_machine":
		return "local_machine"
	case "3", "build_server":
		return "build_server"
	}
	return "host_server"
}

// promptRequired reads a non-empty line from stdin, reprompting on
// empty answers. It cannot tell an empty line from EOF (the shared
// prompt helper returns the default for both), so after 3 consecutive
// empty answers it gives up with an error telling the user to edit
// pier.toml instead.
func promptRequired(stdout io.Writer, stdin io.Reader, label, def string) (string, error) {
	for empty := 0; ; {
		v := prompt(stdout, stdin, label, def)
		if v != "" {
			return v, nil
		}
		empty++
		if empty >= 3 {
			return "", fmt.Errorf("%s: required value missing (3 empty answers); edit pier.toml instead", label)
		}
	}
}
```

Check the imports of `init.go`: no new imports are needed — the added helpers use only `fmt` and `io` (both already imported), and `config` is already imported (line 11).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/cli/`
Expected: PASS — the new deploy tests pass; `TestInitScaffoldsDeployProductionServices` still passes (its config has no deploy flags, the deploy section is written with `builder = "host_server"` and passes validation).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/init.go internal/cli/init_test.go
git commit -m "feat(cli): init asks deploy target and build machine, always writes deploy section"
```

---

### Task 3: Remove `pier buildmode`

**Files:**
- Delete: `internal/cli/buildmode.go`
- Delete: `internal/cli/buildmode_test.go`
- Modify: `internal/cli/root.go` (line 51)

**Interfaces:**
- Consumes: nothing — nothing outside `buildmode.go`/`buildmode_test.go` references `newBuildmodeCmd`, `runBuildmode`, `pickBuilderTUI`, or `promptString` (verified: `grep` across the repo matches only those two files plus docs).
- Produces: no `buildmode` command; README/CHANGELOG rows are cleaned up in Task 4.

- [ ] **Step 1: Delete the files and the registration**

Run:

```bash
git rm internal/cli/buildmode.go internal/cli/buildmode_test.go
```

In `internal/cli/root.go`, remove this line (51):

```go
	root.AddCommand(newBuildmodeCmd(stdout, stderr))
```

- [ ] **Step 2: Verify nothing else references the removed symbols**

Run: `grep -rn "buildmode\|pickBuilderTUI\|promptString" internal/ --include='*.go'`
Expected: no matches (remaining `promptString`-like usages such as `prompt` in init.go are different identifiers).

- [ ] **Step 3: Run the full test suite**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/root.go
git commit -m "chore(cli): remove buildmode command, superseded by init deploy setup"
```

---

### Task 4: Docs — README and CHANGELOG

**Files:**
- Modify: `README.md`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: the behavior implemented in Tasks 1-3.
- Produces: README documents the init deploy questions and the removal; CHANGELOG entry rewritten.

- [ ] **Step 1: Update README features**

Replace the `pier init` bullet (README lines 56-59) with:

```markdown
- **`pier init`** — Detect Laravel, write `pier.toml`, generate
  `docker-compose.yml`, runtime Dockerfiles, and a matching
  `vite.config.ts` patch in one pass. Smart-merges into an existing
  `docker-compose.yml` with warn-and-confirm on unknown keys. Asks
  the full deploy setup too: host, user, path, branch, and the build
  machine (`host_server` / `local_machine` / `build_server`, plus
  build host/user/path when `build_server` is chosen).
```

Remove the `pier buildmode` bullet (lines 80-87) entirely, and fold the
build-mode streaming explanation into the `pier deploy` bullet (lines
76-79):

```markdown
- **`pier deploy <env>`** — Build, sync, up, health-check, and
  commit a production image tag over SSH. A Bubble Tea TUI shows
  live phase progress. Key auth is tried first; password-only
  servers get an interactive prompt. The image is built on the
  deploy host by default; `pier init` can pick `local_machine` or
  `build_server`, which stream the finished image to the host over
  SSH (`docker save` → `docker load`) in a `transfer` deploy phase —
  no registry, no temp files.
```

- [ ] **Step 2: Update README commands table**

Replace the `pier init [path]` row (line 245) with:

```markdown
| `pier init [path]` | Detect Laravel, write `pier.toml`, generate `docker-compose.yml` + runtime, patch `vite.config.ts`. Prompts for the deploy target (host/user/path/branch) and the build machine; `--builder` / `--host` / `--user` / `--path` / `--build-host` / `--build-user` / `--build-path` skip the prompts. |
```

Remove the `pier buildmode <env>` row (line 252) entirely.

- [ ] **Step 3: Update CHANGELOG**

Replace the `pier buildmode` Added entry (lines 7-9) with:

```markdown
- `pier init` asks the full deploy setup — deploy host/user/path/branch
  and the build machine (host_server / local_machine / build_server,
  with build host/user/path when build_server); `--builder` /
  `--host` / `--user` / `--path` / `--build-host` / `--build-user` /
  `--build-path` flags skip the prompts.
```

Add a `### Removed` section after the Added list (after line 27):

```markdown
### Removed

- `pier buildmode <env>` — the init flow now asks the build-machine
  question; change it later by editing `pier.toml`.
```

- [ ] **Step 4: Verify no stale references**

Run: `grep -rn "buildmode" README.md CHANGELOG.md`
Expected: no matches.

- [ ] **Step 5: Run all tests and lint**

Run: `go test -race ./... && golangci-lint run`
Expected: PASS / clean (the pre-existing `gocyclo`/`errcheck` findings in `sftp_openssh_test.go` and the `goimports` finding in `dev_test.go` predate this plan — leave them).

- [ ] **Step 6: Commit**

```bash
git add README.md CHANGELOG.md
git commit -m "docs: init deploy setup, drop buildmode references"
```

---

### Task 5: Final verification

- [ ] **Step 1: Run the full unit suite**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 2: Run the linter**

Run: `golangci-lint run`
Expected: only the pre-existing findings named in Task 4 Step 5.

- [ ] **Step 3: Manual smoke — flag-driven init**

```bash
go build -o pier ./cmd/pier
cd /tmp/some-laravel-app
../pier init --php 8.3 --node 22 --builder build_server \
  --host prod.example.com --user deploy --path /srv/app \
  --build-host build.example.com --build-user builder --build-path /srv/build
cat pier.toml   # verify builder/build_* keys
../pier init .  # verify: refused, pier.toml exists
```

- [ ] **Step 4: Manual smoke — interactive init**

```bash
cd /tmp/fresh-laravel-app
../pier init
```
Expected: TUI runs PHP → Node → Services → Build machine (host_server
pre-ticked); choosing build_server prompts for build host/user/path
after the TUI; empty host/user/path answers are accepted; pier.toml
passes `../pier status`.

- [ ] **Step 5: Commit any final fixes**

```bash
git add -A
git commit -m "chore: final verification fixes"
```
(Skip this commit if nothing changed.)
