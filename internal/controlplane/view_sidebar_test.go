package controlplane

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/planets"
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

func navigationFixture() Model {
	m := New(&fakeData{}, &recordingActor{}, nopPaneHost{}, nopClipboard{}, NewKeyMap(nil))
	m.width, m.height = 100, 30
	m.rows = demoRows()
	m.rows = append(m.rows, control.Row{Kind: control.RowProject, Project: "beta", Name: "beta"}, control.Row{Kind: control.RowSandbox, Project: "beta", Name: "venus", Container: "cspace-beta-venus", State: control.StateRunning, Selectable: true})
	m.selected = 1
	m.sessions = map[sandboxKey][]control.Session{
		{Project: "alpha", Name: "mercury"}: {{Name: control.SessionClaude, ID: "first", State: control.InteractiveState{State: "working"}}, {Name: "cspace-claude-2", ID: "second", State: control.InteractiveState{State: "needs-input"}}},
		{Project: "beta", Name: "venus"}:    {{Name: control.SessionClaude, ID: "third", State: control.InteractiveState{State: "idle"}}},
	}
	m.tabs = []*tab{{id: 1, kind: KindClaude, project: "alpha", sandbox: "mercury", session: m.sessions[sandboxKey{Project: "alpha", Name: "mercury"}][0]},
		{id: 2, kind: KindShell, project: "alpha", sandbox: "mercury"}, {id: 3, kind: KindSupervisor, project: "alpha", sandbox: "mercury"}, {id: 4, kind: KindHostShell}}
	m.focused = 0
	return m
}

func TestNavigationIsProjectContainerSessionTree(t *testing.T) {
	m := navigationFixture()
	host := &fakeHost{t: t}
	opened, err := host.Open(context.Background(), KindClaude, m.rows[1], 70, 20)
	if err != nil {
		t.Fatal(err)
	}
	m.tabs[0].p = opened.Pane
	items := m.navigation()
	counts := map[navigationKind]int{}
	found := map[string]navigationItem{}
	for _, n := range items {
		counts[n.kind]++
		found[n.id] = n
		if n.kind != navPane && n.row.Kind != control.RowProject && n.row.Kind != control.RowSandbox && n.kind != navHost {
			t.Errorf("non-navigation container leaked into tree: %+v", n)
		}
		if n.kind == navNewSession && n.row.State == control.StateStopped {
			t.Errorf("stopped container offers a new session: %+v", n)
		}
	}
	for kind, want := range map[navigationKind]int{navProject: 2, navContainer: 3, navSession: 3, navPane: 3, navNewSession: 2, navNewContainer: 2, navHost: 1} {
		if got := counts[kind]; got != want {
			t.Errorf("kind %d count=%d, want %d", kind, got, want)
		}
	}
	if n := found[sessionNavID(m.rows[1], m.sessions[keyOf(m.rows[1])][0])]; n.tabID != 1 {
		t.Errorf("discovered open session was not mapped to its existing pane: %+v", n)
	}
	// Default Claude is represented once, even though discovery and open panes
	// both know it. Shell and supervisor remain siblings beneath the container.
	if _, duplicated := found[paneNavID(1)]; duplicated {
		t.Error("open Claude appears twice")
	}
	for _, id := range []int{2, 3} {
		if n := found[paneNavID(id)]; n.depth != 2 || n.row.Name != "mercury" {
			t.Errorf("pane %d is not under mercury: %+v", id, n)
		}
	}
	if n := found[paneNavID(4)]; n.depth != 1 || n.row.Project != "" {
		t.Errorf("host shell is not in Host group: %+v", n)
	}
}

func TestNavigationCollapsedGroupsHideOnlyTheirChildren(t *testing.T) {
	m := navigationFixture()
	m.toggleNavigation(containerNavID(m.rows[1]))
	for _, n := range m.navigation() {
		if n.row.Project == "alpha" && n.row.Name == "mercury" && n.kind != navContainer {
			t.Errorf("collapsed container retained child: %+v", n)
		}
	}
	m.toggleNavigation("project:beta")
	for _, n := range m.navigation() {
		if n.row.Project == "beta" && n.kind != navProject {
			t.Errorf("collapsed project retained child: %+v", n)
		}
	}
	m.toggleNavigation("host")
	for _, n := range m.navigation() {
		if n.tabID == 4 {
			t.Error("collapsed Host retained its shell")
		}
	}
	m.toggleNavigation("project:beta")
	foundVenus := false
	for _, n := range m.navigation() {
		if n.kind == navContainer && n.row.Name == "venus" {
			foundVenus = true
		}
	}
	if !foundVenus {
		t.Error("expanding beta did not restore venus")
	}
}

