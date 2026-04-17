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

func TestGetShapeDimensions(t *testing.T) {
	names := []string{"mercury", "venus", "earth", "mars", "jupiter", "saturn", "uranus", "neptune"}
	for _, name := range names {
		s := GetShape(name)
		if len(s) != ShapeRows {
			t.Errorf("%s: got %d rows, want %d", name, len(s), ShapeRows)
		}
		for r, row := range s {
			if len(row) != ShapeCols {
				t.Errorf("%s row %d: got %d cols, want %d", name, r, len(row), ShapeCols)
			}
		}
	}
}

func TestGetShapeUnknown(t *testing.T) {
	// Unknown names should fall back to the mercury shape so custom
	// instance names still render something.
	s := GetShape("ci-bot")
	if len(s) != ShapeRows {
		t.Errorf("fallback shape: got %d rows, want %d", len(s), ShapeRows)
	}
}

func TestShapeValuesInRange(t *testing.T) {
	names := []string{"mercury", "venus", "earth", "mars", "jupiter", "saturn", "uranus", "neptune"}
	for _, name := range names {
		s := GetShape(name)
		for r, row := range s {
			for c, v := range row {
				if v < 0 || v > 1 {
					t.Errorf("%s[%d][%d] = %v, must be in [0,1]", name, r, c, v)
				}
			}
		}
	}
}
