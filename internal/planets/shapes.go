package planets

import "math"

// Shape dimensions: 48 rows × 48 cols of SUB-pixels. Rendering pairs
// consecutive rows into one terminal line using the ▀/▄ half-block
// characters, so output is ShapeRows/2 = 24 terminal rows tall.
const (
	ShapeRows = 48
	ShapeCols = 48
)

// Shape is a row × col grid of shade values in [0.0, 1.0]. 0.0 = empty,
// 1.0 = maximum intensity. Consumers render each cell as one terminal
// character selected from a phase-specific palette.
type Shape = [ShapeRows][ShapeCols]float64

// Overlay is a secondary surface feature applied on top of the base
// terrain during rendering: at each cell, the base color is blended
// toward Color by the overlay's shade value. Used for clouds (white),
// Jupiter's Great Red Spot (red), etc.
type Overlay struct {
	Shape Shape
	Color [3]uint8
}

// lightDir: classic 3D-sphere key light slightly up-left-of-viewer.
var lightDir = [3]float64{-0.40, -0.30, 0.85}

// ambient: base illumination on the shadow side so the silhouette stays
// visible past the terminator.
const ambient = 0.18

// sphereShape produces a directionally lit disk centered at (cx, cy)
// with semi-axes (rx, ry). Shading uses a Lambertian model with the
// light direction defined above.
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

// applyJupiterBands applies latitude-based bands with soft top/bottom
// edges and a small positional noise term so the bands curve with the
// sphere (narrower at poles, wider at equator) and look turbulent rather
// than drawn with a ruler.
func applyJupiterBands(s Shape) Shape {
	type band struct{ minDy, maxDy, dim float64 }
	bands := []band{
		{-0.95, -0.72, 0.62},
		{-0.68, -0.48, 0.90},
		{-0.44, -0.22, 0.58}, // NEB
		{-0.18, 0.06, 0.94},  // EZ
		{0.10, 0.32, 0.60},   // SEB
		{0.36, 0.56, 0.88},
		{0.60, 0.82, 0.64},
	}
	const bodyR = 0.47
	const edge = 0.045
	for r := 0; r < ShapeRows; r++ {
		for c := 0; c < ShapeCols; c++ {
			if s[r][c] == 0 {
				continue
			}
			y := (float64(r) + 0.5) / float64(ShapeRows)
			dy := (y - 0.5) / bodyR
			shade := 1.0
			for _, b := range bands {
				if dy < b.minDy || dy > b.maxDy {
					continue
				}
				dim := b.dim
				if dy-b.minDy < edge {
					t := (dy - b.minDy) / edge
					dim = 1 + (dim-1)*t
				} else if b.maxDy-dy < edge {
					t := (b.maxDy - dy) / edge
					dim = 1 + (dim-1)*t
				}
				shade = dim
				break
			}
			// Turbulence: small per-cell jitter.
			noise := (shapeHash(r, c) - 0.5) * 0.06
			shade += noise
			if shade < 0.30 {
				shade = 0.30
			}
			if shade > 1 {
				shade = 1
			}
			s[r][c] *= shade
		}
	}
	return s
}

