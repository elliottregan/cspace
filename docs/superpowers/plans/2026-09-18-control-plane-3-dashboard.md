# Dashboard on bubbletea v2 (`internal/controlplane`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `cspace tui` with a bubbletea v2 dashboard in a new `internal/controlplane` package — fixed 24-column sidebar, detail band, footer and help overlay, every action the CLI already has — landing in the same change that deletes `internal/tui` and `internal/cli/tui_actor.go`, so `cspace tui` is never absent from the branch.

**Architecture:** `internal/controlplane` is the bubbletea v2 UI layer and nothing else: it reads through a consumer-defined `Data` interface and acts through a consumer-defined `Actor` interface, both satisfied from `internal/cli`, so it never imports `internal/cli`. `internal/control` stays the data/action boundary and grows exactly three things this needs (a stats-free snapshot, a tmux accessor, a project-aware `Up`). No panes: the main area holds the detail band, and the attach action suspends the program into `container exec` exactly as the v1 dashboard does today. The `cspace up` overlay stays on bubbletea v1, which coexists with v2 under a different module path.

**Tech Stack:** Go 1.26; `charm.land/bubbletea/v2`, `charm.land/lipgloss/v2`, `charm.land/bubbles/v2` (`key`, `help`, `spinner`, `textinput`), `charm.land/huh/v2`; `github.com/charmbracelet/x/ansi` (tests); `internal/{control,config,registry,substrate/applecontainer}`; Cobra unchanged.

**Spec:** `docs/superpowers/specs/2026-09-17-control-plane-design.md` — this plan implements **rollout step 3, "Dashboard on v2"** only.

## Global Constraints

- **Scope is rollout step 3 only.** No `internal/pane`, no embedded terminals, no supervisor view, no mouse, no image paste, no leader key dispatch, no tabs beyond one reserved line. Those are steps 4 and 5. The `cspace up` overlay (`internal/overlay`) is not migrated and not touched.
- **`cspace tui` is never absent.** The new dashboard is built alongside the old one (Tasks 3-7); **Task 8** is the only task that switches `cmd_tui.go` over and deletes `internal/tui` + `internal/cli/tui_actor.go` + `internal/cli/tui_actor_test.go`. Every task before it leaves the old dashboard compiling and passing its tests.
- **Dependency direction:** `internal/controlplane` may import `internal/control`; it must **not** import `internal/cli` or `internal/tui`. `internal/control` imports neither. Verified with `go list -deps` in Task 5.
- **Exact dependency versions** (verified against proxy.golang.org on 2026-09-18; the 2026-09-17 spike resolved the same bubbletea/lipgloss pair):
  - `charm.land/bubbletea/v2 v2.0.9`
  - `charm.land/lipgloss/v2 v2.0.6`
  - `charm.land/bubbles/v2 v2.2.1`
  - `charm.land/huh/v2 v2.0.3`
  - `github.com/charmbracelet/x/ansi v0.11.7` — go.mod at HEAD pins **v0.11.6** as an indirect; `bubbles/v2 v2.2.1` requires v0.11.7, so adding bubbles raises it, and Task 4's `go get` makes it direct for the tests' `ansi.Strip`.
  - **The v2 versions arrive in two steps, not one.** `bubbles/v2 v2.2.1`'s own go.mod asks for `bubbletea/v2 v2.0.8` and `lipgloss/v2 v2.0.5`, so Task 3's `go get` lands those; Task 4 raises lipgloss to v2.0.6 and Task 5 raises bubbletea to v2.0.9. The versions above are what the module graph settles on by the end of Task 5, which is what the plan's code is written against.
  - `github.com/charmbracelet/bubbletea v1.3.10` and `github.com/charmbracelet/lipgloss v1.1.0` **stay** — `internal/overlay` is on them. A module may require both majors; the import paths differ entirely (`charm.land/…/v2` vs `github.com/charmbracelet/…`).
  - `charm.land/glamour/v2`, `github.com/charmbracelet/x/vt` and `github.com/creack/pty` belong to rollout step 4. Do not add them.
- **bubbletea v2 API facts this plan relies on** (read from the module source, not from memory):
  - `Model.View() tea.View`, not `string`. `tea.NewView(s)` builds one; `v.AltScreen = true` replaces v1's `tea.WithAltScreen()` program option (v2 has no such option).
  - Key events arrive as `tea.KeyPressMsg`, a struct (`Text`, `Mod`, `Code`, …). Its `String()` returns `Text` when printable, else the keystroke form (`"enter"`, `"up"`, `"ctrl+c"`, `"ctrl+space"`). Tests construct them as `tea.KeyPressMsg{Code: 'j', Text: "j"}` / `tea.KeyPressMsg{Code: tea.KeyEnter}`.
  - `tea.WindowSizeMsg`, `tea.Batch`, `tea.Tick`, `tea.Quit`, `tea.ExecProcess`, `tea.Exec` all exist with v1 signatures. `tea.Exec(c tea.ExecCommand, fn tea.ExecCallback)` takes an interface (`Run() error`, `SetStdin/SetStdout/SetStderr`) — the program releases the terminal, calls `Run()`, then restores. That is how this plan keeps attach bookkeeping out of `Update`.
  - `key.Matches[K fmt.Stringer](k K, b ...key.Binding) bool` skips **disabled** bindings, and `help.Model.ShortHelpView` skips them too — so one filtered `KeyMap` drives both the gating and the footer.
  - lipgloss v2 `Style.Render` always emits ANSI (there is no render-time TTY sniffing); tests compare `ansi.Strip`-ed output.
- **Widget decision, made at plan time by reading `charm.land/bubbles/v2@v2.2.1/tree`:** the sidebar is **hand-rolled**, rendered with lipgloss v2 styles — not `bubbles/v2/tree`. Reason, checked in the source: `tree.Model` moves its cursor with `updateViewport(movement)`, which does `yOffset = clamp(yOffset+movement)` over every visible node with no skip predicate, and a node is either rendered-and-selectable or `SetHidden` (not rendered at all). Non-selectable project headers with selectable children cannot be expressed. The spec names exactly this as the fallback condition. Our rows are already a flat `[]control.Row` in display order carrying `Selectable`, and the v1 dashboard's skip-and-restore selection logic is proven — that is what this plan carries over.
- **Always build through `make`.** `internal/assets/embedded/` is gitignored and populated by `make sync-embedded`; `make vet`, `make lint` and `make test` all run it first. A bare `go build` on a clean checkout embeds an empty asset tree — and Task 3 adds a key to `lib/defaults.json`, which only reaches the binary through that sync.
- **`make check` must be green after every task**: `make fmt-check vet lint test test-scripts`.
- **Carry-forwards from the step-2 whole-branch review** (each has a task that honours it):
  - `AgentStatus` reuses the snapshot's 800 ms probe timeout — fine for the 1 s fast ticker (Task 5).
  - `control.Interrupt` treats HTTP 409 as success. Keep that for the dashboard (Task 7 test).
  - `Ports` errors for a stopped sandbox, so the slow ticker only asks for rows whose `State` is running or degraded (Task 5).
  - `Ports`, `InteractiveState`, `Events` and `Up` have never run against a real sandbox — **Task 9** is the manual verification that they do.
  - `containerName` in `internal/control` stays unexported: the actor takes names off `Row.Container`, and the one probe that needs a name the row does not carry goes through the `Client`'s own tmux driver (Task 2, Task 7).
  - Two tmux drivers exist (`cli.defaultTmux` for `cspace attach`, `Client.tmux`). The new actor uses the `Client`'s, via `Client.Tmux()` (Task 2), and `beginAttachOrWarn` takes the driver as a parameter (Task 7).
  - **Sandbox-name shape validation in `up`/`down` is deferred**, deliberately. The exposure it would close is `cmd_down.go`'s `os.RemoveAll(clonePath)`/`os.RemoveAll(sessionsPath)`, which join a sandbox name into a host path. This plan adds no new input to it: every name the dashboard hands `Up`/`Down` comes off a `control.Row`, which `Correlate` builds from registry entries and `container ls` — never from a person typing into the UI, which has no free-text sandbox field. The check belongs in `cmd_up.go`/`cmd_down.go` with the rest of name handling, not in the dashboard. No finding file records it yet; file one under `.cspace/context/findings/` before rollout step 4, which is where a name could first arrive from somewhere else.
- **Preserve these known behaviours; do not "fix" them:**
  - `.cspace/context/findings/2026-07-20-tui-down-reports-benign-teardown-warnings-as-failure.md` — `Down` reports any `warning:` text as failure. Unchanged (it lives in `internal/control`).
  - `.cspace/context/findings/2026-07-20-tui-browser-row-orphaned-when-project-has-no-registry-entry.md` — `Correlate` derives projects from registry entries only. Unchanged.
- **Findings this plan resolves** (append a timestamped entry to the finding's `## Updates` and put `(cs-finding:<slug>)` in that task's commit message):
  - Task 1 → `2026-09-18-registry-entries-do-not-record-a-project-root`
  - Task 4 → `2026-07-20-tui-row-list-has-no-viewport-scrolling`
- **Module path:** `github.com/elliottregan/cspace`. Commit messages are short imperative sentences, e.g. `Record the project root in every registry entry`.
- **Keybindings** are declared with `bubbles/v2/key` and overridable from the **user-level** cspace config, `~/.cspace/config.json`, under `tui.keys`. The leader key is declared there too but is not dispatched in step 3 — the config shape has to leave room for it.

---

## File Structure

New package `internal/controlplane` (all files `package controlplane`):

| File | Responsibility | Task |
|---|---|---|
| `controlplane.go` | package doc, `sandboxKey`, `liveState`, `keyOf` | 3 |
| `keys.go` | action-name constants, `KeyMap`, `NewKeyMap`, the per-row gate (`forRow`) and its predicates, `ShortHelp`/`FullHelp` | 3 |
| `styles.go` | the lipgloss v2 palette and the sidebar's width constants | 4, 5 |
| `view_sidebar.go` | glyph precedence, row rendering, the line window that follows the selection | 4 |
| `view_detail.go` | the detail band and its formatters (memory, uptime, last event, timestamps) | 4 |
| `poll.go` | `Data` (the query seam), the three tickers, their commands and messages | 5 |
| `actor.go` | `Actor` (the action seam), `actionResultMsg`, `Result`/`ResultLabel`/`ResultErr` | 5 |
| `model.go` | `Model`, `New`, `Init`, `Update`, selection, snapshot/notice state | 5 |
| `view.go` | `View() tea.View`: layout, the reserved tabs line, the footer | 5 |
| `input.go` | key dispatch, action start, the send input mode | 6 |
| `confirm.go` | the `huh/v2` teardown confirmation | 6 |

Tests (same package):

| File | Covers | Task |
|---|---|---|
| `keys_test.go` | default/override resolution, the defaults.json drift lock, the per-row gate | 3 |
| `view_sidebar_test.go` | glyph precedence table, grouping, ports, the window | 4 |
| `view_detail_test.go` | formatters, the band's sandbox/browser/stopped shapes | 4 |
| `model_test.go` | tickers and their in-flight guards, selection, degrade, notices, golden views | 5 |
| `input_test.go` | key dispatch and gating against a recording actor, send input, the teardown confirmation, the help overlay | 6 |

Modified elsewhere:

| File | Change | Task |
|---|---|---|
| `internal/registry/registry.go` | `Entry.ProjectRoot` | 1 |
| `internal/registry/registry_test.go` | round-trip + legacy-entry test | 1 |
| `internal/cli/cmd_up.go` | both `Register` calls write `ProjectRoot` | 1 |
| `internal/control/up.go` | `Up(ctx, project, sandbox)`, `projectRootFor` | 1 |
| `internal/control/client.go` | `Options.Project`, `Client.project`, `Client.Tmux()` | 1, 2 |
| `internal/control/snapshot.go` | `SnapshotOpts`, `SnapshotWith`, `Snapshot` delegating; the unused `Snapshotter` removed | 2, 8 |
| `internal/config/config.go` | `TUIConfig`, `Config.TUI` | 3 |
| `internal/config/user.go` | **New.** `UserConfigPath`, `LoadUser` | 3 |
| `lib/defaults.json` | the `tui.keys` block | 3 |
| `internal/cli/controlplane_actor.go` | **New.** `cpActor`, `attachExec` | 7 |
| `internal/cli/cmd_attach.go`, `cmd_attach_test.go` | `beginAttachOrWarn` takes the tmux driver | 7 |
| `internal/cli/cmd_tui.go` | builds the v2 model | 8 |
| `internal/cli/cmd_tui_test.go` | drops the v1 `--interval` assertion | 8 |
| `internal/cli/root.go` | `tui` tolerates a missing project config | 8 |
| `internal/control/control.go`, `host.go` | doc comments name `internal/controlplane` | 8 |
| `CLAUDE.md` | the control bullet, a controlplane bullet | 8 |
| `docs/superpowers/specs/2026-09-17-control-plane-design.md` | the detail band's and tabs row's step-3 placement | 8 |
| `internal/tui/`, `internal/cli/tui_actor{,_test}.go` | **Deleted** | 8 |

---

### Task 1: Record the project root in the registry and make `Up` project-aware

The sidebar's boot key must work for any project on screen, not just the one `cspace tui` was launched in. Today nothing records where a project is checked out, so `Up` is single-project by construction. This adds the field at the one place that knows it — `cspace up` — and teaches `Up` to read it back.

**Files:**
- Modify: `internal/registry/registry.go:25-37` (the `Entry` struct)
- Test: `internal/registry/registry_test.go` (append)
- Modify: `internal/cli/cmd_up.go:691-700` and `internal/cli/cmd_up.go:774-783` (both `Register` calls)
- Modify: `internal/control/up.go` (whole file)
- Modify: `internal/control/client.go` (`Options`, `Client`, `New`)
- Test: `internal/control/up_test.go` (rewrite the four `Up` tests, keep the two `runHostCommand` tests verbatim)
- Modify: `.cspace/context/findings/2026-09-18-registry-entries-do-not-record-a-project-root.md`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `registry.Entry.ProjectRoot string` (JSON `project_root`, omitempty)
  - `control.Options.Project string`
  - `func (c *Client) Up(ctx context.Context, project, sandbox string) error`
  - unexported `func (c *Client) projectRootFor(project string) (string, error)`
  - `var ErrNoProjectRoot error` (already exists in `internal/control/up.go`; its message becomes `control: no project root` — nothing asserts the old text)

- [ ] **Step 1: Write the failing registry test**

Append to `internal/registry/registry_test.go`:

```go
func TestEntryRoundTripsTheProjectRoot(t *testing.T) {
	r := &Registry{Path: filepath.Join(t.TempDir(), "sandbox-registry.json")}
	if err := r.Register(Entry{
		Project:     "myproj",
		Name:        "mercury",
		ControlURL:  "http://192.168.64.5:6201",
		ProjectRoot: "/Users/x/code/myproj",
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, err := r.Lookup("myproj", "mercury")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.ProjectRoot != "/Users/x/code/myproj" {
		t.Errorf("ProjectRoot = %q, want /Users/x/code/myproj", got.ProjectRoot)
	}

	// The on-disk key is snake_case like every other field; the daemon
	// serves this file over HTTP, so the name is part of the wire format.
	data, err := os.ReadFile(r.Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), `"project_root": "/Users/x/code/myproj"`) {
		t.Errorf("registry file missing project_root:\n%s", data)
	}
}

// An entry written before this field existed must still load, with an empty
// root rather than an error — control.Up falls back for exactly that case.
func TestLegacyEntryWithoutAProjectRootLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sandbox-registry.json")
	legacy := `{"myproj:mercury":{"control_url":"http://192.168.64.5:6201","started_at":"2026-09-18T00:00:00Z"}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	r := &Registry{Path: path}
	got, err := r.Lookup("myproj", "mercury")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.ProjectRoot != "" {
		t.Errorf("ProjectRoot = %q, want empty for a legacy entry", got.ProjectRoot)
	}
}
```

Add `"os"` and `"strings"` to that file's import block (it currently imports `fmt`, `path/filepath`, `sync`, `testing`, `time`).

- [ ] **Step 2: Run it to watch it fail**

Run: `go test ./internal/registry/ -run 'ProjectRoot|LegacyEntry' -v`
Expected: FAIL — `got.ProjectRoot undefined (type Entry has no field or method ProjectRoot)`.

- [ ] **Step 3: Add the field**

In `internal/registry/registry.go`, inside `type Entry struct`, after `BrowserContainer`:

```go
	// ProjectRoot is the host directory `cspace up` ran in — the tree its
	// boot flow read .cspace.json and .devcontainer from. Recorded so a
	// process with a different cwd, or none worth having (the daemon, the
	// control plane), can boot another sandbox for this project without
	// guessing where the project lives. Empty on entries written before
	// this field existed; readers fall back rather than fail.
	ProjectRoot string `json:"project_root,omitempty"`
