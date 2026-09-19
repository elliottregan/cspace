# Panes, part B: panes in the dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn `cspace tui` into the window the control-plane design describes: real tabs over live panes, a focus model, the leader key and its second keys, a supervisor view, and the detach protocol on every pane open, pane close and quit — replacing step 3's suspend-the-program attach.

**Architecture:** `internal/controlplane` grows a tab list over `internal/pane`. Each tab is either a pane (a Claude session, a shell, or a host shell) or the supervisor view, which is a viewport over `control.Events` and not a PTY at all. Panes are opened through a `PaneHost` seam declared here and satisfied in `internal/cli` — the same shape as the existing `Data` and `Actor` seams — so this package never imports `internal/cli` and `internal/pane` never learns what a sandbox is. The detach protocol rides on `control.BeginAttach`/`Attachment.Close`, which rollout step 2 already shipped, plus a startup sweep this plan adds to `internal/control`.

**Tech Stack:** Go 1.26; `charm.land/bubbletea/v2 v2.0.9`, `lipgloss/v2 v2.0.6`, `bubbles/v2 v2.2.1` (`key`, `help`, `spinner`, `textinput`, `textarea`, `viewport`), `huh/v2 v2.0.3`, `charm.land/glamour/v2 v2.0.1` (new, this plan); `internal/pane` and `internal/control`.

**Spec:** `docs/superpowers/specs/2026-09-17-control-plane-design.md` — this plan implements the dashboard half of **rollout step 4, "Panes"**. The engine half is `docs/superpowers/plans/2026-09-18-control-plane-4a-pane-engine.md` and **must land first**: every task here consumes something it produces.

## Global Constraints

- **Scope is rollout step 4's dashboard half.** No mouse and no image paste: those are step 5. The `v` image-paste binding is *declared* (so the config shape is stable and the footer can name it) and deliberately **not dispatched**. No `cspace ports` / `control.Ports` convergence, no second tmux driver cleanup, no panes for sidecars.
- **4a first.** This plan starts from a tree where `internal/pane` exists with `Open`, `Command`, `HostShell`, `KeyEvent`, `KeyMod` and `Mod*`; `control.ShellAttach` exists; `registry.MarkStopped` exists; and `scripts/tui-smoke/` exists.
- **Dependency direction**, verified with `go list -deps` in Task 2:
  - `internal/pane` imports **neither** `internal/controlplane` **nor** `internal/cli`.
  - `internal/controlplane` imports `internal/pane` and `internal/control` only.
  - `internal/control` imports none of them.
- **Exact module pins.** This plan adds exactly one module:
  - `charm.land/glamour/v2 v2.0.1` — read against the proxy on 2026-09-18: its own go.mod asks for `lipgloss/v2 v2.0.4`, `x/ansi v0.11.7`, `go-colorful v1.4.0`, `x/text v0.24.0`, all **below** what this repo already pins, so **no version moves**. It brings `alecthomas/chroma/v2 v2.14.0`, `dlclark/regexp2 v1.11.0`, `microcosm-cc/bluemonday v1.0.27`, `aymerick/douceur v0.2.0`, `gorilla/css v1.0.1`, `yuin/goldmark v1.7.8`, `yuin/goldmark-emoji v1.0.5` and `charmbracelet/x/exp/slice` as new indirects. It has **no** dependency on bubbletea/bubbles/huh.
  - Already present and unchanged: `charm.land/bubbletea/v2 v2.0.9`, `charm.land/lipgloss/v2 v2.0.6`, `charm.land/bubbles/v2 v2.2.1`, `charm.land/huh/v2 v2.0.3`, `github.com/charmbracelet/x/ansi v0.11.8`, `github.com/charmbracelet/x/vt v0.0.0-20260913004009-c615ff2f7805`, `github.com/creack/pty v1.1.24`, `github.com/charmbracelet/ultraviolet v0.0.0-20260811164956-006e29f97886`. `github.com/charmbracelet/bubbletea v1.3.10`, `github.com/charmbracelet/bubbles v1.0.0` and `github.com/charmbracelet/lipgloss v1.1.0` stay for `internal/overlay`, which is still on the v1 line.
