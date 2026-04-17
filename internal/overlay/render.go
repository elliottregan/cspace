// Package overlay renders the bubbletea provisioning overlay — a planet
// "coming into focus" as provisioning advances.
//
// Focus-pull uses three axes:
//   - Pixelation: at phase 1 the shape is sampled at a coarse block size
//     so the planet reads as a chunky mosaic of solid-color squares;
//     block size drops linearly to 1 at phase=total.
//   - Color saturation: base color lerps from dim grey toward the planet's
//     canonical RGB as phase advances.
//   - Surface texture: past 70% focus, a braille-dither overlay stipples
//     subtle shadow/highlight variation across lit cells, ramping up with
//     focus. Contrast is intentionally small — just enough to suggest
//     surface detail without looking like random noise.
//
// Every cell paints explicit black bg so the planet always sits inside a
// consistent black viewport regardless of terminal theme.
package overlay

import (
	"fmt"
	"math"
	"strings"

	"github.com/elliottregan/cspace/internal/planets"
)

// greyStart is the washed-out grey the focus-pull begins with before
// interpolating toward the planet's canonical color as phases advance.
var greyStart = [3]uint8{110, 110, 110}

// haloThreshold suppresses dim tails below this shade so the silhouette
// collapses cleanly to black rather than fringing.
const haloThreshold = 0.08

// maxBlockSize is the pixelation block size used at phase 1 — big solid
// squares that shrink toward 1 as focus approaches 1. 8 means at phase 1
// the planet is effectively rendered at ShapeRows/8 × ShapeCols/8 resolution.
const maxBlockSize = 8

// textureStart is the focus value at which braille dither kicks in.
// Below this focus we render only half-block shapes; above, a growing
// fraction of lit cells are painted with a braille character to suggest
// surface texture.
const textureStart = 0.70

// textureDensity is the fraction of cells that receive braille treatment
// when focus hits 1.0. Below, the fraction scales linearly from 0.
const textureDensity = 0.28

// textureContrast is the fg/bg shade differential used by braille cells.
// Small (fg slightly brighter than bg), so the texture reads as surface
// detail rather than discrete dots.
const textureContrast = 0.12

// viewportBg is the panel-interior background every cell paints.
var viewportBg = [3]uint8{0, 0, 0}

// upperHalf / lowerHalf / fullBlock pack two stacked sub-pixels per cell.
const (
	upperHalf = "▀"
	lowerHalf = "▄"
)

// LerpColor linearly interpolates each channel of from→to by t∈[0,1].
// Values outside [0,1] are clamped.
func LerpColor(from, to [3]uint8, t float64) [3]uint8 {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	lerp := func(a, b uint8) uint8 {
		v := float64(a) + t*(float64(b)-float64(a))
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return uint8(v)
	}
	return [3]uint8{
		lerp(from[0], to[0]),
		lerp(from[1], to[1]),
		lerp(from[2], to[2]),
	}
}

// scaleColor multiplies each channel of rgb by shade ∈ [0,1].
func scaleColor(rgb [3]uint8, shade float64) [3]uint8 {
	if shade <= 0 {
		return [3]uint8{0, 0, 0}
	}
	if shade >= 1 {
		return rgb
	}
	return [3]uint8{
		uint8(float64(rgb[0]) * shade),
		uint8(float64(rgb[1]) * shade),
		uint8(float64(rgb[2]) * shade),
	}
}

const ansiReset = "\x1b[0m"

func fgBgAnsi(fg, bg [3]uint8) string {
	return fmt.Sprintf("\x1b[38;2;%d;%d;%d;48;2;%d;%d;%dm",
		fg[0], fg[1], fg[2], bg[0], bg[1], bg[2])
}

func bgAnsi(bg [3]uint8) string {
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", bg[0], bg[1], bg[2])
}

// hash2 is a cheap deterministic float in [0,1) keyed on (r, c). Used for
// per-cell texture decisions so the stipple pattern stays stable across
// frames instead of twinkling.
func hash2(r, c int) float64 {
	h := uint32(r)*73856093 ^ uint32(c)*19349663
	h ^= h >> 13
	h *= 0x5bd1e995
	h ^= h >> 15
	return float64(h%10000) / 10000.0
}

