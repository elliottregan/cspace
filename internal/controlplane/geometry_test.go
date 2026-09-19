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
	m := New(&fakeData{snap: testSnapshot()}, &recordingActor{}, nopPaneHost{}, nopClipboard{}, NewKeyMap(nil))
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
