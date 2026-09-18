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

// A short terminal can hand the layout a negative row budget (height
// computed from window dimensions minus fixed chrome). renderSidebar must
// render nothing rather than pass a negative capacity to make() and panic.
func TestRenderSidebarToleratesANonPositiveHeight(t *testing.T) {
	for _, height := range []int{0, -3} {
		out := renderSidebar(demoRows(), nil, nil, 1, height)
		if out != "" {
			t.Errorf("height %d: renderSidebar = %q, want \"\"", height, out)
		}
	}

	// An empty row list must not panic either, and should render as blank
	// padding lines only.
	out := renderSidebar(nil, nil, nil, 0, 10)
	for _, l := range strings.Split(plain(out), "\n") {
		if strings.TrimSpace(l) != "" {
			t.Errorf("empty rows: expected only blank lines, got %q in:\n%s", l, plain(out))
		}
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