// pickBraille chooses a braille character (U+2800..U+28FF) whose dot
// pattern is a stable function of (r, c). Dot count is biased by the
// per-cell noise so textured regions vary visibly without clumping.
func pickBraille(r, c int) string {
	h := hash2(r, c)
	// 2..6 dots keeps texture visible without turning cells into solid
	// blocks (which would defeat the purpose).
	dots := 2 + int(h*5)
	mask := 0
	// Use every other bit position so the resulting braille pattern
	// spreads across both columns of the 2x4 dot grid.
	for i := 0; i < dots; i++ {
		mask |= 1 << ((i * 3) % 8)
	}
	return string(rune(0x2800 + mask))
}

// RenderPlanet returns one frame of the focus-pull animation.
func RenderPlanet(shape planets.Shape, p planets.Planet, phase, total int) string {
	if total <= 0 {
		total = 1
	}
	focus := float64(phase) / float64(total)
	if focus < 0 {
		focus = 0
	}
	if focus > 1 {
		focus = 1
	}

	// Pixelation: chunky at phase 1, pixel-perfect at phase total.
	blockSize := int(math.Round(float64(maxBlockSize) * (1 - focus)))
	if blockSize < 1 {
		blockSize = 1
	}
	frame := pixelateShape(shape, blockSize)

	baseColor := LerpColor(greyStart, p.Color, focus)

	// Texture ramp: 0 until textureStart, then linearly up to textureDensity.
	var textureFrac float64
	if focus > textureStart {
		textureFrac = (focus - textureStart) / (1 - textureStart) * textureDensity
	}

	termRows := planets.ShapeRows / 2
	var rows []string
	for r := 0; r < termRows; r++ {
		var line strings.Builder
		for c := 0; c < planets.ShapeCols; c++ {
			top := frame[r*2][c]
			bot := frame[r*2+1][c]
			topOn := top >= haloThreshold
			botOn := bot >= haloThreshold

			switch {
			case !topOn && !botOn:
				line.WriteString(bgAnsi(viewportBg))
				line.WriteByte(' ')
				line.WriteString(ansiReset)
			case topOn && botOn:
				// Braille texture overlay on fully-lit cells past textureStart.
				if textureFrac > 0 && hash2(r, c) < textureFrac {
					mid := (top + bot) * 0.5
					bg := scaleColor(baseColor, mid)
					fg := scaleColor(baseColor, math.Min(1, mid+textureContrast))
					line.WriteString(fgBgAnsi(fg, bg))
					line.WriteString(pickBraille(r, c))
				} else {
					topColor := scaleColor(baseColor, top)
					botColor := scaleColor(baseColor, bot)
					line.WriteString(fgBgAnsi(topColor, botColor))
					line.WriteString(upperHalf)
				}
				line.WriteString(ansiReset)
			case topOn:
				topColor := scaleColor(baseColor, top)
				line.WriteString(fgBgAnsi(topColor, viewportBg))
				line.WriteString(upperHalf)
				line.WriteString(ansiReset)
			case botOn:
				botColor := scaleColor(baseColor, bot)
				line.WriteString(fgBgAnsi(botColor, viewportBg))
				line.WriteString(lowerHalf)
				line.WriteString(ansiReset)
			}
		}
		rows = append(rows, line.String())
	}
	return strings.Join(rows, "\n")
}

// pixelateShape returns a new shape where every blockSize×blockSize region
// of the input is flattened to the region's mean shade. blockSize <= 1
// returns the input unchanged.
func pixelateShape(s planets.Shape, blockSize int) planets.Shape {
	if blockSize <= 1 {
		return s
	}
	var out planets.Shape
	rows := planets.ShapeRows
	cols := planets.ShapeCols
	for blockR := 0; blockR < rows; blockR += blockSize {
		for blockC := 0; blockC < cols; blockC += blockSize {
			var sum float64
			var count int
			for r := blockR; r < blockR+blockSize && r < rows; r++ {
				for c := blockC; c < blockC+blockSize && c < cols; c++ {
					sum += s[r][c]
					count++
				}
			}
			mean := 0.0
			if count > 0 {
				mean = sum / float64(count)
			}
			for r := blockR; r < blockR+blockSize && r < rows; r++ {
				for c := blockC; c < blockC+blockSize && c < cols; c++ {
					out[r][c] = mean
				}
			}
		}
	}
	return out
}