// applyRings wraps Saturn-style rings around a sphere. The ring is a
// thin tilted ellipse (ringRx × ringRy). Where the ring passes below
// the sphere's equator it is drawn IN FRONT of the sphere; above the
// equator the sphere occludes the ring. Three concentric bands with a
// Cassini-style gap produce the striped disk look.
func applyRings(s Shape, bodyRx float64) Shape {
	const (
		ringRx = 0.48
		ringRy = 0.10
	)
	innerU := bodyRx/ringRx + 0.08
	bands := []struct{ inner, outer, shade float64 }{
		{innerU, innerU + 0.18, 0.88},        // inner bright ring (A)
		{innerU + 0.20, innerU + 0.24, 0.22}, // Cassini gap
		{innerU + 0.26, 0.98, 0.74},          // outer ring (B)
	}
	const edgeFade = 0.015
	for r := 0; r < ShapeRows; r++ {
		for c := 0; c < ShapeCols; c++ {
			y := (float64(r) + 0.5) / float64(ShapeRows)
			x := (float64(c) + 0.5) / float64(ShapeCols)
			dx := (x - 0.5) / ringRx
			dy := (y - 0.5) / ringRy
			u := math.Sqrt(dx*dx + dy*dy)

			var ringShade float64
			for _, b := range bands {
				if u < b.inner || u > b.outer {
					continue
				}
				shade := b.shade
				if u-b.inner < edgeFade {
					shade *= (u - b.inner) / edgeFade
				}
				if b.outer-u < edgeFade {
					shade *= (b.outer - u) / edgeFade
				}
				ringShade = shade
				break
			}
			if ringShade == 0 {
				continue
			}

			// Check sphere occlusion. The front half of the ring (below
			// the equator line in screen space) draws over the sphere;
			// the back half is hidden where the sphere overlaps.
			frontSide := y > 0.5
			sDx := (x - 0.5) / bodyRx
			sDy := (y - 0.5) / bodyRx
			inSphere := sDx*sDx+sDy*sDy < 1
			if inSphere && !frontSide {
				continue
			}
			if ringShade > s[r][c] {
				s[r][c] = ringShade
			}
		}
	}
	return s
}

// earthCloudShape generates soft wispy cloud patches scattered across
// the disk — used as an Overlay to produce bright white puffs over the
// blue base.
func earthCloudShape() Shape {
	patches := []struct{ cx, cy, rx, ry, intensity float64 }{
		{0.28, 0.32, 0.10, 0.05, 0.85},
		{0.52, 0.24, 0.11, 0.04, 0.72},
		{0.68, 0.44, 0.11, 0.05, 0.80},
		{0.40, 0.52, 0.09, 0.05, 0.60},
		{0.24, 0.52, 0.08, 0.04, 0.62},
		{0.60, 0.60, 0.12, 0.05, 0.72},
		{0.38, 0.72, 0.10, 0.05, 0.65},
		{0.72, 0.30, 0.08, 0.03, 0.50},
		{0.48, 0.40, 0.07, 0.03, 0.55},
	}
	const bodyR = 0.45
	var s Shape
	for r := 0; r < ShapeRows; r++ {
		for c := 0; c < ShapeCols; c++ {
			x := (float64(c) + 0.5) / float64(ShapeCols)
			y := (float64(r) + 0.5) / float64(ShapeRows)
			bdx := (x - 0.5) / bodyR
			bdy := (y - 0.5) / bodyR
			if bdx*bdx+bdy*bdy >= 1 {
				continue
			}
			var intensity float64
			for _, p := range patches {
				dx := (x - p.cx) / p.rx
				dy := (y - p.cy) / p.ry
				d2 := dx*dx + dy*dy
				if d2 >= 1 {
					continue
				}
				v := p.intensity * math.Exp(-d2*2.5)
				if v > intensity {
					intensity = v
				}
			}
			// Wispy positional noise.
			noise := (shapeHash(r, c) - 0.5) * 0.18
			intensity += noise
			if intensity < 0 {
				intensity = 0
			}
			if intensity > 1 {
				intensity = 1
			}
			s[r][c] = intensity
		}
	}
	return s
}

// venusCloudShape creates swirling cloud bands — used as an Overlay
// with a warm cream color to produce Venus's thick haze.
func venusCloudShape() Shape {
	const bodyR = 0.47
	var s Shape
	for r := 0; r < ShapeRows; r++ {
		for c := 0; c < ShapeCols; c++ {
			x := (float64(c) + 0.5) / float64(ShapeCols)
			y := (float64(r) + 0.5) / float64(ShapeRows)
			bdx := (x - 0.5) / bodyR
			bdy := (y - 0.5) / bodyR
			if bdx*bdx+bdy*bdy >= 1 {
				continue
			}
			// Two overlapping sine fields give a swirling look.
			v := 0.30 * (math.Sin(11*x+7*y+1.2) + math.Sin(17*x-6*y+3.3))
			v = 0.5 + v*0.3
			// Soft fade toward the silhouette edge.
			rad := math.Sqrt(bdx*bdx + bdy*bdy)
			if rad > 0.80 {
				v *= (1.0 - rad) / 0.20
			}
			if v < 0 {
				v = 0
			}
			if v > 0.85 {
				v = 0.85
			}
			s[r][c] = v
		}
	}
	return s
}

