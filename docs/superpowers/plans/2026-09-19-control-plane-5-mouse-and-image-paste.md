# Mouse and image paste Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finish the control plane: `cspace tui` takes the mouse — click to select a sidebar row, focus a tab or focus the main area, wheel to scroll — and leader `v` pastes the clipboard's image into a pane as a file path.

**Architecture:** The model gains one small value, `geometry`, computed from the same arithmetic the view uses and refreshed at the end of every `Update`, so a mouse message can be hit-tested without rendering anything. Clicks and wheels are routed in a new `mouse.go` against that geometry and never reach a child. Image paste gets a third seam beside `Data`, `Actor` and `PaneHost`: a `Clipboard` interface declared in `internal/controlplane` and satisfied in `internal/cli` by `osascript` (the PNG) and `pbpaste` (the text fallback), always from inside a `tea.Cmd`.

**Tech Stack:** Go 1.26; `charm.land/bubbletea/v2 v2.0.9` (mouse messages and `View.MouseMode`), `charm.land/lipgloss/v2 v2.0.6`, `charm.land/bubbles/v2 v2.2.1` (`viewport`), `github.com/charmbracelet/x/ansi v0.11.8`; `internal/pane`, `internal/control`. **No new module.**

**Spec:** `docs/superpowers/specs/2026-09-17-control-plane-design.md` — this plan implements **rollout step 5, "Mouse and image paste."**, the last step. Steps 1–4 are landed; this plan's Task 6 strikes step 5 through in the Rollout list the way they are.

## Global Constraints

- **Scope is rollout step 5 and nothing else.** No drag-to-select, no copy inside a pane, no forwarding of any mouse event to the child (spec Non-goals). No new bindings: `v` was declared in plan 4b Task 1 and deliberately left undispatched — this plan wires it, it does not redeclare it.
- **No new module.** `go.mod` and `go.sum` must be byte-identical at the end of this plan. `osascript` and `pbpaste` are host binaries invoked with `os/exec`; a missing one is a footer error, never a crash and never a build-time dependency.
- **Dependency direction**, unchanged from 4b and re-checked in Task 4:
  - `internal/pane` imports **neither** `internal/controlplane` **nor** `internal/cli`.
  - `internal/controlplane` imports `internal/pane` and `internal/control` only.
  - `internal/control` imports none of them.
- **Concurrency rules, unchanged:** nothing outside the pane engine touches an emulator except through `*pane.Pane`'s methods; `Render`, `Cursor` and `ScrollbackView` are called only from `View`; the writer channel is bounded and never blocks the UI. **No `PaneHost` method and no `Clipboard` method may be called from `Update`** — every one of them goes through a `tea.Cmd` with a bounded context, because both shell out (an exec into a container; `osascript`).
- **bubbletea v2 mouse API facts this plan relies on**, read from the module source at `charm.land/bubbletea/v2 v2.0.9` in `$(go env GOMODCACHE)` on 2026-09-19, not from memory:
  - `mouse.go` declares four concrete message types, each a `Mouse` under a new name: **`tea.MouseClickMsg`**, **`tea.MouseReleaseMsg`**, **`tea.MouseWheelMsg`**, **`tea.MouseMotionMsg`**. `tea.MouseMsg` is an *interface* (`fmt.Stringer` plus `Mouse() Mouse`) that all four satisfy — a `case tea.MouseMsg:` arm would swallow all four, so this plan matches the concrete types.
  - `type Mouse struct { X, Y int; Button MouseButton; Mod KeyMod }`. **"The X and Y coordinates are zero-based, with (0,0) being the upper left corner of the terminal."** The concrete messages are defined `type MouseClickMsg Mouse`, so `msg.X`, `msg.Y` and `msg.Button` are read directly off them — no `.Mouse()` call is needed.
  - Buttons are aliases of `ultraviolet`'s, which alias `x/ansi`'s: `tea.MouseNone`, `tea.MouseLeft`, `tea.MouseMiddle`, `tea.MouseRight`, `tea.MouseWheelUp`, `tea.MouseWheelDown`, `tea.MouseWheelLeft`, `tea.MouseWheelRight`, `tea.MouseBackward`, `tea.MouseForward`, `tea.MouseButton10`, `tea.MouseButton11`. In `x/ansi v0.11.8`'s `mouse.go`, `MouseLeft = MouseButton1`, `MouseWheelUp = MouseButton4`, `MouseWheelDown = MouseButton5` — which is why the SGR codes the smoke harness sends in Task 7 are `0` for a left press and `64`/`65` for the wheel.
  - **Enabling the mouse is a property of the rendered view, not a program option and not a command.** There is no `tea.WithMouseCellMotion` and no `tea.EnableMouseCellMotion` in v2.0.9 (`grep -n -i mouse options.go commands.go` finds nothing). `tea.View` carries `MouseMode MouseMode` (`tea.go:177`), and `cursed_renderer.go:384` diffs it against the last view and emits the enable/disable sequences. The three values (`tea.go:283-306`) are `tea.MouseModeNone` (the zero value), **`tea.MouseModeCellMotion`** — *"enables mouse click, release, and wheel events. Mouse movement events are also captured if a mouse button is pressed (i.e., drag events). Cell motion mode is better supported than all motion mode."* — and `tea.MouseModeAllMotion`. So "mouse mode is enabled at program start" means **`View()` sets `v.MouseMode = tea.MouseModeCellMotion` on every return path, including the pre-size `"starting cspace tui…"` one.**
  - `tea.View` also has an `OnMouse func(MouseMsg) Cmd` hook the renderer calls (`tea.go:126`, `tea.go:808`). **This plan does not use it:** it is an extra path in addition to `Update`, its result is dispatched from the renderer's goroutine, and hit-testing belongs in `Update` against stored geometry, which is what makes it testable.
- **Mouse messages must be consumed explicitly.** `Model.Update` ends with a fall-through that hands any unmatched message to whichever widget owns the keyboard (`m.input`, `m.confirm`, `m.picker`). Without arms of their own, mouse messages would be fed to a `textinput` or a `huh.Form`. Every one of the four types gets an arm.
- **Timestamp format is `20060102-150405.000`** (Go reference layout), and the `paste/` directory is created on demand with mode **0700**.
- **`make check` must be green after every task.** The pasteboard tests Task 4 adds are gated behind `CSPACE_CLIPBOARD_TESTS=1` and skip without it: `make check` runs `go test ./...` and `scripts/release.sh` runs `make check`, so ungated they would overwrite the developer's clipboard — and an image on it cannot be put back — on every check and every release. `make test-race` (`go test -race -count=1 ./internal/pane/... ./internal/controlplane/... ./internal/control/...`) must be clean after **Tasks 2, 3 and 5** — the three that add a new reader or writer of a live `*pane.Pane` from the UI goroutine. Each of those tasks' final step runs it.
- **`cspace tui` must start, render and keep every step-4 capability working after every task.** Nothing in this plan is allowed a "deliberately down in between" window: the geometry lands inert (Task 1), then each input path is switched on behind it.
- **Preserve these known behaviours; do not "fix" them:**
  - `.cspace/context/findings/2026-07-20-tui-down-reports-benign-teardown-warnings-as-failure.md` — `control.Down` reports any `warning:` text as failure. Unchanged.
  - `.cspace/context/findings/2026-07-20-tui-browser-row-orphaned-when-project-has-no-registry-entry.md` — `Correlate` derives projects from registry entries only. Unchanged.
  - `.cspace/context/findings/2026-09-19-scroll-mode-never-reaches-a-tmux-backed-panes-history.md` — a tmux-backed pane has no emulator scrollback, because tmux runs on the alternate screen. This plan does **not** close it: the wheel over such a pane is a no-op with the same footer notice leader `[` gives. Task 6 adds an Updates entry saying the wheel inherited the limitation; the finding stays `open`.
