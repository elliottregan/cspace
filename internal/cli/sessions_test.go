package cli

import (
	"testing"
)

// filterVolumesByProject mirrors the suffix-matching logic in
// findLegacyClaudeHomeVolumes. Extracted here as a pure function so the
// test doesn't need `docker` on PATH.
func filterVolumesByProject(volumes []string, project string) []string {
	prefix := "cs-" + project + "-"
	const suffix = "_claude-home"
	var matched []string
	for _, name := range volumes {
		if startsWith(name, prefix) && endsWith(name, suffix) {
			matched = append(matched, name)
		}
	}
	return matched
}

func startsWith(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }
func endsWith(s, p string) bool   { return len(s) >= len(p) && s[len(s)-len(p):] == p }

func TestFilterVolumesByProject(t *testing.T) {
	tests := []struct {
		name    string
		volumes []string
		project string
		want    []string
	}{
		{
			name: "picks exact project claude-home volumes",
			volumes: []string{
				"cs-myapp-mercury_claude-home",
				"cs-myapp-venus_claude-home",
				"cs-myapp-mercury_workspace",
				"cs-otherapp-mercury_claude-home",
				"cs-myapp-mercury_gh-config",
			},
			project: "myapp",
			want: []string{
				"cs-myapp-mercury_claude-home",
				"cs-myapp-venus_claude-home",
			},
		},
		{
			name:    "no matches returns nil",
			volumes: []string{"unrelated", "cs-other-foo_claude-home"},
			project: "myapp",
			want:    nil,
		},
		{
			name:    "empty input returns nil",
			volumes: []string{},
			project: "myapp",
			want:    nil,
		},
		{
			name: "project name matching is exact (no prefix collision)",
			volumes: []string{
				"cs-myapp-mercury_claude-home",
				"cs-myapp2-mercury_claude-home", // longer project name must NOT match "myapp"
			},
			project: "myapp",
			want:    []string{"cs-myapp-mercury_claude-home"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filterVolumesByProject(tc.volumes, tc.project)
			if len(got) != len(tc.want) {
				t.Fatalf("length: got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("index %d: got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
