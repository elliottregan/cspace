// Package overlay renders the bubbletea provisioning overlay — a planet
// "coming into focus" as provisioning advances. The focus-pull effect uses
// two axes: (1) the shape is blurred heavily at phase 1 and iteratively
// sharpened toward the target image, and (2) the color lerps from a dim
// grey toward the planet's canonical RGB. The character palette is a
// static 4-step density ramp (no half-blocks, no braille — those looked
// like noise rather than detail).
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

// palette maps shade magnitude to a block-shading character. Index 0 is
// always space for shade == 0; subsequent entries span [0, 1] evenly.
// Intentionally short — extra half-block or braille characters produced
// visual noise rather than added detail.
var palette = []string{" ", "░", "▒", "▓", "█"}

// haloThreshold suppresses dim blur tails below this shade value so the
// halo stays bounded rather than flood-filling the frame at early phases.
const haloThreshold = 0.08

// maxBlurIters caps the iterative 3x3 box-blur passes applied to the
// target shape at phase 1 (the most defocused frame). 0 iterations at
// phase == total gives the sharp target image.
const maxBlurIters = 6

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

// ShadeToChar maps a shade in [0,1] to a single character from the density
// palette. Shade <= 0 and shade below haloThreshold both render as space
// so blur halos taper cleanly to empty rather than fringing with ░.
func ShadeToChar(shade float64) string {
	if shade < haloThreshold {
		return " "
	}
	nonBlank := palette[1:]
	idx := int(shade * float64(len(nonBlank)))
	if idx >= len(nonBlank) {
		idx = len(nonBlank) - 1
	}
	return nonBlank[idx]
}

// ansiColor returns the 24-bit foreground SGR escape for rgb.
func ansiColor(rgb [3]uint8) string {
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", rgb[0], rgb[1], rgb[2])
}

// ansiReset restores default SGR.
const ansiReset = "\x1b[0m"

// RenderPlanet returns one frame of the focus-pull animation. Blur iterations
// decrease with phase (target shape sharpens); color lerps from grey toward
// planet.Color. The output is a multi-line string with each non-space cell
// wrapped in truecolor ANSI + reset.
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

	rgb := LerpColor(greyStart, p.Color, focus)
	color := ansiColor(rgb)

	var rows []string
	for _, row := range frame {
		var line strings.Builder
		for _, shade := range row {
			ch := ShadeToChar(shade)
			if ch == " " {
				line.WriteByte(' ')
				continue
			}
			line.WriteString(color)
			line.WriteString(ch)
			line.WriteString(ansiReset)
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
