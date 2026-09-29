package controlplane

import (
	"context"
	"maps"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/elliottregan/cspace/internal/control"
)

type headerData interface {
	HeaderStatus(context.Context, string, string, bool) (control.HeaderStatus, error)
}
type serviceData interface {
	Services(context.Context, string, string) ([]control.ServiceStatus, error)
}
type sessionData interface {
	Sessions(context.Context, string, string) ([]control.Session, error)
	SessionState(string, string, control.Session) control.InteractiveState
}
type SessionPaneHost interface {
	OpenSession(context.Context, control.Row, control.AttachRequest, int, int) (Opened, error)
}

type headerSample struct {
	status control.HeaderStatus
	err    error
}
type headerMsg struct {
	key    sandboxKey
	sample headerSample
	detail bool
}
type sessionsMsg struct {
	sessions map[sandboxKey][]control.Session
	errs     map[sandboxKey]error
}

func (m Model) headerCmd(force bool) tea.Cmd {
	return m.headerFor(m.activeRow(), force, false)
}

func (m Model) headerFor(r control.Row, force, detail bool) tea.Cmd {
	data, ok := m.data.(headerData)
	if !ok || r.Name == "" || r.Kind != control.RowSandbox {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		status, err := data.HeaderStatus(ctx, r.Project, r.Name, force)
		return headerMsg{key: keyOf(r), sample: headerSample{status: status, err: err}, detail: detail}
	}
}
func (m Model) sessionsCmd() tea.Cmd {
	data, ok := m.data.(sessionData)
	if !ok {
		return nil
	}
	targets := sandboxTargets(m.rows)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		out := sessionsMsg{sessions: map[sandboxKey][]control.Session{}, errs: map[sandboxKey]error{}}
		for _, k := range targets {
			s, err := data.Sessions(ctx, k.Project, k.Name)
			if err != nil {
				out.errs[k] = err
			} else {
				out.sessions[k] = s
			}
		}
		return out
	}
}
func (m Model) updateWorkspace(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case headerMsg:
		if !msg.detail {
			m.readingHeader = false
		}
		m.headers = maps.Clone(m.headers)
		if m.headers == nil {
			m.headers = map[sandboxKey]headerSample{}
		}
		m.headers[msg.key] = msg.sample
		if !msg.detail && m.refreshHeader {
			m.refreshHeader = false
			if cmd := m.headerCmd(true); cmd != nil {
				m.readingHeader = true
				return m, cmd, true
			}
		}
		// A different pane may have become active while this request was pending.
		if !msg.detail && msg.key != keyOf(m.activeRow()) {
			if cmd := m.headerCmd(false); cmd != nil {
				m.readingHeader = true
				return m, cmd, true
			}
		}
		return m, nil, true
	case sessionsMsg:
		m.pollingSessions = false
		next := maps.Clone(msg.sessions)
		for k := range msg.errs {
			if old, ok := m.sessions[k]; ok {
				next[k] = append([]control.Session(nil), old...)
				for i := range next[k] {
					next[k][i].State = control.InteractiveState{}
				}
			}
		}
		m.sessions, m.sessionErrors = next, msg.errs
		return m, nil, true
	case createReadyMsg:
		m.action = ""
		if msg.err != nil {
			m.notice = notice{text: msg.err.Error(), isErr: true}
			return m, nil, true
		}
		m.mode = modeCreate
		m.creating = control.Row{Kind: control.RowSandbox, Project: msg.project, Name: msg.name}
		m.createForm = m.newCreateForm(msg.project, msg.name)
		return m, m.createForm.Init(), true
	case actionResultMsg:
		if msg.label == LabelUp && m.creating.Name != "" {
			target := m.creating
			m.creating = control.Row{}
			m.action = ""
			if msg.err != nil {
				m.notice = notice{text: "up failed: " + msg.err.Error(), isErr: true}
				return m, nil, true
			}
			target.Container = "cspace-" + target.Project + "-" + target.Name
			target.State = control.StateRunning
			returnModel, cmd := m.openOrFocus(KindClaude, target)
			return returnModel.(Model), tea.Batch(cmd, m.snapshotCmd()), true
		}
	}
	return m, nil, false
}

func (m Model) openSession(row control.Row, req control.AttachRequest) (tea.Model, tea.Cmd) {
	if !canAttach(row) {
		m.notice = notice{text: "container is not running", isErr: true}
		return m, nil
	}
	for i, t := range m.tabs {
		if !req.New && t.kind == KindClaude && t.attachable() && t.project == row.Project && t.sandbox == row.Name && t.session.Name == req.Session && (req.ExpectedID == "" || t.session.ID == req.ExpectedID) {
			return m.focusTab(i), nil
		}
	}
	host, ok := m.host.(SessionPaneHost)
	if !ok {
		m.notice = notice{text: "named session attach unavailable", isErr: true}
		return m, nil
	}
	cols, rows := m.paneSize()
	return m.startAction(LabelOpenPane, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), openTimeout)
		defer cancel()
		opened, err := host.OpenSession(ctx, row, req, cols, rows)
		return paneOpenedMsg{kind: KindClaude, row: row, opened: opened, err: err}
	})
}

// WithLinkOpener connects host URL opening without making views or mouse
// handlers perform I/O. A missing opener produces a visible error.
func (m Model) WithLinkOpener(opener LinkOpener) Model { m.opener = opener; return m }

// WithProject keeps the current project available for its first container,
// before there are any registry rows from which to discover it.
func (m Model) WithProject(project string) Model { m.project = project; return m }
func (m Model) openURL(url string) (tea.Model, tea.Cmd) {
	if m.action != "" {
		return m, nil
	}
	if m.opener == nil {
		m.notice = notice{text: "URL opener unavailable", isErr: true}
		return m, nil
	}
	opener := m.opener
	return m.startAction("open link", func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return Result("open link", opener.OpenURL(ctx, url))
	})
}

// QuitWarning reports an operation that could not be observed to completion.
func (m Model) QuitWarning() string { return m.exitWarning }

// A fresh lifecycle snapshot supersedes old listener and tmux samples.
func (m *Model) reconcileWorkspace(rows []control.Row) {
	services := make(map[sandboxKey][]control.ServiceStatus)
	sessions := make(map[sandboxKey][]control.Session)
	for _, row := range rows {
		if row.Kind != control.RowSandbox {
			continue
		}
		k := keyOf(row)
		if row.State != control.StateStopped {
			services[k], sessions[k] = m.services[k], m.sessions[k]
			continue
		}
		for _, service := range m.services[k] {
			if service.Declared {
				service.State = control.ServiceStopped
				services[k] = append(services[k], service)
			}
		}
	}
	m.services, m.sessions = services, sessions
}
