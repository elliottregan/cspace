// Package control holds cspace's queries and actions as plain Go functions:
// no terminal code, no cobra, no bubbletea. It is the single implementation
// the CLI commands and the TUI both call, so "how do you attach to a
// sandbox", "how do you send it a turn", "how do you read its status" each
// have exactly one answer.
//
// The attach argv (argv.go), the tmux driver (tmux.go) and the per-sandbox
// attach bookkeeping (attach.go) are plain functions and need no Client.
// Everything else — the Snapshot / AgentStatus / Ports / Events /
// InteractiveState queries and the Down / Send / Interrupt / RestartBrowser
// / Up / ListClients / DetachClient actions — hangs off Client (client.go),
// seamed on ContainerCLI (the substrate), EntryStore (the registry) and Host
// (the internal/cli-owned operations this package cannot import) so it is
// testable without any of them. internal/tui consumes this package through
// type aliases (internal/tui/types.go) rather than keeping its own copies,
// so a Row built there is the same type as one built here. The package
// depends on internal/devcontainer for exactly one thing: Ports reads a
// project's devcontainer.json portsAttributes for its port labels.
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
	// passed with `tmux -f` wherever a session might be created
	// (new-session), so ~/.tmux.conf and /etc/tmux.conf inside the sandbox
	// are never read and no user binding can swallow a key the host meant
	// for Claude. list-clients and detach-client never start a server and so
	// never need it.
	TmuxConf = "/usr/local/etc/cspace-tmux.conf"

	// Workspace is the sandbox's project directory, and the working
	// directory every session starts in.
	Workspace = "/workspace"

	// DNSDomain is the suffix cspace's daemon answers DNS queries for; each
	// sandbox is reachable at http://<sandbox>.<project>.cspace.test:<port>/.
	// ResolverFile is the macOS resolver stanza `sudo cspace dns install`
	// writes; its presence is what makes those names resolve on the host.
	DNSDomain    = "cspace.test"
	ResolverFile = "/etc/resolver/" + DNSDomain
)

// ControlPlaneDir is where cspace keeps its per-sandbox attach bookkeeping on
// the host: the attach lock and one record file per live tmux client. It is
// deliberately not the session directory — `cspace down` wipes that, and a
// client record must outlive nothing but its own client.
func ControlPlaneDir(home, project, sandbox string) string {
	return filepath.Join(home, ".cspace", "controlplane", project, sandbox)
}
