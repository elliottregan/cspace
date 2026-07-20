// Package tui implements the `cspace tui` dashboard: a read-and-act view over
// the host's cspace containers. This file holds the pure domain types shared
// by the poller, the correlation fold, and the Bubble Tea model/view.
package tui

import (
	"fmt"
	"time"
)

// RowKind classifies a display row. Only RowSandbox and RowBrowser are
// selectable / actionable.
type RowKind int

const (
	RowProject RowKind = iota // project header, non-selectable
	RowSandbox                // a registered sandbox
	RowSidecar                // a compose sidecar nested under a sandbox
	RowBrowser                // the project's shared browser sidecar
	RowSystem                 // buildkit / unmatched containers, dimmed
)

// RowState is the coarse lifecycle a row renders with.
type RowState int

const (
	StateStopped  RowState = iota // registry entry, no running container
	StateBooting                  // registry State == "starting"
	StateRunning                  // container running; for a sandbox, supervisor reachable
	StateDegraded                 // container running but supervisor unreachable
)

// AgentStatus is a sandbox supervisor's GET /status, decoded. Reachable is
// false when the probe failed (timeout / connection refused / non-2xx).
type AgentStatus struct {
	Reachable        bool
	State            string // "working" | "idle"
	Session          string
	QueueDepth       int
	LastEventType    string
	LastEventSubtype string
	LastEventTs      string
}

// Row is one line in the dashboard. Container is the full container name
// (cspace-<project>-<name>); "" for project headers. ControlURL/Token drive
// actions on sandbox rows and are never rendered (Token is a live secret).
type Row struct {
	Kind       RowKind
	Project    string
	Name       string // sandbox/sidecar/browser display name
	Container  string
	State      RowState
	IP         string
	MemoryB    int64
	Uptime     time.Duration
	Agent      AgentStatus   // meaningful only for RowSandbox
	Browser    BrowserHealth // meaningful only for RowBrowser
	ControlURL string
	Token      string
	Selectable bool
}

// DaemonHealth is the host daemon's GET /health, decoded.
type DaemonHealth struct {
	Reachable bool
	Version   string
}

// BrowserHealth is a browser sidecar's last-known CDP probe result
// (GET :9222/json/version). Meaningful only for RowBrowser.
type BrowserHealth struct {
	Reachable bool
	Version   string
}

// Snapshot is one poll's worth of state the model renders. Err is set when
// `container ls` failed; Rows may then be registry-only or empty.
type Snapshot struct {
	Rows    []Row
	Daemon  DaemonHealth
	Err     error
	TakenAt time.Time
}

// containerName is the workspace container for a sandbox.
func containerName(project, name string) string {
	return fmt.Sprintf("cspace-%s-%s", project, name)
}

// browserContainerName is the project's shared browser sidecar.
func browserContainerName(project string) string {
	return fmt.Sprintf("cspace-%s-browser", project)
}
