package controlplane

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/pane"
)

// Kind is what a tab holds.
type Kind int

const (
	KindClaude     Kind = iota // the sandbox's interactive claude, in tmux
	KindShell                  // a login shell in the sandbox, in tmux
	KindHostShell              // the operator's own shell, no container
	KindSupervisor             // the event tail and a send box; not a pty
)

func (k Kind) String() string {
	switch k {
	case KindClaude:
		return "claude"
	case KindShell:
		return "shell"
	case KindHostShell:
		return "host shell"
	case KindSupervisor:
		return "supervisor"
	}
	return "pane"
}

// openTimeout bounds one pane open. It has to cover the tmux presence probe
// (10s) plus control's attach lock wait (12s), because a pane opening while
// a `cspace attach` is still identifying its own client legitimately waits
// that lock out.
const openTimeout = 30 * time.Second

// closeTimeout bounds one pane close: a detach-client exec into the sandbox
// plus the engine's own teardown, whose kill grace is two seconds.
const closeTimeout = 20 * time.Second

// sweepTimeout bounds the whole startup sweep, run once from Init before any
// pane can open. It is deliberately its own, more generous budget rather
// than a reuse of openTimeout: the sweep may have to wait out several
// sandboxes' own attach-lock contention (control's attachLockWait) on top of
// several bounded execs per sandbox directory, so a single pane-open's
// budget would risk cutting a legitimate sweep off mid-sandbox.
const sweepTimeout = 60 * time.Second

// The labels the footer shows while a pane is opening or closing, and that
// the corresponding results carry back.
const (
	LabelOpenPane  = "open pane"
	LabelClosePane = "close pane"
)

// Detacher ends one tmux client's attachment. *control.Attachment satisfies
// it; a host-shell pane has none.
type Detacher interface {
	Close(ctx context.Context) error
}

// Opened is what a PaneHost hands back.
type Opened struct {
	Pane   *pane.Pane
	Detach Detacher
	// Warning is a notice the person must read even though the open worked —
	// the no-tmux fallback, whose session will not survive this window.
	Warning string
}

// SweepOutcome is what a PaneHost's Sweep reports: the subset of
// control.SweepResult's counts the operator can act on or at least ought to
// be told about. Kept never crosses this boundary — a kept record is nothing
// happening, and the dashboard has nothing to show for it.
type SweepOutcome struct {
	Detached, Deleted, Errors int
}

// PaneHost opens panes and runs their attach bookkeeping. Like Data and
// Actor it is declared here by the consumer and implemented in internal/cli,
// which is what keeps this package from importing it — and keeps
// internal/pane from ever learning what a sandbox is.
//
// Neither method may be called from Update: Open probes a container and
// takes a file lock, and both would freeze the UI goroutine. Every caller
// goes through a tea.Cmd.
type PaneHost interface {
	Open(ctx context.Context, kind Kind, row control.Row, cols, rows int) (Opened, error)
	// Sweep reaps the client records of attaches whose host process is gone
	// and reports what it did.
	Sweep(ctx context.Context) (SweepOutcome, error)
}

// nopPaneHost is the host a Model built without one gets: every open fails
// with an explanation rather than a nil dereference, which is the same
// fail-closed rule control.Client applies to its own unset seams.
type nopPaneHost struct{}

func (nopPaneHost) Open(context.Context, Kind, control.Row, int, int) (Opened, error) {
	return Opened{}, errors.New("no pane host configured")
}
func (nopPaneHost) Sweep(context.Context) (SweepOutcome, error) { return SweepOutcome{}, nil }