// jupiterRedSpotShape returns a small elliptical cloud for the Great
// Red Spot — used as an Overlay with a muted red color on top of
// Jupiter's banded base.
func jupiterRedSpotShape() Shape {
	const (
		cx = 0.62
		cy = 0.62
		rx = 0.08
		ry = 0.038
	)
	var s Shape
	for r := 0; r < ShapeRows; r++ {
		for c := 0; c < ShapeCols; c++ {
			x := (float64(c) + 0.5) / float64(ShapeCols)
			y := (float64(r) + 0.5) / float64(ShapeRows)
			dx := (x - cx) / rx
			dy := (y - cy) / ry
			d2 := dx*dx + dy*dy
			if d2 >= 1 {
				continue
			}
			s[r][c] = 0.88 * math.Exp(-d2*2.0)
		}
	}
	return s
}

// shapeHash is a cheap deterministic float in [0,1) keyed on (r, c).
// Reused by both shapes.go and overlay/render.go so the noise patterns
// stay stable across frames.
func shapeHash(r, c int) float64 {
	h := uint32(r)*73856093 ^ uint32(c)*19349663
	h ^= h >> 13
	h *= 0x5bd1e995
	h ^= h >> 15
	return float64(h%10000) / 10000.0
}

var (
	mercurySimpleSphere = sphereShape(0.5, 0.5, 0.46, 0.46)
	venusUniformHaze    = sphereShape(0.5, 0.5, 0.47, 0.47)
	earthBaseSphere     = sphereShape(0.5, 0.5, 0.45, 0.45)
	marsPolarCap        = applyPolarCap(sphereShape(0.5, 0.5, 0.43, 0.43), 10, 1.25)
	jupiterBands        = applyJupiterBands(sphereShape(0.5, 0.5, 0.47, 0.47))
	saturnRings         = applyRings(sphereShape(0.5, 0.5, 0.26, 0.26), 0.26)
	uranusSmallSphere   = sphereShape(0.5, 0.5, 0.40, 0.40)
	neptuneDenseCore    = sphereShape(0.5, 0.5, 0.40, 0.40)
)

var shapes = map[string]Shape{
	"mercury": mercurySimpleSphere,
	"venus":   venusUniformHaze,
	"earth":   earthBaseSphere,
	"mars":    marsPolarCap,
	"jupiter": jupiterBands,
	"saturn":  saturnRings,
	"uranus":  uranusSmallSphere,
	"neptune": neptuneDenseCore,
}

// GetShape returns the base shade grid for the named planet. Unknown
// names fall back to the mercury sphere so custom instance names still
// render.
func GetShape(name string) Shape {
	if s, ok := shapes[name]; ok {
		return s
	}
	return mercurySimpleSphere
}

// planetOverlays holds per-planet surface features that color-blend
// onto the base terrain during rendering. Earth gets white clouds,
// Venus gets warm haze, Jupiter gets a red spot.
var planetOverlays = map[string][]Overlay{
	"earth":   {{Shape: earthCloudShape(), Color: [3]uint8{250, 250, 250}}},
	"venus":   {{Shape: venusCloudShape(), Color: [3]uint8{255, 240, 200}}},
	"jupiter": {{Shape: jupiterRedSpotShape(), Color: [3]uint8{175, 55, 35}}},
}

// GetOverlays returns the per-planet color overlays, or nil if the
// planet has none.
func GetOverlays(name string) []Overlay {
	return planetOverlays[name]
}
