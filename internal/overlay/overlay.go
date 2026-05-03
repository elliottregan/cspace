// Package overlay renders the bubbletea provisioning overlay — a planet
// "coming into focus" as a sandbox boots through its phases.
//
// This is the slim v1 reimplementation. v0's version handled compose
// pipelines with a dozen sub-phases and a multi-line log tail; v1's
// boot is five phases and under ten seconds end-to-end, so the overlay
// is correspondingly tighter: one focus-pull animation, one phase
// label, one spinner, no log tail.
package overlay

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/elliottregan/cspace/internal/planets"
)

// Phase identifies a step in the v1 sandbox boot pipeline. Values are
// monotonically increasing so the renderer can compute a focus-pull
// fraction with `int(phase) / TotalPhases`.
type Phase int

const (
	PhasePending    Phase = iota // pre-roll, planet entirely out of focus
	PhaseDaemon                  // ensureRegistryDaemon
	PhaseClone                   // provisionClone
	PhaseBoot                    // substrate.Run + waitForIP
	PhaseSupervisor              // waitForHealth
	PhaseReady                   // MarkReady, terminal success
)

// TotalPhases is the denominator of the focus-pull. PhaseDaemon
// through PhaseReady inclusive.
const TotalPhases = int(PhaseReady)

// phaseLabels are shown beside the spinner. PhasePending is unused at
// runtime — we always start animating from PhaseDaemon at the latest.
var phaseLabels = map[Phase]string{
	PhasePending:    "preparing",
	PhaseDaemon:     "starting cspace daemon",
	PhaseClone:      "preparing workspace",
	PhaseBoot:       "booting microVM",
	PhaseSupervisor: "starting supervisor",
	PhaseReady:      "ready",
}

// Event is the message a channel-backed reporter sends to the running
// overlay. Exactly one of Phase / Err / Done should be set per Event;
// the receiver matches on whichever is non-zero (Done > Err > Phase).
type Event struct {
	Phase Phase
	Err   error
	Done  bool
}

// Reporter is the worker-side handle the boot pipeline calls into to
// signal phase boundaries. Two implementations:
//
//   - chanReporter sends events into a bubbletea-driven planet animation.
//   - LineReporter prints flat status lines to an io.Writer, used when
//     the overlay is disabled (--no-overlay or non-TTY stdout).
//
// The boot pipeline doesn't need to know which one it has.
type Reporter interface {
	Phase(p Phase)
	Done()
	Error(err error)
}

type chanReporter struct{ ch chan<- Event }

func (r *chanReporter) Phase(p Phase)     { r.ch <- Event{Phase: p} }
func (r *chanReporter) Done()             { r.ch <- Event{Done: true} }
func (r *chanReporter) Error(err error)   { r.ch <- Event{Err: err} }

// LineReporter prints one line per phase to the given Writer. Used when
// the overlay is disabled. Phase labels mirror the overlay's status
// line so the two outputs feel like the same UX in different skins.
type LineReporter struct{ Out io.Writer }

func (r *LineReporter) Phase(p Phase) {
	if r.Out == nil {
		return
	}
	label := phaseLabels[p]
	if label == "" {
		return
	}
	_, _ = fmt.Fprintf(r.Out, "[%d/%d] %s\n", int(p), TotalPhases, label)
}
func (r *LineReporter) Done()           {}
func (r *LineReporter) Error(err error) {}

// Start launches the planet overlay for sandbox `name` on a goroutine
// and returns a Reporter for the boot pipeline plus a done channel
// that closes once the bubbletea program has torn down. Caller is
// responsible for calling reporter.Done() (or .Error()) and then
// waiting on <-done before printing anything to stdout — bubbletea
// owns the terminal until then.
//
// If name is not a known planet the overlay falls back to a neutral
// grey circle so custom-named sandboxes still get the boot animation.
func Start(name string) (Reporter, <-chan struct{}) {
	events := make(chan Event, 16)
	reporter := &chanReporter{ch: events}

	model := initialModel(name, events)
	prog := tea.NewProgram(model)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = prog.Run()
	}()
	return reporter, done
}

// nextEventCmd produces a tea.Cmd that pulls one Event from the
// channel and returns it as a tea.Msg. The Update loop chains
// these so a steady stream of phase advances becomes a steady
// stream of redraws.
func nextEventCmd(ch <-chan Event) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return Event{Done: true}
		}
		return e
	}
}

type model struct {
	name    string
	planet  planets.Planet
	phase   Phase
	err     error
	done    bool
	events  <-chan Event
	spinner spinner.Model
}

func initialModel(name string, events <-chan Event) model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	return model{
		name:    name,
		planet:  planets.MustGet(name),
		phase:   PhasePending,
		events:  events,
		spinner: sp,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, nextEventCmd(m.events))
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case Event:
		if msg.Done {
			m.phase = PhaseReady
			m.done = true
			// Quit after one final render so the fully-focused planet
			// flashes briefly. tea.Quit fires immediately; the View
			// for the current state is what the user sees last.
			return m, tea.Quit
		}
		if msg.Err != nil {
			m.err = msg.Err
			return m, tea.Quit
		}
		if msg.Phase > m.phase {
			m.phase = msg.Phase
		}
		// Pull the next event.
		return m, nextEventCmd(m.events)
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		// Ctrl-C aborts; everything else is ignored — the overlay is
		// not interactive, just a status display.
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m model) View() string {
	var b strings.Builder

	// Planet rendering. Capped at TotalPhases so PhaseReady renders
	// at full focus. Overlays (clouds, Great Red Spot, ring shadows…)
	// resolve in lockstep with the base shape.
	phase := int(m.phase)
	if phase > TotalPhases {
		phase = TotalPhases
	}
	shape := planets.GetShape(m.name)
	opts := DefaultRenderOptions()
	opts.Overlays = planets.GetOverlays(m.name)
	b.WriteString(RenderPlanetWith(shape, m.planet, phase, TotalPhases, opts))
	b.WriteString("\n\n")

	// Status line: spinner + phase label, dimmed when done.
	label := phaseLabels[m.phase]
	if m.err != nil {
		errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Bold(true)
		b.WriteString(errStyle.Render("✗ failed: ") + m.err.Error())
		b.WriteString("\n")
		return b.String()
	}
	if m.done {
		okStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#5fffaf")).Bold(true)
		b.WriteString(okStyle.Render("✓ " + m.name) + "  " + label)
		b.WriteString("\n")
		return b.String()
	}
	dim := lipgloss.NewStyle().Faint(true)
	b.WriteString(m.spinner.View() + dim.Render(" "+m.name+"  "+label))
	b.WriteString("\n")
	return b.String()
}
