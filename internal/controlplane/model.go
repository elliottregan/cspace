package controlplane

import (
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/elliottregan/cspace/internal/control"
)

// uiMode is what the keyboard is currently doing. modeConfirmDown and
// modeInput are declared here because paused() reads them; the states
// themselves are entered in input.go and confirm.go.
type uiMode int

const (
	modeNormal uiMode = iota
	modeConfirmDown
	modeInput
)

// noticeLifetime is how long a success notice stays in the footer. Error
// notices stay until the next keypress instead.
const noticeLifetime = 3 * time.Second

// notice is a transient footer message. isErr marks the ones that take the
// alert style and stay until the next keypress — failures, and the warnings
// ResultWarn carries. Everything else fades after noticeLifetime.
type notice struct {
	text  string
	isErr bool
}

// noticeExpireMsg clears a success notice. gen guards against a stale timer
// clearing a newer notice.
type noticeExpireMsg struct{ gen int }

// Model is the dashboard. It is a value type with value-receiver
// Init/Update/View, like every other Bubble Tea model in this repo: mutate
// the local m and return it.
//
// Its maps are never mutated in place. Update assigns a freshly built map
// instead, because a Model is copied on every Update and an in-place write
// would be visible to a copy that had already been handed elsewhere.
type Model struct {
	data  Data
	actor Actor
	keys  KeyMap
	help  help.Model
	now   func() time.Time

	rows     []control.Row
	selected int
	daemon   control.DaemonHealth
	snapErr  error
	lastSnap time.Time

	live   map[sandboxKey]liveState
	memory map[string]int64 // container name -> live usage bytes
	ports  map[sandboxKey][]control.Port
	// portsErr is keyed like ports: a failed probe belongs to the sandbox it
	// failed for. One shared error made a sandbox that stopped between two
	// slow ticks render "ports unavailable" in every other sandbox's band.
	portsErr map[sandboxKey]error

	events    []control.EventLine
	eventsErr error

	pollingFast   bool
	pollingMedium bool
	pollingSlow   bool

	// slowSeeded marks that the slow cadence's first poll has already been
	// kicked off out of band, by the first successful snapshot rather than
	// by slowTickMsg. Without it, a freshly started dashboard shows no ports
	// for a full slowInterval — the first snapshot has sandboxes worth
	// asking about well before the first 10s tick arrives.
	slowSeeded bool

	mode    uiMode
	confirm *huh.Form
	// pending is the row a prompt (the send box or the teardown
	// confirmation) was opened against. Its completion path acts on this,
	// not on selectedRow(): moveSelection or a snapshot landing while the
	// prompt is open must not retarget an action already named at a
	// specific sandbox. Set when entering modeInput/modeConfirmDown,
	// cleared on every exit from either mode.
	pending  control.Row
	showHelp bool
	input    textinput.Model
	action   string // in-flight action label; "" when idle
	spinner  spinner.Model

	notice    notice
	noticeGen int

	width, height int
	quitting      bool
}

// New builds the dashboard over the query and action seams and the resolved
// keymap. Nothing is polled until Init runs.
func New(data Data, actor Actor, keys KeyMap) Model {
	ti := textinput.New()
	ti.Placeholder = "message"
	ti.CharLimit = 2000
	return Model{
		data:     data,
		actor:    actor,
		keys:     keys,
		help:     help.New(),
		now:      time.Now,
		input:    ti,
		spinner:  spinner.New(spinner.WithSpinner(spinner.Dot)),
		live:     map[sandboxKey]liveState{},
		memory:   map[string]int64{},
		ports:    map[sandboxKey][]control.Port{},
		portsErr: map[sandboxKey]error{},
	}
}

// Init starts all three cadences through their own tick messages rather than
// issuing the queries directly: the tick handlers own the in-flight guards
// and the re-arm, and a first poll that went around them could race the
// first real tick.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		func() tea.Msg { return fastTickMsg{} },
		func() tea.Msg { return mediumTickMsg{} },
		func() tea.Msg { return slowTickMsg{} },
	)
}

