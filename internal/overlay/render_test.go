package overlay

import (
	"strings"
	"testing"

	"github.com/elliottregan/cspace/internal/planets"
)

func TestLerpColorEndpoints(t *testing.T) {
	from := [3]uint8{0, 0, 0}
	to := [3]uint8{100, 200, 50}

	if got := LerpColor(from, to, 0); got != from {
		t.Errorf("t=0: got %v, want %v", got, from)
	}
	if got := LerpColor(from, to, 1); got != to {
		t.Errorf("t=1: got %v, want %v", got, to)
	}
	got := LerpColor(from, to, 0.5)
	if got != [3]uint8{50, 100, 25} {
		t.Errorf("t=0.5: got %v, want [50 100 25]", got)
	}
}

func TestLerpColorReverseDirection(t *testing.T) {
	// Lerping from a lighter to a darker channel must not underflow uint8.
	from := [3]uint8{200, 200, 200}
	to := [3]uint8{50, 50, 50}
	got := LerpColor(from, to, 0.5)
	if got[0] != 125 || got[1] != 125 || got[2] != 125 {
		t.Errorf("got %v, want [125 125 125]", got)
	}
}

func TestScaleColor(t *testing.T) {
	rgb := [3]uint8{200, 100, 50}
	if got := scaleColor(rgb, 0); got != ([3]uint8{0, 0, 0}) {
		t.Errorf("shade 0: got %v, want black", got)
	}
	if got := scaleColor(rgb, 1); got != rgb {
		t.Errorf("shade 1: got %v, want %v", got, rgb)
	}
	got := scaleColor(rgb, 0.5)
	if got != ([3]uint8{100, 50, 25}) {
		t.Errorf("shade 0.5: got %v, want [100 50 25]", got)
	}
}

func TestRenderPlanetDimensions(t *testing.T) {
	p := planets.MustGet("mercury")
	shape := planets.GetShape("mercury")
	out := RenderPlanet(shape, p, 14, 14)
	lines := strings.Split(out, "\n")
	want := planets.ShapeRows / 2
	if len(lines) != want {
		t.Errorf("got %d lines, want %d (ShapeRows/2)", len(lines), want)
	}
}

func TestRenderPlanetBlurryAtPhaseOne(t *testing.T) {
	// At phase 1 the shape is maximally blurred, so early and late frames
	// must differ visibly.
	p := planets.MustGet("mercury")
	shape := planets.GetShape("mercury")
	early := RenderPlanet(shape, p, 1, 14)
	late := RenderPlanet(shape, p, 14, 14)
	if early == late {
		t.Error("expected render to differ between phase 1 (blurry) and phase 14 (sharp)")
	}
}

func TestRenderPlanetContainsAnsiColor(t *testing.T) {
	p := planets.MustGet("mercury")
	shape := planets.GetShape("mercury")
	out := RenderPlanet(shape, p, 14, 14)
	if !strings.Contains(out, "\x1b[38;2;") {
		t.Error("expected truecolor foreground ANSI escape in output")
	}
	if !strings.Contains(out, "\x1b[0m") {
		t.Error("expected ANSI reset in output")
	}
}

func TestRenderPlanetUsesHalfBlocks(t *testing.T) {
	p := planets.MustGet("mercury")
	shape := planets.GetShape("mercury")
	out := RenderPlanet(shape, p, 14, 14)
	if !strings.Contains(out, "▀") && !strings.Contains(out, "▄") {
		t.Error("expected at least one half-block character (▀ or ▄) in output")
	}
}