// tab is one entry in the tabs row: a live pane, or the supervisor view.
//
// Tabs are held by pointer, and that is a deliberate exception to the rule
// the Model's doc states about never mutating shared state in place. A tab
// owns a running process and a scrollback — things a Model copy must not
// fork — so the slice is what gets rebuilt on every change (addTab, dropTab)
// while each tab is shared. Nothing mutates a tab from anywhere but the UI
// goroutine.
type tab struct {
	id      int
	kind    Kind
	project string
	sandbox string

	// p is nil for KindSupervisor, which runs no process.
	p *pane.Pane
	// detach is nil for KindHostShell and KindSupervisor, neither of which
	// is a tmux client.
	detach Detacher
	// sup is non-nil only for KindSupervisor (see supervisor.go).
	sup *supervisor

	// closing is set the moment a teardown is handed to a command — by
	// closeTab, or by the paneOutputMsg arm reaping a child that exited on
	// its own — and it exists to stop awaitOutput re-arming on a pane that
	// is going away.
	//
	// The hazard is the opposite of a leak. 4a closes the dirty channel
	// inside Pane.Close (see Pane.Dirty's doc): a receive on a closed pane's
	// Dirty() returns immediately, and forever. An unguarded re-arm is
	// therefore a ~30 Hz message loop on a dead pane — a whole dashboard
	// redrawn at the tick rate for the rest of the session — not a
	// goroutine parked on a channel nothing can refill.
	//
	// closing is this package's own bookkeeping, not the ground truth: a
	// Close that gave up on a wedged waiter (4a) can leave Exited() still
	// reporting the child live even though Dirty is already closed, and once
	// Task 7's host owns Close on its own paths too, a pane can end up
	// closed by code this field never saw set. The paneOutputMsg re-arm
	// therefore also asks p.Closed() directly — the pane's own structural
	// answer — rather than trusting closing/reaped alone.
	closing bool
	// reaped is set once an exited pane's own teardown has finished, so a
	// later signal cannot start a second one. closing covers the window
	// while it is in flight; this covers everything after.
	reaped bool
}

// title is what the tabs row shows: "<project>/<sandbox> · <kind>". A host
// shell belongs to no sandbox, so it says so.
func (t *tab) title() string {
	if t.kind == KindHostShell {
		return "host · shell"
	}
	return fmt.Sprintf("%s/%s · %s", t.project, t.sandbox, t.kind)
}

// focusArea is what the keyboard is pointed at.
type focusArea int

const (
	focusSidebar focusArea = iota
	focusMain
)

// The pane messages.
type (
	// paneOpenedMsg carries one open's outcome. The kind and row travel with
	// it because the selection may have moved while the open was in flight.
	paneOpenedMsg struct {
		kind   Kind
		row    control.Row
		opened Opened
		err    error
	}
	// paneOutputMsg says a pane has new output worth drawing. The id, not an
	// index: tabs are reordered by closes.
	paneOutputMsg struct{ id int }
	// paneClosedMsg reports a teardown, whether or not it went cleanly.
	paneClosedMsg struct {
		id  int
		err error
	}
	// paneReapedMsg reports the teardown of a pane whose child exited on
	// its own. Unlike paneClosedMsg it does NOT drop the tab: an exited
	// pane still shows its last screen, and closing the tab stays the
	// operator's decision.
	paneReapedMsg struct {
		id  int
		err error
	}
	// sweepMsg reports the startup sweep, which is advisory: what it found
	// was already broken, and there is nothing for a person to do about it.
	sweepMsg struct {
		outcome SweepOutcome
		err     error
	}
	// paneNudgeMsg is the one-shot repaint nudge for the tab that just
	// opened, carried by id because a close reorders the slice.
	paneNudgeMsg struct{ id int }
)

// paneNudgeDelay is how long after a tmux-backed pane opens the repaint
// nudge fires. It has to outlast tmux's own attach draw — a Claude pane
// needs ~4-6s to paint its UI, but what the nudge is cleaning up is the
// *first* frame tmux writes, which lands within a few hundred
// milliseconds — and it has to be short enough that nobody has time to read
// the garbled frame. 600ms is the design's number.
const paneNudgeDelay = 600 * time.Millisecond

// nudgeRepaint schedules the repaint nudge for one tab.
//
// The problem it exists for: tmux draws a session it is reattaching at the
// size that session already had, and the app inside then repaints
// incrementally over it, so the emulator's first frame interleaves two
// layouts until something forces a full redraw. A SIGWINCH is what forces
// one, and the operator gets it for free on the next terminal resize — this
// just does it for them, once, immediately.
func nudgeRepaint(id int) tea.Cmd {
	return tea.Tick(paneNudgeDelay, func(time.Time) tea.Msg { return paneNudgeMsg{id: id} })
}

// nudgePane makes a pane repaint by taking it one row off the current
// layout and straight back, which is two SIGWINCHes to the child.
//
// Only a live pane is touched: a tab whose child exited keeps its last
// screen (view_pane.go's exited branch) and resizing it would rewrap a
// frozen frame, and a pane already closed or closing has no pty left to
// take the ioctl. Both errors are dropped — this is cosmetic, and a pane
// whose resize fails has worse problems that its own next write reports.
//
// Called from the UI goroutine, like every other Resize fan-out.
func (m Model) nudgePane(id int) {
	t, _ := m.tabByID(id)
	if t == nil || t.p == nil || t.closing || t.reaped || t.p.Closed() {
		return
	}
	if _, _, exited := t.p.Exited(); exited {
		return
	}
	cols, rows := m.paneSize()
	if rows < 3 {
		return // paneSize's floor is 2; one row less is not a legal size
	}
	_ = t.p.Resize(cols, rows-1)
	_ = t.p.Resize(cols, rows)
}