- **`cspace attach` is unchanged.** It keeps its own foreground-child flow, its signal handling and `restoreBlockingStreams`. Nothing in this plan touches `cmd_attach.go`.
- **tmux inside the sandbox has `mouse off`** (`lib/runtime/tmux.conf`: `set -g mouse off`, under the file's own "No key bindings of any kind" rule). That is the other half of "nothing is forwarded to the child": even if a byte escaped, the guest tmux would not act on it. Do not change that file.
- **Worktree:** `/Users/elliott/Projects/cspace-control-plane-5`, branch `control-plane-5-mouse-paste`, from `c0bf984` (plan 4b's final head). Do not touch `/Users/elliott/Projects/cspace` or the sibling worktrees.
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
| `geometry.go` | `rect`, `tabSpan`, `geometry`, `Model.computeGeometry` (the `Update` wrapper lives in `model.go`) | 1 |
| `mouse.go` | `handleClick`, `handleWheel`, `wheelMain`, `selectListRow`, `focusTab` | 2, 3 |
| `clipboard.go` | `Clipboard`, `ErrNoImage`, `nopClipboard`, `pasteMsg`, `pasteImageCmd` | 4, 5 |

New file in `internal/cli`:

| File | Responsibility | Task |
|---|---|---|
| `clipboard.go` | `osaClipboard` — the `osascript` PNG probe/write and the `pbpaste` text read | 4 |

New tests (same packages): `internal/controlplane/geometry_test.go` (1), `internal/controlplane/mouse_test.go` (2, 3), `internal/controlplane/clipboard_test.go` (5), `internal/cli/clipboard_test.go` (4).

New script: `scripts/tui-smoke/mouse.py` (7).

Modified:

| File | Change | Task |
|---|---|---|
| `internal/controlplane/view_sidebar.go` | `sidebarSplit` extracted from `sidebarColumn`'s band arithmetic | 1 |
| `internal/controlplane/view_pane.go` | `planTabs` extracted from `renderTabs`; `sidebarColumn` calls `sidebarSplit` | 1 |
| `internal/controlplane/model.go` | `geom` field, `Update`→`update` plus the refreshing wrapper (1); the four mouse arms (2, 3); the `clip` field and `New`'s parameter (4); the `pasteMsg` arm (5) | 1, 2, 3, 4, 5 |
| `internal/controlplane/view.go` | `v.MouseMode` on both return paths (2); `helpView`'s mouse and selection note (6) | 2, 6 |
| `internal/controlplane/leader.go` | `moveTab` in terms of `focusTab` and the shared `noScrollbackNotice` (3); the `PasteImage` arm dispatches (5) | 3, 5 |
| `internal/controlplane/leader_test.go` | `TestLeaderDispatch`'s `v` case: the binding pastes now, so the placeholder assertion is replaced | 5 |
| `internal/controlplane/model_test.go`, `detach_test.go`, and the `geometry_test.go`/`mouse_test.go` cases Tasks 1–2 add | the `New` call sites gain the clipboard | 4 |
| `internal/cli/cmd_tui.go` | `newClipboard(home)` passed to `controlplane.New` | 4 |
| `docs/superpowers/specs/2026-09-17-control-plane-design.md` | the Input section's Mouse and Paste paragraphs; Rollout step 5 struck through | 6 |
| `CLAUDE.md` | the `cspace tui` command line and the **controlplane** architecture bullet | 6 |
| `.cspace/context/findings/2026-09-19-scroll-mode-never-reaches-a-tmux-backed-panes-history.md` | an Updates entry: the wheel inherits the limitation | 6 |

---

### Task 1: Geometry — one source of truth for where things are drawn

Hit testing needs to know which screen cell holds which sidebar row, which tab and which part of the main area. The model does not keep that today: the view computes it while rendering and throws it away. This task extracts the two pieces of arithmetic the view owns (`sidebarSplit`, `planTabs`), builds a `geometry` value from them, and refreshes it at the end of every `Update`. Nothing reads it yet — this task adds no behaviour at all, which is what makes it independently reviewable.

**Files:**
- Create: `internal/controlplane/geometry.go`
- Modify: `internal/controlplane/view_sidebar.go`, `internal/controlplane/view_pane.go`, `internal/controlplane/model.go`
- Test: `internal/controlplane/geometry_test.go`

**Interfaces:**
- Consumes: `Model.width`, `Model.height`, `Model.rows`, `Model.live`, `Model.ports`, `Model.selected`, `Model.tabs`, `Model.focused`, `Model.focus`, `mainWidthFor(int) int`, `sidebarWidth`/`sidebarInner`, `sidebarLines`, `sidebarWindow`, `fit`, `tabsElided`, `styleTabActive`/`styleTabFocused`/`styleTabIdle` — all existing.
- Produces:
  - `type rect struct{ x, y, w, h int }` with `func (r rect) contains(x, y int) bool`
  - `type tabSpan struct{ index, from, to int }` — `index` into `Model.tabs`; `[from, to)` absolute screen columns
  - `type geometry struct { sidebar, list, main rect; listRows []int; tabsY int; tabs []tabSpan }`
  - `func (m Model) computeGeometry() geometry`
  - `Model.geom geometry` — refreshed by `Update`, read by Tasks 2 and 3
  - `func sidebarSplit(height int) (list, band int)`
  - `type tabsPlan struct{ row string; spans []tabSpan }` and `func planTabs(tabs []*tab, focused, width int, active bool) tabsPlan`; `renderTabs` keeps its signature and returns `planTabs(...).row`

- [ ] **Step 1: Write the failing test**

Create `internal/controlplane/geometry_test.go`:

```go
package controlplane

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/elliottregan/cspace/internal/control"
)

// rect is the hit test every mouse message ends in, and its edges are a
// half-open interval — so the inputs that matter are the ones an interior
// point never reaches. A table, because they are a list and not a story.
func TestRectContains(t *testing.T) {
	r := rect{x: 5, y: 2, w: 3, h: 4} // columns 5..7, rows 2..5
	for _, tc := range []struct {
		name string
		x, y int
		want bool
	}{
		{"the origin cell", 5, 2, true},
		{"the last included cell", 7, 5, true},
		{"one column left of it", 4, 3, false},
		{"one column past the right edge", 8, 3, false},
		{"one row above it", 6, 1, false},
		{"one row past the bottom edge", 6, 6, false},
		{"a negative column", -1, 3, false},
		{"a negative row", 6, -1, false},
	} {
		if got := r.contains(tc.x, tc.y); got != tc.want {
			t.Errorf("%s: rect%+v.contains(%d, %d) = %v, want %v", tc.name, r, tc.x, tc.y, got, tc.want)
		}
	}

	// A rect with no width or no height is nowhere at all, whatever its
	// origin says — which is what every region of an unsized model is.
	for _, empty := range []rect{{x: 5, y: 2, w: 0, h: 4}, {x: 5, y: 2, w: 3, h: 0}, {}} {
		if empty.contains(empty.x, empty.y) {
			t.Errorf("rect%+v contains its own origin", empty)
		}
	}
}

// The geometry has to agree with what View actually paints, and the only
// honest way to check that is to render the same thing and look at where
// the text landed. These tests therefore render and then index, rather
// than re-deriving the numbers a second time and comparing two copies of
// the same arithmetic.

func TestGeometryIsEmptyBeforeTheFirstWindowSize(t *testing.T) {
	m := New(&fakeData{snap: testSnapshot()}, &recordingActor{}, nopPaneHost{}, NewKeyMap(nil))
	mm, _ := m.Update(snapshotMsg{snap: testSnapshot()})
	g := mm.(Model).geom
	if g.list.contains(0, 0) || g.main.contains(30, 5) || g.sidebar.contains(0, 0) {
		t.Fatalf("an unsized model claims to occupy the screen: %+v", g)
	}
	if len(g.listRows) != 0 || len(g.tabs) != 0 {
		t.Errorf("listRows=%v tabs=%v, want both empty", g.listRows, g.tabs)
	}
}

func TestGeometryListRowsMatchTheRenderedSidebar(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	g := m.geom

	if g.sidebar.w != sidebarWidth {
		t.Errorf("sidebar width = %d, want %d", g.sidebar.w, sidebarWidth)
	}
	if g.list.h <= 0 || g.list.h != len(g.listRows) {
		t.Fatalf("list height %d does not match listRows %d", g.list.h, len(g.listRows))
	}

	// The same lines View renders into the list region, plain.
	lines := strings.Split(plain(renderSidebar(m.rows, m.live, m.ports, m.selected, g.list.h)), "\n")
	if len(lines) != g.list.h {
		t.Fatalf("renderSidebar gave %d lines, geometry says %d", len(lines), g.list.h)
	}
	named := 0
	for y, idx := range g.listRows {
		if idx < 0 {
			continue
		}
		if idx >= len(m.rows) {
			t.Fatalf("listRows[%d] = %d, out of range for %d rows", y, idx, len(m.rows))
		}
		if m.rows[idx].Kind == control.RowSidecar {
			// Correlate prefixes a sidecar with its sandbox's name and
			// sidebarRow strips it straight back off ("mercury-convex"
			// draws as "   ├ convex"), so a sidecar's line does not hold
			// its own Name. The mapping is still checked by the rows
			// either side of it.
			continue
		}
		name := m.rows[idx].Name
		if !strings.Contains(lines[y], truncatedName(name)) {
			t.Errorf("listRows[%d] says row %q, but the line reads %q", y, name, lines[y])
		}
		named++
	}
	if named == 0 {
		t.Fatal("no line was mapped to a row")
	}
}

// truncatedName is how much of a name a 24-column sidebar can show. The
// fixture's names all fit; this is here so a future fixture with a long
// name fails on the geometry rather than on the ellipsis.
func truncatedName(name string) string {
	if len(name) > sidebarContent-2 {
		return name[:sidebarContent-2]
	}
	return name
}

func TestGeometryTabSpansMatchTheRenderedTabsRow(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = stepPump(t, m, "enter") // a claude pane
	m.focus = focusSidebar
	m = stepPump(t, m, "s") // a shell pane
	mustTabs(t, m, 2)

	g := m.geom
	if len(g.tabs) != 2 {
		t.Fatalf("tab spans = %v, want one per tab", g.tabs)
	}
	row := []rune(plain(m.tabsRow(mainWidthFor(m.width))))
	for _, s := range g.tabs {
		from, to := s.from-sidebarWidth, s.to-sidebarWidth
		if from < 0 || to > len(row) || from >= to {
			t.Fatalf("span %+v is outside the rendered row of %d cells", s, len(row))
		}
		want := m.tabs[s.index].title()
		if got := string(row[from:to]); !strings.Contains(got, want) {
			t.Errorf("span %+v reads %q, want it to hold %q", s, got, want)
		}
	}
	if g.tabs[0].to > g.tabs[1].from {
		t.Errorf("spans overlap: %+v", g.tabs)
	}
}

func TestGeometryMainAreaMatchesTheCursorArithmetic(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	g := m.geom
	// View places the pane's cursor at (x+sidebarWidth+1, y+1), so the
	// pane's own cell (0,0) is that screen cell — and it must be inside
	// the rect a click is tested against.
	if !g.main.contains(sidebarWidth+1, 1) {
		t.Errorf("main %+v does not contain the pane's first cell", g.main)
	}
	if g.tabsY != 0 || g.main.y != 1 {
		t.Errorf("tabsY=%d main.y=%d, want 0 and 1", g.tabsY, g.main.y)
	}
	// The footer is not part of it.
	if g.main.contains(sidebarWidth+1, m.height-1) {
		t.Errorf("main %+v swallowed the footer row %d", g.main, m.height-1)
	}
	// Neither is the sidebar.
	if g.main.contains(sidebarWidth-1, 5) {
		t.Errorf("main %+v reaches into the sidebar", g.main)
	}
}

func TestGeometryFollowsAResize(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	wide := m.geom.main.w
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	narrow := mm.(Model).geom
	if narrow.main.w >= wide {
		t.Errorf("main width %d did not shrink from %d", narrow.main.w, wide)
	}
	if narrow.main.w != mainWidthFor(60) {
		t.Errorf("main width = %d, want %d", narrow.main.w, mainWidthFor(60))
	}
	if narrow.list.h != len(narrow.listRows) {
		t.Errorf("listRows (%d) did not follow the list height (%d)", len(narrow.listRows), narrow.list.h)
	}
}

func TestSidebarSplitMatchesTheRenderedColumn(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	for _, height := range []int{1, 8, 11, 12, 13, 23, 39, 60} {
		list, band := sidebarSplit(height)
		got := len(strings.Split(m.sidebarColumn(height), "\n"))
		want := list
		if band > 0 {
			want = list + 1 + band // the rule between them
		}
		if height <= 0 {
			want = 1
		}
		if got != want {
			t.Errorf("height %d: sidebarColumn rendered %d lines, split says %d (list %d, band %d)",
				height, got, want, list, band)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/controlplane/ -run 'Geometry|SidebarSplit' -v`
Expected: FAIL to compile — `undefined: sidebarSplit`, `m.geom undefined`, `undefined: rect`.

- [ ] **Step 3: Extract `sidebarSplit`**

In `internal/controlplane/view_sidebar.go`, add below `renderSidebar`:

```go
// sidebarSplit divides the sidebar column of `height` lines into the row
// list and the detail band beneath it, and reports how many lines each
// gets. A zero band means there is no band and no rule: below twelve lines
// there is not enough for both, and the row list is the thing you cannot
// navigate without.
//
// It is a function rather than four lines inside sidebarColumn because the
// mouse has to know where the list ends without rendering anything, and two
// copies of this arithmetic is two places for the list's last line to move
// out from under a click.
func sidebarSplit(height int) (list, band int) {
	if height <= 0 {
		return 0, 0
	}
	band = height / 3
	switch {
	case height < 12:
		band = 0
	case band < 6:
		band = 6
	case band > 14:
		band = 14
	}
	list = height
	if band > 0 {
		list = height - band - 1 // the rule
	}
	return list, band
}
```

In `internal/controlplane/view_pane.go`, replace the body of `sidebarColumn`'s band arithmetic. The current lines

```go
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
```

become

```go
	list, band := sidebarSplit(height)
```

- [ ] **Step 4: Extract `planTabs`**

In `internal/controlplane/view_pane.go`, replace the whole of `renderTabs` (keeping its doc comment, which still describes the elision policy) with a plan plus a thin renderer:

```go
// tabsPlan is the tabs row before it is a string: the row itself, and where
// each tab's label landed. The columns are relative to the row's own start,
// so the caller adds the sidebar's width to get screen columns.
//
// One function decides both, because a click that lands on the wrong tab is
// exactly what two copies of the elision arithmetic would produce.
type tabsPlan struct {
	row   string
	spans []tabSpan
}

// planTabs lays out the tabs row: one tab per open pane, titled
// "<project>/<sandbox> · <kind>".
//
// active says whether the keyboard is pointed at the main area. It is the
// third cue that typing will reach the child, beside the footer switching to
// the leader's keys and the cursor appearing in the pane — and the only one
// that is visible without reading anything.
//
// When the tabs do not fit, the row keeps the focused tab and shrinks a
// window around it until the rest fits, counting what it dropped on each
// side: "+L" before the row, "+R" after it. That is the design's open
// question 1, whose default is to drop from the left and say how many — the
// right-hand count is what makes the answer honest when the focused tab is
// near the start, which is also the case where dropping from the left alone
// can drop nothing at all.
//
// Which side gives way is decided by distance from the focused tab, so its
// neighbours — the ones n/p reaches next — are the last to go, and a tie
// goes to the left, which is the documented default.
//
// The elision markers get no span: a click on "+2" does nothing, because
// "+2" is not a tab and there is no sensible tab for it to mean.
func planTabs(tabs []*tab, focused, width int, active bool) tabsPlan {
	if len(tabs) == 0 {
		return tabsPlan{}
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

	from, to := 0, len(tabs)
	for {
		left, right := tabsElided(from, true), tabsElided(len(tabs)-to, false)
		row := strings.Join(rendered[from:to], "")
		if ansi.StringWidth(left)+ansi.StringWidth(row)+ansi.StringWidth(right) <= width {
			return tabsPlan{
				row:   left + row + right,
				spans: tabSpansFor(rendered, from, to, ansi.StringWidth(left)),
			}
		}
		if to-from <= 1 {
			break
		}
		// Whichever side is farther from the focused tab gives way; a tie
		// drops from the left.
		if focused-from >= to-1-focused {
			from++
		} else {
			to--
		}
	}

	// Even the focused tab alone does not fit: truncate its text rather
	// than the rendered string, so no escape sequence is cut in half and no
	// closing reset is lost.
	//
	// The budget is the width less the three things that are added around
	// the title and are not part of it: the two elision counts, measured
	// rather than assumed (a two-digit count is four cells, not three), and
	// the two columns of Padding(0, 1) that every tab style carries. A
	// hard-coded 4 here is one cell too many and the row comes out at
	// width+1.
	left, right := tabsElided(focused, true), tabsElided(len(tabs)-focused-1, false)
	budget := width - ansi.StringWidth(left) - ansi.StringWidth(right) - 2
	if budget < 1 {
		budget = 1
	}
	label := focusedStyle.Render(fit(tabs[focused].title(), budget))
	x := ansi.StringWidth(left)
	return tabsPlan{
		row:   left + label + right,
		spans: []tabSpan{{index: focused, from: x, to: x + ansi.StringWidth(label)}},
	}
}

// tabSpansFor walks the rendered tabs from column x, giving each one the
// columns it occupies. Widths are measured, not assumed: the styles carry
// padding and a title can hold a multi-byte separator.
func tabSpansFor(rendered []string, from, to, x int) []tabSpan {
	spans := make([]tabSpan, 0, to-from)
	for i := from; i < to; i++ {
		w := ansi.StringWidth(rendered[i])
		spans = append(spans, tabSpan{index: i, from: x, to: x + w})
		x += w
	}
	return spans
}

// renderTabs is planTabs' row. Every caller that only draws goes through
// this; the mouse goes through planTabs for the spans.
func renderTabs(tabs []*tab, focused, width int, active bool) string {
	return planTabs(tabs, focused, width, active).row
}
```

- [ ] **Step 5: Add the geometry type and the computation**

Create `internal/controlplane/geometry.go`:

```go
package controlplane

// Where the dashboard puts things on screen, kept as data so a mouse
// message can be answered without rendering anything.
//
// The arithmetic is not repeated here: sidebarSplit and planTabs are the
// same functions View draws from, and everything else is read off
// mainWidthFor and the same bodyHeight View computes. What this file adds
// is the *inverse* — cell to meaning — which rendering alone cannot give.

// rect is a half-open region of the screen in terminal cells: columns
// [x, x+w) and rows [y, y+h). A zero width or height is nowhere at all,
// which is what every hit test against an unsized model gets.
type rect struct{ x, y, w, h int }

func (r rect) contains(x, y int) bool {
	return r.w > 0 && r.h > 0 && x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

// tabSpan is one tab's label on the tabs row: its index in Model.tabs, and
// the half-open range of screen columns [from, to) it occupies.
type tabSpan struct {
	index    int
	from, to int
}

// geometry is the last layout, in screen cells.
//
// It is rebuilt at the end of every Update rather than in View, because
// View has a value receiver and cannot store anything — and because a
// mouse message must be answered against the layout the person was
// looking at when they clicked, which is the one the previous Update left
// behind.
type geometry struct {
	// sidebar is the whole left column, its vertical rule included.
	sidebar rect
	// list is the row list inside it, starting at the top.
	list rect
	// listRows maps a row of the list — index 0 is list.y — to an index
	// into Model.rows, or -1 for a line that belongs to no row (the
	// "— system —" divider, or padding below the last line).
	listRows []int

	// tabsY is the screen row the tabs sit on. main starts below it.
	tabsY int
	tabs  []tabSpan

	// main is the pane area: everything right of the sidebar and below the
	// tabs row, down to but not including the footer. It is one column
	// wider on each side than the emulator's own screen, because
	// styleMain's padding lives inside it — a click on that padding is
	// still a click on the pane area, which is all this rect is asked.
	main rect
}

// computeGeometry measures the layout View is about to draw.
//
// Every number here has exactly one other home: sidebarSplit is
// sidebarColumn's, sidebarWindow is renderSidebar's, planTabs is tabsRow's.
// bodyHeight and mainWidth are View's own arithmetic, repeated here because
// View has a value receiver and can store nothing. main.h is the body less
// the tabs row — NOT paneSize's floored row count, which View uses for the
// emulator itself — so on a window too small for a pane the rect is empty
// and every hit test against it is false.
func (m Model) computeGeometry() geometry {
	if m.width <= 0 || m.height <= 0 {
		return geometry{}
	}
	bodyHeight := m.height - 1 // the footer
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	mainWidth := mainWidthFor(m.width)
	listHeight, _ := sidebarSplit(bodyHeight)

	g := geometry{
		sidebar: rect{x: 0, y: 0, w: sidebarWidth, h: bodyHeight},
		list:    rect{x: 0, y: 0, w: sidebarInner, h: listHeight},
		tabsY:   0,
		main:    rect{x: sidebarWidth, y: 1, w: mainWidth, h: bodyHeight - 1},
	}

	if listHeight > 0 {
		lines := sidebarLines(m.rows, m.live, m.ports, m.selected)
		from, to := sidebarWindow(lines, m.selected, listHeight)
		g.listRows = make([]int, listHeight)
		for i := range g.listRows {
			g.listRows[i] = -1
		}
		for i, l := range lines[from:to] {
			if i >= listHeight {
				break
			}
			g.listRows[i] = l.row
		}
	}

	// The spans come back relative to the row's own start; the row starts
	// where the sidebar ends.
	for _, s := range planTabs(m.tabs, m.focused, mainWidth, m.focus == focusMain).spans {
		g.tabs = append(g.tabs, tabSpan{
			index: s.index,
			from:  s.from + sidebarWidth,
			to:    s.to + sidebarWidth,
		})
	}
	return g
}
```

- [ ] **Step 6: Store it on the model and refresh it**

In `internal/controlplane/model.go`, add the field to `Model`, directly under `width, height int`:

```go
	width, height int
	// geom is where the last layout put everything, so a mouse message can
	// be hit-tested without rendering. Update refreshes it after every
	// message; View never writes it.
	geom geometry
```

Then rename the existing method and wrap it. Change the signature line

```go
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
```

to

```go
// Update refreshes the layout geometry after handling a message, and does
// nothing else the method below does not.
//
// The refresh is here rather than at each of update's thirty-odd return
// points, and rather than in View, which has a value receiver and can store
// nothing. It runs on every message, including a pane's ~30/s redraw
// signal: it re-renders the row list and the tabs — the same work View
// does for those two regions, and small beside the emulator render that
// same signal triggers.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	mm, ok := next.(Model)
	if !ok {
		// Unreachable: every return in update is a Model. Kept so a future
		// arm that returns something else degrades to "no geometry" rather
		// than panicking under the operator's cursor.
		return next, cmd
	}
	mm.geom = mm.computeGeometry()
	return mm, cmd
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/controlplane/ -run 'Geometry|SidebarSplit|RenderTabs' -v`
Expected: PASS, including the existing `TestRenderTabs*` cases — `renderTabs` kept its signature and its output.

- [ ] **Step 8: Run the whole check**

Run: `make check`
Expected: green. Nothing reads `geom` yet, so no behaviour changed.

- [ ] **Step 9: Commit**

```bash
git add internal/controlplane/geometry.go internal/controlplane/geometry_test.go \
        internal/controlplane/view_pane.go internal/controlplane/view_sidebar.go \
        internal/controlplane/model.go
git commit -m "Keep the dashboard's layout as data so the mouse can hit-test it"
```

---

### Task 2: Mouse mode on, and the click

The mouse is switched on and clicks are routed. Nothing is forwarded to the child — there is no code path that could, since a click never reaches `SendKey`.

**Files:**
- Create: `internal/controlplane/mouse.go`
- Modify: `internal/controlplane/view.go`, `internal/controlplane/model.go`, `internal/controlplane/leader.go`
- Test: `internal/controlplane/mouse_test.go`

**Interfaces:**
- Consumes: `Model.geom` (Task 1), `rect.contains`, `tabSpan`, `Model.eventsCmd()`, `Model.moveSelection`, `notice`, `focusSidebar`/`focusMain`, `modeNormal`.
- Produces:
  - `func (m Model) handleClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd)`
  - `func (m Model) selectListRow(line int) (tea.Model, tea.Cmd)`
  - `func (m Model) focusTab(i int) Model` — this task's `moveTab` (Step 6) and the click both go through it

- [ ] **Step 1: Write the failing test**

Create `internal/controlplane/mouse_test.go`:

```go
package controlplane

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/elliottregan/cspace/internal/control"
)

// click delivers a left-button press at a zero-based screen cell and
// returns the new model. The release that a real terminal sends after it
// is delivered too, because the model must ignore it — a click that acted
// twice would move the selection twice.
func click(t *testing.T, m Model, x, y int) Model {
	t.Helper()
	mm, _ := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m = mm.(Model)
	mm, _ = m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	return mm.(Model)
}

// clickCmd is click without the release, for the cases that assert on the
// command a click produced.
func clickCmd(t *testing.T, m Model, x, y int) (Model, tea.Cmd) {
	t.Helper()
	mm, cmd := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	return mm.(Model), cmd
}

// listLineOf is the list line the given row index is drawn on, read out of
// the geometry rather than guessed.
func listLineOf(t *testing.T, m Model, row int) int {
	t.Helper()
	for y, idx := range m.geom.listRows {
		if idx == row {
			return y
		}
	}
	t.Fatalf("row %d is not on screen; listRows = %v", row, m.geom.listRows)
	return -1
}

func TestViewEnablesCellMotionMouseMode(t *testing.T) {
	m := New(&fakeData{snap: testSnapshot()}, &recordingActor{}, nopPaneHost{}, NewKeyMap(nil))
	// Before the first WindowSizeMsg, too: the mode is a property of every
	// view, and the starting screen is a view.
	if got := m.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Errorf("starting view MouseMode = %v, want cell motion", got)
	}
	sized := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	if got := sized.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Errorf("dashboard view MouseMode = %v, want cell motion", got)
	}
}

func TestClickOnASidebarRowSelectsIt(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	m.focus = focusMain

	// The fixture's second selectable sandbox: rows[3] is issue-42.
	want := 3
	if m.rows[want].Name != "issue-42" {
		t.Fatalf("fixture moved: rows[3] = %q", m.rows[want].Name)
	}
	y := listLineOf(t, m, want)

	got, cmd := clickCmd(t, m, 4, y)
	if got.selected != want {
		t.Errorf("selected = %d (%q), want %d (issue-42)", got.selected, got.rows[got.selected].Name, want)
	}
	if got.focus != focusSidebar {
		t.Error("a click in the sidebar did not point the keyboard at it")
	}
	if cmd == nil {
		t.Error("selecting a row must re-read its events, as j/k do")
	}
}

func TestClickOnAPortLineSelectsItsSandbox(t *testing.T) {
	snap := testSnapshot()
	m := newTestModel(&fakeData{snap: snap}, &recordingActor{})
	// A port line under mercury (rows[1]) belongs to mercury.
	key := sandboxKey{Project: "alpha", Name: "mercury"}
	mm, _ := m.Update(slowMsg{snap: snap, ports: map[sandboxKey][]control.Port{
		key: {{Port: 5173, Label: "web", URL: "http://mercury.alpha.cspace.test:5173/"}},
	}, portsErr: map[sandboxKey]error{}})
	m = mm.(Model)
	// Move off mercury through Update, not by assignment: the geometry is
	// rebuilt after every message, and a selection set behind its back
	// would be hit-tested against a layout that never existed.
	m = step(t, m, "down") // issue-42

	// The line after mercury's own is its port line, and both map to row 1.
	mercury := listLineOf(t, m, 1)
	got := click(t, m, 6, mercury+1)
	if got.selected != 1 {
		t.Errorf("selected = %d, want mercury (1): a port line belongs to its sandbox", got.selected)
	}
}

func TestClickOnANonSelectableRowOnlyMovesFocus(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	m.focus = focusMain
	before := m.selected

	header := listLineOf(t, m, 0) // the project header
	got := click(t, m, 4, header)
	if got.selected != before {
		t.Errorf("selected moved to %d; a project header cannot be selected", got.selected)
	}
	if got.focus != focusSidebar {
		t.Error("the click should still point the keyboard at the sidebar")
	}
}

func TestClickOnATabFocusesIt(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = stepPump(t, m, "enter")
	m.focus = focusSidebar
	m = stepPump(t, m, "s")
	mustTabs(t, m, 2)
	m.focused = 1

	first := m.geom.tabs[0]
	got := click(t, m, first.from+1, m.geom.tabsY)
	if got.focused != 0 {
		t.Errorf("focused = %d, want the clicked tab (0)", got.focused)
	}
	if got.focus != focusMain {
		t.Error("clicking a tab did not point the keyboard at the main area")
	}

	// The span is half-open, so both of its own edges belong to it and the
	// column after it belongs to the next tab. An interior click cannot
	// tell those apart, and lighting the tab next door is exactly what two
	// copies of the elision arithmetic would produce.
	if edge := click(t, m, first.from, m.geom.tabsY); edge.focused != 0 {
		t.Errorf("focused = %d after a click on the span's first column, want 0", edge.focused)
	}
	if edge := click(t, m, first.to-1, m.geom.tabsY); edge.focused != 0 {
		t.Errorf("focused = %d after a click on the span's last column, want 0", edge.focused)
	}
	if len(got.geom.tabs) != 2 {
		t.Fatalf("tab spans = %+v, want one per tab", got.geom.tabs)
	}
	second := got.geom.tabs[1]
	if got.geom.tabs[0].to != second.from {
		t.Fatalf("the spans are not adjacent: %+v", got.geom.tabs)
	}
	if edge := click(t, got, second.from, got.geom.tabsY); edge.focused != 1 {
		t.Errorf("focused = %d after a click on the second span's first column, want 1", edge.focused)
	}
}

func TestClickOnAnElisionMarkerDoesNothing(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = stepPump(t, m, "enter")
	m.focus = focusSidebar
	m = stepPump(t, m, "s")
	mustTabs(t, m, 2)
	// Narrow enough that at least one tab is elided.
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 48, Height: 24})
	m = mm.(Model)
	if len(m.geom.tabs) >= 2 {
		t.Skip("both tabs still fit; nothing was elided")
	}
	if !strings.Contains(plain(m.tabsRow(mainWidthFor(m.width))), "+") {
		t.Fatal("expected an elision marker")
	}
	before := m.focused
	// Column 0 of the row is the marker.
	got := click(t, m, sidebarWidth, m.geom.tabsY)
	if got.focused != before {
		t.Errorf("focused moved to %d; a marker is not a tab", got.focused)
	}
}

func TestClickInThePaneAreaFocusesMain(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = stepPump(t, m, "enter")
	m.focus = focusSidebar

	got := click(t, m, m.geom.main.x+5, m.geom.main.y+3)
	if got.focus != focusMain {
		t.Error("a click in the pane area did not focus it")
	}
}

func TestClickInTheMainAreaWithNoTabsDoesNothing(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	got := click(t, m, m.geom.main.x+5, m.geom.main.y+3)
	if got.focus != focusSidebar {
		t.Error("focus moved to a main area with nothing in it")
	}
}

func TestClickDisarmsTheLeaderAndIsSwallowed(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = stepPump(t, m, "enter")
	m = step(t, m, "ctrl+space")
	if !m.leaderArmed {
		t.Fatal("the leader did not arm")
	}
	before := m.focused
	got := click(t, m, m.geom.main.x+2, m.geom.main.y+1)
	if got.leaderArmed {
		t.Error("a click left the leader armed")
	}
	if got.focused != before {
		t.Error("the click that disarmed the leader also acted")
	}
}

func TestClickDismissesTheHelpOverlayAndIsSwallowed(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	m = step(t, m, "?")
	if !m.showHelp {
		t.Fatal("? did not open the overlay")
	}
	before := m.selected
	other := listLineOf(t, m, 3)
	got := click(t, m, 4, other)
	if got.showHelp {
		t.Error("the click did not dismiss the overlay")
	}
	if got.selected != before {
		t.Error("the click that dismissed the overlay also selected a row")
	}
}

func TestClickUnderAModalIsSwallowedWithoutAnsweringIt(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	m = step(t, m, "d") // the teardown confirmation
	if m.mode != modeConfirmDown {
		t.Fatalf("mode = %v, want the confirmation", m.mode)
	}
	got := click(t, m, m.geom.main.x+5, m.geom.main.y+2)
	if got.mode != modeConfirmDown {
		t.Errorf("mode = %v; a click is not an answer and must not cancel a prompt", got.mode)
	}
}

func TestANonLeftClickDoesNothing(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	m.focus = focusMain
	before := m.selected
	y := listLineOf(t, m, 3)
	mm, _ := m.Update(tea.MouseClickMsg{X: 4, Y: y, Button: tea.MouseRight})
	got := mm.(Model)
	if got.selected != before || got.focus != focusMain {
		t.Error("a right click acted like a left one")
	}
}

func TestMotionAndReleaseNeverReachTheWidgets(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	m = step(t, m, "m") // the send box
	if m.mode != modeInput {
		t.Fatalf("mode = %v, want the send box", m.mode)
	}
	for _, msg := range []tea.Msg{
		tea.MouseMotionMsg{X: 30, Y: 5, Button: tea.MouseLeft},
		tea.MouseReleaseMsg{X: 30, Y: 5, Button: tea.MouseLeft},
	} {
		mm, cmd := m.Update(msg)
		if cmd != nil {
			t.Errorf("%T produced a command; it must be dropped", msg)
		}
		if mm.(Model).input.Value() != "" {
			t.Errorf("%T reached the send box", msg)
		}
	}
}
```

The file's imports are `strings`, `testing`, `tea "charm.land/bubbletea/v2"` and `"github.com/elliottregan/cspace/internal/control"` — the last because `TestClickOnAPortLineSelectsItsSandbox` builds `control.Port` values. Everything else it uses (`step`, `plain`, `fakeData`, `fakeHost`, `recordingActor`, `testSnapshot`, `newTestModel`, `newTestModelWithHost`, `stepPump`, `mustTabs`, `openOne`, `leader`, `sandboxKey`) is already in the package's tests; reuse it, do not copy it.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/controlplane/ -run 'Click|MouseMode|Motion' -v`
Expected: FAIL — `MouseMode` is zero (`tea.MouseModeNone`), and every click case leaves the model untouched because nothing handles the message.

- [ ] **Step 3: Turn the mouse on**

In `internal/controlplane/view.go`, in `View`, the early return becomes:

```go
	if m.width == 0 || m.height == 0 {
		v := tea.NewView("starting cspace tui…")
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion
		return v
	}
```

and the main return, directly after `v.AltScreen = true`, gains:

```go
	// Cell motion, not all motion: it reports clicks, releases, the wheel
	// and drags, which is everything the design asks for, and it is the
	// better supported of the two. In bubbletea v2 this is a property of
	// the view — there is no program option and no command — so it is set
	// on every frame, including the starting one above.
	//
	// The cost is the terminal's own selection: with mouse reporting on,
	// a drag belongs to the program. Ghostty and friends still select on
	// shift+drag, and the help overlay says so.
	v.MouseMode = tea.MouseModeCellMotion
```

- [ ] **Step 4: Route the click**

Create `internal/controlplane/mouse.go`:

```go
package controlplane

import (
	tea "charm.land/bubbletea/v2"
)

// The mouse, hit-tested against Model.geom.
//
// Nothing here reaches a child. The design forwards no mouse event to the
// pane (spec Non-goals), and the sandbox's own tmux is configured `mouse
// off` besides — so a pane's mouse is the dashboard's, and that is the
// whole of the rule: there is no code path from a mouse message to
// Pane.SendKey.

// handleClick routes a left-button press. Every branch either acts on the
// dashboard or does nothing; the release that follows is dropped in Update.
func (m Model) handleClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if msg.Button != tea.MouseLeft {
		// Middle and right have no meaning here, and inventing one for
		// them is how a stray thumb button tears a sandbox down.
		return m, nil
	}
	// An error notice stays until the next input, and a click is input —
	// the same rule handleKey applies to a keypress.
	if m.notice.isErr {
		m.notice = notice{}
	}

	if m.leaderArmed {
		// A half-typed chord. The click is not its second key, and leaving
		// it armed would send the *next* ordinary key somewhere the person
		// did not ask for. Disarm and swallow.
		m.leaderArmed = false
		return m, nil
	}
	if m.showHelp {
		// The overlay swallows the very next input whatever it is, which
		// is exactly what handleKey does with a key. Dismiss and swallow.
		m.showHelp = false
		return m, nil
	}
	if m.mode != modeNormal {
		// The send box, the teardown confirmation and the new-pane picker
		// are unanswered questions holding state. A click is not an answer
		// and must not throw one away, so it is swallowed WITHOUT
		// dismissing: esc still cancels, and the form keeps what was typed.
		return m, nil
	}

	g := m.geom
	x, y := msg.X, msg.Y
	switch {
	case g.list.contains(x, y):
		return m.selectListRow(y - g.list.y)

	case g.sidebar.contains(x, y):
		// The vertical rule, the detail band, the padding under a short
		// list: still the sidebar, so point the keyboard at it — but there
		// is no row under the pointer to select.
		m.focus = focusSidebar
		m.scrolling, m.scroll = false, 0
		return m, nil

	case y == g.tabsY:
		for _, s := range g.tabs {
			if x >= s.from && x < s.to {
				return m.focusTab(s.index), nil
			}
		}
		// An elision marker, the empty end of the row, or the daemon
		// health line that stands in for the tabs while none are open.
		// None of them is a tab, so none of them does anything.
		return m, nil

	case g.main.contains(x, y):
		if len(m.tabs) == 0 {
			// The same refusal the focusMain binding makes: there is
			// nothing to point the keyboard at, and a focus that renders
			// no cursor and takes no keys is a focus nobody can see.
			return m, nil
		}
		m.focus = focusMain
		return m, nil
	}
	// The footer, and anything a future layout leaves uncovered.
	return m, nil
}

// selectListRow moves the selection to whatever the clicked line of the row
// list belongs to.
//
// A line that belongs to no row (the "— system —" divider, the padding
// below the last one) and a row that cannot be selected (a project header,
// a sidecar) leave the selection where it was. The click still points the
// keyboard at the sidebar: that much the person did ask for by clicking in
// it.
//
// A sandbox's port lines carry their sandbox's index, so clicking a URL
// selects the sandbox it belongs to rather than nothing.
func (m Model) selectListRow(line int) (tea.Model, tea.Cmd) {
	m.focus = focusSidebar
	m.scrolling, m.scroll = false, 0
	if line < 0 || line >= len(m.geom.listRows) {
		return m, nil
	}
	idx := m.geom.listRows[line]
	if idx < 0 || idx >= len(m.rows) || !m.rows[idx].Selectable || idx == m.selected {
		return m, nil
	}
	m.selected = idx
	// The same re-read j/k do: the detail band's event tail belongs to the
	// selection, and without this it would keep showing the old row's.
	return m, m.eventsCmd()
}

// focusTab points the keyboard at one tab by index. It is what the leader's
// n/p do once they have worked out which tab they mean, and what a click on
// a tab does directly.
func (m Model) focusTab(i int) Model {
	if i < 0 || i >= len(m.tabs) {
		return m
	}
	m.focused = i
	m.focus = focusMain
	m.scrolling, m.scroll = false, 0
	return m
}
```

- [ ] **Step 5: Give the messages their arms**

In `internal/controlplane/model.go`, inside `update`'s type switch, directly after the `case tea.PasteMsg:` block's closing brace and before the switch's closing brace, add:

```go
	case tea.MouseClickMsg:
		return m.handleClick(msg)

	case tea.MouseReleaseMsg, tea.MouseMotionMsg:
		// Cell motion mode reports a release for every click, and motion
		// while a button is held — a drag. The design has no drag gesture
		// and forwards nothing to the child, so both are dropped HERE
		// rather than left to fall through to the widget switch at the
		// bottom of Update, which would hand them to a textinput or a huh
		// form.
		return m, nil
```

`tea.PasteMsg`'s arm has no `return` on its fall-through path, so the new arms must come after it as sibling cases, not inside it.

- [ ] **Step 6: Route `moveTab` through `focusTab`**

In `internal/controlplane/leader.go`, replace `moveTab`:

```go
// moveTab steps the focus through the tabs, wrapping. The step is the only
// thing it decides; focusTab does the rest, so the leader's n/p and a click
// on a tab cannot end up leaving the model in different states.
func (m Model) moveTab(dir int) Model {
	if len(m.tabs) == 0 {
		return m
	}
	return m.focusTab((m.focused + dir + len(m.tabs)) % len(m.tabs))
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/controlplane/ -run 'Click|MouseMode|Motion|Tab' -v`
Expected: PASS, the existing tab tests included.

- [ ] **Step 8: Check the whole package, then the repo**

Run: `make check`
Expected: green.

Run: `make test-race`
Expected: green. The click reads `t.p` only through `focusTab`'s bookkeeping, but this is the first task to touch tab state from a new entry point.

- [ ] **Step 9: Commit**

```bash
git add internal/controlplane/mouse.go internal/controlplane/mouse_test.go \
        internal/controlplane/view.go internal/controlplane/model.go \
        internal/controlplane/leader.go
git commit -m "Turn the mouse on and route clicks to rows, tabs and the pane area"
```

---

### Task 3: The wheel

The wheel over the sidebar moves the selection; over the main area it scrolls what can be scrolled and says so when nothing can. It never reaches a child, and it never moves the focus.

**Files:**
- Modify: `internal/controlplane/mouse.go`, `internal/controlplane/model.go`, `internal/controlplane/leader.go`
- Test: `internal/controlplane/mouse_test.go`

**Interfaces:**
- Consumes: `Model.geom`, `Model.focusedTab()`, `*pane.Pane.ScrollbackLen()`, `clampScroll`, `supervisor.vp` (`viewport.Model`), `Model.moveSelection`, `Model.eventsCmd()`.
- Produces:
  - `const wheelLines = 3`
  - `func noScrollbackNotice() notice` — shared by leader `[` and the wheel
  - `func (m Model) handleWheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd)`
  - `func (m Model) wheelMain(dir int) (tea.Model, tea.Cmd)`

- [ ] **Step 1: Write the failing test**

Append to `internal/controlplane/mouse_test.go`:

```go
// wheel delivers one notch. up is +1 in the model's own direction — toward
// older output, toward the row above.
func wheel(t *testing.T, m Model, x, y int, up bool) Model {
	t.Helper()
	button := tea.MouseWheelDown
	if up {
		button = tea.MouseWheelUp
	}
	mm, _ := m.Update(tea.MouseWheelMsg{X: x, Y: y, Button: button})
	return mm.(Model)
}

func TestWheelOverTheSidebarMovesTheSelection(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	m.selected = 1 // mercury
	m.focus = focusMain

	down := wheel(t, m, 4, 3, false)
	if down.selected != 3 {
		t.Errorf("selected = %d, want the next selectable row (3)", down.selected)
	}
	if down.focus != focusMain {
		t.Error("the wheel moved the focus; it is a look, not a commitment")
	}
	up := wheel(t, down, 4, 3, true)
	if up.selected != 1 {
		t.Errorf("selected = %d, want back at mercury (1)", up.selected)
	}
}

func TestWheelOverATmuxBackedPaneRefusesLikeLeaderBracket(t *testing.T) {
	h := &fakeHost{t: t}
	m := openOne(t, h) // `sleep 30`: nothing on the alternate screen, and no scrollback

	byKey := leader(t, m, "[")
	byWheel := wheel(t, m, m.geom.main.x+4, m.geom.main.y+4, true)

	if byWheel.scrolling {
		t.Error("the wheel armed scroll mode on a pane with no scrollback")
	}
	if byWheel.notice.text != byKey.notice.text {
		t.Errorf("wheel says %q, leader [ says %q — they must say the same thing",
			byWheel.notice.text, byKey.notice.text)
	}
	if !byWheel.notice.isErr {
		t.Error("the refusal should stay until the next keypress")
	}
	if !strings.Contains(byWheel.notice.text, "PgUp") {
		t.Errorf("the refusal must say where the history is: %q", byWheel.notice.text)
	}
}

// A pane whose child prints to the NORMAL screen, which the fake host's
// `history` child does and a real tmux-backed pane never does. openOne
// presses enter, so this is a Claude-kind tab — but the fake runs /bin/sh
// for every kind, so it stands in for a host shell. The distinction the
// refusal test above turns on is tmux and the alternate screen, not the
// tab's kind.
func TestWheelOnAPaneWithScrollbackEntersScrollModeAndScrolls(t *testing.T) {
	h := &fakeHost{t: t, history: true}
	m := openOne(t, h)
	waitForHistory(t, m.tabs[0], 2*wheelLines)

	up := wheel(t, m, m.geom.main.x+4, m.geom.main.y+4, true)
	if !up.scrolling {
		t.Fatal("the wheel did not enter scroll mode on a pane with scrollback")
	}
	if up.scroll != wheelLines {
		t.Errorf("scroll = %d, want one notch (%d)", up.scroll, wheelLines)
	}
	further := wheel(t, up, up.geom.main.x+4, up.geom.main.y+4, true)
	if further.scroll != 2*wheelLines {
		t.Errorf("scroll = %d, want two notches", further.scroll)
	}
	back := wheel(t, further, further.geom.main.x+4, further.geom.main.y+4, false)
	if back.scroll != wheelLines {
		t.Errorf("scroll = %d after a notch down, want one notch", back.scroll)
	}
}

func TestWheelDownOnALivePaneDoesNotArmScrollMode(t *testing.T) {
	h := &fakeHost{t: t, history: true}
	m := openOne(t, h)
	waitForHistory(t, m.tabs[0], 1)

	got := wheel(t, m, m.geom.main.x+4, m.geom.main.y+4, false)
	if got.scrolling {
		t.Error("wheeling down from the live screen armed scroll mode; there is nothing below it")
	}
}

func TestWheelOverThePaneWithTheSidebarFocusedDoesNotArmScrollMode(t *testing.T) {
	h := &fakeHost{t: t, history: true}
	m := openOne(t, h)
	waitForHistory(t, m.tabs[0], 2*wheelLines)
	m.focus = focusSidebar

	got := wheel(t, m, m.geom.main.x+4, m.geom.main.y+4, true)
	if got.scrolling {
		t.Error("the wheel armed scroll mode while the keyboard was on the sidebar: " +
			"only handlePaneKey leaves that mode, and the sidebar's keys never reach it, " +
			"so the pane would sit under a banner promising that any key returns to live")
	}
	if got.focus != focusSidebar {
		t.Error("the wheel moved the focus")
	}
}

func TestWheelOverASupervisorTabScrollsItsViewport(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = stepPump(t, m, "a")
	mustTabs(t, m, 1)
	sup := m.tabs[0].sup
	if sup == nil {
		t.Fatal("the supervisor tab has no view")
	}
	sup.vp.SetContent(strings.Repeat("line\n", 200))
	sup.vp.GotoBottom()
	before := sup.vp.YOffset()
	if before == 0 {
		t.Fatal("the viewport did not scroll to the bottom")
	}

	wheel(t, m, m.geom.main.x+4, m.geom.main.y+4, true)
	if sup.vp.YOffset() >= before {
		t.Errorf("YOffset = %d, want less than %d", sup.vp.YOffset(), before)
	}
}

func TestWheelNeverReachesTheChild(t *testing.T) {
	h := &fakeHost{t: t, echo: true}
	m := openOne(t, h)
	for i := 0; i < 5; i++ {
		m = wheel(t, m, m.geom.main.x+4, m.geom.main.y+4, true)
		m = wheel(t, m, m.geom.main.x+4, m.geom.main.y+4, false)
	}
	// A key the child WILL echo, sent after all ten notches, is the
	// barrier: once "z" is on the screen the pty has delivered everything
	// queued before it, so an absent escape sequence is absent rather than
	// merely late. A sleep would only prove the test was patient.
	m = step(t, m, "z")
	waitForPaneScreen(t, m.tabs[0], "z")
	// `cat -v` prints ESC as ^[ , so a forwarded SGR report would read
	// "^[[<64;...".
	if screen := plain(m.tabs[0].p.Render()); strings.Contains(screen, "^[[<") {
		t.Errorf("a mouse sequence reached the child: %q", screen)
	}
}

func TestWheelIsInertUnderAModalAndTheHelpOverlay(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	before := m.selected

	help := step(t, m, "?")
	if got := wheel(t, help, 4, 3, false); got.selected != before {
		t.Error("the wheel moved the selection behind the help overlay")
	}
	box := step(t, m, "m")
	if got := wheel(t, box, 4, 3, false); got.selected != before {
		t.Error("the wheel moved the selection behind the send box")
	}
}
```

`waitForHistory(t *testing.T, tb *tab, n int)` and `waitForPaneScreen(t *testing.T, tb *tab, want string)` already exist in `leader_test.go` — same package, so use them; do not add copies.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/controlplane/ -run Wheel -v`
Expected: FAIL to compile — `undefined: wheelLines` — and once that is stubbed, every case fails because the message is dropped.

- [ ] **Step 3: Share the refusal**

In `internal/controlplane/leader.go`, add above `handleLeaderKey`:

```go
// noScrollbackNotice is the refusal a pane with no history gives, shared by
// leader [ and the wheel so the two can never drift into saying different
// things about the same pane.
//
// This is the permanent state of every Claude and shell pane, not a rare
// one. tmux switches the terminal to the ALTERNATE screen the moment it
// starts (measured against the image's tmux 3.3a: its first bytes are
// ESC[?1049h), and nothing written to the alternate screen ever enters
// scrollback. The history is real, but it is on the child's side — tmux's
// copy-mode holds it, and Claude Code scrolls its own transcript with
// PgUp/PgDn, which reach the child precisely because this mode is off. See
// the scroll-mode-never-reaches-a-tmux-backed-panes-history finding.
func noScrollbackNotice() notice {
	return notice{
		text:  "nothing to scroll: this pane has no scrollback — its child keeps its own history (PgUp/PgDn go to it)",
		isErr: true,
	}
}
```

and replace the body of the `Scroll` arm's refusal — the lines

```go
		if t.p.ScrollbackLen() == 0 {
			// Refused rather than armed: there is nothing to walk back
			// through, and the mode is not inert — it swallows the next
			// keypress as the one that returns to live, so arming it here
			// costs a keystroke and shows a counter that can only ever say
			// "0 lines back".
			//
			// This is the permanent state of every Claude and shell pane,
			// not a rare one. tmux switches the terminal to the ALTERNATE
			// screen the moment it starts (measured against the image's
			// tmux 3.3a: its first bytes are ESC[?1049h), and nothing
			// written to the alternate screen ever enters scrollback. The
			// history is real, but it is on the child's side — tmux's
			// copy-mode holds it, and Claude Code scrolls its own
			// transcript with PgUp/PgDn, which reach the child precisely
			// because this mode is off.
			m.notice = notice{
				text:  "nothing to scroll: this pane has no scrollback — its child keeps its own history (PgUp/PgDn go to it)",
				isErr: true,
			}
			return m, nil
		}
```

with

```go
		if t.p.ScrollbackLen() == 0 {
			// Refused rather than armed: there is nothing to walk back
			// through, and the mode is not inert — it swallows the next
			// keypress as the one that returns to live, so arming it here
			// costs a keystroke and shows a counter that can only ever say
			// "0 lines back". The wheel gives the same refusal.
			m.notice = noScrollbackNotice()
			return m, nil
		}
```

- [ ] **Step 4: Route the wheel**

Append to `internal/controlplane/mouse.go`:

```go
// wheelLines is how far one notch moves a scrollback or a viewport. Three
// is what a terminal's own wheel does; one line per notch makes reading a
// long pane feel broken, and a page per notch overshoots.
const wheelLines = 3

// handleWheel routes one notch. It never moves the focus: a wheel is a
// look, not a commitment, and a person reading the sidebar while typing
// into a pane should stay typing into the pane.
func (m Model) handleWheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	var dir int
	switch msg.Button {
	case tea.MouseWheelUp:
		dir = 1 // toward older output, toward the row above
	case tea.MouseWheelDown:
		dir = -1
	default:
		// MouseWheelLeft / MouseWheelRight: nothing here scrolls sideways.
		return m, nil
	}
	if m.mode != modeNormal || m.showHelp {
		// A modal or the overlay owns the screen; there is nothing behind
		// it the person can see to scroll.
		return m, nil
	}

	g := m.geom
	switch {
	case g.sidebar.contains(msg.X, msg.Y):
		// The list is windowed on the selection (sidebarWindow), so moving
		// the selection IS scrolling the sidebar — and it is the move the
		// person can act on afterwards, which a detached scroll offset
		// would not be.
		m.moveSelection(-dir)
		return m, m.eventsCmd()

	case g.main.contains(msg.X, msg.Y):
		return m.wheelMain(dir)
	}
	return m, nil
}

// wheelMain is the wheel over the main area: the focused pane's scrollback,
// or the supervisor view's viewport.
//
// Nothing is forwarded to the child. A pane whose history lives inside the
// child — every tmux-backed one, which is every sandbox pane — gets the
// same refusal leader [ gives, rather than silence that reads as a dropped
// event.
func (m Model) wheelMain(dir int) (tea.Model, tea.Cmd) {
	t := m.focusedTab()
	if t == nil {
		return m, nil
	}
	if t.sup != nil {
		// Not a pane and not scrollback: the supervisor view is a viewport
		// over the event tail, and pgup/pgdn already move it. The wheel is
		// the same gesture with a smaller step.
		if dir > 0 {
			t.sup.vp.ScrollUp(wheelLines)
		} else {
			t.sup.vp.ScrollDown(wheelLines)
		}
		return m, nil
	}
	if t.p == nil {
		return m, nil
	}
	if t.p.ScrollbackLen() == 0 {
		// Checked before the direction, so a wheel either way over a
		// tmux-backed pane says the same thing. Silence in one direction
		// and an explanation in the other would read as a bug in the
		// explanation.
		m.notice = noScrollbackNotice()
		return m, nil
	}
	if m.scrolling {
		m.scroll = clampScroll(m.scroll+dir*wheelLines, t.p.ScrollbackLen())
		return m, nil
	}
	if dir < 0 {
		// Live already: there is nowhere below the live screen to go, and
		// arming scroll mode to sit at offset 0 would swallow the next key.
		return m, nil
	}
	if m.focus != focusMain {
		// Scroll mode is escapable only through handlePaneKey, which the
		// keyboard reaches only while the main area has focus. Arming it
		// from here would leave the pane under a banner that says any key
		// returns to live while every key went to the sidebar instead,
		// with leader g the only way out. A wheel is a look: it does not
		// take the focus, so it does not arm a mode that needs it either.
		return m, nil
	}
	// Exactly what leader [ does, plus the notch that asked for it.
	m.scrolling = true
	m.scroll = clampScroll(wheelLines, t.p.ScrollbackLen())
	return m, nil
}
```

- [ ] **Step 5: Give the wheel its arm**

In `internal/controlplane/model.go`, beside the click's arm:

```go
	case tea.MouseWheelMsg:
		return m.handleWheel(msg)
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/controlplane/ -run 'Wheel|Scroll' -v`
Expected: PASS, the existing scroll-mode tests included.

- [ ] **Step 7: Check**

Run: `make check`
Expected: green.

Run: `make test-race`
Expected: green. `wheelMain` reads `ScrollbackLen` on a live pane from the UI goroutine, which is a new caller of the engine.

- [ ] **Step 8: Commit**

```bash
git add internal/controlplane/mouse.go internal/controlplane/mouse_test.go \
        internal/controlplane/model.go internal/controlplane/leader.go
git commit -m "Scroll the sidebar and the focused pane with the wheel"
```

---

### Task 4: The clipboard seam, and osascript behind it

The seam and its one real implementation, wired but not yet reachable from a key. Splitting it from Task 5 is deliberate: this half is testable against the real clipboard on a Mac and has nothing to do with the dashboard's state machine.

**Files:**
- Create: `internal/controlplane/clipboard.go`, `internal/cli/clipboard.go`
- Modify: `internal/controlplane/model.go`, `internal/controlplane/model_test.go`, `internal/controlplane/detach_test.go`, `internal/cli/cmd_tui.go`, `internal/controlplane/geometry_test.go`, `internal/controlplane/mouse_test.go`
- Test: `internal/cli/clipboard_test.go`

**Interfaces:**
- Consumes: `control.SessionDir(home, project, sandbox) string` (`internal/control/paths.go`).
- Produces:
  - `var controlplane.ErrNoImage error`
  - `type controlplane.Clipboard interface { Image(ctx context.Context, project, sandbox string) (string, error); Text(ctx context.Context) (string, error) }`
  - `type controlplane.nopClipboard struct{}`
  - `func controlplane.New(data Data, actor Actor, host PaneHost, clip Clipboard, keys KeyMap) Model` — the clipboard is the new fourth parameter
  - `func cli.newClipboard(home string) *osaClipboard`
  - `const cli.pasteStamp = "20060102-150405.000"`

- [ ] **Step 1: Write the failing test**

Create `internal/cli/clipboard_test.go`:

```go
package cli

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elliottregan/cspace/internal/controlplane"
)

// These run against the real pasteboard, because a stubbed osascript would
// only prove that the stub works. They skip rather than fail where
// osascript is missing: everything else in this package builds and tests on
// any host, and cspace itself is macOS-only for other reasons.
func requireOsascript(t *testing.T) {
	t.Helper()
	if os.Getenv("CSPACE_CLIPBOARD_TESTS") == "" {
		// Opt-in, because these overwrite the REAL pasteboard and
		// keepClipboard can only put text back. `make check` runs
		// `go test ./...`, and `scripts/release.sh` runs `make check` —
		// so without this gate, cutting a release destroys whatever
		// image the developer had on their clipboard.
		t.Skip("set CSPACE_CLIPBOARD_TESTS=1: these tests overwrite the real pasteboard and cannot restore an image")
	}
	for _, bin := range []string{"osascript", "pbcopy", "pbpaste"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s is not on PATH; the clipboard tests are macOS-only", bin)
		}
	}
}

// keepClipboard saves the clipboard's TEXT and puts it back afterwards.
//
// It cannot save an image. pbcopy writes text and nothing else, and there
// is no supported way to restore an arbitrary pasteboard flavour from a
// shell — so if the clipboard held a picture when this test started, that
// picture is gone once it finishes. That is a known and accepted cost of
// testing against the real pasteboard; there is no second pasteboard to
// use instead.
func keepClipboard(t *testing.T) {
	t.Helper()
	saved, err := exec.Command("pbpaste").Output()
	if err != nil {
		t.Fatalf("pbpaste: %v", err)
	}
	t.Cleanup(func() {
		c := exec.Command("pbcopy")
		c.Stdin = bytes.NewReader(saved)
		_ = c.Run()
	})
}

// putPNG writes a tiny PNG and puts it on the clipboard as «class PNGf».
func putPNG(t *testing.T, dir string) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	path := filepath.Join(dir, "src.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	out, err := exec.Command("osascript",
		"-e", "on run argv",
		"-e", "set the clipboard to (read (POSIX file (item 1 of argv)) as «class PNGf»)",
		"-e", "end run",
		path).CombinedOutput()
	if err != nil {
		t.Fatalf("put the png on the clipboard: %v: %s", err, out)
	}
	return path
}

func TestClipboardWritesAPNGWhereTheSandboxPaneCanReadIt(t *testing.T) {
	requireOsascript(t)
	keepClipboard(t)
	putPNG(t, t.TempDir())

	home := t.TempDir()
	c := newClipboard(home)
	c.now = func() time.Time { return time.Date(2026, 9, 19, 14, 30, 1, 123_000_000, time.UTC) }

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	typed, err := c.Image(ctx, "alpha", "mercury")
	if err != nil {
		t.Fatalf("Image: %v", err)
	}
	// The path typed into the pane is the one the pane can open: cmd_up.go
	// bind-mounts ~/.cspace/sessions/<project>/<sandbox> at /sessions.
	if want := "/sessions/paste/20260919-143001.123.png"; typed != want {
		t.Errorf("typed path = %q, want %q", typed, want)
	}

	hostPath := filepath.Join(home, ".cspace", "sessions", "alpha", "mercury",
		"paste", "20260919-143001.123.png")
	// Byte-identity with the source is what the design measured, but it is
	// the pasteboard's promise and not this code's — so the assertion is
	// that a real PNG of the right size came out the other end.
	f, err := os.Open(hostPath)
	if err != nil {
		t.Fatalf("open the written png: %v", err)
	}
	defer func() { _ = f.Close() }()
	cfg, err := png.DecodeConfig(f)
	if err != nil {
		t.Fatalf("the written file is not a png: %v", err)
	}
	if cfg.Width != 2 || cfg.Height != 2 {
		t.Errorf("png is %dx%d, want 2x2", cfg.Width, cfg.Height)
	}

	info, err := os.Stat(filepath.Dir(hostPath))
	if err != nil {
		t.Fatalf("stat the paste dir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != fs.FileMode(0o700) {
		t.Errorf("paste dir mode = %v, want 0700", perm)
	}
}

func TestClipboardWritesAHostShellsImageOutsideAnySandbox(t *testing.T) {
	requireOsascript(t)
	keepClipboard(t)
	putPNG(t, t.TempDir())

	home := t.TempDir()
	c := newClipboard(home)
	c.now = func() time.Time { return time.Date(2026, 9, 19, 14, 30, 1, 0, time.UTC) }

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	typed, err := c.Image(ctx, "", "")
	if err != nil {
		t.Fatalf("Image: %v", err)
	}
	want := filepath.Join(home, ".cspace", "paste", "20260919-143001.000.png")
	if typed != want {
		t.Errorf("typed path = %q, want the host path %q", typed, want)
	}
	if _, err := os.Stat(typed); err != nil {
		t.Errorf("the host path does not exist: %v", err)
	}
}

func TestClipboardReportsErrNoImageForATextClipboard(t *testing.T) {
	requireOsascript(t)
	keepClipboard(t)
	c := exec.Command("pbcopy")
	c.Stdin = strings.NewReader("line1\nline2")
	if err := c.Run(); err != nil {
		t.Fatalf("pbcopy: %v", err)
	}

	clip := newClipboard(t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if _, err := clip.Image(ctx, "alpha", "mercury"); !errors.Is(err, controlplane.ErrNoImage) {
		t.Errorf("Image err = %v, want ErrNoImage", err)
	}
	text, err := clip.Text(ctx)
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	// Byte for byte: a pasted diff that gained a trailing newline is a
	// pasted diff that was changed on the way through.
	if text != "line1\nline2" {
		t.Errorf("Text = %q, want %q", text, "line1\nline2")
	}
}

func TestClipboardImageLeavesNoFileWhenThereIsNoImage(t *testing.T) {
	requireOsascript(t)
	keepClipboard(t)
	c := exec.Command("pbcopy")
	c.Stdin = strings.NewReader("no picture here")
	if err := c.Run(); err != nil {
		t.Fatalf("pbcopy: %v", err)
	}

	home := t.TempDir()
	clip := newClipboard(home)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, _ = clip.Image(ctx, "alpha", "mercury")
	dir := filepath.Join(home, ".cspace", "sessions", "alpha", "mercury", "paste")
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		t.Errorf("a failed probe left %d file(s) behind in %s", len(entries), dir)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cli/ -run Clipboard -v`
Expected: FAIL to compile — `undefined: newClipboard`, `undefined: controlplane.ErrNoImage`.

- [ ] **Step 3: Declare the seam**

Create `internal/controlplane/clipboard.go`:

```go
package controlplane

import (
	"context"
	"errors"
)

// ErrNoImage is what a Clipboard reports when the clipboard holds no image.
//
// It is not a failure. It is the branch the design names: "an empty or
// text-only clipboard falls back to a text paste" — so the dispatcher tests
// for it with errors.Is and goes on to Text, rather than putting it in the
// footer.
var ErrNoImage = errors.New("clipboard holds no image")

// Clipboard is the host's clipboard.
//
// Declared here and implemented in internal/cli, exactly like Data, Actor
// and PaneHost: this package stays free of the host it runs on, and the
// tests get a stub instead of a pasteboard.
//
// Neither method may be called from Update. Both shell out to a host
// binary, and a blocked exec on the UI goroutine is a frozen window. Every
// caller goes through a tea.Cmd with a bounded context.
type Clipboard interface {
	// Image writes the clipboard's image as a PNG somewhere the named
	// sandbox's pane can read it, and returns the path to TYPE INTO that
	// pane: the in-sandbox /sessions path for a sandbox pane, and a host
	// path when project and sandbox are both empty, which is how a host
	// shell asks (it has no sandbox and no bind mount).
	//
	// It returns ErrNoImage, possibly wrapped, when the clipboard holds no
	// image. Every other error is a real failure and reaches the footer.
	Image(ctx context.Context, project, sandbox string) (string, error)

	// Text is the clipboard's text, byte for byte, and empty when it holds
	// none.
	Text(ctx context.Context) (string, error)
}

// nopClipboard is what a Model built without one gets: the same fail-closed
// rule nopPaneHost applies, so a missing seam is an explained footer error
// rather than a nil dereference.
type nopClipboard struct{}

func (nopClipboard) Image(context.Context, string, string) (string, error) {
	return "", errors.New("no clipboard configured")
}

func (nopClipboard) Text(context.Context) (string, error) { return "", nil }
```

- [ ] **Step 4: Hold it on the model**

In `internal/controlplane/model.go`, add the field beside `host`:

```go
type Model struct {
	data  Data
	actor Actor
	host  PaneHost
	clip  Clipboard
```

and extend `New`:

```go
// New builds the dashboard over the query, action, pane and clipboard seams
// and the resolved keymap. Nothing is polled and nothing is opened until
// Init runs.
func New(data Data, actor Actor, host PaneHost, clip Clipboard, keys KeyMap) Model {
	ti := textinput.New()
	ti.Placeholder = "message"
	ti.CharLimit = 2000
	if host == nil {
		host = nopPaneHost{}
	}
	if clip == nil {
		clip = nopClipboard{}
	}
	return Model{
		data:     data,
		actor:    actor,
		host:     host,
		clip:     clip,
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

Update every existing `New(...)` call site to pass `nopClipboard{}` as the fourth argument. Find them with `grep -rn 'New(.*NewKeyMap' internal/controlplane` (seven rows) plus `grep -n 'controlplane.New(' internal/cli/cmd_tui.go`, whose call is split across four lines and which no single-line grep finds. Counting the two files Tasks 1 and 2 added, the nine sites are:

- `internal/controlplane/model_test.go:148` (`newTestModel`) — `New(d, a, nopPaneHost{}, nopClipboard{}, NewKeyMap(nil))`
- `internal/controlplane/model_test.go:159` (`newTestModelWithHost`) — `New(d, a, h, nopClipboard{}, NewKeyMap(nil))`
- `internal/controlplane/model_test.go:168`, `:306`, `:782` — `nopPaneHost{}, nopClipboard{}`
- `internal/controlplane/detach_test.go:11`, `:31` — `New(&fakeData{snap: testSnapshot()}, &recordingActor{}, h, nopClipboard{}, NewKeyMap(nil))`
- `internal/controlplane/geometry_test.go` (`TestGeometryIsEmptyBeforeTheFirstWindowSize`, added in Task 1) — `nopPaneHost{}, nopClipboard{}`
- `internal/controlplane/mouse_test.go` (`TestViewEnablesCellMotionMouseMode`, added in Task 2) — `nopPaneHost{}, nopClipboard{}`

- [ ] **Step 5: Implement it with osascript**

Create `internal/cli/clipboard.go`:

```go
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/controlplane"
)

// osaClipboard is controlplane.Clipboard over the macOS pasteboard.
//
// It lives here for the reason every other seam does: internal/controlplane
// must not know what host it is running on, and internal/control is pure Go
// with no business shelling out to AppleScript. Nothing is compiled in —
// osascript and pbpaste are looked up at call time, and a host without them
// gets a footer error rather than a build that will not link.
type osaClipboard struct {
	home string
	// now names the file. A seam because a test cannot assert on a path it
	// cannot predict.
	now func() time.Time
}

var _ controlplane.Clipboard = (*osaClipboard)(nil)

func newClipboard(home string) *osaClipboard {
	return &osaClipboard{home: home, now: time.Now}
}

// pasteStamp is the filename's timestamp, to the millisecond: two images
// pasted in the same second are two files, and the name still sorts.
const pasteStamp = "20060102-150405.000"

// Image probes the clipboard, writes its PNG, and reports the path to type.
//
// The probe is a separate osascript run rather than an attempt-and-catch,
// because "the clipboard holds no image" and "AppleScript failed" arrive
// through the same non-zero exit and must not be confused: one is the
// design's text-paste fallback and the other belongs in the footer.
func (c *osaClipboard) Image(ctx context.Context, project, sandbox string) (string, error) {
	has, err := c.hasImage(ctx)
	if err != nil {
		return "", err
	}
	if !has {
		return "", controlplane.ErrNoImage
	}

	hostDir, paneDir := c.pasteDirs(project, sandbox)
	// 0700: these are whatever was on the person's clipboard, under their
	// home directory, and nothing but this process and the sandbox's own
	// bind mount has business reading them.
	if err := os.MkdirAll(hostDir, 0o700); err != nil {
		return "", fmt.Errorf("create paste dir: %w", err)
	}
	name := c.now().Format(pasteStamp) + ".png"
	if err := c.writePNG(ctx, filepath.Join(hostDir, name)); err != nil {
		return "", err
	}
	return filepath.Join(paneDir, name), nil
}

// pasteDirs is where the file goes on the host, and the directory the pane
// will see it in.
//
// A sandbox pane reads it through the /sessions bind mount — cmd_up.go
// mounts ~/.cspace/sessions/<project>/<sandbox> there — so the path typed
// into it is /sessions/paste/<name> however the host spells the directory.
// A host shell has no mount and belongs to no sandbox: its images go to
// ~/.cspace/paste and the host path is what gets typed.
func (c *osaClipboard) pasteDirs(project, sandbox string) (hostDir, paneDir string) {
	if project == "" && sandbox == "" {
		d := filepath.Join(c.home, ".cspace", "paste")
		return d, d
	}
	return filepath.Join(control.SessionDir(c.home, project, sandbox), "paste"), "/sessions/paste"
}

// hasImage is the probe. `clipboard info` lists one entry per flavour the
// pasteboard can supply, and macOS offers «class PNGf» for anything it can
// hand over as an image.
//
// Measured on this host 2026-09-19:
//
//	an image: «class PNGf», 73, «class AVIF», 375, «class 8BPS», 3342, …
//	text:     «class utf8», 9, «class ut16», 20, string, 9, Unicode text, 18
//	empty:    «class utf8», 0, «class ut16», 2, string, 0, Unicode text, 0
func (c *osaClipboard) hasImage(ctx context.Context) (bool, error) {
	out, err := osascript(ctx, []string{"clipboard info"})
	if err != nil {
		return false, err
	}
	return strings.Contains(out, "«class PNGf»"), nil
}

// writePNG asks AppleScript for the clipboard's PNG flavour and writes it
// to path. The design's decision row measured the round trip byte-identical,
// and so did a re-measurement on 2026-09-19.
//
// The path travels as an argv item read by `on run argv`, never
// interpolated into the script: a sandbox name is operator-supplied and
// AppleScript string escaping is not something to reinvent. `set eof f to 0`
// truncates first, so a retry onto an existing name cannot leave a tail of
// the previous image behind, and the write sits in a `try` whose handler
// closes the file and then RE-RAISES. A bare `try` is not enough: measured
// on 2026-09-19, `osascript -e try -e 'error "boom"' -e 'end try'` exits 0,
// so a failed write would come back as success and leader `v` would type
// the path of a zero-byte file into a pane.
func (c *osaClipboard) writePNG(ctx context.Context, path string) error {
	_, err := osascript(ctx, []string{
		"on run argv",
		"set p to item 1 of argv",
		"set d to (the clipboard as «class PNGf»)",
		"set f to open for access (POSIX file p) with write permission",
		"try",
		"set eof f to 0",
		"write d to f",
		"close access f",
		"on error e",
		"try",
		"close access f",
		"end try",
		"error e",
		"end try",
		"end run",
	}, path)
	if err != nil {
		return fmt.Errorf("write the clipboard's png: %w", err)
	}
	return nil
}

// Text is the clipboard's text, byte for byte.
//
// pbpaste, not osascript. `osascript -e 'the clipboard as «class utf8»'`
// prints the script's RESULT, which arrives with a newline appended —
// measured 2026-09-19, "line1\nline2" came back as "line1\nline2\n" — and a
// pasted diff that gained a trailing newline is a pasted diff that was
// changed on the way through. pbpaste writes the pasteboard's bytes and
// nothing else.
func (c *osaClipboard) Text(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "pbpaste").Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", errors.New("pbpaste not found: reading the clipboard needs macOS")
		}
		return "", fmt.Errorf("pbpaste: %w", err)
	}
	return string(out), nil
}

