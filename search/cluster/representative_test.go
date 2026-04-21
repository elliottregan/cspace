package cluster

import "testing"

func TestPickRepresentative_PrefersFileKind(t *testing.T) {
	pts := []representative{
		{ID: 1, Path: "a.go", Kind: "chunk", LineStart: 50},
		{ID: 2, Path: "a.go", Kind: "file", LineStart: 0},
		{ID: 3, Path: "b.go", Kind: "chunk", LineStart: 1},
	}
	got := pickRepresentative(pts)
	if len(got) != 2 {
		t.Fatalf("expected 2 reps, got %d", len(got))
	}
	m := map[string]representative{}
	for _, r := range got {
		m[r.Path] = r
	}
	if m["a.go"].ID != 2 {
		t.Errorf("expected ID 2 (file kind) for a.go, got %d", m["a.go"].ID)
	}
	if m["b.go"].ID != 3 {
		t.Errorf("expected ID 3 for b.go, got %d", m["b.go"].ID)
	}
}

func TestPickRepresentative_FallsBackToLowestLineStart(t *testing.T) {
	pts := []representative{
		{ID: 1, Path: "a.go", Kind: "chunk", LineStart: 100},
		{ID: 2, Path: "a.go", Kind: "chunk", LineStart: 1},
		{ID: 3, Path: "a.go", Kind: "chunk", LineStart: 50},
	}
	got := pickRepresentative(pts)
	if len(got) != 1 {
		t.Fatalf("expected 1 rep, got %d", len(got))
	}
	if got[0].ID != 2 {
		t.Errorf("expected ID 2 (lowest LineStart) for a.go, got %d", got[0].ID)
	}
}
