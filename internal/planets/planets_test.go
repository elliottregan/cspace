package planets

import "testing"

func TestGetMercury(t *testing.T) {
	p, ok := Get("mercury")
	if !ok {
		t.Fatal("expected mercury to be present")
	}
	if p.Symbol != "☿" {
		t.Errorf("symbol: got %q, want ☿", p.Symbol)
	}
	if p.Color != [3]uint8{169, 169, 169} {
		t.Errorf("color: got %v, want [169 169 169]", p.Color)
	}
}

func TestGetUnknown(t *testing.T) {
	if _, ok := Get("pluto"); ok {
		t.Error("expected pluto to be absent")
	}
}

func TestMustGetFallback(t *testing.T) {
	p := MustGet("pluto")
	if p.Symbol == "" {
		t.Error("expected non-empty fallback symbol")
	}
}

func TestAllPlanetsPresent(t *testing.T) {
	want := []string{"mercury", "venus", "earth", "mars", "jupiter", "saturn", "uranus", "neptune"}
	for _, name := range want {
		if _, ok := Get(name); !ok {
			t.Errorf("missing planet %q", name)
		}
	}
}