// osascript runs a script given one line per -e, with args after it, where
// an `on run argv` handler can read them. It returns stdout, and folds
// stderr into the error: osascript reports a failed clipboard coercion
// there ("Can't make some data into the expected type. (-1700)") and a
// naked exit status would tell the operator nothing.
func osascript(ctx context.Context, script []string, args ...string) (string, error) {
	argv := make([]string, 0, len(script)*2+len(args))
	for _, line := range script {
		argv = append(argv, "-e", line)
	}
	argv = append(argv, args...)

	cmd := exec.CommandContext(ctx, "osascript", argv...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", errors.New("osascript not found: image paste needs macOS")
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("osascript: %s", msg)
		}
		return "", fmt.Errorf("osascript: %w", err)
	}
	return string(out), nil
}
```

- [ ] **Step 6: Wire it into the program**

In `internal/cli/cmd_tui.go`, the model construction becomes:

```go
			model := controlplane.New(ctrl,
				newControlPlaneActor(ctrl, home),
				newPaneHost(ctrl, home),
				newClipboard(home),
				controlplane.NewKeyMap(userCfg.TUI.Keys))
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `CSPACE_CLIPBOARD_TESTS=1 go test ./internal/cli/ -run Clipboard -v`
Expected: PASS on a Mac with a real pasteboard; SKIP under a bare `make check`, which does not set the variable; SKIP too where `osascript`, `pbcopy` or `pbpaste` is missing.