```

- [ ] **Step 4: Run it to watch it pass**

Run: `go test ./internal/registry/ -run 'ProjectRoot|LegacyEntry' -v`
Expected: PASS for both.

- [ ] **Step 5: Write the failing control tests**

Replace the four `Up` tests at the top of `internal/control/up_test.go` with these six (leave `TestRunHostCommandGivesTheChildAPipeStdin` and `TestRunHostCommandSurfacesExitStatus` exactly as they are):

```go
func TestUpRunsThisBinaryInTheProjectRoot(t *testing.T) {
	var gotDir, gotBin string
	var gotArgs []string
	c := New(Options{Containers: &fakeContainers{}, ProjectRoot: "/Users/x/proj"})
	c.executable = func() (string, error) { return "/usr/local/bin/cspace", nil }
	c.runCommand = func(_ context.Context, dir, bin string, args ...string) (string, error) {
		gotDir, gotBin, gotArgs = dir, bin, args
		return "sandbox issue-42 up\n", nil
	}

	if err := c.Up(context.Background(), "alpha", "issue-42"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if gotDir != "/Users/x/proj" || gotBin != "/usr/local/bin/cspace" {
		t.Errorf("ran %q in %q", gotBin, gotDir)
	}
	if want := []string{"up", "issue-42"}; !reflect.DeepEqual(gotArgs, want) {
		t.Errorf("args = %v, want %v", gotArgs, want)
	}
}

// The registry is the source of truth for a project's root: any sandbox of
// that project names the checkout it was booted from, which is what lets one
// dashboard boot sandboxes for projects the process was never started in.
func TestUpResolvesTheRootFromAnotherProjectsEntry(t *testing.T) {
	reg := &registry.Registry{Path: filepath.Join(t.TempDir(), "reg.json")}
	for _, e := range []registry.Entry{
		{Project: "beta", Name: "venus", ProjectRoot: "/Users/x/code/beta", State: "ready"},
		{Project: "alpha", Name: "mercury", ProjectRoot: "/Users/x/code/alpha", State: "ready"},
	} {
		if err := reg.Register(e); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	c := New(Options{Containers: &fakeContainers{}, Entries: reg,
		Project: "alpha", ProjectRoot: "/Users/x/code/alpha"})
	c.executable = func() (string, error) { return "/usr/local/bin/cspace", nil }
	var gotDir string
	c.runCommand = func(_ context.Context, dir, _ string, _ ...string) (string, error) {
		gotDir = dir
		return "", nil
	}

	if err := c.Up(context.Background(), "beta", "earth"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if gotDir != "/Users/x/code/beta" {
		t.Errorf("ran in %q, want beta's own root", gotDir)
	}
}

// A project with no entry that records a root can still be booted when it is
// the Client's own project — that is the launching process's ProjectRoot.
// Any other project has no answer, and saying so beats booting the wrong
// checkout.
func TestUpRefusesAnUnknownProject(t *testing.T) {
	reg := &registry.Registry{Path: filepath.Join(t.TempDir(), "reg.json")}
	c := New(Options{Containers: &fakeContainers{}, Entries: reg,
		Project: "alpha", ProjectRoot: "/Users/x/code/alpha"})
	c.executable = func() (string, error) { return "/usr/local/bin/cspace", nil }
	ran := false
	c.runCommand = func(context.Context, string, string, ...string) (string, error) {
		ran = true
		return "", nil
	}

	err := c.Up(context.Background(), "gamma", "issue-7")
	if !errors.Is(err, ErrNoProjectRoot) {
		t.Errorf("err = %v, want ErrNoProjectRoot", err)
	}
	if !strings.Contains(fmt.Sprint(err), "gamma") {
		t.Errorf("err = %v, want it to name the project", err)
	}
	if ran {
		t.Error("Up must not run anything without a resolved root")
	}
}

func TestUpSurfacesFailureWithOutput(t *testing.T) {
	c := New(Options{Containers: &fakeContainers{}, ProjectRoot: "/Users/x/proj"})
	c.executable = func() (string, error) { return "/usr/local/bin/cspace", nil }
	c.runCommand = func(context.Context, string, string, ...string) (string, error) {
		return "error: sandbox issue-42 already exists\n", errors.New("exit status 1")
	}
	err := c.Up(context.Background(), "alpha", "issue-42")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("err = %v, want the command's output carried through", err)
	}
}

func TestUpReportsAnUnresolvableBinary(t *testing.T) {
	c := New(Options{Containers: &fakeContainers{}, ProjectRoot: "/Users/x/proj"})
	c.executable = func() (string, error) { return "", errors.New("no such file") }
	if err := c.Up(context.Background(), "alpha", "issue-42"); err == nil {
		t.Error("want an error when the running binary cannot be resolved")
	}
}

// An empty ProjectRoot and no registry answer must fail before Up touches
// the executable or runCommand seams: exec.Cmd treats an empty Dir as
// "inherit this process's cwd", which is wrong for a long-lived
// control-plane caller with no cwd of its own that means anything.
func TestUpRequiresAProjectRoot(t *testing.T) {
	var ranExecutable, ranCommand bool
	c := New(Options{Containers: &fakeContainers{}})
	c.executable = func() (string, error) { ranExecutable = true; return "/usr/local/bin/cspace", nil }
	c.runCommand = func(context.Context, string, string, ...string) (string, error) {
		ranCommand = true
		return "", nil
	}
	err := c.Up(context.Background(), "alpha", "issue-42")
	if !errors.Is(err, ErrNoProjectRoot) {
		t.Errorf("err = %v, want ErrNoProjectRoot", err)
	}
	if ranExecutable || ranCommand {
		t.Error("Up must not touch the executable/runCommand seams without a root")
	}
}
```

The file's imports become:

```go
import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elliottregan/cspace/internal/registry"
)
```

- [ ] **Step 6: Run them to watch them fail**

Run: `go test ./internal/control/ -run TestUp -v`
Expected: FAIL — `too many arguments in call to c.Up` on every case.

- [ ] **Step 7: Add `Options.Project` and the `Client` field**

In `internal/control/client.go`, inside `type Options struct`, immediately after the `ProjectRoot` field:

```go
	// Project is the project the launching process is in — the one
	// ProjectRoot belongs to. Up consults it before letting ProjectRoot
	// answer for a project the registry does not know. Empty means
	// "ProjectRoot answers for any project", which is the single-project
	// behaviour a caller that sets only ProjectRoot gets.
	Project string
```

In `type Client struct`, this block replaces the existing `projectRoot string` field declaration:

```go
	project     string
	projectRoot string
```

The result is exactly these two lines in that spot, not three — the existing `projectRoot` line is re-shown here so the alignment is right, not added a second time.

And in `New`, this block replaces the existing `projectRoot: o.ProjectRoot,` literal key:

```go
		project:           o.Project,
		projectRoot:       o.ProjectRoot,
```

The result is exactly these two lines in that spot, not three — the existing `projectRoot` line is re-shown here so the alignment is right, not added a second time.

- [ ] **Step 8: Rewrite `Up` and add `projectRootFor`**

Replace everything in `internal/control/up.go` above `runHostCommand` with:

```go
// ErrNoProjectRoot is returned when no project root can be resolved for the
// project a caller asked to boot. exec.Cmd treats an empty Dir as "inherit
// this process's cwd" — silently correct for a one-off CLI invocation,
// silently wrong for a long-lived control-plane process, which has no cwd of
// its own that means anything. New deliberately does not default ProjectRoot
// (no os.Getwd fallback): a caller that wants Up must say which project.
var ErrNoProjectRoot = errors.New("control: no project root")

// Up boots a sandbox for project by running this same cspace binary's `up`
// command from that project's root.
//
// `cspace up` is a cobra command wrapping a long boot flow and a Bubble Tea
// overlay, not a callable function, and internal/control must not import
// internal/cli — so control shells out to the binary it is already running
// inside, exactly as a person would. Everything the boot flow needs (config,
// devcontainer, compose) it reads from the directory it runs in.
func (c *Client) Up(ctx context.Context, project, sandbox string) error {
	root, err := c.projectRootFor(project)
	if err != nil {
		return err
	}
	exe, err := c.executable()
	if err != nil {
		return fmt.Errorf("resolve the running cspace binary: %w", err)
	}
	out, err := c.runCommand(ctx, root, exe, "up", sandbox)
	if err != nil {
		out = strings.TrimSpace(out)
		if out == "" {
			return fmt.Errorf("cspace up %s: %w", sandbox, err)
		}
		return fmt.Errorf("cspace up %s: %w (%s)", sandbox, err, out)
	}
	return nil
}

// projectRootFor resolves the directory `cspace up` must run in to boot a
// sandbox of project.
//
// The registry is the source of truth: every entry cspace up writes records
// the root it booted from (registry.Entry.ProjectRoot), so any existing
// sandbox of a project names that project's checkout — which is what lets
// one dashboard boot sandboxes for projects the process was never started
// in. Entries are scanned in sandbox-name order so the answer is
// deterministic when two checkouts of one project disagree.
//
// A project with no entry recording a root — nothing ever booted, or only
// entries written before the field existed — falls back to the launching
// process's own ProjectRoot, and only when that is the same project (or the
// Client never named one, the single-project case). The root is not checked
// for existence here: a stale one fails loudly in cspace up's own chdir,
// with a better message than this could invent.
func (c *Client) projectRootFor(project string) (string, error) {
	if c.entries != nil {
		if entries, err := c.entries.List(); err == nil {
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
			for _, e := range entries {
				if e.Project == project && e.ProjectRoot != "" {
					return e.ProjectRoot, nil
				}
			}
		}
	}
	if c.projectRoot != "" && (c.project == "" || c.project == project) {
		return c.projectRoot, nil
	}
	return "", fmt.Errorf("%w for project %s: boot one of its sandboxes with `cspace up` from its checkout first", ErrNoProjectRoot, project)
}
```

The file's imports become `bytes`, `context`, `errors`, `fmt`, `os/exec`, `sort`, `strings`.

- [ ] **Step 9: Run the control tests**

Run: `go test ./internal/control/ -run TestUp -v`
Expected: PASS — six tests, including `TestUpResolvesTheRootFromAnotherProjectsEntry` and `TestUpRefusesAnUnknownProject`.

- [ ] **Step 10: Write the root at `cspace up` time**

In `internal/cli/cmd_up.go`, add `ProjectRoot: projectRoot,` to **both** `r.Register(registry.Entry{…})` literals — the early "claim the slot" write (~line 691) and the re-register with the real ControlURL/IP (~line 774). `projectRoot` is already in scope: it is assigned from `cfg.ProjectRoot` at ~line 119. The early write becomes:

```go
			if regErr := r.Register(registry.Entry{
				Project:          project,
				Name:             name,
				ControlURL:       fmt.Sprintf("http://0.0.0.0:%d", supervisorPort),
				Token:            token,
				IP:               "",
				StartedAt:        startedAt,
				BrowserContainer: browserContainer,
				ProjectRoot:      projectRoot,
				State:            "starting",
			}); regErr != nil {
```

and the second one identically, keeping its own `ControlURL: ctlURL` and `IP: ip`.

- [ ] **Step 11: Verify the whole build and suite**

Run: `make vet && make test`
Expected: both clean. (`internal/tui` and `internal/cli` are untouched by the `Up` signature change — nothing there calls it.)

- [ ] **Step 12: Resolve the finding**

Append to `.cspace/context/findings/2026-09-18-registry-entries-do-not-record-a-project-root.md`, under `## Updates`, and change the frontmatter `status: open` to `status: resolved`:

```markdown
### 2026-09-18 — status: resolved
Fix candidate 1 shipped with rollout step 3: `registry.Entry` gained
`project_root`, written by both of `cmd_up.go`'s `Register` calls from the
`projectRoot` the boot flow already resolved. `control.Client.Up` is now
`Up(ctx, project, sandbox)` and resolves the root through
`projectRootFor`: the registry first (entries scanned in sandbox-name order
so two checkouts of one project give a deterministic answer), then the
launching process's own `Options.ProjectRoot` when the project matches
`Options.Project` or the Client never named one. An unknown project returns
`ErrNoProjectRoot` naming it rather than booting the wrong checkout.
Legacy entries carry no root and fall through to that same fallback.
```

- [ ] **Step 13: Commit**

```bash
cd /Users/elliott/Projects/cspace-control-plane-3
git add internal/registry internal/cli/cmd_up.go internal/control/up.go internal/control/client.go internal/control/up_test.go .cspace/context/findings
git commit -m "Record the project root in every registry entry and make Up project-aware (cs-finding:2026-09-18-registry-entries-do-not-record-a-project-root)"
```

---

### Task 2: A stats-free snapshot and the `Client`'s tmux driver

The dashboard polls on three cadences (spec, "Data flow and cadence"): the medium one asks for a `Snapshot` *without* stats, because `container stats` costs ~2 s against Apple Container 1.3 and a 2 s ticker cannot pay that. The slow one asks for the full thing. The attach action needs the `Client`'s own tmux driver rather than building a second one.

**Files:**
- Modify: `internal/control/snapshot.go:20-62`
- Modify: `internal/control/client.go` (append `Tmux`)
- Test: `internal/control/snapshot_test.go` (extend `fakeContainers`, add two tests)

**Interfaces:**
- Consumes: Task 1's `Options.Project` (no direct use here).
- Produces:
  - `type SnapshotOpts struct { SkipStats bool }`
  - `func (c *Client) SnapshotWith(ctx context.Context, opts SnapshotOpts) Snapshot`
  - `func (c *Client) Snapshot(ctx context.Context) Snapshot` — unchanged signature, now `SnapshotWith(ctx, SnapshotOpts{})`, so `Snapshotter` and `internal/tui` keep working until Task 8 deletes them
  - `func (c *Client) Tmux() *Tmux`

- [ ] **Step 1: Write the failing tests**

In `internal/control/snapshot_test.go`, add a call counter to the existing `fakeContainers` (a new field on the struct and one line in `Stats`):

```go
type fakeContainers struct {
	out      []applecontainer.ContainerSummary
	err      error
	stats    []applecontainer.ContainerStats
	statsErr error
	// statsCalls counts Stats calls. Snapshot samples stats on its own
	// goroutine but waits for it before returning, so a plain int read
	// after Snapshot returns is properly ordered.
	statsCalls int

	execOut    string
	execStderr string
	execExit   int
	execErr    error
	execCalls  []execCall
}

func (f *fakeContainers) Stats(context.Context) ([]applecontainer.ContainerStats, error) {
	f.statsCalls++
	return f.stats, f.statsErr
}
```

Then append:

```go
// The medium ticker polls twice a second-and-a-half; `container stats` costs
// ~2s. SkipStats is what keeps that cadence affordable, so it must actually
// skip the call, not just discard its result.
func TestSnapshotWithSkipStatsDoesNotSampleStats(t *testing.T) {
	cli := &fakeContainers{
		out: []applecontainer.ContainerSummary{
			{Name: "cspace-alpha-mercury", State: "running", IP: "192.168.64.5", MemoryB: 16 << 30},
		},
		stats: []applecontainer.ContainerStats{
			{Name: "cspace-alpha-mercury", MemoryUsedB: 1 << 30},
		},
	}
	reg := writeRegistry(t, "alpha", "mercury", "", "")
	c := New(Options{Containers: cli, Entries: reg, Now: func() time.Time { return time.Unix(1_000_000, 0) }})

	snap := c.SnapshotWith(context.Background(), SnapshotOpts{SkipStats: true})
	if cli.statsCalls != 0 {
		t.Errorf("Stats called %d times, want 0", cli.statsCalls)
	}
	var found bool
	for _, r := range snap.Rows {
		if r.Kind == RowSandbox && r.Name == "mercury" {
			found = true
			if r.MemoryUsedB != 0 {
				t.Errorf("MemoryUsedB = %d, want 0 with no sample", r.MemoryUsedB)
			}
			if r.MemoryB != 16<<30 {
				t.Errorf("MemoryB = %d, want the cap to survive", r.MemoryB)
			}
		}
	}
	if !found {
		t.Fatalf("no mercury row in %+v", snap.Rows)
	}
}

// The default is unchanged: Snapshot still samples, and the sample still
// lands on the row, because the slow ticker and every existing caller
// depend on it.
func TestSnapshotSamplesStatsByDefault(t *testing.T) {
	cli := &fakeContainers{
		out: []applecontainer.ContainerSummary{
			{Name: "cspace-alpha-mercury", State: "running", IP: "192.168.64.5", MemoryB: 16 << 30},
		},
		stats: []applecontainer.ContainerStats{
			{Name: "cspace-alpha-mercury", MemoryUsedB: 1 << 30},
		},
	}
	reg := writeRegistry(t, "alpha", "mercury", "", "")
	c := New(Options{Containers: cli, Entries: reg, Now: func() time.Time { return time.Unix(1_000_000, 0) }})

	snap := c.Snapshot(context.Background())
	if cli.statsCalls != 1 {
		t.Errorf("Stats called %d times, want 1", cli.statsCalls)
	}
	for _, r := range snap.Rows {
		if r.Kind == RowSandbox && r.Name == "mercury" && r.MemoryUsedB != 1<<30 {
			t.Errorf("MemoryUsedB = %d, want the sample", r.MemoryUsedB)
		}
	}
}

// The dashboard's attach must probe for tmux and hand BeginAttach the same
// driver the Client uses, so the memoized presence probe and the exec
// transport are shared rather than duplicated.
func TestTmuxReturnsTheClientsDriver(t *testing.T) {
	tm := NewTmux()
	c := New(Options{Containers: &fakeContainers{}, Tmux: tm})
	if c.Tmux() != tm {
		t.Error("Tmux() should hand back the injected driver")
	}
	if New(Options{Containers: &fakeContainers{}}).Tmux() == nil {
		t.Error("Tmux() should never be nil: New always builds one")
	}
}
```

- [ ] **Step 2: Run them to watch them fail**

Run: `go test ./internal/control/ -run 'TestSnapshotWithSkipStats|TestSnapshotSamplesStatsByDefault|TestTmuxReturnsTheClientsDriver' -v`
Expected: FAIL — `c.SnapshotWith undefined` and `c.Tmux undefined`.

- [ ] **Step 3: Split `Snapshot`**

In `internal/control/snapshot.go`, replace the `Snapshot` method (keeping `Snapshotter` and its `var _` assertion above it exactly as they are) with:

```go
// SnapshotOpts tunes one Snapshot.
type SnapshotOpts struct {
	// SkipStats omits the `container stats` sample. That sample costs ~2s
	// against Apple Container 1.3 — two orders of magnitude more than every
	// other source in a snapshot — so a caller polling on a short cadence
	// asks for it on a slower cadence of its own instead. Rows then carry
	// MemoryUsedB 0, exactly as a failed stats probe already leaves them,
	// and render their cap alone.
	SkipStats bool
}

// Snapshot reports every sandbox on the host grouped by project: lifecycle,
// memory cap and usage, uptime, nested compose sidecars, the project's
// browser sidecar and its health, and daemon health.
func (c *Client) Snapshot(ctx context.Context) Snapshot {
	return c.SnapshotWith(ctx, SnapshotOpts{})
}

// SnapshotWith is Snapshot with the options above.
func (c *Client) SnapshotWith(ctx context.Context, opts SnapshotOpts) Snapshot {
	if c.containers == nil {
		return Snapshot{Err: ErrNoContainerCLI, TakenAt: c.now()}
	}
	if c.entries == nil {
		return Snapshot{Err: ErrNoEntryStore, TakenAt: c.now()}
	}
	containers, listErr := c.containers.List(ctx)
	entries, _ := c.entries.List() // missing file => empty slice, nil

	// When stats are wanted they run concurrently with the HTTP probes
	// rather than adding their ~2s to them: run sequentially they would
	// push a snapshot toward the caller's context ceiling and start timing
	// the whole thing out. A nil map is a legal read target, so the
	// skip path needs no other branch.
	var (
		stats   map[string]applecontainer.ContainerStats
		statsWG sync.WaitGroup
	)
	if !opts.SkipStats {
		statsWG.Add(1)
		go func() {
			defer statsWG.Done()
			stats = c.fetchStats(ctx)
		}()
	}

	statuses := c.fetchStatuses(ctx, entries)
	browserHealth := c.fetchBrowserHealth(ctx, containers)
	daemon := c.fetchDaemon(ctx)
	statsWG.Wait()

	return Correlate(c.now(), containers, entries, statuses, browserHealth, stats, daemon, listErr)
}
```

- [ ] **Step 4: Expose the driver**

Append to `internal/control/client.go`:

```go
// Tmux is this Client's in-sandbox tmux driver: the one exec transport and
// the one memoized per-sandbox presence probe this package uses. Exposed so
// a caller that needs the driver itself — the dashboard's attach, which
// probes for tmux and then hands BeginAttach a driver — uses this Client's
// rather than standing up a second one over its own `container` CLI. New
// always builds one, so this is never nil.
func (c *Client) Tmux() *Tmux { return c.tmux }
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/control/ -run 'TestSnapshot|TestTmux' -v`
Expected: PASS, including the pre-existing `TestSnapshotFansOutStatusAndCorrelates`.

- [ ] **Step 6: Verify nothing else moved**

Run: `make vet && make test`
Expected: clean — `internal/tui` still calls `Snapshot(ctx)` through `control.Snapshotter` and is unaffected.

- [ ] **Step 7: Commit**

```bash
cd /Users/elliott/Projects/cspace-control-plane-3
git add internal/control
git commit -m "Add a stats-free snapshot option and expose the Client's tmux driver"
```

---

### Task 3: The user-level config layer and the keymap

`cspace tui` spans every project on the host, so its bindings cannot live in a project's `.cspace.json`. This adds the user-level layer (`~/.cspace/config.json`, merged over the embedded defaults with the same `DeepMerge`), the `tui.keys` object in `lib/defaults.json`, and the `bubbles/v2/key` keymap the dashboard matches against — including the per-row gate that disables a binding the selection cannot use, which both the key dispatch (Task 6) and the footer (Task 5) read.

**Files:**
- Modify: `lib/defaults.json`
- Modify: `internal/config/config.go` (the `Config` struct, plus one new type)
- Create: `internal/config/user.go`
- Test: `internal/config/user_test.go`
- Create: `internal/controlplane/controlplane.go`
- Create: `internal/controlplane/keys.go`
- Test: `internal/controlplane/keys_test.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `config.TUIConfig{ Keys map[string][]string }` and `config.Config.TUI TUIConfig`
  - `func config.UserConfigPath(home string) string`
  - `func config.LoadUser(home string) (*config.Config, error)`
  - `controlplane.sandboxKey{Project, Name string}`, `controlplane.liveState{Agent control.AgentStatus; Interactive control.InteractiveState}`, `func keyOf(row control.Row) sandboxKey`
  - `controlplane.KeyMap` with fields `MoveUp, MoveDown, Attach, Send, Interrupt, Teardown, BrowserRestart, Boot, Refresh, Help, Quit, Leader key.Binding`
  - `func controlplane.NewKeyMap(overrides map[string][]string) KeyMap`
  - `func (KeyMap) ShortHelp() []key.Binding`, `func (KeyMap) FullHelp() [][]key.Binding`
  - `func (KeyMap) forRow(row control.Row, live liveState) KeyMap`
  - action-name constants `ActionMoveUp … ActionLeader`
  - `forceQuit key.Binding` (ctrl+c, not configurable)

- [ ] **Step 1: Write the failing config test**

Create `internal/config/user_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

// LoadUser answers from the embedded defaults alone when the user has no
// config file — the overwhelmingly common case, and not an error.
func TestLoadUserWithNoFileReturnsTheDefaults(t *testing.T) {
	cfg, err := LoadUser(t.TempDir())
	if err != nil {
		t.Fatalf("LoadUser: %v", err)
	}
	if len(cfg.TUI.Keys) == 0 {
		t.Fatal("defaults.json should carry a tui.keys object")
	}
	if got := cfg.TUI.Keys["attach"]; len(got) == 0 {
		t.Errorf("tui.keys.attach = %v, want the default keystrokes", got)
	}
}

// A user file overrides one action and leaves every other alone: DeepMerge
// merges objects recursively and replaces arrays wholesale, so "attach" is
// replaced, not appended to, and "quit" survives untouched.
func TestLoadUserMergesOverTheDefaults(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".cspace"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(UserConfigPath(home),
		[]byte(`{"tui":{"keys":{"attach":["o"]}}}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := LoadUser(home)
	if err != nil {
		t.Fatalf("LoadUser: %v", err)
	}
	if got := cfg.TUI.Keys["attach"]; len(got) != 1 || got[0] != "o" {
		t.Errorf("tui.keys.attach = %v, want [o] (arrays replace wholesale)", got)
	}
	if len(cfg.TUI.Keys["quit"]) == 0 {
		t.Error("an override of one action must not drop the others")
	}
}

// A malformed user file is an error: silently ignoring it would leave a
// person staring at bindings they thought they had changed.
func TestLoadUserRejectsMalformedJSON(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".cspace"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(UserConfigPath(home), []byte("{nope"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := LoadUser(home); err == nil {
		t.Error("want an error for a malformed user config")
	}
}

// LoadUser needs no git repository and no project root: the dashboard it
// serves spans every project and may be started from anywhere.
func TestLoadUserNeedsNoProjectRoot(t *testing.T) {
	cfg, err := LoadUser(t.TempDir())
	if err != nil {
		t.Fatalf("LoadUser: %v", err)
	}
	if cfg.ProjectRoot != "" {
		t.Errorf("ProjectRoot = %q, want empty", cfg.ProjectRoot)
	}
}
```

- [ ] **Step 2: Run it to watch it fail**

Run: `make sync-embedded && go test ./internal/config/ -run TestLoadUser -v`
Expected: FAIL — `undefined: LoadUser`, `undefined: UserConfigPath`.

- [ ] **Step 3: Add the config type**

In `internal/config/config.go`, add the field to `type Config struct`, after `Credentials`:

```go
	TUI         TUIConfig              `json:"tui,omitempty"`
```

and the type, next to `CredentialsConfig`:

```go
// TUIConfig configures `cspace tui`. Keys maps an action name — the names in
// internal/controlplane's Action* constants — to the keystrokes that trigger
// it. The strings are matched against bubbletea v2's KeyPressMsg.String(),
// so they are things like "enter", "up", "?", "ctrl+space". An action absent
// from the map keeps its built-in default; an unknown action name is
// ignored. Because DeepMerge replaces arrays wholesale, setting one action's
// list replaces that action's keystrokes and leaves every other action's
// alone.
type TUIConfig struct {
	Keys map[string][]string `json:"keys,omitempty"`
}
```

- [ ] **Step 4: Add the user-level loader**

Create `internal/config/user.go`:

```go
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/elliottregan/cspace/internal/assets"
)

// UserConfigPath is the user-level cspace config file: ~/.cspace/config.json.
// It sits beside the registry, the session tree and the daemon log rather
// than in a project, because what it configures — `cspace tui` — spans every
// project on the host.
func UserConfigPath(home string) string {
	return filepath.Join(home, ".cspace", "config.json")
}

// LoadUser merges the embedded defaults with the user-level config file.
//
// Unlike Load it needs no project root and no git repository: the dashboard
// it serves shows every project on the host and may be started from
// anywhere, including a directory that is no project at all. A missing file
// is not an error — the defaults are then the whole answer. A malformed one
// is, because silently ignoring it would leave a person staring at bindings
// they believe they changed.
func LoadUser(home string) (*Config, error) {
	defaultsBytes, err := assets.DefaultsJSON()
	if err != nil {
		return nil, fmt.Errorf("reading embedded defaults.json: %w", err)
	}
	var base map[string]interface{}
	if err := json.Unmarshal(defaultsBytes, &base); err != nil {
		return nil, fmt.Errorf("parsing defaults.json: %w", err)
	}

	path := UserConfigPath(home)
	if data, err := os.ReadFile(path); err == nil {
		var overlay map[string]interface{}
		if err := json.Unmarshal(data, &overlay); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		base = DeepMerge(base, overlay)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	merged, err := json.Marshal(base)
	if err != nil {
		return nil, fmt.Errorf("marshaling merged config: %w", err)
	}
	cfg := &Config{}
	if err := json.Unmarshal(merged, cfg); err != nil {
		return nil, fmt.Errorf("unmarshaling config into struct: %w", err)
	}
	// No autoDetect: that fills project name/repo/prefix from a project
	// root this loader deliberately does not have.
	return cfg, nil
}
```

- [ ] **Step 5: Declare the defaults**

In `lib/defaults.json`, add a `tui` block after `"credentials"` (keep the file valid JSON — the `credentials` object needs a trailing comma):

```json
  "credentials": {
    "runwayWarningHours": 4
  },
  "tui": {
    "keys": {
      "moveUp": ["up", "k"],
      "moveDown": ["down", "j"],
      "attach": ["enter"],
      "send": ["m"],
      "interrupt": ["i"],
      "teardown": ["d"],
      "browserRestart": ["b"],
      "boot": ["u"],
      "refresh": ["r"],
      "help": ["?"],
      "quit": ["q"],
      "leader": ["ctrl+space"]
    }
  }
```

- [ ] **Step 6: Run the config tests**

Run: `make sync-embedded && go test ./internal/config/ -v`
Expected: PASS — the four new tests plus every existing one.

- [ ] **Step 7: Add the v2 widget dependency**

Run:
```bash
cd /Users/elliott/Projects/cspace-control-plane-3
go get charm.land/bubbles/v2@v2.2.1
```
Expected: `go.mod` gains `charm.land/bubbles/v2 v2.2.1`, plus — from bubbles' own go.mod — `charm.land/bubbletea/v2 v2.0.8` and `charm.land/lipgloss/v2 v2.0.5` as indirects, and `github.com/charmbracelet/x/ansi` raised from v0.11.6 to v0.11.7. Those are the versions bubbles asks for; Task 4 raises lipgloss to v2.0.6 and Task 5 raises bubbletea to v2.0.9, so do not expect the final numbers yet. The v1 `github.com/charmbracelet/bubbletea v1.3.10` and `github.com/charmbracelet/lipgloss v1.1.0` lines must still be there — `internal/overlay` and `internal/tui` are on them, and both majors coexist. Do not run `go mod tidy` yet: nothing imports the new module until Step 9, and tidy would drop it.

- [ ] **Step 8: Write the failing keymap test**

Create `internal/controlplane/keys_test.go`:

```go
package controlplane

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"charm.land/bubbles/v2/key"

	"github.com/elliottregan/cspace/internal/assets"
	"github.com/elliottregan/cspace/internal/control"
)

func bindingFor(k KeyMap, action string) key.Binding {
	switch action {
	case ActionMoveUp:
		return k.MoveUp
	case ActionMoveDown:
		return k.MoveDown
	case ActionAttach:
		return k.Attach
	case ActionSend:
		return k.Send
	case ActionInterrupt:
		return k.Interrupt
	case ActionTeardown:
		return k.Teardown
	case ActionBrowserRestart:
		return k.BrowserRestart
	case ActionBoot:
		return k.Boot
	case ActionRefresh:
		return k.Refresh
	case ActionHelp:
		return k.Help
	case ActionQuit:
		return k.Quit
	case ActionLeader:
		return k.Leader
	}
	return key.Binding{}
}

func TestNewKeyMapUsesTheBuiltInDefaults(t *testing.T) {
	k := NewKeyMap(nil)
	for action, want := range defaultKeys {
		if got := bindingFor(k, action).Keys(); !reflect.DeepEqual(got, want) {
			t.Errorf("%s keys = %v, want %v", action, got, want)
		}
		if bindingFor(k, action).Help().Desc == "" && action != ActionLeader {
			t.Errorf("%s has no help description", action)
		}
	}
}

func TestNewKeyMapAppliesOverrides(t *testing.T) {
	k := NewKeyMap(map[string][]string{
		ActionAttach: {"o"},
		"nonsense":   {"z"}, // unknown names are ignored, not fatal
	})
	if got := k.Attach.Keys(); !reflect.DeepEqual(got, []string{"o"}) {
		t.Errorf("attach keys = %v, want [o]", got)
	}
	if got := k.Quit.Keys(); !reflect.DeepEqual(got, defaultKeys[ActionQuit]) {
		t.Errorf("quit keys = %v, want the default %v", got, defaultKeys[ActionQuit])
	}
	// An empty list means "say nothing", not "unbind": a config that
	// round-trips through a tool emitting [] must not silently disarm a key.
	if got := NewKeyMap(map[string][]string{ActionAttach: {}}).Attach.Keys(); !reflect.DeepEqual(got, defaultKeys[ActionAttach]) {
		t.Errorf("empty override = %v, want the default", got)
	}
}

// defaults.json documents the shipped bindings and Go carries the fallback.
// They must not drift: a user who edits one action in ~/.cspace/config.json
// keeps defaults.json's values for the rest, and a binary whose embedded
// defaults disagreed with its own fallback would behave differently
// depending on which path a key came from.
func TestDefaultsJSONMatchesTheBuiltInKeys(t *testing.T) {
	raw, err := assets.DefaultsJSON()
	if err != nil {
		t.Fatalf("DefaultsJSON: %v", err)
	}
	var doc struct {
		TUI struct {
			Keys map[string][]string `json:"keys"`
		} `json:"tui"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse defaults.json: %v", err)
	}
	if !reflect.DeepEqual(doc.TUI.Keys, defaultKeys) {
		t.Errorf("defaults.json tui.keys = %v, want %v", doc.TUI.Keys, defaultKeys)
	}
	names := make([]string, 0, len(defaultKeys))
	for n := range defaultKeys {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if len(bindingFor(NewKeyMap(nil), n).Keys()) == 0 {
			t.Errorf("action %q resolves to no keys", n)
		}
	}
}

func TestForRowDisablesWhatTheSelectionCannotDo(t *testing.T) {
	working := control.Row{Kind: control.RowSandbox, State: control.StateRunning,
		Agent: control.AgentStatus{Reachable: true, State: "working"}}
	idle := control.Row{Kind: control.RowSandbox, State: control.StateRunning,
		Agent: control.AgentStatus{Reachable: true, State: "idle"}}
	stopped := control.Row{Kind: control.RowSandbox, State: control.StateStopped}
	degraded := control.Row{Kind: control.RowSandbox, State: control.StateDegraded}
	browser := control.Row{Kind: control.RowBrowser, State: control.StateRunning}

	cases := []struct {
		name   string
		row    control.Row
		live   liveState
		action string
		want   bool
	}{
		{"attach a running sandbox", working, liveState{}, ActionAttach, true},
		{"attach a stopped sandbox", stopped, liveState{}, ActionAttach, false},
		{"attach the browser row", browser, liveState{}, ActionAttach, false},
		{"boot a stopped sandbox", stopped, liveState{}, ActionBoot, true},
		{"boot a running sandbox", working, liveState{}, ActionBoot, false},
		{"down a running sandbox", working, liveState{}, ActionTeardown, true},
		{"down a stopped sandbox", stopped, liveState{}, ActionTeardown, false},
		{"send to a reachable agent", idle, liveState{}, ActionSend, true},
		{"send to a degraded sandbox", degraded, liveState{}, ActionSend, false},
		{"interrupt a working agent", working, liveState{}, ActionInterrupt, true},
		{"interrupt an idle agent", idle, liveState{}, ActionInterrupt, false},
		{"browser restart on a sandbox", working, liveState{}, ActionBrowserRestart, true},
		{"browser restart on the browser row", browser, liveState{}, ActionBrowserRestart, true},
		// The fast ticker's fresher status wins over the snapshot's: a
		// sandbox the snapshot saw idle but that is working now can be
		// interrupted without waiting for the next snapshot.
		{"interrupt on the live sample", idle,
			liveState{Agent: control.AgentStatus{Reachable: true, State: "working"}}, ActionInterrupt, true},
		// Keys that never depend on the selection stay enabled.
		{"move stays enabled", stopped, liveState{}, ActionMoveDown, true},
		{"help stays enabled", stopped, liveState{}, ActionHelp, true},
		{"quit stays enabled", stopped, liveState{}, ActionQuit, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := bindingFor(NewKeyMap(nil).forRow(tc.row, tc.live), tc.action).Enabled()
			if got != tc.want {
				t.Errorf("%s enabled = %v, want %v", tc.action, got, tc.want)
			}
		})
	}
}

// forceQuit is the one binding no config can touch: NewKeyMap never sees it
// and Update matches it before any mode does, so a dashboard cannot be
// configured into having no way out.
func TestForceQuitIsCtrlCAndNotConfigurable(t *testing.T) {
	if got := forceQuit.Keys(); !reflect.DeepEqual(got, []string{"ctrl+c"}) {
		t.Errorf("forceQuit keys = %v, want [ctrl+c]", got)
	}
	if _, ok := defaultKeys["forceQuit"]; ok {
		t.Error("forceQuit must not be reachable from tui.keys")
	}
}

// keyOf is how per-sandbox state (agent status, interactive state, ports)
// survives a poll that rebuilds every row from scratch.
func TestKeyOfIdentifiesTheSandbox(t *testing.T) {
	row := control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "mercury"}
	if got, want := keyOf(row), (sandboxKey{Project: "alpha", Name: "mercury"}); got != want {
		t.Errorf("keyOf(%+v) = %+v, want %+v", row, got, want)
	}
	// Two projects' sandboxes that share a name are different keys — which
	// is the whole reason the project rides along in the key.
	other := control.Row{Kind: control.RowSandbox, Project: "beta", Name: "mercury"}
	if keyOf(row) == keyOf(other) {
		t.Error("sandboxes of different projects must not share a key")
	}
}

// The leader is declared so the config shape is stable for rollout step 4,
// but nothing in step 3 dispatches it and it must not appear in help.
func TestLeaderIsDeclaredButNotAdvertised(t *testing.T) {
	k := NewKeyMap(nil)
	if len(k.Leader.Keys()) == 0 {
		t.Error("the leader binding should be declared")
	}
	for _, b := range k.ShortHelp() {
		if reflect.DeepEqual(b.Keys(), k.Leader.Keys()) {
			t.Error("the leader must not appear in short help until panes land")
		}
	}
	for _, col := range k.FullHelp() {
		for _, b := range col {
			if reflect.DeepEqual(b.Keys(), k.Leader.Keys()) {
				t.Error("the leader must not appear in full help until panes land")
			}
		}
	}
}
```

- [ ] **Step 9: Run it to watch it fail**

Run: `make sync-embedded && go test ./internal/controlplane/ -v`
Expected: FAIL to build — `no required module provides package .../internal/controlplane` (the package does not exist yet).

- [ ] **Step 10: Create the package's shared types**

Create `internal/controlplane/controlplane.go`:

```go
// Package controlplane is `cspace tui`: a Bubble Tea v2 dashboard over every
// cspace container on the host.
//
// It is the UI layer and nothing else. Every query it makes goes through the
// Data interface (poll.go) and every action through the Actor interface
// (actor.go); both are declared here, implemented in internal/cli, and
// injected — so this package never imports internal/cli, and internal/control
// stays the one implementation of "what is running" and "do this to it".
//
// Rollout step 3 has no panes: the main area holds the detail band for the
// selected row, and attach suspends the whole program into `container exec`
// the way the v1 dashboard did. The tabs line, the leader binding and the
// detail renderer's width parameter are the seams rollout step 4 grows into.
package controlplane

import "github.com/elliottregan/cspace/internal/control"

// sandboxKey identifies one sandbox across polls. Rows are rebuilt from
// scratch on every snapshot, so per-sandbox state the model carries between
// them (agent status, interactive state, ports) is keyed by identity rather
// than by row index.
type sandboxKey struct {
	Project string
	Name    string
}

// liveState is the fast ticker's per-sandbox sample: the supervisor's status
// and the interactive session's hook-written state. The zero value means
// "not sampled yet", which readers fall back from rather than render.
type liveState struct {
	Agent       control.AgentStatus
	Interactive control.InteractiveState
}

// keyOf identifies the sandbox a row belongs to. Sidecar and browser rows
// carry their project, so they key cleanly too; project headers key to the
// zero-name entry nothing ever samples.
func keyOf(row control.Row) sandboxKey {
	return sandboxKey{Project: row.Project, Name: row.Name}
}
```

- [ ] **Step 11: Write the keymap**

Create `internal/controlplane/keys.go`:

```go
package controlplane

import (
	"charm.land/bubbles/v2/key"

	"github.com/elliottregan/cspace/internal/control"
)

// The dashboard's action names. These strings are the keys of the `tui.keys`
// object in lib/defaults.json and in a person's ~/.cspace/config.json, so
// renaming one is a breaking config change.
const (
	ActionMoveUp         = "moveUp"
	ActionMoveDown       = "moveDown"
	ActionAttach         = "attach"
	ActionSend           = "send"
	ActionInterrupt      = "interrupt"
	ActionTeardown       = "teardown"
	ActionBrowserRestart = "browserRestart"
	ActionBoot           = "boot"
	ActionRefresh        = "refresh"
	ActionHelp           = "help"
	ActionQuit           = "quit"
	ActionLeader         = "leader"
)

// defaultKeys is the built-in keystroke list per action, and must stay
// identical to lib/defaults.json's tui.keys (keys_test.go locks the two
// together). Go carries it as well as the JSON so a binary whose embedded
// assets are missing an action still has a working dashboard.
//
// `s` and `a` are the spec's sidebar keys for the shell pane and the
// supervisor view, which arrive with panes in rollout step 4. Neither is
// bound here — a default this ships and step 4 has to take back is a
// user-visible breaking change, and these strings are published in
// lib/defaults.json. So attach is Enter alone, and send is on `m` (for
// message) rather than on the `s` that is spoken for.
var defaultKeys = map[string][]string{
	ActionMoveUp:         {"up", "k"},
	ActionMoveDown:       {"down", "j"},
	ActionAttach:         {"enter"},
	ActionSend:           {"m"},
	ActionInterrupt:      {"i"},
	ActionTeardown:       {"d"},
	ActionBrowserRestart: {"b"},
	ActionBoot:           {"u"},
	ActionRefresh:        {"r"},
	ActionHelp:           {"?"},
	ActionQuit:           {"q"},
	ActionLeader:         {"ctrl+space"},
}

// actionHelp is the label and description each binding shows in the footer
// and the help overlay. The label is written for a human ("↑/k"), not
// derived from the keystrokes, so an overridden binding still reads well.
var actionHelp = map[string][2]string{
	ActionMoveUp:         {"↑/k", "up"},
	ActionMoveDown:       {"↓/j", "down"},
	ActionAttach:         {"enter", "attach"},
	ActionSend:           {"m", "send a turn"},
	ActionInterrupt:      {"i", "interrupt"},
	ActionTeardown:       {"d", "tear down"},
	ActionBrowserRestart: {"b", "restart browser"},
	ActionBoot:           {"u", "boot"},
	ActionRefresh:        {"r", "refresh"},
	ActionHelp:           {"?", "help"},
	ActionQuit:           {"q", "quit"},
}

// forceQuit is Ctrl+C: always bound, never configurable, and handled before
// anything else sees a key. A dashboard that could be configured into having
// no way out is a bug, and Ctrl+C has to work while a modal or the send box
// holds every other key.
var forceQuit = key.NewBinding(key.WithKeys("ctrl+c"))

// KeyMap is the dashboard's bindings. A KeyMap is a value: forRow returns a
// copy with the bindings the selection cannot use disabled, and both
// key.Matches and bubbles/help skip disabled bindings — so one filtered copy
// drives the gating and the footer at once.
type KeyMap struct {
	MoveUp         key.Binding
	MoveDown       key.Binding
	Attach         key.Binding
	Send           key.Binding
	Interrupt      key.Binding
	Teardown       key.Binding
	BrowserRestart key.Binding
	Boot           key.Binding
	Refresh        key.Binding
	Help           key.Binding
	Quit           key.Binding

	// Leader is declared for rollout step 4's pane bindings so the config
	// shape is stable now. Nothing in step 3 dispatches it, and it is kept
	// out of ShortHelp/FullHelp so the footer does not advertise a key that
	// does nothing yet.
	Leader key.Binding
}

// NewKeyMap builds the bindings, applying the user-level config's tui.keys
// over the built-in defaults. An action missing from overrides — or present
// with an empty list, which is what a config round-tripped through some
// other tool can produce — keeps its default. An unknown action name is
// ignored rather than rejected: a config written for a newer cspace must not
// stop an older one from starting.
func NewKeyMap(overrides map[string][]string) KeyMap {
	binding := func(action string) key.Binding {
		keys := defaultKeys[action]
		if over, ok := overrides[action]; ok && len(over) > 0 {
			keys = over
		}
		opts := []key.BindingOpt{key.WithKeys(keys...)}
		if h, ok := actionHelp[action]; ok {
			opts = append(opts, key.WithHelp(h[0], h[1]))
		}
		return key.NewBinding(opts...)
	}
	return KeyMap{
		MoveUp:         binding(ActionMoveUp),
		MoveDown:       binding(ActionMoveDown),
		Attach:         binding(ActionAttach),
		Send:           binding(ActionSend),
		Interrupt:      binding(ActionInterrupt),
		Teardown:       binding(ActionTeardown),
		BrowserRestart: binding(ActionBrowserRestart),
		Boot:           binding(ActionBoot),
		Refresh:        binding(ActionRefresh),
		Help:           binding(ActionHelp),
		Quit:           binding(ActionQuit),
		Leader:         binding(ActionLeader),
	}
}

// ShortHelp is the footer's one line, in the order a person reads it.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.MoveUp, k.MoveDown, k.Attach, k.Send, k.Interrupt,
		k.Teardown, k.BrowserRestart, k.Boot, k.Help, k.Quit}
}

