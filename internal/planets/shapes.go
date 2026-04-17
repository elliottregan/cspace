package planets

import "math"

// Shape dimensions: 10 rows × 24 cols. The 2.4:1 column:row ratio
// compensates for typical terminal char aspect (~0.5 w/h), yielding
// a visually round disk.
const (
	ShapeRows = 10
	ShapeCols = 24
)

// Shape is a row × col grid of shade values in [0.0, 1.0]. 0.0 = empty,
// 1.0 = maximum intensity. Consumers render each cell as one terminal
// character selected from a phase-specific palette.
type Shape = [ShapeRows][ShapeCols]float64

// lightDir points from the sphere surface toward the directional key light.
// Slightly up-left-of-viewer so the bright spot sits in the upper-left
// quadrant of the disk and the terminator shadow falls toward the lower
// right — a classic 3D-sphere lighting setup.
var lightDir = [3]float64{-0.40, -0.30, 0.85}

// ambient is the base illumination on the shadowed hemisphere. Above 0 so
// the silhouette stays visible past the terminator; low enough that the
// directional shadow is clearly asymmetric.
const ambient = 0.18

// sphereShape produces a directionally lit elliptical disk centered at
// (cx, cy) with semi-axes (rx, ry) in unit space. Each in-disk cell is
// treated as a point on a 3D unit sphere: surface normal N = (dx, dy, z),
// shade = ambient + (1 - ambient) · max(0, N · lightDir). This offsets the
// bright spot from geometric center, producing a visible shadow.
func sphereShape(cx, cy, rx, ry float64) Shape {
	var s Shape
	for r := 0; r < ShapeRows; r++ {
		for c := 0; c < ShapeCols; c++ {
			y := (float64(r) + 0.5) / float64(ShapeRows)
			x := (float64(c) + 0.5) / float64(ShapeCols)
			dx := (x - cx) / rx
			dy := (y - cy) / ry
			d2 := dx*dx + dy*dy
			if d2 >= 1 {
				continue
			}
			z := math.Sqrt(1 - d2)
			dot := dx*lightDir[0] + dy*lightDir[1] + z*lightDir[2]
			if dot < 0 {
				dot = 0
			}
			shade := ambient + (1-ambient)*dot
			if shade > 1 {
				shade = 1
			}
			s[r][c] = shade
		}
	}
	return s
}

// applyBands dims every other band of `period` rows by `dim`, producing
// the horizontal stripes characteristic of gas giants.
func applyBands(s Shape, period int, dim float64) Shape {
	for r := 0; r < ShapeRows; r++ {
		if (r/period)%2 == 0 {
			continue
		}
		for c := 0; c < ShapeCols; c++ {
			s[r][c] *= dim
		}
	}
	return s
}

// applyPolarCap brightens the top `rows` rows of a shape by `boost`,
// clamping to 1.0. Used for Mars's northern ice cap.
func applyPolarCap(s Shape, rows int, boost float64) Shape {
	for r := 0; r < rows && r < ShapeRows; r++ {
		for c := 0; c < ShapeCols; c++ {
			if s[r][c] > 0 {
				v := s[r][c] * boost
				if v > 1 {
					v = 1
				}
				s[r][c] = v
			}
		}
	}
	return s
}

// applyRings overlays a thin ring at the equator row, extending outward
// past the planet body to the frame edges. The ring fades from brighter
// near the body's terminator toward the outer edge of the frame.
func applyRings(s Shape, bodyRx float64) Shape {
	mid := ShapeRows / 2
	rows := []int{mid - 1, mid}
	for _, r := range rows {
		if r < 0 || r >= ShapeRows {
			continue
		}
		for c := 0; c < ShapeCols; c++ {
			x := (float64(c) + 0.5) / float64(ShapeCols)
			d := math.Abs(x - 0.5)
			// Ring extends from just outside the body (d > bodyRx) to
			// near the frame edge (d < 0.48).
			if d <= bodyRx || d >= 0.48 {
				continue
			}
			// Brighter near body, fading to 0.4 at outer edge.
			t := (d - bodyRx) / (0.48 - bodyRx)
			shade := 0.8 - 0.4*t
			if shade > s[r][c] {
				s[r][c] = shade
			}
		}
	}
	return s
}

// applyContinents darkens a handful of interior cells to suggest land
// masses against the ocean fill.
func applyContinents(s Shape) Shape {
	patches := [][2]int{
		{3, 8}, {3, 9}, {4, 7}, {4, 10}, {4, 11},
		{5, 14}, {5, 15}, {6, 15},
		{3, 17}, {4, 18},
		{6, 11}, {7, 10},
	}
	for _, p := range patches {
		r, c := p[0], p[1]
		if r >= 0 && r < ShapeRows && c >= 0 && c < ShapeCols && s[r][c] > 0 {
			s[r][c] *= 0.55
		}
	}
	return s
}

var (
	mercurySimpleSphere = sphereShape(0.5, 0.5, 0.46, 0.46)
	venusUniformHaze    = sphereShape(0.5, 0.5, 0.47, 0.47)
	earthContinents     = applyContinents(sphereShape(0.5, 0.5, 0.45, 0.45))
	marsPolarCap        = applyPolarCap(sphereShape(0.5, 0.5, 0.43, 0.43), 2, 1.25)
	jupiterBands        = applyBands(sphereShape(0.5, 0.5, 0.47, 0.47), 1, 0.72)
	saturnRings         = applyRings(sphereShape(0.5, 0.5, 0.32, 0.32), 0.32)
	uranusSmallSphere   = sphereShape(0.5, 0.5, 0.40, 0.40)
	neptuneDenseCore    = sphereShape(0.5, 0.5, 0.40, 0.40)
)

var shapes = map[string]Shape{
	"mercury": mercurySimpleSphere,
	"venus":   venusUniformHaze,
	"earth":   earthContinents,
	"mars":    marsPolarCap,
	"jupiter": jupiterBands,
	"saturn":  saturnRings,
	"uranus":  uranusSmallSphere,
	"neptune": neptuneDenseCore,
}

// GetShape returns the shade grid for the named planet. Unknown names
// fall back to the mercury sphere so custom instance names still render.
func GetShape(name string) Shape {
	if s, ok := shapes[name]; ok {
		return s
	}
	return mercurySimpleSphere
}