Run: `go test ./internal/controlplane/`
Expected: PASS — the `New` call sites compile again.

- [ ] **Step 8: Confirm nothing was added to the module**

Run: `git diff --stat go.mod go.sum`
Expected: no output. The clipboard is two host binaries, not a dependency.

Run: `go list -deps ./internal/controlplane | grep 'cspace/internal/cli' && echo LEAKED || echo clean`
Expected: `clean`. (`grep -c` prints `0` but exits 1, which reads as a failed command.)

- [ ] **Step 9: Check**

Run: `make check`
Expected: green.

- [ ] **Step 10: Commit**

```bash
git add internal/controlplane/clipboard.go internal/controlplane/model.go \
        internal/controlplane/model_test.go internal/controlplane/detach_test.go \
        internal/controlplane/geometry_test.go internal/controlplane/mouse_test.go \
        internal/cli/clipboard.go internal/cli/clipboard_test.go internal/cli/cmd_tui.go
git commit -m "Add a Clipboard seam and read the macOS pasteboard behind it"
```

---

### Task 5: Leader `v` pastes the image

The binding declared in 4b finally acts. Every case the design and the rulings name gets a branch: a sandbox pane, a host shell, a supervisor tab, a pane that has exited, no tab at all, a text-only clipboard, an empty one, and a host with no `osascript`.