// FullHelp is the help overlay, grouped into columns: moving, acting on the
// selection, and the dashboard itself.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.MoveUp, k.MoveDown},
		{k.Attach, k.Send, k.Interrupt},
		{k.Teardown, k.Boot, k.BrowserRestart},
		{k.Refresh, k.Help, k.Quit},
	}
}

// forRow returns a copy with every binding the selection cannot act on
// disabled. Disabling rather than branching at dispatch time is what keeps
// the footer and the gate from ever disagreeing: key.Matches ignores a
// disabled binding, and help.ShortHelpView skips it.
func (k KeyMap) forRow(row control.Row, live liveState) KeyMap {
	k.Attach.SetEnabled(canAttach(row))
	k.Teardown.SetEnabled(canDown(row))
	k.Boot.SetEnabled(canBoot(row))
	k.Send.SetEnabled(canSend(row, live))
	k.Interrupt.SetEnabled(canInterrupt(row, live))
	k.BrowserRestart.SetEnabled(canBrowser(row))
	return k
}

// The contextual predicates. Pure, and tested directly: a running container
// is any State other than StateStopped.

func canAttach(r control.Row) bool {
	return r.Kind == control.RowSandbox && r.State != control.StateStopped
}

func canDown(r control.Row) bool {
	return r.Kind == control.RowSandbox && r.State != control.StateStopped
}

// canBoot is the mirror of canDown: `u` offers to start what is registered
// but not running. A sandbox that is already up has nothing to boot.
func canBoot(r control.Row) bool {
	return r.Kind == control.RowSandbox && r.State == control.StateStopped
}

func canSend(r control.Row, l liveState) bool {
	return r.Kind == control.RowSandbox && agentOf(r, l).Reachable
}

func canInterrupt(r control.Row, l liveState) bool {
	a := agentOf(r, l)
	return r.Kind == control.RowSandbox && a.Reachable && a.State == "working"
}

func canBrowser(r control.Row) bool {
	return r.Kind == control.RowBrowser || r.Kind == control.RowSandbox
}

// agentOf prefers the fast ticker's fresh status and falls back to the one
// the snapshot carried, so the first second after start — before any fast
// tick has landed — does not read as "supervisor unreachable" and disable
// send and interrupt on every row.
func agentOf(r control.Row, l liveState) control.AgentStatus {
	if l.Agent.Reachable {
		return l.Agent
	}
	return r.Agent
}
```

- [ ] **Step 12: Run the keymap tests**

Run: `make sync-embedded && go test ./internal/controlplane/ -v`
Expected: PASS — `TestNewKeyMapUsesTheBuiltInDefaults`, `TestNewKeyMapAppliesOverrides`, `TestDefaultsJSONMatchesTheBuiltInKeys`, `TestForRowDisablesWhatTheSelectionCannotDo` (17 subtests), `TestForceQuitIsCtrlCAndNotConfigurable`, `TestKeyOfIdentifiesTheSandbox`, `TestLeaderIsDeclaredButNotAdvertised`.

- [ ] **Step 13: Tidy and check**

Run:
```bash
cd /Users/elliott/Projects/cspace-control-plane-3
go mod tidy && make check
```
Expected: `go.mod` keeps `charm.land/bubbles/v2 v2.2.1` in the direct require block (keys.go imports it now), `bubbletea/v2 v2.0.8` and `lipgloss/v2 v2.0.5` as indirects, and both v1 charm modules in theirs; `make check` green.

`make check` runs `golangci-lint`, whose `unused` linter (on by default — `.golangci.yml` sets `linters.default: standard`) reports any unexported package-level symbol nothing references. `forceQuit`, `keyOf` and `sandboxKey` are first used by Tasks 4-6, so `TestForceQuitIsCtrlCAndNotConfigurable` and `TestKeyOfIdentifiesTheSandbox` are what keep this task's lint clean — a reference from a `_test.go` file counts as a use. Do not delete them as redundant when the later tasks land.

- [ ] **Step 14: Commit**

```bash
cd /Users/elliott/Projects/cspace-control-plane-3
git add go.mod go.sum lib/defaults.json internal/config internal/controlplane
git commit -m "Add the user-level config layer and the dashboard keymap"
```

---

### Task 4: The sidebar and the detail band

The two renderers, as pure functions over rows and samples — no model, no Bubble Tea. The sidebar is the design's fixed 24-column list: project headers, sandboxes with one state glyph, their labeled ports as OSC 8 hyperlinks, sidecars nested and dimmed, the browser row. The detail band is everything the 24 columns have no room for. Rendering the sidebar into a window of lines that follows the selection is also what closes the v1 dashboard's overflow finding.

**Files:**
- Create: `internal/controlplane/styles.go`
- Create: `internal/controlplane/view_sidebar.go`
- Create: `internal/controlplane/view_detail.go`
- Test: `internal/controlplane/view_sidebar_test.go`
- Test: `internal/controlplane/view_detail_test.go`
- Modify: `go.mod`, `go.sum`
- Modify: `.cspace/context/findings/2026-07-20-tui-row-list-has-no-viewport-scrolling.md`

**Interfaces:**
- Consumes: `sandboxKey`, `liveState`, `keyOf` (Task 3).
- Produces:
  - `const sidebarWidth = 24`, `sidebarInner`, `sidebarContent`
  - `func stateGlyph(row control.Row, live liveState) string`
  - `func renderSidebar(rows []control.Row, live map[sandboxKey]liveState, ports map[sandboxKey][]control.Port, selected, height int) string`
  - `func sidebarLines(...) []sidebarLine` and `func sidebarWindow(lines []sidebarLine, selected, height int) (from, to int)`
  - `func renderDetail(row control.Row, live liveState, ports []control.Port, portsErr error, events []control.EventLine, eventsErr error, memoryUsedB int64, width int) string`
  - formatters `formatMemory`, `formatMemUsage`, `formatUptime`, `formatAge`, `stateLabel`, `lastEventLabel`, `shortTs`, `fit`
  - styles `styleProject`, `styleDim`, `styleSelected`, `styleErr`, `styleOK`, `stylePort`, `styleSidebar` (the layout's `styleTabs` and `styleMain` land with `view.go` in Task 5, so each style arrives with its first consumer and `unused` stays quiet)
  - glyphs `glyphStopped`, `glyphBooting`, `glyphDegraded`, `glyphWorking`, `glyphIdle`, `glyphNeedsInput`, `glyphHealthy`, plus `sidebarLine`, `tailEvents`, `sessionOr`, `detailEvents`

- [ ] **Step 1: Add the rendering dependencies**

Run:
```bash
cd /Users/elliott/Projects/cspace-control-plane-3
go get charm.land/lipgloss/v2@v2.0.6
go get github.com/charmbracelet/x/ansi@v0.11.7
```
Expected: `lipgloss/v2` is raised from the v2.0.5 bubbles asked for to v2.0.6, and `x/ansi` stays at v0.11.7. Both are still marked `// indirect` at this point: `go get` records a requirement but does not reclassify it, and nothing in this package imports either one until Step 5. They move into the direct block at the next `go mod tidy` (Task 8's Step 7); until then the `// indirect` comment is stale but harmless — it is advisory, and the build reads only the version. Do not hand-edit it, and do not run `go mod tidy` here to tidy it away: nothing imports `bubbles/v2` outside `keys.go` yet and tidy would churn the file for nothing.

- [ ] **Step 2: Write the failing sidebar test**

Create `internal/controlplane/view_sidebar_test.go`:

```go
package controlplane

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/elliottregan/cspace/internal/control"
)

// plain is what a person sees: lipgloss v2 always emits ANSI (it no longer
// sniffs for a TTY at render time), and an OSC 8 hyperlink leaves a BEL
// behind the stripper treats as printable. Goldens compare this.
func plain(s string) string {
	return strings.ReplaceAll(ansi.Strip(s), "\x07", "")
}

func TestStateGlyphPrecedence(t *testing.T) {
	sandbox := func(state control.RowState, agent control.AgentStatus) control.Row {
		return control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "mercury",
			State: state, Agent: agent}
	}
	reachable := func(state string) control.AgentStatus {
		return control.AgentStatus{Reachable: true, State: state}
	}
	interactive := func(state string) liveState {
		return liveState{Interactive: control.InteractiveState{State: state}}
	}

	cases := []struct {
		name string
		row  control.Row
		live liveState
		want string
	}{
		{"stopped beats everything", sandbox(control.StateStopped, reachable("working")),
			interactive("working"), glyphStopped},
		{"booting beats agent state", sandbox(control.StateBooting, reachable("working")),
			interactive("working"), glyphBooting},
		{"degraded is its own glyph", sandbox(control.StateDegraded, control.AgentStatus{}),
			liveState{}, glyphDegraded},
		{"interactive working wins over supervisor idle",
			sandbox(control.StateRunning, reachable("idle")), interactive("working"), glyphWorking},
		{"interactive needs-input", sandbox(control.StateRunning, reachable("idle")),
			interactive("needs-input"), glyphNeedsInput},
		{"interactive idle", sandbox(control.StateRunning, reachable("working")),
			interactive("idle"), glyphIdle},
		{"interactive starting", sandbox(control.StateRunning, reachable("idle")),
			interactive("starting"), glyphBooting},
		// An ended interactive session says nothing about the headless
		// supervisor, which may well still be working — fall through.
		{"interactive exited falls through to the supervisor",
			sandbox(control.StateRunning, reachable("working")), interactive("exited"), glyphWorking},
		{"no interactive state uses the supervisor",
			sandbox(control.StateRunning, reachable("working")), liveState{}, glyphWorking},
		{"unknown everything is idle", sandbox(control.StateRunning, control.AgentStatus{}),
			liveState{}, glyphIdle},
		{"a healthy browser row", control.Row{Kind: control.RowBrowser, State: control.StateRunning,
			Browser: control.BrowserHealth{Reachable: true}}, liveState{}, glyphHealthy},
		{"a running browser with no CDP", control.Row{Kind: control.RowBrowser,
			State: control.StateRunning}, liveState{}, glyphDegraded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stateGlyph(tc.row, tc.live); got != tc.want {
				t.Errorf("glyph = %q, want %q", got, tc.want)
			}
		})
	}
}

func demoRows() []control.Row {
	return []control.Row{
		{Kind: control.RowProject, Project: "alpha", Name: "alpha"},
		{Kind: control.RowSandbox, Project: "alpha", Name: "mercury", Container: "cspace-alpha-mercury",
			State: control.StateRunning, Selectable: true,
			Agent: control.AgentStatus{Reachable: true, State: "idle"}},
		{Kind: control.RowSidecar, Project: "alpha", Name: "mercury-convex",
			Container: "cspace-alpha-mercury-convex", State: control.StateRunning},
		{Kind: control.RowSandbox, Project: "alpha", Name: "issue-42",
			Container: "cspace-alpha-issue-42", State: control.StateStopped, Selectable: true},
		{Kind: control.RowBrowser, Project: "alpha", Name: "browser (shared)",
			Container: "cspace-alpha-browser", State: control.StateRunning, Selectable: true,
			Browser: control.BrowserHealth{Reachable: true}},
		{Kind: control.RowSystem, Name: "buildkit", Container: "buildkit", State: control.StateRunning},
	}
}

func TestRenderSidebarGroupsAndMarksTheSelection(t *testing.T) {
	ports := map[sandboxKey][]control.Port{
		{Project: "alpha", Name: "mercury"}: {
			{Port: 5173, Label: "web", URL: "http://mercury.alpha.cspace.test:5173/"},
		},
	}
	out := plain(renderSidebar(demoRows(), nil, ports, 1, 20))
	lines := strings.Split(out, "\n")

	want := []string{
		"▾ alpha",
		// demoRows' mercury has a reachable but idle supervisor and no
		// interactive sample, so its glyph is ○; ▸ is the selection.
		"▸○ mercury",
		"   5173 web",
		"   ├ convex",
		"✕ issue-42",
		"✓ browser",
		"— system —",
		"buildkit",
	}
	for _, w := range want {
		found := false
		for _, l := range lines {
			if strings.Contains(l, w) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("sidebar missing %q; got:\n%s", w, out)
		}
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w > sidebarInner {
			t.Errorf("line %d is %d cells wide, want <= %d: %q", i, w, sidebarInner, l)
		}
	}
}

// A hyphenated sandbox name must not eat its sidecar's service name: the
// prefix Correlate leaves on a sidecar is the parent sandbox's whole name.
func TestSidebarTrimsTheSidecarPrefixByItsParent(t *testing.T) {
	rows := []control.Row{
		{Kind: control.RowProject, Project: "alpha", Name: "alpha"},
		{Kind: control.RowSandbox, Project: "alpha", Name: "issue-42",
			State: control.StateRunning, Selectable: true},
		{Kind: control.RowSidecar, Project: "alpha", Name: "issue-42-convex",
			State: control.StateRunning},
	}
	out := plain(renderSidebar(rows, nil, nil, 1, 10))
	if !strings.Contains(out, "├ convex") {
		t.Errorf("a sidecar should show its service name alone; got:\n%s", out)
	}
}

// The port row carries the OSC 8 hyperlink, so a terminal that supports them
// makes the label itself clickable. This is the one place the raw output is
// asserted rather than the stripped one.
func TestRenderSidebarHyperlinksPorts(t *testing.T) {
	ports := map[sandboxKey][]control.Port{
		{Project: "alpha", Name: "mercury"}: {
			{Port: 5173, Label: "web", URL: "http://mercury.alpha.cspace.test:5173/"},
		},
	}
	raw := renderSidebar(demoRows(), nil, ports, 1, 20)
	if !strings.Contains(raw, "\x1b]8;;http://mercury.alpha.cspace.test:5173/") {
		t.Errorf("port row should be an OSC 8 hyperlink; got:\n%q", raw)
	}
}

// lipgloss v2's Width is the *whole block's* width, border included
// (style.go subtracts the border before wrapping) — so styleSidebar is given
// sidebarWidth, not sidebarInner, and renderSidebar's sidebarInner-wide
// lines land beside the rule unwrapped. Width(sidebarInner) would leave 22
// columns of content, wrap every row onto two lines, and double the height
// of a layout whose line count is fixed.
func TestSidebarStyleIsExactlyTheDesignsWidth(t *testing.T) {
	const height = 3
	block := styleSidebar.Height(height).Render(renderSidebar(demoRows(), nil, nil, 1, height))
	lines := strings.Split(block, "\n")
	if len(lines) != height {
		t.Fatalf("styled sidebar rendered %d lines, want %d — a wrapped row doubles them:\n%s",
			len(lines), height, plain(block))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != sidebarWidth {
			t.Errorf("line %d is %d cells wide, want exactly %d: %q", i, w, sidebarWidth, plain(l))
		}
	}
}

// The finding this closes: a long row list used to push the detail band and
// the footer off a short terminal. The sidebar now renders exactly `height`
// lines, always including the selected row.
func TestRenderSidebarWindowsToTheHeight(t *testing.T) {
	var rows []control.Row
	rows = append(rows, control.Row{Kind: control.RowProject, Project: "alpha", Name: "alpha"})
	for i := 0; i < 40; i++ {
		rows = append(rows, control.Row{Kind: control.RowSandbox, Project: "alpha",
			Name:  "sandbox-" + string(rune('a'+i%26)) + strings.Repeat("x", i/26),
			State: control.StateRunning, Selectable: true})
	}
	const height = 10
	out := renderSidebar(rows, nil, nil, 35, height)
	lines := strings.Split(out, "\n")
	if len(lines) != height {
		t.Fatalf("rendered %d lines, want exactly %d", len(lines), height)
	}
	if !strings.Contains(plain(out), plain(rows[35].Name)) {
		t.Errorf("the selected row must be inside the window; got:\n%s", plain(out))
	}
}

func TestSidebarWindowKeepsTheSelectionVisible(t *testing.T) {
	lines := make([]sidebarLine, 50)
	for i := range lines {
		lines[i] = sidebarLine{text: "row", row: i}
	}
	cases := []struct{ selected, height int }{{0, 10}, {5, 10}, {25, 10}, {49, 10}, {3, 100}}
	for _, tc := range cases {
		from, to := sidebarWindow(lines, tc.selected, tc.height)
		if to-from > tc.height {
			t.Errorf("selected %d: window %d..%d exceeds height %d", tc.selected, from, to, tc.height)
		}
		if tc.selected < from || tc.selected >= to {
			t.Errorf("selected %d fell outside window %d..%d", tc.selected, from, to)
		}
		if from < 0 || to > len(lines) {
			t.Errorf("window %d..%d out of range", from, to)
		}
	}
}
```

- [ ] **Step 3: Write the failing detail test**

Create `internal/controlplane/view_detail_test.go`:

```go
package controlplane

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/elliottregan/cspace/internal/control"
)

func TestFormatMemory(t *testing.T) {
	cases := map[int64]string{
		16 << 30:  "16G",
		1 << 30:   "1G",
		512 << 20: "512M",
		0:         "-",
	}
	for in, want := range cases {
		if got := formatMemory(in); got != want {
			t.Errorf("formatMemory(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatMemUsage(t *testing.T) {
	const gib = int64(1) << 30
	cases := []struct {
		name      string
		used, cap int64
		want      string
	}{
		// Whole gigabytes would collapse these two distinct sandboxes onto
		// the same "1G", which is why usage keeps one decimal.
		{"gib scale keeps one decimal", 1193979904, 16 * gib, "1.1G/16G"},
		{"gib scale distinguishes neighbours", 1717986918, 16 * gib, "1.6G/16G"},
		{"sub-gib usage renders as MiB", 512 * (1 << 20), 4 * gib, "512M/4G"},
		{"missing sample falls back to cap", 0, 4 * gib, "4G"},
		{"missing sample and no cap", 0, 0, "-"},
		{"usage with no cap shows usage alone", 2 * gib, 0, "2.0G"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatMemUsage(tc.used, tc.cap); got != tc.want {
				t.Errorf("formatMemUsage(%d, %d) = %q, want %q", tc.used, tc.cap, got, tc.want)
			}
		})
	}
}

func TestFormatUptime(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, ""},
		{45 * time.Second, "↑45s"},
		{5 * time.Minute, "↑5m"},
		{2*time.Hour + 14*time.Minute, "↑2h14m"},
	}
	for _, tc := range cases {
		if got := formatUptime(tc.in); got != tc.want {
			t.Errorf("formatUptime(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// formatAge feeds the footer's staleness marker when a poll fails. It is
// Task 5 that renders it; it is tested here with the other formatters.
func TestFormatAge(t *testing.T) {
	now := time.Date(2026, 9, 18, 14, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		in   time.Time
		want string
	}{
		{"never polled", time.Time{}, "never"},
		{"seconds", now.Add(-12 * time.Second), "12s ago"},
		{"minutes", now.Add(-3 * time.Minute), "3m ago"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatAge(tc.in, now); got != tc.want {
				t.Errorf("formatAge = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderDetailSandbox(t *testing.T) {
	row := control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "mercury",
		Container: "cspace-alpha-mercury", State: control.StateRunning,
		IP: "192.168.64.5", MemoryB: 16 << 30, Uptime: 2*time.Hour + 14*time.Minute,
		Agent: control.AgentStatus{Reachable: true, State: "idle", Session: "primary"}}
	live := liveState{
		Agent: control.AgentStatus{Reachable: true, State: "working", Session: "primary",
			QueueDepth: 2, LastEventType: "assistant", LastEventSubtype: "text",
			LastEventTs: "2026-09-18T14:02:11Z"},
		Interactive: control.InteractiveState{State: "needs-input",
			Event: "PreToolUse", At: "2026-09-18T14:03:00Z"},
	}
	ports := []control.Port{{Port: 5173, Label: "web", URL: "http://mercury.alpha.cspace.test:5173/"}}
	events := []control.EventLine{
		{Ts: "2026-09-18T14:02:11Z", Kind: "sdk", Type: "assistant", Subtype: "text"},
	}

	out := plain(renderDetail(row, live, ports, nil, events, nil, 1717986918, 70))
	for _, want := range []string{
		"mercury", "running", "↑2h14m", "1.6G/16G",
		"agent: working", "session primary", "queue 2", "assistant/text",
		"claude: needs-input", "PreToolUse",
		"5173", "web", "http://mercury.alpha.cspace.test:5173/",
		"14:02:11",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("detail missing %q; got:\n%s", want, out)
		}
	}
}

// An unreachable supervisor has to say so, because send and interrupt are
// off for that row and a person needs to know why.
func TestRenderDetailUnreachableAgent(t *testing.T) {
	row := control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "mercury",
		State: control.StateDegraded}
	out := plain(renderDetail(row, liveState{}, nil, nil, nil, nil, 0, 70))
	if !strings.Contains(out, "supervisor unreachable") {
		t.Errorf("detail should name the unreachable supervisor; got:\n%s", out)
	}
	if !strings.Contains(out, "send and interrupt are off") {
		t.Errorf("detail should explain the disabled actions; got:\n%s", out)
	}
}

func TestRenderDetailStoppedSandboxOffersBoot(t *testing.T) {
	row := control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "issue-42",
		State: control.StateStopped}
	out := plain(renderDetail(row, liveState{}, nil, nil, nil, nil, 0, 70))
	if !strings.Contains(out, "stopped") || !strings.Contains(out, "press u to boot") {
		t.Errorf("a stopped sandbox should offer the boot key; got:\n%s", out)
	}
}

func TestRenderDetailBrowserHealth(t *testing.T) {
	row := control.Row{Kind: control.RowBrowser, Project: "alpha", Name: "browser (shared)",
		State: control.StateRunning, Browser: control.BrowserHealth{Reachable: true, Version: "Chrome/140"}}
	out := plain(renderDetail(row, liveState{}, nil, nil, nil, nil, 0, 70))
	if !strings.Contains(out, "CDP") || !strings.Contains(out, "Chrome/140") {
		t.Errorf("browser detail should show CDP health; got:\n%s", out)
	}

	row.Browser = control.BrowserHealth{}
	out = plain(renderDetail(row, liveState{}, nil, nil, nil, nil, 0, 70))
	if !strings.Contains(strings.ToLower(out), "restart") {
		t.Errorf("an unreachable browser should hint the restart key; got:\n%s", out)
	}
}

// A ports probe that failed degrades that one line — the band keeps
// rendering everything else it knows.
func TestRenderDetailPortsError(t *testing.T) {
	row := control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "mercury",
		State: control.StateRunning, Agent: control.AgentStatus{Reachable: true, State: "idle"}}
	out := plain(renderDetail(row, liveState{}, nil, errors.New("ss: exit 127"), nil, nil, 0, 70))
	if !strings.Contains(out, "ports unavailable") || !strings.Contains(out, "exit 127") {
		t.Errorf("a failed ports probe should degrade one line; got:\n%s", out)
	}
	if !strings.Contains(out, "mercury") {
		t.Errorf("the rest of the band must still render; got:\n%s", out)
	}
}

// Spec, Error handling: every poll failure degrades the fields it feeds. An
// unreadable event log is one of them — "no agent events yet" for a sandbox
// whose log could not be opened is a lie the band must not tell.
func TestRenderDetailEventsError(t *testing.T) {
	row := control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "mercury",
		State: control.StateRunning, Agent: control.AgentStatus{Reachable: true, State: "idle"}}
	out := plain(renderDetail(row, liveState{}, nil, nil, nil,
		errors.New("open events.ndjson: permission denied"), 0, 70))
	if !strings.Contains(out, "events unavailable") || !strings.Contains(out, "permission denied") {
		t.Errorf("a failed event read should degrade one line; got:\n%s", out)
	}
	if strings.Contains(out, "no agent events yet") {
		t.Errorf("a failed read must not read as an empty log; got:\n%s", out)
	}
}

// Every line is padded (never truncated past) the width it is given, so the
// main area's right edge stays straight.
func TestRenderDetailFitsItsWidth(t *testing.T) {
	row := control.Row{Kind: control.RowSandbox, Project: "alpha",
		Name:  "a-very-long-sandbox-name-that-will-not-fit-in-forty-columns",
		State: control.StateRunning, Agent: control.AgentStatus{Reachable: true, State: "idle"}}
	for _, line := range strings.Split(plain(renderDetail(row, liveState{}, nil, nil, nil, nil, 0, 40)), "\n") {
		if len([]rune(line)) > 40 {
			t.Errorf("line wider than 40: %q", line)
		}
	}
}
```