// paused reports whether a cadence should skip its poll this time round: a
// modal owns the screen, and attach owns the terminal outright (the program
// is suspended into `container exec`), so neither is a moment to replace the
// row set underneath the person.
//
// Every other action is deliberately *not* paused. They run as ordinary
// commands with the dashboard fully on screen, and they are the long ones —
// `up` is bounded at ten minutes — so pausing on them would freeze every row
// on the host for the whole boot. Watching a booting sandbox turn ○ and gain
// its ports is exactly what the poll loop is for. The one-action-at-a-time
// gate lives in handleNormalKey and is unaffected by this.
func (m Model) paused() bool { return m.mode != modeNormal || m.action == "attach" }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.SetWidth(msg.Width)
		if m.mode == modeInput {
			m.input.SetWidth(sendInputWidth(m.pending.Name, msg.Width))
		}
		return m, nil

	case fastTickMsg:
		cmds := []tea.Cmd{tea.Tick(fastInterval, func(t time.Time) tea.Msg { return fastTickMsg{at: t} })}
		if !m.pollingFast && !m.paused() {
			m.pollingFast = true
			cmds = append(cmds, m.liveCmd())
		}
		return m, tea.Batch(cmds...)

	case mediumTickMsg:
		cmds := []tea.Cmd{tea.Tick(mediumInterval, func(t time.Time) tea.Msg { return mediumTickMsg{at: t} })}
		if !m.pollingMedium && !m.paused() {
			m.pollingMedium = true
			cmds = append(cmds, m.snapshotCmd())
		}
		return m, tea.Batch(cmds...)

	case slowTickMsg:
		cmds := []tea.Cmd{tea.Tick(slowInterval, func(t time.Time) tea.Msg { return slowTickMsg{at: t} })}
		if !m.pollingSlow && !m.paused() {
			m.pollingSlow = true
			cmds = append(cmds, m.slowCmd())
		}
		return m, tea.Batch(cmds...)

	case liveMsg:
		m.pollingFast = false
		m.live = msg.states
		return m, nil

	case snapshotMsg:
		m.pollingMedium = false
		m.applySnapshot(msg.snap)
		cmds := []tea.Cmd{m.eventsCmd()}
		// Seed the slow cadence right after the first successful snapshot
		// rather than waiting out the first slowInterval: ports and stats
		// would otherwise stay blank for up to 10s after a fresh start,
		// even though the row set worth asking about is already known.
		// This runs once; the regular 10s tick chain is untouched.
		if msg.snap.Err == nil && !m.slowSeeded && !m.pollingSlow && len(sandboxTargets(m.rows)) > 0 {
			m.slowSeeded = true
			m.pollingSlow = true
			cmds = append(cmds, m.slowCmd())
		}
		return m, tea.Batch(cmds...)

	case slowMsg:
		m.pollingSlow = false
		// Ports first, then the snapshot: applySnapshot drops the entries
		// this sample carries for rows its own, newer row set reports as
		// stopped. The other order would let a sample taken against the
		// previous row set reinstate them.
		m.ports, m.portsErr = msg.ports, msg.portsErr
		m.applySnapshot(msg.snap)
		return m, nil

	case eventsMsg:
		// A tail read for the row that used to be selected can land after
		// the one read for the row that is selected now — the reads are
		// concurrent and neither cancels the other. Dropping the mismatched
		// one is what stops another sandbox's events rendering under this
		// sandbox's name until the next medium tick.
		if msg.key != keyOf(m.selectedRow()) {
			return m, nil
		}
		m.events, m.eventsErr = msg.lines, msg.err
		return m, nil

	case actionResultMsg:
		m.action = ""
		if msg.err != nil {
			// Error notices stay until the next keypress.
			m.notice = notice{text: msg.label + " failed: " + msg.err.Error(), isErr: true}
			return m, nil
		}
		if msg.warn != "" {
			// A warning is not a failure, but it is the thing the person
			// most needs to read — so it takes the alert style and the
			// same stays-until-dismissed lifetime an error gets, rather
			// than fading on a three-second timer.
			m.notice = notice{text: msg.warn, isErr: true}
			return m, nil
		}
		m.notice = notice{text: msg.label + " ok"}
		m.noticeGen++
		gen := m.noticeGen
		return m, tea.Tick(noticeLifetime, func(time.Time) tea.Msg { return noticeExpireMsg{gen: gen} })

	case noticeExpireMsg:
		if msg.gen == m.noticeGen && !m.notice.isErr {
			m.notice = notice{}
		}
		return m, nil

	case spinner.TickMsg:
		// Animate only while an action is in flight; when idle let the tick
		// chain die rather than redraw a whole dashboard forever.
		if m.action == "" {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tea.KeyPressMsg:
		// Ctrl+C is not configurable and is never routed to a modal: a
		// dashboard with no way out is a bug.
		if key.Matches(msg, forceQuit) {
			m.quitting = true
			return m, tea.Quit
		}
		return m.handleKey(msg)
	}

	// Anything the branches above did not consume goes to whichever widget
	// currently owns the keyboard — a textinput's cursor blink, a form's
	// own timers. The tick and data messages are handled above, so a widget
	// can never swallow a poll's re-arm.
	switch m.mode {
	case modeInput:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	case modeConfirmDown:
		if m.confirm != nil {
			return m.updateConfirm(msg)
		}
	}
	return m, nil
}

