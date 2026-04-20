package corpus

import "testing"

func TestRecord_IDIsStableForSameInputs(t *testing.T) {
	r1 := Record{Path: "foo.go", LineStart: 10, LineEnd: 20, Kind: "chunk"}
	r2 := Record{Path: "foo.go", LineStart: 10, LineEnd: 20, Kind: "chunk"}
	if r1.ID() != r2.ID() {
		t.Fatalf("Record.ID should be deterministic: %d vs %d", r1.ID(), r2.ID())
	}
}

func TestRecord_IDDiffersOnDifferentKindOrRange(t *testing.T) {
	base := Record{Path: "foo.go", LineStart: 10, LineEnd: 20, Kind: "chunk"}
	cases := []Record{
		{Path: "bar.go", LineStart: 10, LineEnd: 20, Kind: "chunk"},
		{Path: "foo.go", LineStart: 1, LineEnd: 20, Kind: "chunk"},
		{Path: "foo.go", LineStart: 10, LineEnd: 21, Kind: "chunk"},
		{Path: "foo.go", LineStart: 10, LineEnd: 20, Kind: "file"},
	}
	for i, c := range cases {
		if c.ID() == base.ID() {
			t.Errorf("case %d: IDs collided: %d", i, c.ID())
		}
	}
}
