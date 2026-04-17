package provision

import (
	"fmt"
	"os"
)

// Reporter receives provisioning progress notifications. Implementations
// choose how to surface them — fmt.Printf (logReporter) or a bubbletea
// channel (overlay.ChannelReporter).
//
// All methods are called from provision.Run's own goroutine; implementations
// that forward to channels should use buffered channels so they never block
// provisioning.
type Reporter interface {
	// Phase announces entry into a named phase (1-indexed).
	Phase(name string, num, total int)
	// Warn surfaces a non-fatal issue.
	Warn(msg string)
	// Done is called once, on successful completion.
	Done()
	// Error is called once, on fatal failure. phase is the last phase
	// that started before the failure ("" = unknown).
	Error(phase string, err error)
}

// Phases lists the human-readable label for each of the 14 provisioning
// phases, in order. Exposed so callers (e.g. the overlay) can show the
// total count ahead of provisioning starting.
var Phases = []string{
	"Validating name",
	"Removing orphans",
	"Bundling repo",
	"Creating volumes",
	"Creating network",
	"Starting reverse proxy",
	"Setting up directories",
	"Starting containers",
	"Waiting for container",
	"Configuring hosts",
	"Setting permissions",
	"Initializing workspace",
	"Configuring git & env",
	"Installing plugins",
}

// logReporter is the default reporter used when Params.Reporter is nil.
// It mimics pre-overlay behavior: plain fmt.Printf lines on stdout,
// warnings on stderr.
type logReporter struct{}

func (logReporter) Phase(name string, num, total int) {
	fmt.Printf("[%d/%d] %s...\n", num, total, name)
}

func (logReporter) Warn(msg string) {
	fmt.Fprintf(os.Stderr, "warning: %s\n", msg)
}

func (logReporter) Done() {
	fmt.Println("Setup complete.")
}

func (logReporter) Error(phase string, err error) {
	if phase != "" {
		fmt.Fprintf(os.Stderr, "error in phase %q: %v\n", phase, err)
	}
}