**Files:**
- Modify: `internal/controlplane/clipboard.go`, `internal/controlplane/model.go`, `internal/controlplane/leader.go`, `internal/controlplane/leader_test.go`
- Test: `internal/controlplane/clipboard_test.go`

**Interfaces:**
- Consumes: `Clipboard`, `ErrNoImage`, `Model.clip`, `Model.focusedTab()`, `Model.startAction`, `*pane.Pane.Paste(string)`, `*pane.Pane.Exited()`, `Model.tabByID`.
- Produces:
  - `const LabelPasteImage = "paste image"`
  - `const clipboardTimeout = 10 * time.Second`
  - `type pasteMsg struct{ id int; text string; err error }`
  - `func (m Model) pasteImageCmd(t *tab) tea.Cmd`

- [ ] **Step 1: Write the failing test**

Create `internal/controlplane/clipboard_test.go`:

```go
package controlplane

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// fakeClipboard records what it was asked for and answers with what the
// test set.
type fakeClipboard struct {
	path    string
	imgErr  error
	text    string
	textErr error

	calls []string
}

func (c *fakeClipboard) Image(_ context.Context, project, sandbox string) (string, error) {
	c.calls = append(c.calls, fmt.Sprintf("image:%s/%s", project, sandbox))
	if c.imgErr != nil {
		return "", c.imgErr
	}
	return c.path, nil
}

func (c *fakeClipboard) Text(context.Context) (string, error) {
	c.calls = append(c.calls, "text")
	return c.text, c.textErr
}

// newTestModelWithClipboard is newTestModelWithHost plus a clipboard.
func newTestModelWithClipboard(h PaneHost, c Clipboard) Model {
	d := &fakeData{snap: testSnapshot()}
	m := New(d, &recordingActor{}, h, c, NewKeyMap(nil))
	m.now = func() time.Time { return time.Unix(1_000_060, 0) }
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = mm.(Model)
	mm, _ = m.Update(snapshotMsg{snap: d.snap})
	return mm.(Model)
}

// pasteV arms the leader, presses v, runs the command it produced, and
// feeds the result back.
func pasteV(t *testing.T, m Model) Model {
	t.Helper()
	m = step(t, m, "ctrl+space")
	mm, cmd := m.Update(press("v"))
	return pump(t, mm.(Model), cmd)
}

func TestLeaderVPastesTheImagePathIntoAClaudePane(t *testing.T) {
	h := &fakeHost{t: t, echo: true}
	c := &fakeClipboard{path: "/sessions/paste/20260919-143001.123.png"}
	m := newTestModelWithClipboard(h, c)
	m = stepPump(t, m, "enter")
	mustTabs(t, m, 1)

	m = pasteV(t, m)

	if len(c.calls) != 1 || c.calls[0] != "image:alpha/mercury" {
		t.Fatalf("clipboard calls = %v, want one Image for the pane's own sandbox", c.calls)
	}
	if m.notice.isErr {
		t.Errorf("notice = %q, want no error", m.notice.text)
	}
	// The echo child prints back what it read, in caret notation.
	waitForPaneScreen(t, m.tabs[0], "20260919-143001.123.png")
	// No newline: the path is typed, not sent. `cat -v` would print a CR
	// as ^M.
	if screen := plain(m.tabs[0].p.Render()); strings.Contains(screen, "^M") {
		t.Errorf("the paste carried a carriage return: %q", screen)
	}
}

func TestLeaderVOnAHostShellAsksForTheHostPath(t *testing.T) {
	h := &fakeHost{t: t}
	c := &fakeClipboard{path: "/Users/x/.cspace/paste/20260919-143001.000.png"}
	m := newTestModelWithClipboard(h, c)
	// The picker is the only way to a host shell: it belongs to no row.
	// openHostShell (leader_test.go) already drives it.
	m = openHostShell(t, m)
	mustTabs(t, m, 1)

	pasteV(t, m)
	if len(c.calls) != 1 || c.calls[0] != "image:/" {
		t.Fatalf("clipboard calls = %v, want an Image with no sandbox", c.calls)
	}
}

func TestLeaderVOnASupervisorTabSaysThereIsNoPane(t *testing.T) {
	h := &fakeHost{t: t}
	c := &fakeClipboard{path: "/sessions/paste/x.png"}
	m := newTestModelWithClipboard(h, c)
	m = stepPump(t, m, "a")
	mustTabs(t, m, 1)

	m = pasteV(t, m)
	if len(c.calls) != 0 {
		t.Errorf("clipboard was read for a tab with no pane: %v", c.calls)
	}
	if !m.notice.isErr || !strings.Contains(m.notice.text, "no pane") {
		t.Errorf("notice = %q, want a 'no pane' error", m.notice.text)
	}
}

func TestLeaderVWithNoTabsSaysThereIsNoPane(t *testing.T) {
	c := &fakeClipboard{path: "/sessions/paste/x.png"}
	m := newTestModelWithClipboard(&fakeHost{t: t}, c)

	m = pasteV(t, m)
	if len(c.calls) != 0 {
		t.Errorf("clipboard was read with no tab open: %v", c.calls)
	}
	if !m.notice.isErr || !strings.Contains(m.notice.text, "no pane") {
		t.Errorf("notice = %q, want a 'no pane' error", m.notice.text)
	}
}

func TestATextOnlyClipboardFallsBackToATextPaste(t *testing.T) {
	h := &fakeHost{t: t, echo: true}
	c := &fakeClipboard{imgErr: ErrNoImage, text: "func main() {}"}
	m := newTestModelWithClipboard(h, c)
	m = stepPump(t, m, "enter")

	m = pasteV(t, m)
	if len(c.calls) != 2 || c.calls[1] != "text" {
		t.Fatalf("clipboard calls = %v, want the image probe then the text", c.calls)
	}
	waitForPaneScreen(t, m.tabs[0], "func main()")
}

func TestAWrappedErrNoImageStillFallsBack(t *testing.T) {
	h := &fakeHost{t: t, echo: true}
	c := &fakeClipboard{imgErr: fmt.Errorf("probe: %w", ErrNoImage), text: "wrapped"}
	m := newTestModelWithClipboard(h, c)
	m = stepPump(t, m, "enter")

	pasteV(t, m)
	if len(c.calls) != 2 {
		t.Fatalf("clipboard calls = %v; errors.Is must see through the wrap", c.calls)
	}
}

func TestAnEmptyClipboardSaysSoAndTypesNothing(t *testing.T) {
	h := &fakeHost{t: t, echo: true}
	c := &fakeClipboard{imgErr: ErrNoImage, text: ""}
	m := newTestModelWithClipboard(h, c)
	m = stepPump(t, m, "enter")

	m = pasteV(t, m)
	if !m.notice.isErr || !strings.Contains(m.notice.text, "empty") {
		t.Errorf("notice = %q, want it to say the clipboard is empty", m.notice.text)
	}
}

func TestAMissingOsascriptIsAFooterErrorNotACrash(t *testing.T) {
	h := &fakeHost{t: t}
	c := &fakeClipboard{imgErr: errors.New("osascript not found: image paste needs macOS")}
	m := newTestModelWithClipboard(h, c)
	m = stepPump(t, m, "enter")

	m = pasteV(t, m)
	if !m.notice.isErr || !strings.Contains(m.notice.text, "osascript") {
		t.Errorf("notice = %q, want the osascript failure in the footer", m.notice.text)
	}
	if m.action != "" {
		t.Errorf("action = %q, want the in-flight marker cleared", m.action)
	}
}

func TestAPasteForATabThatClosedIsDropped(t *testing.T) {
	c := &fakeClipboard{path: "/sessions/paste/x.png"}
	m := newTestModelWithClipboard(&fakeHost{t: t}, c)
	// No tab with this id has ever existed.
	mm, _ := m.Update(pasteMsg{id: 4242, text: "/sessions/paste/x.png"})
	if got := mm.(Model); got.notice.isErr {
		t.Errorf("notice = %q, want silence for a tab that is gone", got.notice.text)
	}
}
```