// sweepCmd reaps the client records of attaches whose host process is gone.
// It runs once, from Init, before any pane can open.
func (m Model) sweepCmd() tea.Cmd {
	host := m.host
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), sweepTimeout)
		defer cancel()
		outcome, err := host.Sweep(ctx)
		return sweepMsg{outcome: outcome, err: err}
	}
}

// openOrFocus focuses the tab for (kind, row) if it exists, and otherwise
// starts opening one.
func (m Model) openOrFocus(kind Kind, row control.Row) (tea.Model, tea.Cmd) {
	// A host shell is exempt from the match: it belongs to no sandbox, so
	// the (kind, project, sandbox) key every other tab is found by is empty
	// for all of them. Asking for one always opens one — two host shells are
	// two tabs, both titled "host · shell" — rather than silently refocusing
	// whichever was opened first.
	if kind != KindHostShell {
		for i, t := range m.tabs {
			if t.kind == kind && t.project == row.Project && t.sandbox == row.Name {
				m.focused = i
				m.focus = focusMain
				m.scrolling, m.scroll = false, 0
				return m, nil
			}
		}
	}
	cols, rows := m.paneSize()
	if kind == KindSupervisor {
		// Nothing to spawn: the supervisor view is a reader. It is sized
		// here and in the window-resize fan-out, and nowhere else — its
		// view is read-only, so this is the only chance a brand-new one
		// gets to learn its height before it is first drawn.
		sup := newSupervisor(cols)
		sup.resize(cols, rows)
		m = m.addTab(&tab{
			kind: kind, project: row.Project, sandbox: row.Name,
			sup: sup,
		})
		return m, m.supervisorEventsCmd(m.tabs[m.focused])
	}
	host := m.host
	return m.startAction(LabelOpenPane, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), openTimeout)
		defer cancel()
		opened, err := host.Open(ctx, kind, row, cols, rows)
		return paneOpenedMsg{kind: kind, row: row, opened: opened, err: err}
	})
}

// addTab appends a tab, focuses it and points the keyboard at it.
func (m Model) addTab(t *tab) Model {
	t.id = m.nextTabID
	m.nextTabID++
	m.tabs = append(append([]*tab{}, m.tabs...), t)
	m.focused = len(m.tabs) - 1
	m.focus = focusMain
	m.scrolling, m.scroll = false, 0
	return m
}

// focusedTab is the tab the main area shows, or nil when there are none.
func (m Model) focusedTab() *tab {
	if m.focused < 0 || m.focused >= len(m.tabs) {
		return nil
	}
	return m.tabs[m.focused]
}

// tabByID finds a tab by identity, which is what every message carries:
// closing a tab shifts every index after it.
func (m Model) tabByID(id int) (*tab, int) {
	for i, t := range m.tabs {
		if t.id == id {
			return t, i
		}
	}
	return nil, -1
}

// awaitOutput waits for one pane's next frame and asks for a redraw. The
// engine's signal channel holds one slot, so a burst collapses into one
// message; the tick in front of it caps the redraw rate at ~30/s, which is
// the design's budget and well under bubbletea's own 60fps renderer.
func awaitOutput(t *tab) tea.Cmd {
	if t.p == nil {
		return nil
	}
	p, id := t.p, t.id
	return tea.Tick(paneRedrawInterval, func(time.Time) tea.Msg {
		<-p.Dirty()
		return paneOutputMsg{id: id}
	})
}

// paneRedrawInterval is the floor between two redraws of one pane.
const paneRedrawInterval = 33 * time.Millisecond

// closeTab runs the design's pane-close order: detach the tmux client, then
// the engine's teardown handshake. control.Attachment.Close does the detach
// and deletes the client's record file together, so the record is gone by
// the time the handshake runs rather than after it — the ordering difference
// is immaterial, since both happen once the client is detached.
func (m Model) closeTab(id int) tea.Cmd {
	t, _ := m.tabByID(id)
	if t == nil {
		return nil
	}
	t.closing = true
	detach, p := t.detach, t.p
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()
		var err error
		if detach != nil {
			err = detach.Close(ctx)
		}
		if p != nil {
			if closeErr := p.Close(ctx); closeErr != nil && err == nil {
				err = closeErr
			}
		}
		return paneClosedMsg{id: id, err: err}
	}
}