func TestSidebarResponsiveTreeFitsAndOmitsInfrastructureDetails(t *testing.T) {
	for _, width := range []int{80, 100, 140} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := navigationFixture()
			m.width = width
			m.ports = map[sandboxKey][]control.Port{{Project: "alpha", Name: "mercury"}: {{Port: 5173, Label: "dev", URL: "http://mercury.alpha.cspace.test:5173"}}}
			sw := sidebarWidthFor(width)
			block := styleSidebar.Width(sw).MaxWidth(sw).Height(29).Render(m.sidebarColumn(29))
			lines := strings.Split(block, "\n")
			if len(lines) != 29 {
				t.Fatalf("sidebar renders %d rows, want 29", len(lines))
			}
			for _, line := range lines {
				if got := ansi.StringWidth(line); got != sw {
					t.Errorf("line has width %d, want %d: %q", got, sw, plain(line))
				}
			}
			text := plain(block)
			for _, want := range []string{"alpha", "beta", "mercury", "venus", "Claude 1", "Claude 2", "shell", "supervisor", "Host", "New session", "New container"} {
				if !strings.Contains(text, want) {
					t.Errorf("sidebar missing %q:\n%s", want, text)
				}
			}
			for _, notWanted := range []string{"5173", "convex", "CDP", "http://", "16G"} {
				if strings.Contains(text, notWanted) {
					t.Errorf("sidebar retains detail %q", notWanted)
				}
			}
		})
	}
}

func TestNavigationSelectionRetainsCanonicalPlanetColors(t *testing.T) {
	m := navigationFixture()
	for _, n := range m.navigation() {
		if n.kind != navContainer || n.row.Name == "issue-42" {
			continue
		}
		p := planets.MustGet(n.row.Name)
		selected := m.navigationLine(n, 27, true)
		unselected := m.navigationLine(n, 27, false)
		color := fmt.Sprintf("38;2;%d;%d;%d", p.Color[0], p.Color[1], p.Color[2])
		for _, raw := range []string{selected, unselected} {
			if !strings.Contains(raw, color) || !strings.Contains(plain(raw), p.Symbol) {
				t.Errorf("%s lost canonical glyph/color: %q", n.row.Name, raw)
			}
		}
		if !strings.Contains(selected, "48;2;48;53;68") || strings.Contains(unselected, "48;2;48;53;68") {
			t.Errorf("selection background missing or always on: %q / %q", selected, unselected)
		}
	}
}

func TestNavigationLongNamesAreTruncatedWithoutSplittingGlyphs(t *testing.T) {
	m := navigationFixture()
	name := "issue-" + strings.Repeat("界", 20)
	n := navigationItem{kind: navContainer, row: control.Row{Name: name}, label: name, depth: 1}
	for _, width := range []int{80, 100, 140} {
		available := sidebarWidthFor(width) - 1
		line := plain(m.navigationLine(n, available, true))
		if got := ansi.StringWidth(line); got != available {
			t.Errorf("width=%d rendered %d cells", width, got)
		}
		if !strings.Contains(line, "…") || strings.ContainsRune(line, '\ufffd') {
			t.Errorf("long Unicode name truncation=%q", line)
		}
	}
}

func TestNavigationWindowKeepsSelectedSessionVisible(t *testing.T) {
	m := navigationFixture()
	const count = 40
	m.sessions = map[sandboxKey][]control.Session{}
	k := keyOf(m.rows[1])
	for i := 2; i < count+2; i++ {
		m.sessions[k] = append(m.sessions[k], control.Session{Name: fmt.Sprintf("cspace-claude-%d", i), ID: fmt.Sprint(i)})
	}
	target := m.sessions[k][35]
	m.navID = sessionNavID(m.rows[1], target)
	m.height = 10
	column := plain(m.sidebarColumn(9))
	if got := strings.Count(column, "\n") + 1; got != 9 {
		t.Fatalf("sidebar renders %d lines", got)
	}
	if !strings.Contains(column, target.Label()) {
		t.Errorf("selected session absent:\n%s", column)
	}
	for _, tc := range []struct{ selected, height int }{{0, 10}, {5, 10}, {25, 10}, {49, 10}, {3, 100}} {
		from, to := navigationWindow(50, tc.selected, tc.height)
		if to-from > tc.height || from < 0 || to > 50 || tc.selected < from || tc.selected >= to {
			t.Errorf("selection %d window=%d..%d for height %d", tc.selected, from, to, tc.height)
		}
	}
}