Every helper this file leans on already exists in the package and must be reused, not copied: `step`, `stepPump`, `pump`, `press`, `leader`, `mustTabs`, `plain`, `fakeHost`, `fakeData`, `recordingActor`, `testSnapshot`, `waitForPaneScreen(t, tb, want)` and `openHostShell(t, m)` (the last two in `leader_test.go`).

The file's imports are `context`, `errors`, `fmt`, `strings`, `testing`, `time` and `tea "charm.land/bubbletea/v2"`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/controlplane/ -run 'LeaderV|Clipboard|Paste' -v`
Expected: FAIL to compile — `undefined: pasteMsg`, `undefined: LabelPasteImage`.

- [ ] **Step 3: Add the command and the message**

Append to `internal/controlplane/clipboard.go` (and add `"time"`, `tea "charm.land/bubbletea/v2"` to its imports):

```go
// LabelPasteImage is what the footer calls an image paste while it is in
// flight and when it fails.
const LabelPasteImage = "paste image"

// clipboardTimeout bounds one image paste: the probe, the write, and the
// text read the fallback needs. osascript is fast, but it is a host binary
// that can be slow to start under load, and the UI must never wait on it.
const clipboardTimeout = 10 * time.Second

// pasteMsg carries one clipboard read's outcome back to the tab it was
// started for. text is what to type into the pane: the path of the written
// PNG, or the clipboard's own text when it held no image.
type pasteMsg struct {
	id   int
	text string
	err  error
}

// pasteImageCmd reads the clipboard for one tab, off the UI goroutine.
//
// The fallback is here rather than in a second round trip because it is one
// decision: "an empty or text-only clipboard falls back to a text paste"
// (the design). Two commands would also mean two deadlines and a window in
// which the tab could close between them.
//
// The tab's identity travels by value. The command outlives nothing, but it
// must not read the model it was built from — that Model is a copy, and by
// the time this runs the real one has moved on.
func (m Model) pasteImageCmd(t *tab) tea.Cmd {
	clip, id, project, sandbox := m.clip, t.id, t.project, t.sandbox
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), clipboardTimeout)
		defer cancel()

		path, err := clip.Image(ctx, project, sandbox)
		if err == nil {
			return pasteMsg{id: id, text: path}
		}
		if !errors.Is(err, ErrNoImage) {
			return pasteMsg{id: id, err: err}
		}
		text, err := clip.Text(ctx)
		if err != nil {
			return pasteMsg{id: id, err: err}
		}
		return pasteMsg{id: id, text: text}
	}
}
```

