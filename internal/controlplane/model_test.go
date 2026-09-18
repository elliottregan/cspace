package controlplane

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

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

// A fresh dashboard would otherwise show no ports for up to a full
// slowInterval: Init fires all three ticks at t=0 while m.rows is still
// empty, so the very first slow poll has no targets. Seeding the slow
// cadence from the first successful snapshot — which already knows the row
// set — means ports and stats show up right after that snapshot lands
// instead of waiting out the regular 10s chain.
func TestFirstSnapshotTriggersAnImmediateSlowPoll(t *testing.T) {
	d := &fakeData{snap: testSnapshot(), ports: []control.Port{
		{Port: 5173, Label: "web", URL: "http://mercury.alpha.cspace.test:5173/"},
	}}
	m := New(d, &recordingActor{}, NewKeyMap(nil))
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = mm.(Model)
	_ = m.Init() // the cadences start; their own ticks are irrelevant here

	_, cmd := m.Update(snapshotMsg{snap: d.snap})
	drain(cmd)

	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.portsFor) == 0 {
		t.Fatal("the first successful snapshot should trigger an immediate slow poll")
	}
	if d.portsFor[0] != "alpha/mercury" {
		t.Errorf("ports asked for %v, want alpha/mercury", d.portsFor)
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
	// control leaves Daemon zeroed on its own error paths, so a failed poll
	// must not be allowed to flip the tabs line from healthy to
	// unreachable — the daemon itself did not go anywhere.
	if !m.daemon.Reachable {
		t.Errorf("daemon = %+v, want the last-known health preserved", m.daemon)
	}
	out := plain(m.View().Content)
	if !strings.Contains(out, "mercury") {
		t.Error("the sidebar must keep rendering the last-known rows")
	}
	if !strings.Contains(out, "snapshot failed") {
		t.Errorf("the footer should name what failed; got:\n%s", out)
	}
	if !strings.Contains(out, "apiserver down") {
		t.Errorf("the footer should carry the poll error; got:\n%s", out)
	}
	if !strings.Contains(out, "ago") {
		t.Errorf("the footer should mark how stale the rows are; got:\n%s", out)
	}
	if !strings.Contains(out, "daemon 1.0.0-rc.48") {
		t.Errorf("the tabs line should still show the last-known daemon health; got:\n%s", out)
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

// When a busy host's row set collapses while the selection sat deep in the
// old list, the old index can land past the end of the new one entirely —
// restoreSelection's outward search probes only m.selected±d, and every one
// of those probes is out of range. It must not fall straight to index 0 (a
// project header): a last linear scan should still find a selectable row.
func TestSelectionFallsBackToASelectableRowWhenRowsShrinkPastTheOldIndex(t *testing.T) {
	d := &fakeData{snap: control.Snapshot{
		TakenAt: time.Unix(1_000_000, 0),
		Rows: []control.Row{
			{Kind: control.RowProject, Project: "alpha", Name: "alpha"},
			{Kind: control.RowSandbox, Project: "alpha", Name: "mercury", State: control.StateRunning, Selectable: true},
			{Kind: control.RowSidecar, Project: "alpha", Name: "mercury-convex"},
			{Kind: control.RowSandbox, Project: "alpha", Name: "issue-42", State: control.StateStopped, Selectable: true},
			{Kind: control.RowBrowser, Project: "alpha", Name: "browser (shared)", State: control.StateRunning, Selectable: true},
			{Kind: control.RowProject, Project: "beta", Name: "beta"},
			{Kind: control.RowSandbox, Project: "beta", Name: "venus", State: control.StateRunning, Selectable: true},
			{Kind: control.RowSidecar, Project: "beta", Name: "venus-db"},
			{Kind: control.RowSandbox, Project: "beta", Name: "earth", State: control.StateRunning, Selectable: true}, // index 8
			{Kind: control.RowBrowser, Project: "beta", Name: "browser (shared)", State: control.StateRunning, Selectable: true},
		},
	}}
	m := newTestModel(d, &recordingActor{})
	m.selected = 8 // "beta/earth"
	if got := m.selectedRow().Name; got != "earth" {
		t.Fatalf("test setup: selected = %q, want earth", got)
	}

	shrunk := control.Snapshot{
		TakenAt: time.Unix(1_000_010, 0),
		Rows: []control.Row{
			{Kind: control.RowProject, Project: "alpha", Name: "alpha"},
			{Kind: control.RowSandbox, Project: "alpha", Name: "mercury", State: control.StateRunning, Selectable: true},
			{Kind: control.RowSandbox, Project: "alpha", Name: "issue-42", State: control.StateStopped, Selectable: true},
			{Kind: control.RowSandbox, Project: "alpha", Name: "other", State: control.StateRunning, Selectable: true},
		},
	}
	mm, _ := m.Update(snapshotMsg{snap: shrunk})
	m = mm.(Model)

	got := m.selectedRow()
	if !got.Selectable {
		t.Errorf("selection landed on a non-selectable row: %+v", got)
	}
	if got.Kind != control.RowSandbox {
		t.Errorf("selection landed on kind %v, want a sandbox row: %+v", got.Kind, got)
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

// Before the first WindowSizeMsg lands, View has no geometry to lay out and
// falls back to a placeholder — but that frame still has to run in the
// alternate screen like every other one, or it can be painted on the normal
// screen and left behind in scrollback once the real layout takes over.
func TestPreSizeViewRunsInTheAlternateScreen(t *testing.T) {
	m := New(&fakeData{}, &recordingActor{}, NewKeyMap(nil))
	v := m.View()
	if !strings.Contains(v.Content, "starting cspace tui") {
		t.Errorf("pre-size view content = %q, want the placeholder", v.Content)
	}
	if !v.AltScreen {
		t.Error("the pre-size view should also run in the alternate screen")
	}
}

// tabsLine clamps its gap to a minimum of 1 but, before this fix, never
// truncated title or health — so a narrow window (a project/sandbox title
// alongside "daemon <version>") could render a couple of cells wider than
// the pane. Reproduces the design's own example: W=57 -> mainWidth 33,
// "alpha/mercury" + "daemon 1.0.0-rc.48" don't fit with even a 1-cell gap
// once styleTabs' own padding is counted.
func TestTabsLineFitsANarrowWindow(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	const mainWidth = 57 - sidebarWidth // 33

	line := plain(m.tabsLine(mainWidth))
	if w := ansi.StringWidth(line); w > mainWidth {
		t.Errorf("tabs line width = %d, want <= %d: %q", w, mainWidth, line)
	}
	if !strings.Contains(line, "daemon") {
		t.Errorf("tabs line should still show daemon health; got %q", line)
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
