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
// equator the sphere occludes the ring. Five concentric bands with
// a Cassini division and a thin C/B gap give the striped disk its
// natural layered look.
func applyRings(s Shape, bodyRx float64) Shape {
	const (
		ringRx = 0.48
		ringRy = 0.10
	)
	innerU := bodyRx/ringRx + 0.08
	bands := saturnRingBands(innerU)
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

// formationCenter defines one soft patch contributing to a wispy surface
// feature. Many overlapping centers produce clouds/craters/storm
// patterns depending on the color they're rendered in.
type formationCenter struct {
	cx, cy, rx, ry, intensity float64
}

// buildFormationShape applies the cloud/formation noise algorithm
// (additive gaussian patches + 3-octave sine wisps + per-cell speckle)
// to a list of formation centers, constrained to a disk of radius
// bodyR centered at (0.5, 0.5). Reusable across planets to produce
// coherent surface textures.
func buildFormationShape(formations []formationCenter, bodyR float64) Shape {
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
			for _, p := range formations {
				dx := (x - p.cx) / p.rx
				dy := (y - p.cy) / p.ry
				d2 := dx*dx + dy*dy
				if d2 >= 1.8 {
					continue
				}
				intensity += 0.55 * p.intensity * math.Exp(-d2*1.1)
			}

			wisp := 0.14 * math.Sin(8*x+4*y+0.7)
			wisp += 0.08 * math.Sin(16*x-6*y+2.3)
			wisp += 0.05 * math.Sin(24*x+11*y+4.1)
			if intensity > 0.04 {
				intensity += wisp * math.Min(1.0, intensity*1.8)
			}

			intensity += (shapeHash(r, c) - 0.5) * 0.10

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

// earthCloudShape — big wispy weather systems across tropical, mid-
// latitude and polar bands.
func earthCloudShape() Shape {
	return buildFormationShape([]formationCenter{
		{0.22, 0.40, 0.16, 0.06, 0.72},
		{0.36, 0.36, 0.15, 0.05, 0.82},
		{0.48, 0.42, 0.18, 0.06, 0.88},
		{0.60, 0.38, 0.17, 0.05, 0.80},
		{0.72, 0.44, 0.13, 0.07, 0.70},
		{0.28, 0.22, 0.13, 0.05, 0.70},
		{0.42, 0.18, 0.11, 0.04, 0.60},
		{0.58, 0.24, 0.16, 0.05, 0.72},
		{0.24, 0.64, 0.15, 0.06, 0.72},
		{0.40, 0.72, 0.19, 0.05, 0.82},
		{0.58, 0.68, 0.16, 0.06, 0.76},
		{0.72, 0.62, 0.12, 0.05, 0.62},
		{0.50, 0.12, 0.20, 0.04, 0.55},
		{0.50, 0.86, 0.20, 0.04, 0.55},
	}, 0.45)
}

// mercuryCratersShape — dense scattering of small crater-rim shadows
// across the disk. Rendered with a darker overlay so they read as the
// pockmark texture that dominates real Mercury's appearance.
func mercuryCratersShape() Shape {
	const bodyR = 0.46
	var formations []formationCenter
	for i := 0; i < 90; i++ {
		h1 := shapeHash(i*13+1, i*7+19)
		h2 := shapeHash(i*11+5, i*17+3)
		h3 := shapeHash(i*19+7, i*23+11)
		h4 := shapeHash(i*29+13, i*31+17)

		cx := 0.08 + h1*0.84
		cy := 0.08 + h2*0.84
		dx := (cx - 0.5) / bodyR
		dy := (cy - 0.5) / bodyR
		if dx*dx+dy*dy > 0.82 {
			continue
		}
		formations = append(formations, formationCenter{
			cx:        cx,
			cy:        cy,
			rx:        0.010 + h3*0.022, // smaller pockmarks
			ry:        0.008 + h4*0.018,
			intensity: 0.30 + h3*0.40,
		})
	}
	return buildFormationShape(formations, bodyR)
}

// mercuryRaysShape — bright impact crater centers with radial ray
// systems extending outward. This is the dominant visual feature of
// real Mercury (e.g. Hokusai, Kuiper craters): a small bright crater
// floor surrounded by long straight ejecta rays that span a significant
// fraction of the disk.
func mercuryRaysShape() Shape {
	const bodyR = 0.46

	// Bright rayed craters. Placed asymmetrically so they feel natural.
	centers := []struct {
		cx, cy        float64 // crater position in unit space
		radius        float64 // outer reach of the rays
		strength      float64 // peak brightness
		rayCount      int     // N-fold symmetric ray pattern
		phaseOffset   float64 // rotation of the ray pattern
	}{
		{0.50, 0.58, 0.32, 0.60, 14, 0.7},  // central, just below equator
		{0.66, 0.22, 0.22, 0.45, 11, 1.9},  // near north pole, ~1 o'clock
	}

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

			for _, k := range centers {
				dx := x - k.cx
				dy := y - k.cy
				dist := math.Sqrt(dx*dx + dy*dy)

				// Bright crater disc at the center.
				const craterR = 0.018
				if dist < craterR {
					t := 1 - dist/craterR
					v := k.strength * (0.4 + 0.6*t)
					if v > s[r][c] {
						s[r][c] = v
					}
					continue
				}

				if dist > k.radius {
					continue
				}

				// Rays: N-fold-symmetric cosine wave in angle, thresholded
				// so only the ray peaks show.
				angle := math.Atan2(dy, dx)
				ray := math.Cos(float64(k.rayCount)*angle + k.phaseOffset)
				if ray < 0.55 {
					continue
				}
				// Normalize ray amplitude.
				rayAmp := (ray - 0.55) / 0.45

				// Fade outward and taper at tips.
				outer := (dist - craterR) / (k.radius - craterR)
				if outer > 1 {
					outer = 1
				}
				distFade := 1 - outer
				widthFade := math.Pow(distFade, 0.6)

				v := rayAmp * widthFade * distFade * k.strength
				if v > s[r][c] {
					s[r][c] = v
				}
			}

			// Fade near disk edge so rays don't bleed off the silhouette.
			rad := math.Sqrt(bdx*bdx + bdy*bdy)
			if rad > 0.85 {
				s[r][c] *= (1 - rad) / 0.15
			}
		}
	}
	return s
}

