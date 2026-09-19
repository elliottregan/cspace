// Package controlplane is `cspace tui`: a Bubble Tea v2 dashboard over every
// cspace container on the host.
//
// It is the UI layer and nothing else. Every query it makes goes through the
// Data interface (poll.go) and every action through the Actor interface
// (actor.go); both are declared here, implemented in internal/cli, and
// injected — so this package never imports internal/cli, and internal/control
// stays the one implementation of "what is running" and "do this to it".
//
// Rollout step 4 grew the seams step 3 left: the main area holds the focused
// pane (internal/pane, opened through the PaneHost seam), the detail band
// moved under the sidebar, and the leader binding dispatches. Attach no
// longer suspends the program — a Claude session runs in a pane inside the
// window.
package controlplane

import "github.com/elliottregan/cspace/internal/control"

// sandboxKey identifies one sandbox across polls. Rows are rebuilt from
// scratch on every snapshot, so per-sandbox state the model carries between
// them (agent status, interactive state, ports) is keyed by identity rather
// than by row index.
type sandboxKey struct {
	Project string
	Name    string
}

// liveState is the fast ticker's per-sandbox sample: the supervisor's status
// and the interactive session's hook-written state. The zero value means
// "not sampled yet", which readers fall back from rather than render.
type liveState struct {
	Agent       control.AgentStatus
	Interactive control.InteractiveState
}

// keyOf identifies the sandbox a row belongs to. Sidecar and browser rows
// carry their project, so they key cleanly too; project headers key to the
// zero-name entry nothing ever samples.
func keyOf(row control.Row) sandboxKey {
	return sandboxKey{Project: row.Project, Name: row.Name}
}
