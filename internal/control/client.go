package control

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
)

// probeTimeout bounds each control-port / daemon HTTP probe inside a
// Snapshot. Short so a wedged supervisor degrades one row rather than
// stalling the whole query.
const probeTimeout = 800 * time.Millisecond

// actionTimeout bounds one action's HTTP round trip (send, interrupt).
// Actions are user-initiated and may legitimately take a moment, so they do
// not share the snapshot's aggressive probe budget.
const actionTimeout = 10 * time.Second

// maxProbeConcurrency caps the status fan-out so a host with many sandboxes
// doesn't open an unbounded burst of sockets per snapshot.
const maxProbeConcurrency = 8

// browserCDPPort is the Chrome DevTools HTTP port the browser sidecar
// exposes; the snapshot's health probe is GET http://<ip>:9222/json/version.
const browserCDPPort = 9222

// ContainerCLI is the slice of *applecontainer.Adapter control needs. An
// interface so tests inject canned results without the `container` CLI.
type ContainerCLI interface {
	List(ctx context.Context) ([]applecontainer.ContainerSummary, error)
	Stats(ctx context.Context) ([]applecontainer.ContainerStats, error)
	Exec(ctx context.Context, name string, cmd []string, opts substrate.ExecOpts) (substrate.ExecResult, error)
}

// containerExecer adapts a ContainerCLI to the Execer that tmux.go's driver
// takes, so this package has one exec transport. CLIExecer stays the default
// only for callers that build no Client (the `cspace attach` path).
type containerExecer struct{ cli ContainerCLI }

var _ Execer = containerExecer{}

// Exec implements Execer. A non-zero exit is not an error — tmux says
// ordinary things like "no server running" that way — and the adapter
// already folds *exec.ExitError into ExitCode, so the code passes straight
// through. combineOutput (tmux.go) folds a non-zero exit's stderr into the
// returned string, matching Execer's contract; only a transport failure (the
// command could not even be started) returns a non-nil error.
func (e containerExecer) Exec(ctx context.Context, container string, cmdline []string) (string, int, error) {
	res, err := e.cli.Exec(ctx, container, cmdline, substrate.ExecOpts{})
	if err != nil {
		return res.Stdout, -1, err
	}
	if res.ExitCode != 0 {
		return combineOutput(res.Stdout, res.Stderr), res.ExitCode, nil
	}
	return res.Stdout, 0, nil
}

// EntryStore is the slice of *registry.Registry control needs. An interface
// so an in-sandbox caller can supply a daemon-backed lookup instead of the
// host's registry file.
type EntryStore interface {
	List() ([]registry.Entry, error)
	Lookup(project, name string) (registry.Entry, error)
}

// Options configures a Client. Containers and Entries are required for every
// query; DaemonURL is the host daemon's HTTP base (e.g.
// "http://127.0.0.1:6280"); Home is the host home directory that the session
// and clone trees hang off; Now is injected for testable timestamps and
// defaults to time.Now.
type Options struct {
	Containers ContainerCLI
	Entries    EntryStore

	// Tmux is the in-sandbox tmux driver (tmux.go). Nil builds one over
	// Containers, so the package shares one exec transport and one memoized
	// per-sandbox presence probe.
	Tmux *Tmux

	DaemonURL string
	Home      string
	Now       func() time.Time
}

// Client answers cspace's control queries and runs its control actions. It is
// safe for concurrent use: every method is either read-only over injected
// dependencies or delegates to one that is.
type Client struct {
	containers ContainerCLI
	tmux       *Tmux
	entries    EntryStore
	daemonURL  string
	home       string
	now        func() time.Time

	probeClient  *http.Client
	actionClient *http.Client

	// browserCDPURL builds the CDP version-probe URL from a sidecar IP. A
	// field (not a hardcoded string) so tests can point it at an httptest
	// server; production uses the fixed DevTools port.
	browserCDPURL func(ip string) string
}

// New builds a Client from Options, filling in the defaults.
func New(o Options) *Client {
	now := o.Now
	if now == nil {
		now = time.Now
	}
	tm := o.Tmux
	if tm == nil {
		tm = NewTmux()
		if o.Containers != nil {
			tm.Exec = containerExecer{cli: o.Containers}
		}
	}
	return &Client{
		containers:    o.Containers,
		tmux:          tm,
		entries:       o.Entries,
		daemonURL:     o.DaemonURL,
		home:          o.Home,
		now:           now,
		probeClient:   &http.Client{Timeout: probeTimeout},
		actionClient:  &http.Client{Timeout: actionTimeout},
		browserCDPURL: func(ip string) string { return fmt.Sprintf("http://%s:%d/json/version", ip, browserCDPPort) },
	}
}
