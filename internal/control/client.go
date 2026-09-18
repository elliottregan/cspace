package control

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
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

// BrowserCDPPort is the Chrome DevTools HTTP port the browser sidecar
// exposes; the snapshot's health probe is GET http://<ip>:9222/json/version.
// Exported so internal/cli's browser-sidecar code (which builds the same
// URLs from the host side) has one definition to import instead of its own
// copy of the port number.
const BrowserCDPPort = 9222

// ContainerCLI is the slice of *applecontainer.Adapter control needs. An
// interface so tests inject canned results without the `container` CLI.
type ContainerCLI interface {
	List(ctx context.Context) ([]applecontainer.ContainerSummary, error)
	Stats(ctx context.Context) ([]applecontainer.ContainerStats, error)
	Exec(ctx context.Context, name string, cmd []string, opts substrate.ExecOpts) (substrate.ExecResult, error)
}

// containerExecer adapts a ContainerCLI to the Execer that tmux.go's driver
// takes, so this package has one exec transport when a Client owns a
// ContainerCLI. New builds one whenever Options.Containers is set; a Client
// built without one gets noContainerExecer instead, which fails closed
// rather than falling back to CLIExecer's real `container` CLI. CLIExecer
// itself stays the default only where a *Tmux is built directly by NewTmux
// with no Client at all — cmd_attach.go's `defaultTmux`.
type containerExecer struct{ cli ContainerCLI }

var _ Execer = containerExecer{}

// Exec implements Execer, including its combined-output contract: stdout as
// given on a clean exit; on a non-zero exit, stdout with that exit's trimmed
// stderr folded in by combineOutput (tmux.go). A non-zero exit is not an
// error here — tmux says ordinary things like "no server running" that way,
// and the adapter already folds *exec.ExitError into ExitCode, so it passes
// straight through as data. Only a transport failure (the command could not
// even be started) returns a non-nil error, with that same trimmed stderr
// folded into its text, matching CLIExecer's transport-failure branch.
func (e containerExecer) Exec(ctx context.Context, container string, cmdline []string) (string, int, error) {
	res, err := e.cli.Exec(ctx, container, cmdline, substrate.ExecOpts{})
	if err != nil {
		if stderr := strings.TrimSpace(res.Stderr); stderr != "" {
			return res.Stdout, -1, fmt.Errorf("%w: %s", err, stderr)
		}
		return res.Stdout, -1, err
	}
	if res.ExitCode != 0 {
		return combineOutput(res.Stdout, res.Stderr), res.ExitCode, nil
	}
	return res.Stdout, 0, nil
}

// noContainerExecer stands in for Execer when a Client is built with no
// ContainerCLI. Every call fails closed with ErrNoContainerCLI, without
// touching the host — so Present/ListClients/DetachClient degrade to an
// error instead of a Tmux driver silently falling back to CLIExecer's real
// `container` CLI underneath a caller that never configured one.
type noContainerExecer struct{}

var _ Execer = noContainerExecer{}

// Exec implements Execer.
func (noContainerExecer) Exec(context.Context, string, []string) (string, int, error) {
	return "", -1, ErrNoContainerCLI
}

// EntryStore is the slice of *registry.Registry control needs. An interface
// so an in-sandbox caller can supply a daemon-backed lookup instead of the
// host's registry file.
type EntryStore interface {
	List() ([]registry.Entry, error)
	Lookup(project, name string) (registry.Entry, error)
}

// Options configures a Client. Every field is optional and every seam it
// backs fails closed on its own when left unset — a nil Containers or
// Entries, an empty Home or ProjectRoot, a nil Host — rather than panicking,
// so a caller that only needs some of what Client does can build a partial
// one. DaemonURL is the host daemon's HTTP base (e.g.
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

	// ResolverInstalled reports whether macOS routes *.cspace.test at the
	// cspace daemon. Injected so tests don't depend on the host's /etc; nil
	// means "check ResolverFile".
	ResolverInstalled func() bool

	// ProjectRoot is the working directory `cspace up` runs in — the tree it
	// reads .cspace.json and .devcontainer from.
	ProjectRoot string

	// Host supplies the operations whose implementations still live in
	// internal/cli. Nil is legal: read-only callers need no Host, and the
	// actions that do fail with ErrNoHost.
	Host Host
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
	host       Host

	resolverInstalled func() bool

	projectRoot string

	// executable and runCommand are the process seams Up uses, fields so a
	// test drives it without spawning anything.
	executable func() (string, error)
	runCommand func(ctx context.Context, dir, bin string, args ...string) (string, error)

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
		} else {
			tm.Exec = noContainerExecer{}
		}
	}
	resolver := o.ResolverInstalled
	if resolver == nil {
		resolver = func() bool {
			_, err := os.Stat(ResolverFile)
			return err == nil
		}
	}
	return &Client{
		containers:        o.Containers,
		tmux:              tm,
		entries:           o.Entries,
		daemonURL:         o.DaemonURL,
		home:              o.Home,
		now:               now,
		host:              o.Host,
		resolverInstalled: resolver,
		projectRoot:       o.ProjectRoot,
		executable:        os.Executable,
		runCommand:        runHostCommand,
		probeClient:       &http.Client{Timeout: probeTimeout},
		actionClient:      &http.Client{Timeout: actionTimeout},
		browserCDPURL:     func(ip string) string { return fmt.Sprintf("http://%s:%d/json/version", ip, BrowserCDPPort) },
	}
}