// marsSurfaceShape — large dusty basins and storm streaks. Rendered
// with a dark rust color so regions look like deep Martian terrain
// (Hellas Planitia, Valles Marineris) against the lighter orange body.
func marsSurfaceShape() Shape {
	return buildFormationShape([]formationCenter{
		{0.28, 0.38, 0.13, 0.06, 0.70},
		{0.54, 0.46, 0.16, 0.05, 0.75}, // Valles-like east-west stripe
		{0.72, 0.32, 0.09, 0.05, 0.60},
		{0.20, 0.62, 0.10, 0.06, 0.62},
		{0.45, 0.68, 0.12, 0.06, 0.68},
		{0.66, 0.66, 0.08, 0.05, 0.55},
		{0.34, 0.52, 0.07, 0.04, 0.50},
		{0.58, 0.22, 0.08, 0.04, 0.55},
	}, 0.43)
}

// uranusHazeShape — soft wide haze formations for the otherwise
// featureless methane atmosphere. Rendered with a brighter cyan so it
// reads as high-altitude haze.
func uranusHazeShape() Shape {
	return buildFormationShape([]formationCenter{
		{0.28, 0.38, 0.24, 0.08, 0.62},
		{0.58, 0.52, 0.22, 0.09, 0.68},
		{0.44, 0.66, 0.20, 0.07, 0.55},
		{0.52, 0.28, 0.19, 0.06, 0.52},
		{0.72, 0.36, 0.14, 0.06, 0.48},
	}, 0.40)
}

// neptuneStormShape — compact bright storm systems (separate from the
// darker Great Dark Spot overlay).
func neptuneStormShape() Shape {
	return buildFormationShape([]formationCenter{
		{0.64, 0.54, 0.10, 0.04, 0.62},
		{0.52, 0.28, 0.08, 0.04, 0.55},
		{0.36, 0.66, 0.09, 0.04, 0.62},
		{0.68, 0.30, 0.07, 0.03, 0.48},
		{0.50, 0.52, 0.06, 0.03, 0.45},
	}, 0.40)
}