- **Concurrency rules:** nothing outside the pane engine touches an emulator except through `*pane.Pane`'s methods; `Render`, `Cursor` and `ScrollbackView` are called only from the UI goroutine (i.e. only from `View`); the writer channel is bounded and never blocks the UI. No `PaneHost` method is called from `Update` — every one of them goes through a `tea.Cmd`, because opening a pane probes a container and takes a file lock.
- **glamour/v2 API facts this plan relies on** (read from the module source, not from memory): the renderer is `glamour.NewTermRenderer(opts ...TermRendererOption) (*TermRenderer, error)` and the render call is the method `(*TermRenderer).Render(in string) (string, error)`. `WithAutoStyle` and `WithColorProfile` are **gone in v2**; the style comes from `WithStandardStyle(styles.DarkStyle)` and the width from `WithWordWrap(n)`. Word wrap is construction-time only — there is no setter — so **a resize rebuilds the renderer**.
- **bubbles/v2 API facts this plan relies on:** `viewport.New(viewport.WithWidth(w), viewport.WithHeight(h))` — not `New(w, h)`; size is `SetWidth`/`SetHeight`/`Width()`/`Height()`, there are no exported `Width`/`Height` fields; `YOffset()` is a method, not a field; `GotoBottom()` returns `[]string`; `Model.View()` returns `string`, not `tea.View`. `textarea.New()` takes no options, `Focus()` returns a `tea.Cmd`, and its default `KeyMap.InsertNewline` is bound to `enter` — it must be cleared for Enter to submit.
- **Always build through `make`.** `internal/assets/embedded/` is gitignored and populated by `make sync-embedded`; Task 1 adds keys to `lib/defaults.json`, which only reaches the binary through that sync.
- **`make check` must be green after every task**, and **`cspace tui` must start, render and keep every step-3 action working after every task**: the pane machinery lands behind the existing dashboard (Tasks 1-2) before the layout switches over (Task 3). One capability is deliberately down in between — **between Task 2 and Task 7, opening a pane (`Enter`, `s`, `a`) returns the footer error `open pane failed: no pane host configured`**, because Task 2 retires the old suspend-the-program attach and the real pane host only lands in Task 7. Nothing else regresses: the row list, the detail band, send, interrupt, teardown, boot, browser restart and quit all keep working, and the error is a footer notice rather than a crash. `make test-race` (4a's target) must stay clean after Tasks **2, 4 and 7** — the three that add a concurrent owner of a pane — and Task 2 widens that target to `./internal/pane/... ./internal/controlplane/...`, because what those three tasks can race is the tab bookkeeping, which the engine-only scope could never see. Each of those tasks' final step runs it.
- **Preserve these known behaviours; do not "fix" them:**
  - `.cspace/context/findings/2026-07-20-tui-down-reports-benign-teardown-warnings-as-failure.md` — `control.Down` reports any `warning:` text as failure. Unchanged.
  - `.cspace/context/findings/2026-07-20-tui-browser-row-orphaned-when-project-has-no-registry-entry.md` — `Correlate` derives projects from registry entries only. Unchanged.
- **`cspace attach` is unchanged.** It keeps its own foreground-child flow, its signal handling and `restoreBlockingStreams`. Only the *dashboard's* attach — `cpActor.Attach` and `attachExec` — is replaced, by opening a Claude pane (Task 7).
- **Worktree:** `/Users/elliott/Projects/cspace-control-plane-4b`, branch `control-plane-4b-panes`, stacked on the final head of `control-plane-4a-pane-engine` (plan 4a's branch, in the worktree `/Users/elliott/Projects/cspace-control-plane-4`). Create it from that head, not from `main`: every task here consumes something 4a produced. Do not touch `/Users/elliott/Projects/cspace` or the sibling worktrees.
- **Module path:** `github.com/elliottregan/cspace`. Commit messages are short imperative sentences, ending with the two-line trailer this branch's commits carry:

  ```
  Co-Authored-By: <the model doing the work> <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01W64zstC3PZojywARTtnSW7
  ```

---

## File Structure

New files in `internal/controlplane` (all `package controlplane`):

| File | Responsibility | Task |
|---|---|---|
| `panes.go` | `Kind`, `PaneHost`, `Opened`, `Detacher`, the `tab` type, open/focus/close bookkeeping, the pane messages and commands | 2 |
| `view_pane.go` | the tabs row, the focused pane's frame, the exited/failed pane, the empty main area | 3 |
| `leader.go` | leader dispatch, key routing to the focused pane, scroll mode, the new-pane picker | 4 |
| `supervisor.go` | the supervisor view: viewport, glamour, textarea, spinner. Created as a six-method stub in Task 2 so the package compiles; Task 6 replaces the file wholesale | 2 (stub), 6 |

New tests (same package): `panes_test.go` (2), `view_pane_test.go` (3), `leader_test.go` (4), `detach_test.go` (5), `supervisor_test.go` (6).

Existing tests updated in place: `model_test.go` (2, 3, 5, 7), `input_test.go` (1, 2, 4), `internal/cli/controlplane_actor_test.go` (7).


Modified:

| File | Change | Task |
|---|---|---|
| `internal/controlplane/keys.go`, `keys_test.go` | the pane actions and the leader's second keys, `LeaderHelp`, `PaneFullHelp`, `forRow` gates (1); `actionHelp[ActionLeader]` (3); `actionHelp[ActionAttach]`'s description (7) | 1, 3, 7 |
| `lib/defaults.json` | the new `tui.keys` entries | 1 |
| `internal/controlplane/model.go` | the tab list, focus, `PaneHost`, resize fan-out, quit (2); `modePicker` and `picker` (3); `leaderArmed`, key routing, paste (4); the startup sweep (5); the supervisor feed and the per-tab spinner (6); `paused` loses `LabelAttach` (7) | 2, 3, 4, 5, 6, 7 |
| `internal/controlplane/view.go` | `helpView`'s second help row (1); the sidebar column (rows + band), the main area, the footer by focus (3); the leader note in `helpView` (4) | 1, 3, 4 |
| `internal/controlplane/view_detail.go` | the band's header folds at the sidebar's width | 3 |
| `internal/controlplane/input.go` | `Enter`/`s`/`a` open panes; `Tab` focuses the main area; Ctrl+C by focus | 2, 4 |
| `internal/controlplane/actor.go` | `Actor.Attach` and `LabelAttach` removed | 7 |
| `internal/controlplane/panes.go` | `sweepMsg`/`sweepCmd` join the other pane messages | 5 |
| `internal/controlplane/styles.go` | the three tab styles | 3 |
| `internal/control/events.go`, `events_test.go` | `EventLine.Text` and `.Tools` | 6 |
| `internal/control/attach.go`, `attach_test.go` | `SweepClientRecords`, `SweepResult` | 5 |
| `internal/control/client.go` | the `processAlive` seam | 5 |
| `internal/cli/controlplane_panes.go` | **New.** `paneHost` | 7 |
| `internal/cli/controlplane_actor.go`, `controlplane_actor_test.go` | `Attach`/`attachExec`/`attachResult`/`attachRunErr` deleted | 7 |
| `internal/cli/cmd_tui.go` | `nil` host (2), then the real one (7) | 2, 7 |
| `Makefile` | `test-race` widens to `./internal/pane/... ./internal/controlplane/...` | 2 |
| `CLAUDE.md` | the `make test-race` line (2); the controlplane bullet (7) | 2, 7 |
| the spec | the step-4 layout, main-area and rollout notes | 7 |

---

### Task 1: The pane bindings and the leader's second keys

Step 3 shipped the leader binding declared but undispatched, and deliberately left `s` and `a` unbound because taking a default back later would be a breaking config change. This task binds them and adds the leader's second keys, all through the same `tui.keys` mechanism, so the drift test keeps `lib/defaults.json` and the Go fallback honest.

**Files:**
- Modify: `internal/controlplane/keys.go`, `internal/controlplane/view.go` (`helpView`), `lib/defaults.json`
- Test: `internal/controlplane/keys_test.go`, `internal/controlplane/input_test.go` (`TestHelpOverlayToggles`)

**Interfaces:**
- Consumes: `KeyMap`, `NewKeyMap`, `defaultKeys`, `actionHelp`, `forRow`, the `Action*` constants, `helpView` (step 3).
- Produces:
  - action names `ActionShell = "shell"`, `ActionSupervisor = "supervisor"`, `ActionFocusMain = "focusMain"`, `ActionFocusSidebar = "focusSidebar"`, `ActionNextTab = "nextTab"`, `ActionPrevTab = "prevTab"`, `ActionNewPane = "newPane"`, `ActionClosePane = "closePane"`, `ActionScroll = "scroll"`, `ActionLive = "live"`, `ActionPasteImage = "pasteImage"`
  - `KeyMap` fields `Shell`, `Supervisor`, `FocusMain`, `FocusSidebar`, `NextTab`, `PrevTab`, `NewPane`, `ClosePane`, `Scroll`, `Live`, `PasteImage`
  - `func (k KeyMap) LeaderHelp() []key.Binding` — the footer when the main area has focus
  - `func (k KeyMap) PaneFullHelp() [][]key.Binding` — the help overlay's second block, rendered as its own `FullHelpView` row (`FullHelp` itself is unchanged)
  - `canShell(r control.Row) bool`, `canSupervisor(r control.Row) bool`

- [ ] **Step 1: Write the failing tests**

Add to `internal/controlplane/keys_test.go`, and extend `bindingFor` with one `case` per new action (returning the matching field):

```go
func TestPaneBindingsHaveDefaults(t *testing.T) {
	k := NewKeyMap(nil)
	want := map[string][]string{
		ActionShell:        {"s"},
		ActionSupervisor:   {"a"},
		ActionFocusMain:    {"tab"},
		ActionFocusSidebar: {"h"},
		ActionNextTab:      {"n"},
		ActionPrevTab:      {"p"},
		ActionNewPane:      {"t"},
		ActionClosePane:    {"x"},
		ActionScroll:       {"["},
		ActionLive:         {"g"},
		ActionPasteImage:   {"v"},
	}
	for action, keys := range want {
		if got := bindingFor(k, action).Keys(); !reflect.DeepEqual(got, keys) {
			t.Errorf("%s keys = %v, want %v", action, got, keys)
		}
	}
	// The leader must not be ctrl+b: Claude Code uses it to background a task.
	for _, s := range k.Leader.Keys() {
		if s == "ctrl+b" {
			t.Error("the leader is ctrl+b, which Claude Code owns")
		}
	}
}

func TestLeaderHelpNamesTheSecondKeys(t *testing.T) {
	k := NewKeyMap(nil)
	var descs []string
	for _, b := range k.LeaderHelp() {
		descs = append(descs, b.Help().Key+" "+b.Help().Desc)
	}
	joined := strings.Join(descs, " · ")
	for _, want := range []string{"h ", "n ", "t ", "x ", "[ ", "v "} {
		if !strings.Contains(joined, want) {
			t.Errorf("leader help %q is missing %q", joined, want)
		}
	}
}

// The supervisor view reads events.ndjson from the host's session directory,
// which outlives the container — so a sandbox stopped with
// `cspace down --keep-state` still has a history worth opening, and
// canSupervisor is deliberately wider than canAttach and canShell. This is
// its own test rather than a row in the table below because the reason is
// the whole point of the predicate.
func TestSupervisorStaysOpenableOnAStoppedSandbox(t *testing.T) {
	stopped := control.Row{Kind: control.RowSandbox, State: control.StateStopped}
	if !NewKeyMap(nil).forRow(stopped, liveState{}).Supervisor.Enabled() {
		t.Error("supervisor is off for a stopped sandbox")
	}
}
```

(add `"strings"` to the file's imports.)

The rest of the gating is what `TestForRowDisablesWhatTheSelectionCannotDo`
(keys_test.go:108) is already a table for, so it goes in there rather than
into a second test asking the same question a second way. Add four rows to
its `cases` slice, beside the `attach` ones it already has — the fixtures
`working`, `stopped` and `browser` are declared at the top of that test:

```go
		{"shell in a running sandbox", working, liveState{}, ActionShell, true},
		{"shell in a stopped sandbox", stopped, liveState{}, ActionShell, false},
		{"shell on the browser row", browser, liveState{}, ActionShell, false},
		{"supervisor on the browser row", browser, liveState{}, ActionSupervisor, false},
```

`TestDefaultsJSONMatchesTheBuiltInKeys` already compares the whole map, so it fails until `lib/defaults.json` is updated too — that is the point.

- [ ] **Step 2: Run to verify they fail**

Run: `cd /Users/elliott/Projects/cspace-control-plane-4b && go test ./internal/controlplane/ -run 'TestPaneBindings|TestLeaderHelp|TestSupervisorStaysOpenable|TestForRowDisables|TestDefaultsJSON'`
Expected: FAIL to build — `undefined: ActionShell` and friends.

- [ ] **Step 3: Add the actions and the bindings**

In `internal/controlplane/keys.go`, extend the constant block:

```go
	// The pane actions. The first two are sidebar keys the design reserved
	// in step 3 and step 4 now binds; the rest are the leader's second keys,
	// declared as ordinary actions so one config mechanism covers every
	// binding in the program.
	ActionShell        = "shell"
	ActionSupervisor   = "supervisor"
	ActionFocusMain    = "focusMain"
	ActionFocusSidebar = "focusSidebar"
	ActionNextTab      = "nextTab"
	ActionPrevTab      = "prevTab"
	ActionNewPane      = "newPane"
	ActionClosePane    = "closePane"
	ActionScroll       = "scroll"
	ActionLive         = "live"
	ActionPasteImage   = "pasteImage"
```

Extend `defaultKeys` — and replace its comment, which explains why `s` and `a` were left unbound:

```go
// defaultKeys is the built-in keystroke list per action, and must stay
// identical to lib/defaults.json's tui.keys (keys_test.go locks the two
// together). Go carries it as well as the JSON so a binary whose embedded
// assets are missing an action still has a working dashboard.
//
// The leader's second keys are ordinary actions here rather than a nested
// map: one mechanism covers every binding, `tui.keys` stays flat, and
// key.Matches works the same in both contexts. `?` and `q` are deliberately
// shared between the sidebar and the leader — the design lists them under
// both, and one binding means one label in both footers.
var defaultKeys = map[string][]string{
	ActionMoveUp:         {"up", "k"},
	ActionMoveDown:       {"down", "j"},
	ActionAttach:         {"enter"},
	ActionShell:          {"s"},
	ActionSupervisor:     {"a"},
	ActionSend:           {"m"},
	ActionInterrupt:      {"i"},
	ActionTeardown:       {"d"},
	ActionBrowserRestart: {"b"},
	ActionBoot:           {"u"},
	ActionRefresh:        {"r"},
	ActionHelp:           {"?"},
	ActionQuit:           {"q"},
	ActionLeader:         {"ctrl+space"},
	ActionFocusMain:      {"tab"},
	ActionFocusSidebar:   {"h"},
	ActionNextTab:        {"n"},
	ActionPrevTab:        {"p"},
	ActionNewPane:        {"t"},
	ActionClosePane:      {"x"},
	ActionScroll:         {"["},
	ActionLive:           {"g"},
	ActionPasteImage:     {"v"},
}
```

Extend `actionHelp`:

```go
	ActionShell:        {"s", "shell pane"},
	ActionSupervisor:   {"a", "supervisor"},
	ActionFocusMain:    {"tab", "focus pane"},
	ActionFocusSidebar: {"h", "sidebar"},
	ActionNextTab:      {"n", "next tab"},
	ActionPrevTab:      {"p", "prev tab"},
	ActionNewPane:      {"t", "new"},
	ActionClosePane:    {"x", "close"},
	ActionScroll:       {"[", "scroll"},
	ActionLive:         {"g", "live"},
	ActionPasteImage:   {"v", "paste image"},
```

Extend the `KeyMap` struct and `NewKeyMap`'s return with the eleven fields, each `binding(Action…)`. Replace `Leader`'s comment:

```go
	// Leader is the prefix for every pane binding. Ctrl+Space by default,
	// and it must not be Ctrl+B — Claude Code uses that to background a
	// task, and the whole point of a pane is that Claude's keys reach it.
	Leader key.Binding
```

Add `LeaderHelp` beside `ShortHelp`:

```go
// LeaderHelp is the footer the main area gets: what the leader's second keys
// do, in the design's order. PasteImage is listed because it is bound and
// the config shape is stable; rollout step 5 is what makes it act.
//
// It is deliberately shorter than the full set of second keys. This is ONE
// line shared with the leader's own label, and help.ShortHelpView elides
// from the right once it runs out of width — so a list that does not fit is
// a list whose tail nobody ever reads. All nine render to about 98 cells,
// which overflows the design's 100-column reference window before the
// leader's seven-cell prefix is even counted; these seven fit in 76. The two
// that give are the ones already advertised elsewhere: PrevTab is in
// PaneFullHelp below, and Help is in FullHelp, which the overlay renders
// first.
func (k KeyMap) LeaderHelp() []key.Binding {
	return []key.Binding{k.FocusSidebar, k.NextTab, k.NewPane,
		k.ClosePane, k.Scroll, k.PasteImage, k.Quit}
}
```

Leave `FullHelp` **as it is** and add a second method beside it:

```go
// PaneFullHelp is the help overlay's second block: the sidebar keys that
// open a pane, and the leader's second keys.
//
// It is a separate method rather than three more columns on FullHelp
// because help.FullHelpView drops whole columns once their total exceeds
// its width and appends an ellipsis. The overlay renders into the main
// area — 74 columns at a 100-column window — and FullHelp's four columns
// already fill that, so a fifth and sixth would never be drawn at any
// realistic size. helpView renders this as its own row instead, which
// gives it the width back.
func (k KeyMap) PaneFullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Shell, k.Supervisor, k.FocusMain},
		{k.FocusSidebar, k.NextTab, k.PrevTab},
		{k.NewPane, k.ClosePane, k.Scroll, k.Live},
	}
}
```

Extend `forRow` and add the two predicates:

```go
	k.Shell.SetEnabled(canShell(row))
	k.Supervisor.SetEnabled(canSupervisor(row))
```

```go
// canShell mirrors canAttach: a shell pane runs inside the container, so
// there has to be one.
func canShell(r control.Row) bool {
	return r.Kind == control.RowSandbox && r.State != control.StateStopped
}

// canSupervisor is wider than the other two on purpose. The supervisor view
// reads events.ndjson from the host's session directory, which survives the
// container — so a sandbox stopped with `cspace down --keep-state` still has
// a readable history, and reading it is often exactly what a person wants
// before deciding whether to boot it again.
func canSupervisor(r control.Row) bool { return r.Kind == control.RowSandbox }
```

- [ ] **Step 4: Show the pane bindings in the help overlay**

A method nothing renders is not a binding a person can find. First the
assertion, in `internal/controlplane/input_test.go`'s existing
`TestHelpOverlayToggles`, beside the two it already makes — the window there
is 100 columns, which is the width the column-dropping happens at:

```go
	if !strings.Contains(out, "shell pane") {
		t.Errorf("the help overlay should list the pane bindings too:\n%s", out)
	}
```

Run it and watch it fail. Then, in `internal/controlplane/view.go`'s
`helpView`, render `PaneFullHelp` as a second row of its own inside `lines`,
immediately after the existing `h.FullHelpView(...)`:

```go
		h.FullHelpView(m.keys.FullHelp()),
		"",
		styleDim.Render(fit("panes", width)),
		h.FullHelpView(m.keys.PaneFullHelp()),
```

Two `FullHelpView` calls, not one wider one: each gets the full `width`
budget, so neither drops a column. The rest of `helpView` — the heading, the
three dim notes — is unchanged.

- [ ] **Step 5: Update `lib/defaults.json`**

Add the eleven entries to `tui.keys`, in the same order as `defaultKeys` —
which interleaves `shell`/`supervisor` right after `attach` rather than
appending all eleven at the end — so the two lists read as one. Replace the
whole `tui.keys` object with:

```json
    "keys": {
      "moveUp": ["up", "k"],
      "moveDown": ["down", "j"],
      "attach": ["enter"],
      "shell": ["s"],
      "supervisor": ["a"],
      "send": ["m"],
      "interrupt": ["i"],
      "teardown": ["d"],
      "browserRestart": ["b"],
      "boot": ["u"],
      "refresh": ["r"],
      "help": ["?"],
      "quit": ["q"],
      "leader": ["ctrl+space"],
      "focusMain": ["tab"],
      "focusSidebar": ["h"],
      "nextTab": ["n"],
      "prevTab": ["p"],
      "newPane": ["t"],
      "closePane": ["x"],
      "scroll": ["["],
      "live": ["g"],
      "pasteImage": ["v"]
    }
```

(`TestDefaultsJSONMatchesTheBuiltInKeys` compares maps, so order cannot fail
it. This is for whoever reads the two side by side.)

- [ ] **Step 6: Run the tests**

Run: `cd /Users/elliott/Projects/cspace-control-plane-4b && make test`
Expected: PASS, including `TestDefaultsJSONMatchesTheBuiltInKeys` — which only passes once the JSON and the Go map agree exactly — and `TestHelpOverlayToggles`, whose new assertion is what proves the second help row survives the overlay's width. (Run it through `make`, not `go test`: the drift test reads the *embedded* defaults, which `make sync-embedded` populates.)

- [ ] **Step 7: Run the gate and commit**

```bash
cd /Users/elliott/Projects/cspace-control-plane-4b
make check
git add internal/controlplane lib/defaults.json
git commit -m "$(cat <<'EOF'
Bind the pane keys and the leader's second keys

s and a were reserved in step 3 and left unbound because taking a shipped
default back would be a breaking config change; they are bound now, together
with the leader's second keys, as ordinary tui.keys actions so one mechanism
covers every binding. The image-paste key is declared and not yet dispatched.

Co-Authored-By: <model> <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01W64zstC3PZojywARTtnSW7
EOF
)"
```

---

### Task 2: Tabs, the `PaneHost` seam, and opening a pane

The tab list and its bookkeeping, with no layout change yet: after this task `Enter`, `s` and `a` open panes that exist, are tracked, redraw, and close — but the main area still shows the detail band. Task 3 is what puts them on screen. Splitting it this way means the state machine is testable before any golden view depends on it, and `cspace tui` keeps starting, rendering and doing everything step 3 did throughout.

**One capability goes down here and comes back in Task 7.** Step 5 removes the `m.actor.Attach(row)` dispatch and points `Enter`/`s`/`a` at a `PaneHost` that `cmd_tui.go` does not yet supply, so from this task until Task 7 those three keys report `open pane failed: no pane host configured` in the footer. That is the deliberate cost of landing the seam before its implementation; Task 7 Step 5 closes the gap by passing the real `newPaneHost(ctrl, home)`.

**Files:**
- Create: `internal/controlplane/panes.go`; `internal/controlplane/supervisor.go` (the stub, Step 7)
- Test: `internal/controlplane/panes_test.go`
- Modify: `internal/controlplane/model.go`, `internal/controlplane/input.go`, `internal/cli/cmd_tui.go` (Step 6), `Makefile` and `CLAUDE.md` (Step 9 widens `test-race` and re-describes it)
- Test (existing, updated): `internal/controlplane/model_test.go` (`newTestModelWithHost`, and all four `New(` call sites), `internal/controlplane/input_test.go` (`TestAttachAndInterruptDispatch` splits)

**Interfaces:**
- Consumes: `pane.Open`, `pane.Command`, `pane.HostShell`, `*pane.Pane` (4a); `control.Row`; `Model`, `startAction`, `Result` (step 3).
- Produces:
  - `type Kind int` with `KindClaude`, `KindShell`, `KindHostShell`, `KindSupervisor`, and `func (k Kind) String() string` → `"claude" | "shell" | "host shell" | "supervisor"`
  - `type Detacher interface { Close(ctx context.Context) error }`
  - `type Opened struct { Pane *pane.Pane; Detach Detacher; Warning string }`
  - `type PaneHost interface { Open(ctx context.Context, kind Kind, row control.Row, cols, rows int) (Opened, error); Sweep(ctx context.Context) (int, error) }`
  - `func New(data Data, actor Actor, host PaneHost, keys KeyMap) Model` — **`New` grows a parameter**; `internal/cli` is updated in Task 7 and a `nopPaneHost` keeps the tests compiling until then
  - `LabelOpenPane = "open pane"`, `LabelClosePane = "close pane"`
  - on `Model`: `openOrFocus(kind Kind, row control.Row) (tea.Model, tea.Cmd)`, `focusedTab() *tab`, `paneSize() (cols, rows int)`

- [ ] **Step 1: Write the failing test**

Create `internal/controlplane/panes_test.go`:

```go
package controlplane

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/pane"
)

// fakeHost opens real panes running a harmless command, so the tab
// bookkeeping is exercised against the real engine without a container.
//
// t is not decoration: every pane it opens is a real child on a real pty
// with four goroutines behind it, and most of these tests never close the
// tab they opened. Registering the close as a cleanup is what internal/pane's
// own openTestPane does (pane_test.go:33), and without it one `go test` run
// of this package leaves a dozen `sleep 30` children and their pty masters
// alive until the test binary exits — under `make test-race`, with a race
// detector watching all of them.
type fakeHost struct {
	t       *testing.T
	opens   []Kind
	rows    []control.Row
	sweeps  int
	openErr error
	warn    string
	// history makes the child print enough lines to fill a scrollback, for
	// the tests that scroll.
	history bool
	// echo makes the child turn the tty's own echo off and print back what
	// it reads, in caret notation. It is how Task 4 proves a keystroke
	// actually reached the child: with a 512-slot queue being drained,
	// Dropped() is zero whether or not a byte was ever sent.
	echo bool
	// exits makes the child die the moment it starts, for the tests that
	// watch a pane end on its own rather than by the operator's key.
	exits bool
}

func (h *fakeHost) Open(_ context.Context, kind Kind, row control.Row, cols, rows int) (Opened, error) {
	h.opens = append(h.opens, kind)
	h.rows = append(h.rows, row)
	if h.openErr != nil {
		return Opened{}, h.openErr
	}
	script := "sleep 30"
	switch {
	case h.history:
		script = "i=0; while [ $i -lt 200 ]; do echo line$i; i=$((i+1)); done; sleep 30"
	case h.echo:
		// raw so a single byte is delivered without waiting for a newline,
		// -echo so what lands on the screen is the child's doing and not
		// the line discipline's, and `cat -v` so a control byte is visible
		// (NUL prints as ^@).
		script = "stty raw -echo; cat -v"
	case h.exits:
		script = "exit 0"
	}
	p, err := pane.Open(pane.Command{Path: "/bin/sh", Args: []string{"sh", "-c", script}}, cols, rows)
	if err != nil {
		return Opened{}, err
	}
	// Close is idempotent (sync.Once), so a tab the test closed itself, or
	// one the dashboard reaped when its child exited, costs nothing here.
	h.t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.Close(ctx)
	})
	return Opened{Pane: p, Detach: &fakeDetacher{}, Warning: h.warn}, nil
}

func (h *fakeHost) Sweep(context.Context) (int, error) { h.sweeps++; return 0, nil }

type fakeDetacher struct{ closed int }

func (d *fakeDetacher) Close(context.Context) error { d.closed++; return nil }

// stepPump delivers a key and runs the command it produced. step() throws
// the command away, which is fine for the keys that only change state and
// useless for the ones whose whole effect is in a command — opening a pane,
// closing one, quitting.
func stepPump(t *testing.T, m Model, k string) Model {
	t.Helper()
	mm, cmd := m.Update(press(k))
	return pump(t, mm.(Model), cmd)
}

// pump runs one Cmd and feeds the messages it produced back into the model.
//
// It goes exactly one level deep, and that is deliberate: the follow-up
// command an open produces is awaitOutput, which blocks until the child's
// next frame — a child running `sleep 30` never has one, and a recursive
// pump would hang on it rather than fail. The three-second ceiling covers
// the same hazard for the command it does run.
func pump(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	done := make(chan []tea.Msg, 1)
	go func() { done <- drain(cmd) }()
	select {
	case msgs := <-done:
		for _, msg := range msgs {
			if msg == nil {
				continue
			}
			mm, _ := m.Update(msg)
			m = mm.(Model)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a command never produced a message")
	}
	return m
}

func TestEnterOpensAClaudePaneAndSecondEnterFocusesIt(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)

	m = stepPump(t, m, "enter")
	mustTabs(t, m, 1)
	if len(h.opens) != 1 || h.opens[0] != KindClaude {
		t.Fatalf("opens = %v, want one KindClaude", h.opens)
	}
	if m.focus != focusMain {
		t.Error("opening a pane did not move focus to the main area")
	}

	// A second Enter on the same row focuses the tab it already has rather
	// than starting a second Claude against one workspace.
	//
	// The focus has to go back to the sidebar first, and not as a
	// convenience: from Task 4 on, a key pressed while the main area has
	// focus goes to the child, so an Enter left pointed at the pane would
	// never reach openOrFocus and both assertions below would hold for the
	// wrong reason. TestShellAndSupervisorOpenTheirOwnTabs does the same.
	m.focus = focusSidebar
	m2 := step(t, m, "enter")
	if len(h.opens) != 1 {
		t.Errorf("opens = %v, want the existing tab focused", h.opens)
	}
	if m2.focused != 0 {
		t.Errorf("focused = %d, want the existing tab", m2.focused)
	}
}

func TestShellAndSupervisorOpenTheirOwnTabs(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = stepPump(t, m, "s")
	mustTabs(t, m, 1)
	m.focus = focusSidebar
	m = stepPump(t, m, "a")
	mustTabs(t, m, 2)

	if got := []Kind{m.tabs[0].kind, m.tabs[1].kind}; got[0] != KindShell || got[1] != KindSupervisor {
		t.Errorf("kinds = %v, want shell then supervisor", got)
	}
	// The supervisor view is not a pane: it runs no process.
	if m.tabs[1].p != nil {
		t.Error("the supervisor tab opened a pty")
	}
	if len(h.opens) != 1 {
		t.Errorf("host opened %d panes, want only the shell", len(h.opens))
	}
}

func TestAFailedOpenBecomesAFooterErrorAndNoTab(t *testing.T) {
	h := &fakeHost{t: t, openErr: errors.New("no container yet")}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = stepPump(t, m, "enter")
	if len(m.tabs) != 0 {
		t.Errorf("tabs = %d, want none after a failed open", len(m.tabs))
	}
	if !m.notice.isErr {
		t.Error("a failed open left no error notice")
	}
	if m.action != "" {
		t.Error("the action gate is still held after a failed open")
	}
}

func TestCloseTabDetachesAndTearsDown(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = stepPump(t, m, "enter")
	mustTabs(t, m, 1)
	d := m.tabs[0].detach.(*fakeDetacher)

	m = pump(t, m, m.closeTab(m.tabs[0].id))
	if len(m.tabs) != 0 {
		t.Errorf("tabs = %d, want none", len(m.tabs))
	}
	if d.closed != 1 {
		t.Errorf("detacher closed %d times, want 1", d.closed)
	}
	if m.focus != focusSidebar {
		t.Error("closing the last tab did not return focus to the sidebar")
	}
}

// TestAnExitedPaneReapsItself is the close nobody presses a key for, and it
// is the commonest one: the child ends on its own. 4a closes Dirty only
// inside Pane.Close, so the waiter's final markDirty is the last signal an
// exited-but-unclosed pane will ever emit — miss it and the tmux client
// stays attached inside the sandbox, its record file stays on the host, and
// the pty master and x/vt's parser buffer stay live until the operator
// happens to press leader x.
func TestAnExitedPaneReapsItself(t *testing.T) {
	h := &fakeHost{t: t, exits: true}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = stepPump(t, m, "enter")
	mustTabs(t, m, 1)
	tb := m.tabs[0]
	d := tb.detach.(*fakeDetacher)

	// The redraw wait the open armed, run by hand: stepPump discards it.
	out, ok := runCmd(t, awaitOutput(tb)).(paneOutputMsg)
	if !ok {
		t.Fatal("the pane never signalled")
	}
	mm, cmd := m.Update(out)
	m = mm.(Model)
	if cmd == nil {
		t.Fatal("an exited pane produced no teardown; it would leak until leader x")
	}
	if !tb.closing {
		t.Error("the reap was not marked in flight, so a second signal would start another")
	}
	reaped, ok := runCmd(t, cmd).(paneReapedMsg)
	if !ok {
		t.Fatal("the teardown did not report back")
	}
	if reaped.err != nil {
		t.Errorf("reap error = %v", reaped.err)
	}
	mm, _ = m.Update(reaped)
	m = mm.(Model)

	if d.closed != 1 {
		t.Errorf("detacher closed %d times, want exactly 1 — and nobody pressed a key", d.closed)
	}
	// The tab stays, so the last screen is still readable; leader x is what
	// removes it.
	mustTabs(t, m, 1)
	// And nothing re-arms on it. A closed pane's Dirty() returns
	// immediately and forever, so a re-armed wait would spin at the tick
	// rate rather than park.
	if _, again := m.Update(paneOutputMsg{id: tb.id}); again != nil {
		t.Error("an exited pane re-armed its output wait")
	}
}

// TestAnOpenThatWarnsGoesThroughResultWarn pins the mechanism, not the text:
// an open that worked but degraded — the no-tmux fallback — reports through
// ResultWarn, the same path every other "it worked, now read this" takes, so
// there is one place that decides such a notice is sticky.
func TestAnOpenThatWarnsGoesThroughResultWarn(t *testing.T) {
	// exits, so the redraw wait batched with the warning comes back at once:
	// a child that prints nothing and never dies leaves awaitOutput parked
	// on Dirty, and drain would wait on it.
	h := &fakeHost{t: t, exits: true,
		warn: "mercury has no tmux: this session will not survive the window"}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)

	mm, cmd := m.Update(press("enter"))
	m = mm.(Model)
	opened, ok := runCmd(t, cmd).(paneOpenedMsg)
	if !ok {
		t.Fatal("enter did not open a pane")
	}
	mm, cmd = m.Update(opened)
	m = mm.(Model)

	warn := ""
	for _, msg := range drain(cmd) {
		if got := ResultWarnText(msg); got != "" {
			warn = got
			mm, _ = m.Update(msg)
			m = mm.(Model)
		}
	}
	if !strings.Contains(warn, "no tmux") {
		t.Fatalf("the open emitted no warning result; got %q", warn)
	}
	if !m.notice.isErr || !strings.Contains(m.notice.text, "no tmux") {
		t.Errorf("notice = %+v, want the warning in the alert style", m.notice)
	}
}

// runCmd runs one command and hands back the message it produced. The
// ceiling is the same hazard pump documents: a command that never answers
// should fail the test rather than hang it.
func runCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command to run")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		return msg
	case <-time.After(10 * time.Second):
		t.Fatal("a command never produced a message")
		return nil
	}
}

// mustTabs is a fatal check, not a wait, and deliberately so: stepPump has
// already run the open's command and fed paneOpenedMsg back into the model
// before this is called, so the tab either exists by now or never will.
// Polling could not help anyway — Model is a value with a value-receiver
// Update, and nothing else holds a reference to this one to mutate.
// (waitForHistory in leader_test.go is different: it polls a live *pane.Pane
// that a goroutine really is filling in.)
func mustTabs(t *testing.T, m Model, n int) {
	t.Helper()
	if len(m.tabs) != n {
		t.Fatalf("tabs = %d, want %d", len(m.tabs), n)
	}
}
```

Add to `internal/controlplane/model_test.go`, beside `newTestModel`:

```go
// newTestModelWithHost is newTestModel with a pane host, for the tests that
// open tabs.
func newTestModelWithHost(d *fakeData, a Actor, h PaneHost) Model {
	m := New(d, a, h, NewKeyMap(nil))
	m.now = func() time.Time { return time.Unix(1_000_060, 0) }
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = mm.(Model)
	mm, _ = m.Update(snapshotMsg{snap: d.snap})
	return mm.(Model)
}
```

`New` grows a parameter, so **every** call to it in the test package moves,
not just `newTestModel`'s. At HEAD there are four, and all four are in
`internal/controlplane/model_test.go`:

| Line | Caller | New call |
|---|---|---|
| 152 | `newTestModel` | `New(d, a, nopPaneHost{}, NewKeyMap(nil))` |
| 161 | `TestInitKicksAllThreeCadences` | `New(&fakeData{snap: testSnapshot()}, &recordingActor{}, nopPaneHost{}, NewKeyMap(nil))` |
| 299 | `TestFirstSnapshotTriggersAnImmediateSlowPoll` | `New(d, &recordingActor{}, nopPaneHost{}, NewKeyMap(nil))` |
| 768 | `TestPreSizeViewRunsInTheAlternateScreen` | `New(&fakeData{}, &recordingActor{}, nopPaneHost{}, NewKeyMap(nil))` |

Grep for `New(` in that file rather than trusting the line numbers; three of
the four are standalone and easy to miss, and the package does not compile
until all of them move.

- [ ] **Step 2: Run to verify it fails**

Run: `cd /Users/elliott/Projects/cspace-control-plane-4b && go test ./internal/controlplane/ -run TestEnterOpens`
Expected: FAIL to build — `undefined: PaneHost`, `undefined: focusMain`.

- [ ] **Step 3: Write `panes.go`**

```go
package controlplane

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/pane"
)

// Kind is what a tab holds.
type Kind int

const (
	KindClaude     Kind = iota // the sandbox's interactive claude, in tmux
	KindShell                  // a login shell in the sandbox, in tmux
	KindHostShell              // the operator's own shell, no container
	KindSupervisor             // the event tail and a send box; not a pty
)

func (k Kind) String() string {
	switch k {
	case KindClaude:
		return "claude"
	case KindShell:
		return "shell"
	case KindHostShell:
		return "host shell"
	case KindSupervisor:
		return "supervisor"
	}
	return "pane"
}

// openTimeout bounds one pane open. It has to cover the tmux presence probe
// (10s) plus control's attach lock wait (12s), because a pane opening while
// a `cspace attach` is still identifying its own client legitimately waits
// that lock out.
const openTimeout = 30 * time.Second

// closeTimeout bounds one pane close: a detach-client exec into the sandbox
// plus the engine's own teardown, whose kill grace is two seconds.
const closeTimeout = 20 * time.Second

// The labels the footer shows while a pane is opening or closing, and that
// the corresponding results carry back.
const (
	LabelOpenPane  = "open pane"
	LabelClosePane = "close pane"
)

// Detacher ends one tmux client's attachment. *control.Attachment satisfies
// it; a host-shell pane has none.
type Detacher interface {
	Close(ctx context.Context) error
}

// Opened is what a PaneHost hands back.
type Opened struct {
	Pane   *pane.Pane
	Detach Detacher
	// Warning is a notice the person must read even though the open worked —
	// the no-tmux fallback, whose session will not survive this window.
	Warning string
}

// PaneHost opens panes and runs their attach bookkeeping. Like Data and
// Actor it is declared here by the consumer and implemented in internal/cli,
// which is what keeps this package from importing it — and keeps
// internal/pane from ever learning what a sandbox is.
//
// Neither method may be called from Update: Open probes a container and
// takes a file lock, and both would freeze the UI goroutine. Every caller
// goes through a tea.Cmd.
type PaneHost interface {
	Open(ctx context.Context, kind Kind, row control.Row, cols, rows int) (Opened, error)
	// Sweep reaps the client records of attaches whose host process is gone.
	// It returns how many records it dealt with.
	Sweep(ctx context.Context) (int, error)
}

// nopPaneHost is the host a Model built without one gets: every open fails
// with an explanation rather than a nil dereference, which is the same
// fail-closed rule control.Client applies to its own unset seams.
type nopPaneHost struct{}

func (nopPaneHost) Open(context.Context, Kind, control.Row, int, int) (Opened, error) {
	return Opened{}, errors.New("no pane host configured")
}
func (nopPaneHost) Sweep(context.Context) (int, error) { return 0, nil }

// tab is one entry in the tabs row: a live pane, or the supervisor view.
//
// Tabs are held by pointer, and that is a deliberate exception to the rule
// the Model's doc states about never mutating shared state in place. A tab
// owns a running process and a scrollback — things a Model copy must not
// fork — so the slice is what gets rebuilt on every change (addTab, dropTab)
// while each tab is shared. Nothing mutates a tab from anywhere but the UI
// goroutine.
type tab struct {
	id      int
	kind    Kind
	project string
	sandbox string

	// p is nil for KindSupervisor, which runs no process.
	p *pane.Pane
	// detach is nil for KindHostShell and KindSupervisor, neither of which
	// is a tmux client.
	detach Detacher
	// sup is non-nil only for KindSupervisor (see supervisor.go).
	sup *supervisor

	// closing is set the moment a teardown is handed to a command — by
	// closeTab, or by the paneOutputMsg arm reaping a child that exited on
	// its own — and it exists to stop awaitOutput re-arming on a pane that
	// is going away.
	//
	// The hazard is the opposite of a leak. 4a closes the dirty channel
	// inside Pane.Close (see Pane.Dirty's doc): a receive on a closed pane's
	// Dirty() returns immediately, and forever. An unguarded re-arm is
	// therefore a ~30 Hz message loop on a dead pane — a whole dashboard
	// redrawn at the tick rate for the rest of the session — not a
	// goroutine parked on a channel nothing can refill.
	closing bool
	// reaped is set once an exited pane's own teardown has finished, so a
	// later signal cannot start a second one. closing covers the window
	// while it is in flight; this covers everything after.
	reaped bool
}

// title is what the tabs row shows: "<project>/<sandbox> · <kind>". A host
// shell belongs to no sandbox, so it says so.
//
//nolint:unused // filled in by Task 3, which is the first caller
func (t *tab) title() string {
	if t.kind == KindHostShell {
		return "host · shell"
	}
	return fmt.Sprintf("%s/%s · %s", t.project, t.sandbox, t.kind)
}

// focusArea is what the keyboard is pointed at.
type focusArea int

const (
	focusSidebar focusArea = iota
	focusMain
)

// The pane messages.
type (
	// paneOpenedMsg carries one open's outcome. The kind and row travel with
	// it because the selection may have moved while the open was in flight.
	paneOpenedMsg struct {
		kind   Kind
		row    control.Row
		opened Opened
		err    error
	}
	// paneOutputMsg says a pane has new output worth drawing. The id, not an
	// index: tabs are reordered by closes.
	paneOutputMsg struct{ id int }
	// paneClosedMsg reports a teardown, whether or not it went cleanly.
	paneClosedMsg struct {
		id  int
		err error
	}
	// paneReapedMsg reports the teardown of a pane whose child exited on
	// its own. Unlike paneClosedMsg it does NOT drop the tab: an exited
	// pane still shows its last screen, and closing the tab stays the
	// operator's decision.
	paneReapedMsg struct {
		id  int
		err error
	}
)

// openOrFocus focuses the tab for (kind, row) if it exists, and otherwise
// starts opening one.
func (m Model) openOrFocus(kind Kind, row control.Row) (tea.Model, tea.Cmd) {
	// A host shell is exempt from the match: it belongs to no sandbox, so
	// the (kind, project, sandbox) key every other tab is found by is empty
	// for all of them. Asking for one always opens one — two host shells are
	// two tabs, both titled "host · shell" — rather than silently refocusing
	// whichever was opened first.
	if kind != KindHostShell {
		for i, t := range m.tabs {
			if t.kind == kind && t.project == row.Project && t.sandbox == row.Name {
				m.focused = i
				m.focus = focusMain
				m.scrolling, m.scroll = false, 0
				return m, nil
			}
		}
	}
	cols, rows := m.paneSize()
	if kind == KindSupervisor {
		// Nothing to spawn: the supervisor view is a reader. It is sized
		// here and in the window-resize fan-out, and nowhere else — its
		// view is read-only, so this is the only chance a brand-new one
		// gets to learn its height before it is first drawn.
		sup := newSupervisor(cols)
		sup.resize(cols, rows)
		m = m.addTab(&tab{
			id: m.nextTabID, kind: kind, project: row.Project, sandbox: row.Name,
			sup: sup,
		})
		return m, m.supervisorEventsCmd(m.tabs[m.focused])
	}
	host := m.host
	return m.startAction(LabelOpenPane, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), openTimeout)
		defer cancel()
		opened, err := host.Open(ctx, kind, row, cols, rows)
		return paneOpenedMsg{kind: kind, row: row, opened: opened, err: err}
	})
}

// addTab appends a tab, focuses it and points the keyboard at it.
func (m Model) addTab(t *tab) Model {
	t.id = m.nextTabID
	m.nextTabID++
	m.tabs = append(append([]*tab{}, m.tabs...), t)
	m.focused = len(m.tabs) - 1
	m.focus = focusMain
	m.scrolling, m.scroll = false, 0
	return m
}

// focusedTab is the tab the main area shows, or nil when there are none.
//
//nolint:unused // filled in by Task 3, which is the first caller
func (m Model) focusedTab() *tab {
	if m.focused < 0 || m.focused >= len(m.tabs) {
		return nil
	}
	return m.tabs[m.focused]
}

// tabByID finds a tab by identity, which is what every message carries:
// closing a tab shifts every index after it.
func (m Model) tabByID(id int) (*tab, int) {
	for i, t := range m.tabs {
		if t.id == id {
			return t, i
		}
	}
	return nil, -1
}

// awaitOutput waits for one pane's next frame and asks for a redraw. The
// engine's signal channel holds one slot, so a burst collapses into one
// message; the tick in front of it caps the redraw rate at ~30/s, which is
// the design's budget and well under bubbletea's own 60fps renderer.
func awaitOutput(t *tab) tea.Cmd {
	if t.p == nil {
		return nil
	}
	p, id := t.p, t.id
	return tea.Tick(paneRedrawInterval, func(time.Time) tea.Msg {
		<-p.Dirty()
		return paneOutputMsg{id: id}
	})
}

// paneRedrawInterval is the floor between two redraws of one pane.
const paneRedrawInterval = 33 * time.Millisecond

// closeTab runs the design's pane-close order: detach the tmux client, then
// the engine's teardown handshake. control.Attachment.Close does the detach
// and deletes the client's record file together, so the record is gone by
// the time the handshake runs rather than after it — the ordering difference
// is immaterial, since both happen once the client is detached.
func (m Model) closeTab(id int) tea.Cmd {
	t, _ := m.tabByID(id)
	if t == nil {
		return nil
	}
	t.closing = true
	detach, p := t.detach, t.p
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()
		var err error
		if detach != nil {
			err = detach.Close(ctx)
		}
		if p != nil {
			if closeErr := p.Close(ctx); closeErr != nil && err == nil {
				err = closeErr
			}
		}
		return paneClosedMsg{id: id, err: err}
	}
}

// reapExited is closeTab's teardown without the drop: the same detach and
// the same engine handshake, run for a pane whose child ended on its own,
// while the tab stays on screen.
//
// It exists because an exited pane is not a closed one. 4a's engine leaves
// the pty master, x/vt's parser buffer, the guest tmux client and this
// attach's record file all live until Close runs, and the child exiting is
// not Close — so without this the commonest way a pane ends would keep every
// one of those until the operator noticed and pressed leader x. What the tab
// keeps is only what a person still wants: the dimmed last screen
// (view_pane.go's exited branch) and the key that dismisses it.
func (m Model) reapExited(t *tab) tea.Cmd {
	id, detach, p := t.id, t.detach, t.p
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()
		var err error
		if detach != nil {
			err = detach.Close(ctx)
		}
		if p != nil {
			if closeErr := p.Close(ctx); closeErr != nil && err == nil {
				err = closeErr
			}
		}
		return paneReapedMsg{id: id, err: err}
	}
}

// dropTab removes a closed tab and re-points the focus at a neighbour,
// falling back to the sidebar when the last one goes.
func (m Model) dropTab(id int) Model {
	_, idx := m.tabByID(id)
	if idx < 0 {
		return m
	}
	tabs := make([]*tab, 0, len(m.tabs)-1)
	tabs = append(tabs, m.tabs[:idx]...)
	tabs = append(tabs, m.tabs[idx+1:]...)
	m.tabs = tabs
	switch {
	case len(m.tabs) == 0:
		m.focused = -1
		m.focus = focusSidebar
	case m.focused >= len(m.tabs):
		m.focused = len(m.tabs) - 1
	}
	m.scrolling, m.scroll = false, 0
	return m
}

// quitCmd tears every open pane down and then quits.
//
// The design says quitting does not confirm, because tmux holds every
// session — but a window that vanished without detaching would leave a
// client attached inside each sandbox and a record file stranded for the
// next start's sweep, so the detach still runs on the way out.
//
// The closes run CONCURRENTLY under one shared deadline, so the wait before
// the program ends is one closeTimeout however many tabs are open — not one
// per tab. That is not a micro-optimisation: control.Attachment.Close waits
// on its own tracking goroutine with no context of its own, bounded only by
// Tmux.PollFor, so one wedged sandbox is enough to make a sequential quit
// look hung while the operator stares at a frozen window.
//
// It is one closure rather than tea.Sequence(closes…, tea.Quit) because the
// closes have to finish before the program ends, and because a sequence's
// own message is unexported and therefore unobservable from a test: the
// thing that must not regress here is that quitting detaches.
func (m Model) quitCmd() tea.Cmd {
	type teardown struct {
		detach Detacher
		p      *pane.Pane
	}
	downs := make([]teardown, 0, len(m.tabs))
	for _, t := range m.tabs {
		if t.detach == nil && t.p == nil {
			continue // the supervisor view owns no process and no client
		}
		t.closing = true
		downs = append(downs, teardown{detach: t.detach, p: t.p})
	}
	if len(downs) == 0 {
		return tea.Quit
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()
		var wg sync.WaitGroup
		for _, d := range downs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				// The errors have nowhere to go — the program is ending —
				// but running these is what performs the detach.
				if d.detach != nil {
					_ = d.detach.Close(ctx)
				}
				if d.p != nil {
					_ = d.p.Close(ctx)
				}
			}()
		}
		wg.Wait()
		return tea.QuitMsg{}
	}
}
```

- [ ] **Step 4: Wire the tabs into `Model`**

In `internal/controlplane/model.go`:

- add to the struct, after `actor Actor`:

```go
	host PaneHost

	tabs      []*tab
	focused   int // index into tabs; -1 when there are none
	nextTabID int
	focus     focusArea

	// scrolling and scroll are the focused pane's scrollback position:
	// scroll is how many lines above the live screen the view sits, and
	// scrolling is whether the arrow keys are moving it rather than reaching
	// the child. Both reset whenever the focused tab changes.
	scrolling bool
	scroll    int
```

- change `New`'s signature and body:

```go
// New builds the dashboard over the query, action and pane seams and the
// resolved keymap. Nothing is polled and nothing is opened until Init runs.
func New(data Data, actor Actor, host PaneHost, keys KeyMap) Model {
	ti := textinput.New()
	ti.Placeholder = "message"
	ti.CharLimit = 2000
	if host == nil {
		host = nopPaneHost{}
	}
	return Model{
		data:     data,
		actor:    actor,
		host:     host,
		keys:     keys,
		help:     help.New(),
		now:      time.Now,
		input:    ti,
		spinner:  spinner.New(spinner.WithSpinner(spinner.Dot)),
		live:     map[sandboxKey]liveState{},
		memory:   map[string]int64{},
		ports:    map[sandboxKey][]control.Port{},
		portsErr: map[sandboxKey]error{},
		focused:  -1,
	}
}
```

- handle the new messages in `Update`, before the `tea.KeyPressMsg` case:

```go
	case paneOpenedMsg:
		m.action = ""
		if msg.err != nil {
			m.notice = notice{text: LabelOpenPane + " failed: " + msg.err.Error(), isErr: true}
			return m, nil
		}
		project, sandbox := msg.row.Project, msg.row.Name
		if msg.kind == KindHostShell {
			// It was opened from whatever row happened to be selected and
			// belongs to none of them: leave the identity empty rather than
			// let a later reader take it for a pane on that sandbox.
			project, sandbox = "", ""
		}
		m = m.addTab(&tab{
			kind:    msg.kind,
			project: project,
			sandbox: sandbox,
			p:       msg.opened.Pane,
			detach:  msg.opened.Detach,
		})
		wait := awaitOutput(m.tabs[m.focused])
		if msg.opened.Warning != "" {
			// Through ResultWarn rather than written into m.notice here:
			// "it worked, now read this" already has a mechanism, and the
			// actionResultMsg arm is where the rule that such a notice is
			// sticky lives. A second copy of that rule is a second place to
			// forget it.
			warning := msg.opened.Warning
			return m, tea.Batch(wait, func() tea.Msg { return ResultWarn(LabelOpenPane, warning) })
		}
		return m, wait

	case paneOutputMsg:
		// The redraw is the Update itself; the rest is deciding whether to
		// wait again. A tab closed while its wait was in flight drops the
		// message, and so does one whose teardown is already running.
		t, _ := m.tabByID(msg.id)
		if t == nil || t.p == nil {
			return m, nil
		}
		if _, _, exited := t.p.Exited(); exited {
			// The child ended on its own, and this signal — the waiter's
			// final markDirty — is the last one this pane will ever emit:
			// Dirty is closed by Pane.Close and by nothing else. Tear the
			// pane down here, or its tmux client stays attached inside the
			// sandbox and its record file on the host until the operator
			// presses leader x. The tab survives the reap.
			if !t.closing && !t.reaped {
				t.closing = true
				return m, m.reapExited(t)
			}
			return m, nil
		}
		if !t.closing {
			return m, awaitOutput(t)
		}
		return m, nil

	case paneClosedMsg:
		m.action = ""
		m = m.dropTab(msg.id)
		if msg.err != nil {
			m.notice = notice{text: LabelClosePane + ": " + msg.err.Error(), isErr: true}
		}
		return m, nil

	case paneReapedMsg:
		// No dropTab and no m.action: the reap was nobody's action, and the
		// tab stays to show the dead pane's last screen. Clearing closing
		// and setting reaped is what stops a second signal starting the
		// teardown again; dropping the detacher is what keeps a later
		// leader x from closing an attachment this already closed (Pane.Close
		// is idempotent on its own).
		if t, _ := m.tabByID(msg.id); t != nil {
			t.closing, t.reaped, t.detach = false, true, nil
		}
		if msg.err != nil {
			m.notice = notice{text: LabelClosePane + ": " + msg.err.Error(), isErr: true}
		}
		return m, nil
```

- fan a resize out to every pane, in the `tea.WindowSizeMsg` case, after `m.help.SetWidth(msg.Width)`:

```go
		cols, rows := m.paneSize()
		for _, t := range m.tabs {
			if t.p != nil {
				// The error is the ioctl's; a pane whose pty has gone will
				// be reaped by its own exit, and failing the resize of one
				// must not stop the others.
				_ = t.p.Resize(cols, rows)
			}
			if t.sup != nil {
				t.sup.resize(m.paneWidth(), rows)
			}
		}
```

- extend `paused` so a poll does not replace the row set while a pane is opening. `LabelAttach` stays in the condition until Task 7 deletes the action itself:

```go
func (m Model) paused() bool {
	return m.mode != modeNormal || m.action == LabelAttach || m.action == LabelOpenPane
}
```

- add the geometry helpers at the bottom of `model.go`:

```go
// paneSize is the emulator geometry for the main area: the window less the
// sidebar and the one column of padding on each side, and less the tabs row
// and the footer. Floored so a very small window still gets a legal size.
func (m Model) paneSize() (cols, rows int) {
	cols = m.paneWidth()
	rows = m.height - 2 // the tabs row and the footer
	if rows < 2 {
		rows = 2
	}
	return cols, rows
}

// paneWidth is the main area's usable width, which the supervisor view wraps
// its markdown to as well.
func (m Model) paneWidth() int {
	w := mainWidthFor(m.width) - 2 // styleMain's padding
	if w < 4 {
		w = 4
	}
	return w
}
```

- [ ] **Step 5: Open panes from the sidebar keys**

In `internal/controlplane/input.go`, `handleNormalKey`, add three cases inside the `switch` that uses `keys` (the `forRow` copy), before `keys.Teardown`:

```go
	case key.Matches(msg, keys.Attach):
		return m.openOrFocus(KindClaude, row)
	case key.Matches(msg, keys.Shell):
		return m.openOrFocus(KindShell, row)
	case key.Matches(msg, keys.Supervisor):
		return m.openOrFocus(KindSupervisor, row)
```

and delete the old `keys.Attach` case that called `m.actor.Attach(row)`. (`Actor.Attach` itself is removed in Task 7; leaving it declared and uncalled for now keeps `internal/cli` compiling.)

That deletion breaks a step-3 test **here**, at Task 2, not at Task 7 — and
it names neither `LabelAttach` nor `Actor.Attach`, so Task 7's catch-all does
not cover it. `internal/controlplane/input_test.go`'s
`TestAttachAndInterruptDispatch` presses `enter` and then asserts
`len(a.attach) == 1` and `m2.action == "attach"`; after this change Enter
reaches the `PaneHost` instead, so `a.attach` stays empty and `m.action` is
`LabelOpenPane`. Split it: drop the attach half and rename the rest, leaving

```go
func TestInterruptDispatch(t *testing.T) {
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

	if got := step(t, m, "i"); len(a.interrupt) != 1 {
		t.Errorf("interrupt calls = %d, want 1 (model %v)", len(a.interrupt), got.action)
	}
}
```

What Enter does now is covered by `TestEnterOpensAClaudePaneAndSecondEnterFocusesIt`
in `panes_test.go`, which asserts it against the host rather than the actor.

Add `Tab` to the keys that do not depend on the selection, in the first `switch`:

```go
	case key.Matches(msg, m.keys.FocusMain):
		if len(m.tabs) == 0 {
			return m, nil
		}
		m.focus = focusMain
		return m, nil
```

- [ ] **Step 6: Keep `cspace tui` building**

`New` grew a parameter, so its one production caller has to move with it. In `internal/cli/cmd_tui.go`, pass `nil` — `New` turns that into `nopPaneHost`, whose every open fails with an explanation. Task 7 replaces it with the real host:

```go
			model := controlplane.New(ctrl, newControlPlaneActor(ctrl, home),
				nil, // the pane host lands in Task 7
				controlplane.NewKeyMap(userCfg.TUI.Keys))
```

- [ ] **Step 7: Stub the supervisor so the package compiles**

The supervisor view is Task 6. Its shape is needed now, because the tab
bookkeeping refers to it — and so do Tasks 3 and 4, which call `view` and
`handleSupervisorKey` before Task 6 exists. The stub carries all six, so
**Task 6 replaces this file wholesale** rather than adding to it.

Three of them have no caller yet, and `unused` is in `.golangci.yml`'s
`linters.default: standard`, so a bare stub fails `make lint` at Step 10 —
and would go on failing through Tasks 3, 4 and 5, against this plan's own
"green after every task" constraint. Each of the three therefore carries a
`//nolint:unused` naming the task that calls it first, **and that task's
step removes the directive as it adds the caller**: Task 3 for `view`, Task
4 for `handleSupervisorKey`, Task 6 for `setEvents`. `resize` and
`supervisorEventsCmd` need no directive — Task 2's own resize fan-out and
`openOrFocus` call them.

Create `internal/controlplane/supervisor.go` with exactly this:

```go
package controlplane

import (
	tea "charm.land/bubbletea/v2"

	"github.com/elliottregan/cspace/internal/control"
)

// supervisor is the read-only view over a sandbox's event log. Task 6 of the
// panes plan fills it in and replaces this whole file; this is the shape the
// tab bookkeeping, the main area (Task 3) and the key routing (Task 4) need
// in order to compile, and `make check` has to be green after every task.
type supervisor struct{ width, height int }

func newSupervisor(width int) *supervisor { return &supervisor{width: width} }

func (s *supervisor) resize(width, height int) { s.width, s.height = width, height }

//nolint:unused // filled in by Task 6, which is the first caller
func (s *supervisor) setEvents([]control.EventLine) {}

//nolint:unused // filled in by Task 3, which is the first caller
func (s *supervisor) view(width, height int) string { return "" }

func (m Model) supervisorEventsCmd(*tab) tea.Cmd { return nil }

//nolint:unused // filled in by Task 4, which is the first caller
func (m Model) handleSupervisorKey(*tab, tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	return m, nil
}
```

- [ ] **Step 8: Run the tests**

Run: `cd /Users/elliott/Projects/cspace-control-plane-4b && go test ./internal/controlplane/ -v -run 'TestEnterOpens|TestShellAndSupervisor|TestAFailedOpen|TestCloseTab|TestAnExitedPane|TestAnOpenThatWarns'`
Expected: PASS.

- [ ] **Step 9: Widen the race target, then verify the dependency direction and the race build**

This task is the first of the three that give a pane a second concurrent
owner, and the bookkeeping that does it lives in `internal/controlplane`.
4a's `test-race` target covers `./internal/pane/...` only, so it cannot see
a race in a tab slice. Widen it in the `Makefile`:

```make
# Race check for the pane engine and the tab bookkeeping over it: four
# goroutines per pane, plus the dashboard that opens, resizes and closes
# them. Deliberately not part of `make check` — a race build is slow and
# these are the two packages that need it. Run it after touching either.
test-race: sync-embedded
	go test -race -count=1 ./internal/pane/... ./internal/controlplane/...
```

and update the line 4a added to `CLAUDE.md`'s `## Development` block to
match:

```
make test-race    # -race over internal/pane + internal/controlplane; NOT part of make check
```

Then:

```bash
cd /Users/elliott/Projects/cspace-control-plane-4b
go list -deps ./internal/controlplane | grep -E 'elliottregan/cspace/internal/cli' && echo "LEAK" || echo "clean"
go list -deps ./internal/pane | grep -E 'elliottregan/cspace/internal/(cli|controlplane|control)' && echo "LEAK" || echo "clean"
make test-race
```
Expected: `clean`, `clean`, and no `WARNING: DATA RACE`.

- [ ] **Step 10: Run the gate and commit**

`cmd_tui.go` is in the `git add` because Step 6 changed it; `internal/controlplane` alone would leave that edit uncommitted for five tasks and then sweep it into Task 7's commit.

```bash
cd /Users/elliott/Projects/cspace-control-plane-4b
make check
git add internal/controlplane internal/cli/cmd_tui.go Makefile CLAUDE.md
git commit -m "$(cat <<'EOF'
Add the tab list and the pane host seam

Enter, s and a now open panes through a PaneHost the consumer satisfies, the
same shape as Data and Actor, so this package still never imports
internal/cli. Nothing renders them yet — the layout switches over next — but
they open, redraw, resize and close, and closing runs the detach.

Co-Authored-By: <model> <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01W64zstC3PZojywARTtnSW7
EOF
)"
```

---

### Task 3: The layout moves

The design's geometry: the detail band under the sidebar, the tabs row carrying real tabs, the focused pane filling the main area. Step 3 put the band in the main area and the selection's title in the tabs row — deliberately, and with a comment saying step 4 would move them. This is that move.

**Files:**
- Create: `internal/controlplane/view_pane.go`
- Test: `internal/controlplane/view_pane_test.go`
- Modify: `internal/controlplane/view.go`, `internal/controlplane/styles.go`, `internal/controlplane/view_detail.go`, `internal/controlplane/model.go`, `internal/controlplane/keys.go` (Step 7 adds `actionHelp[ActionLeader]`), `internal/controlplane/supervisor.go` and `internal/controlplane/panes.go` (Step 3 drops all three `//nolint:unused` lines Task 2 left for it — `supervisor.view`, `focusedTab`, `tab.title`)
- Test (existing, updated): `internal/controlplane/model_test.go` (`TestTabsLineFitsANarrowWindow` → `TestTabsRowFitsANarrowWindow`)

**Interfaces:**
- Consumes: `tab`, `focusArea`, `focusedTab`, `paneSize`, `paneWidth` (Task 2); `renderSidebar`, `renderDetail`, `fit`, `mainWidthFor`, `sidebarWidth`, `sidebarInner`, `sidebarContent` (step 3).
- Produces:
  - `func renderTabs(tabs []*tab, focused, width int, active bool) string` — four parameters, not three; `active` is whether the keyboard is pointed at the main area
  - `func (m Model) tabsRow(width int) string` — replaces step 3's `tabsLine`
  - `func (m Model) paneArea(width, height int) string`
  - `func fitLines(s string, n int) string`
  - `func (m Model) sidebarColumn(height int) string`
  - styles `styleTabActive`, `styleTabFocused`, `styleTabIdle`

  There is deliberately **no pane border**. The spec's "inside a border
  colored by state" is dropped here and the drop is written into the spec in
  Task 7 Step 7: a border costs two of the main area's columns and two of its
  rows for a cue the tabs row already carries (`styleTabActive` versus
  `styleTabFocused`), beside the footer switching to the leader's keys and
  the cursor appearing in the pane.

- [ ] **Step 1: Write the failing test**

Create `internal/controlplane/view_pane_test.go`:

```go
package controlplane

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderTabsTruncatesFromTheLeftAndCounts(t *testing.T) {
	tabs := []*tab{
		{id: 1, kind: KindClaude, project: "resume-redux", sandbox: "mercury"},
		{id: 2, kind: KindShell, project: "resume-redux", sandbox: "mercury"},
		{id: 3, kind: KindSupervisor, project: "cspace", sandbox: "issue-42"},
	}
	wide := plain(renderTabs(tabs, 0, 100, true))
	for _, want := range []string{"resume-redux/mercury · claude", "· shell", "issue-42 · supervisor"} {
		if !strings.Contains(wide, want) {
			t.Errorf("wide tabs %q missing %q", wide, want)
		}
	}

	// Narrow: the design's open question 1 — truncate from the left and show
	// a count, so the focused tab stays legible and the rest are accounted
	// for rather than silently gone. Width is display cells, not bytes: the
	// separator is a multi-byte "·".
	narrow := plain(renderTabs(tabs, 2, 30, true))
	if w := ansi.StringWidth(narrow); w > 30 {
		t.Errorf("narrow tabs are %d cells wide, want at most 30: %q", w, narrow)
	}
	if !strings.Contains(narrow, "issue-42") {
		t.Errorf("narrow tabs %q dropped the focused tab", narrow)
	}
	if !strings.Contains(narrow, "+2") {
		t.Errorf("narrow tabs %q does not say how many are hidden", narrow)
	}
}

func TestEmptyMainAreaNamesTheKeysThatOpenAPane(t *testing.T) {
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, &fakeHost{t: t})
	got := plain(m.paneArea(60, 10))
	for _, want := range []string{"enter", "s ", "a "} {
		if !strings.Contains(got, want) {
			t.Errorf("empty main area %q does not offer %q", got, want)
		}
	}
	if lines := strings.Count(m.paneArea(60, 10), "\n") + 1; lines != 10 {
		t.Errorf("empty main area is %d lines, want exactly 10", lines)
	}
}

func TestSidebarColumnHoldsRowsAndTheDetailBand(t *testing.T) {
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, &fakeHost{t: t})
	col := m.sidebarColumn(23)
	lines := strings.Split(col, "\n")
	if len(lines) != 23 {
		t.Fatalf("sidebar column is %d lines, want 23", len(lines))
	}
	got := plain(col)
	if !strings.Contains(got, "mercury") {
		t.Error("the row list is gone")
	}
	if !strings.Contains(got, "running") {
		t.Error("the detail band is not under the sidebar")
	}
	// The memory figure is the reason the band's header folds at this width:
	// on one line `fit` would cut it off the end.
	if !strings.Contains(got, "16G") {
		t.Error("the band's memory figure did not survive the 24-column fold")
	}
	for _, l := range strings.Split(plain(col), "\n") {
		if ansi.StringWidth(l) > sidebarInner {
			t.Errorf("line %q is wider than the sidebar", l)
		}
	}
}

func TestViewPutsAFocusedPaneInTheMainArea(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = stepPump(t, m, "enter")
	mustTabs(t, m, 1)

	view := plain(m.View().Content)
	if !strings.Contains(view, "mercury · claude") {
		t.Errorf("view %q has no tab for the open pane", view)
	}
	// The footer follows the focus: with a pane focused it names the
	// leader's keys, not the sidebar's.
	if !strings.Contains(view, "sidebar") || !strings.Contains(view, "close") {
		t.Errorf("view %q does not show the leader footer", view)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /Users/elliott/Projects/cspace-control-plane-4b && go test ./internal/controlplane/ -run 'TestRenderTabs|TestEmptyMainArea|TestSidebarColumn|TestViewPuts'`
Expected: FAIL to build — `undefined: renderTabs`, `undefined: paneArea`, `undefined: sidebarColumn`.

- [ ] **Step 3: Write `view_pane.go`**

This step is the first caller of **three** symbols Task 2 had to annotate,
so **delete the `//nolint:unused // filled in by Task 3, which is the first
caller` line above each of them** as part of it:

- `func (s *supervisor) view` in `internal/controlplane/supervisor.go` —
  `paneArea` below calls it;
- `func (m Model) focusedTab` in `internal/controlplane/panes.go` —
  `paneArea` and `View` both call it;
- `func (t *tab) title` in `internal/controlplane/panes.go` — `renderTabs`
  calls it.

Leaving any of them would be a lie by the time `make check` runs, and
`nolintlint` is not what catches that — a reader is.

```go
package controlplane

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// renderTabs draws the tabs row: one tab per open pane, titled
// "<project>/<sandbox> · <kind>".
//
// active says whether the keyboard is pointed at the main area. It is the
// third cue that typing will reach the child, beside the footer switching to
// the leader's keys and the cursor appearing in the pane — and the only one
// that is visible without reading anything.
//
// When the tabs do not fit, the row keeps the focused tab and drops from the
// LEFT, then says how many it dropped — the design's open question 1, whose
// default is exactly that. Dropping from the left rather than the right
// keeps the focused tab and its neighbours, which are the ones n/p reaches
// next; a bare truncation would hide the tab a person just opened.
func renderTabs(tabs []*tab, focused, width int, active bool) string {
	if len(tabs) == 0 {
		return ""
	}
	focusedStyle := styleTabFocused
	if active {
		focusedStyle = styleTabActive
	}
	rendered := make([]string, len(tabs))
	for i, t := range tabs {
		style := styleTabIdle
		if i == focused {
			style = focusedStyle
		}
		rendered[i] = style.Render(t.title())
	}

	from := 0
	for {
		row := strings.Join(rendered[from:], "")
		hidden := from
		suffix := ""
		if hidden > 0 {
			suffix = styleDim.Render(fmt.Sprintf(" +%d", hidden))
		}
		if ansi.StringWidth(row)+ansi.StringWidth(suffix) <= width {
			return suffix + row
		}
		if from >= focused || from == len(tabs)-1 {
			// Even the focused tab alone does not fit: truncate its text
			// rather than the rendered string, so no escape sequence is cut
			// in half and no closing reset is lost.
			//
			// The budget is the width less the two things that are added
			// around the title and are not part of it: the "+N " prefix,
			// measured rather than assumed (a two-digit count is four
			// cells, not three), and the two columns of Padding(0, 1) that
			// every tab style carries. A hard-coded 4 here is one cell too
			// many and the row comes out at width+1.
			prefix := fmt.Sprintf("+%d ", from)
			budget := width - ansi.StringWidth(prefix) - 2
			if budget < 1 {
				budget = 1
			}
			return styleDim.Render(prefix) +
				focusedStyle.Render(fit(tabs[focused].title(), budget))
		}
		from++
	}
}

// paneArea is the main area's content: the focused pane's screen, the
// supervisor view, or — with no tabs at all — the keys that open one.
//
// Render, Cursor and ScrollbackView are called from here and nowhere else,
// which is what keeps the "UI goroutine only" rule checkable: View is the
// only caller of paneArea.
func (m Model) paneArea(width, height int) string {
	t := m.focusedTab()
	if t == nil {
		return fitLines(strings.Join([]string{
			styleDim.Render("no panes open"),
			"",
			styleDim.Render("enter   a claude session in the selected sandbox"),
			styleDim.Render("s       a shell in it"),
			styleDim.Render("a       its supervisor's events"),
			styleDim.Render("⌃Space t   the new-pane picker, host shell included"),
		}, "\n"), height)
	}
	if t.sup != nil {
		return fitLines(t.sup.view(width, height), height)
	}
	if t.p == nil {
		return fitLines(styleErr.Render("this pane has no process"), height)
	}

	if code, err, exited := t.p.Exited(); exited {
		reason := fmt.Sprintf("exited with status %d", code)
		if err != nil {
			reason = "exited: " + err.Error()
		}
		// The last screen, dimmed, under the reason — a dead pane still
		// shows what it was doing when it died. By the time this renders
		// the pane has also been reaped (model.go's paneOutputMsg arm), and
		// that is fine: Pane.Close ends the child, the pty and the tmux
		// client, but the emulator keeps its screen and its scrollback —
		// only its input pipe is closed — so Render and ScrollbackView
		// still answer.
		body := styleDim.Render(fitLines(t.p.ScrollbackView(m.scroll, height-2), height-2))
		return fitLines(strings.Join([]string{
			styleErr.Render(fit(reason+" — ⌃Space x closes this tab", width)),
			"",
			body,
		}, "\n"), height)
	}

	if m.scrolling {
		return fitLines(strings.Join([]string{
			styleDim.Render(fit(fmt.Sprintf("scroll · %d lines back · ⌃Space g or any other key returns to live",
				m.scroll), width)),
			t.p.ScrollbackView(m.scroll, height-1),
		}, "\n"), height)
	}
	return fitLines(t.p.Render(), height)
}

// fitLines pads or truncates s to exactly n lines, so a block always
// occupies the height the layout gave it. Lines are not touched: their width
// is already the caller's problem, and cutting a styled line would risk
// losing its closing reset.
func fitLines(s string, n int) string {
	if n <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// sidebarColumn is the left column: the row list, a rule, and the detail
// band for the selection.
//
// The band is 24 columns wide here, which is the design's placement once
// panes take the main area over. A URL does not fit — but the sidebar's own
// port lines carry the URL as an OSC 8 hyperlink, so the address is still
// one click away, and what the band adds at this width is the state, the
// uptime, the memory and the agent's last event.
//
// On a short window the band is what gives: below twelve rows there is not
// enough left for both, and the row list is the thing you cannot navigate
// without.
func (m Model) sidebarColumn(height int) string {
	if height <= 0 {
		return ""
	}
	band := height / 3
	switch {
	case height < 12:
		band = 0
	case band < 6:
		band = 6
	case band > 14:
		band = 14
	}
	list := height
	if band > 0 {
		list = height - band - 1 // the rule
	}
	parts := []string{renderSidebar(m.rows, m.live, m.ports, m.selected, list)}
	if band > 0 {
		row := m.selectedRow()
		k := keyOf(row)
		parts = append(parts,
			styleDim.Render(strings.Repeat("─", sidebarInner)),
			fitLines(renderDetail(row, m.live[k], m.ports[k], m.portsErr[k],
				m.events, m.eventsErr, m.memory[row.Container], sidebarInner), band))
	}
	return strings.Join(parts, "\n")
}
```

- [ ] **Step 4: Fold the detail band's header at the sidebar's width**

`sidebarColumn` renders `renderDetail` at `sidebarInner` — 23 columns — and
the band's first line is four fields joined with ` · `. At 23 columns `fit`
truncates it with an ellipsis and the memory figure, the last field, is the
one that goes: `"mercury · running · "` is already 20 cells. That silently
takes away one of the four things the band exists to show, and it breaks
`TestMemoryUsageSurvivesAStatsFreeSnapshot` (model_test.go), which asserts
`1.6G/16G` is on screen.

In `internal/controlplane/view_detail.go`, replace the `control.RowSandbox`
branch's first `add` call:

```go
		add(lipglossStyle{}, "%s · %s · %s · %s", row.Name, stateLabel(row),
			formatUptime(row.Uptime), formatMemUsage(memoryUsedB, row.MemoryB))
```

with:

```go
		// Under the sidebar `width` is sidebarInner — 23 columns, one less
		// than sidebarWidth's 24 — and these four fields do not fit on one
		// line; `fit` would cut the memory figure off the end, which is the
		// number the band is there for. Fold rather than lose it. In the
		// main area, where the band was until rollout step 4, the header
		// still fits and still renders as one line.
		head := fmt.Sprintf("%s · %s · %s · %s", row.Name, stateLabel(row),
			formatUptime(row.Uptime), formatMemUsage(memoryUsedB, row.MemoryB))
		if ansi.StringWidth(head) > width {
			add(lipglossStyle{}, "%s · %s", row.Name, stateLabel(row))
			add(lipglossStyle{}, "%s", strings.TrimPrefix(
				formatUptime(row.Uptime)+" · "+formatMemUsage(memoryUsedB, row.MemoryB), " · "))
		} else {
			add(lipglossStyle{}, "%s", head)
		}
```

Add `"github.com/charmbracelet/x/ansi"` to `view_detail.go`'s imports;
`fmt` and `strings` are already there. `TestRenderDetailSandbox` renders at 70
columns and is unaffected; `TestRenderDetailFitsItsWidth` renders a 59-character
name at 40 and now folds, which is still within its width assertion.

- [ ] **Step 5: Add the styles**

In `internal/controlplane/styles.go`, inside the `var` block:

```go
	// The tabs row. styleTabActive is the focused tab while the keyboard is
	// pointed at the main area; styleTabFocused is the same tab while the
	// sidebar has it — bold, so it is still findable, but not lit. The rest
	// are dim, so the row reads at a glance even without colour.
	styleTabActive = lipgloss.NewStyle().Bold(true).Padding(0, 1).
			Foreground(lipgloss.Color("#5fffaf"))
	styleTabFocused = lipgloss.NewStyle().Bold(true).Padding(0, 1)
	styleTabIdle    = lipgloss.NewStyle().Faint(true).Padding(0, 1)
```

- [ ] **Step 6: Rewrite `View`**

Replace `View`, `mainArea` and — under its new name — `tabsLine` in
`internal/controlplane/view.go`. `tabsRow` is not a same-named replacement:
it is a rename, and two things go with it. `tabsLine` was `view.go`'s only
user of the `internal/control` import (the `case row.Kind == control.RowProject`
in its title arithmetic, view.go:64), so **drop
`"github.com/elliottregan/cspace/internal/control"` from that file's imports**
or the package fails to build with "imported and not used". And
`internal/controlplane/model_test.go`'s `TestTabsLineFitsANarrowWindow`
(model_test.go:788) calls `m.tabsLine(mainWidth)` by name, so it stops
compiling: rewrite it as

```go
// With no panes open the row carries daemon health, right-aligned, and it
// has to fit a narrow window — the design's own example: W=57 -> mainWidth
// 33, where the old selection title and "daemon 1.0.0-rc.48" together did
// not fit even with a one-cell gap.
func TestTabsRowFitsANarrowWindow(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	const mainWidth = 57 - sidebarWidth // 33

	line := plain(m.tabsRow(mainWidth))
	if w := ansi.StringWidth(line); w > mainWidth {
		t.Errorf("tabs row width = %d, want <= %d: %q", w, mainWidth, line)
	}
	if !strings.Contains(line, "daemon") {
		t.Errorf("with no tabs the row should still show daemon health; got %q", line)
	}
}
```


```go
// View lays out the design's fixed geometry: a 24-column sidebar on the left
// carrying the row list and, beneath it, the detail band; on the right a
// tabs row, the main area, and a one-line footer across the bottom.
//
// Rollout step 3 put the band in the main area and the selection's title in
// the tabs row, because there were no panes to compete for either. This is
// the move that comment promised.
func (m Model) View() tea.View {
	if m.width == 0 || m.height == 0 {
		v := tea.NewView("starting cspace tui…")
		v.AltScreen = true
		return v
	}

	bodyHeight := m.height - 1 // the footer
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	mainWidth := mainWidthFor(m.width)

	side := styleSidebar.Height(bodyHeight).Render(m.sidebarColumn(bodyHeight))

	// The main area's own size comes from paneSize, not from bodyHeight-1
	// and mainWidth-2 recomputed here. They are the same arithmetic at any
	// usable window — and at a tiny one they are not, because paneSize
	// floors, so an emulator sized 2 rows would be rendered into 0. One
	// source keeps the pane's geometry and the box it is drawn in equal,
	// which is the property supervisor.view's read-only design rests on.
	paneCols, paneRows := m.paneSize()
	main := lipgloss.NewStyle().Width(mainWidth).Height(bodyHeight).MaxHeight(bodyHeight).Render(
		lipgloss.JoinVertical(lipgloss.Left,
			m.tabsRow(mainWidth),
			styleMain.Render(m.mainArea(paneCols, paneRows))))

	v := tea.NewView(lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.JoinHorizontal(lipgloss.Top, side, main),
		m.footer()))
	v.AltScreen = true

	// The cursor belongs to the focused pane and only when the keyboard is
	// pointed at it: a cursor blinking in a pane the keys do not reach is a
	// lie about where typing goes.
	if t := m.focusedTab(); t != nil && t.p != nil && m.focus == focusMain && !m.scrolling {
		if _, _, exited := t.p.Exited(); !exited {
			x, y := t.p.Cursor()
			v.Cursor = tea.NewCursor(x+sidebarWidth+1, y+1)
		}
	}
	return v
}

// tabsRow is the row of pane tabs above the main area. With no panes open it
// carries daemon health instead, so the line is never blank — an empty line
// above the main area reads as a rendering bug rather than as a promise.
func (m Model) tabsRow(width int) string {
	health, style := "daemon unreachable", styleErr
	if m.daemon.Reachable {
		health, style = "daemon "+m.daemon.Version, styleDim
	}
	if len(m.tabs) == 0 {
		gap := width - ansi.StringWidth(health) - 1
		if gap < 1 {
			gap = 1
		}
		return strings.Repeat(" ", gap) + style.Render(fit(health, width-gap))
	}
	// With tabs on the row, health moves to the footer's domain: the tabs
	// are what the row is named for and they get all of it.
	return renderTabs(m.tabs, m.focused, width, m.focus == focusMain)
}

// mainArea is what sits under the tabs row: the help overlay when it is
// open, a prompt while it is unanswered, otherwise the focused pane.
func (m Model) mainArea(width, height int) string {
	switch {
	case m.showHelp:
		return fitLines(m.helpView(width), height)
	case m.mode == modeConfirmDown && m.confirm != nil:
		return fitLines(m.confirm.View(), height)
	case m.mode == modePicker && m.picker != nil:
		return fitLines(m.picker.View(), height)
	}
	return m.paneArea(width, height)
}
```

`modePicker` and `m.picker` arrive in Task 4. Add the mode constant and the field now so this compiles — `"charm.land/huh/v2"` is already imported by `model.go` for the teardown confirmation's `confirm *huh.Form`:

- in `model.go`'s `uiMode` block, add `modePicker` after `modeInput`;
- in the `Model` struct, add `picker *huh.Form` beside `confirm`.

- [ ] **Step 7: Make the footer follow the focus**

In `internal/controlplane/view.go`'s `footer`, replace the final two lines:

```go
	// The footer names the keys that will actually work, which depends on
	// where the keyboard is pointed: the sidebar's own keys, or — with a
	// pane focused, where every key but the leader goes to the child — the
	// leader's second keys, prefixed by the leader itself.
	if m.focus == focusMain && m.focusedTab() != nil {
		lead := m.keys.Leader.Help().Key
		if lead == "" {
			lead = strings.Join(m.keys.Leader.Keys(), "/")
		}
		// helpView's pattern, for helpView's reason: m.help is sized to the
		// whole window, this line has the leader's label in front of it, and
		// a local copy is how one call gets a different budget without
		// changing the model's.
		//
		// Not fit(): the composed line is already styled ANSI, and fit drops
		// trailing *runes* — the hazard tabsLine and sendBoxLine both
		// document, where an escape sequence is cut in half and its closing
		// reset lost, so the style bleeds into whatever the terminal draws
		// next. help counts cells and elides between bindings, which is what
		// this line wants anyway: LeaderHelp is trimmed to what fits, and
		// anything that still does not is dropped whole rather than sliced.
		h := m.help
		h.SetWidth(max(1, m.width-ansi.StringWidth(lead)-1))
		return lead + " " + h.ShortHelpView(m.keys.LeaderHelp())
	}
	row := m.selectedRow()
	return m.help.ShortHelpView(m.keys.forRow(row, m.live[keyOf(row)]).ShortHelp())
```

`m.keys.Leader` has no help text (step 3 left it out of `actionHelp` on purpose), so add one now in `keys.go`'s `actionHelp`:

```go
	ActionLeader: {"⌃Space", "leader"},
```

- [ ] **Step 8: Run the tests**

Run: `cd /Users/elliott/Projects/cspace-control-plane-4b && go test ./internal/controlplane/ -v`
Expected: PASS. Three step-3 tests are in the blast radius of this task and all three are dealt with above rather than here: `TestTabsLineFitsANarrowWindow` becomes `TestTabsRowFitsANarrowWindow` (Step 6), `TestMemoryUsageSurvivesAStatsFreeSnapshot` keeps passing because Step 4 folds the band's header instead of truncating it, and `TestViewGeometry` asserts a line count and that no line is wider than the window — both still hold, and its footer assertion is Task 7's problem, not this task's. If any other step-3 test asserts the detail band's text appears in the main area, update it to look in the sidebar column instead: that is the move this task is.

- [ ] **Step 9: Look at it** — *manual, skip if no host is available*

This starts the real binary under a pty against the real host, the way Task
8 does; a headless executor should skip it and say so rather than fail the
task. Step 10's `make check` is the gate that has to be green everywhere.

```bash
cd /Users/elliott/Projects/cspace-control-plane-4b
make build && python3 scripts/tui-smoke/smoke.py sandboxes daemon
```
Expected: the printed screen shows the row list at the top-left, a rule, and the detail band beneath it; the right-hand side is the "no panes open" text with the three keys; the footer is the sidebar's short help; and the printed `exit` line reads `0`.

Read the screen, not the needles. `smoke.py` prints whether each needle was found and then always exits 0 (smoke.py:17-34), so "found: sandboxes" is a report, not an assertion — and with the layout moved, `sandboxes` is a word this dashboard never renders anyway. The check here is the screen above and a clean exit; Task 8 Step 3 is the step that drives `tuilib.Tui` directly when an assertion is wanted.

- [ ] **Step 10: Run the gate and commit**

```bash
cd /Users/elliott/Projects/cspace-control-plane-4b
make check
git add internal/controlplane
git commit -m "$(cat <<'EOF'
Move the detail band under the sidebar and make the tabs row real

The band renders at 24 columns now, where a URL does not fit — the sidebar's
own port lines still carry it as a hyperlink. The main area is the focused
pane, or the keys that open one when there are none, and the footer follows
the focus: the sidebar's keys, or the leader's.

Co-Authored-By: <model> <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01W64zstC3PZojywARTtnSW7
EOF
)"
```

---

### Task 4: The leader, key routing, scroll mode and the picker

With a pane focused, every key goes to the child except the leader. That is what makes a pane worth having — Claude Code's own bindings, `vim`'s, a shell's `Ctrl+R` — and it is why the leader must not be `Ctrl+b`.

**Files:**
- Create: `internal/controlplane/leader.go`
- Test: `internal/controlplane/leader_test.go`
- Modify: `internal/controlplane/input.go`, `internal/controlplane/model.go`, `internal/controlplane/view.go` (Step 5's line in `helpView`), `internal/controlplane/supervisor.go` (Step 4 drops `handleSupervisorKey`'s `//nolint:unused`, since `handlePaneKey` is now its caller)
- Test (existing, updated): `internal/controlplane/input_test.go` (three new `press` cases). `internal/controlplane/panes_test.go` is **not** touched: Task 2 Step 1 already gives `fakeHost` every script variant this task needs, the echoing child included.

**Interfaces:**
- Consumes: the `KeyMap` fields (Task 1); `tab`, `focusArea`, `closeTab`, `quitCmd`, `openOrFocus`, `fakeHost` (Task 2); `canAttach` (step 3); `pane.KeyEvent`, `pane.KeyMod` (4a).
- Produces:
  - `func (m Model) handleLeaderKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd)`
  - `func (m Model) handlePaneKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd)`
  - `func paneKey(msg tea.KeyPressMsg) pane.KeyEvent`
  - `func newPanePicker(width int) *huh.Form`, `const pickerField = "kind"`
  - `func (m Model) updatePicker(msg tea.Msg) (tea.Model, tea.Cmd)`

- [ ] **Step 1: Write the failing test**

Create `internal/controlplane/leader_test.go`:

```go
package controlplane

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/elliottregan/cspace/internal/pane"
)

// leader arms the leader and delivers its second key.
func leader(t *testing.T, m Model, second string) Model {
	t.Helper()
	m = step(t, m, "ctrl+space")
	if !m.leaderArmed {
		t.Fatal("ctrl+space did not arm the leader")
	}
	return step(t, m, second)
}

// openOne opens a Claude pane on the selected row and returns the settled
// model.
func openOne(t *testing.T, h *fakeHost) Model {
	t.Helper()
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = stepPump(t, m, "enter")
	mustTabs(t, m, 1)
	return m
}

func TestPaneKeyConversionMatchesBubbleteasModifiers(t *testing.T) {
	// The cast in paneKey is only safe if the two bitmasks agree, which is
	// what this locks. A drift here would silently send the wrong modifier.
	if pane.KeyMod(tea.ModCtrl) != pane.ModCtrl ||
		pane.KeyMod(tea.ModShift) != pane.ModShift ||
		pane.KeyMod(tea.ModAlt) != pane.ModAlt ||
		pane.KeyMod(tea.ModMeta) != pane.ModMeta {
		t.Fatal("pane.KeyMod and tea.KeyMod have drifted apart")
	}
	got := paneKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl, Text: "c"})
	if got.Code != 'c' || got.Mod != pane.ModCtrl || got.Text != "c" {
		t.Errorf("paneKey = %+v", got)
	}
}

func TestKeysReachTheFocusedPaneAndTheLeaderDoesNot(t *testing.T) {
	h := &fakeHost{t: t}
	m := openOne(t, h)

	// `q` quits from the sidebar. With a pane focused it is a letter.
	m2 := step(t, m, "q")
	if m2.quitting {
		t.Error("q quit the program while a pane had focus")
	}
	// `d` likewise: no teardown confirmation may open behind a focused pane.
	if got := step(t, m, "d"); got.mode != modeNormal {
		t.Errorf("d opened %v while a pane had focus", got.mode)
	}

	// The leader gets through.
	if !step(t, m, "ctrl+space").leaderArmed {
		t.Error("the leader did not arm")
	}
}

func TestLeaderDispatch(t *testing.T) {
	h := &fakeHost{t: t}
	m := openOne(t, h)

	if got := leader(t, m, "h"); got.focus != focusSidebar {
		t.Error("leader h did not focus the sidebar")
	}
	if got := leader(t, m, "?"); !got.showHelp {
		t.Error("leader ? did not open the help overlay")
	}
	if got := leader(t, m, "["); !got.scrolling {
		t.Error("leader [ did not enter scroll mode")
	}
	if got := leader(t, m, "t"); got.mode != modePicker || got.picker == nil {
		t.Error("leader t did not open the new-pane picker")
	}
	// v is bound so the config shape is stable, and deliberately does
	// nothing until rollout step 5.
	if got := leader(t, m, "v"); got.mode != modeNormal || got.notice.text != "" {
		t.Error("leader v did something; image paste is step 5")
	}
}

// settlePicker delivers a key to the open new-pane picker and pumps the
// commands it produces back in until the picker leaves modePicker, then
// hands back the command the settled model produced.
//
// It is the test-side equivalent of model.go's modePicker fall-through,
// exactly as answer() is for the teardown confirmation, and it exists for
// the same reason: huh resolves a Select over two asynchronous round trips
// — the field returns huh.NextField as a *command*, the group turns that
// into nextGroup, and only nextGroupMsg makes the form report
// StateCompleted. A single Update leaves the picker open.
func settlePicker(t *testing.T, m Model, k string) (Model, tea.Cmd) {
	t.Helper()
	mm, cmd := m.Update(press(k))
	m = mm.(Model)
	for i := 0; i < 8 && cmd != nil && m.mode == modePicker; i++ {
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
			if m.mode != modePicker {
				break
			}
		}
		cmd = next
	}
	if m.mode == modePicker {
		t.Fatalf("the picker never settled after %q", k)
	}
	return m, cmd
}

// TestThePickerOpensThePaneItPicks is the round trip the fall-through in
// model.go exists for: without it the picker can be opened and never
// answered, and no unit test that only asserts `mode == modePicker` would
// notice.
func TestThePickerOpensThePaneItPicks(t *testing.T) {
	h := &fakeHost{t: t}
	m := openOne(t, h) // one Claude pane on mercury, focus in the main area
	m = leader(t, m, "t")
	if m.mode != modePicker || m.picker == nil {
		t.Fatal("leader t did not open the new-pane picker")
	}

	// "Shell in the sandbox" is the second option.
	m = step(t, m, "down")
	m, cmd := settlePicker(t, m, "enter")
	if m.picker != nil {
		t.Error("the picker survived its own answer")
	}
	m = pump(t, m, cmd)

	mustTabs(t, m, 2)
	if len(h.opens) != 2 || h.opens[1] != KindShell {
		t.Fatalf("opens = %v, want the picker's shell as the second", h.opens)
	}
}

// TestThePickerRefusesAStoppedSandbox is the gate the picker needs and the
// sidebar keys get for free from forRow. A stopped sandbox keeps its row
// and its container name (4a Task 6), so an ungated open would reach the
// tmux probe and fail with a transport-shaped error a person cannot act on.
func TestThePickerRefusesAStoppedSandbox(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = step(t, m, "j") // issue-42, which testSnapshot has as StateStopped
	if got := m.selectedRow().Name; got != "issue-42" {
		t.Fatalf("selection = %q, want issue-42", got)
	}
	m = leader(t, m, "t")
	m, cmd := settlePicker(t, m, "enter") // "Claude session", the default
	m = pump(t, m, cmd)

	if len(m.tabs) != 0 {
		t.Errorf("tabs = %d, want none: the pick was refused", len(m.tabs))
	}
	if len(h.opens) != 0 {
		t.Errorf("opens = %v, want none on a stopped sandbox", h.opens)
	}
	if !m.notice.isErr || !strings.Contains(m.notice.text, "issue-42") {
		t.Errorf("notice = %+v, want an error naming the sandbox", m.notice)
	}
}

// TestTwoHostShellsAreTwoTabs is the exemption openOrFocus makes for the one
// pane kind that belongs to no sandbox. The existing-tab match is
// (kind, project, sandbox), and a host shell has neither of the last two —
// so without the exemption the second one opened from the same row would
// silently refocus the first, and one opened from a different row would sit
// beside it wearing that row's identity under an identical title.
func TestTwoHostShellsAreTwoTabs(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)

	m = openHostShell(t, m)
	m.focus = focusSidebar
	m = step(t, m, "j") // a different row: issue-42
	m = openHostShell(t, m)

	mustTabs(t, m, 2)
	for i, tb := range m.tabs {
		if tb.kind != KindHostShell {
			t.Fatalf("tab %d is a %v, want a host shell", i, tb.kind)
		}
		if tb.project != "" || tb.sandbox != "" {
			t.Errorf("tab %d carries %q/%q; a host shell belongs to no sandbox",
				i, tb.project, tb.sandbox)
		}
		if got := tb.title(); got != "host · shell" {
			t.Errorf("tab %d title = %q", i, got)
		}
	}
}

// openHostShell drives leader `t` → "Host shell", the fourth option, which
// is the only way to open one: it has no sidebar key, because there is no
// row to press one on.
func openHostShell(t *testing.T, m Model) Model {
	t.Helper()
	m = leader(t, m, "t")
	if m.mode != modePicker || m.picker == nil {
		t.Fatal("leader t did not open the new-pane picker")
	}
	for i := 0; i < 3; i++ {
		m = step(t, m, "down") // Claude, Shell, Supervisor, Host shell
	}
	m, cmd := settlePicker(t, m, "enter")
	return pump(t, m, cmd)
}

func TestLeaderTwiceSendsTheLeaderToTheChild(t *testing.T) {
	// An echoing child, because the interesting claim is that the byte
	// arrived. Asserting Dropped() did not change cannot fail for the
	// reason it names: a 512-slot queue being drained drops nothing whether
	// or not anything was ever queued.
	h := &fakeHost{t: t, echo: true}
	m := openOne(t, h)
	m = leader(t, m, "ctrl+space")
	if m.leaderArmed {
		t.Error("the leader stayed armed after being passed through")
	}
	// Ctrl+Space is NUL, which `cat -v` prints as ^@.
	waitForPaneScreen(t, m.tabs[0], "^@")
}

// waitForPaneScreen polls a pane's rendered screen until want appears. The
// child is a real process on a real pty, so the round trip — key, queue,
// pty, child, pty, emulator — is asynchronous with the Update that started
// it.
func waitForPaneScreen(t *testing.T, tb *tab, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(plain(tb.p.Render()), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the pane never showed %q:\n%s", want, plain(tb.p.Render()))
}

func TestTabNavigationWraps(t *testing.T) {
	h := &fakeHost{t: t}
	m := openOne(t, h)
	m.focus = focusSidebar
	m = stepPump(t, m, "s")
	mustTabs(t, m, 2)

	if got := leader(t, m, "n"); got.focused != 0 {
		t.Errorf("next tab from the last = %d, want 0 (wrapped)", got.focused)
	}
	m.focused = 0
	if got := leader(t, m, "p"); got.focused != 1 {
		t.Errorf("previous tab from the first = %d, want 1 (wrapped)", got.focused)
	}
}

func TestScrollModeMovesAndAnyOtherKeyReturnsToLive(t *testing.T) {
	// The offset is clamped to the history that exists, so this pane has to
	// have some: a pane whose child printed nothing cannot scroll, and
	// asserting that it does would be asserting a bug.
	h := &fakeHost{t: t, history: true}
	m := openOne(t, h)
	waitForHistory(t, m.tabs[0], 10)
	m = leader(t, m, "[")

	m = step(t, m, "up")
	if m.scroll != 1 {
		t.Errorf("scroll after up = %d, want 1", m.scroll)
	}
	m = step(t, m, "down")
	if m.scroll != 0 {
		t.Errorf("scroll after down = %d, want 0", m.scroll)
	}

	m = step(t, m, "x") // any other key
	if m.scrolling {
		t.Error("an ordinary key did not leave scroll mode")
	}
	// ...and it did not reach the child either: leaving scroll mode is what
	// that key did.
	if m.scroll != 0 {
		t.Errorf("scroll = %d after leaving", m.scroll)
	}
}

// waitForHistory polls until a pane's scrollback holds at least n lines.
func waitForHistory(t *testing.T, tb *tab, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if tb.p.ScrollbackLen() >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("scrollback holds %d lines, want %d", tb.p.ScrollbackLen(), n)
}

func TestLeaderXClosesTheFocusedPane(t *testing.T) {
	h := &fakeHost{t: t}
	m := openOne(t, h)
	d := m.tabs[0].detach.(*fakeDetacher)
	m, cmd := leaderCmd(t, m, "x")
	m = pump(t, m, cmd)
	if len(m.tabs) != 0 {
		t.Errorf("tabs = %d, want none", len(m.tabs))
	}
	if d.closed != 1 {
		t.Errorf("detacher closed %d times, want 1", d.closed)
	}
}

// leaderCmd is leader() keeping the command, for the second keys that do
// their work in one.
func leaderCmd(t *testing.T, m Model, second string) (Model, tea.Cmd) {
	t.Helper()
	m = step(t, m, "ctrl+space")
	mm, cmd := m.Update(press(second))
	return mm.(Model), cmd
}

func TestCtrlCGoesToTheChildWhenAPaneHasFocus(t *testing.T) {
	h := &fakeHost{t: t}
	m := openOne(t, h)
	if got := step(t, m, "ctrl+c"); got.quitting {
		t.Error("ctrl+c quit the dashboard instead of interrupting the child")
	}
	m.focus = focusSidebar
	if got := step(t, m, "ctrl+c"); !got.quitting {
		t.Error("ctrl+c did not quit from the sidebar")
	}
}

func TestHelpMentionsTheLeader(t *testing.T) {
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, &fakeHost{t: t})
	m.showHelp = true
	if got := plain(m.helpView(80)); !strings.Contains(got, "leader") {
		t.Errorf("help %q never mentions the leader", got)
	}
}
```

`press("ctrl+space")` must produce the right message; add to `press` in `input_test.go`:

```go
	case "ctrl+space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Mod: tea.ModCtrl}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /Users/elliott/Projects/cspace-control-plane-4b && go test ./internal/controlplane/ -run 'TestLeader|TestPaneKey|TestKeysReach|TestScrollMode|TestCtrlC|TestThePicker|TestTwoHostShells'`
Expected: FAIL to build — `undefined: paneKey`, `m.leaderArmed` undefined.

- [ ] **Step 3: Write `leader.go`**

```go
package controlplane

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/pane"
)

// handleLeaderKey dispatches the key after the leader.
//
// The leader is already disarmed by the caller, so every path here is a
// single-shot: there is no mode to get stuck in, which is the property that
// makes a prefix key safe in a window whose other keys all belong to a
// child.
func (m Model) handleLeaderKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// The leader twice sends the leader itself to the child — the standard
	// escape hatch, and the only way to type Ctrl+Space into a program.
	if key.Matches(msg, m.keys.Leader) {
		if t := m.focusedTab(); t != nil && t.p != nil {
			t.p.SendKey(paneKey(msg))
		}
		return m, nil
	}

	switch {
	case key.Matches(msg, m.keys.FocusSidebar):
		m.focus = focusSidebar
		m.scrolling, m.scroll = false, 0
		return m, nil

	case key.Matches(msg, m.keys.NextTab):
		return m.moveTab(1), nil
	case key.Matches(msg, m.keys.PrevTab):
		return m.moveTab(-1), nil

	case key.Matches(msg, m.keys.NewPane):
		m.mode = modePicker
		m.pending = m.selectedRow()
		m.picker = newPanePicker(mainWidthFor(m.width) - 2)
		return m, m.picker.Init()

	case key.Matches(msg, m.keys.ClosePane):
		t := m.focusedTab()
		if t == nil {
			return m, nil
		}
		return m.startAction(LabelClosePane, m.closeTab(t.id))

	case key.Matches(msg, m.keys.Scroll):
		if t := m.focusedTab(); t == nil || t.p == nil {
			return m, nil
		}
		m.scrolling = true
		return m, nil

	case key.Matches(msg, m.keys.Live):
		m.scrolling, m.scroll = false, 0
		return m, nil

	case key.Matches(msg, m.keys.Help):
		m.showHelp = !m.showHelp
		return m, nil

	case key.Matches(msg, m.keys.Quit):
		// No confirmation: tmux holds every session. The panes are still
		// closed properly on the way out, so no tmux client is left attached
		// inside a sandbox and no client record is stranded for the next
		// start's sweep to find.
		m.quitting = true
		return m, m.quitCmd()

	case key.Matches(msg, m.keys.PasteImage):
		// Bound so the config shape is stable and the footer can name it;
		// rollout step 5 is what makes it act. Doing nothing quietly beats a
		// "not implemented" notice on a key the footer advertises.
		return m, nil
	}
	return m, nil
}

// moveTab steps the focus through the tabs, wrapping.
func (m Model) moveTab(dir int) Model {
	if len(m.tabs) == 0 {
		return m
	}
	m.focused = (m.focused + dir + len(m.tabs)) % len(m.tabs)
	m.focus = focusMain
	m.scrolling, m.scroll = false, 0
	return m
}

// handlePaneKey is what a focused pane's keyboard does: in scroll mode, move
// through the history; otherwise every key goes to the child.
func (m Model) handlePaneKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	t := m.focusedTab()
	if t == nil {
		return m, nil
	}
	if t.sup != nil {
		return m.handleSupervisorKey(t, msg)
	}
	if t.p == nil {
		return m, nil
	}

	if m.scrolling {
		_, rows := m.paneSize()
		switch msg.String() {
		case "up":
			m.scroll = clampScroll(m.scroll+1, t.p.ScrollbackLen())
		case "down":
			m.scroll = clampScroll(m.scroll-1, t.p.ScrollbackLen())
		case "pgup":
			m.scroll = clampScroll(m.scroll+rows, t.p.ScrollbackLen())
		case "pgdown":
			m.scroll = clampScroll(m.scroll-rows, t.p.ScrollbackLen())
		case "home":
			m.scroll = t.p.ScrollbackLen()
		case "end":
			m.scroll = 0
		default:
			// Any other key returns to live, and is consumed doing so —
			// which is what the design says, and what stops a stray letter
			// landing in a child a person thought they were only reading.
			m.scrolling, m.scroll = false, 0
		}
		return m, nil
	}

	if _, _, exited := t.p.Exited(); exited {
		// There is nothing to type into: the child is gone and the pane has
		// already been reaped, so a key here would be queued for a writer
		// that has exited. Leader x is what the tab is still on screen for.
		return m, nil
	}
	t.p.SendKey(paneKey(msg))
	return m, nil
}

func clampScroll(n, max int) int {
	if n < 0 {
		return 0
	}
	if n > max {
		return max
	}
	return n
}

// paneKey converts a Bubble Tea keypress into the engine's own event.
//
// The modifier is a cast, not a table: pane.KeyMod's constants are declared
// to be ultraviolet's bit for bit, and bubbletea v2's tea.KeyMod *is*
// ultraviolet's. TestPaneKeyConversionMatchesBubbleteasModifiers is what
// keeps that true.
func paneKey(msg tea.KeyPressMsg) pane.KeyEvent {
	return pane.KeyEvent{
		Code: msg.Code,
		Mod:  pane.KeyMod(msg.Mod),
		Text: msg.Text,
	}
}

// pickerField is the form key the new-pane picker's answer comes back under.
const pickerField = "kind"

// newPanePicker is the leader's `t`: the four things a tab can be.
//
// The host shell is in the list and has no sidebar key, because it belongs
// to no sandbox — there is no row to press a key on. That is the whole
// reason the picker exists rather than a fourth sidebar binding.
func newPanePicker(width int) *huh.Form {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))
	return huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[Kind]().
				Key(pickerField).
				Title("New pane").
				Options(
					huh.NewOption("Claude session", KindClaude),
					huh.NewOption("Shell in the sandbox", KindShell),
					huh.NewOption("Supervisor events", KindSupervisor),
					huh.NewOption("Host shell", KindHostShell),
				),
		),
	).WithKeyMap(km).WithShowHelp(false).WithShowErrors(false).WithWidth(width)
}

// `huh.NewSelect[T comparable]() *Select[T]` and
// `huh.NewOption[T comparable](key string, value T) Option[T]` are both
// generic in v2.0.3, so Kind — an int — is a legal type argument.

// updatePicker feeds the picker and acts on its answer. Like the teardown
// confirmation it resolves m.pending — the row the picker was opened
// against — so a poll landing while it is open cannot retarget it.
func (m Model) updatePicker(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.picker == nil {
		m.mode = modeNormal
		return m, nil
	}
	form, cmd := m.picker.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		m.picker = f
	}
	switch m.picker.State {
	case huh.StateCompleted:
		// huh/v2 has no typed getter beyond GetString/GetInt/GetBool, so
		// the Select's value comes back through Get(key) any. A failed
		// assertion yields KindClaude, which is the option a person pressing
		// Enter on the default would have got anyway.
		kind, _ := m.picker.Get(pickerField).(Kind)
		target := m.pending
		m.mode, m.picker = modeNormal, nil
		m.pending = control.Row{}
		// The picker is the one opener that does not come through forRow,
		// so the gate forRow applies to `enter`/`s`/`a` is applied here
		// instead. It matters because 4a Task 6 keeps a stopped sandbox on
		// screen WITH its container name, so an ungated open would sail
		// past this and die at the tmux probe with a transport-shaped
		// error. The host shell belongs to no sandbox and is always
		// allowed. (A supervisor view on a stopped sandbox is still
		// reachable — from the sidebar's `a`, which canSupervisor permits
		// because the event log lives on the host.)
		if kind != KindHostShell && !canAttach(target) {
			m.notice = notice{
				text:  "cannot open a " + kind.String() + " pane: " + target.Name + " is not running",
				isErr: true,
			}
			return m, nil
		}
		return m.openOrFocus(kind, target)
	case huh.StateAborted:
		m.mode, m.picker = modeNormal, nil
		m.pending = control.Row{}
		return m, nil
	}
	return m, cmd
}
```

- [ ] **Step 4: Route the keys**

In `internal/controlplane/input.go`, replace `handleKey`:

```go
// handleKey routes a keypress to whatever owns the keyboard: a modal, the
// leader, the focused pane, or the sidebar.
func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.notice.isErr {
		m.notice = notice{}
	}
	switch m.mode {
	case modeConfirmDown:
		return m.updateConfirm(msg)
	case modeInput:
		return m.handleInputKey(msg)
	case modePicker:
		return m.updatePicker(msg)
	}
	if m.leaderArmed {
		m.leaderArmed = false
		return m.handleLeaderKey(msg)
	}
	if key.Matches(msg, m.keys.Leader) {
		m.leaderArmed = true
		return m, nil
	}
	if m.showHelp {
		// The overlay swallows the very next key, whatever it is, and that
		// check has to sit HERE rather than at the top of handleNormalKey
		// where step 3 left it. With a pane focused the route below never
		// reaches handleNormalKey, so the overlay would be dismissible only
		// by a second leader `?` while every other key went to a child the
		// overlay is covering — typing blind into Claude while reading help.
		m.showHelp = false
		return m, nil
	}
	if m.focus == focusMain && m.focusedTab() != nil {
		// Every key but the leader belongs to the child. That is the point
		// of a pane, and it is why the leader must not be ctrl+b.
		return m.handlePaneKey(msg)
	}
	return m.handleNormalKey(msg)
}
```

Add `leaderArmed bool` to the `Model` struct, and delete the now-duplicated
`if m.showHelp { … }` block from the top of `handleNormalKey` — the sidebar
path reaches the new check first, so leaving both would be dead code.

`handlePaneKey` is the first caller of the supervisor stub's
`handleSupervisorKey`, so **delete the `//nolint:unused // filled in by Task
4, which is the first caller` line above it in
`internal/controlplane/supervisor.go`** as part of this step.

**Add the `modePicker` arm to `model.go`'s trailing mode switch** — the
non-key fall-through at the bottom of `Update`, the one that already has
`modeInput` and `modeConfirmDown`:

```go
	case modePicker:
		if m.picker != nil {
			return m.updatePicker(msg)
		}
```

This is not symmetry for its own sake, and without it the picker is
decorative. `handleKey`'s `case modePicker` only ever sees a
`tea.KeyPressMsg`, and huh resolves a `Select` over **two asynchronous round
trips**: the field returns `huh.NextField` as a *command*, the group turns
that message into `nextGroup`, and only when the form receives
`nextGroupMsg` does it report `StateCompleted`. Those two messages are not
key presses, so with no arm here they reach nothing, the form never
completes, and leader `t` → Enter opens no pane at all. `input_test.go`'s
`answer()` helper documents the same mechanism for the teardown
confirmation and says in so many words that production gets it from this
fall-through. `TestThePickerOpensThePaneItPicks` is the test that fails
without it.

Add the matching case to `leader_test.go`:

```go
func TestAKeyDismissesTheHelpOverlayWithAPaneFocused(t *testing.T) {
	h := &fakeHost{t: t}
	m := openOne(t, h)
	m = leader(t, m, "?")
	if !m.showHelp {
		t.Fatal("leader ? did not open the help overlay")
	}
	// `x` with a pane focused would otherwise go to the child, leaving the
	// overlay up and the keyboard pointed at something the overlay covers.
	m = step(t, m, "x")
	if m.showHelp {
		t.Error("an ordinary key did not dismiss the overlay")
	}
	// ...and it was consumed doing so: the leader is not armed, no mode
	// opened, and the tab is still there.
	if m.leaderArmed || m.mode != modeNormal || len(m.tabs) != 1 {
		t.Errorf("the dismissing key did something else: armed=%v mode=%v tabs=%d",
			m.leaderArmed, m.mode, len(m.tabs))
	}
}
```

In `model.go`'s `Update`, replace the force-quit branch:

```go
	case tea.KeyPressMsg:
		// Ctrl+C quits from the sidebar and from a modal, where a dashboard
		// with no way out would be a bug. With a pane focused it belongs to
		// the child — interrupting Claude is the single most-used key in
		// there — so the way out is the leader's quit.
		if key.Matches(msg, forceQuit) && !(m.focus == focusMain && m.focusedTab() != nil) {
			m.quitting = true
			return m, m.quitCmd()
		}
		return m.handleKey(msg)
```

and route pasted text to the focused pane, in the same `switch`:

```go
	case tea.PasteMsg:
		// A text paste goes to the focused pane, which brackets it when the
		// child asked for bracketing. With the sidebar focused there is
		// nothing to paste into.
		if t := m.focusedTab(); t != nil && t.p != nil && m.focus == focusMain {
			t.p.Paste(msg.Content)
		}
		return m, nil
```

Make the sidebar's `q` close panes too, in `handleNormalKey`:

```go
	case key.Matches(msg, m.keys.Quit):
		m.quitting = true
		return m, m.quitCmd()
```

- [ ] **Step 5: Name the leader in the help overlay**

In `helpView`, add a line to `lines`:

```go
		styleDim.Render(fit("every other key goes to the focused pane; ⌃Space is the leader", width)),
```

- [ ] **Step 6: Run the tests**

Run: `cd /Users/elliott/Projects/cspace-control-plane-4b && go test ./internal/controlplane/ -v`
Expected: PASS.

- [ ] **Step 7: Run the gate and commit**

```bash
cd /Users/elliott/Projects/cspace-control-plane-4b
make check && make test-race
git add internal/controlplane
git commit -m "$(cat <<'EOF'
Route keys to the focused pane behind a leader

With a pane focused every key goes to the child, including q, d and Ctrl+C —
the interrupt is the most-used key in a Claude session — and the leader is
the only way back out. Leader twice sends the leader itself. Scroll mode
moves through the history and any other key returns to live.

Co-Authored-By: <model> <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01W64zstC3PZojywARTtnSW7
EOF
)"
```

---

### Task 5: The detach protocol's host side and the startup sweep

A dead host side never reaches the guest. Rollout step 2 shipped `control.BeginAttach` and `Attachment.Close`, and rollout step 1 made `cspace attach` use them; Task 2 wired `Close` into pane close and quit. What is still missing is step 4 of the design's protocol: the sweep that reaps records left behind by a control plane or a `cspace attach` that died without detaching.

**Files:**
- Modify: `internal/control/attach.go`, `internal/control/client.go`, `internal/control/control.go` (Step 3 adds `controlPlaneRoot`)
- Test: `internal/control/attach_test.go`
- Test: `internal/controlplane/detach_test.go`
- Modify: `internal/controlplane/model.go` (`Init`, the `sweepMsg` case), `internal/controlplane/panes.go` (Step 5 adds `sweepMsg` and `sweepCmd` beside the other pane messages)
- Test (existing, updated): `internal/controlplane/model_test.go` (`TestInitKicksAllThreeCadences` → `TestInitKicksThreeCadencesAndTheStartupSweep`)

**Interfaces:**
- Consumes: `ClientRecord`, `Tmux.ListClients`, `Tmux.DetachClient`, `ErrClientGone`, `ControlPlaneDir`, `containerName` (step 2); `PaneHost.Sweep` (Task 2).
- Produces:
  - `type SweepResult struct { Kept, Detached, Deleted, Errors int }`
  - `func (c *Client) SweepClientRecords(ctx context.Context) (SweepResult, error)`
  - `Options.ProcessAlive func(pid int) bool` (nil means "ask the OS")
  - on `Model`: `sweepCmd()` and `sweepMsg`

- [ ] **Step 1: Write the failing control test**

Add to `internal/control/attach_test.go`:

```go
func TestSweepClientRecords(t *testing.T) {
	home := t.TempDir()
	dir := ControlPlaneDir(home, "demo", "mercury")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("cspace-claude.dev-pts-1.json",
		`{"session":"cspace-claude","tty":"/dev/pts/1","pid":101,"at":"2026-09-18T00:00:00Z"}`)
	write("cspace-shell.dev-pts-2.json",
		`{"session":"cspace-shell","tty":"/dev/pts/2","pid":102,"at":"2026-09-18T00:00:00Z"}`)
	write("cspace-claude.dev-pts-3.json",
		`{"session":"cspace-claude","tty":"/dev/pts/3","pid":103,"at":"2026-09-18T00:00:00Z"}`)
	write("garbage.json", `not json at all`)
	// The lock is not a record and must survive.
	write("attach.lock", "")

	// fakeExec, testTmux and fakeContainers all already exist in this
	// package — tmux_test.go and snapshot_test.go — and are reused rather
	// than shadowed. testTmux goes through NewTmux, so the driver's
	// memoization map is initialized; a composite `&Tmux{…}` leaves it nil,
	// which is safe only for as long as nothing on this path calls Present.
	f := &fakeExec{reply: func(_ int, cmdline []string) (string, int, error) {
		if len(cmdline) > 1 && cmdline[1] == "list-clients" {
			if cmdline[3] == "cspace-claude" {
				return "/dev/pts/1\n/dev/pts/3\n", 0, nil
			}
			return "", 0, nil
		}
		return "", 0, nil
	}}
	c := New(Options{
		Home: home,
		Containers: &fakeContainers{out: []applecontainer.ContainerSummary{
			{Name: "cspace-demo-mercury", State: "running"},
		}},
		Tmux: testTmux(f),
		// 101 is dead, 102 is dead, 103 is the process that is still running
		// this very attach.
		ProcessAlive: func(pid int) bool { return pid == 103 },
	})

	res, err := c.SweepClientRecords(context.Background())
	if err != nil {
		t.Fatalf("SweepClientRecords: %v", err)
	}
	// Two dead ttys in one pass, and both are handled: pts/1 is still listed
	// so it is detached, pts/2 is not so its record is just deleted.
	if res.Detached != 1 {
		t.Errorf("detached = %d, want 1", res.Detached)
	}
	if res.Deleted != 3 { // pts/1, pts/2 and the unparseable file
		t.Errorf("deleted = %d, want 3", res.Deleted)
	}
	if res.Kept != 1 {
		t.Errorf("kept = %d, want 1 — the live attach's record", res.Kept)
	}
	if res.Errors != 0 {
		t.Errorf("errors = %d, want 0 — nothing in this sweep failed", res.Errors)
	}
	var detached []string
	for _, call := range f.recorded() {
		if len(call) > 3 && call[1] == "detach-client" {
			detached = append(detached, call[3])
		}
	}
	if len(detached) != 1 || detached[0] != "/dev/pts/1" {
		t.Errorf("detached ttys = %v, want [/dev/pts/1]", detached)
	}
	// The live attach's record survives, and so does the lock.
	for _, keep := range []string{"cspace-claude.dev-pts-3.json", "attach.lock"} {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Errorf("%s was removed: %v", keep, err)
		}
	}
}

func TestSweepReapsRecordsWhoseContainerIsGone(t *testing.T) {
	home := t.TempDir()
	dir := ControlPlaneDir(home, "demo", "ghost")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cspace-claude.dev-pts-9.json"),
		[]byte(`{"session":"cspace-claude","tty":"/dev/pts/9","pid":999}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	f := &fakeExec{}
	c := New(Options{
		Home:         home,
		Containers:   &fakeContainers{}, // List returns nothing: the sandbox is gone
		Tmux:         testTmux(f),
		ProcessAlive: func(int) bool { return false },
	})

	res, err := c.SweepClientRecords(context.Background())
	if err != nil {
		t.Fatalf("SweepClientRecords: %v", err)
	}
	if res.Deleted != 1 {
		t.Errorf("deleted = %d, want 1", res.Deleted)
	}
	for _, call := range f.recorded() {
		if len(call) > 1 && call[1] == "detach-client" {
			t.Error("the sweep tried to detach inside a container that is gone")
		}
	}
}

// A sweep that cannot tell which containers exist must not read that as
// "none of them do", and a `list-clients` it could not run must not be read
// as "tmux says nothing is attached". Both are the same mistake — taking the
// absence of an answer for an answer — and both cost the same thing: the
// records of every live client on the machine, after which nothing can ever
// reap them, because the record is the only handle the next sweep has.
//
// Walk it through. `container ls` fails, so liveContainers reports
// known=false and sweepDir is told containerKnownGone=false: the delete
// branch for a container that is gone is off the table. The record's pid is
// dead, so the sweep asks tmux — and that exec fails too, which is what an
// unreachable container does. ListClients returns an error only for a failed
// exec (tmux answering "no such session" is a nil error and an empty list),
// so the listing is not `answered` and the record is kept: Kept 1, Errors 1,
// Deleted 0, and no detach attempted.
//
// It fails against the shape this replaced, which is the point of writing
// it. There, `!known` was folded into `containerLive = true`, and a listing
// that errored produced the same empty tty set as a listing that legitimately
// came back empty — so the tty was "not found", control fell past the detach
// block into an unconditional os.Remove, and both the first assertion
// (Deleted 0) and the last (the file is still there) failed.
func TestSweepLeavesRecordsAloneWhenItCannotListContainers(t *testing.T) {
	home := t.TempDir()
	dir := ControlPlaneDir(home, "demo", "mercury")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "cspace-claude.dev-pts-1.json")
	if err := os.WriteFile(path,
		[]byte(`{"session":"cspace-claude","tty":"/dev/pts/1","pid":101}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// A container the host cannot list is a container it cannot exec into
	// either, so the exec has to fail here too. A bare `&fakeExec{}` replies
	// ("", 0, nil), which is a *successful* empty listing — real evidence the
	// sweep is entitled to act on, and not this case at all.
	unreachable := &fakeExec{reply: func(_ int, _ []string) (string, int, error) {
		return "", 0, errors.New("container exec: connection refused")
	}}
	c := New(Options{
		Home:         home,
		Containers:   &fakeContainers{err: errors.New("container ls: connection refused")},
		Tmux:         testTmux(unreachable),
		ProcessAlive: func(int) bool { return false },
	})

	res, err := c.SweepClientRecords(context.Background())
	if err != nil {
		t.Fatalf("SweepClientRecords: %v", err)
	}
	if res.Deleted != 0 {
		t.Errorf("deleted = %d, want 0 — a failed list is not evidence the container is gone", res.Deleted)
	}
	if res.Kept != 1 || res.Errors != 1 {
		t.Errorf("kept = %d, errors = %d, want 1 and 1 — kept, and counted as unfinished work", res.Kept, res.Errors)
	}
	for _, call := range unreachable.recorded() {
		if len(call) > 1 && call[1] == "detach-client" {
			t.Error("the sweep detached a tty it never saw listed")
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the record was removed: %v", err)
	}
}

// The other half of the same rule: a container list that failed must not
// freeze the sweep either. tmux is the authority on who is attached, so when
// the exec does reach it, its answer is acted on — here it answers that the
// session lists no clients, which means the dead attach's client is already
// detached and the record is nothing but litter.
func TestSweepActsOnTmuxEvidenceWhenTheContainerListFails(t *testing.T) {
	home := t.TempDir()
	dir := ControlPlaneDir(home, "demo", "venus")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "cspace-claude.dev-pts-4.json")
	if err := os.WriteFile(path,
		[]byte(`{"session":"cspace-claude","tty":"/dev/pts/4","pid":104}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	c := New(Options{
		Home:       home,
		Containers: &fakeContainers{err: errors.New("container ls: connection refused")},
		// The default reply — exit 0, no output — is tmux answering that
		// the session has no clients.
		Tmux:         testTmux(&fakeExec{}),
		ProcessAlive: func(int) bool { return false },
	})

	res, err := c.SweepClientRecords(context.Background())
	if err != nil {
		t.Fatalf("SweepClientRecords: %v", err)
	}
	if res.Deleted != 1 || res.Kept != 0 || res.Errors != 0 {
		t.Errorf("deleted = %d, kept = %d, errors = %d, want 1, 0 and 0",
			res.Deleted, res.Kept, res.Errors)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("the stale record survived a listing that said its client was gone")
	}
}
```

**Declare no new fakes.** `attach_test.go` already imports `context`,
`errors`, `os`, `path/filepath`, `strings` and `time`; the one import to add is
`github.com/elliottregan/cspace/internal/substrate/applecontainer`, for
`ContainerSummary`. Everything else these tests need is already in the
package and must be reused rather than redeclared:

- `fakeExec` (tmux_test.go:15) — an `Execer` whose `reply` closure answers by
  argv and whose `recorded()` returns every call, which is how the detaches
  are asserted.
- `testTmux(f *fakeExec) *Tmux` (tmux_test.go:42) — builds through `NewTmux`,
  so `present` is a real map and a later `Present` call cannot panic on a nil
  write.
- `fakeContainers` (snapshot_test.go:28) — **already declared in this
  package**, with fields `out`, `err`, `stats`, … and `List` returning
  `f.out, f.err`. A second `type fakeContainers struct{ names []string }` in
  `attach_test.go` is a duplicate type and duplicate methods in one package:
  the test build fails outright.

The argv shapes the reply closure keys on are `tmux list-clients -t <session>
-F '#{client_tty}'` and `tmux detach-client -t <tty>` — index 3 is the
session or the tty in both.

- [ ] **Step 2: Run to verify it fails**

Run: `cd /Users/elliott/Projects/cspace-control-plane-4b && go test ./internal/control/ -run TestSweep`
Expected: FAIL to build — `undefined: SweepClientRecords`, `unknown field ProcessAlive`.

- [ ] **Step 3: Implement the sweep**

First give the layout one owner. `ControlPlaneDir` (control.go:55) already
knows where the bookkeeping lives, and a sweep that re-derived the same path
as a literal would be a second definition that has to be changed twice — the
very thing `paths.go`'s header warns about. In
`internal/control/control.go`, hoist the root out of `ControlPlaneDir`:

```go
// controlPlaneRoot is the directory ControlPlaneDir hangs off, and the one
// SweepClientRecords walks. It is factored out because two definitions of
// this path is one too many: a sweep that looked anywhere but where the
// writer writes would find nothing, report a clean pass, and leave every
// stale client attached.
func controlPlaneRoot(home string) string {
	return filepath.Join(home, ".cspace", "controlplane")
}
```

and make `ControlPlaneDir` call it, keeping its own doc comment as it is:

```go
func ControlPlaneDir(home, project, sandbox string) string {
	return filepath.Join(controlPlaneRoot(home), project, sandbox)
}
```

Then add to `internal/control/attach.go`:

```go
// SweepResult counts what one sweep did.
//
// Every record the sweep looked at is either kept or deleted, so
// Kept+Deleted is the number of records it saw. Detached counts the subset
// of the deleted whose tmux client had to be detached first — it is not a
// separate outcome, and adding it to Deleted double-counts.
//
// Errors is the count of unfinished work, and it has two sources: a record
// kept because something could not be asked rather than because the
// evidence said to keep it, and a directory that could not be listed at all
// (whose records were therefore never seen, and so appear in neither Kept
// nor Deleted). Either way a sweep with Errors > 0 has work left that the
// next sweep will have to redo, which is the only claim this field makes.
type SweepResult struct {
	Kept     int // records left in place
	Detached int // clients tmux still listed, now detached (a subset of Deleted)
	Deleted  int // record files removed
	Errors   int // unfinished work: a record that could not be decided, or a directory that could not be read
}

// SweepClientRecords is step 4 of the design's detach protocol: for every
// client record under ~/.cspace/controlplane/ whose host process is gone,
// detach the client tmux still lists and delete the record.
//
// It is the backstop for the two ways a client outlives its owner: the
// control plane or a `cspace attach` crashing before its own Close ran, and
// a host terminal closing hard. Run it once at startup, before any pane
// opens — a stale record is inert, but the tmux client it names is not, and
// a sandbox accumulating attached clients is one whose next attach shares a
// screen with a ghost.
//
// Every record is processed in one pass, including several in one sandbox's
// directory: a crash strands one record per pane that was open, and reaping
// only the first would need as many restarts as there were panes.
//
// **A record is deleted only on evidence, and every delete is its own
// branch naming the evidence it has:**
//
//   - the file does not parse — it names no client, so it can only be litter;
//   - the host pid is dead and an *authoritative* container list does not
//     have this container — there is no tmux server left to talk to, so the
//     record is deleted without an exec that would only fail slowly;
//   - the host pid is dead and tmux answered that it does not list this tty
//     — the client is already detached, so only the record is left;
//   - the host pid is dead, tmux listed the tty, and the detach succeeded
//     (or said the client was already gone).
//
// Everything else keeps the record: a live pid, a container list that could
// not be believed (liveContainers' second return), a `list-clients` exec
// that failed, and a detach that failed. The asymmetry is the whole design.
// A record kept one sweep too long costs one exec next time; a record
// deleted without evidence throws away the only handle any later sweep has
// on a client that may still be attached, and nothing can reap it after
// that. Absence of an answer is never an answer.
func (c *Client) SweepClientRecords(ctx context.Context) (SweepResult, error) {
	var res SweepResult
	if c.home == "" {
		return res, ErrNoHome
	}
	root := controlPlaneRoot(c.home)

	projects, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return res, nil // nothing has ever attached
		}
		return res, fmt.Errorf("read %s: %w", root, err)
	}

	live, known := c.liveContainers(ctx)
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		sandboxes, err := os.ReadDir(filepath.Join(root, project.Name()))
		if err != nil {
			// Counted, not swallowed: whatever records are under here were
			// not looked at, so this sweep left work behind — which is
			// exactly what SweepResult.Errors means.
			res.Errors++
			continue
		}
		for _, sandbox := range sandboxes {
			if !sandbox.IsDir() {
				continue
			}
			dir := filepath.Join(root, project.Name(), sandbox.Name())
			container := containerName(project.Name(), sandbox.Name())
			// "Known gone" needs both halves: a list that can be believed
			// *and* this container missing from it. A list that failed says
			// nothing at all, and the sweep falls through to asking tmux —
			// which for a container that really is gone costs one failed
			// exec per session and keeps the records for the next sweep.
			c.sweepDir(ctx, dir, container, known && !live[container], &res)
		}
	}
	return res, nil
}

