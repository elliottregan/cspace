// Package tui implements the `cspace tui` dashboard: a read-and-act view over
// the host's cspace containers.
//
// Its domain types and every data query now live in internal/control, so the
// CLI and this dashboard share one implementation. This file re-exports, as
// type aliases, the names the Bubble Tea model and view use — the dashboard's
// own code is unchanged by the move, and a Row built here is the same type as
// a Row built by internal/control.
package tui

import "github.com/elliottregan/cspace/internal/control"

type (
	RowKind       = control.RowKind
	RowState      = control.RowState
	AgentStatus   = control.AgentStatus
	Row           = control.Row
	DaemonHealth  = control.DaemonHealth
	BrowserHealth = control.BrowserHealth
	Snapshot      = control.Snapshot
	EventLine     = control.EventLine

	// Poller is the snapshot seam NewModel takes. Named for the dashboard's
	// poll loop; satisfied by *control.Client.
	Poller = control.Snapshotter
)

const (
	RowProject = control.RowProject
	RowSandbox = control.RowSandbox
	RowSidecar = control.RowSidecar
	RowBrowser = control.RowBrowser
	RowSystem  = control.RowSystem
)

const (
	StateStopped  = control.StateStopped
	StateBooting  = control.StateBooting
	StateRunning  = control.StateRunning
	StateDegraded = control.StateDegraded
)
