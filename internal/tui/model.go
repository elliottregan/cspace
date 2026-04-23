// internal/tui/model.go
package tui

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/elliottregan/cspace/internal/diagnostics"
)

const maxEvents = 1000

// Model is the root Bubbletea model for cspace watch.
type Model struct {
	addr        string
	agents      []diagnostics.AgentSnapshot
	events      []EventRow
	services    []ServiceStatus
	filter      string
	wsConnected bool
	lastPoll    time.Time
	width       int
	height      int
}

// NewModel constructs the initial model.
func NewModel(addr string) Model {
	return Model{addr: addr}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(pollAgents(m.addr), pollServices(), tick())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case AgentsUpdatedMsg:
		m.agents = []diagnostics.AgentSnapshot(msg)
		m.lastPoll = time.Now()
	case EventReceivedMsg:
		rows := ParseEnvelope(diagnostics.Envelope(msg))
		m.events = append(m.events, rows...)
		if len(m.events) > maxEvents {
			m.events = m.events[len(m.events)-maxEvents:]
		}
	case ServicesUpdatedMsg:
		m.services = []ServiceStatus(msg)
	case WSStatusMsg:
		m.wsConnected = msg.Connected
	case TickMsg:
		return m, tea.Batch(pollAgents(m.addr), tick())
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "f":
			m = m.cycleFilter()
		}
	}
	return m, nil
}

func (m Model) cycleFilter() Model {
	if len(m.agents) == 0 {
		return m
	}
	names := make([]string, 0, len(m.agents)+1)
	names = append(names, "")
	for _, a := range m.agents {
		names = append(names, a.Instance)
	}
	cur := 0
	for i, n := range names {
		if n == m.filter {
			cur = i
			break
		}
	}
	m.filter = names[(cur+1)%len(names)]
	return m
}

func (m Model) View() string {
	if m.width == 0 {
		return "Loading…"
	}
	leftWidth := m.width * 36 / 100
	if leftWidth < 28 {
		leftWidth = 28
	}
	rightWidth := m.width - leftWidth - 1
	bodyHeight := m.height - 2

	leftLines := strings.Split(RenderLeft(m.agents, m.services, leftWidth, bodyHeight), "\n")
	rightLines := strings.Split(RenderRight(m.events, m.filter, rightWidth, bodyHeight), "\n")

	var rows []string
	for i := 0; i < bodyHeight; i++ {
		l, r := "", ""
		if i < len(leftLines) {
			l = leftLines[i]
		}
		if i < len(rightLines) {
			r = rightLines[i]
		}
		rows = append(rows, lipgloss.NewStyle().Width(leftWidth).Render(l)+styleBorder.Render("")+r)
	}

	return m.titleBar() + "\n" + strings.Join(rows, "\n") + "\n" + m.statusBar()
}

func (m Model) titleBar() string {
	ws := styleDotStuck.Render("● ws disconnected")
	if m.wsConnected {
		ws = styleDotActive.Render("● ws connected")
	}
	proj := getenv("CSPACE_PROJECT_NAME")
	if proj == "" {
		proj = "cspace"
	}
	left := fmt.Sprintf("cspace watch · %s · %d agents", proj, len(m.agents))
	gap := m.width - len([]rune(left)) - len([]rune(ws)) - 2
	if gap < 1 {
		gap = 1
	}
	return styleTitle.Width(m.width).Render(left + strings.Repeat(" ", gap) + ws)
}

func (m Model) statusBar() string {
	keys := styleKeyHint.Render("q") + " quit  " +
		styleKeyHint.Render("f") + " filter  " +
		styleFutureKey.Render("i interrupt  s send")
	age := ""
	if !m.lastPoll.IsZero() {
		age = fmt.Sprintf("updated %ds ago", int(time.Since(m.lastPoll).Seconds()))
	}
	gap := m.width - len([]rune(keys)) - len([]rune(age)) - 4
	if gap < 1 {
		gap = 1
	}
	return styleStatusBar.Width(m.width).Render(keys + strings.Repeat(" ", gap) + age)
}

func pollAgents(addr string) tea.Cmd {
	return func() tea.Msg {
		resp, err := http.Get("http://" + addr + "/agents")
		if err != nil {
			return AgentsUpdatedMsg(nil)
		}
		defer resp.Body.Close()
		var snaps []diagnostics.AgentSnapshot
		if err := jsonDecode(resp.Body, &snaps); err != nil {
			return AgentsUpdatedMsg(nil)
		}
		return AgentsUpdatedMsg(snaps)
	}
}

func pollServices() tea.Cmd {
	return func() tea.Msg { return ServicesUpdatedMsg(probeSharedServices()) }
}

func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return TickMsg(t) })
}