// saturnRingBands returns the five ring bands (C, C/B gap, B, Cassini,
// A, outer haze) in order of increasing u. Shared between the base
// shape builder and the overlay mask so band edges stay in sync.
func saturnRingBands(innerU float64) []struct{ inner, outer, shade float64 } {
	return []struct{ inner, outer, shade float64 }{
		{innerU, innerU + 0.09, 0.50},         // C ring — innermost, faint
		{innerU + 0.10, innerU + 0.11, 0.18},  // thin C/B gap
		{innerU + 0.12, innerU + 0.24, 0.88},  // B ring — brightest
		{innerU + 0.25, innerU + 0.28, 0.18},  // Cassini division
		{innerU + 0.29, innerU + 0.36, 0.78},  // A ring
		{innerU + 0.37, 0.97, 0.42},           // outer haze / F region
	}
}

// saturnRingOverlayMask returns a cell-level mask of where Saturn's
// bright ring bands are visible (front half over sphere, both halves
// outside sphere body). Gaps (C/B gap, Cassini division) are excluded
// so they keep the planet-body color and the band structure stays
// legible.
func saturnRingOverlayMask(bodyRx float64) Shape {
	const (
		ringRx = 0.48
		ringRy = 0.10
	)
	innerU := bodyRx/ringRx + 0.08
	// Bright bands only (skip the two gaps and the very outer faint).
	all := saturnRingBands(innerU)
	brightBands := []struct{ inner, outer float64 }{
		{all[0].inner, all[0].outer}, // C ring
		{all[2].inner, all[2].outer}, // B ring
		{all[4].inner, all[4].outer}, // A ring
		{all[5].inner, all[5].outer}, // outer haze
	}
	var s Shape
	for r := 0; r < ShapeRows; r++ {
		for c := 0; c < ShapeCols; c++ {
			y := (float64(r) + 0.5) / float64(ShapeRows)
			x := (float64(c) + 0.5) / float64(ShapeCols)
			dx := (x - 0.5) / ringRx
			dy := (y - 0.5) / ringRy
			u := math.Sqrt(dx*dx + dy*dy)

			onBand := false
			for _, b := range brightBands {
				if u >= b.inner && u <= b.outer {
					onBand = true
					break
				}
			}
			if !onBand {
				continue
			}

			// Respect sphere occlusion: ring above the sphere's equator
			// passes behind the body, so we hide it inside the sphere.
			frontSide := y > 0.5
			sDx := (x - 0.5) / bodyRx
			sDy := (y - 0.5) / bodyRx
			inSphere := sDx*sDx+sDy*sDy < 1
			if inSphere && !frontSide {
				continue
			}

			s[r][c] = 0.82
		}
	}
	return s
}

// saturnShadowMask returns a thin CURVED shadow that traces the ring's
// back-edge projected onto the sphere. Keeping the curve parallel to
// the ring ellipse makes the shadow feel connected to the ring rather
// than a horizontal painted-on stripe. Intensity is intentionally low
// so the shadow reads as a subtle graze.
func saturnShadowMask(bodyRx float64) Shape {
	var s Shape
	const (
		ringRx         = 0.48
		ringRy         = 0.10
		shadowOffset   = 0.008 // shadow sits just below ring's back edge
		bandHalfHeight = 0.018
		maxIntensity   = 0.45
	)
	for r := 0; r < ShapeRows; r++ {
		for c := 0; c < ShapeCols; c++ {
			y := (float64(r) + 0.5) / float64(ShapeRows)
			x := (float64(c) + 0.5) / float64(ShapeCols)

			// Only darken cells on the sphere body.
			sDx := (x - 0.5) / bodyRx
			sDy := (y - 0.5) / bodyRx
			if sDx*sDx+sDy*sDy >= 1 {
				continue
			}

			// Ring's back-edge curve at this x: the upper arc of the
			// tilted ring ellipse.
			normX := (x - 0.5) / ringRx
			if normX*normX >= 1 {
				continue
			}
			ringUpperY := 0.5 - ringRy*math.Sqrt(1-normX*normX)
			shadowY := ringUpperY + shadowOffset

			dist := math.Abs(y - shadowY)
			if dist > bandHalfHeight {
				continue
			}
			t := 1 - dist/bandHalfHeight
			s[r][c] = t * maxIntensity
		}
	}
	return s
}