// sweepDir handles one sandbox's records. containerKnownGone is true only
// when an authoritative container list did not mention this container; a
// list that could not be taken arrives here as false, because "gone" and
// "unanswered" authorise different things.
func (c *Client) sweepDir(ctx context.Context, dir, container string, containerKnownGone bool, res *SweepResult) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Same rule as the per-project read above: a directory that could
		// not be listed is work this sweep did not do.
		res.Errors++
		return
	}

	// One list-clients per session, not per record: a sandbox with a Claude
	// pane and a shell pane has two sessions and any number of records.
	//
	// `answered` is the second half of the evidence. ListClients returns an
	// error only when the exec itself failed — an unreachable container —
	// and returns an empty list with a nil error when tmux answered that
	// there is no such session or no server. Those two look identical in the
	// tty set and mean opposite things, so the set alone can never be read.
	type listing struct {
		ttys     map[string]bool
		answered bool
	}
	listed := map[string]listing{}
	clientsFor := func(session string) listing {
		if got, ok := listed[session]; ok {
			return got
		}
		got := listing{ttys: map[string]bool{}}
		ttys, err := c.tmux.ListClients(ctx, container, session)
		if err == nil {
			got.answered = true
			for _, tty := range ttys {
				got.ttys[tty] = true
			}
		}
		listed[session] = got
		return got
	}

	// remove is the only place a record file is unlinked, so every delete in
	// this function is one of the four evidence branches below. A failed
	// unlink leaves the record, which is the kept-plus-error outcome.
	remove := func(path string) {
		if os.Remove(path) == nil {
			res.Deleted++
			return
		}
		res.Kept++
		res.Errors++
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue // attach.lock, and anything else that is not a record
		}
		path := filepath.Join(dir, name)

		rec, err := readClientRecord(path)
		if err != nil {
			// Evidence: the file names no client, so there is nothing it
			// could be a handle on. Litter.
			remove(path)
			continue
		}
		if c.processAlive(rec.PID) {
			// Evidence: the process that owns this client is running. Its
			// own Close detaches and deletes; the sweep must not race it.
			res.Kept++
			continue
		}
		if containerKnownGone {
			// Evidence: the container is not there, so neither is the tmux
			// server. Delete without an exec.
			remove(path)
			continue
		}

		clients := clientsFor(rec.Session)
		if !clients.answered {
			// No evidence either way: the exec failed, so this container is
			// unreachable, not gone. This is the branch the whole shape of
			// the function exists for — deleting here is what would wipe
			// live clients' records on one bad `container ls` plus one
			// failed exec.
			res.Kept++
			res.Errors++
			continue
		}
		if !clients.ttys[rec.TTY] {
			// Evidence: tmux answered and does not list this tty, so the
			// client is already detached and only the record is left.
			remove(path)
			continue
		}
		if err := c.tmux.DetachClient(ctx, container, rec.TTY); err != nil && !errors.Is(err, ErrClientGone) {
			// Leave the record: the next sweep is the retry, and deleting
			// it would hide a client that is still attached. ErrClientGone
			// is not a failure — it means the detach had already happened.
			res.Kept++
			res.Errors++
			continue
		}
		// Evidence: the client was listed and is now detached.
		res.Detached++
		remove(path)
	}
}

