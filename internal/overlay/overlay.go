package overlay

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/elliottregan/cspace/internal/planets"
)

// EventKind distinguishes provisioning events received by the overlay.
type EventKind int

const (
	// PhaseEvent announces that provisioning has entered a new phase.
	PhaseEvent EventKind = iota
	// WarnEvent carries a provisioning warning (non-fatal).
	WarnEvent
	// DoneEvent signals successful completion.
	DoneEvent
	// ErrorEvent signals a fatal provisioning failure.
	ErrorEvent
)

// ProvisionEvent is the message sent from provision.Run to the overlay over
// a buffered channel. One goroutine writes; bubbletea reads.
type ProvisionEvent struct {
	Kind    EventKind
	Phase   string
	Num     int
	Total   int
	Message string
	Err     error
}

// ChannelReporter implements provision.Reporter by pushing events into a
// buffered channel. It tracks the most-recent phase name so Error() can
// report which phase failed. Not safe for concurrent use — callers must
// serialize reporter calls.
type ChannelReporter struct {
	events    chan<- ProvisionEvent
	lastPhase string
}

// NewChannelReporter builds a reporter that writes into events.
func NewChannelReporter(events chan<- ProvisionEvent) *ChannelReporter {
	return &ChannelReporter{events: events}
}

// Phase records the current phase name and dispatches a PhaseEvent.
func (r *ChannelReporter) Phase(name string, num, total int) {
	r.lastPhase = name
	r.events <- ProvisionEvent{
		Kind:  PhaseEvent,
		Phase: name,
		Num:   num,
		Total: total,
	}
}

// Warn dispatches a WarnEvent. The overlay currently ignores warnings, but
// they're captured so a future version could render a warning stack.
func (r *ChannelReporter) Warn(msg string) {
	r.events <- ProvisionEvent{Kind: WarnEvent, Message: msg}
}

// Done dispatches a DoneEvent. Caller should not send further events.
func (r *ChannelReporter) Done() {
	r.events <- ProvisionEvent{Kind: DoneEvent}
}

// Error dispatches an ErrorEvent using the most recently reported phase
// when phase is empty (e.g. when called from a deferred error handler).
func (r *ChannelReporter) Error(phase string, err error) {
	if phase == "" {
		phase = r.lastPhase
	}
	r.events <- ProvisionEvent{Kind: ErrorEvent, Phase: phase, Err: err}
}

// ModelConfig bundles the constructor arguments for NewModel so callers
// (and tests) do not need to remember field order.
type ModelConfig struct {
	Name   string
	Planet planets.Planet
	Total  int
	Events <-chan ProvisionEvent
	Now    func() time.Time // injectable for tests
}

// Model is the bubbletea model driving the provisioning overlay.
type Model struct {
	cfg      ModelConfig
	phase    string
	phaseNum int
	start    time.Time
	err      error
	errPhase string
	done     bool
	spinner  spinner.Model
	width    int
	height   int
}

// NewModel constructs a Model with sensible defaults. Events must be a
// channel the caller feeds from provision.Run goroutines. start is seeded
// from cfg.Now() so the elapsed timer counts up from model construction.
func NewModel(cfg ModelConfig) Model {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Total <= 0 {
		cfg.Total = 14
	}
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	return Model{
		cfg:     cfg,
		start:   cfg.Now(),
		spinner: sp,
	}
}

// tickMsg fires once per second to keep the elapsed timer fresh.
type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// waitForEvent returns a command that blocks on the events channel and
// converts the next event into a tea.Msg so bubbletea can dispatch it.
func waitForEvent(events <-chan ProvisionEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-events
		if !ok {
			// Channel closed without a Done event — treat as done.
			return ProvisionEvent{Kind: DoneEvent}
		}
		return ev
	}
}

// Init is part of the tea.Model interface.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		tickCmd(),
		waitForEvent(m.cfg.Events),
	)
}

// Update is part of the tea.Model interface.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.err != nil {
			// Any key dismisses the error panel.
			m.done = true
			return m, tea.Quit
		}
		if msg.Type == tea.KeyCtrlC {
			m.done = true
			return m, tea.Quit
		}
		return m, nil

	case tickMsg:
		if m.done {
			return m, nil
		}
		return m, tickCmd()

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case ProvisionEvent:
		switch msg.Kind {
		case PhaseEvent:
			m.phase = msg.Phase
			m.phaseNum = msg.Num
			if msg.Total > 0 {
				m.cfg.Total = msg.Total
			}
			return m, waitForEvent(m.cfg.Events)
		case WarnEvent:
			// Drop silently for now.
			return m, waitForEvent(m.cfg.Events)
		case DoneEvent:
			m.done = true
			return m, tea.Quit
		case ErrorEvent:
			m.err = msg.Err
			m.errPhase = msg.Phase
			// Stop draining events; stay on screen until keypress.
			// Further events pushed into the channel are dropped.
			return m, nil
		}
	}
	return m, nil
}

// View is part of the tea.Model interface.
func (m Model) View() string {
	if m.done && m.err == nil {
		return ""
	}
	if m.err != nil {
		return m.errorView()
	}
	return m.loadingView()
}

var nameStyle = lipgloss.NewStyle().Bold(true)

func (m Model) loadingView() string {
	shape := planets.GetShape(m.cfg.Name)
	art := RenderPlanet(shape, m.cfg.Planet, m.phaseNum, m.cfg.Total)

	elapsed := m.cfg.Now().Sub(m.start)
	mm := int(elapsed.Minutes())
	ss := int(elapsed.Seconds()) % 60
	timer := fmt.Sprintf("%02d:%02d", mm, ss)

	planetColor := lipgloss.Color(fmt.Sprintf("#%02x%02x%02x",
		m.cfg.Planet.Color[0], m.cfg.Planet.Color[1], m.cfg.Planet.Color[2]))
	nameLine := nameStyle.Foreground(planetColor).Render(m.cfg.Name)

	phaseLine := fmt.Sprintf("%s  %s", m.spinner.View(), m.phase)

	content := strings.Join([]string{
		art,
		"",
		nameLine,
		"",
		phaseLine,
		"",
		timer,
	}, "\n")

	if m.width == 0 || m.height == 0 {
		// No WindowSizeMsg yet; return un-centered.
		return content
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
}

var errorPanelStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(lipgloss.Color("9")).
	Padding(1, 2)

func (m Model) errorView() string {
	lines := []string{
		"✗  Provisioning failed",
		"",
		fmt.Sprintf("Phase: %s", m.errPhase),
		"",
		"For the full log, run:",
		fmt.Sprintf("  cspace up %s --verbose", m.cfg.Name),
		"",
		"Press any key to exit",
	}
	panel := errorPanelStyle.Render(strings.Join(lines, "\n"))
	if m.width == 0 || m.height == 0 {
		return panel
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, panel)
}

// Run starts the bubbletea program in alt-screen mode and blocks until
// the user dismisses the error panel or provisioning completes.
func Run(cfg ModelConfig) error {
	p := tea.NewProgram(NewModel(cfg), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
