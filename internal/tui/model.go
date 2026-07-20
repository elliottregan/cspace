package tui

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type uiMode int

const (
	modeNormal uiMode = iota
	modeConfirmDown
	modeInput
)

// notice is a transient footer message. err notices persist until the next
// keypress; success notices fade (the model just clears them on the next tick).
type notice struct {
	text  string
	isErr bool
}

// Model is the dashboard state. Value type with value-receiver Init/Update/View
// (mirrors internal/overlay): mutate the local m and return it.
type Model struct {
	poller   Poller
	actor    Actor
	home     string
	interval time.Duration
	now      func() time.Time

	rows     []Row
	selected int // index into rows; always kept on a Selectable row
	daemon   DaemonHealth
	snapErr  error
	lastPoll time.Time
	polling  bool

	events    []EventLine
	eventsErr error

	mode      uiMode
	input     textinput.Model
	action    string // in-flight action label; "" when idle
	spinner   spinner.Model
	notice    notice
	noticeGen int // bumped per success notice so a stale fade-timer can't clear a newer one
	help      bool

	width, height int
	quitting      bool
}

// message types
type snapshotMsg struct{ snap Snapshot }
type eventsMsg struct {
	lines []EventLine
	err   error
}
type pollTickMsg time.Time

// noticeExpireMsg fires ~3s after a success notice is set; the generation guard
// (gen) ensures a stale timer can't clear a newer notice.
type noticeExpireMsg struct{ gen int }

// NewModel constructs the dashboard model. home is the host home dir (for the
// event-log path); interval is the poll cadence; now is injected for tests.
func NewModel(p Poller, a Actor, home string, interval time.Duration, now func() time.Time) Model {
	ti := textinput.New()
	ti.Placeholder = "message"
	ti.CharLimit = 2000
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	return Model{
		poller: p, actor: a, home: home, interval: interval, now: now,
		input: ti, spinner: sp,
	}
}

func (m Model) Init() tea.Cmd {
	// Kick the first poll through the SAME guarded pollTickMsg path (rather than
	// issuing pollNowCmd directly from Init, which can't set m.polling and would
	// race a second poll from the first real tick). The spinner is seeded only
	// when an action starts (see actionResultMsg / action dispatch), not here —
	// this is a long-running dashboard, so we don't want a perpetual idle redraw.
	return func() tea.Msg { return pollTickMsg(time.Time{}) }
}

func pollTickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return pollTickMsg(t) })
}

// pollNowCmd runs one poll off the UI goroutine; each poll gets its own bounded
// context so a wedged probe can't hang forever.
func (m Model) pollNowCmd() tea.Cmd {
	p := m.poller
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return snapshotMsg{snap: p.Poll(ctx)}
	}
}