- [ ] **Step 4: Run both to watch them fail**

Run: `go test ./internal/controlplane/ -run 'Sidebar|Glyph|Detail|Format' -v`
Expected: FAIL to build — `undefined: stateGlyph`, `undefined: renderSidebar`, `undefined: renderDetail`, `undefined: formatMemory`.

- [ ] **Step 5: Write the styles**

Create `internal/controlplane/styles.go`:

```go
package controlplane

import "charm.land/lipgloss/v2"

// The dashboard's palette.
//
// lipgloss v2 has no global renderer and does no TTY sniffing at Render
// time: Render always emits ANSI and the program's renderer downsamples on
// write. Tests therefore compare ansi-stripped output (see plain() in
// view_sidebar_test.go), never raw bytes.
var (
	styleProject  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#8888ff"))
	styleDim      = lipgloss.NewStyle().Faint(true)
	styleSelected = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#5fffaf"))
	styleErr      = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))
	styleOK       = lipgloss.NewStyle().Foreground(lipgloss.Color("#5fffaf"))
	stylePort     = lipgloss.NewStyle().Foreground(lipgloss.Color("#87afff"))

	// styleSidebar draws the vertical rule that separates the list from the
	// main area. The rule is the sidebar's 24th column, and lipgloss v2's
	// Width is the *whole block's* width — Render subtracts the border size
	// before wrapping — so this is sidebarWidth, not sidebarInner. Giving it
	// sidebarInner would leave sidebarContent columns for a line that is
	// sidebarInner cells wide and wrap every single row onto two lines.
	// MaxWidth is the same number for the same reason: it truncates after
	// the border is applied, so anything smaller would cut the rule off.
	styleSidebar = lipgloss.NewStyle().
			Width(sidebarWidth).MaxWidth(sidebarWidth).
			Border(lipgloss.NormalBorder(), false, true, false, false).
			BorderForeground(lipgloss.Color("#444444"))
)
```

- [ ] **Step 6: Write the sidebar**

Create `internal/controlplane/view_sidebar.go`:

```go
package controlplane

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/elliottregan/cspace/internal/control"
)

const (
	// sidebarWidth is the sidebar's total width, fixed by the design at 24
	// columns. The last of them is the vertical rule separating it from the
	// main area, and the first of what remains is the selection marker, so
	// a row's text gets sidebarContent.
	sidebarWidth   = 24
	sidebarInner   = sidebarWidth - 1
	sidebarContent = sidebarInner - 1
)

// The sidebar's state glyphs. One character each: at 24 columns there is no
// room for a word, and the detail band spells the state out anyway.
const (
	glyphStopped    = "✕"
	glyphBooting    = "◐"
	glyphDegraded   = "!"
	glyphWorking    = "●"
	glyphIdle       = "○"
	glyphNeedsInput = "▲"
	glyphHealthy    = "✓"
)

// stateGlyph is a row's one-character state, in the design's precedence
// order: lifecycle first — stopped, booting, degraded — then, for a running
// sandbox, the interactive session's state when a hook has ever written one,
// else the supervisor's, else idle.
//
// An interactive "exited" (or any state this does not know) falls through to
// the supervisor: the person's session being over says nothing about whether
// the headless agent is still working.
func stateGlyph(row control.Row, live liveState) string {
	switch row.State {
	case control.StateStopped:
		return glyphStopped
	case control.StateBooting:
		return glyphBooting
	case control.StateDegraded:
		return glyphDegraded
	}
	if row.Kind == control.RowBrowser {
		if row.Browser.Reachable {
			return glyphHealthy
		}
		return glyphDegraded
	}
	if row.Kind != control.RowSandbox {
		return glyphHealthy
	}
	if live.Interactive.Known() {
		switch live.Interactive.State {
		case "working":
			return glyphWorking
		case "needs-input":
			return glyphNeedsInput
		case "idle":
			return glyphIdle
		case "starting":
			return glyphBooting
		}
	}
	if a := agentOf(row, live); a.Reachable && a.State == "working" {
		return glyphWorking
	}
	return glyphIdle
}

// sidebarLine is one rendered line and the row it belongs to. Rows expand to
// more than one line (a sandbox owns a line per labeled port), so the window
// arithmetic works in lines while the model keeps thinking in rows. row is
// -1 for a line that belongs to no row, like the system divider.
type sidebarLine struct {
	text string
	row  int
}

// sidebarLines renders every row, and each sandbox's known ports beneath it,
// into flat lines of at most sidebarInner cells.
func sidebarLines(rows []control.Row, live map[sandboxKey]liveState, ports map[sandboxKey][]control.Port, selected int) []sidebarLine {
	out := make([]sidebarLine, 0, len(rows)+4)
	dividerDone := false
	// parent is the sandbox the following sidecar rows belong to: Correlate
	// names a sidecar "<sandbox>-<service>", and only the row order says
	// where that prefix ends.
	parent := ""
	for i, row := range rows {
		if row.Kind == control.RowSystem && !dividerDone {
			out = append(out, sidebarLine{text: styleDim.Render(fit("— system —", sidebarInner)), row: -1})
			dividerDone = true
		}
		if row.Kind == control.RowSandbox {
			parent = row.Name
		}

		marker, isSelected := " ", i == selected && row.Selectable
		text, style := sidebarRow(row, live[keyOf(row)], parent)
		if isSelected {
			// The marker alone carries the selection at 24 columns on a
			// terminal with no colour; styleSelected carries it everywhere
			// else. Only selectable rows can be selected, and their rows
			// come back unstyled, so nothing is being overridden here.
			marker, style = "▸", styleSelected
		}
		out = append(out, sidebarLine{text: marker + style.Render(fit(text, sidebarContent)), row: i})

		if row.Kind != control.RowSandbox {
			continue
		}
		for _, p := range ports[keyOf(row)] {
			label := strings.TrimSpace(fmt.Sprintf("%d %s", p.Port, p.Label))
			// The URL rides as an OSC 8 hyperlink on the visible label, so
			// the row stays inside 24 columns and the terminal still opens
			// the real address.
			out = append(out, sidebarLine{
				text: " " + stylePort.Hyperlink(p.URL).Render(fit("   "+label, sidebarContent)),
				row:  i,
			})
		}
	}
	return out
}

// sidebarRow is one row's text and style, without the selection marker.
// parent is the sandbox a sidecar hangs under, whose name prefixes it.
func sidebarRow(row control.Row, live liveState, parent string) (string, lipglossStyle) {
	switch row.Kind {
	case control.RowProject:
		return "▾ " + row.Name, styleProject
	case control.RowSidecar:
		// Sidecars nest under their sandbox; Correlate left that sandbox's
		// name on the front of theirs, which is redundant here. Trimming by
		// the parent rather than at the first "-" is what keeps a
		// hyphenated sandbox name (issue-42) from eating its service name.
		name := strings.TrimPrefix(row.Name, parent+"-")
		return "   ├ " + name, styleDim
	case control.RowSystem:
		return "  " + row.Name, styleDim
	}
	return stateGlyph(row, live) + " " + row.Name, lipglossStyle{}
}

// sidebarWindow is the half-open range of lines to render so the selected
// row stays on screen in a region of `height` lines. It is a pure function
// of the selection rather than a remembered scroll offset: rows are rebuilt
// from scratch every poll, and an offset carried across that would drift
// against a list that grew or shrank underneath it.
func sidebarWindow(lines []sidebarLine, selected, height int) (from, to int) {
	if height <= 0 || len(lines) == 0 {
		return 0, 0
	}
	if len(lines) <= height {
		return 0, len(lines)
	}
	anchor := 0
	for i, l := range lines {
		if l.row == selected {
			anchor = i
			break
		}
	}
	from = anchor - height/2
	if from < 0 {
		from = 0
	}
	if from+height > len(lines) {
		from = len(lines) - height
	}
	return from, from + height
}

// renderSidebar renders exactly height lines: the window above, padded out
// so the vertical rule runs the full height even when there is little to
// show. Rendering a fixed number of lines is what stops a busy host from
// pushing the detail band and the footer off a short terminal.
func renderSidebar(rows []control.Row, live map[sandboxKey]liveState, ports map[sandboxKey][]control.Port, selected, height int) string {
	lines := sidebarLines(rows, live, ports, selected)
	from, to := sidebarWindow(lines, selected, height)
	out := make([]string, 0, height)
	for _, l := range lines[from:to] {
		out = append(out, l.text)
	}
	for len(out) < height {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

// fit pads s with spaces, or truncates it with an ellipsis, so it occupies
// exactly w cells. Width is measured in display cells (ansi.StringWidth), so
// a glyph like ● or a multi-byte name is never cut mid-character.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if width := ansi.StringWidth(s); width <= w {
		return s + strings.Repeat(" ", w-width)
	}
	runes := []rune(s)
	for len(runes) > 0 && ansi.StringWidth(string(runes))+1 > w {
		runes = runes[:len(runes)-1]
	}
	out := string(runes) + "…"
	if pad := w - ansi.StringWidth(out); pad > 0 {
		out += strings.Repeat(" ", pad)
	}
	return out
}
```

Add the small alias this file uses at the bottom of `styles.go` so the row
renderer can return a style without importing lipgloss everywhere:

```go
// lipglossStyle is lipgloss.Style under a local name, so view code can hand
// styles around without every file importing lipgloss. The zero value
// renders text unchanged, which is what an unstyled row wants.
type lipglossStyle = lipgloss.Style
```

- [ ] **Step 7: Write the detail band**

Create `internal/controlplane/view_detail.go`:

```go
package controlplane

import (
	"fmt"
	"strings"
	"time"

	"github.com/elliottregan/cspace/internal/control"
)

// detailEvents is how many event-log lines the band shows.
const detailEvents = 8

// renderDetail renders the selected row's detail band: uptime, memory, agent
// session and last event, the labeled URLs, and the tail of the supervisor's
// event log — everything the 24-column sidebar has no room for.
//
// It takes an explicit width so one renderer serves both placements: the
// main area now, and the narrow strip under the sidebar once panes take the
// main area over in rollout step 4.
func renderDetail(row control.Row, live liveState, ports []control.Port, portsErr error, events []control.EventLine, eventsErr error, memoryUsedB int64, width int) string {
	var lines []string
	add := func(style lipglossStyle, format string, args ...any) {
		lines = append(lines, style.Render(fit(fmt.Sprintf(format, args...), width)))
	}

	switch row.Kind {
	case control.RowSandbox:
		add(lipglossStyle{}, "%s · %s · %s · %s", row.Name, stateLabel(row),
			formatUptime(row.Uptime), formatMemUsage(memoryUsedB, row.MemoryB))
		if row.State == control.StateStopped {
			add(styleDim, "not running — press u to boot it, or select another sandbox")
			return strings.Join(lines, "\n")
		}
		if row.IP != "" {
			add(styleDim, "%s · %s", row.Container, row.IP)
		}

		if a := agentOf(row, live); a.Reachable {
			add(lipglossStyle{}, "agent: %s · session %s · queue %d · last event %s",
				a.State, sessionOr(a.Session), a.QueueDepth, lastEventLabel(a))
		} else {
			add(styleErr, "agent: supervisor unreachable — send and interrupt are off")
		}
		if is := live.Interactive; is.Known() {
			add(lipglossStyle{}, "claude: %s · %s · %s", is.State, is.Event, shortTs(is.At))
		}

		lines = append(lines, "")
		switch {
		case portsErr != nil:
			add(styleDim, "ports unavailable: %v", portsErr)
		case len(ports) == 0:
			add(styleDim, "no listening ports")
		default:
			for _, p := range ports {
				add(stylePort.Hyperlink(p.URL), "  %-6d %-12s %s", p.Port, p.Label, p.URL)
			}
		}

		lines = append(lines, "")
		switch {
		case eventsErr != nil:
			// Degrade the same way a failed ports probe does: an unreadable
			// log is not an empty one, and saying "no events yet" for a
			// sandbox whose events.ndjson could not be opened hides the
			// only clue there is.
			add(styleDim, "events unavailable: %v", eventsErr)
		case len(events) == 0:
			add(styleDim, "no agent events yet")
		default:
			add(styleDim, "recent events")
			for _, e := range tailEvents(events, detailEvents) {
				add(styleDim, "  %s %-10s %s", shortTs(e.Ts), e.Type, e.Subtype)
			}
		}

	case control.RowBrowser:
		add(lipglossStyle{}, "%s · %s", row.Name, stateLabel(row))
		if row.Browser.Reachable {
			version := row.Browser.Version
			if version == "" {
				version = "reachable"
			}
			add(styleOK, "CDP :%d · %s", control.BrowserCDPPort, version)
		} else {
			add(styleErr, "CDP :%d unreachable — press b to restart the sidecar",
				control.BrowserCDPPort)
		}
		if row.IP != "" {
			add(styleDim, "%s · %s", row.Container, row.IP)
		}

	case control.RowSidecar, control.RowSystem:
		add(lipglossStyle{}, "%s · %s", row.Name, stateLabel(row))
		add(styleDim, "%s · %s · %s", row.Container, row.IP,
			formatMemUsage(memoryUsedB, row.MemoryB))

	default:
		add(styleDim, "select a sandbox")
	}
	return strings.Join(lines, "\n")
}

// tailEvents keeps the last n of what the reader returned. control.Events
// already bounds its read; this bounds what a narrow band shows.
func tailEvents(events []control.EventLine, n int) []control.EventLine {
	if len(events) <= n {
		return events
	}
	return events[len(events)-n:]
}

func sessionOr(session string) string {
	if session == "" {
		return "-"
	}
	return session
}

// stateLabel is the status word a row prints, derived from State so a
// stopped or degraded sidecar, browser or system container is not
// mislabeled "running".
func stateLabel(r control.Row) string {
	switch r.State {
	case control.StateRunning:
		return "running"
	case control.StateDegraded:
		return "degraded"
	case control.StateBooting:
		return "booting"
	}
	return "stopped"
}

// formatMemory renders bytes as a compact G/M string; 0 is "-".
func formatMemory(b int64) string {
	switch {
	case b <= 0:
		return "-"
	case b >= 1<<30:
		return fmt.Sprintf("%dG", b/(1<<30))
	default:
		return fmt.Sprintf("%dM", b/(1<<20))
	}
}

// formatMemUsage renders live usage against the cap as "<used>/<cap>",
// falling back to the cap alone when there is no sample — a row never loses
// information it used to show. Usage keeps one decimal at GiB scale: whole
// gigabytes would render a 1.6G sandbox and a 1.1G one identically.
func formatMemUsage(usedB, capB int64) string {
	if usedB <= 0 {
		return formatMemory(capB)
	}
	var used string
	if usedB >= 1<<30 {
		used = fmt.Sprintf("%.1fG", float64(usedB)/float64(int64(1)<<30))
	} else {
		used = fmt.Sprintf("%dM", usedB/(1<<20))
	}
	if capB <= 0 {
		return used
	}
	return used + "/" + formatMemory(capB)
}

// formatUptime renders a duration as ↑<h>h<m>m / ↑<m>m / ↑<s>s.
func formatUptime(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("↑%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("↑%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("↑%ds", int(d.Seconds()))
	}
}

// formatAge renders how long ago t was, for the footer's staleness marker.
func formatAge(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	if d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm ago", int(d.Minutes()))
}

func lastEventLabel(a control.AgentStatus) string {
	if a.LastEventType == "" {
		return "-"
	}
	if a.LastEventSubtype != "" {
		return a.LastEventType + "/" + a.LastEventSubtype
	}
	return a.LastEventType
}

// shortTs is HH:MM:SS out of an ISO 8601 timestamp, passed through
// unchanged when it is not one.
func shortTs(ts string) string {
	if len(ts) >= 19 {
		return ts[11:19]
	}
	return ts
}
```

- [ ] **Step 8: Run the view tests**

Run: `go test ./internal/controlplane/ -v`
Expected: PASS — the Task 3 keymap tests plus `TestStateGlyphPrecedence` (12 subtests), the six sidebar tests (including `TestSidebarStyleIsExactlyTheDesignsWidth`), and the detail/formatter tests.

- [ ] **Step 9: Check the whole build**

Run: `make check`
Expected: green.

Two of this task's tests exist partly to keep it that way: `golangci-lint`'s `unused` linter reports any unexported package-level symbol nothing references, and `styleSidebar` and `formatAge` have no non-test consumer until Task 5's `view.go`. `TestSidebarStyleIsExactlyTheDesignsWidth` and `TestFormatAge` reference them, which counts. `styleTabs` and `styleMain` are deliberately *not* declared here for the same reason — they land with `view.go` in Task 5.

- [ ] **Step 10: Resolve the scrolling finding**

In `.cspace/context/findings/2026-07-20-tui-row-list-has-no-viewport-scrolling.md`, change the frontmatter `status: open` to `status: resolved` and append under `## Updates`:

```markdown
### 2026-09-18 — status: resolved
Closed by the rollout step 3 dashboard (`internal/controlplane`), which
replaces the v1 view this was filed against. `renderSidebar` renders exactly
the number of lines the layout gives it, choosing the window with
`sidebarWindow` — a pure function of the selection rather than a remembered
offset, since rows are rebuilt from scratch on every poll and a carried
offset would drift against a list that grew or shrank underneath it. The
detail band and the footer are laid out at fixed heights beside it, so
neither can be pushed off a short terminal.
```

- [ ] **Step 11: Commit**

```bash
cd /Users/elliott/Projects/cspace-control-plane-3
git add go.mod go.sum internal/controlplane .cspace/context/findings
git commit -m "Render the dashboard sidebar and detail band (cs-finding:2026-07-20-tui-row-list-has-no-viewport-scrolling)"
```

---

### Task 5: The model, the three tickers and the layout

The dashboard's state and its poll loop. Three tickers, exactly as the design's cadence table says: fast (1 s) for `InteractiveState` and `AgentStatus`, medium (2 s) for a stats-free `Snapshot`, slow (10 s) for the stats-bearing one and `Ports`. A failed poll degrades the fields it feeds and leaves the last-known rows on screen. `View` composes Task 4's renderers into the design's fixed geometry: 24-column sidebar, a reserved tabs line, the main area, a one-line footer.

**Files:**
- Create: `internal/controlplane/poll.go`
- Create: `internal/controlplane/actor.go`
- Create: `internal/controlplane/model.go`
- Create: `internal/controlplane/view.go`
- Modify: `internal/controlplane/styles.go` (append `styleTabs` and `styleMain`)
- Test: `internal/controlplane/model_test.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: `KeyMap`/`forRow`/`forceQuit` (Task 3); `renderSidebar`, `renderDetail`, `fit`, `formatAge`, the styles (Task 4); `control.SnapshotOpts` (Task 2).
- Produces:
  - `styleTabs`, `styleMain` (appended to Task 4's `styles.go`, where the rest of the palette lives)
  - `type Data interface{ SnapshotWith(context.Context, control.SnapshotOpts) control.Snapshot; AgentStatus(context.Context, string, string) (control.AgentStatus, error); InteractiveState(string, string) control.InteractiveState; Ports(context.Context, string, string) ([]control.Port, error); Events(string, string, int) ([]control.EventLine, error) }`
  - `type Actor interface{ Attach(control.Row) tea.Cmd; Down(control.Row) tea.Cmd; Send(control.Row, string) tea.Cmd; Interrupt(control.Row) tea.Cmd; RestartBrowser(control.Row) tea.Cmd; Up(control.Row) tea.Cmd }`
  - `func Result(label string, err error) tea.Msg`, `func ResultWarn(label, warn string) tea.Msg`, `func ResultLabel(tea.Msg) (string, bool)`, `func ResultErr(tea.Msg) error`, `func ResultWarnText(tea.Msg) string`
  - `type Model` with `Init() tea.Cmd`, `Update(tea.Msg) (tea.Model, tea.Cmd)`, `View() tea.View`
  - `func New(data Data, actor Actor, keys KeyMap) Model`

- [ ] **Step 1: Add bubbletea v2 as a direct dependency**

Run:
```bash
cd /Users/elliott/Projects/cspace-control-plane-3
go get charm.land/bubbletea/v2@v2.0.9
```
Expected: `charm.land/bubbletea/v2` is raised from the v2.0.8 bubbles asked for to v2.0.9, still marked `// indirect` (as `lipgloss/v2` and `x/ansi` still are — Task 8's `go mod tidy` is what reclassifies all three; the comment is advisory and the build reads only the version). `github.com/charmbracelet/bubbletea v1.3.10` must still be listed — `internal/overlay` and (until Task 8) `internal/tui` are on it.

- [ ] **Step 2: Write the failing model test**

Create `internal/controlplane/model_test.go`:

