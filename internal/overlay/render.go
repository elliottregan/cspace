// Package overlay renders the bubbletea provisioning overlay — a planet
// "coming into focus" as provisioning advances. The focus-pull effect uses
// two axes: (1) the shape is blurred heavily at phase 1 and iteratively
// sharpened toward the target image, and (2) the color lerps from a dim
// grey toward the planet's canonical RGB.
//
// Each output cell uses the ▀/▄ half-block character with independent
// foreground and background truecolors, so every terminal cell encodes
// TWO vertically stacked sub-pixels — doubling effective vertical
// resolution. Per-subpixel color is derived from its shade value via
// truecolor scaling, so gradients are smooth (no density-palette banding).
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

// haloThreshold suppresses dim blur tails below this shade value so the
// halo stays bounded rather than flood-filling the frame at early phases.
const haloThreshold = 0.08

// maxBlurIters caps the iterative 3x3 box-blur passes applied to the
// target shape at phase 1 (the most defocused frame). 0 iterations at
// phase == total gives the sharp target image. Scales roughly with grid
// size — doubled from 6 when the shape grid grew from 24² to 48².
const maxBlurIters = 12

// viewportBg is the panel-interior background every planet cell paints,
// so the blur tails blend into the enclosing black viewport regardless
// of the user's terminal theme.
var viewportBg = [3]uint8{0, 0, 0}

// upperHalf and lowerHalf are the Unicode half-block characters we use
// to pack two vertical sub-pixels into one terminal cell. upperHalf
// shows fg on top / bg on bottom; lowerHalf is its mirror.
const (
	upperHalf = "▀"
	lowerHalf = "▄"
)

// LerpColor linearly interpolates each channel of from→to by t∈[0,1].
// Values outside [0,1] are clamped. Arithmetic uses float64 to avoid
// uint8 underflow when a channel shrinks (e.g. 200 → 50).
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

// scaleColor multiplies each channel of rgb by shade ∈ [0,1], producing a
// darker version of rgb suitable for the unlit portion of a cell.
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

// RenderPlanet returns one frame of the focus-pull animation. Blur
// iterations decrease with phase (target shape sharpens); color lerps from
// grey toward planet.Color. Output height is planets.ShapeRows/2 terminal
// rows because each output row represents two stacked sub-pixels.
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

	iters := int(math.Round(float64(maxBlurIters) * (1 - focus)))
	frame := blurShape(shape, iters)

	baseColor := LerpColor(greyStart, p.Color, focus)

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
				topColor := scaleColor(baseColor, top)
				botColor := scaleColor(baseColor, bot)
				line.WriteString(fgBgAnsi(topColor, botColor))
				line.WriteString(upperHalf)
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

// blurShape applies `iters` passes of a 3x3 box blur to s. Each pass smooths
// edges and spreads shade outward; iterating many passes approximates a
// Gaussian with sigma ≈ √iters. Out-of-bounds neighbors are treated as 0
// so the halo fades to black at the frame edges.
func blurShape(s planets.Shape, iters int) planets.Shape {
	for i := 0; i < iters; i++ {
		s = boxBlur3x3(s)
	}
	return s
}

func boxBlur3x3(s planets.Shape) planets.Shape {
	var out planets.Shape
	rows := planets.ShapeRows
	cols := planets.ShapeCols
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			var sum float64
			for dr := -1; dr <= 1; dr++ {
				for dc := -1; dc <= 1; dc++ {
					rr, cc := r+dr, c+dc
					if rr < 0 || rr >= rows || cc < 0 || cc >= cols {
						continue
					}
					sum += s[rr][cc]
				}
			}
			out[r][c] = sum / 9
		}
	}
	return out
}
