// Package overlay renders the bubbletea provisioning overlay — a planet
// "coming into focus" as provisioning advances.
//
// Focus-pull uses three axes:
//   - Pixelation: at phase 1 the shape is sampled at a coarse block size
//     so the planet reads as a chunky mosaic of solid-color squares;
//     block size drops linearly to 1 at phase=total.
//   - Color saturation: base color lerps from dim grey toward the planet's
//     canonical RGB as phase advances.
//   - Surface texture: past TextureStart focus, a braille-dither overlay
//     stipples subtle shading across lit cells to suggest surface detail.
//     Dots are slightly DARKER than the cell they sit in so the texture
//     reads as shadow rather than glint.
//
// Planets may carry color Overlays (clouds, Great Red Spot, etc.) whose
// per-cell shade controls how much the base color blends toward the
// overlay's color. Overlays are pixelated in lockstep with the base
// shape so they resolve with the planet during the focus pull.
package overlay

import (
	"fmt"
	"math"
	"strings"

	"github.com/elliottregan/cspace/internal/planets"
)

// Default tuning constants. Exposed as variables through
// DefaultRenderOptions so the preview tool can override per-request.
var greyStart = [3]uint8{110, 110, 110}

const (
	haloThreshold   = 0.08
	maxBlockSize    = 8
	textureStart    = 0.85 // kick in only near the end of the pull
	textureDensity  = 0.30
	textureContrast = 0.09 // small — dots sit just slightly below base
)

// viewportBg is the panel-interior background every cell paints.
var viewportBg = [3]uint8{0, 0, 0}

// upperHalf / lowerHalf pack two stacked sub-pixels per cell.
const (
	upperHalf = "▀"
	lowerHalf = "▄"
)

const ansiReset = "\x1b[0m"

// brailleTextures is a curated pool of braille patterns with 2–5 dots
// scattered across the 2×4 cell grid. Picking from a varied pool gives
// the surface a stippled texture rather than repeating rows of the
// same two dots.
var brailleTextures = []string{
	"⠃", "⠅", "⠆", "⠉", "⠊", "⠌", "⠑", "⠒",
	"⠔", "⠘", "⠙", "⠚", "⠜", "⡀", "⡁", "⡂",
	"⡃", "⡉", "⡐", "⡘", "⢁", "⢂", "⢄", "⢈",
	"⢉", "⢐", "⢘", "⣀", "⣁", "⣂", "⣄", "⣈",
	"⣐", "⣒", "⣡", "⣢", "⣤", "⣨", "⣪", "⣰",
}

// RenderOptions lets callers tweak image parameters per-render without
// editing constants and rebuilding.
type RenderOptions struct {
	MaxBlockSize    int
	HaloThreshold   float64
	TextureStart    float64
	TextureDensity  float64
	TextureContrast float64
	GreyStart       [3]uint8
	Overlays        []planets.Overlay
}

// DefaultRenderOptions returns the package defaults used by RenderPlanet.
func DefaultRenderOptions() RenderOptions {
	return RenderOptions{
		MaxBlockSize:    maxBlockSize,
		HaloThreshold:   haloThreshold,
		TextureStart:    textureStart,
		TextureDensity:  textureDensity,
		TextureContrast: textureContrast,
		GreyStart:       greyStart,
	}
}

// LerpColor linearly interpolates each channel of from→to by t∈[0,1].
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

func fgBgAnsi(fg, bg [3]uint8) string {
	return fmt.Sprintf("\x1b[38;2;%d;%d;%d;48;2;%d;%d;%dm",
		fg[0], fg[1], fg[2], bg[0], bg[1], bg[2])
}

func bgAnsi(bg [3]uint8) string {
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", bg[0], bg[1], bg[2])
}

// hash2 is a deterministic per-cell float used for both braille
// character selection and texture placement.
func hash2(r, c int) float64 {
	h := uint32(r)*73856093 ^ uint32(c)*19349663
	h ^= h >> 13
	h *= 0x5bd1e995
	h ^= h >> 15
	return float64(h%10000) / 10000.0
}