// readClientRecord parses one record file.
func readClientRecord(path string) (ClientRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ClientRecord{}, err
	}
	var rec ClientRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return ClientRecord{}, err
	}
	if rec.TTY == "" || rec.Session == "" || rec.PID <= 0 {
		return ClientRecord{}, errors.New("control: incomplete client record")
	}
	return rec, nil
}

// liveContainers is the set of container names the substrate currently has,
// running or not — and whether that set can be believed.
//
// The second return is the whole point of the function's shape. An empty map
// and an unanswerable question look identical, and a caller that reads "not
// in the map" as "the container is gone, delete the record without even
// trying to detach" would, on a substrate with no ContainerCLI or one
// transient `container ls` failure at `cspace tui` startup, delete every
// client record under ~/.cspace/controlplane/ — including the ones naming
// clients that really are attached, after which nothing can ever reap them,
// because the record is the only handle the next sweep has. false means
// "ask again next time".
func (c *Client) liveContainers(ctx context.Context) (map[string]bool, bool) {
	out := map[string]bool{}
	if c.containers == nil {
		return out, false
	}
	list, err := c.containers.List(ctx)
	if err != nil {
		return out, false
	}
	for _, ct := range list {
		out[ct.Name] = true
	}
	return out, true
}
```

Add the imports `encoding/json`, `io/fs`, `path/filepath`, `strings` if `attach.go` lacks any of them (it already has `encoding/json`, `io/fs`, `path/filepath` and `strings`).

- [ ] **Step 4: Add the `processAlive` seam**

In `internal/control/client.go`:

- in `Options`:

```go
	// ProcessAlive reports whether a host pid is still running. Injected so
	// the sweep's tests do not depend on what happens to be running on the
	// machine; nil asks the operating system.
	ProcessAlive func(pid int) bool