```go
package controlplane

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/elliottregan/cspace/internal/control"
)

// fakeData is the Data seam: canned answers, and a record of what was asked
// for, so the cadence rules can be asserted without any host.
type fakeData struct {
	mu sync.Mutex

	snap      control.Snapshot
	agent     control.AgentStatus
	inter     control.InteractiveState
	ports     []control.Port
	portsErr  error
	events    []control.EventLine
	eventsErr error

	snapshotOpts []control.SnapshotOpts
	portsFor     []string
	agentFor     []string
}

func (f *fakeData) SnapshotWith(_ context.Context, opts control.SnapshotOpts) control.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshotOpts = append(f.snapshotOpts, opts)
	return f.snap
}

func (f *fakeData) AgentStatus(_ context.Context, project, sandbox string) (control.AgentStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agentFor = append(f.agentFor, project+"/"+sandbox)
	return f.agent, nil
}

func (f *fakeData) InteractiveState(string, string) control.InteractiveState { return f.inter }

func (f *fakeData) Ports(_ context.Context, project, sandbox string) ([]control.Port, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.portsFor = append(f.portsFor, project+"/"+sandbox)
	return f.ports, f.portsErr
}

func (f *fakeData) Events(string, string, int) ([]control.EventLine, error) {
	return f.events, f.eventsErr
}

// recordingActor records what the dashboard asked for and reports success.
type recordingActor struct {
	attach, down, interrupt, browser, up []control.Row
	sends                                []struct {
		row  control.Row
		text string
	}
}

func (a *recordingActor) result(label string) tea.Cmd {
	return func() tea.Msg { return Result(label, nil) }
}
func (a *recordingActor) Attach(r control.Row) tea.Cmd {
	a.attach = append(a.attach, r)
	return a.result("attach")
}
func (a *recordingActor) Down(r control.Row) tea.Cmd {
	a.down = append(a.down, r)
	return a.result("down")
}
func (a *recordingActor) Interrupt(r control.Row) tea.Cmd {
	a.interrupt = append(a.interrupt, r)
	return a.result("interrupt")
}
func (a *recordingActor) RestartBrowser(r control.Row) tea.Cmd {
	a.browser = append(a.browser, r)
	return a.result("browser restart")
}
func (a *recordingActor) Up(r control.Row) tea.Cmd {
	a.up = append(a.up, r)
	return a.result("up")
}
func (a *recordingActor) Send(r control.Row, text string) tea.Cmd {
	a.sends = append(a.sends, struct {
		row  control.Row
		text string
	}{r, text})
	return a.result("send")
}

func testSnapshot() control.Snapshot {
	return control.Snapshot{
		TakenAt: time.Unix(1_000_000, 0),
		Daemon:  control.DaemonHealth{Reachable: true, Version: "1.0.0-rc.48"},
		Rows: []control.Row{
			{Kind: control.RowProject, Project: "alpha", Name: "alpha"},
			{Kind: control.RowSandbox, Project: "alpha", Name: "mercury",
				Container: "cspace-alpha-mercury", State: control.StateRunning, Selectable: true,
				MemoryB: 16 << 30, Agent: control.AgentStatus{Reachable: true, State: "idle"}},
			{Kind: control.RowSidecar, Project: "alpha", Name: "mercury-convex",
				Container: "cspace-alpha-mercury-convex", State: control.StateRunning},
			{Kind: control.RowSandbox, Project: "alpha", Name: "issue-42",
				Container: "cspace-alpha-issue-42", State: control.StateStopped, Selectable: true},
			{Kind: control.RowBrowser, Project: "alpha", Name: "browser (shared)",
				Container: "cspace-alpha-browser", State: control.StateRunning, Selectable: true,
				Browser: control.BrowserHealth{Reachable: true, Version: "Chrome/140"}},
		},
	}
}

// newTestModel returns a sized model with one snapshot already applied.
func newTestModel(d *fakeData, a Actor) Model {
	m := New(d, a, NewKeyMap(nil))
	m.now = func() time.Time { return time.Unix(1_000_060, 0) }
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = mm.(Model)
	mm, _ = m.Update(snapshotMsg{snap: d.snap})
	return mm.(Model)
}

func TestInitKicksAllThreeCadences(t *testing.T) {
	m := New(&fakeData{snap: testSnapshot()}, &recordingActor{}, NewKeyMap(nil))
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init must start the poll loop")
	}
	// tea.Batch returns a BatchMsg carrying one Cmd per member.
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("Init should batch its ticks, got %T", cmd())
	}
	if len(batch) != 3 {
		t.Errorf("Init started %d cadences, want 3", len(batch))
	}
}

// Every ticker re-arms itself even while it is skipping a poll, or the
// dashboard would stop updating after the first slow query.
func TestTickersAlwaysRearm(t *testing.T) {
	d := &fakeData{snap: testSnapshot()}
	m := newTestModel(d, &recordingActor{})
	for _, msg := range []tea.Msg{fastTickMsg{}, mediumTickMsg{}, slowTickMsg{}} {
		mm, cmd := m.Update(msg)
		m = mm.(Model)
		if cmd == nil {
			t.Fatalf("%T produced no command", msg)
		}
	}
	if !m.pollingFast || !m.pollingMedium || !m.pollingSlow {
		t.Error("each tick should mark its own poll in flight")
	}
	// A second tick while one is in flight must not start another.
	mm, _ := m.Update(mediumTickMsg{})
	m = mm.(Model)
	mm, _ = m.Update(snapshotMsg{snap: d.snap})
	m = mm.(Model)
	if m.pollingMedium {
		t.Error("a landed snapshot should clear the medium in-flight flag")
	}
}

// The medium cadence must ask for a stats-free snapshot; the slow one for
// the full thing. That split is the whole reason SnapshotOpts exists.
func TestCadencesAskForTheRightSnapshot(t *testing.T) {
	d := &fakeData{snap: testSnapshot()}
	m := newTestModel(d, &recordingActor{})

	// The poll commands are run directly rather than through the tick
	// batches: a batch also carries its own re-arm, and tea.Tick's command
	// blocks for the whole interval when called.
	drain(m.snapshotCmd())
	drain(m.slowCmd())

	d.mu.Lock()
	defer d.mu.Unlock()
	var sawSkip, sawFull bool
	for _, o := range d.snapshotOpts {
		sawSkip = sawSkip || o.SkipStats
		sawFull = sawFull || !o.SkipStats
	}
	if !sawSkip {
		t.Error("the medium ticker should skip stats")
	}
	if !sawFull {
		t.Error("the slow ticker should sample stats")
	}
}

// control.Ports execs `ss` inside the sandbox and errors for one that is not
// running, so the slow ticker must only ask about running or degraded rows.
func TestSlowPollOnlyAsksPortsOfRunningSandboxes(t *testing.T) {
	d := &fakeData{snap: testSnapshot()}
	m := newTestModel(d, &recordingActor{})
	drain(m.slowCmd())

	d.mu.Lock()
	defer d.mu.Unlock()
	for _, target := range d.portsFor {
		if target == "alpha/issue-42" {
			t.Error("Ports was asked about a stopped sandbox")
		}
	}
	if len(d.portsFor) != 1 || d.portsFor[0] != "alpha/mercury" {
		t.Errorf("ports asked for %v, want just alpha/mercury", d.portsFor)
	}
}

// Same rule for the fast ticker: a stopped sandbox has no supervisor to
// probe and no session file to read.
func TestFastPollOnlyProbesRunningSandboxes(t *testing.T) {
	d := &fakeData{snap: testSnapshot()}
	m := newTestModel(d, &recordingActor{})
	drain(m.liveCmd())

	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.agentFor) != 1 || d.agentFor[0] != "alpha/mercury" {
		t.Errorf("agent probed for %v, want just alpha/mercury", d.agentFor)
	}
}

// Memory usage arrives on the slow cadence only; the medium snapshots in
// between carry zeroes and must not blank the column.
func TestMemoryUsageSurvivesAStatsFreeSnapshot(t *testing.T) {
	d := &fakeData{snap: testSnapshot()}
	m := newTestModel(d, &recordingActor{})

	withStats := testSnapshot()
	withStats.Rows[1].MemoryUsedB = 1717986918
	mm, _ := m.Update(slowMsg{snap: withStats})
	m = mm.(Model)
	if got := m.memory["cspace-alpha-mercury"]; got != 1717986918 {
		t.Fatalf("memory = %d, want the slow sample", got)
	}

	mm, _ = m.Update(snapshotMsg{snap: testSnapshot()}) // no stats
	m = mm.(Model)
	if got := m.memory["cspace-alpha-mercury"]; got != 1717986918 {
		t.Errorf("memory = %d after a stats-free snapshot, want it carried forward", got)
	}
	if !strings.Contains(plain(m.View().Content), "1.6G/16G") {
		t.Error("the detail band should still show usage against the cap")
	}

	// A sandbox that stopped drops its remembered usage rather than
	// reporting a number from before it died.
	stopped := testSnapshot()
	stopped.Rows[1].State = control.StateStopped
	mm, _ = m.Update(snapshotMsg{snap: stopped})
	m = mm.(Model)
	if _, ok := m.memory["cspace-alpha-mercury"]; ok {
		t.Error("a stopped sandbox should not keep a remembered usage sample")
	}
}

// Spec, Error handling: a failed poll degrades what it feeds and the sidebar
// never blanks.
func TestSnapshotErrorKeepsTheLastKnownRows(t *testing.T) {
	d := &fakeData{snap: testSnapshot()}
	m := newTestModel(d, &recordingActor{})
	before := len(m.rows)

	mm, _ := m.Update(snapshotMsg{snap: control.Snapshot{Err: errors.New("apiserver down")}})
	m = mm.(Model)
	if len(m.rows) != before {
		t.Errorf("rows = %d after a failed poll, want the last-known %d", len(m.rows), before)
	}
	out := plain(m.View().Content)
	if !strings.Contains(out, "mercury") {
		t.Error("the sidebar must keep rendering the last-known rows")
	}
	if !strings.Contains(out, "apiserver down") {
		t.Errorf("the footer should carry the poll error; got:\n%s", out)
	}
	if !strings.Contains(out, "ago") {
		t.Errorf("the footer should mark how stale the rows are; got:\n%s", out)
	}
}

func TestSelectionSkipsNonSelectableRows(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	if got := m.selectedRow().Name; got != "mercury" {
		t.Fatalf("initial selection = %q, want mercury", got)
	}
	m.moveSelection(1) // skips the sidecar
	if got := m.selectedRow().Name; got != "issue-42" {
		t.Errorf("after one move, selection = %q, want issue-42", got)
	}
	m.moveSelection(-1)
	if got := m.selectedRow().Name; got != "mercury" {
		t.Errorf("after moving back, selection = %q, want mercury", got)
	}
	m.moveSelection(-1) // already at the first selectable row
	if got := m.selectedRow().Name; got != "mercury" {
		t.Errorf("selection ran off the top: %q", got)
	}
}

func TestSnapshotPreservesSelectionByIdentity(t *testing.T) {
	d := &fakeData{snap: testSnapshot()}
	m := newTestModel(d, &recordingActor{})
	m.moveSelection(1) // issue-42

	grown := testSnapshot()
	grown.Rows = append([]control.Row{
		{Kind: control.RowProject, Project: "aaa", Name: "aaa"},
		{Kind: control.RowSandbox, Project: "aaa", Name: "earth", State: control.StateRunning, Selectable: true},
	}, grown.Rows...)
	mm, _ := m.Update(snapshotMsg{snap: grown})
	m = mm.(Model)
	if got := m.selectedRow().Name; got != "issue-42" {
		t.Errorf("selection after a grown snapshot = %q, want issue-42", got)
	}
}

// A torn-down sandbox takes its row with it; the selection lands on a
// neighbour rather than jumping to the top.
func TestSelectionFallsBackWhenTheRowDisappears(t *testing.T) {
	d := &fakeData{snap: testSnapshot()}
	m := newTestModel(d, &recordingActor{})
	m.moveSelection(1) // issue-42

	shrunk := testSnapshot()
	shrunk.Rows = append(shrunk.Rows[:3], shrunk.Rows[4:]...) // drop issue-42
	mm, _ := m.Update(snapshotMsg{snap: shrunk})
	m = mm.(Model)
	if !m.selectedRow().Selectable {
		t.Errorf("selection landed on a non-selectable row: %+v", m.selectedRow())
	}
}

// The fast ticker's interactive state drives the sidebar glyph.
func TestLiveStateDrivesTheGlyph(t *testing.T) {
	d := &fakeData{snap: testSnapshot()}
	m := newTestModel(d, &recordingActor{})
	mm, _ := m.Update(liveMsg{states: map[sandboxKey]liveState{
		{Project: "alpha", Name: "mercury"}: {
			Interactive: control.InteractiveState{State: "needs-input", Event: "PermissionRequest"},
		},
	}})
	m = mm.(Model)
	if !strings.Contains(plain(m.View().Content), glyphNeedsInput+" mercury") {
		t.Errorf("sidebar should show the needs-input glyph; got:\n%s", plain(m.View().Content))
	}
}

func TestNoticesFadeAndPersist(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})

	mm, _ := m.Update(actionResultMsg{label: "down", err: errors.New("boom")})
	m = mm.(Model)
	if !m.notice.isErr || !strings.Contains(plain(m.View().Content), "boom") {
		t.Fatal("a failed action should leave an error notice in the footer")
	}

	mm, cmd := m.Update(actionResultMsg{label: "send"})
	m = mm.(Model)
	if m.notice.isErr || !strings.Contains(m.notice.text, "send") {
		t.Fatalf("notice = %+v, want a send success", m.notice)
	}
	if cmd == nil {
		t.Fatal("a success notice should schedule its own expiry")
	}
	// A stale timer must not clear a newer notice.
	mm, _ = m.Update(noticeExpireMsg{gen: m.noticeGen - 1})
	m = mm.(Model)
	if m.notice.text == "" {
		t.Error("an out-of-date expiry cleared a newer notice")
	}
	mm, _ = m.Update(noticeExpireMsg{gen: m.noticeGen})
	m = mm.(Model)
	if m.notice.text != "" {
		t.Error("the matching expiry should clear the notice")
	}
}

// Spec, Error handling: an attach that fell back to the no-tmux exec has to
// say so. A warning is not an error, but it stays on screen like one — a
// notice that faded in three seconds while the person was inside `claude`
// would never be read at all.
func TestActionWarningStaysInTheFooter(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	mm, cmd := m.Update(ResultWarn("attach",
		"mercury has no tmux — rebuild the image with cspace image build"))
	m = mm.(Model)
	if cmd != nil {
		t.Error("a warning notice must not schedule its own expiry")
	}
	if !m.notice.isErr {
		t.Errorf("notice = %+v, want the sticky alert style", m.notice)
	}
	if out := plain(m.View().Content); !strings.Contains(out, "cspace image build") {
		t.Errorf("the footer should carry the warning; got:\n%s", out)
	}
	if m.action != "" {
		t.Errorf("action = %q, want it cleared by the result", m.action)
	}
}

// Only attach suspends the dashboard. A ten-minute `up` must not freeze
// every row on the host while it runs — watching the booting sandbox is the
// point of the poll loop.
func TestPollingContinuesWhileALongActionRuns(t *testing.T) {
	d := &fakeData{snap: testSnapshot()}
	m := newTestModel(d, &recordingActor{})

	m.action = "up"
	mm, _ := m.Update(mediumTickMsg{})
	m = mm.(Model)
	if !m.pollingMedium {
		t.Error("the medium ticker should still poll while `up` is in flight")
	}

	m.pollingMedium, m.action = false, "attach"
	mm, _ = m.Update(mediumTickMsg{})
	m = mm.(Model)
	if m.pollingMedium {
		t.Error("attach owns the terminal: its poll must be skipped")
	}
}

// The layout is fixed: a 24-column sidebar, a tabs line, the main area, and
// exactly one footer line, all inside the window.
func TestViewGeometry(t *testing.T) {
	d := &fakeData{snap: testSnapshot(), ports: []control.Port{
		{Port: 5173, Label: "web", URL: "http://mercury.alpha.cspace.test:5173/"},
	}}
	m := newTestModel(d, &recordingActor{})
	mm, _ := m.Update(slowMsg{snap: d.snap, ports: map[sandboxKey][]control.Port{
		{Project: "alpha", Name: "mercury"}: d.ports,
	}})
	m = mm.(Model)

	out := plain(m.View().Content)
	lines := strings.Split(out, "\n")
	if len(lines) != 24 {
		t.Fatalf("rendered %d lines, want the window's 24:\n%s", len(lines), out)
	}
	for i, l := range lines {
		if len([]rune(l)) > 100 {
			t.Errorf("line %d is wider than the window: %q", i, l)
		}
	}
	// Sidebar on the left, detail on the right, footer at the bottom.
	if !strings.Contains(lines[0], "alpha") {
		t.Errorf("first line should start the sidebar; got %q", lines[0])
	}
	if !strings.Contains(out, "5173") {
		t.Error("the ports the slow poll found should be on screen")
	}
	if !strings.Contains(lines[len(lines)-1], "attach") {
		t.Errorf("the last line should be the footer's short help; got %q", lines[len(lines)-1])
	}
	if !m.View().AltScreen {
		t.Error("the dashboard runs in the alternate screen")
	}
}

// Ctrl+C is not configurable and quits from anywhere.
func TestCtrlCQuits(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+c should return a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("ctrl+c produced %T, want tea.QuitMsg", cmd())
	}
}

// drain runs a command (and, for a batch, each of its members) so the fake's
// records are populated. Bubble Tea would run them on its own goroutines;
// running them here is equivalent and deterministic. Never hand it a tick
// batch: tea.Tick's command sleeps out its whole interval when called.
func drain(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var out []tea.Msg
	for _, c := range batch {
		out = append(out, drain(c)...)
	}
	return out
}
```

- [ ] **Step 3: Run it to watch it fail**

Run: `go test ./internal/controlplane/ -run 'TestInit|TestTicker|TestCadence|TestSlowPoll|TestFastPoll|TestMemory|TestSnapshot|TestSelection|TestLive|TestNotice|TestAction|TestPolling|TestView|TestCtrlC' -v`
Expected: FAIL to build — `undefined: New`, `undefined: Model`, `undefined: snapshotMsg`, `undefined: Result`.

- [ ] **Step 4: Write the action seam**

Create `internal/controlplane/actor.go`:

```go
package controlplane

import (
	tea "charm.land/bubbletea/v2"

	"github.com/elliottregan/cspace/internal/control"
)

// Actor runs the dashboard's side effects. It is declared here — by the
// consumer — and implemented in internal/cli, whose actor delegates to
// internal/control for everything but attach, which needs to hand the
// terminal to a child and is therefore a Bubble Tea concern. Injecting it is
// what keeps this package from importing internal/cli.
//
// Every method returns a tea.Cmd that eventually emits the message Result
// builds. None of them may do I/O before the returned command runs: Update
// calls these on the UI goroutine, and a probe or a lock taken there freezes
// the whole dashboard.
type Actor interface {
	Attach(row control.Row) tea.Cmd
	Down(row control.Row) tea.Cmd
	Send(row control.Row, text string) tea.Cmd
	Interrupt(row control.Row) tea.Cmd
	RestartBrowser(row control.Row) tea.Cmd
	Up(row control.Row) tea.Cmd
}

// actionResultMsg reports an Actor command's outcome. label is the short
// verb the footer shows ("attach", "down", "send", "interrupt",
// "browser restart", "up"); err is nil on success; warn carries a notice
// from an action that succeeded but has something the person must read.
type actionResultMsg struct {
	label string
	warn  string
	err   error
}

// Result builds the message an Actor returns to report an outcome.
// Exported because the implementation lives in another package.
func Result(label string, err error) tea.Msg { return actionResultMsg{label: label, err: err} }

// ResultWarn reports an action that worked but degraded — the spec's no-tmux
// attach fallback is the one this exists for: the attach succeeds, and the
// person has to be told their session will not outlive the window. It is not
// an error (nothing failed) and it must not fade unread, so the footer keeps
// it in the alert style until the next keypress, exactly like an error.
func ResultWarn(label, warn string) tea.Msg {
	return actionResultMsg{label: label, warn: warn}
}

// ResultLabel, ResultErr and ResultWarnText read a Result back, for
// out-of-package tests.
func ResultLabel(m tea.Msg) (string, bool) {
	r, ok := m.(actionResultMsg)
	return r.label, ok
}

func ResultErr(m tea.Msg) error {
	if r, ok := m.(actionResultMsg); ok {
		return r.err
	}
	return nil
}

func ResultWarnText(m tea.Msg) string {
	if r, ok := m.(actionResultMsg); ok {
		return r.warn
	}
	return ""
}
```

- [ ] **Step 5: Write the poll loop**

Create `internal/controlplane/poll.go`:

```go
package controlplane

import (
	"context"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/elliottregan/cspace/internal/control"
)

// The design's three cadences, and the budget each poll gets.
//
// The fast one probes every running sandbox's supervisor; control's own
// per-probe timeout is 800ms and the fan-out is bounded, so a 1s cadence
// with a 3s ceiling cannot pile up. The slow one pays for `container stats`
// (~2s) plus one `ss` exec per running sandbox, which is why it is 10s and
// why its ceiling is generous.
const (
	fastInterval   = 1 * time.Second
	mediumInterval = 2 * time.Second
	slowInterval   = 10 * time.Second

	fastTimeout   = 3 * time.Second
	mediumTimeout = 5 * time.Second
	slowTimeout   = 45 * time.Second

	// eventTail is how many events.ndjson lines the detail band reads.
	eventTail = 8

	// liveConcurrency bounds the fast ticker's fan-out, matching the bound
	// control uses inside a snapshot: a host with many sandboxes must not
	// open an unbounded burst of sockets every second.
	liveConcurrency = 8
)

// Data is the query surface the dashboard polls. Declared by the consumer
// and satisfied by *control.Client, so the model's tests inject canned data
// without a registry, a daemon or the `container` CLI.
type Data interface {
	SnapshotWith(ctx context.Context, opts control.SnapshotOpts) control.Snapshot
	AgentStatus(ctx context.Context, project, sandbox string) (control.AgentStatus, error)
	InteractiveState(project, sandbox string) control.InteractiveState
	Ports(ctx context.Context, project, sandbox string) ([]control.Port, error)
	Events(project, sandbox string, n int) ([]control.EventLine, error)
}

var _ Data = (*control.Client)(nil)

// One message per cadence, so each has its own in-flight guard and its own
// re-arm.
type (
	fastTickMsg   struct{ at time.Time }
	mediumTickMsg struct{ at time.Time }
	slowTickMsg   struct{ at time.Time }
)

// liveMsg carries the whole fast sample. The model replaces its map wholesale
// rather than merging into it: a sandbox that went away must lose its state,
// and a Model is copied on every Update, so nothing mutates a shared map.
type liveMsg struct{ states map[sandboxKey]liveState }

type snapshotMsg struct{ snap control.Snapshot }

type slowMsg struct {
	snap     control.Snapshot
	ports    map[sandboxKey][]control.Port
	portsErr error
}

type eventsMsg struct {
	lines []control.EventLine
	err   error
}

// sandboxTargets is every sandbox worth asking about: running or degraded.
// A stopped sandbox has no supervisor to probe, no session file to read, and
// control.Ports errors outright for one, so asking would turn an ordinary
// state into an error banner.
func sandboxTargets(rows []control.Row) []sandboxKey {
	var out []sandboxKey
	for _, r := range rows {
		if r.Kind != control.RowSandbox {
			continue
		}
		if r.State == control.StateRunning || r.State == control.StateDegraded {
			out = append(out, keyOf(r))
		}
	}
	return out
}

// liveCmd is the fast cadence: the supervisor's status and the interactive
// session's hook-written state for every running sandbox, concurrently.
func (m Model) liveCmd() tea.Cmd {
	data, targets := m.data, sandboxTargets(m.rows)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fastTimeout)
		defer cancel()

		states := make(map[sandboxKey]liveState, len(targets))
		var mu sync.Mutex
		sem := make(chan struct{}, liveConcurrency)
		var wg sync.WaitGroup
		for _, k := range targets {
			wg.Add(1)
			go func(k sandboxKey) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				// An unreachable supervisor is not an error here: control
				// reports it as AgentStatus{Reachable:false}, which is
				// exactly what the row should render.
				agent, _ := data.AgentStatus(ctx, k.Project, k.Name)
				inter := data.InteractiveState(k.Project, k.Name)
				mu.Lock()
				states[k] = liveState{Agent: agent, Interactive: inter}
				mu.Unlock()
			}(k)
		}
		wg.Wait()
		return liveMsg{states: states}
	}
}

// snapshotCmd is the medium cadence: the row set, without the ~2s stats
// sample the slow cadence pays for.
func (m Model) snapshotCmd() tea.Cmd {
	data := m.data
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), mediumTimeout)
		defer cancel()
		return snapshotMsg{snap: data.SnapshotWith(ctx, control.SnapshotOpts{SkipStats: true})}
	}
}

// slowCmd is the slow cadence: the stats-bearing snapshot and one Ports
// query per running sandbox. The port queries run in sequence — each is a
// `container exec` and they share one transport, so a fan-out would only
// queue inside the substrate.
func (m Model) slowCmd() tea.Cmd {
	data, targets := m.data, sandboxTargets(m.rows)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), slowTimeout)
		defer cancel()

		snap := data.SnapshotWith(ctx, control.SnapshotOpts{})
		ports := make(map[sandboxKey][]control.Port, len(targets))
		var firstErr error
		for _, k := range targets {
			p, err := data.Ports(ctx, k.Project, k.Name)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			ports[k] = p
		}
		return slowMsg{snap: snap, ports: ports, portsErr: firstErr}
	}
}

// eventsCmd reads the selected sandbox's event tail. Anything else selected
// clears the tail rather than leaving the previous sandbox's events under a
// new name.
func (m Model) eventsCmd() tea.Cmd {
	row := m.selectedRow()
	if row.Kind != control.RowSandbox {
		return func() tea.Msg { return eventsMsg{} }
	}
	data, project, name := m.data, row.Project, row.Name
	return func() tea.Msg {
		lines, err := data.Events(project, name, eventTail)
		return eventsMsg{lines: lines, err: err}
	}
}
```

- [ ] **Step 6: Write the model**

Create `internal/controlplane/model.go`:

```go
package controlplane

import (
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/elliottregan/cspace/internal/control"
)

// uiMode is what the keyboard is currently doing. modeConfirmDown and
// modeInput are declared here because paused() reads them; the states
// themselves are entered in input.go and confirm.go.
type uiMode int

const (
	modeNormal uiMode = iota
	modeConfirmDown
	modeInput
)

// noticeLifetime is how long a success notice stays in the footer. Error
// notices stay until the next keypress instead.
const noticeLifetime = 3 * time.Second

// notice is a transient footer message. isErr marks the ones that take the
// alert style and stay until the next keypress — failures, and the warnings
// ResultWarn carries. Everything else fades after noticeLifetime.
type notice struct {
	text  string
	isErr bool
}

// noticeExpireMsg clears a success notice. gen guards against a stale timer
// clearing a newer notice.
type noticeExpireMsg struct{ gen int }

// Model is the dashboard. It is a value type with value-receiver
// Init/Update/View, like every other Bubble Tea model in this repo: mutate
// the local m and return it.
//
// Its maps are never mutated in place. Update assigns a freshly built map
// instead, because a Model is copied on every Update and an in-place write
// would be visible to a copy that had already been handed elsewhere.
type Model struct {
	data  Data
	actor Actor
	keys  KeyMap
	help  help.Model
	now   func() time.Time

	rows     []control.Row
	selected int
	daemon   control.DaemonHealth
	snapErr  error
	lastSnap time.Time

	live     map[sandboxKey]liveState
	memory   map[string]int64 // container name -> live usage bytes
	ports    map[sandboxKey][]control.Port
	portsErr error

	events    []control.EventLine
	eventsErr error

	pollingFast   bool
	pollingMedium bool
	pollingSlow   bool

	mode    uiMode
	input   textinput.Model
	action  string // in-flight action label; "" when idle
	spinner spinner.Model

	notice    notice
	noticeGen int

	width, height int
	quitting      bool
}

// New builds the dashboard over the query and action seams and the resolved
// keymap. Nothing is polled until Init runs.
func New(data Data, actor Actor, keys KeyMap) Model {
	ti := textinput.New()
	ti.Placeholder = "message"
	ti.CharLimit = 2000
	return Model{
		data:    data,
		actor:   actor,
		keys:    keys,
		help:    help.New(),
		now:     time.Now,
		input:   ti,
		spinner: spinner.New(spinner.WithSpinner(spinner.Dot)),
		live:    map[sandboxKey]liveState{},
		memory:  map[string]int64{},
		ports:   map[sandboxKey][]control.Port{},
	}
}

// Init starts all three cadences through their own tick messages rather than
// issuing the queries directly: the tick handlers own the in-flight guards
// and the re-arm, and a first poll that went around them could race the
// first real tick.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		func() tea.Msg { return fastTickMsg{} },
		func() tea.Msg { return mediumTickMsg{} },
		func() tea.Msg { return slowTickMsg{} },
	)
}

// paused reports whether a cadence should skip its poll this time round: a
// modal owns the screen, and attach owns the terminal outright (the program
// is suspended into `container exec`), so neither is a moment to replace the
// row set underneath the person.
//
// Every other action is deliberately *not* paused. They run as ordinary
// commands with the dashboard fully on screen, and they are the long ones —
// `up` is bounded at ten minutes — so pausing on them would freeze every row
// on the host for the whole boot. Watching a booting sandbox turn ○ and gain
// its ports is exactly what the poll loop is for. The one-action-at-a-time
// gate lives in handleNormalKey and is unaffected by this.
func (m Model) paused() bool { return m.mode != modeNormal || m.action == "attach" }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.SetWidth(msg.Width)
		return m, nil

	case fastTickMsg:
		cmds := []tea.Cmd{tea.Tick(fastInterval, func(t time.Time) tea.Msg { return fastTickMsg{at: t} })}
		if !m.pollingFast && !m.paused() {
			m.pollingFast = true
			cmds = append(cmds, m.liveCmd())
		}
		return m, tea.Batch(cmds...)

	case mediumTickMsg:
		cmds := []tea.Cmd{tea.Tick(mediumInterval, func(t time.Time) tea.Msg { return mediumTickMsg{at: t} })}
		if !m.pollingMedium && !m.paused() {
			m.pollingMedium = true
			cmds = append(cmds, m.snapshotCmd())
		}
		return m, tea.Batch(cmds...)

	case slowTickMsg:
		cmds := []tea.Cmd{tea.Tick(slowInterval, func(t time.Time) tea.Msg { return slowTickMsg{at: t} })}
		if !m.pollingSlow && !m.paused() {
			m.pollingSlow = true
			cmds = append(cmds, m.slowCmd())
		}
		return m, tea.Batch(cmds...)

	case liveMsg:
		m.pollingFast = false
		m.live = msg.states
		return m, nil

	case snapshotMsg:
		m.pollingMedium = false
		m.applySnapshot(msg.snap)
		return m, m.eventsCmd()

	case slowMsg:
		m.pollingSlow = false
		m.applySnapshot(msg.snap)
		if msg.ports != nil {
			m.ports = msg.ports
		}
		m.portsErr = msg.portsErr
		return m, nil

	case eventsMsg:
		m.events, m.eventsErr = msg.lines, msg.err
		return m, nil

	case actionResultMsg:
		m.action = ""
		if msg.err != nil {
			// Error notices stay until the next keypress.
			m.notice = notice{text: msg.label + " failed: " + msg.err.Error(), isErr: true}
			return m, nil
		}
		if msg.warn != "" {
			// A warning is not a failure, but it is the thing the person
			// most needs to read — so it takes the alert style and the
			// same stays-until-dismissed lifetime an error gets, rather
			// than fading on a three-second timer.
			m.notice = notice{text: msg.warn, isErr: true}
			return m, nil
		}
		m.notice = notice{text: msg.label + " ok"}
		m.noticeGen++
		gen := m.noticeGen
		return m, tea.Tick(noticeLifetime, func(time.Time) tea.Msg { return noticeExpireMsg{gen: gen} })

	case noticeExpireMsg:
		if msg.gen == m.noticeGen && !m.notice.isErr {
			m.notice = notice{}
		}
		return m, nil

	case spinner.TickMsg:
		// Animate only while an action is in flight; when idle let the tick
		// chain die rather than redraw a whole dashboard forever.
		if m.action == "" {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tea.KeyPressMsg:
		// Ctrl+C is not configurable and is never routed to a modal: a
		// dashboard with no way out is a bug.
		if key.Matches(msg, forceQuit) {
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil
	}
	return m, nil
}

// applySnapshot folds a poll's rows into the model. A failed `container ls`
// keeps the last-known rows and only records the error: the footer marks how
// stale they are, and the sidebar never blanks.
func (m *Model) applySnapshot(snap control.Snapshot) {
	prev := m.selectedRow()
	m.daemon = snap.Daemon
	m.snapErr = snap.Err
	if snap.Err != nil {
		return
	}
	m.rows = snap.Rows
	m.lastSnap = snap.TakenAt
	m.memory = mergeMemory(m.memory, snap.Rows)
	m.restoreSelection(prev)
}

// mergeMemory carries live usage forward across the snapshots that skip
// `container stats`. A row's MemoryUsedB is 0 both when no sample was taken
// and when the container is stopped, so a zero never clears a remembered
// value — but a stopped container drops its, rather than reporting a number
// from before it died, and a container that disappeared drops out entirely.
func mergeMemory(prev map[string]int64, rows []control.Row) map[string]int64 {
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		if r.Container == "" || r.State == control.StateStopped {
			continue
		}
		switch {
		case r.MemoryUsedB > 0:
			out[r.Container] = r.MemoryUsedB
		case prev[r.Container] > 0:
			out[r.Container] = prev[r.Container]
		}
	}
	return out
}

// selectedRow is the current selection, or a zero Row when there is none.
func (m Model) selectedRow() control.Row {
	if m.selected >= 0 && m.selected < len(m.rows) {
		return m.rows[m.selected]
	}
	return control.Row{}
}

// moveSelection moves to the next selectable row in direction dir (+1/-1),
// skipping project headers, sidecars and system rows.
func (m *Model) moveSelection(dir int) {
	n := len(m.rows)
	for i := 1; i <= n; i++ {
		idx := m.selected + dir*i
		if idx < 0 || idx >= n {
			return
		}
		if m.rows[idx].Selectable {
			m.selected = idx
			return
		}
	}
}

// restoreSelection re-points the selection at the row matching prev's
// identity after a new snapshot. When that row is gone — a teardown, say —
// it moves to the nearest remaining selectable row, searching outward from
// the old index rather than jumping to the top.
func (m *Model) restoreSelection(prev control.Row) {
	for i, r := range m.rows {
		if r.Selectable && r.Kind == prev.Kind && r.Project == prev.Project && r.Name == prev.Name {
			m.selected = i
			return
		}
	}
	n := len(m.rows)
	for d := 0; d < n; d++ {
		for _, idx := range [2]int{m.selected + d, m.selected - d} {
			if idx >= 0 && idx < n && m.rows[idx].Selectable {
				m.selected = idx
				return
			}
		}
	}
	m.selected = 0
}
```

