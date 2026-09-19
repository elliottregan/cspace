package controlplane

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"

	"github.com/elliottregan/cspace/internal/control"
)

// No "time" here: nothing in this file schedules. supervisorTail and
// supervisorInputHeight are plain ints, the feed is driven by the medium
// ticker in model.go, and the spinner's tick chain is re-armed there too.

// supervisorTail is how many events the supervisor view reads. Far more than
// the detail band's eight: this is the feed, and the viewport scrolls it.
const supervisorTail = 200

// supervisorInputHeight is how many lines the send box gets.
const supervisorInputHeight = 3

// supervisor is the read-only window on the headless agent: the event tail
// with the assistant's text rendered as markdown, a box for the next turn,
// and a spinner while it is working.
//
// It is not a pane. There is no pty and no child — the events come off the
// host's own session directory through control.Events, and sending goes
// through the supervisor's HTTP control port like `cspace send` does.
type supervisor struct {
	vp    viewport.Model
	input textarea.Model
	spin  spinner.Model

	md    *glamour.TermRenderer
	mdErr error

	width, height int
	lines         []control.EventLine
	working       bool
	// reading is set while this tab's own Events read is in flight, so a
	// read slower than the medium interval cannot stack up behind itself.
	reading bool
	err     error
}

func newSupervisor(width int) *supervisor {
	ta := textarea.New()
	ta.Placeholder = "send a turn"
	ta.ShowLineNumbers = false
	ta.CharLimit = 4000
	// Enter must submit, and textarea binds it to InsertNewline by default.
	ta.KeyMap.InsertNewline.SetEnabled(false)
	ta.SetHeight(supervisorInputHeight)
	ta.Focus()

	s := &supervisor{
		vp:    viewport.New(viewport.WithWidth(width), viewport.WithHeight(1)),
		input: ta,
		spin:  spinner.New(spinner.WithSpinner(spinner.Dot)),
		width: width,
	}
	s.rebuildRenderer()
	return s
}

// rebuildRenderer builds the markdown renderer at the current width.
// glamour v2 bakes the wrap width at construction — there is no setter — so
// a resize has to make a new one.
func (s *supervisor) rebuildRenderer() {
	width := s.width
	if width < 20 {
		width = 20
	}
	md, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(styles.DarkStyle),
		glamour.WithWordWrap(width),
	)
	s.md, s.mdErr = md, err
}

func (s *supervisor) resize(width, height int) {
	rewrap := width != s.width
	if rewrap {
		s.width = width
		s.rebuildRenderer()
	}
	s.height = height
	s.vp.SetWidth(width)
	s.input.SetWidth(width)
	body := height - supervisorInputHeight - 1
	if body < 1 {
		body = 1
	}
	s.vp.SetHeight(body)
	if rewrap {
		// The content was wrapped at the old width by the old renderer, so
		// a new one is not enough on its own — the feed has to be rendered
		// again through it.
		atBottom := s.vp.AtBottom()
		s.vp.SetContent(s.render())
		if atBottom {
			_ = s.vp.GotoBottom()
		}
	}
}

// setEvents replaces the feed and follows it when the view was already at
// the bottom — the reading rule every log window wants: a person who has
// scrolled back stays where they were, and one who has not sees the newest.
func (s *supervisor) setEvents(lines []control.EventLine) {
	atBottom := s.vp.AtBottom()
	s.lines = lines
	s.vp.SetContent(s.render())
	if atBottom {
		_ = s.vp.GotoBottom()
	}
}