// neptuneDarkSpotShape — a single compact darker formation representing
// Neptune's Great Dark Spot. Rendered as a darker-blue overlay on top
// of the lighter storm formations.
func neptuneDarkSpotShape() Shape {
	return buildFormationShape([]formationCenter{
		{0.32, 0.40, 0.09, 0.045, 0.85},
	}, 0.40)
}

// applySaturnBands dims the sphere shade by latitude to add the subtle
// horizontal banding real Saturn shows — much softer than Jupiter's.
// Multiplies the base shade by a modest factor within each band and
// smooths band edges so no visible seams appear.
func applySaturnBands(s Shape) Shape {
	type band struct{ minDy, maxDy, dim float64 }
	bands := []band{
		{-0.90, -0.55, 1.04}, // slightly brighter northern mid-lat
		{-0.20, 0.12, 0.93},  // slightly dimmer equatorial band
		{0.30, 0.65, 1.03},   // slightly brighter southern mid-lat
		{0.78, 0.95, 0.92},   // slightly dimmer far south
	}
	const bodyR = 0.26
	const edge = 0.05
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
			v := s[r][c] * shade
			if v > 1 {
				v = 1
			}
			s[r][c] = v
		}
	}
	return s
}

// saturnEquatorTint returns a subtle mask that concentrates at the
// equator, used to paint a warmer pink/salmon tint across the middle
// of the disk — the signature hue shift real Saturn shows at mid-
// latitudes.
func saturnEquatorTint(bodyRx float64) Shape {
	var s Shape
	const (
		centerY     = 0.50
		bandHalfHt  = 0.055
		maxBlend    = 0.35
	)
	for r := 0; r < ShapeRows; r++ {
		y := (float64(r) + 0.5) / float64(ShapeRows)
		dist := math.Abs(y - centerY)
		if dist > bandHalfHt {
			continue
		}
		t := 1 - dist/bandHalfHt
		for c := 0; c < ShapeCols; c++ {
			x := (float64(c) + 0.5) / float64(ShapeCols)
			sDx := (x - 0.5) / bodyRx
			sDy := (y - 0.5) / bodyRx
			if sDx*sDx+sDy*sDy >= 1 {
				continue
			}
			s[r][c] = t * maxBlend
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
	saturnRings         = applyRings(applySaturnBands(sphereShape(0.5, 0.5, 0.26, 0.26)), 0.26)
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
// onto the base terrain during rendering. Each overlay's shade at a
// given cell controls how much the cell's rendered color leans toward
// the overlay color. Order matters — earlier overlays are applied
// first, so later overlays can tint on top of darkened/shadowed cells.
var planetOverlays = map[string][]Overlay{
	"mercury": {
		{Shape: mercuryCratersShape(), Color: [3]uint8{95, 95, 95}},
		{Shape: mercuryRaysShape(), Color: [3]uint8{205, 205, 202}},
	},
	"venus": {{Shape: venusCloudShape(), Color: [3]uint8{255, 240, 200}}},
	"earth": {{Shape: earthCloudShape(), Color: [3]uint8{250, 250, 250}}},
	"mars":  {{Shape: marsSurfaceShape(), Color: [3]uint8{95, 30, 10}}},
	"jupiter": {
		{Shape: jupiterRedSpotShape(), Color: [3]uint8{170, 95, 50}},
	},
	"saturn": {
		{Shape: saturnEquatorTint(0.26), Color: [3]uint8{218, 172, 140}},
		{Shape: saturnShadowMask(0.26), Color: [3]uint8{12, 8, 4}},
		{Shape: saturnRingOverlayMask(0.26), Color: [3]uint8{218, 192, 155}},
	},
	"uranus": {{Shape: uranusHazeShape(), Color: [3]uint8{195, 245, 245}}},
	"neptune": {
		{Shape: neptuneDarkSpotShape(), Color: [3]uint8{20, 35, 90}},
		{Shape: neptuneStormShape(), Color: [3]uint8{110, 145, 230}},
	},
}

// GetOverlays returns the per-planet color overlays, or nil if the
// planet has none.
func GetOverlays(name string) []Overlay {
	return planetOverlays[name]
}
