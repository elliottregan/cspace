package controlplane

import (
	"fmt"
	"strings"
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
	modePicker
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
	host  PaneHost

	tabs      []*tab
	focused   int // index into tabs; -1 when there are none
	nextTabID int
	focus     focusArea

	// scrolling and scroll are the focused pane's scrollback position:
	// scroll is how many lines above the live screen the view sits, and
	// scrolling is whether the arrow keys are moving it rather than reaching
	// the child. Both reset whenever the focused tab changes.
	scrolling bool
	scroll    int

	keys KeyMap
	help help.Model
	now  func() time.Time

	// leaderArmed is whether the leader's first key has been pressed and the
	// next key is its second key rather than an ordinary one. It resets the
	// moment that next key is dispatched, in handleKey — there is no mode to
	// get stuck in.
	leaderArmed bool

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
	// picker is the new-pane picker's form, arriving in Task 4; the field is
	// declared here so mainArea's modePicker branch compiles now.
	picker *huh.Form
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
	// geom is where the last layout put everything, so a mouse message can
	// be hit-tested without rendering. Update refreshes it after every
	// message; View never writes it.
	geom geometry
	// quitting is nothing in production — tea.Quit is what actually ends the
	// program — but it is the observable two tests assert on: that `q` quits
	// from the sidebar and does *not* from inside the send box, where the
	// same key is text. It is a test seam, not dead state; deleting it takes
	// the only check on that rule with it.
	quitting bool
}

// New builds the dashboard over the query, action and pane seams and the
// resolved keymap. Nothing is polled and nothing is opened until Init runs.
func New(data Data, actor Actor, host PaneHost, keys KeyMap) Model {
	ti := textinput.New()
	ti.Placeholder = "message"
	ti.CharLimit = 2000
	if host == nil {
		host = nopPaneHost{}
	}
	return Model{
		data:     data,
		actor:    actor,
		host:     host,
		keys:     keys,
		help:     help.New(),
		now:      time.Now,
		input:    ti,
		spinner:  spinner.New(spinner.WithSpinner(spinner.Dot)),
		live:     map[sandboxKey]liveState{},
		memory:   map[string]int64{},
		ports:    map[sandboxKey][]control.Port{},
		portsErr: map[sandboxKey]error{},
		focused:  -1,
	}
}

// Init starts all three cadences through their own tick messages rather than
// issuing the queries directly: the tick handlers own the in-flight guards
// and the re-arm, and a first poll that went around them could race the
// first real tick.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.sweepCmd(),
		func() tea.Msg { return fastTickMsg{} },
		func() tea.Msg { return mediumTickMsg{} },
		func() tea.Msg { return slowTickMsg{} },
	)
}

// paused reports whether a cadence should skip its poll this time round: a
// modal owns the screen, and an open pane in flight owns the row it is
// opening against, so neither is a moment to replace the row set underneath
// the person.
//
// Every other action is deliberately *not* paused. They run as ordinary
// commands with the dashboard fully on screen, and they are the long ones —
// `up` is bounded at ten minutes — so pausing on them would freeze every row
// on the host for the whole boot. Watching a booting sandbox turn ○ and gain
// its ports is exactly what the poll loop is for. The one-action-at-a-time
// gate lives in handleNormalKey and is unaffected by this.
func (m Model) paused() bool {
	return m.mode != modeNormal || m.action == LabelOpenPane
}

