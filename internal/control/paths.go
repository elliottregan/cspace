package control

import "path/filepath"

// The host-side layout cspace materializes per sandbox. Every path here
// mirrors a literal cmd_up.go uses when it builds the container's mounts —
// change one and you must change the other.
//
// ControlPlaneDir (~/.cspace/controlplane/<project>/<sandbox>, the attach lock
// and client records) is the fourth member of this layout and already lives in
// control.go — do not redeclare it here.

// SessionDir is the host directory bind-mounted into a sandbox as /sessions.
func SessionDir(home, project, sandbox string) string {
	return filepath.Join(home, ".cspace", "sessions", project, sandbox)
}

// SessionEventsPath is the supervisor's event log. The "primary" segment is
// the supervisor's hardcoded SESSION_ID — do not make it configurable.
func SessionEventsPath(home, project, sandbox string) string {
	return filepath.Join(SessionDir(home, project, sandbox), DefaultSession, "events.ndjson")
}

// AgentStatePath is the interactive session's state file, written by the
// Claude Code hooks running cspace-agent-state.sh inside the sandbox. It sits
// at the root of /sessions, not under a session id: there is one interactive
// Claude per sandbox.
func AgentStatePath(home, project, sandbox string) string {
	return filepath.Join(SessionDir(home, project, sandbox), "agent-state.json")
}