func pickBraille(r, c int) string {
	idx := int(hash2(r, c) * float64(len(brailleTextures)))
	if idx >= len(brailleTextures) {
		idx = len(brailleTextures) - 1
	}
	return brailleTextures[idx]
}

// RenderPlanet returns one frame of the focus-pull animation using the
// package-default RenderOptions.
func RenderPlanet(shape planets.Shape, p planets.Planet, phase, total int) string {
	return RenderPlanetWith(shape, p, phase, total, DefaultRenderOptions())
}

// RenderPlanetWith is RenderPlanet with tweakable options.
func RenderPlanetWith(shape planets.Shape, p planets.Planet, phase, total int, opts RenderOptions) string {
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

	blockSize := int(math.Round(float64(opts.MaxBlockSize) * (1 - focus)))
	if blockSize < 1 {
		blockSize = 1
	}
	frame := pixelateShape(shape, blockSize)

	// Pixelate overlay shapes in lockstep so they resolve with the base.
	overlayFrames := make([]planets.Shape, len(opts.Overlays))
	for i, ov := range opts.Overlays {
		overlayFrames[i] = pixelateShape(ov.Shape, blockSize)
	}

	baseColor := LerpColor(opts.GreyStart, p.Color, focus)

	var textureFrac float64
	if focus > opts.TextureStart && opts.TextureStart < 1 {
		textureFrac = (focus - opts.TextureStart) / (1 - opts.TextureStart) * opts.TextureDensity
	}

	// blendSub takes a base shade and the sub-pixel's (row, col) in the
	// shape grid, returns the final cell color with overlay blending
	// applied.
	blendSub := func(shade float64, row, col int) [3]uint8 {
		col0 := scaleColor(baseColor, shade)
		for i, ov := range opts.Overlays {
			ovShade := overlayFrames[i][row][col]
			if ovShade > 0 {
				col0 = LerpColor(col0, ov.Color, ovShade)
			}
		}
		return col0
	}

	termRows := planets.ShapeRows / 2
	var rows []string
	for r := 0; r < termRows; r++ {
		var line strings.Builder
		for c := 0; c < planets.ShapeCols; c++ {
			topRow, botRow := r*2, r*2+1
			top := frame[topRow][c]
			bot := frame[botRow][c]
			topOn := top >= opts.HaloThreshold
			botOn := bot >= opts.HaloThreshold

			switch {
			case !topOn && !botOn:
				line.WriteString(bgAnsi(viewportBg))
				line.WriteByte(' ')
				line.WriteString(ansiReset)

			case topOn && botOn:
				if textureFrac > 0 && hash2(r, c) < textureFrac {
					// Braille dither: fg slightly darker than bg, both
					// sharing the same (overlay-blended) base color.
					mid := (top + bot) * 0.5
					bg := blendSub(mid, topRow, c)
					// Blend the fg's darker shade with the SAME overlay
					// shade as the bg so only lightness differs.
					fgShade := mid - opts.TextureContrast
					if fgShade < 0 {
						fgShade = 0
					}
					fg := blendSub(fgShade, topRow, c)
					line.WriteString(fgBgAnsi(fg, bg))
					line.WriteString(pickBraille(r, c))
				} else {
					topColor := blendSub(top, topRow, c)
					botColor := blendSub(bot, botRow, c)
					line.WriteString(fgBgAnsi(topColor, botColor))
					line.WriteString(upperHalf)
				}
				line.WriteString(ansiReset)

			case topOn:
				topColor := blendSub(top, topRow, c)
				line.WriteString(fgBgAnsi(topColor, viewportBg))
				line.WriteString(upperHalf)
				line.WriteString(ansiReset)

			case botOn:
				botColor := blendSub(bot, botRow, c)
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
// of the input is flattened to the region's mean shade.
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
