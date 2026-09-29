package controlplane

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

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

// Geometry must describe what was painted, including the new navigation rows
// which need not correspond one-to-one with the container snapshot.
func TestGeometryIsEmptyBeforeTheFirstWindowSize(t *testing.T) {
	m := New(&fakeData{snap: testSnapshot()}, &recordingActor{}, nopPaneHost{}, nopClipboard{}, NewKeyMap(nil))
	mm, _ := m.Update(snapshotMsg{snap: testSnapshot()})
	g := mm.(Model).geom
	if g.sidebar.contains(0, 0) || g.main.contains(30, 5) || g.header.contains(30, 0) || g.environment.contains(0, 20) {
		t.Fatalf("unsized model occupies screen: %+v", g)
	}
	if len(g.listRows) != 0 || len(g.navItems) != 0 || len(g.headerLinks) != 0 {
		t.Fatalf("unsized model has hit targets: %+v", g)
	}
}

func TestGeometryNavigationRowsMatchRenderedSidebar(t *testing.T) {
	for _, width := range []int{80, 100, 140} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
			mm, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
			m = mm.(Model)
			g := m.geom
			wantSidebar := 24
			if width >= 100 {
				wantSidebar = 28
			}
			if g.sidebar.w != wantSidebar {
				t.Fatalf("sidebar width=%d, want %d", g.sidebar.w, wantSidebar)
			}
			lines := strings.Split(plain(m.sidebarColumn(23)), "\n")
			if len(lines) != 23 || len(g.listRows) != g.list.h {
				t.Fatalf("render/list mismatch: lines=%d geometry=%+v", len(lines), g)
			}
			if len(g.navItems) == 0 {
				t.Fatal("no navigation items")
			}
			for y, n := range g.navItems {
				if !strings.Contains(lines[y], n.label) {
					t.Errorf("navigation row %q maps to %q", n.id, lines[y])
				}
				if g.listRows[y] != n.rowIndex {
					t.Errorf("row %d snapshot index=%d, want %d", y, g.listRows[y], n.rowIndex)
				}
				if !g.list.contains(0, y) || g.environment.contains(0, y) {
					t.Errorf("navigation row %d not exclusively in list", y)
				}
			}
			for _, n := range g.navItems {
				if n.row.Kind == control.RowSidecar || n.row.Kind == control.RowBrowser || n.row.Kind == control.RowSystem {
					t.Errorf("infrastructure leaked into navigation: %+v", n)
				}
			}
		})
	}
}

func TestGeometrySessionPaneRowsFocusTheRepresentedPane(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	mm, cmd := m.openOrFocus(KindClaude, m.selectedRow())
	m = pump(t, mm.(Model), cmd)
	m.focus = focusSidebar
	mm, cmd = m.openOrFocus(KindShell, m.selectedRow())
	m = pump(t, mm.(Model), cmd)
	mustTabs(t, m, 2)
	m.geom = m.computeGeometry()
	var selected []int
	for _, n := range m.geom.navItems {
		if n.kind != navPane {
			continue
		}
		mm, cmd := m.activateNavigation(n)
		if cmd != nil {
			t.Fatal("focusing an open pane launched a command")
		}
		m = mm.(Model)
		if m.focus != focusMain || m.focusedTab() == nil || m.focusedTab().id != n.tabID {
			t.Fatalf("navigation item %+v focused the wrong pane", n)
		}
		selected = append(selected, n.tabID)
	}
	if len(selected) != 2 {
		t.Fatalf("found %d pane navigation rows, want 2", len(selected))
	}
}

func TestGeometryHeaderLinksMatchRenderedHeader(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	k := keyOf(m.selectedRow())
	m.headers = map[sandboxKey]headerSample{k: {status: control.HeaderStatus{Branch: "feature", PR: &control.PullRequestStatus{Number: 42, URL: "https://github.com/demo/repo/pull/42"}}}}
	m.services = map[sandboxKey][]control.ServiceStatus{k: {{Label: "dev", URL: "http://mercury.demo.cspace.test:3000", State: control.ServiceRunning}}}
	for _, width := range []int{80, 100, 140} {
		mm, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		m = mm.(Model)
		g := m.geom
		lines := strings.Split(plain(m.planHeader(m.paneWidth()).text), "\n")
		if len(g.headerLinks) != 3 {
			t.Fatalf("width %d: got %d header links, want Details, PR, dev", width, len(g.headerLinks))
		}
		for _, link := range g.headerLinks {
			text := ansi.Cut(lines[link.y], link.x, link.x+link.width)
			want := "dev"
			if link.details {
				want = "Details"
			} else if strings.Contains(link.url, "github.com") {
				want = "PR #42"
			}
			if !strings.Contains(text, want) {
				t.Errorf("width %d: hit target %+v painted %q, want %q", width, link, text, want)
			}
			if !g.header.contains(g.header.x+link.x, g.header.y+link.y) {
				t.Errorf("link %+v outside header %+v", link, g.header)
			}
		}
	}
}

func TestGeometryMainAreaMatchesTheCursorArithmetic(t *testing.T) {
	for _, width := range []int{80, 100, 140} {
		m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
		mm, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		m = mm.(Model)
		g := m.geom
		sw := sidebarWidthFor(width)
		if !g.main.contains(sw+1, 2) || g.main.y != 2 || g.header.h != 2 {
			t.Fatalf("pane/header geometry=%+v", g)
		}
		if g.main.contains(sw+1, m.height-1) || g.main.contains(sw-1, 5) || g.main.contains(sw+1, 1) {
			t.Fatalf("pane overlaps sidebar, header, or footer: %+v", g)
		}
		if g.environment.y+g.environment.h != m.height-1 || g.environment.y != g.list.h {
			t.Fatalf("environment is not anchored below navigation: %+v", g)
		}
	}
}

func TestGeometryFollowsAResize(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	wide := m.geom.main.w
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = mm.(Model)
	narrow := m.geom
	if narrow.main.w >= wide || narrow.main.w != mainWidthFor(80) {
		t.Fatalf("resized main width=%d from %d", narrow.main.w, wide)
	}
	if narrow.list.h != len(narrow.listRows) || narrow.sidebar.w != 24 {
		t.Fatalf("sidebar did not follow resize: %+v", narrow)
	}
	if got := len(strings.Split(m.sidebarColumn(11), "\n")); got != 11 {
		t.Fatalf("resized sidebar has %d rows, want 11", got)
	}
}

func TestCompactEnvironmentRespectsShortTerminals(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	for _, height := range []int{1, 2, 5, 6, 8, 11, 23, 39} {
		column := plain(m.sidebarColumn(height))
		lines := strings.Split(column, "\n")
		if len(lines) != height {
			t.Errorf("height %d rendered %d rows", height, len(lines))
		}
		if height < 6 {
			if !strings.Contains(lines[len(lines)-1], "Environment") {
				t.Errorf("height %d lost compact environment: %q", height, column)
			}
		} else {
			if !strings.Contains(lines[len(lines)-2], "Browser") || !strings.Contains(lines[len(lines)-1], "Daemon") {
				t.Errorf("height %d lost environment indicators: %q", height, column)
			}
		}
	}
}
