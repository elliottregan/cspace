package planets

import "math"

// Shape dimensions: 48 rows × 48 cols of SUB-pixels. Rendering pairs
// consecutive rows into one terminal line using the ▀/▄ half-block
// characters, so output is ShapeRows/2 = 24 terminal rows tall. With
// subpixels counted as 1 visual unit and terminal chars as 1 unit wide
// × 2 units tall, this gives a square unit space and a visually round
// disk when rx == ry in sphereShape.
const (
	ShapeRows = 48
	ShapeCols = 48
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

// applyRings overlays Saturn-like rings centered on the equator, one
// subpixel row thick. Three bands plus a Cassini-style gap:
//
//	[body] | A-ring (bright) | gap (dim) | B-ring (medium) | taper
//
// The body's shadow passes in front of the rings naturally because
// sphereShape has already filled the body rows at a higher shade than
// the ring shades, so `if shade > s[row][c]` preserves the body.
func applyRings(s Shape, bodyRx float64) Shape {
	// Two thin equatorial subpixel rows; very thin elliptical disc.
	midRow := ShapeRows / 2
	rows := []int{midRow - 1, midRow}

	// Ring bands: (innerX, outerX, shade). Measured from center; symmetric
	// around x=0.5.
	bands := []struct{ inner, outer, shade float64 }{
		{bodyRx + 0.015, bodyRx + 0.065, 0.90}, // inner bright ring (A)
		{bodyRx + 0.075, bodyRx + 0.090, 0.30}, // Cassini gap
		{bodyRx + 0.100, 0.470, 0.75},          // outer ring (B)
	}

	for i, row := range rows {
		if row < 0 || row >= ShapeRows {
			continue
		}
		// Upper and lower subpixel rows at the equator get slightly less
		// intensity than the true center row so the ring reads as very
		// thin rather than a solid slab.
		rowScale := 1.0
		if i == 0 {
			rowScale = 0.55
		}
		for c := 0; c < ShapeCols; c++ {
			x := (float64(c) + 0.5) / float64(ShapeCols)
			d := math.Abs(x - 0.5)
			for _, b := range bands {
				if d < b.inner || d > b.outer {
					continue
				}
				// Fade at extreme outer edge so rings don't hard-cut at
				// the frame edge.
				taper := 1.0
				if d > 0.42 {
					taper = (0.47 - d) / 0.05
					if taper < 0 {
						taper = 0
					}
				}
				shade := b.shade * rowScale * taper
				if shade > s[row][c] {
					s[row][c] = shade
				}
				break
			}
		}
	}
	return s
}

// applyContinents darkens a scattered set of interior cells to suggest
// landmasses. Patch centers are normalized so they stay inside the disk
// across any grid size.
func applyContinents(s Shape) Shape {
	patches := [][2]float64{
		{0.35, 0.35}, {0.40, 0.30}, {0.30, 0.45},
		{0.62, 0.40}, {0.68, 0.55}, {0.55, 0.65},
		{0.42, 0.62}, {0.72, 0.30}, {0.48, 0.75},
	}
	dRow := ShapeRows / 24
	dCol := ShapeCols / 12
	for _, p := range patches {
		rc := int(p[1] * float64(ShapeRows))
		cc := int(p[0] * float64(ShapeCols))
		for dr := -dRow; dr <= dRow; dr++ {
			for dc := -dCol; dc <= dCol; dc++ {
				r, c := rc+dr, cc+dc
				if r < 0 || r >= ShapeRows || c < 0 || c >= ShapeCols {
					continue
				}
				if s[r][c] > 0 {
					s[r][c] *= 0.60
				}
			}
		}
	}
	return s
}

var (
	mercurySimpleSphere = sphereShape(0.5, 0.5, 0.46, 0.46)
	venusUniformHaze    = sphereShape(0.5, 0.5, 0.47, 0.47)
	earthContinents     = applyContinents(sphereShape(0.5, 0.5, 0.45, 0.45))
	marsPolarCap        = applyPolarCap(sphereShape(0.5, 0.5, 0.43, 0.43), 10, 1.25)
	jupiterBands        = applyBands(sphereShape(0.5, 0.5, 0.47, 0.47), 6, 0.70)
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