```

- in `Client`: `processAlive func(pid int) bool`
- in `New`, before the return:

```go
	alive := o.ProcessAlive
	if alive == nil {
		alive = processAlive
	}
```

and `processAlive: alive,` in the struct literal.

- at the bottom of `client.go`:

```go
// processAlive reports whether a pid is still running. Signal 0 performs the
// error checking without sending anything, which is the portable way to ask;
// EPERM means the process exists and belongs to someone else, which for this
// purpose is still alive.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
```

with `errors` and `syscall` imported.

- [ ] **Step 5: Run the sweep at startup**

In `internal/controlplane/model.go`:

- add the message beside the others in `panes.go`:

```go
	// sweepMsg reports the startup sweep, which is advisory: what it found
	// was already broken, and there is nothing for a person to do about it.
	sweepMsg struct {
		n   int
		err error
	}
```

- add the command in `panes.go`:

```go
// sweepCmd reaps the client records of attaches whose host process is gone.
// It runs once, from Init, before any pane can open.
func (m Model) sweepCmd() tea.Cmd {
	host := m.host
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), openTimeout)
		defer cancel()
		n, err := host.Sweep(ctx)
		return sweepMsg{n: n, err: err}
	}
}
```

- add it to `Init`:

```go
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.sweepCmd(),
		func() tea.Msg { return fastTickMsg{} },
		func() tea.Msg { return mediumTickMsg{} },
		func() tea.Msg { return slowTickMsg{} },
	)
}
```

`Init` now batches four commands, so step 3's
`TestInitKicksAllThreeCadences` (model_test.go:161) fails on
`len(batch) != 3`. Rename it and widen the count — the batch is three
cadences plus the one-shot startup sweep:

```go
func TestInitKicksThreeCadencesAndTheStartupSweep(t *testing.T) {
	m := New(&fakeData{snap: testSnapshot()}, &recordingActor{}, nopPaneHost{}, NewKeyMap(nil))
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init must start the poll loop")
	}
	// tea.Batch returns a BatchMsg carrying one Cmd per member.
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("Init should batch its ticks, got %T", cmd())
	}
	if len(batch) != 4 {
		t.Errorf("Init started %d commands, want 4 — three cadences plus the sweep", len(batch))
	}
}
```

- handle it in `Update`:

```go
	case sweepMsg:
		// Advisory: a swept record was already stale. Only a failure is
		// worth a line, and only a quiet one. isErr, because this branch
		// schedules no expiry — a notice that is neither timed nor
		// dismissible sits in the footer for the rest of the session.
		if msg.err != nil {
			m.notice = notice{text: "attach sweep: " + msg.err.Error(), isErr: true}
		} else if msg.n > 0 {
			m.notice = notice{text: fmt.Sprintf("swept %d stale tmux client(s)", msg.n)}
			m.noticeGen++
			gen := m.noticeGen
			return m, tea.Tick(noticeLifetime, func(time.Time) tea.Msg { return noticeExpireMsg{gen: gen} })
		}
		return m, nil
