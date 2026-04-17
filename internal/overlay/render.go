// Package overlay renders the bubbletea provisioning overlay — a planet
// "coming into focus" through six palette stages while color lerps from
// grey to the planet's canonical RGB.
package overlay

import (
	"fmt"
	"strings"

	"github.com/elliottregan/cspace/internal/planets"
)

// greyStart is the dim grey color the focus-pull begins with before
// interpolating toward the planet's canonical color as phases advance.
var greyStart = [3]uint8{85, 85, 85}

// palettes[phase-1] is the lookup table used during phase N (1..6).
// Index 0 is always the space rendered for shade 0; subsequent indices
// map non-zero shade values (split evenly across [0,1]) to progressively
// denser characters. See the issue for the "block → half-block → braille"
// progression.
var palettes = [6][]string{
	{" ", "█"},
	{" ", "▓", "█"},
	{" ", "▒", "▓", "█"},
	{" ", "░", "▒", "▓", "█"},
	{" ", "░", "▒", "▓", "▌", "█"},
	{" ", "⠂", "░", "▒", "▓", "▌", "█"},
}

// LerpColor linearly interpolates each channel of from→to by t∈[0,1].
// Values outside [0,1] are clamped. Channel arithmetic uses float64 to
// avoid uint8 underflow when a channel shrinks (e.g. 200 → 50).
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

// ShadeToChar maps a shade in [0,1] to a single character chosen from the
// palette for the given phase (1..6). Phases outside the range are clamped.
// Shade <= 0 always maps to " ".
func ShadeToChar(shade float64, phase int) string {
	if shade <= 0 {
		return " "
	}
	if phase < 1 {
		phase = 1
	}
	if phase > 6 {
		phase = 6
	}
	pal := palettes[phase-1]
	// Skip the leading " " for non-zero shades so a low but positive shade
	// always renders a visible glyph.
	nonBlank := pal[1:]
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

// RenderPlanet returns the colored ASCII art for one frame of the focus-pull
// animation. Color lerps from grey to planet.Color by (phase/total) and the
// palette densifies as phase grows. The return value is a 6-line string with
// each non-empty cell wrapped in truecolor ANSI and a trailing reset.
func RenderPlanet(shape planets.Shape, p planets.Planet, phase, total int) string {
	if total <= 0 {
		total = 1
	}
	t := float64(phase) / float64(total)
	rgb := LerpColor(greyStart, p.Color, t)
	color := ansiColor(rgb)

	var rows []string
	for _, row := range shape {
		var line strings.Builder
		for _, shade := range row {
			ch := ShadeToChar(shade, phaseStage(phase, total))
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

// phaseStage maps the provisioning phase index (1..total) to a 1..6 palette
// stage. With 14 provisioning phases, each palette stage covers ~2.3 phases.
// Phase 0 is treated as stage 1.
func phaseStage(phase, total int) int {
	if phase < 1 {
		return 1
	}
	if total < 1 {
		total = 1
	}
	stage := 1 + (phase-1)*6/total
	if stage < 1 {
		stage = 1
	}
	if stage > 6 {
		stage = 6
	}
	return stage
}
