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

// The count has to describe both sides. Dropping from the left alone can
// drop nothing — with tab 0 focused there is nothing to its left — and the
// row then rendered a literal "+0" while hiding every tab to the right of
// the focused one. Reachable at ordinary sizes: four tabs of ~26 cells
// overflow a 96-column main area, and ⌃Space p walks the focus to tab 0.
func TestRenderTabsCountsBothSidesAndNeverSaysPlusZero(t *testing.T) {
	tabs := []*tab{
		{id: 1, kind: KindClaude, project: "resume-redux", sandbox: "mercury"},
		{id: 2, kind: KindShell, project: "resume-redux", sandbox: "mercury"},
		{id: 3, kind: KindSupervisor, project: "cspace", sandbox: "issue-42"},
	}
	cases := []struct {
		name         string
		focused      int
		width        int
		wantKeep     string // a tab that must still be legible
		wantElisions []string
		wantAbsent   []string
		wantPrefix   string // set only where a specific side's marker matters
		wantSuffix   string
	}{
		// Tab 0 focused: nothing is to its left, so the row drops from the
		// right and says so.
		{"the first tab keeps its neighbour and counts the right",
			0, 70, "resume-redux/mercury", []string{"+1"}, []string{"+0"}, "", ""},
		{"the first tab alone still counts the right",
			0, 30, "resume-redux", []string{"+2"}, []string{"+0"}, "", ""},
		// A middle tab: one dropped on each side, both counted — on their
		// own side, not just "a +1 appears somewhere in the row". A row
		// that dropped the right-hand marker but kept the left one's "+1"
		// would still satisfy a bare strings.Contains(got, "+1"), so pin
		// each marker at its own end instead.
		{"a middle tab counts both sides",
			1, 40, "resume-redux", []string{"+1"}, []string{"+0"}, "+1 ", " +1"},
		// The last tab is the original case: everything dropped is on the
		// left, and there is no right-hand count to show.
		{"the last tab counts only the left",
			2, 40, "issue-42", []string{"+2"}, []string{"+0"}, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := plain(renderTabs(tabs, tc.focused, tc.width, true))
			if w := ansi.StringWidth(got); w > tc.width {
				t.Errorf("row is %d cells wide, want at most %d: %q", w, tc.width, got)
			}
			if !strings.Contains(got, tc.wantKeep) {
				t.Errorf("row %q dropped the focused tab %q", got, tc.wantKeep)
			}
			for _, want := range tc.wantElisions {
				if !strings.Contains(got, want) {
					t.Errorf("row %q does not carry the elision count %q", got, want)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("row %q claims %q, which describes nothing", got, absent)
				}
			}
			if tc.wantPrefix != "" && !strings.HasPrefix(got, tc.wantPrefix) {
				t.Errorf("row %q does not open with the left elision count %q", got, tc.wantPrefix)
			}
			if tc.wantSuffix != "" && !strings.HasSuffix(got, tc.wantSuffix) {
				t.Errorf("row %q does not close with the right elision count %q", got, tc.wantSuffix)
			}
		})
	}

	// The whole row when everything fits: no counts at all, either side.
	if got := plain(renderTabs(tabs, 0, 100, true)); strings.Contains(got, "+") {
		t.Errorf("a row that fits carries an elision count: %q", got)
	}
}

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