```

(`fmt` is already imported by `view.go`; add it to `model.go`.)

- [ ] **Step 6: Write the controlplane-side test**

Create `internal/controlplane/detach_test.go`:

```go
package controlplane

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestInitRunsTheStartupSweep(t *testing.T) {
	h := &fakeHost{t: t}
	m := New(&fakeData{snap: testSnapshot()}, &recordingActor{}, h, NewKeyMap(nil))
	for _, msg := range drain(m.Init()) {
		if _, ok := msg.(sweepMsg); ok {
			if h.sweeps != 1 {
				t.Errorf("sweeps = %d, want 1", h.sweeps)
			}
			return
		}
	}
	t.Fatal("Init never swept")
}

func TestQuitClosesEveryPaneBeforeItQuits(t *testing.T) {
	h := &fakeHost{t: t}
	m := openOne(t, h)
	d := m.tabs[0].detach.(*fakeDetacher)
	m.focus = focusSidebar

	mm, cmd := m.Update(press("q"))
	m = mm.(Model)
	if !m.quitting {
		t.Fatal("q did not quit")
	}
	if cmd == nil {
		t.Fatal("quit produced no command; the panes were never closed")
	}
	// The quit command closes every pane and then yields QuitMsg, so running
	// it is what performs the detach — and the message proves the program
	// still ends afterwards.
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("the quit command did not end the program")
	}
	if d.closed != 1 {
		t.Errorf("detacher closed %d times on quit, want 1 — a quit that skipped the detach leaves a client attached and a record behind", d.closed)
	}
}
```

- [ ] **Step 7: Run the tests**

Run: `cd /Users/elliott/Projects/cspace-control-plane-4b && go test ./internal/control/ ./internal/controlplane/ -v -run 'Sweep|Quit'`
Expected: PASS.

- [ ] **Step 8: Run the gate and commit**

```bash
cd /Users/elliott/Projects/cspace-control-plane-4b
make check
git add internal/control internal/controlplane
git commit -m "$(cat <<'EOF'
Sweep the client records of attaches that died without detaching

Step 4 of the detach protocol: at startup, every record whose host pid is
gone gets its tmux client detached and its file removed, including several in
one sandbox — a crash strands one per open pane. Records for a container that
no longer exists are deleted without an exec, and a failed detach leaves its
record for the next sweep rather than hiding an attached client.

Co-Authored-By: <model> <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01W64zstC3PZojywARTtnSW7
EOF
)"
```

---

### Task 6: The supervisor view

The fourth tab kind: a read-only window on the headless agent. The design's main-area description is "a viewport over the event tail with assistant text rendered as markdown, a text area for the next prompt (Enter sends through `Send`; a key interrupts), and a spinner while working" — and open question 2's default is "assistant text plus one-line tool summaries, as the events stream already distinguishes them".

`control.EventLine` carries only timestamps and types today, because the detail band only ever showed those. This task widens it.

**Files:**
- Modify: `internal/control/events.go`, `internal/control/events_test.go`
- Create: `internal/controlplane/supervisor.go` (replacing Task 2's stub wholesale — including the last `//nolint:unused`, the one on `setEvents`, whose first caller Step 7 adds)
- Test: `internal/controlplane/supervisor_test.go`
- Modify: `internal/controlplane/model.go`, `internal/controlplane/panes.go` (`openOrFocus` sizes the new supervisor), `go.mod`, `go.sum`

**Interfaces:**
- Consumes: `control.Events`, `Data` (step 3); `tab`, `Actor.Send`, `Actor.Interrupt` (steps 3-4).
- Produces:
  - `control.EventLine` gains `Text string` and `Tools []string`
  - `func newSupervisor(width int) *supervisor`
  - `func (s *supervisor) resize(width, height int)`, `func (s *supervisor) setEvents(lines []control.EventLine)`, `func (s *supervisor) view(width, height int) string`
  - `func (m Model) handleSupervisorKey(t *tab, msg tea.KeyPressMsg) (tea.Model, tea.Cmd)`
  - `func (m Model) supervisorEventsCmd(t *tab) tea.Cmd`, `supervisorEventsMsg`

- [ ] **Step 1: Add glamour**

```bash
cd /Users/elliott/Projects/cspace-control-plane-4b
go get charm.land/glamour/v2@v2.0.1
git diff go.mod | head -40
```
Expected: `charm.land/glamour/v2 v2.0.1` in the direct block and a handful of new indirects (`chroma/v2`, `regexp2`, `bluemonday`, `douceur`, `gorilla/css`, `goldmark`, `goldmark-emoji`, `x/exp/slice`). **No existing version moves**; if one does, stop.

**This step reaches the network**, and it is the only one in the plan that does. Several of those indirects — `microcosm-cc/bluemonday`, `aymerick/douceur`, `gorilla/css` and `charmbracelet/x/exp/slice` — are not in this machine's module cache, so `go get` goes to the proxy. An executor without network access fails here, at a one-line step whose failure is obvious, rather than several steps later in the middle of `make check`; if that happens, stop and say so rather than working around it.

- [ ] **Step 2: Write the failing control test**

Add to `internal/control/events_test.go`:

```go
func TestTailEventsReadsAssistantTextAndToolCalls(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "events.ndjson")
	writeLines(t, p,
		`{"ts":"2026-09-18T10:00:00Z","kind":"sdk-event","data":{"type":"assistant","message":{"content":[{"type":"text","text":"Reading the file now."},{"type":"tool_use","name":"Read","input":{"file_path":"/workspace/main.go"}}]}}}`,
		`{"ts":"2026-09-18T10:00:01Z","kind":"sdk-event","data":{"type":"user","message":{"content":"a plain string body"}}}`,
		`{"ts":"2026-09-18T10:00:02Z","kind":"sdk-event","data":{"type":"result","subtype":"success"}}`,
	)
	got, err := TailEvents(p, 8)
	if err != nil {
		t.Fatalf("TailEvents: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].Text != "Reading the file now." {
		t.Errorf("assistant text = %q", got[0].Text)
	}
	if len(got[0].Tools) != 1 || !strings.Contains(got[0].Tools[0], "Read") ||
		!strings.Contains(got[0].Tools[0], "main.go") {
		t.Errorf("tools = %v, want one Read naming the file", got[0].Tools)
	}
	// A content field that is a bare string must not lose the whole line:
	// the SDK's shapes vary and the tail has to tolerate all of them.
	if got[1].Text != "a plain string body" {
		t.Errorf("string content = %q", got[1].Text)
	}
	if got[2].Type != "result" || got[2].Subtype != "success" {
		t.Errorf("result line = %+v", got[2])
	}
}
```

Add `"strings"` to `events_test.go`'s imports: at HEAD it has only `errors`,
`os`, `path/filepath` and `testing`.

- [ ] **Step 3: Widen `EventLine`**

In `internal/control/events.go`:

```go
// EventLine is one parsed events.ndjson record, narrowed to what the
// dashboard renders: the detail band shows the timestamps and types, and the
// supervisor view shows the assistant's own words with a one-line summary of
// each tool it called.
type EventLine struct {
	Ts      string
	Kind    string
	Type    string
	Subtype string
	// Text is the assistant's text blocks, joined — or, for the SDK shapes
	// whose message content is a bare string, that string.
	Text string
	// Tools is one short line per tool call, "<name>(<argument>)".
	Tools []string
}

// eventRecord mirrors the on-disk NDJSON line shape. Content is raw because
// the Agent SDK sends it both as a block array and as a plain string, and a
// typed field would fail the whole line on the shape it did not expect —
// which for a tail means silently dropping events.
type eventRecord struct {
	Ts   string `json:"ts"`
	Kind string `json:"kind"`
	Data struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	} `json:"data"`
}

// contentBlock is one element of the block-array form.
type contentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// decodeContent fills in an EventLine's Text and Tools from whichever shape
// the message content arrived in.
func decodeContent(raw json.RawMessage, line *EventLine) {
	if len(raw) == 0 {
		return
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			line.Text = s
		}
		return
	}
	var texts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				texts = append(texts, b.Text)
			}
		case "tool_use":
			line.Tools = append(line.Tools, toolSummary(b.Name, b.Input))
		}
	}
	line.Text = strings.Join(texts, "\n\n")
}

// toolArgKeys are the input fields worth showing, most specific first. A
// tool call's whole input is often a file's contents; what a person reading
// a feed wants is which file.
var toolArgKeys = []string{"file_path", "command", "path", "pattern", "url", "description", "prompt", "query"}

// toolSummaryLimit is how much of the argument survives, in runes.
const toolSummaryLimit = 60

// toolSummary renders one tool call as a single line.
func toolSummary(name string, input json.RawMessage) string {
	if name == "" {
		name = "tool"
	}
	var args map[string]any
	if json.Unmarshal(input, &args) != nil || len(args) == 0 {
		return name
	}
	pick := ""
	for _, k := range toolArgKeys {
		if v, ok := args[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				pick = s
				break
			}
		}
	}
	if pick == "" {
		keys := make([]string, 0, len(args))
		for k := range args {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if s, ok := args[keys[0]].(string); ok {
			pick = s
		}
	}
	pick = strings.ReplaceAll(strings.TrimSpace(pick), "\n", " ")
	if r := []rune(pick); len(r) > toolSummaryLimit {
		pick = string(r[:toolSummaryLimit]) + "…"
	}
	if pick == "" {
		return name
	}
	return name + "(" + pick + ")"
}
```

and in `TailEvents`'s loop, replace the append with:

```go
		line := EventLine{
			Ts:      rec.Ts,
			Kind:    rec.Kind,
			Type:    rec.Data.Type,
			Subtype: rec.Data.Subtype,
		}
		decodeContent(rec.Data.Message.Content, &line)
		all = append(all, line)
```

Add `"sort"` and `"strings"` to the imports.

- [ ] **Step 4: Run the control test**

Run: `cd /Users/elliott/Projects/cspace-control-plane-4b && go test ./internal/control/ -run TestTailEvents -v`
Expected: PASS, including the three step-3 tests, which assert only the fields that did not change.

- [ ] **Step 5: Write the failing view test**

Create `internal/controlplane/supervisor_test.go`:

```go
package controlplane

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/elliottregan/cspace/internal/control"
)

func supervisorLines() []control.EventLine {
	return []control.EventLine{
		{Ts: "2026-09-18T10:00:00Z", Kind: "sdk-event", Type: "assistant",
			Text:  "I will read **main.go** first.",
			Tools: []string{"Read(/workspace/main.go)"}},
		{Ts: "2026-09-18T10:00:02Z", Kind: "sdk-event", Type: "result", Subtype: "success"},
	}
}

func TestSupervisorViewRendersTextAndToolSummaries(t *testing.T) {
	s := newSupervisor(60)
	s.resize(60, 16)
	s.setEvents(supervisorLines())
	got := plain(s.view(60, 16))

	if !strings.Contains(got, "main.go") {
		t.Errorf("view %q lost the assistant's text", got)
	}
	// Markdown is rendered, so the asterisks are gone even though the text
	// carried them.
	if strings.Contains(got, "**main.go**") {
		t.Errorf("view %q shows raw markdown", got)
	}
	if !strings.Contains(got, "Read(/workspace/main.go)") {
		t.Errorf("view %q has no tool summary", got)
	}
	if !strings.Contains(got, "result") {
		t.Errorf("view %q dropped the result line", got)
	}
	// At most 16: fitLines guarantees the ceiling, and the floor is not
	// bubbles' to promise. textarea.View() renders a prompt column and a
	// cursor column of its own, so "exactly SetHeight(3) lines at exactly
	// SetWidth(w) cells" is an assumption about a widget's internals — the
	// same one sendInputWidth (view.go:189) exists to work around.
	if lines := strings.Count(s.view(60, 16), "\n") + 1; lines > 16 {
		t.Errorf("view is %d lines, want at most 16", lines)
	}
}

func TestSupervisorViewSurvivesAResize(t *testing.T) {
	s := newSupervisor(60)
	s.resize(60, 16) // view is read-only; geometry arrives through resize
	s.setEvents(supervisorLines())
	_ = s.view(60, 16)
	// glamour bakes its wrap width at construction, so a resize has to
	// rebuild the renderer rather than set a field.
	s.resize(30, 10)
	got := s.view(30, 10)
	for _, line := range strings.Split(plain(got), "\n") {
		// At most 30 cells: the claim is that the feed was re-wrapped by a
		// renderer built at the new width, not that every widget in the
		// stack stops at exactly Width(). The send box does not — bubbles
		// draws its prompt and its cursor past that, which is what
		// sendInputWidth subtracts for the footer's own input.
		if ansi.StringWidth(line) > 30 {
			t.Errorf("line %q is wider than the resized view", line)
		}
	}
}

func TestSupervisorEnterSendsAndEscInterrupts(t *testing.T) {
	a := &recordingActor{}
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, a, h)
	m = stepPump(t, m, "a") // open the supervisor tab on mercury
	if m.focusedTab() == nil || m.focusedTab().sup == nil {
		t.Fatal("no supervisor tab")
	}

	for _, r := range "hello" {
		m = step(t, m, string(r))
	}
	m = step(t, m, "enter")
	if len(a.sends) != 1 || a.sends[0].text != "hello" {
		t.Fatalf("sends = %+v, want one hello", a.sends)
	}
	if got := m.focusedTab().sup.input.Value(); got != "" {
		t.Errorf("the box kept %q after sending", got)
	}
	if m.action != LabelSend {
		t.Fatalf("action = %q, want the send in flight", m.action)
	}

	// Esc while the send is still out is refused: the supervisor tab is
	// inside the same one-action-at-a-time gate the sidebar's keys pass
	// through. Two actions in flight means two results, the first of which
	// clears the gate for the second and leaves the footer naming the wrong
	// verb.
	if got := step(t, m, "esc"); len(a.interrupt) != 0 {
		t.Errorf("interrupts = %d while the send was in flight, want 0 (action %q)",
			len(a.interrupt), got.action)
	}

	// Once the send's result lands the gate opens and esc interrupts.
	mm, _ := m.Update(Result(LabelSend, nil))
	m = mm.(Model)
	m = step(t, m, "esc")
	if len(a.interrupt) != 1 {
		t.Errorf("interrupts = %d, want 1", len(a.interrupt))
	}
}

// TestSupervisorWorkingFollowsTheAgentStatus pins where the spinner's state
// comes from. Not from the tail: events.ndjson carries lines that are not
// sdk-events at all, so "the last event is not a result" is a guess that can
// never go false again — and a spinner that never stops keeps the whole
// dashboard redrawing at spinner cadence for the rest of the session, which
// is exactly what model.go's own tick chain is written to avoid.
func TestSupervisorWorkingFollowsTheAgentStatus(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = stepPump(t, m, "a")
	tb := m.focusedTab()
	if tb == nil || tb.sup == nil {
		t.Fatal("no supervisor tab")
	}

	// A tail whose last line is an assistant turn, and no agent status
	// saying anything is running.
	mm, _ := m.Update(supervisorEventsMsg{id: tb.id, lines: supervisorLines()[:1]})
	m = mm.(Model)
	if tb.sup.working {
		t.Error("the supervisor is working with nothing reporting that it is")
	}

	mm, _ = m.Update(liveMsg{states: map[sandboxKey]liveState{
		{Project: "alpha", Name: "mercury"}: {
			Agent: control.AgentStatus{Reachable: true, State: "working"}},
	}})
	m = mm.(Model)
	mm, cmd := m.Update(supervisorEventsMsg{id: tb.id, lines: supervisorLines()[:1]})
	m = mm.(Model)
	if !tb.sup.working {
		t.Error("the supervisor is idle while the fast ticker reports the agent working")
	}
	if cmd == nil {
		t.Error("this spinner's own tick chain never started")
	}
}
```

- [ ] **Step 6: Write `supervisor.go`**

This **replaces** Task 2's stub file in full, so the last surviving
`//nolint:unused` — the one on `setEvents` — goes with it. Step 7 adds
`setEvents`' first caller in the same task, which is what makes that legal;
nothing is left annotated for a caller that does not exist.

