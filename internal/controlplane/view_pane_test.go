package controlplane

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// A rebound leader has to reach every line that names it. The footer
// derives its label from the binding; the three lines in the main area used
// to spell ⌃Space out.
func TestPaneAreaNamesTheConfiguredLeader(t *testing.T) {
	keys := NewKeyMap(map[string][]string{"leader": {"ctrl+x"}})
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, &fakeHost{t: t})
	m.keys = keys

	got := plain(m.paneArea(70, 10)) // the empty state
	if !strings.Contains(got, "ctrl+x t") {
		t.Errorf("the empty state %q does not name the configured leader", got)
	}
	if strings.Contains(got, "⌃Space") {
		t.Errorf("the empty state %q still names the default leader", got)
	}

	m = openOne(t, &fakeHost{t: t, history: true})
	m.keys = keys
	m.scrolling = true
	if got := plain(m.paneArea(70, 10)); !strings.Contains(got, "ctrl+x g") {
		t.Errorf("the scroll header %q does not name the configured leader", got)
	}
}

// TestScrollBannerWinsOverTheExitedLine pins review-task3.md's Minor 1: scroll
// mode has to announce itself whether or not the pane's child is still
// running, because handlePaneKey's own scrolling branch already treats the
// two identically — any key returns to live, exited pane or not. Before
// this, the exited line won the render, so the mode was armed and eating a
// key while showing no counter and no "returns to live" hint at all.
func TestScrollBannerWinsOverTheExitedLine(t *testing.T) {
	h := &fakeHost{t: t, exits: true}
	m := openOne(t, h)
	waitForExit(t, m.tabs[0])

	notScrolling := plain(m.paneArea(70, 10))
	if !strings.Contains(notScrolling, "exited with status") {
		t.Fatalf("setup: an exited, non-scrolling pane does not show its reason: %q", notScrolling)
	}

	m.scrolling = true
	got := plain(m.paneArea(70, 10))
	if !strings.Contains(got, "returns to live") {
		t.Errorf("scroll mode on an exited pane rendered no banner: %q", got)
	}
	if strings.Contains(got, "exited with status") {
		t.Errorf("the exited line is still shown while scrolling, hiding the banner: %q", got)
	}
}

// The terminal cursor is the dashboard's claim about where typing goes, so
// it may only sit on a pane the operator can actually see. The help
// overlay and the two modals all render over the main area while the focus
// stays on the pane behind them — mainArea's own switch is the list — so a
// cursor placed by focus alone ends up blinking on top of the help text.
func TestTheCursorIsNotPlacedUnderAnOverlay(t *testing.T) {
	h := &fakeHost{t: t}
	m := openOne(t, h)
	if m.View().Cursor == nil {
		t.Fatal("a focused live pane has no cursor at all")
	}

	m.showHelp = true
	if m.View().Cursor != nil {
		t.Error("the cursor is placed at the pane under the help overlay")
	}
	m.showHelp = false

	m.mode = modePicker
	m.picker = newPanePicker(40)
	if m.View().Cursor != nil {
		t.Error("the cursor is placed at the pane under the new-pane picker")
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

func TestEmptyMainAreaExplainsStoppedContainerBoot(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	m.keys = NewKeyMap(map[string][]string{ActionBoot: {"v"}})
	m = selectContainerInTest(t, m, "alpha", "issue-42")
	got := plain(m.paneArea(80, 12))
	for _, want := range []string{"issue-42 is stopped", "v   boot", "only bind-mounted files are kept"} {
		if !strings.Contains(got, want) {
			t.Errorf("stopped-container empty state lacks %q: %s", want, got)
		}
	}
	if strings.Contains(got, "enter") || strings.Contains(got, "a shell in it") {
		t.Fatalf("empty state advertises an unavailable session/shell action: %s", got)
	}
}

func TestSidebarColumnShowsNavigationAndCompactEnvironment(t *testing.T) {
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, &fakeHost{t: t})
	col := m.sidebarColumn(23)
	if got := len(strings.Split(col, "\n")); got != 23 {
		t.Fatalf("sidebar has %d rows, want 23", got)
	}
	for _, want := range []string{"mercury", "New session", "New container", "Browser", "BuildKit", "Daemon"} {
		if !strings.Contains(plain(col), want) {
			t.Errorf("sidebar missing %q: %s", want, plain(col))
		}
	}
	for _, unwanted := range []string{"16G", "9222", "CDP"} {
		if strings.Contains(plain(col), unwanted) {
			t.Errorf("sidebar still has persistent details %q", unwanted)
		}
	}
	for _, line := range strings.Split(plain(col), "\n") {
		if ansi.StringWidth(line) > sidebarWidthFor(m.width)-1 {
			t.Errorf("sidebar line too wide: %q", line)
		}
	}
}

func TestViewPutsAFocusedPaneInTheMainArea(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = stepPump(t, m, "enter")
	mustTabs(t, m, 1)

	view := plain(m.View().Content)
	if !strings.Contains(view, "mercury · Claude") {
		t.Errorf("view %q has no header for the open pane", view)
	}
	// The footer follows the focus: with a pane focused it names the
	// leader's keys, not the sidebar's.
	if !strings.Contains(view, "sidebar") || !strings.Contains(view, "close") {
		t.Errorf("view %q does not show the leader footer", view)
	}
}
