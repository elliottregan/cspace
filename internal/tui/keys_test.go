package tui

import "testing"

func TestContextualPredicates(t *testing.T) {
	runningSandbox := Row{Kind: RowSandbox, State: StateRunning, Agent: AgentStatus{Reachable: true, State: "working"}}
	idleSandbox := Row{Kind: RowSandbox, State: StateRunning, Agent: AgentStatus{Reachable: true, State: "idle"}}
	stoppedSandbox := Row{Kind: RowSandbox, State: StateStopped}
	unreachableSandbox := Row{Kind: RowSandbox, State: StateDegraded, Agent: AgentStatus{Reachable: false}}
	browser := Row{Kind: RowBrowser}
	project := Row{Kind: RowProject}

	cases := []struct {
		name string
		fn   func(Row) bool
		row  Row
		want bool
	}{
		{"attach running sandbox", canAttach, runningSandbox, true},
		{"attach stopped sandbox", canAttach, stoppedSandbox, false},
		{"attach browser", canAttach, browser, false},
		{"down running sandbox", canDown, runningSandbox, true},
		{"down stopped sandbox", canDown, stoppedSandbox, false},
		{"send reachable sandbox", canSend, idleSandbox, true},
		{"send unreachable sandbox", canSend, unreachableSandbox, false},
		{"send browser", canSend, browser, false},
		{"interrupt working sandbox", canInterrupt, runningSandbox, true},
		{"interrupt idle sandbox", canInterrupt, idleSandbox, false},
		{"interrupt unreachable sandbox", canInterrupt, unreachableSandbox, false},
		{"browser on browser row", canBrowser, browser, true},
		{"browser on sandbox row", canBrowser, runningSandbox, true},
		{"browser on project row", canBrowser, project, false},
	}
	for _, tc := range cases {
		if got := tc.fn(tc.row); got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, got, tc.want)
		}
	}
}