```go
package controlplane

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"

	"github.com/elliottregan/cspace/internal/control"
)

// No "time" here: nothing in this file schedules. supervisorTail and
// supervisorInputHeight are plain ints, the feed is driven by the medium
// ticker in model.go, and the spinner's tick chain is re-armed there too.

// supervisorTail is how many events the supervisor view reads. Far more than
// the detail band's eight: this is the feed, and the viewport scrolls it.
const supervisorTail = 200

// supervisorInputHeight is how many lines the send box gets.
const supervisorInputHeight = 3

// supervisor is the read-only window on the headless agent: the event tail
// with the assistant's text rendered as markdown, a box for the next turn,
// and a spinner while it is working.
//
// It is not a pane. There is no pty and no child — the events come off the
// host's own session directory through control.Events, and sending goes
// through the supervisor's HTTP control port like `cspace send` does.
type supervisor struct {
	vp    viewport.Model
	input textarea.Model
	spin  spinner.Model

	md    *glamour.TermRenderer
	mdErr error

	width, height int
	lines         []control.EventLine
	working       bool
	// reading is set while this tab's own Events read is in flight, so a
	// read slower than the medium interval cannot stack up behind itself.
	reading bool
	err     error
}

func newSupervisor(width int) *supervisor {
	ta := textarea.New()
	ta.Placeholder = "send a turn"
	ta.ShowLineNumbers = false
	ta.CharLimit = 4000
	// Enter must submit, and textarea binds it to InsertNewline by default.
	ta.KeyMap.InsertNewline.SetEnabled(false)
	ta.SetHeight(supervisorInputHeight)
	ta.Focus()

	s := &supervisor{
		vp:    viewport.New(viewport.WithWidth(width), viewport.WithHeight(1)),
		input: ta,
		spin:  spinner.New(spinner.WithSpinner(spinner.Dot)),
		width: width,
	}
	s.rebuildRenderer()
	return s
}

// rebuildRenderer builds the markdown renderer at the current width.
// glamour v2 bakes the wrap width at construction — there is no setter — so
// a resize has to make a new one.
func (s *supervisor) rebuildRenderer() {
	width := s.width
	if width < 20 {
		width = 20
	}
	md, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(styles.DarkStyle),
		glamour.WithWordWrap(width),
	)
	s.md, s.mdErr = md, err
}

func (s *supervisor) resize(width, height int) {
	rewrap := width != s.width
	if rewrap {
		s.width = width
		s.rebuildRenderer()
	}
	s.height = height
	s.vp.SetWidth(width)
	s.input.SetWidth(width)
	body := height - supervisorInputHeight - 1
	if body < 1 {
		body = 1
	}
	s.vp.SetHeight(body)
	if rewrap {
		// The content was wrapped at the old width by the old renderer, so
		// a new one is not enough on its own — the feed has to be rendered
		// again through it.
		atBottom := s.vp.AtBottom()
		s.vp.SetContent(s.render())
		if atBottom {
			_ = s.vp.GotoBottom()
		}
	}
}

// setEvents replaces the feed and follows it when the view was already at
// the bottom — the reading rule every log window wants: a person who has
// scrolled back stays where they were, and one who has not sees the newest.
func (s *supervisor) setEvents(lines []control.EventLine) {
	atBottom := s.vp.AtBottom()
	s.lines = lines
	s.vp.SetContent(s.render())
	if atBottom {
		_ = s.vp.GotoBottom()
	}
}

// render turns the feed into the viewport's content: assistant text through
// glamour, everything else as one dim line — the design's open question 2,
// whose default is exactly this.
func (s *supervisor) render() string {
	if len(s.lines) == 0 {
		return styleDim.Render("no agent events yet")
	}
	var b strings.Builder
	for _, e := range s.lines {
		if e.Text != "" {
			b.WriteString(s.markdown(e.Text))
			b.WriteString("\n")
		}
		for _, tool := range e.Tools {
			b.WriteString(styleDim.Render("  ⟡ " + tool))
			b.WriteString("\n")
		}
		if e.Text == "" && len(e.Tools) == 0 {
			label := e.Type
			if e.Subtype != "" {
				label += "/" + e.Subtype
			}
			b.WriteString(styleDim.Render(fmt.Sprintf("  %s %s", shortTs(e.Ts), label)))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// markdown renders one block, falling back to the raw text when glamour
// could not be built or chokes on the input. A feed that stopped showing the
// agent's words because a renderer failed would be worse than an ugly one.
func (s *supervisor) markdown(text string) string {
	if s.md == nil || s.mdErr != nil {
		return text
	}
	out, err := s.md.Render(text)
	if err != nil {
		return text
	}
	return strings.TrimRight(out, "\n")
}

// view is the whole tab: the feed, a rule, and the send box.
//
// It is READ-ONLY, and that is load-bearing rather than tidy. Model.View
// is the only caller, Model's own doc insists nothing shared is mutated in
// place, and resize is the opposite of that — it can rebuild the glamour
// renderer and call vp.SetContent. A supervisor learns its geometry in
// exactly two places instead: openOrFocus, when the tab is created, and
// model.go's tea.WindowSizeMsg fan-out. Both pass the same numbers this
// function is handed, because View sizes the main area from paneSize()
// itself rather than recomputing the same arithmetic beside it.
func (s *supervisor) view(width, height int) string {
	status := styleDim.Render(strings.Repeat("─", max(1, width)))
	if s.working {
		status = s.spin.View() + " " + styleDim.Render("working · esc interrupts")
	} else if s.err != nil {
		status = styleErr.Render(fit("events unavailable: "+s.err.Error(), width))
	}
	return fitLines(strings.Join([]string{s.vp.View(), status, s.input.View()}, "\n"), height)
}

// supervisorEventsMsg is one supervisor tab's own, longer tail. It is keyed
// by tab id rather than by sandbox: two supervisor tabs on two sandboxes are
// two feeds, and a reply that landed late must not fill the wrong one.
type supervisorEventsMsg struct {
	id    int
	lines []control.EventLine
	err   error
}

// supervisorEventsCmd reads one supervisor tab's feed. It is a no-op while
// that tab's previous read is still out.
func (m Model) supervisorEventsCmd(t *tab) tea.Cmd {
	if t == nil || t.sup == nil || t.sup.reading {
		return nil
	}
	t.sup.reading = true
	data, id, project, sandbox := m.data, t.id, t.project, t.sandbox
	return func() tea.Msg {
		lines, err := data.Events(project, sandbox, supervisorTail)
		return supervisorEventsMsg{id: id, lines: lines, err: err}
	}
}

// supervisorTickCmds refreshes every open supervisor tab. The medium cadence
// drives it, which is the cadence the design assigns to "Events for the open
// supervisor view".
func (m Model) supervisorTickCmds() []tea.Cmd {
	var cmds []tea.Cmd
	for _, t := range m.tabs {
		if cmd := m.supervisorEventsCmd(t); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

// handleSupervisorKey is the supervisor tab's keyboard. The box owns it:
// Enter sends, Esc interrupts, the page keys scroll the feed, and everything
// else is text. The leader is handled before this is ever reached.
//
// The two arms that start an action check m.action first, because this route
// does not pass through handleNormalKey, where the one-action-at-a-time gate
// lives. Without it a second action can be started under the first: both
// emit an actionResultMsg, the first to land clears the gate for the other,
// and the footer reports whichever verb arrived last. Typing is never gated
// — only the keys that act.
func (m Model) handleSupervisorKey(t *tab, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Both actions take the same row, rebuilt from the current row set
	// rather than remembered: it is hoisted here so the two arms cannot
	// drift apart, and it is cheap — containerFor is a scan of a list the
	// dashboard already holds.
	row := control.Row{Kind: control.RowSandbox, Project: t.project, Name: t.sandbox,
		Container: containerFor(m.rows, t.project, t.sandbox)}

	switch msg.String() {
	case "enter":
		if m.action != "" {
			// The gate, checked before the box is read: a turn thrown away
			// because the last action had not landed yet would be the worst
			// possible reading of "busy". The text stays, and Enter again
			// once the footer clears sends it.
			return m, nil
		}
		text := strings.TrimSpace(t.sup.input.Value())
		t.sup.input.Reset()
		if text == "" {
			return m, nil
		}
		return m.startAction(LabelSend, m.actor.Send(row, text))
	case "esc":
		if m.action != "" {
			return m, nil
		}
		return m.startAction(LabelInterrupt, m.actor.Interrupt(row))
	case "pgup":
		t.sup.vp.PageUp()
		return m, nil
	case "pgdown":
		t.sup.vp.PageDown()
		return m, nil
	}
	var cmd tea.Cmd
	t.sup.input, cmd = t.sup.input.Update(msg)
	return m, cmd
}

// containerFor finds a sandbox's container name in the current row set. The
// actions take a Row, and a supervisor tab holds only the identity — the row
// it was opened from may have been rebuilt by a poll since.
func containerFor(rows []control.Row, project, sandbox string) string {
	for _, r := range rows {
		if r.Kind == control.RowSandbox && r.Project == project && r.Name == sandbox {
			return r.Container
		}
	}
	return ""
}
```


- [ ] **Step 7: Wire the feed into the model**

In `internal/controlplane/model.go`'s `Update`:

```go
	case supervisorEventsMsg:
		if t, _ := m.tabByID(msg.id); t != nil && t.sup != nil {
			t.sup.err = msg.err
			t.sup.setEvents(msg.lines)
			t.sup.reading = false
			// Whether the agent is working is the fast ticker's answer, not
			// a guess from the tail. "The last event is not a result" can
			// never go false — events.ndjson carries lines that are not
			// sdk-events at all, and any tail ending in one of those would
			// leave the spinner's tick chain alive for the rest of the
			// session, redrawing the whole dashboard at spinner cadence.
			// AgentStatus is what the fast cadence already polls for exactly
			// this question.
			working := m.live[sandboxKey{Project: t.project, Name: t.sandbox}].Agent.State == "working"
			started := working && !t.sup.working
			t.sup.working = working
			if started {
				// Start this spinner's own tick chain. bubbles tags each
				// TickMsg with the spinner's id and drops the ones that are
				// not its own, so every spinner needs its own chain — the
				// model's own animates only while an Actor action is in
				// flight, which a supervisor read is not.
				return m, t.sup.spin.Tick
			}
		}
		return m, nil
```

and in the `mediumTickMsg` case, add the supervisor reads **inside the poll
guard**. The spec puts `Events` for the open supervisor view on the medium
ticker alongside the snapshot, and `paused()` is what stops a ticker
replacing the world while a modal is open or a pane is opening; a read issued
unconditionally would also stack up behind itself whenever one takes longer
than the two-second interval, which is why each tab carries `reading`:

```go
	case mediumTickMsg:
		cmds := []tea.Cmd{tea.Tick(mediumInterval, func(t time.Time) tea.Msg { return mediumTickMsg{at: t} })}
		if !m.pollingMedium && !m.paused() {
			m.pollingMedium = true
			cmds = append(cmds, m.snapshotCmd())
			cmds = append(cmds, m.supervisorTickCmds()...)
		}
		return m, tea.Batch(cmds...)
```

and route the spinner ticks, replacing the `spinner.TickMsg` case so the
supervisor's spinners animate too:

```go
	case spinner.TickMsg:
		// Each spinner has its own id and its own chain; Update drops a tick
		// that is not its own, so both are fed and whichever one it belonged
		// to re-arms.
		var cmds []tea.Cmd
		for _, t := range m.tabs {
			if t.sup != nil && t.sup.working {
				var cmd tea.Cmd
				t.sup.spin, cmd = t.sup.spin.Update(msg)
				cmds = append(cmds, cmd)
			}
		}
		// Animate the model's own only while an action is in flight; when
		// idle let its chain die rather than redraw a whole dashboard
		// forever.
		if m.action != "" {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
```

- [ ] **Step 8: Run the tests**

Run: `cd /Users/elliott/Projects/cspace-control-plane-4b && go test ./internal/control/ ./internal/controlplane/ -v`
Expected: PASS.

- [ ] **Step 9: Run the gate and commit**

```bash
cd /Users/elliott/Projects/cspace-control-plane-4b
make check
git add go.mod go.sum internal/control internal/controlplane
git commit -m "$(cat <<'EOF'
Add the supervisor view

A viewport over the event tail with the assistant's text rendered as markdown
and one line per tool call, plus a box that sends through the supervisor's
control port. EventLine now carries the text and the tool summaries; its
content field is decoded leniently, because the SDK sends it both as a block
array and as a bare string and a strict decode would drop whole events.

Co-Authored-By: <model> <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01W64zstC3PZojywARTtnSW7
EOF
)"
```

---

### Task 7: The `internal/cli` pane host, and retiring the suspend-the-program attach

Step 3's attach handed the whole terminal to `container exec` through `tea.Exec`. A Claude pane does the same job without giving up the window, so the old path goes — YAGNI, and two ways to attach from one dashboard is one too many.

`cspace attach` itself is untouched: it keeps `runAttachChild`, its signal handling and `restoreBlockingStreams`. `beginAttachOrWarn` stays and grows a second caller.

**This is the task that closes the gap Task 2 opened.** Since Task 2, `Enter`/`s`/`a` have reported `open pane failed: no pane host configured`, because `cmd_tui.go` passed `nil` for the seam. Step 5 passes the real `newPaneHost(ctrl, home)`, and from here the dashboard opens panes for the first time.

