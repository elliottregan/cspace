package query

import "testing"

func TestDedupeByPath_KeepsBestScorePerPath(t *testing.T) {
	hits := []Hit{
		{Path: "a.go", Score: 0.5, LineStart: 1, LineEnd: 50},
		{Path: "a.go", Score: 0.8, LineStart: 51, LineEnd: 100},
		{Path: "b.go", Score: 0.6, LineStart: 1, LineEnd: 30},
	}
	got := DedupeByPath(hits)
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	if got[0].Path != "a.go" || got[0].Score != 0.8 {
		t.Errorf("expected a.go@0.8 first, got %+v", got[0])
	}
}

func TestDedupeByPath_SortedDescendingByScore(t *testing.T) {
	hits := []Hit{
		{Path: "a.go", Score: 0.3},
		{Path: "b.go", Score: 0.9},
		{Path: "c.go", Score: 0.6},
	}
	got := DedupeByPath(hits)
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].Score > got[i-1].Score {
			t.Errorf("not sorted descending at index %d: %+v", i, got)
		}
	}
}
