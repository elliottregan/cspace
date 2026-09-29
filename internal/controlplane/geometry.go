package controlplane

type rect struct{ x, y, w, h int }

func (r rect) contains(x, y int) bool {
	return r.w > 0 && r.h > 0 && x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

type geometry struct {
	sidebar     rect
	list        rect
	listRows    []int
	navItems    []navigationItem
	environment rect
	header      rect
	headerLinks []headerLink
	main        rect
}

func (m Model) computeGeometry() geometry {
	if m.width <= 0 || m.height <= 0 {
		return geometry{}
	}
	sw := sidebarWidthFor(m.width)
	bodyHeight := max(1, m.height-1)
	envHeight := environmentHeight(bodyHeight)
	lh := bodyHeight - envHeight
	g := geometry{sidebar: rect{0, 0, sw, bodyHeight}, list: rect{0, 0, sw - 1, lh}, environment: rect{0, lh, sw, envHeight}, header: rect{sw + 1, 0, m.paneWidth(), 2}, main: rect{sw, 2, mainWidthFor(m.width), max(0, bodyHeight-2)}}
	items := m.navigation()
	from, to := navigationWindow(len(items), m.navigationIndex(items), lh)
	g.navItems = items[from:to]
	g.listRows = make([]int, lh)
	for i := range g.listRows {
		g.listRows[i] = -1
	}
	for i, n := range g.navItems {
		g.listRows[i] = n.rowIndex
	}
	g.headerLinks = m.planHeader(m.paneWidth()).links
	return g
}