- [ ] **Step 4: Handle the result**

In `internal/controlplane/model.go`, add an arm beside `paneReapedMsg`:

```go
	case pasteMsg:
		m.action = ""
		if msg.err != nil {
			m.notice = notice{text: LabelPasteImage + " failed: " + msg.err.Error(), isErr: true}
			return m, nil
		}
		if msg.text == "" {
			m.notice = notice{text: LabelPasteImage + ": the clipboard is empty", isErr: true}
			return m, nil
		}
		t, _ := m.tabByID(msg.id)
		if t == nil || t.p == nil {
			// The tab closed while the clipboard was being read. There is
			// nowhere to put this and nobody to tell: the person closed it.
			return m, nil
		}
		if _, _, exited := t.p.Exited(); exited {
			m.notice = notice{text: LabelPasteImage + ": the pane exited", isErr: true}
			return m, nil
		}
		// Through Emulator.Paste, which brackets when the child asked —
		// the same path a terminal paste takes — and with no trailing
		// newline, so Claude gets the path in its input box and sends
		// nothing.
		t.p.Paste(msg.text)
		return m, nil
```

- [ ] **Step 5: Dispatch the binding**

In `internal/controlplane/leader.go`, replace the `PasteImage` arm:

```go
	case key.Matches(msg, m.keys.PasteImage):
		t := m.focusedTab()
		if t == nil || t.p == nil {
			// No tabs at all, or a supervisor tab, which runs no process.
			// Either way there is nowhere for a path to be typed.
			m.notice = notice{text: LabelPasteImage + ": no pane", isErr: true}
			return m, nil
		}
		if _, _, exited := t.p.Exited(); exited {
			m.notice = notice{text: LabelPasteImage + ": the pane exited", isErr: true}
			return m, nil
		}
		if m.action != "" {
			// The one-action gate, as the other leader keys apply it: two
			// concurrent osascript runs would race for one footer line.
			return m, nil
		}
		return m.startAction(LabelPasteImage, m.pasteImageCmd(t))
```

- [ ] **Step 6: Retire the placeholder in `TestLeaderDispatch`**

`internal/controlplane/leader_test.go` still asserts that `v` does nothing, and it keeps passing after Step 5 — `mode` is still `modeNormal` and `notice.text` is still `""`, because the only thing the dispatch moved is `m.action`, which the case never reads. Left alone it is a test whose comment and failure message now say the opposite of the shipped behaviour, and which can catch a regression in neither direction. Replace

```go
	// v is bound so the config shape is stable, and deliberately does
	// nothing until rollout step 5.
	if got := leader(t, m, "v"); got.mode != modeNormal || got.notice.text != "" {
		t.Error("leader v did something; image paste is step 5")
	}
```

with

```go
	// v pastes now: on a live pane it starts the clipboard read and marks
	// it in flight. What it reads is clipboard_test.go's business; this is
	// the dispatch. openOne's model carries nopClipboard, and the command
	// is never run here, so nothing touches a pasteboard.
	if got := leader(t, m, "v"); got.action != LabelPasteImage {
		t.Errorf("action = %q, want the image paste in flight", got.action)
	}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/controlplane/ -run 'LeaderV|LeaderDispatch|Clipboard|Paste|Empty|Wrapped|Osascript' -v`
Expected: PASS.

- [ ] **Step 8: Check**

Run: `make check`
Expected: green.

Run: `make test-race`
Expected: green. `t.p.Paste` from the `pasteMsg` arm is a new writer of a live pane from the UI goroutine.

- [ ] **Step 9: Commit**

```bash
git add internal/controlplane/clipboard.go internal/controlplane/clipboard_test.go \
        internal/controlplane/model.go internal/controlplane/leader.go \
        internal/controlplane/leader_test.go
git commit -m "Paste the clipboard's image into a pane on leader v"
```

---

### Task 6: Say what changed — help overlay, spec, CLAUDE.md, finding

The capability is not shipped until the window says it exists and the documents stop describing the old one. The spec's Rollout list gets step 5 struck through, which is what marks the control-plane design complete.

**Files:**
- Modify: `internal/controlplane/view.go`, `internal/controlplane/keys.go`, `docs/superpowers/specs/2026-09-17-control-plane-design.md`, `CLAUDE.md`, `.cspace/context/findings/2026-09-19-scroll-mode-never-reaches-a-tmux-backed-panes-history.md`
- Test: `internal/controlplane/input_test.go` (the existing `TestHelpOverlayToggles` file)

**Interfaces:**
- Consumes: `Model.helpView(width int) string`, `fit`, `styleDim`.
- Produces: nothing new in Go.

- [ ] **Step 1: Write the failing test**

Append to `internal/controlplane/input_test.go`:

```go
// The design's mouse non-goal says the overlay carries the selection note,
// because turning mouse reporting on takes the terminal's own drag-select
// away and there is no other place a person would learn what to do instead.
func TestHelpOverlayNamesTheMouseAndTheSelectionEscapeHatch(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	cols, _ := m.paneSize()
	help := plain(m.helpView(cols))
	for _, want := range []string{"click", "wheel", "shift"} {
		if !strings.Contains(strings.ToLower(help), want) {
			t.Errorf("the help overlay never mentions %q:\n%s", want, help)
		}
	}
	for i, l := range strings.Split(help, "\n") {
		if w := len([]rune(l)); w > cols {
			t.Errorf("help line %d is %d cells, wider than the %d the overlay is drawn in: %q", i, w, cols, l)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/controlplane/ -run HelpOverlayNames -v`
Expected: FAIL — "the help overlay never mentions "click"".

- [ ] **Step 3: Add the note**

In `internal/controlplane/view.go`, in `helpView`'s `lines` slice, add two entries directly after the `"every other key goes to the focused pane…"` line:

```go
		styleDim.Render(fit("mouse: click a row, a tab or the pane; the wheel scrolls both", width)),
		styleDim.Render(fit("hold shift for the terminal's own mouse: drag selects, click opens a link", width)),
```

- [ ] **Step 3b: Retire the "step 5 will make it act" comment**

In `internal/controlplane/keys.go`, in `LeaderHelp`'s doc comment, replace

```
// do, in the design's order. PasteImage is listed because it is bound and
// the config shape is stable; rollout step 5 is what makes it act.
```

with

```
// do, in the design's order.
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/controlplane/ -run 'HelpOverlay' -v`
Expected: PASS, the existing `TestHelpOverlayToggles` included.

- [ ] **Step 5: Amend the spec's Input section**

In `docs/superpowers/specs/2026-09-17-control-plane-design.md`, replace the Mouse paragraph

```
Mouse: cell-motion mode. Click selects a sidebar row or a tab or focuses the
main area; the wheel scrolls the focused pane's scrollback or the sidebar.
Nothing is forwarded to the child.
```

with

