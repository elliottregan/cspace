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
