package cli

import "testing"

// TestInspectHasRecords guards containerExists against Apple Container 1.1.x's
// pretty-printed `container inspect` output. 1.1.x emits the array as
// "[\n  {\n ...", so a byte-prefix check against "[{" wrongly reports an
// existing container as missing — which would let the registry-prune paths
// tear down live sandboxes.
func TestInspectHasRecords(t *testing.T) {
	const prettyOneRecord = `[
  {
    "id" : "buildkit",
    "configuration" : { "id" : "buildkit" },
    "status" : { "state" : "running" }
  }
]`
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"pretty one record (1.1.x)", prettyOneRecord, true},
		{"compact one record (0.12.x)", `[{"id":"x"}]`, true},
		{"empty array", `[]`, false},
		{"empty array pretty", "[\n\n]", false},
		{"empty string", ``, false},
		{"malformed", `{not json`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := inspectHasRecords([]byte(tc.in)); got != tc.want {
				t.Errorf("inspectHasRecords(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
