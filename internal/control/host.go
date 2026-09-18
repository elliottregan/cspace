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
// This is the same direction as internal/tui's Actor — the consumer declares
// the narrow interface it needs and the package that has the implementation
// satisfies it — so the dependency graph stays acyclic while there is still
// exactly one implementation of each operation.
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
