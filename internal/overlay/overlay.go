package overlay

import (
	"fmt"
	"io"
	"os"
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
	// LogEvent carries a sub-phase detail line (e.g. the current plugin
	// being installed). The overlay keeps a short tail of the most recent.
	LogEvent
	// PortEvent announces that a host port mapping has come online.
	// The overlay accumulates them in arrival order.
	PortEvent
	// WarnEvent carries a provisioning warning (non-fatal).
	WarnEvent
	// DoneEvent signals successful completion.
	DoneEvent
	// ErrorEvent signals a fatal provisioning failure.
	ErrorEvent
)

// logTailLen caps how many recent reporter.Log lines the overlay shows
// beneath the phase header. Three is enough to feel live without
// pushing the progress bar off-screen on short terminals.
const logTailLen = 3

// ProvisionEvent is the message sent from provision.Run to the overlay over
// a buffered channel. One goroutine writes; bubbletea reads.
//
// Field aliasing by Kind:
//   - PhaseEvent:  Phase, Num, Total
//   - LogEvent:    Message
//   - PortEvent:   Label, URL
//   - WarnEvent:   Message
//   - ErrorEvent:  Phase, Err
type ProvisionEvent struct {
	Kind    EventKind
	Phase   string
	Num     int
	Total   int
	Message string
	Label   string
	URL     string
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

// Log dispatches a LogEvent with a sub-phase detail line.
func (r *ChannelReporter) Log(msg string) {
	r.events <- ProvisionEvent{Kind: LogEvent, Message: msg}
}

// Port dispatches a PortEvent announcing a host port mapping.
func (r *ChannelReporter) Port(label, url string) {
	r.events <- ProvisionEvent{Kind: PortEvent, Label: label, URL: url}
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

// portEntry is one row in the overlay's port listing.
type portEntry struct {
	Label string
	URL   string
}

// Model is the bubbletea model driving the provisioning overlay.
type Model struct {
	cfg      ModelConfig
	phase    string
	phaseNum int
	logs     []string // most recent sub-phase detail lines (len ≤ logTailLen)
	ports    []portEntry
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
			m.logs = nil // fresh phase starts with an empty log tail
			if msg.Total > 0 {
				m.cfg.Total = msg.Total
			}
			return m, waitForEvent(m.cfg.Events)
		case LogEvent:
			m.logs = append(m.logs, msg.Message)
			if len(m.logs) > logTailLen {
				m.logs = m.logs[len(m.logs)-logTailLen:]
			}
			return m, waitForEvent(m.cfg.Events)
		case PortEvent:
			m.ports = append(m.ports, portEntry{Label: msg.Label, URL: msg.URL})
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
	return SciFiLabelFor(phase)
}

// SciFiLabelFor returns the stylized status word the overlay shows for a
// provisioning phase (e.g. "Configuring hosts" → "CROSSLINK"). Exported
// so the preview tool in cmd/overlay-web can label frames consistently.
func SciFiLabelFor(phase string) string {
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

// Panel geometry. Planet art is ShapeCols wide × ShapeRows/2 rows tall.
// HUD column sits to its right. Panel width = planet + gap + hud + padding
// + border; the outer frame renders in a 4:3-ish landscape proportion.
const (
	hudColWidth    = 32
	gapWidth       = 4
	panelPaddingX  = 3
	panelPaddingY  = 1
	labelColWidth  = 10 // left column in the stat grid
	valueColWidth  = hudColWidth - labelColWidth
	progressBarLen = hudColWidth - 12 // leave room for percentage
)

var (
	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(hudAccent).
			BorderBackground(viewportBgColor).
			Background(viewportBgColor).
			Padding(panelPaddingY, panelPaddingX)

	// panelFill paints the viewport black across the exact cell width so
	// there's no transparent gutter between styled text and panel border.
	panelFill = lipgloss.NewStyle().Background(viewportBgColor)

	hudColStyle = lipgloss.NewStyle().
			Background(viewportBgColor).
			Width(hudColWidth)

	gapStyle = lipgloss.NewStyle().
			Background(viewportBgColor).
			Width(gapWidth)

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

	labelStyle = lipgloss.NewStyle().
			Foreground(hudDim).
			Background(viewportBgColor).
			Width(labelColWidth)

	valueStyle = lipgloss.NewStyle().
			Foreground(hudAccent).
			Background(viewportBgColor).
			Bold(true).
			Width(valueColWidth)

	nameValueStyle = lipgloss.NewStyle().
			Background(viewportBgColor).
			Bold(true).
			Width(valueColWidth)

	errorPanelStyle = lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(errorAccent).
			BorderBackground(viewportBgColor).
			Background(viewportBgColor).
			Padding(panelPaddingY, panelPaddingX).
			Width(hudColWidth + gapWidth + planets.ShapeCols)
)

// renderProgressBar draws a solid-filled bar: [██████████░░░░░░░░]  050.0%
// with bright silver filled cells and dim silver empty cells.
func renderProgressBar(progress float64) string {
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}
	filled := int(progress * float64(progressBarLen))
	if filled > progressBarLen {
		filled = progressBarLen
	}
	empty := progressBarLen - filled

	return hudBaseStyle.Render("[") +
		hudAccentStyle.Render(strings.Repeat("█", filled)) +
		hudDimStyle.Render(strings.Repeat("░", empty)) +
		hudBaseStyle.Render("]") +
		hudBaseStyle.Render(" ") +
		hudAccentStyle.Render(fmt.Sprintf("%05.1f%%", progress*100))
}

// renderStatRow produces one `LABEL    value` line sized to the HUD column.
func renderStatRow(label, value string, accent lipgloss.Style) string {
	labelPart := labelStyle.Render(label)
	valuePart := accent.Render(value)
	return hudColStyle.Render(labelPart + valuePart)
}

// hudLines returns the stacked HUD rows used in the loading view.
func (m Model) hudLines() []string {
	planetHex := fmt.Sprintf("#%02x%02x%02x",
		m.cfg.Planet.Color[0], m.cfg.Planet.Color[1], m.cfg.Planet.Color[2])
	planetStyle := nameValueStyle.Foreground(lipgloss.Color(planetHex))

	// Progress reflects phases *completed*, not entered: we subtract one
	// from phaseNum while the phase is running. Without this, the bar
	// reads 100 % during the final (long) "Installing plugins" phase —
	// which looks like a stall. DoneEvent flips m.done and the view
	// disappears before the bar would otherwise reach 100 %.
	progress := 0.0
	if m.cfg.Total > 0 && m.phaseNum > 0 {
		progress = float64(m.phaseNum-1) / float64(m.cfg.Total)
	}
	elapsed := m.cfg.Now().Sub(m.start)
	mm := int(elapsed.Minutes())
	ss := int(elapsed.Seconds()) % 60

	blank := hudColStyle.Render("")
	header := hudColStyle.Render(
		hudAccentStyle.Render(m.spinner.View()+"  ") +
			hudAccentStyle.Render("CSPACE // ORBITAL INIT"),
	)
	rule := hudColStyle.Render(hudDimStyle.Render(strings.Repeat("─", hudColWidth-2)))

	lines := []string{
		blank,
		header,
		blank,
		rule,
		blank,
		renderStatRow("TARGET", strings.ToUpper(m.cfg.Name), planetStyle),
		renderStatRow("PHASE", fmt.Sprintf("%02d / %02d", m.phaseNum, m.cfg.Total), valueStyle),
		renderStatRow("T+", fmt.Sprintf("%02d:%02d", mm, ss), valueStyle),
		renderStatRow("STATE", sciFiLabelFor(m.phase), valueStyle),
		blank,
		hudColStyle.Render(renderProgressBar(progress)),
	}
	if len(m.ports) > 0 {
		lines = append(lines, blank, hudColStyle.Render(hudDimStyle.Render("UPLINKS")))
		for _, p := range m.ports {
			lines = append(lines, hudColStyle.Render(
				hudAccentStyle.Render(" ↗ ")+formatPortLine(p),
			))
		}
	}
	lines = append(lines,
		blank,
		hudColStyle.Render(hudBaseStyle.Render("›  ")+hudBaseStyle.Render(m.phase)),
	)
	for _, entry := range m.logs {
		lines = append(lines, hudColStyle.Render(
			hudDimStyle.Render("   • ")+hudDimStyle.Render(truncate(entry, hudColWidth-5)),
		))
	}
	return lines
}

// formatPortLine renders one port row as e.g. "app    :30001" — host
// portion only, sized to fit the HUD column. The full URL is reserved
// for the log-reporter (verbose) path.
func formatPortLine(p portEntry) string {
	host := p.URL
	// Strip "http://localhost" prefix when present so the row fits.
	const localhost = "http://localhost"
	host = strings.TrimPrefix(host, localhost)
	label := truncate(p.Label, 16)
	// Pad label to a consistent column so ports align across rows.
	padded := label + strings.Repeat(" ", max(0, 16-runeLen(label)))
	return hudBaseStyle.Render(padded) + hudAccentStyle.Render(host)
}

func runeLen(s string) int { return len([]rune(s)) }

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// truncate returns s clipped to max runes with an ellipsis suffix when
// the input exceeds the budget. Used to keep log-tail entries inside
// the HUD column width.
func truncate(s string, max int) string {
	if max <= 1 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

func (m Model) loadingView() string {
	shape := planets.GetShape(m.cfg.Name)
	opts := DefaultRenderOptions()
	opts.Overlays = planets.GetOverlays(m.cfg.Name)
	art := RenderPlanetWith(shape, m.cfg.Planet, m.phaseNum, m.cfg.Total, opts)

	artHeight := planets.ShapeRows / 2
	hud := m.hudLines()
	// Pad HUD to match planet height so JoinHorizontal aligns cleanly and
	// the black viewport fills from planet-top to planet-bottom.
	for len(hud) < artHeight {
		hud = append(hud, hudColStyle.Render(""))
	}
	if len(hud) > artHeight {
		hud = hud[:artHeight]
	}
	hudBlock := strings.Join(hud, "\n")

	gap := strings.Repeat(gapStyle.Render("")+"\n", artHeight-1) + gapStyle.Render("")

	combined := lipgloss.JoinHorizontal(lipgloss.Top, art, gap, hudBlock)
	panel := panelStyle.Render(combined)

	if m.width == 0 || m.height == 0 {
		return panel
	}
	return lipgloss.Place(m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		panel,
		lipgloss.WithWhitespaceBackground(lipgloss.Color("#000000")),
	)
}

func (m Model) errorView() string {
	lines := []string{
		hudAccentStyle.Render("✕  MISSION ABORT"),
		panelFill.Render(""),
	}
	if m.errPhase != "" {
		lines = append(lines,
			hudBaseStyle.Render("FAULT IN PHASE: ")+hudAccentStyle.Render(strings.ToUpper(sciFiLabelFor(m.errPhase))),
			hudDimStyle.Render(m.errPhase),
			panelFill.Render(""),
		)
	}
	lines = append(lines,
		hudBaseStyle.Render("»  ")+hudAccentStyle.Render("cspace up "+m.cfg.Name+" --verbose"),
		panelFill.Render(""),
		hudDimStyle.Render("[ANY KEY] TO DISENGAGE"),
	)
	panel := errorPanelStyle.Render(strings.Join(lines, "\n"))
	if m.width == 0 || m.height == 0 {
		return panel
	}
	return lipgloss.Place(m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		panel,
	)
}

// Run starts the bubbletea program in alt-screen mode and blocks until
// the user dismisses the error panel or provisioning completes.
func Run(cfg ModelConfig) error {
	return RunOn(cfg, os.Stdout)
}

// RunOn is Run with an explicit output writer. Callers that need to
// redirect the process's os.Stdout (e.g. to silence leaking subprocess
// output) pass the original os.Stdout here so bubbletea keeps writing
// to the real terminal.
func RunOn(cfg ModelConfig, out io.Writer) error {
	p := tea.NewProgram(NewModel(cfg), tea.WithAltScreen(), tea.WithOutput(out))
	_, err := p.Run()
	return err
}
