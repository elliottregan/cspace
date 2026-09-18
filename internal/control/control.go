// Package control holds cspace's queries and actions as plain Go functions:
// no terminal code, no cobra, no bubbletea. It is the single implementation
// the CLI commands and (from rollout step 2 of the control-plane design) the
// TUI both call, so "how do you attach to a sandbox" has exactly one answer.
//
// This is step 1's slice of it: the attach argv, the tmux plumbing, and the
// per-sandbox client bookkeeping. The Snapshot / AgentStatus / Ports / Events
// queries and the Down / Send / Interrupt / RestartBrowser / Up actions move
// here in later steps.
package control

import "path/filepath"

const (
	// SessionClaude and SessionShell are the tmux sessions cspace keeps
	// inside a sandbox: one per pane kind, so two panes on one sandbox never
	// contend for a current window. `cspace attach` from any terminal joins
	// SessionClaude and shares its screen.
	SessionClaude = "cspace-claude"
	SessionShell  = "cspace-shell"

	// TmuxConf is where the sandbox image puts cspace's tmux config. It is
	// always passed with `tmux -f`, so ~/.tmux.conf and /etc/tmux.conf inside
	// the sandbox are never read and no user binding can swallow a key the
	// host meant for Claude.
	TmuxConf = "/usr/local/etc/cspace-tmux.conf"

	// Workspace is the sandbox's project directory, and the working
	// directory every session starts in.
	Workspace = "/workspace"
)

// ControlPlaneDir is where cspace keeps its per-sandbox attach bookkeeping on
// the host: the attach lock and one record file per live tmux client. It is
// deliberately not the session directory — `cspace down` wipes that, and a
// client record must outlive nothing but its own client.
func ControlPlaneDir(home, project, sandbox string) string {
	return filepath.Join(home, ".cspace", "controlplane", project, sandbox)
}
