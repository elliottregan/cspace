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
	sp.Spinner = spinner.Spinner{
		Frames: []string{"◐", "◓", "◑", "◒"},
		FPS:    time.Second / 4,
	}
	sp.Style = hudAccentStyle
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

// sciFiLabels styles each provisioning phase as a terse 2001-style status
// word. Falls back to the raw phase name when unknown (e.g. custom labels
// like "Reusing running container" that are also present in the map).
var sciFiLabels = map[string]string{
	"Validating name":           "DESIGNATE",
	"Reusing running container": "RESUME SEQUENCE",
	"Removing orphans":          "CLEARING VECTOR",
	"Bundling repo":             "PACKAGE PAYLOAD",
	"Creating volumes":          "FORMAT BANKS",
	"Creating network":          "LINK SUBSTRATE",
	"Starting reverse proxy":    "ENGAGE UPLINK",
	"Setting up directories":    "ALIGN BULKHEAD",
	"Starting containers":       "IGNITION",
	"Waiting for container":     "HANDSHAKE",
	"Configuring hosts":         "CROSSLINK",
	"Setting permissions":       "ACCESS CALIBRATE",
	"Initializing workspace":    "BOOT SEQUENCE",
	"Configuring git & env":     "CODE UPLINK",
	"Installing plugins":        "LOAD MODULES",
}

func sciFiLabelFor(phase string) string {
	if s, ok := sciFiLabels[phase]; ok {
		return s
	}
	return strings.ToUpper(phase)
}

// Cold monochrome HUD palette. Planet art carries per-planet color; every
// other chrome element uses these greys so the interface feels like a
// single cold instrument panel.
var (
	viewportBgColor = lipgloss.Color("#000000")
	hudAccent       = lipgloss.Color("#d4d4d4") // bright silver — borders, focus words
	hudBase         = lipgloss.Color("#9a9a9a") // mid silver — regular text
	hudDim          = lipgloss.Color("#606060") // dim silver — subtext, empty bar cells
	errorAccent     = lipgloss.Color("#e05555") // warm red — fault chrome only
)

var (
	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(hudAccent).
			BorderBackground(viewportBgColor).
			Background(viewportBgColor).
			Padding(1, 4)

	panelInnerStyle = lipgloss.NewStyle().
			Background(viewportBgColor)

	hudBaseStyle = lipgloss.NewStyle().
			Foreground(hudBase).
			Background(viewportBgColor)

	hudAccentStyle = lipgloss.NewStyle().
			Foreground(hudAccent).
			Background(viewportBgColor).
			Bold(true)

	hudDimStyle = lipgloss.NewStyle().
			Foreground(hudDim).
			Background(viewportBgColor)

	nameStyle = lipgloss.NewStyle().
			Bold(true).
			Background(viewportBgColor)

	errorPanelStyle = lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(errorAccent).
			BorderBackground(viewportBgColor).
			Background(viewportBgColor).
			Padding(1, 4)
)

// progressBarWidth is the character count of the filled+empty segments
// (brackets excluded). 24 fits comfortably under a 48-col planet.
const progressBarWidth = 24

// renderProgressBar draws a solid-filled bar: [██████████░░░░░░░░░░░░░░]
// with bright silver for filled cells and dim silver for empty ones, plus
// a trailing percentage. Progress is clamped to [0, 1].
func renderProgressBar(progress float64) string {
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}
	filled := int(progress * float64(progressBarWidth))
	if filled > progressBarWidth {
		filled = progressBarWidth
	}
	empty := progressBarWidth - filled

	filledStr := hudAccentStyle.Render(strings.Repeat("█", filled))
	emptyStr := hudDimStyle.Render(strings.Repeat("░", empty))
	bracket := hudBaseStyle.Render
	pct := hudAccentStyle.Render(fmt.Sprintf(" %05.1f%%", progress*100))

	return bracket("[") + filledStr + emptyStr + bracket("]") + pct
}

func (m Model) loadingView() string {
	shape := planets.GetShape(m.cfg.Name)
	art := RenderPlanet(shape, m.cfg.Planet, m.phaseNum, m.cfg.Total)

	planetColor := lipgloss.Color(fmt.Sprintf("#%02x%02x%02x",
		m.cfg.Planet.Color[0], m.cfg.Planet.Color[1], m.cfg.Planet.Color[2]))
	nameLine := nameStyle.Foreground(planetColor).
		Render(strings.ToUpper(m.cfg.Name))

	progress := 0.0
	if m.cfg.Total > 0 {
		progress = float64(m.phaseNum) / float64(m.cfg.Total)
	}
	progressLine := renderProgressBar(progress)

	sciFi := sciFiLabelFor(m.phase)
	statusTop := hudAccentStyle.Render(m.spinner.View()+"  ") + hudAccentStyle.Render(sciFi)
	statusBot := hudDimStyle.Render("    " + m.phase)

	elapsed := m.cfg.Now().Sub(m.start)
	mm := int(elapsed.Minutes())
	ss := int(elapsed.Seconds()) % 60
	stats := hudBaseStyle.Render(fmt.Sprintf("PH %02d/%02d    T+ %02d:%02d",
		m.phaseNum, m.cfg.Total, mm, ss))

	inner := lipgloss.JoinVertical(lipgloss.Center,
		art,
		panelInnerStyle.Render(""),
		nameLine,
		panelInnerStyle.Render(""),
		progressLine,
		panelInnerStyle.Render(""),
		statusTop,
		statusBot,
		panelInnerStyle.Render(""),
		stats,
	)

	panel := panelStyle.Render(inner)
	if m.width == 0 || m.height == 0 {
		return panel
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, panel)
}

func (m Model) errorView() string {
	lines := []string{
		hudAccentStyle.Render("✕  MISSION ABORT"),
		panelInnerStyle.Render(""),
	}
	if m.errPhase != "" {
		lines = append(lines,
			hudBaseStyle.Render("FAULT IN PHASE: "+strings.ToUpper(sciFiLabelFor(m.errPhase))),
			hudDimStyle.Render(m.errPhase),
			panelInnerStyle.Render(""),
		)
	}
	lines = append(lines,
		hudBaseStyle.Render("»  cspace up "+m.cfg.Name+" --verbose"),
		panelInnerStyle.Render(""),
		hudDimStyle.Render("[ANY KEY] TO DISENGAGE"),
	)
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