// render turns the feed into the viewport's content: assistant text through
// glamour, everything else as one dim line — the design's open question 2,
// whose default is exactly this.
func (s *supervisor) render() string {
	if len(s.lines) == 0 {
		return styleDim.Render("no agent events yet")
	}
	var b strings.Builder
	for _, e := range s.lines {
		if e.Text != "" {
			b.WriteString(s.markdown(e.Text))
			b.WriteString("\n")
		}
		for _, tool := range e.Tools {
			b.WriteString(styleDim.Render("  ⟡ " + tool))
			b.WriteString("\n")
		}
		if e.Text == "" && len(e.Tools) == 0 {
			label := e.Type
			if e.Subtype != "" {
				label += "/" + e.Subtype
			}
			b.WriteString(styleDim.Render(fmt.Sprintf("  %s %s", shortTs(e.Ts), label)))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// markdown renders one block, falling back to the raw text when glamour
// could not be built or chokes on the input. A feed that stopped showing the
// agent's words because a renderer failed would be worse than an ugly one.
func (s *supervisor) markdown(text string) string {
	if s.md == nil || s.mdErr != nil {
		return text
	}
	out, err := s.md.Render(text)
	if err != nil {
		return text
	}
	return strings.TrimRight(out, "\n")
}

// view is the whole tab: the feed, a rule, and the send box.
//
// It is READ-ONLY, and that is load-bearing rather than tidy. Model.View
// is the only caller, Model's own doc insists nothing shared is mutated in
// place, and resize is the opposite of that — it can rebuild the glamour
// renderer and call vp.SetContent. A supervisor learns its geometry in
// exactly two places instead: openOrFocus, when the tab is created, and
// model.go's tea.WindowSizeMsg fan-out. Both pass the same numbers this
// function is handed, because View sizes the main area from paneSize()
// itself rather than recomputing the same arithmetic beside it.
func (s *supervisor) view(width, height int) string {
	status := styleDim.Render(strings.Repeat("─", max(1, width)))
	if s.working {
		status = s.spin.View() + " " + styleDim.Render("working · esc interrupts")
	} else if s.err != nil {
		status = styleErr.Render(fit("events unavailable: "+s.err.Error(), width))
	}
	return fitLines(strings.Join([]string{s.vp.View(), status, s.input.View()}, "\n"), height)
}

// supervisorEventsMsg is one supervisor tab's own, longer tail. It is keyed
// by tab id rather than by sandbox: two supervisor tabs on two sandboxes are
// two feeds, and a reply that landed late must not fill the wrong one.
type supervisorEventsMsg struct {
	id    int
	lines []control.EventLine
	err   error
}

// supervisorEventsCmd reads one supervisor tab's feed. It is a no-op while
// that tab's previous read is still out.
func (m Model) supervisorEventsCmd(t *tab) tea.Cmd {
	if t == nil || t.sup == nil || t.sup.reading {
		return nil
	}
	t.sup.reading = true
	data, id, project, sandbox := m.data, t.id, t.project, t.sandbox
	return func() tea.Msg {
		lines, err := data.Events(project, sandbox, supervisorTail)
		return supervisorEventsMsg{id: id, lines: lines, err: err}
	}
}

// supervisorTickCmds refreshes every open supervisor tab. The medium cadence
// drives it, which is the cadence the design assigns to "Events for the open
// supervisor view".
func (m Model) supervisorTickCmds() []tea.Cmd {
	var cmds []tea.Cmd
	for _, t := range m.tabs {
		if cmd := m.supervisorEventsCmd(t); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

// handleSupervisorKey is the supervisor tab's keyboard. The box owns it:
// Enter sends, Esc interrupts, the page keys scroll the feed, and everything
// else is text. The leader is handled before this is ever reached.
//
// The two arms that start an action check m.action first, because this route
// does not pass through handleNormalKey, where the one-action-at-a-time gate
// lives. Without it a second action can be started under the first: both
// emit an actionResultMsg, the first to land clears the gate for the other,
// and the footer reports whichever verb arrived last. Typing is never gated
// — only the keys that act.
//
// They also check the same reachability predicates the sidebar's own keys
// are disabled by (KeyMap.forRow). The spec's error handling says a
// supervisor that cannot be reached has send and interrupt DISABLED on that
// row rather than failing on press; the sidebar gets that by disabling the
// binding, which is not available here because these keys are a switch on
// the raw string rather than bindings. Saying so in the footer and sending
// nothing is the same contract by the other route — and the typed text
// survives, so Enter again once the agent answers sends it.
func (m Model) handleSupervisorKey(t *tab, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Both actions take the same row, read out of the current row set
	// rather than remembered: it is hoisted here so the two arms cannot
	// drift apart, and it is cheap — sandboxRow is a scan of a list the
	// dashboard already holds. The whole row, not a rebuilt identity: the
	// gates below read its agent status, which a synthesized row would
	// report unreachable for every sandbox.
	row := sandboxRow(m.rows, t.project, t.sandbox)
	live := m.live[keyOf(row)]

	switch msg.String() {
	case "enter":
		if !canSend(row, live) {
			m.notice = notice{text: "supervisor unreachable: nothing to send to", isErr: true}
			return m, nil
		}
		if m.action != "" {
			// The gate, checked before the box is read: a turn thrown away
			// because the last action had not landed yet would be the worst
			// possible reading of "busy". The text stays, and Enter again
			// once the footer clears sends it.
			return m, nil
		}
		text := strings.TrimSpace(t.sup.input.Value())
		t.sup.input.Reset()
		if text == "" {
			return m, nil
		}
		return m.startAction(LabelSend, m.actor.Send(row, text))
	case "esc":
		if !canInterrupt(row, live) {
			m.notice = notice{text: "nothing to interrupt: the agent is not working", isErr: true}
			return m, nil
		}
		if m.action != "" {
			return m, nil
		}
		return m.startAction(LabelInterrupt, m.actor.Interrupt(row))
	case "pgup":
		t.sup.vp.PageUp()
		return m, nil
	case "pgdown":
		t.sup.vp.PageDown()
		return m, nil
	}
	var cmd tea.Cmd
	t.sup.input, cmd = t.sup.input.Update(msg)
	return m, cmd
}

// sandboxRow finds a supervisor tab's sandbox in the current row set. A
// supervisor tab holds only the identity — the row it was opened from may
// have been rebuilt by a poll since, and its container name and agent
// status are both things a poll changes.
//
// A sandbox that has left the row set entirely falls back to the identity
// alone: no container to act on and no agent to reach, which is what the
// gates should then say.
func sandboxRow(rows []control.Row, project, sandbox string) control.Row {
	for _, r := range rows {
		if r.Kind == control.RowSandbox && r.Project == project && r.Name == sandbox {
			return r
		}
	}
	return control.Row{Kind: control.RowSandbox, Project: project, Name: sandbox}
}
