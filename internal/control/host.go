package control

import (
	"context"
	"errors"
	"io"
)

// Host is the set of host-side operations control does not own: their
// implementations still live in internal/cli, which control must not import.
// Whoever builds the Client supplies one.
//
// This is the same direction as internal/controlplane's Actor — the consumer
// declares the narrow interface it needs and the package that has the
// implementation satisfies it — so the dependency graph stays acyclic while
// there is still exactly one implementation of each operation.
type Host interface {
	// Teardown stops and removes a sandbox, wiping its clone, sessions and
	// volumes when wipeState is true, and writes progress plus any
	// "[cspace] warning: …" lines to out. It has no error return on purpose:
	// the underlying teardownSandbox has none either, and its only failure
	// signal is that warning text.
	Teardown(ctx context.Context, project, sandbox string, out io.Writer, wipeState bool)

	// RestartBrowser restarts the project's shared browser sidecar through
	// the escalation ladder (stop → SIGKILL → host-process teardown → start)
	// and reverifies liveness with protocol-level probes.
	RestartBrowser(ctx context.Context, project string) error
}

// ErrNoHost is returned by the actions that need a Host when the Client was
// built without one. Failing closed beats a nil-pointer panic in a UI that
// only meant to read.
var ErrNoHost = errors.New("control: no Host configured for this action")

// ErrNoContainerCLI is returned wherever a Client needs its ContainerCLI
// (Options.Containers) and was built without one — Snapshot, and any tmux
// driver call routed through New's containerExecer/noContainerExecer choice.
// Failing closed here is the same rule as ErrNoHost: a Client is legal to
// build with a nil seam (a read-only caller may not need every one), so
// every method that touches a nil seam must degrade to this error rather
// than dereference it.
var ErrNoContainerCLI = errors.New("control: no ContainerCLI configured")

// ErrNoEntryStore is returned wherever a Client needs its EntryStore
// (Options.Entries) and was built without one — Snapshot and lookup (and so
// every action that calls lookup: AgentStatus, Send, Interrupt, Ports).
var ErrNoEntryStore = errors.New("control: no EntryStore configured")
