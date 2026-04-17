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

func TestShadeToCharZero(t *testing.T) {
	for phase := 1; phase <= 6; phase++ {
		if got := ShadeToChar(0, phase); got != " " {
			t.Errorf("phase %d shade 0: got %q, want \" \"", phase, got)
		}
	}
}

func TestShadeToCharNonZero(t *testing.T) {
	for phase := 1; phase <= 6; phase++ {
		if got := ShadeToChar(1.0, phase); got == " " {
			t.Errorf("phase %d shade 1.0: got space, want non-space", phase)
		}
	}
}

func TestShadeToCharPhase1Binary(t *testing.T) {
	// Phase 1 palette has only "█" for non-zero shade.
	for _, v := range []float64{0.1, 0.5, 0.9, 1.0} {
		if got := ShadeToChar(v, 1); got != "█" {
			t.Errorf("phase 1 shade %.1f: got %q, want \"█\"", v, got)
		}
	}
}

func TestRenderPlanetDimensions(t *testing.T) {
	p := planets.MustGet("mercury")
	shape := planets.GetShape("mercury")
	out := RenderPlanet(shape, p, 1, 14)
	lines := strings.Split(out, "\n")
	if len(lines) != 6 {
		t.Errorf("got %d lines, want 6", len(lines))
	}
}

func TestRenderPlanetChangesAcrossPhases(t *testing.T) {
	p := planets.MustGet("mercury")
	shape := planets.GetShape("mercury")
	early := RenderPlanet(shape, p, 1, 14)
	late := RenderPlanet(shape, p, 14, 14)
	if early == late {
		t.Error("expected render to differ between phase 1 and phase 14")
	}
}

func TestRenderPlanetContainsAnsiColor(t *testing.T) {
	p := planets.MustGet("mercury")
	shape := planets.GetShape("mercury")
	out := RenderPlanet(shape, p, 14, 14)
	if !strings.Contains(out, "\x1b[38;2;") {
		t.Error("expected truecolor ANSI escape in output")
	}
	if !strings.Contains(out, "\x1b[0m") {
		t.Error("expected ANSI reset in output")
	}
}