func (m Model) readEventsCmd() tea.Cmd {
	row := m.selectedRow()
	if row.Kind != RowSandbox {
		return func() tea.Msg { return eventsMsg{} }
	}
	home, project, name := m.home, row.Project, row.Name
	return func() tea.Msg {
		lines, err := TailEvents(SessionEventsPath(home, project, name), 8)
		return eventsMsg{lines: lines, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case pollTickMsg:
		var cmds []tea.Cmd
		cmds = append(cmds, pollTickCmd(m.interval)) // always re-arm
		// pause polling while a modal or action is active, or a poll is in flight
		if !m.polling && m.mode == modeNormal && m.action == "" {
			m.polling = true
			cmds = append(cmds, m.pollNowCmd())
		}
		return m, tea.Batch(cmds...)

	case snapshotMsg:
		prev := m.selectedRow()
		m.daemon = msg.snap.Daemon
		m.snapErr = msg.snap.Err
		m.polling = false
		// Only a successful `container ls` replaces the row set. On failure keep
		// the last-known rows and let the view mark them stale with an "as of"
		// marker (spec: Error Handling — render what we have, don't blank).
		if msg.snap.Err == nil {
			m.rows = msg.snap.Rows
			m.lastPoll = msg.snap.TakenAt
			m.restoreSelection(prev)
		}
		return m, m.readEventsCmd()

	case eventsMsg:
		m.events, m.eventsErr = msg.lines, msg.err
		return m, nil

	case actionResultMsg:
		m.action = ""
		if msg.err != nil {
			// Error notices persist until the next keypress (cleared in handleKey).
			m.notice = notice{text: msg.label + " failed: " + msg.err.Error(), isErr: true}
			return m, nil
		}
		// Success notices fade after ~3s; a generation guard prevents a stale
		// timer from clearing a newer notice.
		m.notice = notice{text: msg.label + " ok"}
		m.noticeGen++
		gen := m.noticeGen
		return m, tea.Tick(3*time.Second, func(time.Time) tea.Msg { return noticeExpireMsg{gen: gen} })

	case spinner.TickMsg:
		// Only animate while an action is in flight; when idle, let the tick
		// chain die instead of redrawing the whole dashboard forever.
		if m.action == "" {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case noticeExpireMsg:
		// Clear a success notice ~3s after it was set, unless a newer notice
		// superseded it (generation guard) or it became an error notice.
		if msg.gen == m.noticeGen && !m.notice.isErr {
			m.notice = notice{}
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	// While the send-box is open, deliver any message we didn't consume above
	// (cursor BlinkMsg, clipboard pasteMsg) to the focused textinput so its
	// cursor keeps blinking and ctrl+v paste completes. textinput.Update no-ops
	// when the field is unfocused, so this is safe.
	if m.mode == modeInput {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// An error notice persists until the next keypress (spec §Footer); any key
	// dismisses it. Success notices fade on their own timer — leave them.
	if m.notice.isErr {
		m.notice = notice{}
	}
	// global quit always available
	if msg.Type == tea.KeyCtrlC {
		m.quitting = true
		return m, tea.Quit
	}
	switch m.mode {
	case modeConfirmDown:
		return m.handleConfirmKey(msg)
	case modeInput:
		return m.handleInputKey(msg)
	}
	return m.handleNormalKey(msg)
}

func (m Model) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		m.quitting = true
		return m, tea.Quit
	case "up", "k":
		m.moveSelection(-1)
		return m, m.readEventsCmd()
	case "down", "j":
		m.moveSelection(1)
		return m, m.readEventsCmd()
	case "r":
		if !m.polling {
			m.polling = true
			return m, m.pollNowCmd()
		}
		return m, nil
	case "?":
		m.help = !m.help
		return m, nil
	}
	// action keys — gated while another action is in flight
	if m.action != "" {
		return m, nil
	}
	row := m.selectedRow()
	switch msg.String() {
	case "enter":
		if canAttach(row) {
			m.action = "attach"
			return m, tea.Batch(m.actor.Attach(row), m.spinner.Tick)
		}
	case "d":
		if canDown(row) {
			m.mode = modeConfirmDown
		}
	case "i":
		if canInterrupt(row) {
			m.action = "interrupt"
			return m, tea.Batch(m.actor.Interrupt(row), m.spinner.Tick)
		}
	case "b":
		if canBrowser(row) {
			m.action = "browser restart"
			return m, tea.Batch(m.actor.RestartBrowser(row), m.spinner.Tick)
		}
	case "s":
		if canSend(row) {
			m.mode = modeInput
			m.input.SetValue("")
			return m, m.input.Focus()
		}
	}
	return m, nil
}

func (m Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "y" {
		m.mode = modeNormal
		row := m.selectedRow()
		m.action = "down"
		return m, tea.Batch(m.actor.Down(row), m.spinner.Tick)
	}
	m.mode = modeNormal // any other key cancels
	return m, nil
}

func (m Model) handleInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		text := m.input.Value()
		m.mode = modeNormal
		m.input.Blur()
		if text == "" {
			return m, nil
		}
		row := m.selectedRow()
		m.action = "send"
		return m, tea.Batch(m.actor.Send(row, text), m.spinner.Tick)
	case tea.KeyEsc:
		m.mode = modeNormal
		m.input.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// selectedRow returns the currently selected row, or a zero Row if none.
func (m Model) selectedRow() Row {
	if m.selected >= 0 && m.selected < len(m.rows) {
		return m.rows[m.selected]
	}
	return Row{}
}

// moveSelection moves to the next/prev Selectable row in direction dir (+1/-1).
func (m *Model) moveSelection(dir int) {
	n := len(m.rows)
	for i := 1; i <= n; i++ {
		idx := m.selected + dir*i
		if idx < 0 || idx >= n {
			return
		}
		if m.rows[idx].Selectable {
			m.selected = idx
			return
		}
	}
}

// restoreSelection re-points m.selected at the row matching prev's identity
// after a new snapshot. If that row is gone (e.g. after a down), it moves to the
// nearest remaining selectable row — searching outward from the previous index
// — rather than jumping to the top. Falls back to the first selectable row.
func (m *Model) restoreSelection(prev Row) {
	for i, r := range m.rows {
		if r.Selectable && r.Kind == prev.Kind && r.Project == prev.Project && r.Name == prev.Name {
			m.selected = i
			return
		}
	}
	// nearest-selectable search outward from the old index (still held in m.selected)
	n := len(m.rows)
	for d := 0; d < n; d++ {
		for _, idx := range [2]int{m.selected + d, m.selected - d} {
			if idx >= 0 && idx < n && m.rows[idx].Selectable {
				m.selected = idx
				return
			}
		}
	}
	m.selected = 0
}

// View is implemented in view.go.
