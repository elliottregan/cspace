package control

import "testing"

func TestHostPaths(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"session dir", SessionDir("/home/x", "alpha", "mercury"), "/home/x/.cspace/sessions/alpha/mercury"},
		{"events log", SessionEventsPath("/home/x", "alpha", "mercury"), "/home/x/.cspace/sessions/alpha/mercury/primary/events.ndjson"},
		{"agent state", AgentStatePath("/home/x", "alpha", "mercury"), "/home/x/.cspace/sessions/alpha/mercury/agent-state.json"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}