- [ ] **Step 7: Write the layout**

First append the two styles the layout needs to `internal/controlplane/styles.go`, at the end of its `var (…)` block — after `styleSidebar` and separated from it by a blank line, so gofmt does not re-align the entries above. They live with the rest of the palette but arrive now rather than in Task 4, because `golangci-lint`'s `unused` reports a package-level style nothing renders with yet:

```go
	// styleTabs titles the reserved tabs line and the help overlay. The
	// one-column padding on each side is what tabsLine's arithmetic
	// accounts for.
	styleTabs = lipgloss.NewStyle().Bold(true).Padding(0, 1)

	// styleMain pads the main area off the sidebar's rule.
	styleMain = lipgloss.NewStyle().Padding(0, 1)
```

Then create `internal/controlplane/view.go`:

```go
package controlplane

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/elliottregan/cspace/internal/control"
)

// View lays out the design's fixed geometry: a 24-column sidebar on the
// left; on the right a tabs line, the main area, and a one-line footer
// across the bottom.
func (m Model) View() tea.View {
	if m.width == 0 || m.height == 0 {
		return tea.NewView("starting cspace tui…")
	}

	bodyHeight := m.height - 1 // the footer
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	mainWidth := m.width - sidebarWidth
	if mainWidth < 20 {
		mainWidth = 20
	}

	side := styleSidebar.Height(bodyHeight).Render(
		renderSidebar(m.rows, m.live, m.ports, m.selected, bodyHeight))

	// MaxHeight as well as Height: Height only pads, and a detail band with
	// a long event tail would otherwise push the footer off the window.
	main := lipgloss.NewStyle().Width(mainWidth).Height(bodyHeight).MaxHeight(bodyHeight).Render(
		lipgloss.JoinVertical(lipgloss.Left,
			m.tabsLine(mainWidth),
			m.mainArea(mainWidth, bodyHeight-1)))

	v := tea.NewView(lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.JoinHorizontal(lipgloss.Top, side, main),
		m.footer()))
	v.AltScreen = true
	return v
}

// tabsLine is the row of pane tabs the design reserves above the main area.
// Step 3 has no panes, so it carries the selection's title on the left and
// daemon health on the right: the line is occupied and the geometry beneath
// it is the one step 4 inherits.
func (m Model) tabsLine(width int) string {
	row := m.selectedRow()
	title := "cspace"
	switch {
	case row.Kind == control.RowProject:
		title = row.Name
	case row.Name != "" && row.Project != "":
		title = row.Project + "/" + row.Name
	case row.Name != "":
		title = row.Name
	}

	health, style := "daemon unreachable", styleErr
	if m.daemon.Reachable {
		health, style = "daemon "+m.daemon.Version, styleDim
	}

	// styleTabs pads by one column on each side; account for that so the
	// right-hand text lands on the last column.
	gap := width - ansi.StringWidth(title) - 2 - ansi.StringWidth(health)
	if gap < 1 {
		gap = 1
	}
	return styleTabs.Render(title) + strings.Repeat(" ", gap) + style.Render(health)
}

// mainArea is the detail band for the selection. Rollout step 4 replaces
// this with the focused pane and moves the band under the sidebar; the
// renderer already takes its width, so that is a layout change, not a
// rewrite.
func (m Model) mainArea(width, height int) string {
	row := m.selectedRow()
	k := keyOf(row)
	body := renderDetail(row, m.live[k], m.ports[k], m.portsErr, m.events, m.eventsErr,
		m.memory[row.Container], width-2)
	return styleMain.Height(height).Render(body)
}

// footer is the one line at the bottom: whatever the dashboard most needs to
// say, and otherwise the short help for what the selection can do.
func (m Model) footer() string {
	switch {
	case m.action != "":
		return m.spinner.View() + " " + m.action + "…"
	case m.notice.text != "":
		if m.notice.isErr {
			return styleErr.Render(fit(m.notice.text, m.width))
		}
		return styleOK.Render(fit(m.notice.text, m.width))
	case m.snapErr != nil:
		return styleErr.Render(fit(fmt.Sprintf("container ls failed: %v — showing the last poll (%s); run cspace doctor",
			m.snapErr, formatAge(m.lastSnap, m.now())), m.width))
	}
	row := m.selectedRow()
	return m.help.ShortHelpView(m.keys.forRow(row, m.live[keyOf(row)]).ShortHelp())
}
```

- [ ] **Step 8: Run the model tests**

Run: `go test ./internal/controlplane/ -v`
Expected: PASS — every test from Tasks 3 and 4 plus the sixteen added here.

- [ ] **Step 9: Prove the dependency direction**

Run:
```bash
go list -deps ./internal/controlplane/ | grep -E 'cspace/internal/(cli|tui)$' || echo "clean"
```
Expected: `clean` — the package depends on `internal/control` and nothing above it.

- [ ] **Step 10: Check**

Run: `make check`
Expected: green. (`internal/tui` still builds and still owns `cspace tui`; nothing is wired to the new model yet.)

- [ ] **Step 11: Commit**

```bash
cd /Users/elliott/Projects/cspace-control-plane-3
git add go.mod go.sum internal/controlplane
git commit -m "Add the dashboard model, its three poll cadences and the layout"
```

---

### Task 6: Input — key dispatch, the send box, the teardown confirmation and the help overlay

Every action the CLI already has, on the keys the design gives the sidebar, gated so a key that cannot apply to the selection is not offered and does not fire. The teardown confirmation is a `huh/v2` form rendered in the main area (not the footer: a one-line footer cannot hold a widget whose height depends on a theme, and the layout's line count must not). `?` swaps the main area for the full binding list.

**Files:**
- Create: `internal/controlplane/input.go`
- Create: `internal/controlplane/confirm.go`
- Modify: `internal/controlplane/model.go` (the `tea.KeyPressMsg` branch and the fall-through)
- Modify: `internal/controlplane/view.go` (`mainArea`, `footer`, add `helpView`)
- Test: `internal/controlplane/input_test.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: `Actor`, `Model` (Task 5); `KeyMap.forRow` (Task 3).
- Produces:
  - `func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd)`
  - `func (m Model) handleNormalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd)`
  - `func (m Model) handleInputKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd)`
  - `func (m Model) startAction(label string, cmd tea.Cmd) (tea.Model, tea.Cmd)`
  - `func newDownConfirm(sandbox string, width int) *huh.Form`, `func (m Model) updateConfirm(msg tea.Msg) (tea.Model, tea.Cmd)`, `const confirmField = "confirm"`
  - `Model.confirm *huh.Form`
  - `func (m Model) helpView(width int) string`

- [ ] **Step 1: Add huh**

Run:
```bash
cd /Users/elliott/Projects/cspace-control-plane-3
go get charm.land/huh/v2@v2.0.3
```
Expected: `charm.land/huh/v2 v2.0.3` in the direct require block. Its own go.mod asks for older v2 bubbletea/bubbles/lipgloss; minimal version selection keeps the newer ones this module already requires.

- [ ] **Step 2: Write the failing input test**

Note the two helpers: `step` for keys the model answers synchronously, and `answer` for the teardown confirmation, which it does not — huh routes an answered `Confirm` through `NextField` → `nextGroup` before the form reports `StateCompleted`, so `y` and `n` take two further message round trips that a test has to pump by hand. `esc` is the exception: `Form.Update` sets `StateAborted` inline, so `step` is right for it.

Create `internal/controlplane/input_test.go`:

```go
package controlplane

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/elliottregan/cspace/internal/control"
)

// press builds the KeyPressMsg a terminal would deliver. bubbletea v2 keys
// are structs: printable keys carry Text, named keys carry a Code.
func press(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// step delivers a key and returns the new model, discarding the command.
func step(t *testing.T, m Model, k string) Model {
	t.Helper()
	mm, _ := m.Update(press(k))
	return mm.(Model)
}

// answer delivers a key to an open teardown confirmation and pumps the
// commands it produces back into the model until the confirmation closes.
//
// huh answers a Confirm over two asynchronous round trips, not one: the
// field sets the value and returns huh.NextField (a *command*), the group
// turns that message into nextGroup (another command), and only when the
// form receives nextGroupMsg does it report StateCompleted. A single Update
// therefore leaves the form open, which is why step() is not enough here.
// Production does this for free — Task 6's fall-through routes the
// unconsumed messages straight back into updateConfirm — so this helper is
// the test-side equivalent of that loop and nothing more.
//
// It stops as soon as the mode leaves modeConfirmDown, so the action's own
// command (which carries the spinner tick) is never run here.
func answer(t *testing.T, m Model, k string) Model {
	t.Helper()
	mm, cmd := m.Update(press(k))
	m = mm.(Model)
	for i := 0; i < 8 && cmd != nil && m.mode == modeConfirmDown; i++ {
		var next tea.Cmd
		for _, msg := range drain(cmd) {
			if msg == nil {
				continue
			}
			mm, c := m.Update(msg)
			m = mm.(Model)
			if c != nil {
				next = c
			}
			if m.mode != modeConfirmDown {
				break
			}
		}
		cmd = next
	}
	if m.mode == modeConfirmDown {
		t.Fatalf("the confirmation never settled after %q", k)
	}
	return m
}

func TestMoveKeysChangeTheSelection(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	m = step(t, m, "j")
	if got := m.selectedRow().Name; got != "issue-42" {
		t.Errorf("after j, selection = %q, want issue-42", got)
	}
	m = step(t, m, "k")
	if got := m.selectedRow().Name; got != "mercury" {
		t.Errorf("after k, selection = %q, want mercury", got)
	}
	m = step(t, m, "down")
	if got := m.selectedRow().Name; got != "issue-42" {
		t.Errorf("after ↓, selection = %q, want issue-42", got)
	}
}

func TestAttachAndInterruptDispatch(t *testing.T) {
	a := &recordingActor{}
	d := &fakeData{snap: testSnapshot()}
	m := newTestModel(d, a)
	// mercury's snapshot agent is idle, so interrupt is gated off; the fast
	// ticker reporting it working is what enables the key.
	mm, _ := m.Update(liveMsg{states: map[sandboxKey]liveState{
		{Project: "alpha", Name: "mercury"}: {
			Agent: control.AgentStatus{Reachable: true, State: "working"}},
	}})
	m = mm.(Model)

	m2 := step(t, m, "enter")
	if len(a.attach) != 1 || a.attach[0].Name != "mercury" {
		t.Fatalf("attach calls = %+v, want one for mercury", a.attach)
	}
	if m2.action != "attach" {
		t.Errorf("action = %q, want attach in flight", m2.action)
	}

	m3 := step(t, m, "i")
	if len(a.interrupt) != 1 {
		t.Errorf("interrupt calls = %d, want 1", len(a.interrupt))
	}
	_ = m3
}

func TestInterruptIsGatedOnAWorkingAgent(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(&fakeData{snap: testSnapshot()}, a) // mercury is idle
	step(t, m, "i")
	if len(a.interrupt) != 0 {
		t.Errorf("interrupt fired on an idle agent: %+v", a.interrupt)
	}
}

// Spec, Error handling: an unreachable supervisor disables send and
// interrupt on that row rather than failing on press.
func TestSendAndInterruptAreOffForADegradedSandbox(t *testing.T) {
	snap := testSnapshot()
	snap.Rows[1].State = control.StateDegraded
	snap.Rows[1].Agent = control.AgentStatus{}
	a := &recordingActor{}
	m := newTestModel(&fakeData{snap: snap}, a)

	m = step(t, m, "m")
	if m.mode == modeInput {
		t.Error("the send box opened for an unreachable supervisor")
	}
	step(t, m, "i")
	if len(a.interrupt) != 0 {
		t.Errorf("interrupt fired for an unreachable supervisor: %+v", a.interrupt)
	}
	// The footer must not advertise them either.
	if out := plain(m.View().Content); strings.Contains(out, "send a turn") {
		t.Errorf("footer offered send for a degraded sandbox:\n%s", out)
	}
}

func TestBootOnlyOffersAStoppedSandbox(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(&fakeData{snap: testSnapshot()}, a)

	step(t, m, "u") // mercury is running
	if len(a.up) != 0 {
		t.Errorf("boot fired on a running sandbox: %+v", a.up)
	}
	m = step(t, m, "j") // issue-42 is stopped
	step(t, m, "u")
	if len(a.up) != 1 || a.up[0].Name != "issue-42" {
		t.Errorf("boot calls = %+v, want one for issue-42", a.up)
	}
}

func TestBrowserRestartDispatches(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(&fakeData{snap: testSnapshot()}, a)
	step(t, m, "b")
	if len(a.browser) != 1 || a.browser[0].Project != "alpha" {
		t.Errorf("browser restart calls = %+v, want one for alpha", a.browser)
	}
}

func TestOneActionAtATime(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(&fakeData{snap: testSnapshot()}, a)
	m = step(t, m, "enter") // attach in flight
	m = step(t, m, "d")     // must not open the confirmation
	if m.mode == modeConfirmDown {
		t.Error("a second action started while one was in flight")
	}
	mm, _ := m.Update(actionResultMsg{label: "attach"})
	m = mm.(Model)
	if m.action != "" {
		t.Errorf("action = %q after its result landed, want cleared", m.action)
	}
	m = step(t, m, "d")
	if m.mode != modeConfirmDown {
		t.Error("the confirmation should open once the gate cleared")
	}
}

func TestSendBox(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(&fakeData{snap: testSnapshot()}, a)

	m = step(t, m, "m")
	if m.mode != modeInput {
		t.Fatalf("mode = %v, want modeInput", m.mode)
	}
	if len(a.sends) != 0 {
		t.Fatal("Send called before the text was entered")
	}
	if !strings.Contains(plain(m.View().Content), "send to mercury") {
		t.Errorf("the footer should show the send box:\n%s", plain(m.View().Content))
	}

	// Ordinary keys type rather than dispatch: "q" must not quit here.
	m = step(t, m, "q")
	if m.quitting {
		t.Fatal("q quit the dashboard from inside the send box")
	}

	m.input.SetValue("do the thing")
	m = step(t, m, "enter")
	if len(a.sends) != 1 || a.sends[0].text != "do the thing" || a.sends[0].row.Name != "mercury" {
		t.Errorf("send calls = %+v, want one for mercury", a.sends)
	}
	if m.mode != modeNormal || m.action != "send" {
		t.Errorf("mode = %v, action = %q after sending", m.mode, m.action)
	}
}

func TestSendBoxEmptyAndCancel(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(&fakeData{snap: testSnapshot()}, a)

	m = step(t, m, "m")
	m = step(t, m, "enter") // empty
	if len(a.sends) != 0 || m.action != "" {
		t.Errorf("an empty send should do nothing: sends=%+v action=%q", a.sends, m.action)
	}

	m = step(t, m, "m")
	m.input.SetValue("discarded")
	m = step(t, m, "esc")
	if len(a.sends) != 0 || m.mode != modeNormal {
		t.Errorf("esc should cancel the send box: sends=%+v mode=%v", a.sends, m.mode)
	}
}

func TestTeardownConfirms(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(&fakeData{snap: testSnapshot()}, a)

	m = step(t, m, "d")
	if m.mode != modeConfirmDown || m.confirm == nil {
		t.Fatalf("mode = %v, confirm = %v, want the confirmation open", m.mode, m.confirm)
	}
	if len(a.down) != 0 {
		t.Fatal("Down called before the confirmation was answered")
	}
	if out := plain(m.View().Content); !strings.Contains(out, "Tear down mercury") {
		t.Errorf("the confirmation should name the sandbox:\n%s", out)
	}
	if out := plain(m.View().Content); !strings.Contains(out, "Keep it") {
		t.Errorf("confirm buttons missing from view:\n%s", out)
	}

	m = answer(t, m, "y")
	if len(a.down) != 1 || a.down[0].Name != "mercury" {
		t.Errorf("down calls = %+v, want one for mercury", a.down)
	}
	if m.mode != modeNormal || m.confirm != nil {
		t.Errorf("the confirmation should close after answering: mode=%v", m.mode)
	}
}

func TestTeardownCancels(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(&fakeData{snap: testSnapshot()}, a)
	m = step(t, m, "d")
	m = step(t, m, "esc")
	if len(a.down) != 0 {
		t.Errorf("esc should cancel the teardown: %+v", a.down)
	}
	if m.mode != modeNormal {
		t.Errorf("mode = %v after cancelling, want modeNormal", m.mode)
	}

	// Answering "no" is a cancel too: huh's Reject binding sets the value
	// and advances the form, which completes it with false — over the same
	// two command round trips "y" takes, hence answer() rather than step().
	m = step(t, m, "d")
	m = answer(t, m, "n")
	if len(a.down) != 0 {
		t.Errorf("answering no should not tear down: %+v", a.down)
	}
	if m.mode != modeNormal || m.confirm != nil {
		t.Errorf("answering no should close the confirmation: mode=%v", m.mode)
	}
}

func TestHelpOverlayToggles(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	m = step(t, m, "?")
	out := plain(m.View().Content)
	if !strings.Contains(out, "restart browser") || !strings.Contains(out, "tear down") {
		t.Errorf("the help overlay should list every binding:\n%s", out)
	}
	if !strings.Contains(out, "~/.cspace/config.json") {
		t.Errorf("the help overlay should say where bindings come from:\n%s", out)
	}
	if !strings.Contains(out, "mercury") {
		t.Error("the sidebar stays visible behind the help overlay")
	}
	m = step(t, m, "?")
	if strings.Contains(plain(m.View().Content), "~/.cspace/config.json") {
		t.Error("the help overlay should toggle off")
	}
}

func TestQuitKeyAndErrorNoticeDismissal(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	mm, _ := m.Update(actionResultMsg{label: "down", err: errors.New("boom")})
	m = mm.(Model)

	m = step(t, m, "j") // any key dismisses an error notice
	if m.notice.text != "" {
		t.Errorf("error notice = %q, want it dismissed on the next keypress", m.notice.text)
	}

	_, cmd := m.Update(press("q"))
	if cmd == nil {
		t.Fatal("q should quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("q produced %T, want tea.QuitMsg", cmd())
	}
}
```

- [ ] **Step 3: Run it to watch it fail**

Run: `go test ./internal/controlplane/ -run 'TestMoveKeys|TestAttachAnd|TestInterruptIs|TestSendAnd|TestBoot|TestBrowserRestart|TestOneAction|TestSendBox|TestTeardown|TestHelpOverlay|TestQuitKey' -v`
Expected: FAIL — `m.confirm undefined`, and every dispatch assertion fails because `Update` ignores keys other than Ctrl+C.

- [ ] **Step 4: Write the key dispatch**

Create `internal/controlplane/input.go`:

```go
package controlplane

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// handleKey routes a keypress to whatever owns the keyboard. Ctrl+C never
// arrives here — Update takes it first, so no mode can swallow the way out.
func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// An error notice stays until the next keypress; any key dismisses it.
	// Success notices fade on their own timer, so leave those alone.
	if m.notice.isErr {
		m.notice = notice{}
	}
	switch m.mode {
	case modeConfirmDown:
		return m.updateConfirm(msg)
	case modeInput:
		return m.handleInputKey(msg)
	}
	return m.handleNormalKey(msg)
}

// handleNormalKey is the sidebar's own keyboard. The keys that never depend
// on the selection come first; the rest go through forRow, whose disabled
// bindings key.Matches skips — so a key the selection cannot use does
// nothing rather than failing on press, and the footer has already stopped
// offering it.
func (m Model) handleNormalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Help):
		m.showHelp = !m.showHelp
		return m, nil
	case key.Matches(msg, m.keys.Quit):
		m.quitting = true
		return m, tea.Quit
	case key.Matches(msg, m.keys.MoveUp):
		m.moveSelection(-1)
		return m, m.eventsCmd()
	case key.Matches(msg, m.keys.MoveDown):
		m.moveSelection(1)
		return m, m.eventsCmd()
	case key.Matches(msg, m.keys.Refresh):
		if m.pollingMedium {
			return m, nil
		}
		m.pollingMedium = true
		return m, m.snapshotCmd()
	}

	// One action at a time: the footer reports one outcome, and attach hands
	// the terminal away entirely while it runs.
	if m.action != "" {
		return m, nil
	}

	row := m.selectedRow()
	keys := m.keys.forRow(row, m.live[keyOf(row)])
	switch {
	case key.Matches(msg, keys.Attach):
		return m.startAction("attach", m.actor.Attach(row))
	case key.Matches(msg, keys.Teardown):
		m.mode = modeConfirmDown
		// The form renders into the main area, so it is built at that
		// width: mainArea's width less styleMain's one column of padding
		// on each side. huh ignores a non-positive width, which is what a
		// model that has not been sized yet would pass.
		m.confirm = newDownConfirm(row.Name, m.width-sidebarWidth-2)
		return m, m.confirm.Init()
	case key.Matches(msg, keys.Send):
		m.mode = modeInput
		m.input.SetValue("")
		return m, m.input.Focus()
	case key.Matches(msg, keys.Interrupt):
		return m.startAction("interrupt", m.actor.Interrupt(row))
	case key.Matches(msg, keys.BrowserRestart):
		return m.startAction("browser restart", m.actor.RestartBrowser(row))
	case key.Matches(msg, keys.Boot):
		return m.startAction("up", m.actor.Up(row))
	}
	return m, nil
}

// startAction marks an action in flight and starts the spinner beside it.
// The Actor's command does the work off the UI goroutine; nothing here
// blocks.
func (m Model) startAction(label string, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	m.action = label
	return m, tea.Batch(cmd, m.spinner.Tick)
}

// handleInputKey drives the send box. While it is open every other binding
// is inert — the keys are text.
func (m Model) handleInputKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		text := m.input.Value()
		m.mode = modeNormal
		m.input.Blur()
		if text == "" {
			return m, nil
		}
		return m.startAction("send", m.actor.Send(m.selectedRow(), text))
	case "esc":
		m.mode = modeNormal
		m.input.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}
```

- [ ] **Step 5: Write the confirmation**

Create `internal/controlplane/confirm.go`:

```go
package controlplane

import (
	"fmt"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
)

// confirmField is the form key the teardown answer is read back under.
const confirmField = "confirm"

// newDownConfirm builds the teardown confirmation: one huh field, defaulting
// to "Keep it", so tearing a sandbox down takes an explicit y (or a move to
// the affirmative button and Enter). The wording names what goes with it,
// because control.Down wipes the clone, the sessions and the volumes.
//
// width is the main area's inner width. It has to be passed: huh.NewGroup
// hardcodes 80 columns and the model never forwards its tea.WindowSizeMsg to
// the form, so without this the prompt would wrap at 80 inside an area that
// is 74 wide on a 100-column terminal and be re-wrapped by the outer style.
// Form.WithWidth ignores a non-positive width, so an unsized model is safe.
func newDownConfirm(sandbox string, width int) *huh.Form {
	km := huh.NewDefaultKeyMap()
	// huh's default quit binding is ctrl+c alone, which the dashboard takes
	// before the form ever sees it — so esc is what cancels here.
	km.Quit = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))
	return huh.NewForm(
		huh.NewGroup(
			// Not Inline: at the main area's width (74 columns on a
			// 100-column terminal) the inline form truncates before either
			// button is visible.
			huh.NewConfirm().
				Key(confirmField).
				Title(fmt.Sprintf("Tear down %s? Its clone, sessions and volumes go with it.", sandbox)).
				Affirmative("Down it").
				Negative("Keep it"),
		),
	).WithKeyMap(km).WithShowHelp(false).WithShowErrors(false).WithWidth(width)
}

// updateConfirm feeds the form and acts on its outcome. huh reports both
// answers through State: Completed carries the value (which may be "no"),
// Aborted is esc.
func (m Model) updateConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.confirm == nil { // defensive: no form, no mode
		m.mode = modeNormal
		return m, nil
	}
	form, cmd := m.confirm.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		m.confirm = f
	}
	switch m.confirm.State {
	case huh.StateCompleted:
		down := m.confirm.GetBool(confirmField)
		m.mode, m.confirm = modeNormal, nil
		if !down {
			return m, nil
		}
		return m.startAction("down", m.actor.Down(m.selectedRow()))
	case huh.StateAborted:
		m.mode, m.confirm = modeNormal, nil
		return m, nil
	}
	return m, cmd
}
```

- [ ] **Step 6: Route keys and stray messages in the model**

In `internal/controlplane/model.go`, add the form to the struct, beside `mode`:

```go
	mode     uiMode
	confirm  *huh.Form
	showHelp bool
	input    textinput.Model
