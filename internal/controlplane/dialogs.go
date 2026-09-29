package controlplane

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/elliottregan/cspace/internal/control"
)

type detailsDialog struct {
	row         control.Row
	environment bool
	project     string
	selected    string
	focus       focusArea
	paneID      int
	message     string
}
type dialogAction struct {
	id, label, url string
	row            control.Row
}
type dialogLine struct {
	text   string
	action *dialogAction
}

func (m Model) hasModal() bool {
	return m.showHelp || m.mode == modeDetails || m.mode == modeCreate || m.mode == modePicker || m.mode == modeConfirmDown
}
func (m Model) modalFrame() (rect, int, int) {
	inner := max(8, min(84, m.width-8))
	body := max(1, min(24, m.height-8))
	w, h := inner+4, body+6
	return rect{max(0, (m.width-w)/2), max(0, (m.height-h)/2), w, h}, inner, body
}
func (m Model) openDetails(row control.Row, environment bool) (tea.Model, tea.Cmd) {
	m.mode = modeDetails
	m.modalScroll = 0
	m.dialog = &detailsDialog{row: row, environment: environment, project: row.Project, focus: m.focus}
	if t := m.focusedTab(); t != nil {
		m.dialog.paneID = t.id
	}
	m.events, m.eventsErr = nil, nil
	if environment && row.Project == "" {
		m.dialog.project = m.activeProject()
	}
	return m, tea.Batch(m.eventsCmd(), m.headerFor(row, false, true))
}
func (m Model) detailsLines(width int) []dialogLine {
	var lines []dialogLine
	add := func(text string) {
		for _, s := range strings.Split(ansi.Hardwrap(text, max(1, width), true), "\n") {
			lines = append(lines, dialogLine{text: s})
		}
	}
	action := func(id, label, url string, row control.Row) {
		a := dialogAction{id: id, label: label, url: url, row: row}
		for _, text := range strings.Split(ansi.Hardwrap(label, max(1, width-2), true), "\n") {
			lines = append(lines, dialogLine{text: text, action: &a})
		}
	}
	d := m.dialog
	if d == nil {
		return nil
	}
	if d.environment {
		add("Environment · " + d.project)
		add("")
		daemon := "unreachable"
		if m.daemon.Reachable {
			daemon = m.daemon.Version
		}
		add("Daemon: " + daemon)
		add("Registry: http://127.0.0.1:6280 · DNS :5354")
		if d.message != "" {
			add("")
			add("Action failed")
			add(d.message)
		}
		if m.snapErr != nil {
			add("")
			add("Container discovery failed")
			add(m.snapErr.Error())
			add("Last successful container snapshot: " + formatAge(m.lastSnap, m.now()))
			add("Check Apple Container with: container system status")
		}
		for _, r := range m.rows {
			if r.Kind == control.RowBrowser && r.Project == d.project {
				add("")
				add(r.Name + " · " + stateLabel(r))
				add(r.Container)
				add("Uptime: " + formatUptime(r.Uptime) + "   Memory: " + formatMemUsage(m.memory[r.Container], r.MemoryB))
				if r.IP != "" {
					add("Address: " + r.IP)
				}
				health := "unreachable"
				if r.Browser.Reachable {
					health = r.Browser.Version
				}
				add(fmt.Sprintf("CDP :%d · %s", control.BrowserCDPPort, health))
				action("restart", "Restart browser", "", r)
			}
			if r.Kind == control.RowSystem {
				add("")
				add(r.Name + " · " + stateLabel(r))
				add(r.Container)
				add("Memory: " + formatMemUsage(m.memory[r.Container], r.MemoryB))
			}
		}
		return lines
	}
	r := d.row
	found := false
	for _, candidate := range m.rows {
		if candidate.Kind == r.Kind && candidate.Project == r.Project && candidate.Name == r.Name {
			r = candidate
			found = true
			break
		}
	}
	if !found {
		add("Container is no longer in the latest snapshot.")
	}
	k := keyOf(r)
	add(r.Project + " / " + r.Name + " · " + stateLabel(r))
	add("")
	add("Container: " + r.Container)
	if r.IP != "" {
		add("Address: " + r.IP)
	}
	add("Uptime: " + formatUptime(r.Uptime) + "   Memory: " + formatMemUsage(m.memory[r.Container], r.MemoryB))
	h := m.headers[k]
	if h.status.Branch != "" {
		branch := h.status.Branch
		if h.status.Dirty {
			branch += " (modified)"
		}
		add("Branch: " + branch)
	}
	if h.status.PR != nil {
		pr := h.status.PR
		label := fmt.Sprintf("Open PR #%d · %s", pr.Number, pr.CheckStatus)
		if h.status.PRStale {
			label += " · stale"
		}
		action("pr", label, pr.URL, r)
	} else if h.err == nil && h.status.Branch != "" && !h.status.Detached {
		add("No open PR for this branch.")
	}
	if h.err != nil {
		add("Repository status: " + h.err.Error())
	}
	add("")
	add("Services")
	services := m.services[k]
	if services == nil {
		for _, p := range m.ports[k] {
			services = append(services, control.ServiceStatus{Port: p.Port, Label: p.Label, URL: p.URL, State: control.ServiceRunning})
		}
	}
	if len(services) == 0 {
		add("No services discovered.")
	}
	for _, s := range services {
		label := s.Label
		if label == "" {
			label = "service"
		}
		text := fmt.Sprintf("%s :%d · %s", label, s.Port, s.State)
		if s.State == control.ServiceRunning {
			action(fmt.Sprintf("port:%d", s.Port), text, s.URL, r)
			add("  " + s.URL)
		} else {
			add(text)
		}
	}
	if err := m.portsErr[k]; err != nil {
		add("Services unavailable: " + err.Error())
	}
	add("")
	add("Sessions")
	for _, s := range m.sessions[k] {
		state := s.State.State
		if state == "" {
			state = "unknown"
		}
		add(s.Label() + " · " + state + " · " + s.Name)
	}
	if err := m.sessionErrors[k]; err != nil {
		add("Sessions unavailable: " + err.Error())
	}
	a := agentOf(r, m.live[k])
	if a.Reachable {
		add(fmt.Sprintf("Supervisor: %s · queue %d · session %s", a.State, a.QueueDepth, sessionOr(a.Session)))
		add("Last event: " + lastEventLabel(a))
	} else {
		add("Supervisor: unreachable")
	}
	var deps []control.Row
	for _, x := range m.rows {
		if x.Kind == control.RowSidecar && x.Project == r.Project && strings.HasPrefix(x.Name, r.Name+"-") {
			deps = append(deps, x)
		}
	}
	if len(deps) > 0 {
		add("")
		add("Compose dependencies")
		for _, x := range deps {
			add(strings.TrimPrefix(x.Name, r.Name+"-") + " · " + stateLabel(x) + " · " + x.IP)
		}
	}
	add("")
	add("Recent events")
	if m.eventsErr != nil {
		add(m.eventsErr.Error())
	} else if len(m.events) == 0 {
		add("No events yet.")
	} else {
		for _, e := range tailEvents(m.events, detailEvents) {
			add(shortTs(e.Ts) + " " + eventKind(e) + " " + eventDetail(e))
		}
	}
	if found {
		add("")
		add("Actions")
		if canAttach(r) {
			action("attach", "Open default Claude session", "", r)
			action("new", "New Claude session", "", r)
			action("shell", "Open shell", "", r)
		}
		if canSupervisor(r) {
			action("supervisor", "Open supervisor", "", r)
		}
		if canSend(r, m.live[k]) {
			action("send", "Send a turn to supervisor", "", r)
		}
		if canInterrupt(r, m.live[k]) {
			action("interrupt", "Interrupt supervisor", "", r)
		}
		if canBrowser(r) {
			action("restart", "Restart shared browser", "", r)
		}
		if canBoot(r) {
			action("boot", "Boot container", "", r)
		}
		if canDown(r) {
			action("down", "Tear down container…", "", r)
		}
	}
	return lines
}
func (m Model) modalContent(width int) (string, []dialogLine) {
	switch {
	case m.showHelp:
		var lines []dialogLine
		for _, l := range strings.Split(m.helpView(width), "\n") {
			lines = append(lines, dialogLine{text: l})
		}
		return "Keyboard and mouse", lines
	case m.mode == modeDetails:
		title := "Container details"
		if m.dialog != nil && m.dialog.environment {
			title = "Environment details"
		}
		return title, m.detailsLines(width)
	case m.mode == modeConfirmDown && m.confirm != nil:
		return "Tear down container", plainDialogLines(m.confirm.View(), width)
	case m.mode == modePicker && m.picker != nil:
		return "Open a pane", plainDialogLines(m.picker.View(), width)
	case m.mode == modeCreate && m.createForm != nil:
		return "New container · " + m.creating.Project, plainDialogLines(m.createForm.View(), width)
	}
	return "", nil
}
func plainDialogLines(s string, width int) []dialogLine {
	var lines []dialogLine
	for _, l := range strings.Split(ansi.Hardwrap(s, max(1, width), true), "\n") {
		lines = append(lines, dialogLine{text: l})
	}
	return lines
}
func (m Model) modalView() (string, rect) {
	frame, width, height := m.modalFrame()
	title, lines := m.modalContent(width)
	from := max(0, min(m.modalScroll, max(0, len(lines)-height)))
	visible := make([]string, 0, height)
	for i := from; i < min(len(lines), from+height); i++ {
		l := lines[i]
		text := l.text
		if l.action != nil {
			prefix := "  "
			style := stylePort
			if m.dialog != nil && m.dialog.selected == l.action.id {
				prefix = "› "
				style = style.Background(lipgloss.Color("#303544")).Bold(true)
			}
			text = style.Hyperlink(l.action.url).Render(fit(prefix+text, width))
		}
		visible = append(visible, ansi.Truncate(text, width, "…"))
	}
	body := fitLines(strings.Join(visible, "\n"), height)
	footer := "esc close · ↑↓ scroll · tab actions · enter open"
	if m.mode == modePicker || m.mode == modeCreate || m.mode == modeConfirmDown {
		footer = "esc cancel · enter confirm"
	}
	if len(lines) > height {
		footer = fmt.Sprintf("%d–%d/%d · ", from+1, min(len(lines), from+height), len(lines)) + footer
	}
	content := lipgloss.NewStyle().Bold(true).Render(fit(title, width)) + "\n\n" + body + "\n\n" + styleDim.Render(fit(footer, width))
	box := lipgloss.NewStyle().Width(frame.w).Padding(0, 1).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#687188")).Background(lipgloss.Color("#181b22")).Render(content)
	return box, frame
}
func (m *Model) scrollModal(delta int) {
	_, w, h := m.modalFrame()
	_, lines := m.modalContent(w)
	m.modalScroll = max(0, min(max(0, len(lines)-h), m.modalScroll+delta))
}
func (m Model) handleDetailsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.dialog != nil {
			m.focus = m.dialog.focus
			if _, i := m.tabByID(m.dialog.paneID); i >= 0 {
				m.focused = i
			}
		}
		m.mode = modeNormal
		m.dialog = nil
		m.showHelp = false
		m.modalScroll = 0
		return m, nil
	case "up", "k":
		m.scrollModal(-1)
	case "down", "j":
		m.scrollModal(1)
	case "pgup":
		_, _, h := m.modalFrame()
		m.scrollModal(-h)
	case "pgdown":
		_, _, h := m.modalFrame()
		m.scrollModal(h)
	case "home":
		m.modalScroll = 0
	case "end":
		m.scrollModal(1 << 20)
	case "tab", "shift+tab":
		if m.dialog == nil {
			return m, nil
		}
		_, w, h := m.modalFrame()
		lines := m.detailsLines(w)
		var indices []int
		seen := map[string]bool{}
		current := -1
		for i, l := range lines {
			if l.action != nil && !seen[l.action.id] {
				seen[l.action.id] = true
				if l.action.id == m.dialog.selected {
					current = len(indices)
				}
				indices = append(indices, i)
			}
		}
		if len(indices) > 0 {
			dir := 1
			if msg.String() == "shift+tab" {
				dir = -1
			}
			next := (current + dir + len(indices)) % len(indices)
			if current < 0 && dir < 0 {
				next = len(indices) - 1
			}
			at := indices[next]
			d := *m.dialog
			d.selected = lines[at].action.id
			m.dialog = &d
			if at < m.modalScroll {
				m.modalScroll = at
			}
			if at >= m.modalScroll+h {
				m.modalScroll = at - h + 1
			}
		}
	case "enter":
		if m.dialog != nil {
			_, w, _ := m.modalFrame()
			for _, l := range m.detailsLines(w) {
				if l.action != nil && l.action.id == m.dialog.selected {
					return m.runDialogAction(*l.action)
				}
			}
		}
	}
	return m, nil
}
func (m Model) runDialogAction(a dialogAction) (tea.Model, tea.Cmd) {
	if a.url != "" {
		return m.openURL(a.url)
	}
	if m.action != "" {
		return m, nil
	}
	m.mode = modeNormal
	m.dialog = nil
	m.modalScroll = 0
	r := a.row
	switch a.id {
	case "attach":
		return m.openOrFocus(KindClaude, r)
	case "new":
		return m.openSession(r, control.AttachRequest{New: true})
	case "shell":
		return m.openOrFocus(KindShell, r)
	case "supervisor":
		return m.openOrFocus(KindSupervisor, r)
	case "restart":
		return m.startAction(LabelBrowserRestart, m.actor.RestartBrowser(r))
	case "interrupt":
		return m.startAction(LabelInterrupt, m.actor.Interrupt(r))
	case "send":
		m.mode = modeInput
		m.pending = r
		m.input.SetValue("")
		m.input.SetWidth(sendInputWidth(r.Name, m.width))
		return m, m.input.Focus()
	case "down":
		m.mode = modeConfirmDown
		m.pending = r
		_, w, _ := m.modalFrame()
		m.confirm = newDownConfirm(r.Name, w)
		return m, m.confirm.Init()
	case "boot":
		m.actionTarget = r
		return m.startAction(LabelUp, m.actor.Up(r))
	}
	return m, nil
}

func modalKeymap() *huh.KeyMap {
	k := huh.NewDefaultKeyMap()
	k.Quit = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))
	return k
}

func lastEventLabel(a control.AgentStatus) string {
	if a.LastEventType == "" {
		return "-"
	}
	if a.LastEventSubtype != "" {
		return a.LastEventType + "/" + a.LastEventSubtype
	}
	return a.LastEventType
}