// applySnapshot folds a poll's rows into the model. A failed `container ls`
// keeps the last-known rows and only records the error: the footer marks how
// stale they are, and the sidebar never blanks. control leaves Daemon zeroed
// on its error paths too, so m.daemon is only updated on success — otherwise
// a snapshot failure would flip the tabs line from "daemon 1.0.0-rc.48" to
// "daemon unreachable" on every poll error, which is not what happened.
func (m *Model) applySnapshot(snap control.Snapshot) {
	prev := m.selectedRow()
	m.snapErr = snap.Err
	if snap.Err != nil {
		return
	}
	m.daemon = snap.Daemon
	m.rows = snap.Rows
	m.lastSnap = snap.TakenAt
	m.memory = mergeMemory(m.memory, snap.Rows)
	m.ports, m.portsErr = dropStalePorts(m.ports, m.portsErr, snap.Rows)
	m.restoreSelection(prev)
}

// dropStalePorts keeps only the port lists — and port errors — that still
// describe a live sandbox. Ports arrive on the slow cadence alone, so without
// this a sandbox that stopped would keep advertising URLs that answer nothing
// for up to a full slowInterval, and a probe error would outlive the row it
// was about. Same rule mergeMemory applies to usage: a stopped row drops its
// sample, and a row that vanished drops out entirely.
func dropStalePorts(ports map[sandboxKey][]control.Port, errs map[sandboxKey]error, rows []control.Row) (map[sandboxKey][]control.Port, map[sandboxKey]error) {
	outPorts := make(map[sandboxKey][]control.Port, len(ports))
	outErrs := make(map[sandboxKey]error, len(errs))
	for _, r := range rows {
		if r.Kind != control.RowSandbox || r.State == control.StateStopped {
			continue
		}
		k := keyOf(r)
		if p, ok := ports[k]; ok {
			outPorts[k] = p
		}
		if e, ok := errs[k]; ok {
			outErrs[k] = e
		}
	}
	return outPorts, outErrs
}

// mergeMemory carries live usage forward across the snapshots that skip
// `container stats`. A row's MemoryUsedB is 0 both when no sample was taken
// and when the container is stopped, so a zero never clears a remembered
// value — but a stopped container drops its, rather than reporting a number
// from before it died, and a container that disappeared drops out entirely.
func mergeMemory(prev map[string]int64, rows []control.Row) map[string]int64 {
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		if r.Container == "" || r.State == control.StateStopped {
			continue
		}
		switch {
		case r.MemoryUsedB > 0:
			out[r.Container] = r.MemoryUsedB
		case prev[r.Container] > 0:
			out[r.Container] = prev[r.Container]
		}
	}
	return out
}

// selectedRow is the current selection, or a zero Row when there is none.
func (m Model) selectedRow() control.Row {
	if m.selected >= 0 && m.selected < len(m.rows) {
		return m.rows[m.selected]
	}
	return control.Row{}
}

// moveSelection moves to the next selectable row in direction dir (+1/-1),
// skipping project headers, sidecars and system rows.
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

// restoreSelection re-points the selection at the row matching prev's
// identity after a new snapshot. When that row is gone — a teardown, say —
// it moves to the nearest remaining selectable row, searching outward from
// the old index rather than jumping to the top.
//
// The old index can land past the end of a shrunk row set entirely (a busy
// host's rows collapsing while the selection sat deep in the list), in which
// case every probe in the outward search is out of range. Rather than fall
// straight to index 0 — a project header, not a sandbox — a last linear scan
// picks the first selectable row that exists at all.
func (m *Model) restoreSelection(prev control.Row) {
	for i, r := range m.rows {
		if r.Selectable && r.Kind == prev.Kind && r.Project == prev.Project && r.Name == prev.Name {
			m.selected = i
			return
		}
	}
	n := len(m.rows)
	for d := 0; d < n; d++ {
		for _, idx := range [2]int{m.selected + d, m.selected - d} {
			if idx >= 0 && idx < n && m.rows[idx].Selectable {
				m.selected = idx
				return
			}
		}
	}
	for i, r := range m.rows {
		if r.Selectable {
			m.selected = i
			return
		}
	}
	m.selected = 0
}