```

and add `"charm.land/huh/v2"` to its imports.

Replace the `tea.KeyPressMsg` branch and the function's tail:

```go
	case tea.KeyPressMsg:
		// Ctrl+C is not configurable and is never routed to a modal: a
		// dashboard with no way out is a bug.
		if key.Matches(msg, forceQuit) {
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil
	}
	return m, nil
}
```

with:

```go
	case tea.KeyPressMsg:
		// Ctrl+C is not configurable and is never routed to a modal: a
		// dashboard with no way out is a bug.
		if key.Matches(msg, forceQuit) {
			m.quitting = true
			return m, tea.Quit
		}
		return m.handleKey(msg)
	}

	// Anything the branches above did not consume goes to whichever widget
	// currently owns the keyboard — a textinput's cursor blink, a form's
	// own timers. The tick and data messages are handled above, so a widget
	// can never swallow a poll's re-arm.
	switch m.mode {
	case modeInput:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	case modeConfirmDown:
		if m.confirm != nil {
			return m.updateConfirm(msg)
		}
	}
	return m, nil
}
```

- [ ] **Step 7: Show the confirmation, the send box and the help overlay**

In `internal/controlplane/view.go`, replace `mainArea` and `footer` with:

```go
// mainArea is what sits under the tabs line: the help overlay when it is
// open, the teardown confirmation while it is unanswered, otherwise the
// detail band for the selection.
//
// The confirmation renders here rather than in the footer because a widget's
// height depends on its theme, and the footer is exactly one line. Rollout
// step 4 replaces this with the focused pane and moves the band under the
// sidebar; renderDetail already takes its width, so that is a layout change
// and not a rewrite.
func (m Model) mainArea(width, height int) string {
	style := styleMain.Height(height).MaxHeight(height)
	switch {
	case m.showHelp:
		return style.Render(m.helpView(width - 2))
	case m.mode == modeConfirmDown && m.confirm != nil:
		return style.Render(m.confirm.View())
	}
	row := m.selectedRow()
	k := keyOf(row)
	return style.Render(renderDetail(row, m.live[k], m.ports[k], m.portsErr, m.events, m.eventsErr,
		m.memory[row.Container], width-2))
}

// helpView is the full binding list, plus the notes the footer has no room
// for. Rendering it in the main area keeps the sidebar visible, so a person
// can read the keys against the row they were about to act on.
func (m Model) helpView(width int) string {
	lines := []string{
		styleTabs.Render("keys"),
		"",
		m.help.FullHelpView(m.keys.FullHelp()),
		"",
		styleDim.Render(fit("ctrl+c quits from anywhere · esc leaves a prompt", width)),
		styleDim.Render(fit("keys the selected row cannot use are hidden from the footer", width)),
		styleDim.Render(fit("bindings come from tui.keys in ~/.cspace/config.json", width)),
	}
	return strings.Join(lines, "\n")
}

// footer is the one line at the bottom: whatever the dashboard most needs to
// say, and otherwise the short help for what the selection can do.
func (m Model) footer() string {
	switch {
	case m.mode == modeInput:
		return fit("send to "+m.selectedRow().Name+" › "+m.input.View(), m.width)
	case m.action != "":
		return m.spinner.View() + " " + m.action + "…"
	case m.notice.text != "":
		if m.notice.isErr {
			return styleErr.Render(fit(m.notice.text, m.width))
		}
		return styleOK.Render(fit(m.notice.text, m.width))
	case m.snapErr != nil:
		return styleErr.Render(fit(fmt.Sprintf("container ls failed: %v — showing the last poll (%s); run cspace doctor",
			m.snapErr, formatAge(m.lastSnap, m.now())), m.width))
	}
	row := m.selectedRow()
	return m.help.ShortHelpView(m.keys.forRow(row, m.live[keyOf(row)]).ShortHelp())
}
```

- [ ] **Step 8: Run the input tests**

Run: `go test ./internal/controlplane/ -v`
Expected: PASS — every test in the package, including the thirteen added here.

- [ ] **Step 9: Check**

Run: `make check`
Expected: green.

- [ ] **Step 10: Commit**

```bash
cd /Users/elliott/Projects/cspace-control-plane-3
git add go.mod go.sum internal/controlplane
git commit -m "Dispatch the dashboard's keys, send box, teardown confirm and help"
```

---

### Task 7: The `internal/cli` actor

The implementation half of `controlplane.Actor`: everything but attach delegates straight to `internal/control`, and attach hands the terminal to `container exec` the way the v1 dashboard does — but with the tmux probe and the attach bookkeeping inside the exec, never in `Update`. `tea.Exec` takes an interface, so all of it lives in one `Run()` that Bubble Tea calls after it has released the terminal.

**Files:**
- Create: `internal/cli/controlplane_actor.go`
- Test: `internal/cli/controlplane_actor_test.go`
- Modify: `internal/cli/cmd_attach.go` (`beginAttachOrWarn` takes the tmux driver; `attachInteractive` passes `defaultTmux`)
- Modify: `internal/cli/cmd_attach_test.go` (its two direct calls to `beginAttachOrWarn`)
- Modify: `internal/cli/tui_actor.go` (the v1 dashboard's own call to `beginAttachOrWarn` — Task 8 deletes the file, but it must keep compiling until then)

**Interfaces:**
- Consumes: `controlplane.Actor`, `controlplane.Result`, `controlplane.ResultWarn` (Task 5); `control.Client.Tmux()` (Task 2); `control.Client.Up(ctx, project, sandbox)` (Task 1).
- Produces:
  - `func newControlPlaneActor(ctrl *control.Client, home string) *cpActor` implementing `controlplane.Actor`
  - `type attachExec` implementing `tea.ExecCommand` (with a `noTmux` field Run sets), plus `func (a *cpActor) attachCommand(row control.Row) *attachExec` and `func attachResult(ex *attachExec, err error) tea.Msg`
  - `func beginAttachOrWarn(ctx context.Context, warn io.Writer, tm *control.Tmux, home string, homeErr error, project, sandbox, container, session string) (*control.Attachment, bool, error)`

- [ ] **Step 1: Write the failing actor test**

Create `internal/cli/controlplane_actor_test.go`:

```go
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/controlplane"
	"github.com/elliottregan/cspace/internal/registry"
)

// countingExecer records how many commands were run through it, so a test
// can prove that nothing touched the host.
type countingExecer struct {
	calls atomic.Int64
	err   error
}

func (c *countingExecer) Exec(context.Context, string, []string) (string, int, error) {
	c.calls.Add(1)
	return "", 1, c.err
}

// drainMsg runs a command and returns its message.
func drainMsg(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

// cpActorAgainst builds an actor whose control client resolves
// alpha/mercury to the given stub supervisor, so the HTTP path runs end to
// end through the same registry lookup production uses.
func cpActorAgainst(t *testing.T, controlURL string) *cpActor {
	t.Helper()
	reg := &registry.Registry{Path: filepath.Join(t.TempDir(), "reg.json")}
	if err := reg.Register(registry.Entry{
		Project: "alpha", Name: "mercury", ControlURL: controlURL, Token: "tok", State: "ready",
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return newControlPlaneActor(control.New(control.Options{Entries: reg}), t.TempDir())
}

func TestControlPlaneActorSendPostsToControlURL(t *testing.T) {
	var gotPath, gotAuth, gotCT, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotPath = req.URL.Path
		gotAuth = req.Header.Get("Authorization")
		gotCT = req.Header.Get("Content-Type")
		var body map[string]string
		_ = json.NewDecoder(req.Body).Decode(&body)
		gotBody = body["text"]
		w.WriteHeader(200)
	}))
	defer srv.Close()

	a := cpActorAgainst(t, srv.URL)
	row := control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "mercury"}
	msg := drainMsg(a.Send(row, "hello"))

	if err := controlplane.ResultErr(msg); err != nil {
		t.Errorf("send should succeed, got %v", err)
	}
	if l, _ := controlplane.ResultLabel(msg); l != "send" {
		t.Errorf("label = %q, want \"send\"", l)
	}
	if gotPath != "/send" || gotAuth != "Bearer tok" || gotCT != "application/json" || gotBody != "hello" {
		t.Errorf("send request: path=%q auth=%q ct=%q body=%q", gotPath, gotAuth, gotCT, gotBody)
	}
}

// Carry-forward from the step-2 review: a 409 means the agent was simply
// idle. The dashboard keeps reporting that as success.
func TestControlPlaneActorInterrupt409IsBenign(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(409)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "no active task"})
	}))
	defer srv.Close()

	a := cpActorAgainst(t, srv.URL)
	row := control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "mercury"}
	msg := drainMsg(a.Interrupt(row))
	if err := controlplane.ResultErr(msg); err != nil {
		t.Errorf("interrupt 409 should be benign, got %v", err)
	}
	if l, _ := controlplane.ResultLabel(msg); l != "interrupt" {
		t.Errorf("label = %q, want \"interrupt\"", l)
	}
}

func TestControlPlaneActorInterrupt500Surfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "boom"})
	}))
	defer srv.Close()

	a := cpActorAgainst(t, srv.URL)
	row := control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "mercury"}
	err := controlplane.ResultErr(drainMsg(a.Interrupt(row)))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("interrupt 500 should surface an error, got %v", err)
	}
}

// Up now names its project, so the dashboard can boot a sandbox of any
// project on screen. With no root recorded anywhere this fails cleanly
// rather than booting the wrong checkout.
func TestControlPlaneActorUpReportsAnUnresolvableProject(t *testing.T) {
	a := cpActorAgainst(t, "http://127.0.0.1:1")
	row := control.Row{Kind: control.RowSandbox, Project: "gamma", Name: "issue-7"}
	msg := drainMsg(a.Up(row))
	if l, _ := controlplane.ResultLabel(msg); l != "up" {
		t.Errorf("label = %q, want \"up\"", l)
	}
	if err := controlplane.ResultErr(msg); err == nil || !strings.Contains(err.Error(), "gamma") {
		t.Errorf("err = %v, want it to name the unresolvable project", err)
	}
}

// The step-1 review's carry-forward: the v1 actor ran the tmux probe and the
// attach bookkeeping synchronously inside Update, which froze the whole
// dashboard on a wedged container. Attach must do no I/O until bubbletea
// runs the ExecCommand — not while building the command, and not while the
// returned tea.Cmd produces its message.
func TestControlPlaneActorAttachTouchesNothingInUpdate(t *testing.T) {
	execer := &countingExecer{err: errors.New("boom: transport down")}
	tm := control.NewTmux()
	tm.Exec = execer
	a := newControlPlaneActor(control.New(control.Options{Tmux: tm}), t.TempDir())
	row := control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "mercury",
		Container: "cspace-alpha-attach-idle"}

	cmd := a.Attach(row)
	if cmd == nil {
		t.Fatal("Attach should return a command")
	}
	if msg := cmd(); msg == nil {
		t.Fatal("the attach command produced no message")
	}
	if n := execer.calls.Load(); n != 0 {
		t.Errorf("Attach ran %d commands before bubbletea suspended the UI, want 0", n)
	}
}

// …and when bubbletea does run it, a probe that cannot reach the sandbox
// aborts the attach with that error rather than exec'ing into nothing.
func TestControlPlaneActorAttachAbortsWhenTheTmuxProbeFails(t *testing.T) {
	execer := &countingExecer{err: errors.New("boom: transport down")}
	tm := control.NewTmux()
	tm.Exec = execer
	a := newControlPlaneActor(control.New(control.Options{Tmux: tm}), t.TempDir())
	row := control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "mercury",
		Container: "cspace-alpha-attach-probe-error"}

	ex := a.attachCommand(row)
	ex.SetStdin(strings.NewReader(""))
	ex.SetStdout(io.Discard)
	ex.SetStderr(io.Discard)

	err := ex.Run()
	if err == nil || !strings.Contains(err.Error(), "transport down") {
		t.Fatalf("Run() = %v, want the probe's transport error", err)
	}
	if execer.calls.Load() == 0 {
		t.Error("Run() should have probed the sandbox")
	}
	// The outcome reaches the dashboard as an attach result.
	if l, _ := controlplane.ResultLabel(attachResult(ex, err)); l != "attach" {
		t.Errorf("label = %q, want \"attach\"", l)
	}
	if got := controlplane.ResultErr(attachResult(ex, err)); got == nil {
		t.Error("attachResult should carry the error")
	}
}

// Spec, Error handling: an image built before tmux falls back to the direct
// exec "with a footer warning naming cspace image build". `cspace attach`
// prints that warning; the dashboard has to carry it too, and the only way
// out of a suspended program is the action result.
func TestAttachResultCarriesTheNoTmuxWarning(t *testing.T) {
	row := control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "mercury"}

	msg := attachResult(&attachExec{row: row, noTmux: true}, nil)
	if l, _ := controlplane.ResultLabel(msg); l != "attach" {
		t.Errorf("label = %q, want \"attach\"", l)
	}
	if err := controlplane.ResultErr(msg); err != nil {
		t.Errorf("a no-tmux attach is not a failure, got %v", err)
	}
	warn := controlplane.ResultWarnText(msg)
	if !strings.Contains(warn, "cspace image build") || !strings.Contains(warn, "mercury") {
		t.Errorf("warning = %q, want it to name the sandbox and the rebuild", warn)
	}

	// An ordinary tmux attach warns about nothing.
	if w := controlplane.ResultWarnText(attachResult(&attachExec{row: row}, nil)); w != "" {
		t.Errorf("warning = %q, want none when tmux was present", w)
	}
	// A failure is reported as a failure, warning or not.
	if err := controlplane.ResultErr(attachResult(&attachExec{row: row, noTmux: true},
		errors.New("exit status 1"))); err == nil {
		t.Error("an exec failure must still surface as an error")
	}
}
```

- [ ] **Step 2: Run it to watch it fail**

Run: `go test ./internal/cli/ -run TestControlPlaneActor -v`
Expected: FAIL to build — `undefined: newControlPlaneActor`, `undefined: cpActor`, `undefined: attachResult`.

- [ ] **Step 3: Let `beginAttachOrWarn` take a driver**

In `internal/cli/cmd_attach.go`, change the signature and the two `control.BeginAttach` calls inside it to use the parameter. The doc comment gains one paragraph:

```go
// beginAttachOrWarn opens the attach's control-plane bookkeeping
// (control.BeginAttach) under the given home directory, downgrading two
// classes of failure to a one-line warning plus an inert attachment instead
// of refusing the whole attach:
//
//   - homeErr non-nil (home is then ignored) — resolving the host home
//     directory failed, so there is nowhere to put the lock/records at all.
//     Callers that already know their home directory (the dashboard resolves
//     it once at startup and refuses to launch if that fails) pass nil here.
//   - BeginAttach itself reporting control.ErrBookkeepingUnavailable — the
//     control-plane directory or its lock file could not be created/opened
//     (a permissions problem, a full disk).
//
// A busy lock (another attach's window still open) is not downgraded:
// BeginAttach reports that as a plain error and this still refuses, since
// guessing the wrong tty out from under a concurrent attach is exactly what
// the lock exists to prevent.
//
// tm is the tmux driver to book the attach against. `cspace attach` passes
// this package's process-wide defaultTmux; the dashboard passes its control
// Client's own driver, so the memoized presence probe and the exec transport
// are shared with every other query that Client makes rather than duplicated.
//
// The returned bool reports whether a warning was written to warn, so the
// caller can skip its post-attach screen reset rather than erase it.
func beginAttachOrWarn(ctx context.Context, warn io.Writer, tm *control.Tmux, home string, homeErr error, project, sandbox, container, session string) (*control.Attachment, bool, error) {
	const bookkeepingWarning = "warning: attach bookkeeping unavailable: %v; this session's tmux client will not be detached automatically\n"

	if homeErr != nil {
		_, _ = fmt.Fprintf(warn, bookkeepingWarning, homeErr)
		att, err := control.BeginAttach(ctx, tm, container, "", "")
		return att, true, err
	}

	dir := control.ControlPlaneDir(home, project, sandbox)
	att, err := control.BeginAttach(ctx, tm, container, dir, session)
	if err != nil {
		if errors.Is(err, control.ErrBookkeepingUnavailable) {
			_, _ = fmt.Fprintf(warn, bookkeepingWarning, err)
			att, err := control.BeginAttach(ctx, tm, container, "", "")
			return att, true, err
		}
		return nil, false, err
	}
	return att, false, nil
}
```

And in `attachInteractive`, pass the driver it already probes with:

```go
	att, bookkeepingWarned, err := beginAttachOrWarn(ctx, warn, defaultTmux, home, homeErr, project, sandbox, containerName, spec.Session)
```

Two existing tests call it directly; give them the same driver. In
`internal/cli/cmd_attach_test.go`, `TestBeginAttachOrWarnFallsBackWhenHomeUnavailable`
becomes:

```go
	att, warned, err := beginAttachOrWarn(context.Background(), &buf, defaultTmux,
		"", errors.New("$HOME is not defined"),
		"proj", "sandbox-home-fail", "cspace-proj-sandbox-home-fail", control.SessionClaude)
```

and `TestBeginAttachOrWarnFallsBackWhenControlPlaneDirUnusable`:

```go
	att, warned, err := beginAttachOrWarn(context.Background(), &buf, defaultTmux,
		fakeHome, nil,
		"proj", "sandbox-dir-fail", "cspace-proj-sandbox-dir-fail", control.SessionClaude)
```

There is a **fourth** caller: the v1 dashboard's actor at `internal/cli/tui_actor.go:58`, which Task 8 deletes but which has to keep compiling until then (Global Constraints: every task before 8 leaves the old dashboard building). It probes with `defaultTmux` already, so give it the same driver:

```go
	att, _, err := beginAttachOrWarn(ctx, io.Discard, defaultTmux, t.home, nil, row.Project, row.Name, row.Container, spec.Session)
```

Miss it and `internal/cli` does not compile for the whole of this task — `not enough arguments in call to beginAttachOrWarn` — and Steps 5 and 6 both fail.

- [ ] **Step 4: Write the actor**

Create `internal/cli/controlplane_actor.go`:

```go
package cli

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/controlplane"
)

// What each dashboard action is allowed to take. These are ceilings, not
// expectations: `up` boots a microVM and can legitimately run for minutes,
// while a send is an HTTP round trip to a process on the same host.
const (
	// attachProbeTimeout bounds the tmux presence probe alone, exactly as
	// `cspace attach` bounds its own (cmd_attach.go). attachBookkeepingTimeout
	// bounds BeginAttach, and is separate and larger on purpose: BeginAttach
	// waits up to control's attachLockWait (tm.PollFor + 2s = 12s) for a
	// concurrent attach's window to clear, and that wait has to be allowed to
	// run out. One shared 10s deadline across both would let a slow probe eat
	// the lock's budget and lose a race `cspace attach` would win.
	attachProbeTimeout       = 10 * time.Second
	attachBookkeepingTimeout = 20 * time.Second
	attachDetachTimeout      = 15 * time.Second
	downTimeout              = 60 * time.Second
	sendTimeout              = 30 * time.Second
	interruptTimeout         = 30 * time.Second
	browserTimeout           = 90 * time.Second
	upTimeout                = 10 * time.Minute
)

// cpActor implements controlplane.Actor by delegating to internal/control,
// which owns the one implementation of each action. Only attach stays here:
// it hands the terminal to a child, which is a Bubble Tea concern and
// therefore not control's. home is kept for the attach lock and the client
// records under ~/.cspace/controlplane/.
type cpActor struct {
	ctrl *control.Client
	home string
}

var _ controlplane.Actor = (*cpActor)(nil)

func newControlPlaneActor(ctrl *control.Client, home string) *cpActor {
	return &cpActor{ctrl: ctrl, home: home}
}

// Attach joins the sandbox's tmux session through the same control-plane
// path `cspace attach` uses, so the two share one session rather than
// running two claudes against one workspace.
//
// Everything it does — the tmux probe, the attach bookkeeping, the child,
// the detach — happens inside attachExec.Run, which bubbletea calls only
// after it has released the terminal. Nothing runs on the UI goroutine: the
// v1 dashboard probed inside Update and froze on a wedged container.
func (a *cpActor) Attach(row control.Row) tea.Cmd {
	ex := a.attachCommand(row)
	// The callback closes over the same exec the program ran, which is how
	// Run's no-tmux finding reaches the footer: tea.Exec calls fn after
	// Run returns, on that same goroutine, so the read is ordered.
	return tea.Exec(ex, func(err error) tea.Msg { return attachResult(ex, err) })
}

// attachCommand builds the ExecCommand. Split out because tea.Exec's own
// message is opaque, so this is the only way a test can run the real thing.
func (a *cpActor) attachCommand(row control.Row) *attachExec {
	return &attachExec{ctrl: a.ctrl, home: a.home, row: row}
}

// attachResult maps the exec's outcome onto the dashboard's action result.
//
// Spec, Error handling: a sandbox whose image has no tmux falls back to the
// direct `claude` exec "with a footer warning naming cspace image build".
// The attach itself succeeded, so this is a warning and not an error — but
// a sticky one, because what it says is that the session the person just
// left did not survive.
func attachResult(ex *attachExec, err error) tea.Msg {
	if err == nil && ex.noTmux {
		return controlplane.ResultWarn("attach", fmt.Sprintf(
			"%s has no tmux: that session did not survive this window, and claude may still be running inside it. Rebuild with `cspace image build`, then `cspace down %s && cspace up %s`.",
			ex.row.Name, ex.row.Name, ex.row.Name))
	}
	return controlplane.Result("attach", err)
}

// attachExec is the tea.ExecCommand the dashboard suspends for. bubbletea
// sets its streams to the program's own, runs it with the terminal released,
// and restores the UI afterwards.
type attachExec struct {
	ctrl *control.Client
	home string
	row  control.Row

	// noTmux is set by Run when the probe found no tmux in the image, and
	// read by attachResult afterwards — the warning has nowhere to go while
	// the child owns the terminal.
	noTmux bool

	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

func (a *attachExec) SetStdin(r io.Reader)  { a.stdin = r }
func (a *attachExec) SetStdout(w io.Writer) { a.stdout = w }
func (a *attachExec) SetStderr(w io.Writer) { a.stderr = w }

// Run probes for tmux, opens the bookkeeping, runs `container exec` in the
// foreground, and detaches the tmux client it created.
//
// The detach is the step that makes closing the session actually end the
// guest-side client: a host-side exit never reaches tmux, which would
// otherwise keep the client attached indefinitely.
func (a *attachExec) Run() error {
	probeCtx, probeCancel := context.WithTimeout(context.Background(), attachProbeTimeout)
	present, err := a.ctrl.Tmux().Present(probeCtx, a.row.Container)
	probeCancel()
	if err != nil {
		// A transport error means we could not learn whether tmux is there,
		// not that it isn't — a direct exec would hit the same failure a
		// moment later, so there is nothing useful to fall back to.
		return fmt.Errorf("cannot reach sandbox %s to probe for tmux: %w", a.row.Name, err)
	}
	// An image built before tmux still attaches, but the session will not
	// survive this window and `claude` keeps running inside the sandbox
	// afterwards. Record it so attachResult can put the warning in the
	// footer once the dashboard has the screen back — `cspace attach`
	// prints the same thing to stderr, and the dashboard must not be the
	// one place this goes unsaid.
	a.noTmux = !present
	spec := control.ClaudeAttach(a.row.Container, present)
	bin, argv, err := control.AttachArgv(spec)
	if err != nil {
		return err
	}
	// The bookkeeping gets its own, larger deadline: BeginAttach may wait
	// out a concurrent attach's lock (control's attachLockWait, 12s), which
	// the probe's 10s could not have covered.
	//
	// io.Discard: the dashboard is between screens and the child is about
	// to paint over everything, so a bookkeeping warning has nowhere to be
	// read. beginAttachOrWarn still downgrades that failure to an inert
	// attachment; it just cannot say so here. home was resolved — and hard
	// failed on — at `cspace tui` startup, so it is passed with a nil
	// homeErr rather than re-resolved.
	bookCtx, bookCancel := context.WithTimeout(context.Background(), attachBookkeepingTimeout)
	att, _, err := beginAttachOrWarn(bookCtx, io.Discard, a.ctrl.Tmux(), a.home, nil,
		a.row.Project, a.row.Name, a.row.Container, spec.Session)
	bookCancel()
	if err != nil {
		return err
	}

	child := exec.Command(bin, argv[1:]...)
	child.Stdin, child.Stdout, child.Stderr = a.stdin, a.stdout, a.stderr
	runErr := child.Run()

	// The detach gets its own context: whatever ended the session may have
	// cancelled everything else, and this is the one thing that must still
	// run while the container is reachable.
	closeCtx, closeCancel := context.WithTimeout(context.Background(), attachDetachTimeout)
	defer closeCancel()
	if closeErr := att.Close(closeCtx); closeErr != nil && runErr == nil {
		runErr = closeErr
	}
	return runErr
}

func (a *cpActor) Down(row control.Row) tea.Cmd {
	ctrl, project, name := a.ctrl, row.Project, row.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), downTimeout)
		defer cancel()
		return controlplane.Result("down", ctrl.Down(ctx, project, name))
	}
}

func (a *cpActor) Send(row control.Row, text string) tea.Cmd {
	ctrl, project, name := a.ctrl, row.Project, row.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
		defer cancel()
		return controlplane.Result("send", ctrl.Send(ctx, project, name, "", text))
	}
}

func (a *cpActor) Interrupt(row control.Row) tea.Cmd {
	ctrl, project, name := a.ctrl, row.Project, row.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), interruptTimeout)
		defer cancel()
		return controlplane.Result("interrupt", ctrl.Interrupt(ctx, project, name))
	}
}

func (a *cpActor) RestartBrowser(row control.Row) tea.Cmd {
	ctrl, project := a.ctrl, row.Project
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), browserTimeout)
		defer cancel()
		return controlplane.Result("browser restart", ctrl.RestartBrowser(ctx, project))
	}
}

// Up boots the selected row's sandbox. control.Up resolves the project's
// checkout from the registry, so this works for any project on screen and
// not just the one `cspace tui` was started in.
//
// The action gate holds for the whole boot — minutes, on a cold image — with
// the footer's spinner as the only progress. `cspace up`'s own overlay is
// not reachable from here: the child is headless. Rollout step 4's panes are
// what make a long boot watchable.
func (a *cpActor) Up(row control.Row) tea.Cmd {
	ctrl, project, name := a.ctrl, row.Project, row.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), upTimeout)
		defer cancel()
		return controlplane.Result("up", ctrl.Up(ctx, project, name))
	}
}
```

- [ ] **Step 5: Run the actor tests**

Run: `go test ./internal/cli/ -run 'ControlPlaneActor|Attach' -v`
Expected: PASS — the seven new tests and the existing attach tests, including the two `beginAttachOrWarn` ones that now pass `defaultTmux` explicitly.

- [ ] **Step 6: Check**

Run: `make check`
Expected: green. Both dashboards exist at this point: `cspace tui` still runs the v1 one.

- [ ] **Step 7: Commit**

```bash
cd /Users/elliott/Projects/cspace-control-plane-3
git add internal/cli
git commit -m "Implement the control-plane actor over internal/control"
```

---

### Task 8: Switch `cspace tui` over and delete the v1 dashboard

The one task where `cspace tui` changes. The command builds the v2 model, `internal/tui` and the v1 actor go, and the doc comments and CLAUDE.md that named them are repointed.

**Files:**
- Modify: `internal/cli/cmd_tui.go` (rewrite)
- Modify: `internal/cli/cmd_tui_test.go` (`TestNewTuiCmdBasics` asserts the v1 `--interval` flag this task removes)
- Modify: `internal/cli/root.go:52` (`tolerateErr`)
- Delete: `internal/tui/` (`actions.go`, `keys.go`, `keys_test.go`, `model.go`, `model_test.go`, `types.go`, `view.go`, `view_test.go`)
- Delete: `internal/cli/tui_actor.go`, `internal/cli/tui_actor_test.go`
- Modify: `internal/control/snapshot.go` (drop the now-unused `Snapshotter`)
- Modify: `internal/control/control.go`, `internal/control/host.go` (doc comments naming `internal/tui`)
- Modify: `CLAUDE.md`
- Modify: `docs/superpowers/specs/2026-09-17-control-plane-design.md` (the two step-3 layout resolutions)
- Modify: `go.mod`, `go.sum` (Step 7's `go mod tidy` promotes the charm v2 modules and x/ansi to direct requirements)

**Interfaces:**
- Consumes: everything from Tasks 1-7.
- Produces: no new API. `controlplane.Data` replaces `control.Snapshotter` as the dashboard's query seam.

- [ ] **Step 1: Rewrite the command**

Replace the whole of `internal/cli/cmd_tui.go`:

```go
package cli

