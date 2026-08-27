package tui

import (
	"errors"
	"strings"
	"testing"
	"time"
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

func TestViewRendersDegradedAndDaemon(t *testing.T) {
	m := newTestModel(&recordingActor{})
	m.width, m.height = 100, 30
	m.daemon = DaemonHealth{Reachable: true, Version: "1.0.0-rc.40"}
	m.rows = []Row{
		{Kind: RowProject, Name: "alpha"},
		{Kind: RowSandbox, Project: "alpha", Name: "mercury", State: StateDegraded, Selectable: true},
	}
	out := m.View()
	if !strings.Contains(out, "mercury") {
		t.Error("view missing sandbox name")
	}
	if !strings.Contains(out, "1.0.0-rc.40") {
		t.Error("view missing daemon version")
	}
}

func TestViewConfirmFooter(t *testing.T) {
	m := newTestModel(&recordingActor{})
	m.width, m.height = 100, 30
	m.mode = modeConfirmDown
	out := m.View()
	if !strings.Contains(strings.ToLower(out), "y/n") && !strings.Contains(strings.ToLower(out), "[y/") {
		t.Errorf("confirm footer not shown; got tail:\n%s", out)
	}
}

func TestViewDaemonUnreachable(t *testing.T) {
	m := newTestModel(&recordingActor{})
	m.width, m.height = 100, 30
	m.daemon = DaemonHealth{Reachable: false}
	out := m.View()
	if !strings.Contains(strings.ToLower(out), "unreachable") {
		t.Error("view should mark daemon unreachable")
	}
}

func TestViewNoEvents(t *testing.T) {
	m := newTestModel(&recordingActor{})
	m.width, m.height = 100, 30
	m.events = nil
	out := m.View()
	if !strings.Contains(out, "no events") {
		t.Error("empty event tail should render 'no events yet'")
	}
	_ = time.Second
}

func TestViewBrowserDetailHealth(t *testing.T) {
	m := newTestModel(&recordingActor{})
	m.width, m.height = 100, 30
	m.rows = []Row{
		{Kind: RowBrowser, Name: "browser (shared)", Selectable: true,
			Browser: BrowserHealth{Reachable: true, Version: "Chrome/140"}},
	}
	m.selected = 0
	if out := m.View(); !strings.Contains(out, "CDP") || !strings.Contains(out, "Chrome/140") {
		t.Errorf("browser detail should show CDP health; got:\n%s", out)
	}
	// unreachable branch shows the restart hint
	m.rows[0].Browser = BrowserHealth{Reachable: false}
	if out := m.View(); !strings.Contains(strings.ToLower(out), "restart") {
		t.Errorf("unreachable browser detail should hint restart; got:\n%s", out)
	}
}

func TestFooterHintsVaryBySelection(t *testing.T) {
	running := Row{Kind: RowSandbox, State: StateRunning, Agent: AgentStatus{Reachable: true, State: "working"}}
	stopped := Row{Kind: RowSandbox, State: StateStopped}
	// helper: find a label's on-flag
	on := func(row Row, label string) bool {
		for _, h := range footerHints(row) {
			if h.label == label {
				return h.on
			}
		}
		t.Fatalf("label %q not in footer hints", label)
		return false
	}
	if !on(running, "[enter] attach") {
		t.Error("attach should be enabled for a running sandbox")
	}
	if on(stopped, "[enter] attach") {
		t.Error("attach should be disabled for a stopped sandbox")
	}
	if !on(running, "[i]nterrupt") {
		t.Error("interrupt should be enabled for a working sandbox")
	}
	idle := Row{Kind: RowSandbox, State: StateRunning, Agent: AgentStatus{Reachable: true, State: "idle"}}
	if on(idle, "[i]nterrupt") {
		t.Error("interrupt should be disabled for an idle sandbox")
	}
}

func TestViewSystemDivider(t *testing.T) {
	m := newTestModel(&recordingActor{})
	m.width, m.height = 100, 30
	m.rows = []Row{
		{Kind: RowProject, Name: "alpha"},
		{Kind: RowSandbox, Project: "alpha", Name: "mercury", State: StateRunning, Selectable: true},
		{Kind: RowSystem, Name: "buildkit", State: StateRunning},
	}
	m.selected = 1
	if out := m.View(); !strings.Contains(out, "— system —") {
		t.Errorf("system rows should be preceded by a divider; got:\n%s", out)
	}
}

func TestViewStaleBanner(t *testing.T) {
	m := newTestModel(&recordingActor{})
	m.width, m.height = 100, 30
	m.snapErr = errors.New("apiserver down")
	m.lastPoll = time.Unix(1_000_000, 0)
	if out := m.View(); !strings.Contains(out, "as of") {
		t.Errorf("stale snapshot should render an 'as of' marker; got:\n%s", out)
	}
}

// A stopped sidecar/browser/system row must not print the literal "running";
// its status word is derived from the row's State.
func TestRenderRowStatusWordFollowsState(t *testing.T) {
	for _, kind := range []RowKind{RowSidecar, RowBrowser, RowSystem} {
		stopped := renderRow(Row{Kind: kind, Name: "x", State: StateStopped})
		if strings.Contains(stopped, "running") {
			t.Errorf("kind %v stopped row should not be labeled 'running'; got: %q", kind, stopped)
		}
		if !strings.Contains(stopped, "stopped") {
			t.Errorf("kind %v stopped row should render 'stopped'; got: %q", kind, stopped)
		}
		running := renderRow(Row{Kind: kind, Name: "x", State: StateRunning})
		if !strings.Contains(running, "running") {
			t.Errorf("kind %v running row should render 'running'; got: %q", kind, running)
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
		// The reason usage is rendered with a decimal: whole gigabytes would
		// collapse these two distinct sandboxes onto the same "1G".
		{"gib scale keeps one decimal", 1193979904, 16 * gib, "1.1G/16G"},
		{"gib scale distinguishes neighbours", 1717986918, 16 * gib, "1.6G/16G"},
		{"sub-gib usage renders as MiB", 512 * (1 << 20), 4 * gib, "512M/4G"},
		// No sample (container stopped, or the stats probe failed): fall back
		// to the cap alone so the row never loses information.
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
