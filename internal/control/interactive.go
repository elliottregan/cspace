package control

import (
	"encoding/json"
	"os"
)

// InteractiveState is the interactive Claude session's state, written into a
// sandbox's /sessions directory by the Claude Code hooks that run
// cspace-agent-state.sh. Known states are "starting", "working",
// "needs-input", "idle" and "exited".
//
// It describes the session a person drives through `cspace attach` or a
// control-plane pane — not the headless supervisor, whose state comes from
// AgentStatus.
type InteractiveState struct {
	State     string `json:"state"`
	At        string `json:"at"`
	SessionID string `json:"session_id"`
	Event     string `json:"event"`
}

// Known reports whether a state was actually read. It is false before the
// first hook has fired, after `cspace down` wiped the session tree, and for
// an unparseable record — all cases where the caller should fall back to the
// supervisor's state rather than render a wrong glyph.
func (s InteractiveState) Known() bool { return s.State != "" }

// ReadInteractiveState parses the hook-written state file at path.
//
// It is deliberately total: a missing file, an unreadable one, and a torn
// write all yield the zero value. The writer renames a temp file into place,
// so a partial record should be impossible — but a crashed writer or a
// filesystem without atomic rename can still leave one, and a dashboard
// polling this file once a second must not turn that into an error banner.
func ReadInteractiveState(path string) InteractiveState {
	data, err := os.ReadFile(path)
	if err != nil {
		return InteractiveState{}
	}
	var s InteractiveState
	if err := json.Unmarshal(data, &s); err != nil {
		return InteractiveState{}
	}
	return s
}

// InteractiveState reads the sandbox's hook-written interactive session state.
func (c *Client) InteractiveState(project, sandbox string) InteractiveState {
	return ReadInteractiveState(AgentStatePath(c.home, project, sandbox))
}