**Files:**
- Create: `internal/cli/controlplane_panes.go`
- Test: `internal/cli/controlplane_panes_test.go`
- Modify: `internal/cli/controlplane_actor.go`, `internal/cli/cmd_tui.go`
- Modify: `internal/controlplane/actor.go` (`Actor.Attach`, `LabelAttach`), `internal/controlplane/model.go` (`paused`), `internal/controlplane/keys.go` (`actionHelp[ActionAttach]`), `internal/controlplane/controlplane.go` (Step 7's package doc — the one file 4b otherwise never touches)
- Test (existing, updated): `internal/cli/controlplane_actor_test.go` (six tests and `countingExecer` deleted, and the imports they orphan), `internal/controlplane/model_test.go` (`TestViewGeometry`, `TestPollingContinuesWhileALongActionRuns`, and `recordingActor.Attach`), `internal/controlplane/keys_test.go` (Step 7 rewords `TestLeaderIsDeclaredButNotAdvertised`'s stale comment)
- Modify: `CLAUDE.md`, `docs/superpowers/specs/2026-09-17-control-plane-design.md`

`internal/controlplane/input.go` is **not** in this list: Task 2 already removed its `m.actor.Attach(row)` dispatch, and nothing in this task touches the file.

**Interfaces:**
- Consumes: `controlplane.PaneHost`, `controlplane.Kind`, `controlplane.Opened` (Task 2 of this plan); `control.SweepClientRecords`, `control.SweepResult` (**Task 5 of this plan** — they are not in 4a); `control.ClaudeAttach`, `control.AttachArgv` (rollout step 1); `control.ShellAttach` (4a Task 4); `pane.Open`, `pane.Command`, `pane.HostShell` (4a Task 3); `beginAttachOrWarn` (rollout step 3).
- Produces: `func newPaneHost(ctrl *control.Client, home string) *paneHost`, satisfying `controlplane.PaneHost`.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/controlplane_panes_test.go`:

```go
package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/controlplane"
)

func TestPaneHostOpensAHostShellWithNoContainer(t *testing.T) {
	// pane.HostShell reads $SHELL at call time and runs it with -l, so
	// without this the test sources the operator's own .zprofile in a pty
	// during `go test`. /bin/sh -l is a login shell that does almost
	// nothing, which is all this case needs: that a pane opened and has no
	// detacher.
	t.Setenv("SHELL", "/bin/sh")

	h := newPaneHost(control.New(control.Options{}), t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	opened, err := h.Open(ctx, controlplane.KindHostShell, control.Row{}, 40, 10)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if opened.Pane == nil {
		t.Fatal("no pane")
	}
	if opened.Detach != nil {
		t.Error("a host shell is not a tmux client and must have no detacher")
	}
	_ = opened.Pane.Close(ctx)
}

func TestPaneHostRefusesASandboxWithNoContainer(t *testing.T) {
	h := newPaneHost(control.New(control.Options{}), t.TempDir())
	_, err := h.Open(context.Background(), controlplane.KindClaude,
		control.Row{Kind: control.RowSandbox, Project: "demo", Name: "mercury"}, 40, 10)
	if err == nil {
		t.Fatal("Open accepted a row with no container")
	}
	if !strings.Contains(err.Error(), "mercury") {
		t.Errorf("error = %v, want it to name the sandbox", err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /Users/elliott/Projects/cspace-control-plane-4b && go test ./internal/cli/ -run TestPaneHost`
Expected: FAIL to build — `undefined: newPaneHost`.

- [ ] **Step 3: Write `controlplane_panes.go`**

```go
package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/controlplane"
	"github.com/elliottregan/cspace/internal/pane"
)

// paneHost implements controlplane.PaneHost: it turns a row and a kind into
// a running child on a pty, and books the attach so the tmux client it
// creates can be detached again.
//
// This is where the three layers meet, and why it lives here: internal/pane
// must not know what a sandbox is, internal/controlplane must not import
// internal/cli, and the argv builder plus the attach bookkeeping are
// internal/control's. The seam is satisfied once, in the one package that is
// allowed to see all three.
type paneHost struct {
	ctrl *control.Client
	home string
}

var _ controlplane.PaneHost = (*paneHost)(nil)

func newPaneHost(ctrl *control.Client, home string) *paneHost {
	return &paneHost{ctrl: ctrl, home: home}
}

// Open starts one pane's child. It runs inside a tea.Cmd, never on the UI
// goroutine: the tmux probe is an exec into the container and BeginAttach
// may wait out another attach's lock.
func (h *paneHost) Open(ctx context.Context, kind controlplane.Kind, row control.Row, cols, rows int) (controlplane.Opened, error) {
	if kind == controlplane.KindHostShell {
		// No container, no tmux, no bookkeeping: this is the operator's own
		// shell, and it belongs to no sandbox.
		p, err := pane.Open(pane.HostShell(), cols, rows)
		if err != nil {
			return controlplane.Opened{}, err
		}
		return controlplane.Opened{Pane: p}, nil
	}

	if row.Container == "" {
		// A registered sandbox that is not booted has no container to exec
		// into. Say that, rather than let the probe fail with a
		// transport-shaped error about a name that was never going to answer.
		return controlplane.Opened{}, fmt.Errorf("sandbox %s has no container yet", row.Name)
	}

	present, err := h.ctrl.Tmux().Present(ctx, row.Container)
	if err != nil {
		// Not knowing whether tmux is there is not the same as knowing it
		// isn't: a direct exec would hit the same failure a moment later.
		return controlplane.Opened{}, fmt.Errorf("cannot reach sandbox %s to probe for tmux: %w", row.Name, err)
	}

	var spec control.AttachSpec
	switch kind {
	case controlplane.KindClaude:
		spec = control.ClaudeAttach(row.Container, present)
	case controlplane.KindShell:
		spec = control.ShellAttach(row.Container, present)
	default:
		return controlplane.Opened{}, fmt.Errorf("pane kind %v has no command", kind)
	}
	bin, argv, err := control.AttachArgv(spec)
	if err != nil {
		return controlplane.Opened{}, err
	}

	// io.Discard: a bookkeeping warning has nowhere to be read here — the
	// dashboard owns the screen and this runs off the UI goroutine.
	// beginAttachOrWarn still downgrades that failure to an inert
	// attachment. home was resolved and hard-failed on at `cspace tui`
	// startup, so it is passed with a nil homeErr.
	att, _, err := beginAttachOrWarn(ctx, io.Discard, h.ctrl.Tmux(), h.home, nil,
		row.Project, row.Name, row.Container, spec.Session)
	if err != nil {
		return controlplane.Opened{}, err
	}

	p, err := pane.Open(pane.Command{Path: bin, Args: argv}, cols, rows)
	if err != nil {
		// The bookkeeping is already open; close it rather than strand a
		// lock and a record for a client that never appeared.
		_ = att.Close(ctx)
		return controlplane.Opened{}, err
	}

	warning := ""
	if !present {
		warning = fmt.Sprintf(
			"%s has no tmux: this session will not survive the window, and its process keeps running inside the sandbox. Rebuild with `cspace image build`, then `cspace down %s && cspace up %s`.",
			row.Name, row.Name, row.Name)
	}
	return controlplane.Opened{Pane: p, Detach: att, Warning: warning}, nil
}

// Sweep reaps the client records of attaches whose host process is gone.
//
// The count reported to the footer is Deleted, not Deleted+Detached:
// SweepResult.Detached is the subset of the deleted whose client had to be
// detached first, so adding the two counts those records twice.
func (h *paneHost) Sweep(ctx context.Context) (int, error) {
	res, err := h.ctrl.SweepClientRecords(ctx)
	return res.Deleted, err
}
```

- [ ] **Step 4: Delete the suspend-the-program attach**

From `internal/cli/controlplane_actor.go` remove: the `Attach` method, `attachCommand`, `attachResult`, `attachExec` (the whole type and its four methods), `attachRunErr`, and the `attachProbeTimeout` / `attachBookkeepingTimeout` / `attachDetachTimeout` constants. Remove the now-unused imports: `errors`, **`fmt`**, `io` and `os/exec`.
`tea` **stays** — `Down`, `Send`, `Interrupt`, `RestartBrowser` and `Up` all
return `tea.Cmd` — and so do `context`, `time`, `control` and `controlplane`.
(`fmt`'s only three uses in this file are inside `attachResult` and
`attachExec.Run`, all of which this step deletes; `errors` is used only by
`attachRunErr`, `io` only by `attachExec`'s stream setters and its
`io.Discard`, and `os/exec` only by `attachExec.Run`.) Remove the matching
tests from `internal/cli/controlplane_actor_test.go`.

**Then prune the imports those deletions orphan in the test file too** — at
least `sync/atomic` (used only by `countingExecer`, controlplane_actor_test.go:26)
and `os/exec` (used only by the two `attachRunErr` tests, :237 and :248);
re-check `context`, `errors` and `io` the same way, use by use. `unused`
ignores `_test.go` and so does not help here, but the compiler does not:
"imported and not used" is a build failure, so the package — and Step 6's
`make check` — does not compile until this is done. Confirm with
`go vet ./internal/cli/` before moving on.

Two test fixtures go dead with them and are **not** found by Step 6's
catch-all, because neither names `LabelAttach` nor `Actor.Attach` —
`unused` ignores `_test.go` entirely, so `make check` stays green with both
still sitting there. Delete them by name:

- `countingExecer` (`internal/cli/controlplane_actor_test.go`, the type and
  its methods) — its only users are the two attach tests this step removes.
- `recordingActor.Attach` and the `attach` field it appends to
  (`internal/controlplane/model_test.go`) — `recordingActor` stops needing
  them the moment `Actor` stops declaring `Attach`, and a fake that
  implements a method its interface no longer has is a fake that will be
  read as documentation of a path that no longer exists.

**`ResultWarn` and `ResultWarnText` stay.** They lose their old callers here
— `attachResult` was the production one, and the two assertions inside the
attach tests were the test ones — but they are not dead: Task 2's
`paneOpenedMsg` arm reports the no-tmux warning through `ResultWarn`, which
is the case `ResultWarn`'s own doc comment names, and
`TestAnOpenThatWarnsGoesThroughResultWarn` in
`internal/controlplane/panes_test.go` reads it back with `ResultWarnText`.
Leave both functions, and leave the `ResultWarn` reference in `notice`'s doc
(`internal/controlplane/model.go:33`) alone: it is still exactly how a
warning becomes a sticky footer line.

From `internal/controlplane/actor.go` remove `Attach(row control.Row) tea.Cmd` from the `Actor` interface and the `LabelAttach` constant, and update the `Actor` doc comment's "everything but attach" clause:

```go
// Actor runs the dashboard's side effects. It is declared here — by the
// consumer — and implemented in internal/cli, whose actor delegates to
// internal/control for all of them. Injecting it is what keeps this package
// from importing internal/cli. Panes are a separate seam (PaneHost), because
// opening one produces a live process this package then owns.
```

From `internal/controlplane/model.go`, drop `m.action == LabelAttach ||` from `paused` — a pane open is already covered by `LabelOpenPane` — leaving `return m.mode != modeNormal || m.action == LabelOpenPane`, and delete the sentence about attach owning the terminal from its comment.

From `internal/controlplane/keys.go`, keep `ActionAttach`: the *key* is still `enter` and still called attach, it just opens a pane now. Update its `actionHelp` description:

```go
	ActionAttach: {"enter", "claude pane"},
```

Two step-3 tests in `internal/controlplane/model_test.go` fail on that
change and on the `paused()` one above, and neither names `LabelAttach` or
`Actor.Attach` — both use the bare string — so Step 6's catch-all does not
reach them:

- `TestViewGeometry` (model_test.go:753) asserts the footer's last line
  contains `"attach"`. With `actionHelp[ActionAttach]` reading
  `"claude pane"`, no entry of `ShortHelp()` contains that substring any
  more. Change the assertion to `"claude pane"`.
- `TestPollingContinuesWhileALongActionRuns` (model_test.go:718) sets
  `m.pollingMedium, m.action = false, "attach"` and asserts the medium poll
  is skipped. `paused()` no longer knows that label. Change the literal to
  `LabelOpenPane` and the comment with it — a pane open, not an attach, is
  what holds the row set still now.

- [ ] **Step 5: Hand the host to the model**

In `internal/cli/cmd_tui.go`, replace the model construction:

```go
			model := controlplane.New(ctrl,
				newControlPlaneActor(ctrl, home),
				newPaneHost(ctrl, home),
				controlplane.NewKeyMap(userCfg.TUI.Keys))
			_, err = tea.NewProgram(model).Run()
			return err
```

- [ ] **Step 6: Run everything**

Run: `cd /Users/elliott/Projects/cspace-control-plane-4b && make check && make test-race`
Expected: green, no races. Any test still referring to `LabelAttach` or `Actor.Attach` is testing the path this task deletes; delete it.

- [ ] **Step 7: Update the docs**

First the comments this plan makes wrong, which are in the code rather than
in `CLAUDE.md` and would otherwise survive every gate — `make check` has no
opinion about a paragraph that describes a path that no longer exists. Four
of them:

- `cpActor`'s doc (`internal/cli/controlplane_actor.go:38-42`): "Only attach
  stays here: it hands the terminal to a child, which is a Bubble Tea
  concern and therefore not control's." Attach is what this step deleted.
  Replace those two sentences with: *"Every action delegates; nothing is
  implemented here. Panes are the other seam — `paneHost`
  (controlplane_panes.go) — because opening one produces a live process the
  dashboard then owns."* `home` is still kept for the attach lock and the
  client records, so that sentence stays.
- the package doc (`internal/controlplane/controlplane.go:10-13`) — the one
  file 4b otherwise never touches: "Rollout step 3 has no panes: the main
  area holds the detail band for the selected row, and attach suspends the
  whole program into `container exec` the way the v1 dashboard did. The tabs
  line, the leader binding and the detail renderer's width parameter are the
  seams rollout step 4 grows into." Replace that paragraph with: *"Rollout
  step 4 grew the seams step 3 left: the main area holds the focused pane
  (internal/pane, opened through the PaneHost seam), the detail band moved
  under the sidebar, and the leader binding dispatches. Attach no longer
  suspends the program — a Claude session runs in a pane inside the
  window."*
- `paused()`'s remaining attach sentence (`internal/controlplane/model.go:144-154`).
  Step 4 above drops `LabelAttach` from the condition and the clause about
  attach owning the terminal; finish the job here by reading the whole
  comment back and making sure what is left says why an *open* pauses a
  poll — the row set must not be replaced under a row an open is in flight
  against — with nothing left about a suspended program.
- `TestLeaderIsDeclaredButNotAdvertised`'s comment and failure message
  (`internal/controlplane/keys_test.go:184-186`): "nothing in step 3
  dispatches it" and "until panes land". The test itself still holds and
  stays — the leader is dispatched now but is still deliberately absent from
  `ShortHelp` and `FullHelp`, because it is the prefix, not a key of its
  own, and the leader footer names it separately. Reword both to say that.

Then `CLAUDE.md`: replace the **controlplane** bullet's last two sentences —
the one beginning "Every action goes through an `Actor`" and the one about
keybindings. The `Actor` clause stays, because `Actor` stays: `Down`, `Send`,
`Interrupt`, `RestartBrowser` and `Up` all still run through it, and only
`Attach` left. What goes is the `tea.ExecCommand` half.

```markdown
Every action goes through an `Actor` implemented in `internal/cli` (`controlplane_actor.go`), and panes through a second seam, `PaneHost` (`controlplane_panes.go`), so this package never imports `internal/cli`. Panes are `internal/pane`: one child on a pty behind a terminal emulator, four goroutines each — so a Claude session, a sandbox shell or a host shell runs *inside* the window instead of suspending it. Every key but the leader (`⌃Space`) goes to the focused pane; the leader's second keys move between tabs, open and close them, and enter scroll mode. Opening a pane takes the sandbox's attach lock and writes a client record; closing it detaches the tmux client, and a startup sweep reaps the records of attaches whose process is gone. Keybindings are `bubbles/v2/key` bindings resolved from `tui.keys` in the user-level `~/.cspace/config.json` over the defaults in `lib/defaults.json`.
```

In `docs/superpowers/specs/2026-09-17-control-plane-design.md`, replace the paragraph beginning "Rollout step 3 places both of those differently" with:

```markdown
Rollout step 3 placed both of those differently and step 4 moved them, as
planned: the detail band now renders under the sidebar at 24 columns — where
a URL does not fit, which is why the sidebar's own port lines carry it as an
OSC 8 hyperlink — and the tabs row carries the tabs it is named for, falling
back to daemon health while no pane is open. `renderDetail` takes its width
as a parameter, which is what made that a layout change rather than a
rewrite.
```

Three of the spec's step-4 descriptions were written before the layout was
built and this plan does not implement them; record the departures where the
design is rather than leaving the next reader to discover them. Replace the
main-area list — the four bullets under "Main area, by tab kind:" — with:

```markdown
- Claude pane and shell pane: the emulator's rendered screen, filling the
  main area. It carries no border: a frame costs two of the pane's columns
  and two of its rows, and step 4 buys the same cue for nothing by lighting
  the focused tab (`styleTabActive` while the keyboard is in the main area,
  `styleTabFocused` while it is not), switching the footer to the leader's
  keys, and placing the terminal cursor only while the pane has focus.
- Supervisor view: a viewport over the event tail with assistant text
  rendered as markdown, a text area for the next prompt (Enter sends through
  `Send`; Esc interrupts), and a spinner while working.
- Host shell: the emulator screen.
- Exited pane: the last screen dimmed, under the exit reason and the key that
  closes the tab. The pane's own teardown — the detach and the engine's
  handshake — runs the moment the child exits, not when the tab is dismissed,
  so nothing stays attached inside the sandbox while the screen is being
  read. There is no separate restart key: closing the tab and opening the
  pane again from the sidebar *is* the restart, and it rejoins the same tmux
  session with its screen intact.
```

and replace the first bullet under "## Error handling" with:

```markdown
- Exec failure for a pane: no tab opens, and the footer carries the error
  until the next keypress. Retrying is pressing the same key again, so there
  is no separate retry binding and no empty tab to hold one. Other panes and
  the sidebar are unaffected.
```

and in the Rollout list, mark steps 1-4 as landed:

```markdown
1. ~~**Sandbox side.**~~ Landed.
2. ~~**Control API.**~~ Landed.
3. ~~**Dashboard on v2.**~~ Landed.
4. ~~**Panes.**~~ Landed, as two plans: `2026-09-18-control-plane-4a-pane-engine.md` (the engine, the pane commands, the two step-3 follow-ups, the smoke harness) and `2026-09-18-control-plane-4b-panes-in-the-dashboard.md` (tabs, focus, the leader, the detach protocol, the supervisor view).
5. **Mouse and image paste.**
```

- [ ] **Step 8: Commit**

```bash
cd /Users/elliott/Projects/cspace-control-plane-4b
git add internal/cli internal/controlplane CLAUDE.md docs/superpowers/specs
git commit -m "$(cat <<'EOF'
Open a Claude pane instead of suspending the program

The dashboard's attach handed the whole terminal to container exec; a pane
does the same job inside the window, so tea.Exec, attachExec and Actor.Attach
go. `cspace attach` is unchanged and keeps its own foreground-child flow.

Co-Authored-By: <model> <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01W64zstC3PZojywARTtnSW7
EOF
)"
```

---

### Task 8: Verify the panes against a real sandbox

Apple Container is not available in CI, and nothing in Tasks 1-7 proves that a real `claude` runs in a real pane with real keys reaching it. This task is run **by a human, or by an agent driving `scripts/tui-smoke/`, on a Mac with Apple Container**, from the worktree.

**Files:** none modified. This is verification.

**Interfaces:**
- Consumes: everything from 4a and 4b.
- Produces: nothing. A failure is fixed in the task that owns the file, and re-run.

Throughout, write scratch scripts and captures into the session scratchpad, not the repo.

- [ ] **Step 1: Build, and check the whole gate**

```bash
cd /Users/elliott/Projects/cspace-control-plane-4b
make check && make test-race && make build
```
Expected: green, no races, `bin/cspace-go` built.

- [ ] **Step 2: Boot a throwaway sandbox**

```bash
cd /Users/elliott/Projects/cspace-control-plane-4b && ./bin/cspace-go up panecheck --no-attach
```
Expected: the boot completes and prints the `attach:` / `browse:` summary. The project here is `cspace`, so the container is `cspace-cspace-panecheck`.

- [ ] **Step 3: The startup sweep reaps a record whose process is dead**

Plant a record for a pid that cannot exist, then start the dashboard:

```bash
mkdir -p ~/.cspace/controlplane/cspace/panecheck
cat > ~/.cspace/controlplane/cspace/panecheck/cspace-claude.dev-ttys999.json <<'JSON'
{"session":"cspace-claude","tty":"/dev/ttys999","pid":2147483646,"at":"2026-09-18T00:00:00Z"}
JSON
cd /Users/elliott/Projects/cspace-control-plane-4b
python3 - <<'PY'
import os, sys
sys.path.insert(0, "scripts/tui-smoke")
from tuilib import Tui
t = Tui(cwd=os.getcwd()); t.pump(14); print(t.display()); t.quit()
PY
ls ~/.cspace/controlplane/cspace/panecheck/
```
Expected: the record file is gone (the tty tmux never listed, so it was deleted without a detach), `attach.lock` — if present — survives, and the footer briefly said `swept 1 stale tmux client(s)`.

- [ ] **Step 4: A Claude pane attaches and shows Claude Code's UI**

Run `./bin/cspace-go tui` in a real terminal, select `panecheck`, press Enter.

Expected: within a few seconds the main area fills with Claude Code's own interface — its banner, its prompt box — inside the window, with the sidebar still on the left and a tab reading `cspace/panecheck · claude`. The pane is unframed (see Task 7 Step 7's spec amendment); the cue that the keyboard is in it is that the tab is lit, the footer names the leader's keys, and the cursor is inside the pane. In another terminal:

```bash
container exec cspace-cspace-panecheck tmux list-clients -t cspace-claude -F '#{client_tty}'
ls ~/.cspace/controlplane/cspace/panecheck/
```
Expected: exactly one tty, and one `cspace-claude.<tty>.json` record beside `attach.lock`.

- [ ] **Step 5: Keys reach it**

In the Claude pane, with the pane focused:

- type `what files are in /workspace?` and press **Enter** → Claude answers. Plain typing and Enter work.
- press **Shift+Enter** mid-message → a newline appears in Claude's box rather than the message sending. This is the kitty CSI-u path: Claude Code turns the protocol on at startup, so the overlay must be sending `ESC[13;2u`.
- start a long task ("read every file under /workspace and summarize it") and press **Ctrl+C** → Claude interrupts. The dashboard must **not** quit: with a pane focused Ctrl+C belongs to the child.
- press **Up** → Claude's history recall moves. **Ctrl+Left** / **Ctrl+Right** move by word in its input box.
- press **Esc** → Claude's own escape handling responds.

Expected: all six. If Shift+Enter sends the message instead of inserting a newline, the kitty tracking is not seeing the child's `CSI > u` — check `vtEmulator.registerKitty` and that the tmux config still carries `set -g extended-keys always`.

- [ ] **Step 6: Resize propagates**

Resize the terminal window while the Claude pane is focused.

Expected: Claude redraws to the new width within a second and its box is the right size. `container exec cspace-cspace-panecheck tmux list-clients -t cspace-claude -F '#{client_width}x#{client_height}'` reports the new geometry.

- [ ] **Step 7: A shell pane, and a host shell**

Leader (`Ctrl+Space`) then `h` to the sidebar, select `panecheck`, press `s`.

Expected: a second tab, `cspace/panecheck · shell`, with a `dev@…:/workspace$` prompt. `ls`, `pwd` and `top` (then `q`) all work — `top` is the ncurses check, which is what the TERM mapping exists for.

Leader `t` → pick **Host shell**.

Expected: a third tab titled `host · shell`, running your own login shell on the Mac, with no container in the picture — `hostname` prints the Mac's name.

- [ ] **Step 8: Tabs, focus and scroll**

- Leader `n` and `p` cycle the tabs, wrapping.
- Leader `h` focuses the sidebar; the footer switches from the leader's keys to the sidebar's; `j`/`k` move the selection; `Tab` returns to the main area.
- In the shell pane, run `seq 1 500`, then leader `[`, then `PageUp` a few times.

Expected: the view walks back through the history, the top line says how many lines back it is, `PageDown` returns, and pressing any ordinary letter drops straight back to live without that letter reaching the shell.

- [ ] **Step 9: The supervisor view on a real event stream**

Leader `h`, select `panecheck`, press `a`. Then from another terminal:

```bash
cd /Users/elliott/Projects/cspace-control-plane-4b
./bin/cspace-go send panecheck "list three files in /workspace and explain what each one does"
```

Expected: within two seconds the supervisor tab fills with the agent's prose — rendered as markdown, so bold and lists look like bold and lists, not asterisks — with one dim `⟡ Read(/workspace/…)` line per tool call. The spinner runs while it works and the status line returns to a rule when the result lands. Type a follow-up into the box and press **Enter**: the agent takes it. Press **Esc** while it is working: the footer reports `interrupt ok`.

- [ ] **Step 10: Close a pane and watch the detach**

Focus the Claude pane and press leader `x`.

Expected: the tab disappears, the footer reports nothing worse than `close pane`, and:

```bash
container exec cspace-cspace-panecheck tmux list-clients -t cspace-claude -F '#{client_tty}'
ls ~/.cspace/controlplane/cspace/panecheck/
```
prints **no** ttys and shows the record file gone. The tmux *session* is still there (`container exec cspace-cspace-panecheck tmux ls` lists `cspace-claude`), which is the whole point: reopening the pane rejoins the same Claude with its screen intact. Press Enter on `panecheck` again and confirm the conversation is still there.

- [ ] **Step 11: Quit leaves nothing behind**

With two panes open, press leader `q`.

Expected: the dashboard exits cleanly, the terminal is restored (no leftover alt screen, the cursor is visible), and:

```bash
ls ~/.cspace/controlplane/cspace/panecheck/
container exec cspace-cspace-panecheck tmux list-clients -t cspace-claude -F '#{client_tty}'
container exec cspace-cspace-panecheck tmux list-clients -t cspace-shell -F '#{client_tty}'
```
show no records but `attach.lock`, and no clients on either session. Then confirm the sessions survived: `container exec cspace-cspace-panecheck tmux ls` still lists both.

- [ ] **Step 12: `--keep-state` leaves a bootable row**

```bash
cd /Users/elliott/Projects/cspace-control-plane-4b
./bin/cspace-go up panecheck2 --no-attach
./bin/cspace-go down panecheck2 --keep-state
jq '.["cspace:panecheck2"]' ~/.cspace/sandbox-registry.json
```
Expected: the entry is still there with `"state": "stopped"`. In `./bin/cspace-go tui`, `panecheck2` shows with `✕`, the footer offers `u` on it, and pressing `u` boots it — the row turns `○` and gains its ports.

- [ ] **Step 13: An invalid sandbox name is refused**

```bash
cd /Users/elliott/Projects/cspace-control-plane-4b
./bin/cspace-go up '../../etc'; echo "exit=$?"
./bin/cspace-go down '../../etc'; echo "exit=$?"
./bin/cspace-go up 'dotted.name'; echo "exit=$?"
```
Expected: all three refuse with the shape message and a non-zero exit, and **nothing under `~/.cspace/`** is touched. Confirm with `ls ~/.cspace/clones/cspace/`.

- [ ] **Step 14: The no-tmux fallback still warns**

Only if an image predating tmux is still around (`container images list`). Boot a sandbox from it, open a Claude pane, and expect the pane to open with a sticky footer warning naming `cspace image build`. If no such image exists, say so and skip — the path is covered by `paneHost.Open`'s unit test.

- [ ] **Step 15: Clean up**

```bash
cd /Users/elliott/Projects/cspace-control-plane-4b
./bin/cspace-go down panecheck
./bin/cspace-go down panecheck2
rm -rf ~/.cspace/controlplane/cspace/panecheck ~/.cspace/controlplane/cspace/panecheck2
./bin/cspace-go tui   # confirm no panecheck rows, then leader q
```

- [ ] **Step 16: Commit (only if something needed fixing)**

If Steps 1-15 all passed there is nothing to commit — say so and stop. If a fix was needed, commit it against the task that owns the file:

```bash
cd /Users/elliott/Projects/cspace-control-plane-4b
git add -A
git commit -m "$(cat <<'EOF'
Fix <what the pane verification found>

Co-Authored-By: <model> <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01W64zstC3PZojywARTtnSW7
EOF
)"
```

---

## Self-review notes

Checked against the spec's `internal/controlplane` section, "Sessions", "Detach protocol", "Input", "Error handling", "Testing", rollout step 4 and all three open questions.

- **Layout** — Task 3 makes the design's move: the detail band under the sidebar, the tabs row carrying real tabs. The band is 24 columns wide there, which the spec acknowledges cannot hold a URL; the resolution recorded in `sidebarColumn`'s comment is that the sidebar's own port lines already carry the URL as an OSC 8 hyperlink, so nothing is lost but the visible text. Its header does have to fold in two at that width, though — four fields joined with ` · ` do not fit, and `fit` would cut the memory figure off the end, which is the number the band is there for. On a window under twelve rows the band is dropped entirely rather than squeezing the row list, because the list is the thing you cannot navigate without.
- **The pane is unframed, and that is a departure the spec now records.** The design asks for "the emulator's rendered screen inside a border colored by state". A border costs two of the main area's columns and two of its rows in a window whose whole point is to hold a full Claude Code session, and it duplicates a cue three other things already carry: the focused tab is lit only while the keyboard is in the main area (`styleTabActive` versus `styleTabFocused`), the footer switches to the leader's keys, and the cursor is placed in the pane only then. Task 7 Step 7 rewrites the spec's main-area bullets to say so, together with two smaller departures in the same neighbourhood: a failed open is a footer error and no tab (so there is no pane area to hold a retry key — retrying is pressing the same key again), and an exited pane offers the key that closes the tab rather than a separate restart binding, because closing and reopening rejoins the same tmux session with its screen intact — and the pane behind that dimmed screen has already been torn down, since the child exiting is what starts the reap.
- **Open question 1 (tab titles)** — `renderTabs` truncates from the **left** and prefixes a `+N` count, which is the question's stated default. Truncating from the left rather than the right is the one judgement added: it keeps the focused tab and the neighbours `n`/`p` reach next.
- **Open question 2 (supervisor content)** — assistant text through glamour plus one dim line per tool call, the question's default. `toolSummary` picks the argument a reader wants (`file_path`, `command`, …) and truncates to 60 runes; `decodeContent` tolerates both the block-array and bare-string shapes of `message.content`, because a strict decode would drop whole events for the shape it did not expect. The spinner beside the feed is driven by the fast ticker's `AgentStatus`, not by the shape of the last event: the log carries lines that are not `sdk-event`s at all, so "the last one is not a result" is a heuristic that can never go false again — and a spinner that never stops redraws the whole dashboard at spinner cadence for the rest of the session.
- **Open question 3 (`--no-tmux`)** — untouched. The flag stays hidden on `cspace attach`, as the default says.
- **Focus and input** — Task 4's routing is the spec's: sidebar keys when the sidebar has focus, `Tab` to the main area, and with a pane focused every key to the child except the leader. Three resolutions are recorded in the code. **Ctrl+C goes to the child when a pane has focus** — the spec's step-3 rule ("never routed to a modal") is kept for the sidebar and modals, but interrupting Claude is the most-used key in a pane and a dashboard that stole it would be unusable; the way out is the leader's quit, which the footer names. **Scroll mode consumes the key that leaves it** rather than forwarding it, so a stray letter cannot land in a child a person thought they were only reading. **Leader `v` is bound and dispatches to nothing**, per the brief: the config shape has to be stable now and step 5 is what makes it act. **The help overlay's "swallow the next key" rule moved up**, out of `handleNormalKey` and into `handleKey` ahead of the focus split: with a pane focused the sidebar path is never reached, so leaving it where step 3 put it would have made the overlay dismissible only by a second leader `?` while every other key went to the child the overlay was covering. **The supervisor tab's Enter and Esc are inside the one-action-at-a-time gate** even though the pane route never passes through `handleNormalKey`, where that gate lives: two actions in flight means two results, and the first to land clears the gate for the second, leaving the footer naming the wrong verb. The typed text is never discarded on a refusal. **A host shell is exempt from the existing-tab match** — it belongs to no sandbox, so the `(kind, project, sandbox)` key every other tab is found by is empty for all of them; asking for one always opens one, and its tab carries no sandbox identity. **The leader footer is trimmed, not truncated**: `LeaderHelp` is the seven second keys that fit one line, with `p` and `?` left to the help overlay, and the line is rendered through a local copy of `m.help` so `help` elides between bindings instead of `fit` cutting an escape sequence in half.
- **The leader is `Ctrl+Space`** and a test asserts it is not `Ctrl+b`, which Claude Code uses to background a task.
- **Sessions and the detach protocol** — pane open goes through `control.BeginAttach` (Task 7's host), which takes the attach lock and identifies the client by listing before and after; pane close runs `Attachment.Close`, which detaches and deletes the record. The spec orders the record delete after the teardown handshake; step 2 couples it to the detach inside `Attachment.Close`, and `closeTab`'s comment records that the difference is immaterial since both happen once the client is detached. **A pane that exits on its own detaches itself**, without waiting for the operator: the engine closes its dirty channel only inside `Close`, so the waiter's last signal is the dashboard's only notice that the child died, and `reapExited` — `closeTab`'s teardown minus the drop — runs there. The tab stays, showing the dimmed last screen; what goes is the tmux client, the record file, the pty and the parser buffer. **Quit closes every pane through the same path** before `tea.Quit` — the spec says quit does not confirm, not that it may skip the detach, and a window that vanished would leave a client attached in each sandbox and a record for the next sweep. Those closes run concurrently under one shared deadline, so quitting costs one `closeTimeout` rather than one per tab: `Attachment.Close` waits on its own tracking goroutine with no context, and one wedged sandbox should not make quitting look hung. Ctrl+C from the sidebar does the same.
- **The startup sweep** — Task 5, `control.SweepClientRecords`. It handles every record in one pass, including several in one sandbox's directory (a crash strands one per open pane, and reaping only the first would need as many restarts as there were panes). **Every delete is an explicit branch naming its evidence**, and there are four: an unparseable record (it names no client), a dead pid plus an *authoritative* container list that lacks the container (no exec — the step-2 carry-forward), a dead pid plus tmux answering that it does not list the tty, and a dead pid plus a listed tty the sweep then detached. Everything else keeps the record and, when the reason was a failure rather than evidence, counts it under `SweepResult.Errors`: a `container ls` that could not be believed (`liveContainers`' second return), a `list-clients` exec that failed, a detach that failed, and an unlink that failed. A directory that could not be read at all is counted there too, since its records were never seen and appear in neither `Kept` nor `Deleted`; a live pid is kept but is *not* an error, because that is evidence, not a failure. The two "absence of an answer" cases are the ones worth naming — an unlistable container list never means "gone", and a `list-clients` that could not run never means "nothing is attached" — because the record is the only handle the next sweep has, and deleting it on either would strand a client forever. `SweepResult` reports `Kept`/`Detached`/`Deleted`/`Errors` (`Detached` a subset of `Deleted`), and `attach.lock` is never touched.
- **Error handling** — a failed open is a footer error and no tab (tested); the no-tmux fallback is a sticky warning naming `cspace image build` (`paneHost.Open`), carried by `ResultWarn` so "it worked, now read this" keeps one mechanism; an exited pane reaps itself and then shows its last screen dimmed under the exit reason and the key that closes it; a failed close reports and still drops the tab, because a tab whose pane is gone is not navigable; poll failures degrade exactly as step 3 left them.
- **Testing** — model tests are messages in, state and rendered text out, as step 3 established. The tab tests open **real** panes running `sh` through the fake host, so the bookkeeping is exercised against the real engine without a container — and `fakeHost` carries the `*testing.T` so every pane it opens is closed by a `t.Cleanup`, the way `internal/pane`'s own `openTestPane` does: most of these tests never close the tab they opened, and without it one run leaves a dozen children, their goroutines and their pty masters alive for the life of the test binary, under `-race`; `make test-race` is re-run after Tasks 2, 4 and 7 because those are the ones that add concurrent owners, and the Global Constraints list says the same three — Task 2 also widens the target from `./internal/pane/...` to `./internal/pane/... ./internal/controlplane/...`, since what those three tasks can race is the tab bookkeeping, which the engine-only scope could never have reported. Nothing declares a fake the `internal/control` test package already has: `fakeExec`, `testTmux` and `fakeContainers` are reused by name, since a second `fakeContainers` in one package is a duplicate type, not a stylistic overlap. What is not testable without Apple Container — a real `claude` in a pane, Shift+Enter under kitty, the detach emptying `tmux list-clients` — is Task 8, step by step.
- **Eight step-3 tests change, and each one is named in the task that touches it** rather than left to a catch-all, because four of them mention neither `LabelAttach` nor `Actor.Attach` and would otherwise be found by `make check` instead of by reading. Task 2 splits `TestAttachAndInterruptDispatch` (Enter stops reaching the `Actor`) and updates all four `New(` call sites in `model_test.go`, not just `newTestModel`'s. Task 3 renames `TestTabsLineFitsANarrowWindow` to `TestTabsRowFitsANarrowWindow`, and folds the detail band's header so `TestMemoryUsageSurvivesAStatsFreeSnapshot` keeps passing rather than being weakened. Task 5 renames `TestInitKicksAllThreeCadences` to `TestInitKicksThreeCadencesAndTheStartupSweep` and widens its count to four. Task 7 repoints `TestViewGeometry`'s footer assertion at `"claude pane"` and `TestPollingContinuesWhileALongActionRuns` at `LabelOpenPane`, and deletes two fixtures the catch-all cannot see (`countingExecer`, `recordingActor.Attach`) because `unused` ignores `_test.go`. Task 1 extends `TestHelpOverlayToggles` with a pane binding, which is what proves the overlay's second help row is actually drawn, and adds the pane gates as four rows of `TestForRowDisablesWhatTheSelectionCannotDo`'s existing table rather than as a second test asking the same question a second way — only `canSupervisor` on a stopped sandbox stands alone, because the reason it is wider than `canAttach` is the point of it. Task 7 also rewords `TestLeaderIsDeclaredButNotAdvertised`'s "until panes land" comment: the assertion still holds — the leader is a prefix, not a key of its own, so it stays out of `ShortHelp` and `FullHelp` — but the reason changed.
- **The dashboard is never broken, but it is briefly narrower.** Between Task 2 and Task 7 the three pane keys report `open pane failed: no pane host configured`: Task 2 retires the old suspend-the-program attach and lands the `PaneHost` seam, and only Task 7 supplies an implementation. The Global Constraint is worded for that — start, render, and every step-3 action but this one — and both tasks say so in their own intro, because a task that silently takes a feature away for five commits is the kind of thing a reviewer finds by bisecting.
- **The help overlay renders two `FullHelpView` rows, not one wider one.** `help.FullHelpView` drops whole columns once their total passes its width and appends an ellipsis; the overlay is 74 columns at a 100-column window and step 3's four columns already fill it, so pane bindings appended as a fifth and sixth column would have been invisible at every realistic size while still passing every test. `PaneFullHelp` is therefore its own method rendered as its own row, and `TestHelpOverlayToggles` asserts one of its labels is on screen.
- **The picker applies the gate `forRow` gives the sidebar keys for free.** `updatePicker` is reached from the leader, not from a binding `forRow` disabled, so it checks `canAttach` itself and refuses anything but a host shell on a sandbox that is not running. 4a Task 6 is what makes that reachable: a `--keep-state` teardown leaves a selectable stopped row whose `Container` is still set, so an ungated pick would have failed at the tmux probe with an error about a transport rather than about a stopped sandbox.
- **`supervisor.view` is read-only.** It was the one place a `View` call mutated shared state — resize rebuilds the glamour renderer and calls `SetContent` — which is safe today only because bubbletea v2 runs `View` on the event-loop goroutine. A supervisor now learns its geometry in `openOrFocus` and in the `tea.WindowSizeMsg` fan-out, which pass the same numbers `paneArea` is handed — because `View` sizes the main area from `paneSize()` itself rather than recomputing `bodyHeight-1` and `mainWidth-2` beside it, which would agree at every usable window and disagree at a tiny one, where `paneSize` floors and the raw arithmetic does not.
- **Dependency direction** — asserted with `go list -deps` in Task 2 for both packages. `internal/pane` stays free of every cspace package; `internal/controlplane` gains `internal/pane` and keeps out of `internal/cli`; the one place all three meet is `internal/cli/controlplane_panes.go`, which is the package allowed to see them.
- **What `New`'s new parameter costs** — every construction site moves at once (Task 2 for the tests, Task 7 for `cmd_tui.go`), and a nil host becomes `nopPaneHost`, which fails every open with an explanation rather than a nil dereference. That is the same fail-closed rule `control.Client` applies to its own unset seams.
- **Out of scope, and left alone** — mouse, image paste, `cspace ports` / `control.Ports` convergence, the two tmux drivers (`cli.defaultTmux` for `cspace attach`, `Client.Tmux()` for everything else), the fast-cadence findings, and panes for sidecars. `cspace attach` is unchanged, including `restoreBlockingStreams`, which panes do not need — a pane's child gets a pty this process owns, not the terminal's descriptors.
