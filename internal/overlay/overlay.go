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
	"math"
	"strings"
	"time"

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
	// AltScreen takes over the whole terminal for the boot animation
	// and restores the prior buffer on quit, so the planet renders as
	// a centered, full-window dialog rather than scrolling inline.
	prog := tea.NewProgram(model, tea.WithAltScreen())

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

// Animation tunables. These are the levers if the boot animation
// feels too sluggish or too snappy.
//
//   - animFPS: how often we redraw the focus pull. 30 is plenty
//     for half-block-character animation; 60 is overkill.
//   - focusTraversalDur: how long it takes the planet to traverse
//     the entire 0→1 focus range when the boot finishes
//     instantaneously. Anchors the visual minimum-runtime.
//   - finalHoldDur: how long to keep the fully focused planet on
//     screen after Done arrives. Without this the end frame can
//     flash by faster than the eye registers.
//
// Long-running phases (project install hooks, cold image pulls)
// are handled implicitly: the animation reaches the phase's target
// focus quickly, then sits calmly with the spinner until the next
// phase advances. No frame jitter.
const (
	animFPS           = 30
	focusTraversalDur = 1500 * time.Millisecond
	finalHoldDur      = 500 * time.Millisecond
)

// animTickMsg is the per-frame animation pulse — separate from
// spinner.TickMsg so the two clocks don't fight.
type animTickMsg time.Time

func animTickCmd() tea.Cmd {
	return tea.Tick(time.Second/animFPS, func(t time.Time) tea.Msg {
		return animTickMsg(t)
	})
}

type model struct {
	name        string
	planet      planets.Planet
	targetPhase Phase     // last phase the reporter advanced to
	focus       float64   // animated 0.0..1.0; lerps toward targetPhase/TotalPhases
	doneAt      time.Time // when reporter signaled Done; zero = still in progress
	err         error
	events      <-chan Event
	spinner     spinner.Model
	width       int
	height      int
}

func initialModel(name string, events <-chan Event) model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	return model{
		name:        name,
		planet:      planets.MustGet(name),
		targetPhase: PhasePending,
		focus:       0,
		events:      events,
		spinner:     sp,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, animTickCmd(), nextEventCmd(m.events))
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case Event:
		switch {
		case msg.Err != nil:
			m.err = msg.Err
			return m, tea.Quit
		case msg.Done:
			// Latch a target of full focus. Don't quit yet — the
			// animation tick will let the planet finish its focus
			// pull and then quit after finalHoldDur. Without this,
			// fast boots flash through the last few frames.
			m.targetPhase = PhaseReady
			if m.doneAt.IsZero() {
				m.doneAt = time.Now()
			}
		default:
			if msg.Phase > m.targetPhase {
				m.targetPhase = msg.Phase
			}
		}
		return m, nextEventCmd(m.events)
	case animTickMsg:
		// Animate focus toward the target at a constant velocity.
		// The integer phase enum maps to focus as targetPhase /
		// TotalPhases — events advance the target, the visual chases
		// it smoothly regardless of how fast or slow they arrive.
		target := float64(m.targetPhase) / float64(TotalPhases)
		if target > 1 {
			target = 1
		}
		velocity := 1.0 / focusTraversalDur.Seconds() / float64(animFPS)
		switch {
		case m.focus < target:
			m.focus = math.Min(target, m.focus+velocity)
		case m.focus > target:
			m.focus = math.Max(target, m.focus-velocity)
		}
		// Quit only when Done has been signaled, focus has reached
		// 1.0, and we've held the final frame long enough for the
		// eye to register it.
		if !m.doneAt.IsZero() && m.focus >= 1.0 && time.Since(m.doneAt) >= finalHoldDur {
			return m, tea.Quit
		}
		return m, animTickCmd()
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
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

	// Planet rendering. m.focus is animated continuously by the
	// animTick clock — we re-express it as int phase/total so the
	// existing renderer (which interpolates internally) sees the
	// fractional value. ×100 is more than enough granularity at
	// 30 FPS over our typical few-second boot.
	shape := planets.GetShape(m.name)
	opts := DefaultRenderOptions()
	opts.Overlays = planets.GetOverlays(m.name)
	b.WriteString(RenderPlanetWith(shape, m.planet, int(m.focus*100), 100, opts))
	b.WriteString("\n\n")

	// Status line: spinner + phase label, dimmed when done.
	label := phaseLabels[m.targetPhase]
	if m.err != nil {
		errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Bold(true)
		b.WriteString(errStyle.Render("✗ failed: ") + m.err.Error())
		b.WriteString("\n")
		return b.String()
	}
	if !m.doneAt.IsZero() && m.focus >= 1.0 {
		okStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#5fffaf")).Bold(true)
		b.WriteString(okStyle.Render("✓ " + m.name) + "  " + label)
		b.WriteString("\n")
		content := b.String()
		if m.width > 0 && m.height > 0 {
			return lipgloss.Place(m.width, m.height,
				lipgloss.Center, lipgloss.Center, content,
				lipgloss.WithWhitespaceBackground(lipgloss.Color("#000000")))
		}
		return content
	}
	dim := lipgloss.NewStyle().Faint(true)
	b.WriteString(m.spinner.View() + dim.Render(" "+m.name+"  "+label))
	b.WriteString("\n")
	content := b.String()
	if m.width > 0 && m.height > 0 {
		// Fill the entire alt-screen with the same near-black the
		// planet renderer uses for its viewport background — without
		// this, the area outside the planet's bounding box inherits
		// the host terminal's bg and the dialog reads as "rendered on
		// top of my prior shell" instead of "took over the screen".
		return lipgloss.Place(m.width, m.height,
			lipgloss.Center, lipgloss.Center, content,
			lipgloss.WithWhitespaceBackground(lipgloss.Color("#000000")))
	}
	return content
}