```
Mouse: cell-motion mode, enabled on every rendered view (bubbletea v2 makes
it a `tea.View` property, not a program option). Click selects a sidebar row
— a port line selects its sandbox, a project header and the system divider
select nothing — or focuses a tab, or focuses the main area; a click on an
elision marker, on the empty end of the tabs row, or in a main area with no
tabs open does nothing. A click disarms an armed leader, dismisses the help
overlay, and is swallowed under a modal without answering it. The wheel over
the sidebar moves the selection, three lines to a notch; over the main area
it scrolls the focused pane's scrollback, entering scroll mode exactly as
leader `[` does — which means it works on a host shell and refuses with the
same notice on every tmux-backed pane (the
`scroll-mode-never-reaches-a-tmux-backed-panes-history` finding) — and over
a supervisor tab it scrolls that view's viewport. The wheel never moves the
focus. Nothing is forwarded to the child: there is no path from a mouse
message to the pane engine, and the guest tmux is `mouse off` besides.
Mouse reporting also takes plain-click activation of the sidebar's OSC 8
port links, by the same mechanism that takes drag-selection — a plain click
on a port line now selects its sandbox. The terminal's own bypass modifier
(shift on Ghostty and friends) still opens them, and the help overlay says
so.
```

and the Paste paragraph's last sentence, adding the cases the implementation resolves:

```
Paste: text paste events go to the focused pane through `Emulator.Paste`,
which brackets them when the child asked. Image paste (leader `v`) runs
`osascript` to write the clipboard's PNG to
`~/.cspace/sessions/<project>/<sandbox>/paste/<timestamp>.png`, then types
`/sessions/paste/<timestamp>.png` into the pane with no trailing newline. An
empty or text-only clipboard falls back to a text paste. A host shell has no
sandbox and no bind mount: its images go to `~/.cspace/paste` and the host
path is typed. A supervisor tab and a pane whose child has exited have
nowhere to type, and say so in the footer. The clipboard is a `Clipboard`
seam beside `PaneHost`, satisfied in `internal/cli` by `osascript` for the
image and `pbpaste` for the text — `osascript` prints a script's result with
a newline appended, which would change a pasted diff.
```

- [ ] **Step 6: Strike step 5 off the Rollout list**

In the same file, replace

```
5. **Mouse and image paste.**
```

with

```
5. ~~**Mouse and image paste.**~~ Landed.
```

- [ ] **Step 7: Update CLAUDE.md**

In `CLAUDE.md`, the `cspace tui` command line becomes:

```
- `cspace tui` — full-screen dashboard of all cspace containers (grouped by project) with attach / send / interrupt / down / up / browser restart, the mouse (click to select or focus, wheel to scroll) and leader `v` to paste the clipboard's image into a pane, and `?` for the bindings
```

and the **controlplane** architecture bullet gains one sentence at its end, before the keybindings sentence:

```
The mouse is on in cell-motion mode (a `tea.View` property in bubbletea v2, not a program option) and hit-tested against a `geometry` value the model rebuilds after every message, so a click resolves to a row, a tab or the pane area without rendering; nothing is forwarded to the child, and the terminal's own selection and OSC 8 link clicks are still available on shift+drag and shift+click. Leader `v` writes the clipboard's PNG to the sandbox's `paste/` directory through a `Clipboard` seam (`internal/cli/clipboard.go`, `osascript` plus `pbpaste`) and types the `/sessions/paste/...` path the pane can open.
```

- [ ] **Step 8: Record the wheel on the scrollback finding**

Append to `.cspace/context/findings/2026-09-19-scroll-mode-never-reaches-a-tmux-backed-panes-history.md`'s `## Updates` section:

```
### 2026-09-19 — status: open
Rollout step 5 gave the wheel the same refusal: over a tmux-backed pane it
posts `noScrollbackNotice()`, the line leader `[` already posts, rather than
arming a mode with nothing in it. One function now owns that text, so the
two cannot drift. The gap itself is unchanged — both ways out listed above
still apply — and the finding stays open.
```

- [ ] **Step 9: Check**

Run: `make check`
Expected: green.

- [ ] **Step 10: Commit**

```bash
git add internal/controlplane/view.go internal/controlplane/keys.go internal/controlplane/input_test.go \
        docs/superpowers/specs/2026-09-17-control-plane-design.md CLAUDE.md \
        .cspace/context/findings/2026-09-19-scroll-mode-never-reaches-a-tmux-backed-panes-history.md
git commit -m "Document the mouse and image paste, and land rollout step 5"
```

---

### Task 7: Live verification against a real sandbox

Everything above is model tests and one pasteboard test. This task drives the real binary under a pty against a real sandbox, because a mouse sequence that the terminal encodes differently, a geometry that is one row out, or a path Claude does not recognise are all things a model test cannot see.

**Files:**
- Create: `scripts/tui-smoke/mouse.py`
- Test: the script itself, run by hand against a throwaway sandbox

**Interfaces:**
- Consumes: `scripts/tui-smoke/tuilib.py`'s `Tui` (`send`, `pump`, `display`, `save`, `quit`, `exit_code`), `bin/cspace-go`.
- Produces: `scripts/tui-smoke/mouse.py`, and a written report of what each step showed.

- [ ] **Step 1: Write the harness script**

Create `scripts/tui-smoke/mouse.py`:

```python
#!/usr/bin/env python3
"""Drive `cspace tui`'s mouse and image paste under a pty, Mac-only.

    make build
    bin/cspace-go up mouse-smoke
    python3 scripts/tui-smoke/mouse.py mouse-smoke
    bin/cspace-go down mouse-smoke

Not run by `make check`: it needs a real sandbox and the real pasteboard.
Each step prints what it did and the screen it produced; read them.

Mouse events are SGR sequences, which the harness can send raw. Columns and
rows are ONE-based on the wire and zero-based in bubbletea, which halves
every off-by-one argument if you keep it in mind:

    press    \\x1b[<0;COL;ROWM
    release  \\x1b[<0;COL;ROWm
    wheel up \\x1b[<64;COL;ROWM
    wheel dn \\x1b[<65;COL;ROWM
"""

import os
import subprocess
import sys
import tempfile
import zlib
import struct

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from tuilib import Tui  # noqa: E402

ROWS, COLS = 40, 120

# The layout at 40x120, from internal/controlplane/geometry.go:
#   sidebar   columns 1..24 (1-based), the 24th is the rule
#   list      rows 1..25   (bodyHeight 39 -> band 13, list 39-13-1)
#   tabs row  row 1, columns 25..120
#   main      rows 2..39,  columns 25..120
#   footer    row 40
SIDEBAR_COL = 6
TABS_ROW = 1
TAB_COL = 32
MAIN_COL = 60
MAIN_ROW = 20


def press(t, col, row):
    t.send(("\x1b[<0;%d;%dM" % (col, row)).encode(), then=0.8)
    t.send(("\x1b[<0;%d;%dm" % (col, row)).encode(), then=0.8)


def wheel(t, col, row, up=True):
    code = 64 if up else 65
    t.send(("\x1b[<%d;%d;%dM" % (code, col, row)).encode(), then=0.8)


def make_png(path):
    def chunk(tag, data):
        body = tag + data
        return struct.pack(">I", len(data)) + body + struct.pack(">I", zlib.crc32(body) & 0xFFFFFFFF)

    raw = b"".join(b"\x00" + b"\xff\x00\x00" * 2 for _ in range(2))
    png = (b"\x89PNG\r\n\x1a\n"
           + chunk(b"IHDR", struct.pack(">IIBBBBB", 2, 2, 8, 2, 0, 0, 0))
           + chunk(b"IDAT", zlib.compress(raw))
           + chunk(b"IEND", b""))
    with open(path, "wb") as f:
        f.write(png)


def clipboard_png(path):
    subprocess.check_call(["osascript",
                           "-e", "on run argv",
                           "-e", "set the clipboard to (read (POSIX file (item 1 of argv)) as «class PNGf»)",
                           "-e", "end run",
                           path])


def clipboard_text(text):
    p = subprocess.Popen(["pbcopy"], stdin=subprocess.PIPE)
    p.communicate(text.encode())


def step(n, what, t):
    print("\n=== %d. %s ===" % (n, what))
    print(t.display())


def row_line(t, name):
    """The 1-based screen row whose sidebar cell holds `name`.

    `cspace tui` is host-wide — it lists every project on the machine — so
    which line this sandbox lands on depends on what else is up. A fixed
    row number would quietly drive somebody else's sandbox and the rest of
    the run would prove nothing.
    """
    for i, line in enumerate(t.display().split("\n"), start=1):
        if name in line[:24]:
            return i
    raise SystemExit("%s is not in the sidebar; is it up?" % name)


def main():
    sandbox = sys.argv[1] if len(sys.argv) > 1 else "mouse-smoke"

    # Save the text clipboard and put it back at the end. An IMAGE on the
    # clipboard when this starts cannot be restored — pbcopy writes text
    # only — and is lost. Say so rather than pretend otherwise.
    saved = subprocess.run(["pbpaste"], capture_output=True).stdout

    tmp = tempfile.mkdtemp(prefix="cspace-mouse-smoke-")
    src = os.path.join(tmp, "src.png")
    make_png(src)

    t = Tui(cwd=os.getcwd(), rows=ROWS, cols=COLS)
    try:
        t.pump(12)
        step(0, "booted", t)

        press(t, SIDEBAR_COL, row_line(t, sandbox))
        step(1, "clicked %s's sidebar row (the ▸ marker should be on it)" % sandbox, t)

        t.send(b"\r", then=6.0)   # enter: open a claude pane on it
        step(2, "opened a claude pane", t)

        t.send(b"\x00", then=0.5)  # ctrl+space, the leader
        t.send(b"t", then=1.0)     # the new-pane picker
        t.send(b"\x1b[B" * 3, then=0.5)
        t.send(b"\r", then=4.0)    # host shell
        step(3, "opened a host shell (two tabs now)", t)

        press(t, TAB_COL, TABS_ROW)
        step(4, "clicked the first tab (focus should be back on it)", t)

        press(t, MAIN_COL, MAIN_ROW)
        step(5, "clicked the pane area", t)

        wheel(t, SIDEBAR_COL, 6, up=False)
        wheel(t, SIDEBAR_COL, 6, up=False)
        step(6, "wheeled down over the sidebar (selection should have moved)", t)

        wheel(t, MAIN_COL, MAIN_ROW, up=True)
        step(7, "wheeled up over the CLAUDE pane (expect the no-scrollback notice)", t)

        t.send(b"\x00", then=0.3)
        t.send(b"n", then=1.0)     # next tab: the host shell
        t.send(b"seq 1 200\r", then=2.0)
        wheel(t, MAIN_COL, MAIN_ROW, up=True)
        step(8, "wheeled up over the HOST SHELL (expect 'scroll · N lines back')", t)
        wheel(t, MAIN_COL, MAIN_ROW, up=False)
        step(9, "wheeled back down", t)

        t.send(b"\x00", then=0.3)
        t.send(b"g", then=0.5)     # back to live
        t.send(b"\x00", then=0.3)
        t.send(b"p", then=1.0)     # back to the claude pane

        clipboard_png(src)
        t.send(b"\x00", then=0.3)
        t.send(b"v", then=6.0)
        step(10, "leader v with a PNG on the clipboard "
                 "(expect /sessions/paste/<stamp>.png in Claude's input box, NOT sent)", t)

        clipboard_text("hello from the smoke test")
        t.send(b"\x00", then=0.3)
        t.send(b"v", then=4.0)
        step(11, "leader v with TEXT on the clipboard (expect the text pasted)", t)

        t.send(b"\x00", then=0.3)
        t.send(b"?", then=1.0)
        step(12, "the help overlay (expect the mouse line and the shift-drag note)", t)
        t.send(b"?", then=0.5)

        t.send(b"x", then=1.0)
        step(13, "typed 'x' into the pane after all that "
                 "(expect it in Claude's input box: keys still reach the child)", t)

        t.send(b"\x00", then=0.3)
        t.send(b"x", then=4.0)     # close the pane
        t.send(b"\x00", then=0.3)
        t.send(b"x", then=4.0)     # close the host shell
        t.send(b"\x00", then=0.3)
        t.quit(key=b"q")
        print("\nexit: %s" % t.exit_code())
    finally:
        p = subprocess.Popen(["pbcopy"], stdin=subprocess.PIPE)
        p.communicate(saved)
        print("restored the TEXT clipboard; an image that was on it is gone")

    print("\npaste files written:")
    home = os.path.expanduser("~")
    for root in (os.path.join(home, ".cspace", "sessions"), os.path.join(home, ".cspace", "paste")):
        for dirpath, _, files in os.walk(root):
            if os.path.basename(dirpath) == "paste" or dirpath == root:
                for f in files:
                    print("  %s" % os.path.join(dirpath, f))


if __name__ == "__main__":
    main()
```

- [ ] **Step 2: Build and boot a throwaway sandbox**

```bash
cd /Users/elliott/Projects/cspace-control-plane-5
make build
bin/cspace-go up mouse-smoke
```

`cspace up` spawns the host daemon if it is not already running, provisions a clone at `~/.cspace/clones/cspace/mouse-smoke`, boots the container, and attaches. Leave the attach with the sandbox's own exit (Ctrl+D or `exit`) — the sandbox keeps running; the smoke script needs it up, not attached.

Expected: the sandbox appears in `bin/cspace-go tui`'s sidebar under the `cspace` project.

- [ ] **Step 3: Run the script**

```bash
python3 scripts/tui-smoke/mouse.py mouse-smoke 2>&1 | tee /tmp/mouse-smoke.txt
```

Read every step. What each must show:

1. **Sidebar click** — the `▸` marker is on `<sandbox>`'s row, which the script located by name rather than by a fixed line, and the detail band below the list describes that sandbox. If the marker is on some other row, the sidebar is not laid out as `row_line` assumed and nothing after this step can be trusted.
2. **Claude pane** — a tab appears reading `cspace/<sandbox> · claude`, and Claude's own screen fills the main area.
3. **Host shell** — a second tab, `host · shell`.
4. **Tab click** — the first tab is lit again (`styleTabActive`) and the main area shows Claude.
5. **Pane click** — the footer switches to the leader's keys, which is what focus on the main area looks like.
6. **Sidebar wheel** — the `▸` marker moved down two selectable rows. The focus did **not** move: the footer still shows the leader's keys.
7. **Wheel on the Claude pane** — the footer reads `nothing to scroll: this pane has no scrollback — its child keeps its own history (PgUp/PgDn go to it)`. This is the ruling: a tmux-backed pane's wheel is a no-op with a notice.
8. **Wheel on the host shell** — the main area's first line reads `scroll · 3 lines back · ⌃Space g or any other key returns to live`, and the screen shows older output.
9. **Wheel back down** — the counter falls to 0.
10. **Leader `v` with a PNG** — `/sessions/paste/<timestamp>.png` appears in Claude's input box, with no newline: **nothing is sent**. The file exists at `~/.cspace/sessions/cspace/mouse-smoke/paste/<timestamp>.png` (the script lists it at the end) and its directory is `drwx------`.
11. **Leader `v` with text** — `hello from the smoke test` is pasted into the same box.
12. **Help overlay** — it carries `mouse: click a row, a tab or the pane; the wheel scrolls both` and `hold shift for the terminal's own mouse: drag selects, click opens a link`.
13. **A key after all that** — `x` lands in Claude's input box. Mouse mode did not cost the keyboard.

`exit: 0` at the end, not `HUNG`.

- [ ] **Step 4: Check the file the sandbox sees**

```bash
bin/cspace-go attach mouse-smoke
# inside:
ls -la /sessions/paste/
file /sessions/paste/*.png
exit
```

Expected: the PNG the host wrote, readable, and `file` calls it `PNG image data, 2 x 2`. This is the step that proves the typed path is the path the pane can open.

- [ ] **Step 5: Try the shift-drag escape hatch by hand**

With `bin/cspace-go tui` open in your own terminal, hold shift and drag across a pane. Expected: the terminal's own selection highlights, and the dashboard does not react. Without shift, the drag does nothing visible — cell-motion motion events are dropped.

Then shift+click one of the sidebar's port lines. Expected: the terminal opens its URL, which is the other thing mouse reporting takes away — a plain click on that line now selects its sandbox instead.

- [ ] **Step 6: Tear down**

```bash
bin/cspace-go down mouse-smoke
bin/cspace-go daemon stop
```

`cspace down <name>` stops the container, removes its sidecars and wipes `~/.cspace/sessions/cspace/mouse-smoke/` — the paste files with it — and `~/.cspace/controlplane/cspace/mouse-smoke/`. `cspace daemon stop` stops the dev daemon this worktree's binary spawned, so a later run of the repo's own `cspace` does not talk to a build from this branch.

Verify: `bin/cspace-go tui` no longer lists `mouse-smoke`, and `container ls -a | grep mouse-smoke` is empty.

- [ ] **Step 7: Write up what you saw**

Report to the reviewer, step by step, what each of the thirteen numbered observations actually showed — not "verified", the screens. Anything that did not match is either a bug to fix in this branch or, if it is a limitation rather than a defect, a new finding in `.cspace/context/findings/` with the usual frontmatter (`title`, `date`, `kind: finding`, `status: open`, `category`, `tags`).

- [ ] **Step 8: Check and commit**

Run: `make check`
Expected: green.

```bash
git add scripts/tui-smoke/mouse.py
git commit -m "Add a pty smoke script for the mouse and image paste"
```

---

## Self-review

**1. Spec coverage.** Walking the parts of the spec this step owns:

| Spec requirement | Task |
|---|---|
| "Mouse: cell-motion mode" | 2 (Step 3: `tea.MouseModeCellMotion` on every view) |
| "Click selects a sidebar row" | 2 (`selectListRow`) |
| "…or a tab" | 2 (`focusTab` via `geometry.tabs`) |
| "…or focuses the main area" | 2 (`g.main.contains`) |
| "the wheel scrolls the focused pane's scrollback" | 3 (`wheelMain`) |
| "…or the sidebar" | 3 (`moveSelection`) |
| "Nothing is forwarded to the child" | 2 and 3 (no path to `SendKey`; asserted by `TestWheelNeverReachesTheChild`), Global Constraints (guest tmux `mouse off`) |
| Non-goal: no drag-to-select; shift-drag and shift+click (the OSC 8 port links) still work and the overlay says so | 6 (help note), 7 Step 5 (verified by hand) |
| "Image paste (leader `v`) runs `osascript` to write the clipboard's PNG to `~/.cspace/sessions/<project>/<sandbox>/paste/<timestamp>.png`" | 4 (`writePNG`, `pasteDirs`) |
| "then types `/sessions/paste/<timestamp>.png` into the pane with no trailing newline" | 5 (`t.p.Paste(msg.text)`; asserted no `^M`) |
| "An empty or text-only clipboard falls back to a text paste" | 5 (`ErrNoImage` → `Text`) |
| Error handling: a failure is a footer notice, other panes unaffected | 5 (`pasteMsg.err`), 3 (`noScrollbackNotice`) |
| Testing: "model tests as today — messages in, golden views out" | 1, 2, 3, 5 |
| Rollout step 5 marked landed | 6 |

No gap. Hit testing, the geometry it needs, and the `Clipboard` seam are implementation structure the spec's Architecture line ("layout, focus, keys, mouse, paste") covers rather than spelling out.

**2. Placeholder scan.** No "TBD", no "add error handling", no "similar to Task N", no test described rather than written. Every code step carries the code. Task 7 is a manual step by nature and names the thirteen specific observations rather than saying "verify it works". Where a task's tests lean on a helper the package already has — `openHostShell`, `waitForPaneScreen`, `waitForHistory`, `settlePicker`, `plain`, `pump` — the plan names it by exact signature and says to reuse it rather than reprinting a second copy that could drift.

**3. Type consistency.**

- `geometry` fields: `sidebar`, `list`, `listRows`, `tabsY`, `tabs`, `main`. Used under exactly those names in `computeGeometry` (Task 1), `handleClick` (Task 2), `handleWheel` (Task 3) and the tests.
- `rect` fields are `x, y, w, h` throughout; `tabSpan` is `index, from, to` in both `planTabs` and `computeGeometry`, with the sidebar offset added exactly once, in `computeGeometry`.
- `planTabs(tabs []*tab, focused, width int, active bool) tabsPlan` — called with the same argument order in `renderTabs` and `computeGeometry`.
- `sidebarSplit(height) (list, band)` — both callers destructure in that order; `computeGeometry` discards `band`.
- `Clipboard.Image(ctx, project, sandbox) (string, error)` and `Clipboard.Text(ctx) (string, error)` — identical in the interface (Task 4), `nopClipboard`, `osaClipboard`, `fakeClipboard` (Task 5) and `pasteImageCmd`.
- `New(data, actor, host, clip, keys)` — the clipboard is the fourth parameter in the declaration, in `cmd_tui.go` and in all seven test call sites.
- `pasteMsg{id, text, err}` — produced in `pasteImageCmd`, consumed in `model.go`'s arm, constructed directly in `TestAPasteForATabThatClosedIsDropped`.
- `LabelPasteImage` is the one spelling of the footer label; `noScrollbackNotice()` is the one spelling of the refusal, and the wheel test asserts leader `[` and the wheel produce the same string rather than duplicating it.
- `focusTab(i int) Model` returns a `Model`; `handleClick` wraps it as `return m.focusTab(s.index), nil` and `moveTab` returns it directly. Consistent.