// Update refreshes the layout geometry after handling a message, and does
// nothing else the method below does not.
//
// The refresh is here rather than at each of update's thirty-odd return
// points, and rather than in View, which has a value receiver and can store
// nothing. It runs on every message, including a pane's ~30/s redraw
// signal: it re-renders the row list and the tabs — the same work View
// does for those two regions, and small beside the emulator render that
// same signal triggers.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	mm, ok := next.(Model)
	if !ok {
		// Unreachable: every return in update is a Model. Kept so a future
		// arm that returns something else degrades to "no geometry" rather
		// than panicking under the operator's cursor.
		return next, cmd
	}
	mm.geom = mm.computeGeometry()
	return mm, cmd
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.SetWidth(msg.Width)
		if m.mode == modeInput {
			m.input.SetWidth(sendInputWidth(m.pending.Name, msg.Width))
		}
		cols, rows := m.paneSize()
		for _, t := range m.tabs {
			if t.p != nil {
				// The error is the ioctl's; a pane whose pty has gone will
				// be reaped by its own exit, and failing the resize of one
				// must not stop the others.
				_ = t.p.Resize(cols, rows)
			}
			if t.sup != nil {
				t.sup.resize(m.paneWidth(), rows)
			}
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
			cmds = append(cmds, m.supervisorTickCmds()...)
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

	case supervisorEventsMsg:
		if t, _ := m.tabByID(msg.id); t != nil && t.sup != nil {
			t.sup.err = msg.err
			t.sup.setEvents(msg.lines)
			t.sup.reading = false
			// Whether the agent is working is the fast ticker's answer, not
			// a guess from the tail. "The last event is not a result" can
			// never go false — events.ndjson carries lines that are not
			// sdk-events at all, and any tail ending in one of those would
			// leave the spinner's tick chain alive for the rest of the
			// session, redrawing the whole dashboard at spinner cadence.
			// AgentStatus is what the fast cadence already polls for exactly
			// this question.
			working := m.live[sandboxKey{Project: t.project, Name: t.sandbox}].Agent.State == "working"
			started := working && !t.sup.working
			t.sup.working = working
			if started {
				// Start this spinner's own tick chain. bubbles tags each
				// TickMsg with the spinner's id and drops the ones that are
				// not its own, so every spinner needs its own chain — the
				// model's own animates only while an Actor action is in
				// flight, which a supervisor read is not.
				return m, t.sup.spin.Tick
			}
		}
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

	case sweepMsg:
		// Advisory: a swept record was already stale. Only a failure to run
		// the sweep at all is worth a sticky line — isErr, because that
		// branch schedules no expiry, and a notice that is neither timed nor
		// dismissible would otherwise sit in the footer for the rest of the
		// session. A successful sweep that nonetheless found unfinished work
		// (SweepOutcome.Errors — a record it could not decide, not a call
		// that failed outright) is still just information, so it takes the
		// same timed notice a Detached/Deleted count does; there is nothing
		// for the operator to do about any of these three besides know.
		if msg.err != nil {
			m.notice = notice{text: "attach sweep: " + msg.err.Error(), isErr: true}
			return m, nil
		}
		o := msg.outcome
		if o.Detached == 0 && o.Deleted == 0 && o.Errors == 0 {
			return m, nil
		}
		var parts []string
		if o.Detached > 0 {
			parts = append(parts, fmt.Sprintf("detached %d", o.Detached))
		}
		if o.Deleted > 0 {
			parts = append(parts, fmt.Sprintf("deleted %d", o.Deleted))
		}
		if o.Errors > 0 {
			noun := "error"
			if o.Errors != 1 {
				noun = "errors"
			}
			parts = append(parts, fmt.Sprintf("%d %s", o.Errors, noun))
		}
		m.notice = notice{text: "startup sweep: " + strings.Join(parts, ", ")}
		m.noticeGen++
		gen := m.noticeGen
		return m, tea.Tick(noticeLifetime, func(time.Time) tea.Msg { return noticeExpireMsg{gen: gen} })

	case noticeExpireMsg:
		if msg.gen == m.noticeGen && !m.notice.isErr {
			m.notice = notice{}
		}
		return m, nil

	case spinner.TickMsg:
		// Each spinner has its own id and its own chain; Update drops a tick
		// that is not its own, so both are fed and whichever one it belonged
		// to re-arms.
		var cmds []tea.Cmd
		for _, t := range m.tabs {
			if t.sup != nil && t.sup.working {
				var cmd tea.Cmd
				t.sup.spin, cmd = t.sup.spin.Update(msg)
				cmds = append(cmds, cmd)
			}
		}
		// Animate the model's own only while an action is in flight; when
		// idle let its chain die rather than redraw a whole dashboard
		// forever.
		if m.action != "" {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case paneOpenedMsg:
		m.action = ""
		if msg.err != nil {
			m.notice = notice{text: LabelOpenPane + " failed: " + msg.err.Error(), isErr: true}
			return m, nil
		}
		if msg.opened.Pane == nil {
			// A PaneHost that reports success with no pane would otherwise
			// become a tab with a nil p — every place that reads it already
			// treats nil as "no process" (KindSupervisor's own tabs), so
			// nothing downstream would crash, but the tab could never draw,
			// resize, redraw or close: a zombie the operator can neither use
			// nor get rid of. Treat it as the failure it is instead.
			m.notice = notice{text: LabelOpenPane + " failed: host returned no pane", isErr: true}
			// A host that booked an attachment and then failed to hand
			// back a pane still took the sandbox's attach lock and wrote a
			// client record. Nothing else will ever close that: no tab is
			// made, so no leader x and no quit can reach it.
			return m, closeDetacher(msg.opened.Detach)
		}
		project, sandbox := msg.row.Project, msg.row.Name
		if msg.kind == KindHostShell {
			// It was opened from whatever row happened to be selected and
			// belongs to none of them: leave the identity empty rather than
			// let a later reader take it for a pane on that sandbox.
			project, sandbox = "", ""
		}
		// Re-apply the current layout before the tab exists. The size that
		// went into Open was the one at the keypress, and a Claude pane
		// takes seconds to appear — a WindowSizeMsg landing in between
		// resizes every tab there is, and this one was not yet among them.
		// The error is the pane's to report on its next write; there is no
		// tab to hang a notice on yet.
		_ = msg.opened.Pane.Resize(m.paneSize())
		m = m.addTab(&tab{
			kind:    msg.kind,
			project: project,
			sandbox: sandbox,
			p:       msg.opened.Pane,
			detach:  msg.opened.Detach,
		})
		wait := awaitOutput(m.tabs[m.focused])
		if msg.opened.Warning != "" {
			// Through ResultWarn rather than written into m.notice here:
			// "it worked, now read this" already has a mechanism, and the
			// actionResultMsg arm is where the rule that such a notice is
			// sticky lives. A second copy of that rule is a second place to
			// forget it.
			warning := msg.opened.Warning
			return m, tea.Batch(wait, func() tea.Msg { return ResultWarn(LabelOpenPane, warning) })
		}
		return m, wait

	case paneOutputMsg:
		// The redraw is the Update itself; the rest is deciding whether to
		// wait again. A tab closed while its wait was in flight drops the
		// message, and so does one whose teardown is already running.
		t, _ := m.tabByID(msg.id)
		if t == nil || t.p == nil {
			return m, nil
		}
		_, _, exited := t.p.Exited()
		if exited {
			// The child ended on its own, and this signal — the waiter's
			// final markDirty — is the last one this pane will ever emit:
			// Dirty is closed by Pane.Close and by nothing else. Tear the
			// pane down here, or its tmux client stays attached inside the
			// sandbox and its record file on the host until the operator
			// presses leader x. The tab survives the reap.
			if !t.closing && !t.reaped {
				t.closing = true
				return m, m.reapExited(t)
			}
			return m, nil
		}
		// t.p.Closed() is the structural backstop Exited() alone cannot
		// promise: a Close that gave up on a wedged waiter (4a) — or, once
		// Task 7's host owns Close on its own paths, any close this tab's
		// own bookkeeping never saw — leaves Dirty already closed while
		// Exited() still reports the child live. Re-arming on that pane
		// would spin the redraw loop at the tick rate forever, waiting on a
		// channel nothing will ever refill again.
		if !t.closing && !exited && !t.p.Closed() {
			return m, awaitOutput(t)
		}
		return m, nil

	case paneClosedMsg:
		m.action = ""
		m = m.dropTab(msg.id)
		if msg.err != nil {
			m.notice = notice{text: LabelClosePane + ": " + msg.err.Error(), isErr: true}
		}
		return m, nil

	case paneReapedMsg:
		// No dropTab and no m.action: the reap was nobody's action, and the
		// tab stays to show the dead pane's last screen. Clearing closing
		// and setting reaped is what stops a second signal starting the
		// teardown again; dropping the detacher is what keeps a later
		// leader x from closing an attachment this already closed (Pane.Close
		// is idempotent on its own).
		if t, _ := m.tabByID(msg.id); t != nil {
			t.closing, t.reaped, t.detach = false, true, nil
		}
		if msg.err != nil {
			m.notice = notice{text: LabelClosePane + ": " + msg.err.Error(), isErr: true}
		}
		return m, nil

	case tea.KeyPressMsg:
		// Ctrl+C quits from everywhere except a live pane, which is what
		// helpView promises. Only a running child earns the exemption —
		// interrupting Claude is the single most-used key — and the
		// exemption is exactly as wide as that reason. A modal over a pane
		// must not swallow the one key that always gets out; neither must
		// a supervisor tab, whose textarea binds no ctrl+c at all, nor an
		// exited pane, whose key handler drops the key on the floor. Both
		// of those used to leave leader q as the only way out.
		if key.Matches(msg, forceQuit) && !m.childOwnsKeyboard() {
			m.quitting = true
			return m, m.quitCmd()
		}
		return m.handleKey(msg)

	case tea.PasteMsg:
		// A paste reaches a live pane only when nothing else owns the
		// keyboard: modeNormal, focused on the main area, on a real
		// process. Every other case falls through to the mode switch below
		// instead of being handled here — modeInput's textinput inserts a
		// paste itself (bracketed paste is on by default in bubbletea v2,
		// so without this fall-through a paste into the send box would
		// silently vanish), and a modal open over a focused pane (the
		// picker, the teardown confirm) must not leak the paste through to
		// the child behind it.
		if m.mode == modeNormal && m.focus == focusMain {
			if t := m.focusedTab(); t != nil {
				if t.p != nil {
					t.p.Paste(msg.Content)
					return m, nil
				}
				if t.sup != nil {
					// A supervisor tab's send box is a textarea, which
					// handles PasteMsg itself — but only if it is given
					// one. Returning unconditionally here is what used to
					// swallow a pasted stack trace or diff, which is the
					// obvious thing to put in that box.
					var cmd tea.Cmd
					t.sup.input, cmd = t.sup.input.Update(msg)
					return m, cmd
				}
			}
			return m, nil
		}

	case tea.MouseClickMsg:
		return m.handleClick(msg)

	case tea.MouseWheelMsg:
		return m.handleWheel(msg)

	case tea.MouseReleaseMsg, tea.MouseMotionMsg:
		// Cell motion mode reports a release for every click, and motion
		// while a button is held — a drag. The design has no drag gesture
		// and forwards nothing to the child, so both are dropped HERE
		// rather than left to fall through to the widget switch at the
		// bottom of Update, which would hand them to a textinput or a huh
		// form.
		return m, nil
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
	case modePicker:
		if m.picker != nil {
			return m.updatePicker(msg)
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

// childOwnsKeyboard reports whether the keys are reaching a running child
// rather than the dashboard. It is the exemption Ctrl+C is tested against,
// and the reason for it is the whole of the condition: there has to be a
// live process for the interrupt to mean anything.
//
// A supervisor tab has no process (t.p is nil) and its textarea binds no
// ctrl+c; an exited pane's key handler drops every key. In both, Ctrl+C
// reaching "the pane" means Ctrl+C doing nothing at all. The help overlay
// is the same story from the other side: it covers the pane without moving
// focus off it (see view.go's cursor guard, which excludes it for the same
// reason), and handleKey dismisses it and swallows the key that opened it —
// so with it up, "the pane" is not reachable either.
func (m Model) childOwnsKeyboard() bool {
	if m.mode != modeNormal || m.focus != focusMain || m.showHelp {
		return false
	}
	t := m.focusedTab()
	if t == nil || t.p == nil {
		return false
	}
	_, _, exited := t.p.Exited()
	return !exited
}

// paneSize is the emulator geometry for the main area: the window less the
// sidebar and the one column of padding on each side, and less the tabs row
// and the footer. Floored so a very small window still gets a legal size.
func (m Model) paneSize() (cols, rows int) {
	cols = m.paneWidth()
	rows = m.height - 2 // the tabs row and the footer
	if rows < 2 {
		rows = 2
	}
	return cols, rows
}

// paneWidth is the main area's usable width, which the supervisor view wraps
// its markdown to as well.
func (m Model) paneWidth() int {
	w := mainWidthFor(m.width) - 2 // styleMain's padding
	if w < 4 {
		w = 4
	}
	return w
}
