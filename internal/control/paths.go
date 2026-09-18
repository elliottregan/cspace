package control

import (
	"errors"
	"path/filepath"
)

// The host-side layout cspace materializes per sandbox. Every path here
// mirrors a literal cmd_up.go uses when it builds the container's mounts —
// change one and you must change the other.
//
// ControlPlaneDir (~/.cspace/controlplane/<project>/<sandbox>, the attach lock
// and client records) is the fourth member of this layout and already lives in
// control.go — do not redeclare it here.

// ErrNoHome is returned by the Client methods that build one of these paths
// (Events, Ports) when the Client was built with an empty Options.Home.
// filepath.Join silently accepts an empty first element and produces a path
// relative to whatever the process's cwd happens to be, which is wrong in a
// way nothing would notice until it read the wrong sandbox's files — so
// these methods check first and fail closed instead. InteractiveState has no
// error return; it takes the same empty-Home case to its zero value instead.
var ErrNoHome = errors.New("control: no Home configured")

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

// CloneDir is a sandbox's own git clone on the host — the tree bind-mounted
// into the container as /workspace. Project configuration is read from here,
// never from the caller's cwd: a control-plane window shows sandboxes from
// several projects at once, and `cspace down` already learned this lesson.
func CloneDir(home, project, sandbox string) string {
	return filepath.Join(home, ".cspace", "clones", project, sandbox)
}