import (
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/elliottregan/cspace/internal/config"
	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/controlplane"
	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
)

// daemonBaseURL is the host daemon's HTTP base (registry + health), matching
// daemonHTTPPort in cmd_daemon.go.
const daemonBaseURL = "http://127.0.0.1:6280"

func newTuiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Full-screen dashboard of cspace containers with common actions",
		Long: `Full-screen dashboard of every cspace container on this host, grouped by
project: lifecycle and agent state per sandbox, its labeled URLs, its
compose sidecars and the project's shared browser.

Actions follow the selection — attach, send a turn, interrupt, tear down,
boot, restart the browser sidecar. Press ? for the full binding list;
bindings come from tui.keys in ~/.cspace/config.json.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("resolve home dir: %w", err)
			}
			regPath, err := registry.DefaultPath()
			if err != nil {
				return fmt.Errorf("resolve registry path: %w", err)
			}
			reg := &registry.Registry{Path: regPath}
			adapter := applecontainer.New()

			// cfg is nil when `cspace tui` runs outside a project (root.go
			// tolerates that for this command): the dashboard spans every
			// project on the host, and Up resolves each one's root from the
			// registry. Project/ProjectRoot only back the fallback for a
			// project that has never been booted.
			project, projectRoot := "", ""
			if cfg != nil {
				project, projectRoot = projectName(), cfg.ProjectRoot
			}

			ctrl := control.New(control.Options{
				Containers:  adapter,
				Entries:     reg,
				Host:        newCLIHost(adapter, reg),
				DaemonURL:   daemonBaseURL,
				Home:        home,
				Now:         time.Now,
				Project:     project,
				ProjectRoot: projectRoot,
			})

			userCfg, err := config.LoadUser(home)
			if err != nil {
				return fmt.Errorf("load %s: %w", config.UserConfigPath(home), err)
			}

			model := controlplane.New(ctrl, newControlPlaneActor(ctrl, home),
				controlplane.NewKeyMap(userCfg.TUI.Keys))
			_, err = tea.NewProgram(model).Run()
			return err
		},
	}
}
```

The v1 `--interval` flag is gone on purpose: there are three cadences now (1 s / 2 s / 10 s), and a flag that tuned one of them would describe the dashboard wrongly. The alternate screen moves with it — bubbletea v2 sets that on the view, which `View` already does.

- [ ] **Step 2: Retire the flag's test**

`internal/cli/cmd_tui_test.go` still asserts that flag, so the package's tests go red the moment Step 1 lands. Replace `TestNewTuiCmdBasics` with:

```go
func TestNewTuiCmdBasics(t *testing.T) {
	cmd := newTuiCmd()
	if cmd.Use != "tui" {
		t.Errorf("Use = %q, want tui", cmd.Use)
	}
	if cmd.Short == "" {
		t.Error("Short must be set")
	}
	// The v1 --interval flag is gone: the v2 dashboard polls on three
	// cadences and no single interval describes it.
	if f := cmd.Flags().Lookup("interval"); f != nil {
		t.Errorf("--interval should be gone in the v2 dashboard, got default %q", f.DefValue)
	}
	// The command takes no arguments: it shows every project on the host.
	if err := cmd.Args(cmd, []string{"mercury"}); err == nil {
		t.Error("tui should refuse positional arguments")
	}
}
```

Leave `TestRootRegistersTui` exactly as it is — it still applies, and Step 3's `root.go` change does not move the registration.

- [ ] **Step 3: Let the dashboard run outside a project**

In `internal/cli/root.go`, add `tui` to the commands whose config load is tolerated, and extend the comment:

```go
			// For the root command (no subcommand), attempt config loading
			// but tolerate failure — the TUI falls back to help when cfg is
			// nil. `cspace doctor` is informational and runnable from any
			// directory; the per-credential probes degrade gracefully when
			// cfg is nil (no project secrets file is checked). `cspace tui`
			// shows every project on the host and resolves each one's root
			// from the registry, so it must start from a directory that is
			// no project at all.
			tolerateErr := (cmd.Name() == "cspace" && cmd.Parent() == nil) ||
				cmd.Name() == "doctor" || cmd.Name() == "tui"
```

- [ ] **Step 4: Delete the v1 dashboard**

Run:
```bash
cd /Users/elliott/Projects/cspace-control-plane-3
git rm -r internal/tui
git rm internal/cli/tui_actor.go internal/cli/tui_actor_test.go
```
Expected: ten files removed.

- [ ] **Step 5: Drop the seam nothing consumes any more**

In `internal/control/snapshot.go`, delete the `Snapshotter` interface and its assertion:

```go
// Snapshotter collects one Snapshot of host state. The dashboard takes this
// interface rather than *Client so its model tests can inject a canned
// snapshot.
type Snapshotter interface {
	Snapshot(ctx context.Context) Snapshot
}

var _ Snapshotter = (*Client)(nil)
```

The dashboard declares its own, wider seam now (`controlplane.Data`, which also covers `AgentStatus`, `InteractiveState`, `Ports` and `Events`), and a consumer-defined interface belongs with the consumer. `Snapshot` and `SnapshotWith` themselves stay.

- [ ] **Step 6: Repoint the doc comments**

In `internal/control/control.go`, the package doc's last sentence about `internal/tui` becomes:

```go
// / Up / ListClients / DetachClient actions — hangs off Client (client.go),
// seamed on ContainerCLI (the substrate), EntryStore (the registry) and Host
// (the internal/cli-owned operations this package cannot import) so it is
// testable without any of them. internal/controlplane consumes these types
// directly — a Row it renders is the one built here, not a copy — through
// the Data interface it declares for itself. The package depends on
// internal/devcontainer for exactly one thing: Ports reads a project's
// devcontainer.json portsAttributes for its port labels.
```

In `internal/control/host.go`, the sentence naming `internal/tui`'s Actor becomes:

```go
// This is the same direction as internal/controlplane's Actor — the consumer
// declares the narrow interface it needs and the package that has the
// implementation satisfies it — so the dependency graph stays acyclic while
// there is still exactly one implementation of each operation.
```

- [ ] **Step 7: Build and run everything**

Run:
```bash
cd /Users/elliott/Projects/cspace-control-plane-3
go mod tidy && make check
```
Expected: green, with `internal/tui` gone from the package list. `go.mod` must still carry `github.com/charmbracelet/bubbletea`, `github.com/charmbracelet/lipgloss` and `github.com/charmbracelet/bubbles` (v1): `internal/overlay` imports all three, and it is not migrated. If the build complains about an unused import in `cmd_tui.go`, it is the v1 `tea` import — the file above imports the v2 one.

- [ ] **Step 8: Confirm the command still exists and describes itself**

Run:
```bash
make build && ./bin/cspace-go tui --help
```
Expected: the new long description, no `--interval` flag.

- [ ] **Step 9: Update CLAUDE.md**

In the `## Architecture` → Go CLI list, replace the trailing sentence of the **control** bullet ("`internal/tui` consumes the package through type aliases …") with:

```markdown
`internal/controlplane` consumes these types directly — the `Row` it renders is the one built here — through a `Data` interface it declares for itself, so the dashboard can be tested with canned data and this package never learns about Bubble Tea.
```

and add a new bullet after it:

```markdown
- **controlplane** — `cspace tui` on Bubble Tea v2 (`charm.land/bubbletea/v2` + `lipgloss/v2` + `bubbles/v2` + `huh/v2`; the `cspace up` overlay stays on v1 and the two majors coexist). A fixed 24-column sidebar of every container grouped by project, a detail band for the selection, a one-line footer and a `?` help overlay. It polls on three cadences — 1 s for agent and interactive state, 2 s for a stats-free `Snapshot`, 10 s for `container stats` and `Ports` — and degrades on a failed poll rather than blanking. Every action goes through an `Actor` implemented in `internal/cli` (`controlplane_actor.go`), so this package never imports `internal/cli`; attach runs inside a `tea.ExecCommand` so the tmux probe and the attach bookkeeping never block the UI goroutine. Keybindings are `bubbles/v2/key` bindings resolved from `tui.keys` in the user-level `~/.cspace/config.json` over the defaults in `lib/defaults.json`.
```

In `## Commands`, replace the `cspace tui` line with:

```markdown
- `cspace tui` — full-screen dashboard of all cspace containers (grouped by project) with attach / send / interrupt / down / up / browser restart, and `?` for the bindings
```

- [ ] **Step 10: Record the two layout resolutions in the spec**

This dashboard — rollout step 3 — differs from the spec's wording in two deliberate places, and the spec is what rollout step 4 will be planned from — so it has to say what shipped rather than leave step 4 to design against a document the code no longer matches.

In `docs/superpowers/specs/2026-09-17-control-plane-design.md`, in the `internal/controlplane` section, replace these three lines (note where they wrap):

```markdown
(`●` working, `○` idle), else `○`. Selecting a
sandbox shows a detail band under the sidebar: uptime, memory, agent
session and last event, URLs.

Tabs: one per open pane, titled `<project>/<sandbox> · <kind>`.
```

with:

```markdown
(`●` working, `○` idle), else `○`. Selecting a
sandbox shows a detail band: uptime, memory, agent session and last event,
URLs.

Tabs: one per open pane, titled `<project>/<sandbox> · <kind>`.

Rollout step 3 places both of those differently, and step 4 moves them.
With no panes to compete for it, the detail band occupies the main area —
24 columns cannot hold a URL, and `renderDetail` takes its width as a
parameter so that move is a layout change, not a rewrite. The reserved tabs
row carries the selection's title and daemon health rather than sitting
empty, because an empty line above the main area reads as a rendering bug
and not as a promise. When panes land, the band moves under the sidebar and
the row becomes the tabs it is named for.
```

It is committed with the code in the next step, so the two never disagree on the branch.

- [ ] **Step 11: Commit**

```bash
cd /Users/elliott/Projects/cspace-control-plane-3
git add -A internal/cli internal/tui internal/control CLAUDE.md docs/superpowers/specs go.mod go.sum
git commit -m "Switch cspace tui to the v2 dashboard and delete the v1 one"
```

---

### Task 9: Verify the dashboard against a real sandbox

Apple Container is not available in CI, and four of the queries this dashboard leans on — `Ports`, `InteractiveState`, `Events` and `Up` — have never run against a real sandbox in any task of any plan on this branch. So this task is run **by a human on a Mac with Apple Container**, from the repo checkout. It is the only proof that the dashboard shows the truth.

**Files:** none modified. This is verification.

**Interfaces:**
- Consumes: everything from Tasks 1-8.
- Produces: nothing. A failure is fixed in the task that owns the file, and re-run.

- [ ] **Step 1: Build**

Run:
```bash
cd /Users/elliott/Projects/cspace-control-plane-3 && make build
```
Expected: `bin/cspace-go` builds with no warnings other than the git-hooks notice.

- [ ] **Step 2: Boot a throwaway sandbox**

Run:
```bash
cd /Users/elliott/Projects/cspace-control-plane-3 && ./bin/cspace-go up dashcheck --no-attach
```
Expected: the boot completes and prints the `attach:` / `browse:` summary. In this checkout the project is `cspace`, so the container is `cspace-cspace-dashcheck`.

- [ ] **Step 3: Confirm the registry recorded the project root**

Run:
```bash
jq '.["cspace:dashcheck"].project_root' ~/.cspace/sandbox-registry.json
```
Expected: the absolute path of this checkout (`/Users/elliott/Projects/cspace-control-plane-3`). An empty or missing value means Task 1's `cmd_up.go` change did not land on the write that survived.

- [ ] **Step 4: Smoke-test the dashboard under a pty**

Write the harness (throwaway — it is deleted in Step 14):

```bash
cat > /tmp/cspace-tui-smoke.py <<'PY'
import fcntl, os, pty, select, signal, struct, termios, time

pid, fd = pty.fork()
if pid == 0:
    os.environ["TERM"] = "xterm-256color"
    os.environ["COLORTERM"] = "truecolor"
    os.execv("./bin/cspace-go", ["cspace-go", "tui"])

# The model renders nothing until it learns the window size.
fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 120, 0, 0))

out = b""

# Always keep draining. The dashboard repaints a 40x120 screen on a 1s
# ticker — several KB a time, far more than a pty buffer holds — so a script
# that stops reading while it types wedges the child in write() and never
# gets its keystrokes read.
def pump(seconds):
    global out
    end = time.time() + seconds
    while time.time() < end:
        r, _, _ = select.select([fd], [], [], 0.2)
        if r:
            try:
                out += os.read(fd, 65536)
            except OSError:
                return

pump(12)
os.write(fd, b"?"); pump(1)      # help overlay
os.write(fd, b"?"); pump(0.5)    # back
os.write(fd, b"q"); pump(3)      # quit

status, exited = None, False
for _ in range(50):              # bounded reap: never block on a hung child
    done, st = os.waitpid(pid, os.WNOHANG)
    if done:
        status, exited = st, True
        break
    time.sleep(0.1)
if not exited:
    os.kill(pid, signal.SIGKILL)
    os.waitpid(pid, 0)

text = out.decode("utf-8", "replace")
open("/tmp/cspace-tui-smoke.txt", "w").write(text)
print("exit:", os.waitstatus_to_exitcode(status) if exited else "HUNG (killed)")
for needle in ("dashcheck", "attach", "daemon"):
    print(needle, "->", needle in text)
PY
cd /Users/elliott/Projects/cspace-control-plane-3 && python3 /tmp/cspace-tui-smoke.py
```
Expected: `exit: 0`, and `True` for all three needles — the sandbox is in the sidebar, the footer offers attach, and the tabs line reports the daemon. `exit: HUNG (killed)` means `q` never reached `tea.Quit`; a non-zero exit is a bug in Task 5's `View`/`Init`. Either way `/tmp/cspace-tui-smoke.txt` holds everything the dashboard painted.

- [ ] **Step 5: Read the dashboard by eye**

Run `./bin/cspace-go tui` in a normal terminal and check, against the sandbox you just booted:

- the project header, `dashcheck` beneath it with a glyph, its compose sidecars nested and dimmed, and the project's `browser (shared)` row;
- the detail band on the right: `running`, an uptime that grows, memory as `<used>/<cap>` (the used half appears within ten seconds, when the first slow tick lands), the agent's session and last event;
- the footer offering attach / send / down / browser restart, and **not** offering interrupt while the agent is idle;
- `?` opens the binding list and names `~/.cspace/config.json`; `?` closes it.

- [ ] **Step 6: Verify `Ports` against the CLI**

With a dev server running inside the sandbox (in another terminal: `./bin/cspace-go attach dashcheck`, then `npm run dev` or any listener), compare:

```bash
./bin/cspace-go ports dashcheck
```
Expected: within ten seconds the dashboard's sidebar shows the same ports beneath `dashcheck` and the detail band shows the same URLs. Cmd-click (or the terminal's equivalent) on a sidebar port row opens that URL — that is the OSC 8 hyperlink.

- [ ] **Step 7: Verify the interactive state glyph**

With `./bin/cspace-go attach dashcheck` open in a second terminal, send Claude a prompt and watch the dashboard's glyph:

Expected: `●` while it works, `▲` if it asks a question or a permission, `○` when it settles. Cross-check the file it comes from:
```bash
cat ~/.cspace/sessions/cspace/dashcheck/agent-state.json
```
Expected: the same state, updated within a second of the glyph changing.

- [ ] **Step 8: Verify send, interrupt and the event tail**

In the dashboard, select `dashcheck`, press `m`, type `list three files in /workspace`, press Enter.
Expected: the footer shows `send…` then `send ok`; within two seconds the detail band's agent line reads `working` and new lines appear under `recent events`. While it is working the footer now offers interrupt — press `i`.
Expected: `interrupt ok`, and the agent line returns to `idle`. Press `i` again while idle: the key is not offered and nothing happens.

- [ ] **Step 9: Verify the browser restart**

Select the `browser (shared)` row and press `b`.
Expected: the footer spins on `browser restart…` and reports `browser restart ok` within about a minute; the row's glyph returns to `✓` and the detail band shows a `Chrome/…` version.

- [ ] **Step 10: Verify attach and its detach bookkeeping**

Select `dashcheck`, press Enter.
Expected: the dashboard disappears and an interactive `claude` takes the terminal. In another terminal:
```bash
container exec cspace-cspace-dashcheck tmux list-clients -t cspace-claude -F '#{client_tty}'
ls ~/.cspace/controlplane/cspace/dashcheck/
```
Expected: one tty, and one `cspace-claude.<tty>.json` record beside `attach.lock`.

Exit the session (`/exit`).
Expected: the dashboard comes back, the footer reads `attach ok`, the client list is now empty and the record file is gone.

- [ ] **Step 11: Verify boot from the dashboard, including another project**

In the dashboard, tear down nothing — instead boot a second sandbox from the registry. First create a stopped entry to select:
```bash
./bin/cspace-go up dashcheck2 --no-attach && ./bin/cspace-go down dashcheck2 --keep-state
```
Expected: `dashcheck2` appears in the sidebar with `✕`. Select it and press `u`.
Expected: the footer spins on `up…` for the length of a boot and then reports `up ok`; the row turns `○`/`●` and gains its ports.

Then prove the cross-project path: quit the dashboard, `cd` to a different cspace project you have booted at least once, and run `cspace tui` from there. Select a **cspace** sandbox row that is stopped and press `u`.
Expected: it boots — `control.Up` resolved this checkout's root out of the registry rather than the cwd. Before Task 1 this was impossible.

- [ ] **Step 12: Verify the degraded paths**

Stop the daemon and watch the dashboard:
```bash
./bin/cspace-go daemon stop
```
Expected: the tabs line's right-hand side turns to `daemon unreachable` within two seconds, and the sidebar keeps every row. Bring it back the way `cspace up` does — there is no `daemon start`; the subcommands are `serve` (hidden), `status` and `stop` — with `./bin/cspace-go daemon serve >/dev/null 2>&1 &`, or simply run any `./bin/cspace-go up …`, which respawns it. The tabs line goes back to `daemon <version>` within two seconds.

Then stop a sandbox behind the dashboard's back:
```bash
container stop cspace-cspace-dashcheck2
```
Expected: within two seconds that row's glyph turns `✕`, its ports disappear, the footer stops offering attach/send/down for it and starts offering `u`.

- [ ] **Step 13: Verify a keybinding override**

Run (an existing user config is backed up first, so this cannot clobber one):
```bash
[ -f ~/.cspace/config.json ] && cp ~/.cspace/config.json ~/.cspace/config.json.bak
mkdir -p ~/.cspace && cat > ~/.cspace/config.json <<'JSON'
{"tui": {"keys": {"attach": ["o"], "help": ["f1", "?"]}}}
JSON
./bin/cspace-go tui
```
Expected: the footer shows `o attach`, `o` attaches, Enter no longer does, and both `?` and `f1` open the help. The key names are whatever `KeyPressMsg.String()` prints, which for named keys is always lowercase — `"F1"` would match nothing at all. Restore the backup if one was made (`[ -f ~/.cspace/config.json.bak ] && mv ~/.cspace/config.json.bak ~/.cspace/config.json`), otherwise remove the file (`rm ~/.cspace/config.json`).

- [ ] **Step 14: Verify the teardown confirmation, then tear down**

In the dashboard, select `dashcheck2`, press `d`.
Expected: the main area shows `Tear down dashcheck2? Its clone, sessions and volumes go with it.` with `Keep it` selected. Press `esc`: nothing happens. Press `d` then `y`.
Expected: the footer reports `down ok` and the row disappears within two seconds, with the selection landing on a neighbouring row rather than jumping to the top.

Then clean up:
```bash
./bin/cspace-go down dashcheck
rm -rf ~/.cspace/controlplane/cspace/dashcheck ~/.cspace/controlplane/cspace/dashcheck2
rm -f /tmp/cspace-tui-smoke.py /tmp/cspace-tui-smoke.txt
```
Expected: both sandboxes gone; `./bin/cspace-go tui` shows no `dashcheck` rows.

- [ ] **Step 15: Commit (only if something needed fixing)**

If Steps 1-14 all passed there is nothing to commit — say so and stop. If a fix was needed, commit it against the task that owns the file:

```bash
cd /Users/elliott/Projects/cspace-control-plane-3
git add -A
git commit -m "Fix <what the dashboard verification found>"
```

---

## Self-review notes

Checked against the spec's rollout step 3 scope — its Architecture (`internal/controlplane`), Data flow and cadence, Input, Error handling and Testing sections — plus the step-2 review's carry-forwards.

- **Layout** — Task 5's `View`: the fixed 24-column sidebar (`sidebarWidth`, Task 4), a tabs row, the main area, a one-line footer. The tabs row is "reserved but empty-or-hidden until step 4" in the spec; it is reserved and *occupied* here, by the selection's title and daemon health, because an empty line above the main area reads as a rendering bug rather than as a promise. The geometry beneath it is the one step 4 inherits. Task 8 Step 10 writes both this and the detail band's placement back into the spec, so step 4 is planned from a document that describes the code. lipgloss v2's `Width` is the whole block including its border, so `styleSidebar` carries `sidebarWidth` (not `sidebarInner`); `TestSidebarStyleIsExactlyTheDesignsWidth` locks that, because getting it wrong wraps every row and doubles the sidebar's height.
- **Sidebar rows and glyph precedence** — Task 4, `stateGlyph`, with the spec's order (lifecycle, then interactive, then supervisor) and `!` for degraded, tested as a table. Two cases the spec leaves open are ruled here: an interactive `starting` shows `◐`, and an interactive `exited` falls through to the supervisor, because the person's session ending says nothing about the headless agent.
- **Detail band** — Task 4's `renderDetail`, in the main area. The spec says "under the sidebar"; with no panes there is nothing in the main area to compete with it, and 24 columns cannot hold a URL. The renderer takes its width as a parameter precisely so step 4 can move it under the sidebar without a rewrite; the resolution is recorded in `mainArea`'s comment and, by Task 8 Step 10, in the spec itself.
- **Widgets** — `bubbles/v2` `key` + `help` (Tasks 3, 6), `spinner` and `textinput` (Tasks 5, 6), `huh/v2` `Confirm` inside a one-field form (Task 6), lipgloss v2 `Hyperlink` for port URLs (Task 4, asserted on raw output). The `tree` question is decided in the Global Constraints, with the source-level reason: `tree.Model`'s cursor cannot skip a non-selectable node. `viewport` is not used in step 3 — the sidebar windows itself and the detail band is bounded; it arrives with the supervisor view in step 4.
- **Configurable bindings** — Task 3: `tui.keys` in `~/.cspace/config.json` over `lib/defaults.json`, resolved into `bubbles/v2/key` bindings, with the leader declared but not dispatched. The user-level layer is new; it has to be, since `cspace tui` spans projects and `config.Load` needs a git root.
- **Cadence** — Task 5, three tickers at the spec's 1 s / 2 s / 10 s with the spec's split of queries, each with its own in-flight guard and re-arm. "Snapshot without stats" is what Task 2's `SnapshotOpts` exists for. Memory usage is carried forward between slow ticks so the stats-free snapshots do not blank the column.
- **Degrade, never blank** — Task 5's `applySnapshot` keeps the last-known rows on a failed `container ls` and the footer says how stale they are; a failed `Ports` degrades one line of the band and a failed `Events` read degrades the other (`renderDetail` takes both errors, so neither failure can read as "nothing to show"); an unreachable supervisor is `AgentStatus{Reachable:false}`, not an error. Polling itself pauses only for a modal and for attach — the one action that takes the terminal away — so a ten-minute `up` does not freeze the host's rows while it runs.
- **Input** — Task 6, the sidebar-focused subset: arrows/`j`/`k`, Enter attaches, `d` down with the confirmation, `b` browser restart, `u` up, `?` help, plus `m` send and `i` interrupt so the dashboard keeps every action the CLI has. Neither `s` nor `a` is bound: the spec reserves them for the shell pane and the supervisor view, and `tui.keys` ships in `lib/defaults.json`, so a default taken back in step 4 would be a user-visible breaking change. `m` carries send for the same reason. Leader, panes, mouse and image paste are out of scope and none of them is implemented.
- **Error handling** — exec failure surfaces as the attach result (Task 7, the probe test); the no-tmux fallback surfaces as a sticky footer warning naming `cspace image build`, carried out of the suspended program by `ResultWarn` (Task 5's message, Task 7's `attachResult`, tested on both sides); an unreachable supervisor disables send and interrupt through `forRow` rather than failing on press (Tasks 3 and 6, both tested); poll failure degrades and keeps the last good values (Task 5).
- **Testing** — model tests are messages in, state and rendered text out (Tasks 4-6). Full-view assertions check geometry (exact line count, no line wider than the window, sidebar left, footer last) and content by substring rather than one byte-exact golden string: the padding of a styled two-column layout is not a fact worth locking, and a golden that only a rerun can produce would be a placeholder in this document. The sidebar and detail renderers *are* compared as text, through `plain()`. Two of the widget facts the tests lean on were read out of the pinned modules rather than assumed: lipgloss v2 sizes a block including its border, and huh answers a `Confirm` over two command round trips (`NextField` → `nextGroup`), which is why Task 6's teardown tests pump with `answer()` rather than `step()`. Tab lifecycle has no test because there are no tabs yet — the placeholder is the tabs line itself, asserted by `TestViewGeometry`'s line count; exited-pane rendering is step 4's, as the spec says.
- **Carry-forwards** — `AgentStatus` on the 1 s ticker (Task 5's comment on the fast budget); 409-as-success preserved and tested (Task 7); `Ports` gated on running/degraded (Task 5, `sandboxTargets`, tested); `Up`/`Ports`/`InteractiveState`/`Events` exercised by hand (Task 9); `containerName` stays unexported — the actor uses `Row.Container` and `Client.Tmux()`; the actor uses the `Client`'s tmux driver, which is why `beginAttachOrWarn` grew a parameter, and all four of its callers move together (Task 7). Sandbox-name shape validation is explicitly deferred, with its reason, in the Global Constraints.
- **Attach without panes** — Task 7 runs the v1 flow (`control.ClaudeAttach`/`AttachArgv`, `beginAttachOrWarn`, `Attachment.Close`) inside a `tea.ExecCommand`, so `Present` and `BeginAttach` happen after bubbletea has released the terminal and never on the UI goroutine. `TestControlPlaneActorAttachTouchesNothingInUpdate` is the regression guard.
- **Ordering** — the new dashboard is built in Tasks 3-7 while `internal/tui` still serves `cspace tui`; Task 8 switches `cmd_tui.go` and deletes the old package in one commit. `cspace tui` is never absent from the branch.
- **Findings** — Task 1 resolves the registry project-root finding (with the `(cs-finding:…)` trailer); Task 4 resolves the row-list scrolling finding, which the new sidebar's windowing closes. The two behaviours the step-2 plan preserved (`down`'s warning-as-failure, `Correlate`'s registry-derived projects) are untouched.