// closeDetacher releases an attachment that never became a tab: the pane
// host booked it, then failed to hand back a pane. Nothing else can reach
// it — there is no tab for leader x or quit to find — so the sandbox's
// attach lock and the client record would be held until the process ends.
//
// It is a tea.Cmd rather than an inline call because closing an attachment
// execs into the container to detach the client, which must not run on the
// UI goroutine. A nil detacher is nothing to do, which is the ordinary case.
func closeDetacher(d Detacher) tea.Cmd {
	if d == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()
		_ = d.Close(ctx)
		return nil
	}
}

// reapExited is closeTab's teardown without the drop: the same detach and
// the same engine handshake, run for a pane whose child ended on its own,
// while the tab stays on screen.
//
// It exists because an exited pane is not a closed one. 4a's engine leaves
// the pty master, x/vt's parser buffer, the guest tmux client and this
// attach's record file all live until Close runs, and the child exiting is
// not Close — so without this the commonest way a pane ends would keep every
// one of those until the operator noticed and pressed leader x. What the tab
// keeps is only what a person still wants: the dimmed last screen
// (view_pane.go's exited branch) and the key that dismisses it.
func (m Model) reapExited(t *tab) tea.Cmd {
	id, detach, p := t.id, t.detach, t.p
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()
		var err error
		if detach != nil {
			err = detach.Close(ctx)
		}
		if p != nil {
			if closeErr := p.Close(ctx); closeErr != nil && err == nil {
				err = closeErr
			}
		}
		return paneReapedMsg{id: id, err: err}
	}
}

// dropTab removes a closed tab and re-points the focus at a neighbour,
// falling back to the sidebar when the last one goes.
func (m Model) dropTab(id int) Model {
	_, idx := m.tabByID(id)
	if idx < 0 {
		return m
	}
	tabs := make([]*tab, 0, len(m.tabs)-1)
	tabs = append(tabs, m.tabs[:idx]...)
	tabs = append(tabs, m.tabs[idx+1:]...)
	m.tabs = tabs
	switch {
	case len(m.tabs) == 0:
		m.focused = -1
		m.focus = focusSidebar
	case m.focused >= len(m.tabs):
		m.focused = len(m.tabs) - 1
	}
	m.scrolling, m.scroll = false, 0
	return m
}

// quitCmd tears every open pane down and then quits.
//
// The design says quitting does not confirm, because tmux holds every
// session — but a window that vanished without detaching would leave a
// client attached inside each sandbox and a record file stranded for the
// next start's sweep, so the detach still runs on the way out.
//
// The closes run CONCURRENTLY under one shared deadline, so the wait before
// the program ends does not grow with how many tabs are open — it is O(1) in
// tabs, not one closeTimeout per tab. That said, the ceiling is not exactly
// closeTimeout: ctx only bounds p.Close (4a honours it directly).
// control.Attachment.Close derives its own, ctx-independent waits for its
// tracking goroutine and its detach exec (context.WithoutCancel — see its
// own doc), so the real ceiling is closeTimeout plus whatever those add on
// top. What concurrency buys is that a second wedged sandbox does not add a
// second helping of that on top of the first; one wedged sandbox is still
// enough to make a sequential quit look hung while the operator stares at a
// frozen window.
//
// It is one closure rather than tea.Sequence(closes…, tea.Quit) because the
// closes have to finish before the program ends, and because a sequence's
// own message is unexported and therefore unobservable from a test: the
// thing that must not regress here is that quitting detaches.
func (m Model) quitCmd() tea.Cmd {
	type teardown struct {
		detach Detacher
		p      *pane.Pane
	}
	downs := make([]teardown, 0, len(m.tabs))
	for _, t := range m.tabs {
		if t.detach == nil && t.p == nil {
			continue // the supervisor view owns no process and no client
		}
		t.closing = true
		downs = append(downs, teardown{detach: t.detach, p: t.p})
	}
	if len(downs) == 0 {
		return tea.Quit
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()
		var wg sync.WaitGroup
		for _, d := range downs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				// The errors have nowhere to go — the program is ending —
				// but running these is what performs the detach.
				if d.detach != nil {
					_ = d.detach.Close(ctx)
				}
				if d.p != nil {
					_ = d.p.Close(ctx)
				}
			}()
		}
		wg.Wait()
		return tea.QuitMsg{}
	}
}
