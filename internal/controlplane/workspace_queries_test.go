package controlplane

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/elliottregan/cspace/internal/control"
)

type workspaceSessionData struct {
	*fakeData
	catalog map[sandboxKey][]control.Session
	states  map[string]control.InteractiveState
	err     error
}

func (d *workspaceSessionData) Sessions(_ context.Context, project, sandbox string) ([]control.Session, error) {
	return append([]control.Session(nil), d.catalog[sandboxKey{Project: project, Name: sandbox}]...), d.err
}

func (d *workspaceSessionData) SessionState(_, _ string, session control.Session) control.InteractiveState {
	return d.states[session.ID]
}

type workspaceSessionHost struct {
	*fakeHost
	catalog  map[string]control.Session
	requests []control.AttachRequest
}

func (h *workspaceSessionHost) OpenSession(ctx context.Context, row control.Row, req control.AttachRequest, cols, rows int) (Opened, error) {
	h.requests = append(h.requests, req)
	session, ok := h.catalog[req.Session]
	if !ok || (req.ExpectedID != "" && session.ID != req.ExpectedID) {
		return Opened{}, control.ErrSessionGone
	}
	opened, err := h.Open(ctx, KindClaude, row, cols, rows)
	opened.Session = session
	return opened, err
}

func newWorkspaceSessionModel(t *testing.T, sessions ...control.Session) (Model, *workspaceSessionData, *workspaceSessionHost, control.Row) {
	t.Helper()
	row := testSnapshot().Rows[1]
	d := &workspaceSessionData{
		fakeData: &fakeData{snap: testSnapshot()},
		catalog:  map[sandboxKey][]control.Session{keyOf(row): sessions},
		states:   map[string]control.InteractiveState{},
	}
	h := &workspaceSessionHost{fakeHost: &fakeHost{t: t}, catalog: map[string]control.Session{}}
	for _, s := range sessions {
		h.catalog[s.Name] = s
	}
	m := New(d, &recordingActor{}, h, nopClipboard{}, NewKeyMap(nil))
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = mm.(Model)
	mm, _ = m.Update(snapshotMsg{snap: d.snap})
	m = mm.(Model)
	return pollWorkspaceSessions(t, m), d, h, row
}

func pollWorkspaceSessions(t *testing.T, m Model) Model {
	t.Helper()
	cmd := m.sessionsCmd()
	if cmd == nil {
		t.Fatal("session discovery command missing")
	}
	mm, _ := m.Update(cmd())
	return mm.(Model)
}

func workspaceSessionItem(t *testing.T, m Model, id string) navigationItem {
	t.Helper()
	for _, item := range m.navigation() {
		if item.kind == navSession && item.session.ID == id {
			return item
		}
	}
	t.Fatalf("session %q missing from navigation", id)
	return navigationItem{}
}

func activateWorkspaceItem(t *testing.T, m Model, item navigationItem) Model {
	t.Helper()
	mm, cmd := m.activateNavigation(item)
	return pump(t, mm.(Model), cmd)
}

func TestWorkspaceFirstSessionPaneFocusesWithoutDuplicateJoin(t *testing.T) {
	s := control.Session{Name: control.SessionClaude, ID: "default-incarnation"}
	m, _, h, _ := newWorkspaceSessionModel(t, s)
	m = activateWorkspaceItem(t, m, workspaceSessionItem(t, m, s.ID))
	if len(m.tabs) != 1 || m.tabs[0].id <= 0 {
		t.Fatalf("first pane must have a positive identity: %+v", m.tabs)
	}
	item := workspaceSessionItem(t, m, s.ID)
	if item.tabID != m.tabs[0].id {
		t.Fatalf("discovered session did not resolve to its client: %+v", item)
	}
	m.focus = focusSidebar
	m = activateWorkspaceItem(t, m, item)
	selected, ok := m.selectedNavigation()
	if len(h.requests) != 1 || len(m.tabs) != 1 || m.focus != focusMain || m.focusedTab().id != item.tabID || !ok || selected.kind != navSession || selected.id != item.id {
		t.Fatalf("reselecting first session opened a duplicate or focused a header: requests=%v tabs=%d selected=%+v focus=%v", h.requests, len(m.tabs), selected, m.focus)
	}
}

func TestWorkspaceSessionDisappearanceAndReplacementKeepDistinctIdentities(t *testing.T) {
	old := control.Session{Name: control.SessionClaude, ID: "old-default"}
	m, d, h, row := newWorkspaceSessionModel(t, old)
	m = activateWorkspaceItem(t, m, workspaceSessionItem(t, m, old.ID))
	oldTabID := m.tabs[0].id
	d.catalog[keyOf(row)] = nil
	delete(h.catalog, old.Name)
	m = pollWorkspaceSessions(t, m)
	for _, item := range m.navigation() {
		if item.kind == navSession && item.session.ID == old.ID {
			t.Fatal("disappeared session remained in the discovered catalog")
		}
	}
	if len(m.tabs) != 1 || m.tabs[0].id != oldTabID {
		t.Fatal("discovery deleted the old pane's retained screen")
	}
	replacement := control.Session{Name: old.Name, ID: "new-default"}
	d.catalog[keyOf(row)] = []control.Session{replacement}
	h.catalog[replacement.Name] = replacement
	m = pollWorkspaceSessions(t, m)
	item := workspaceSessionItem(t, m, replacement.ID)
	if item.tabID != 0 {
		t.Fatalf("replacement borrowed the old client: %+v", item)
	}
	m = activateWorkspaceItem(t, m, item)
	if len(h.requests) != 2 || h.requests[1].ExpectedID != replacement.ID || len(m.tabs) != 2 || m.focusedTab().session.ID != replacement.ID {
		t.Fatalf("replacement was not joined by its own identity: requests=%v tabs=%d focused=%+v", h.requests, len(m.tabs), m.focusedTab())
	}
}

func TestWorkspaceDeadClientDoesNotBlockTargetedReconnect(t *testing.T) {
	for _, state := range []string{"closing", "reaped", "closed", "exited"} {
		t.Run(state, func(t *testing.T) {
			s := control.Session{Name: "cspace-claude-2", ID: "surviving-session"}
			m, _, h, _ := newWorkspaceSessionModel(t, s)
			h.exits = state == "exited"
			m = activateWorkspaceItem(t, m, workspaceSessionItem(t, m, s.ID))
			h.exits = false
			old := m.focusedTab()
			switch state {
			case "closing":
				old.closing = true
			case "reaped":
				old.reaped = true
			case "closed":
				if err := old.p.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
			case "exited":
				waitForExit(t, old)
			}
			item := workspaceSessionItem(t, m, s.ID)
			if item.tabID != 0 {
				t.Fatalf("dead client claims the live session: %+v", item)
			}
			m = activateWorkspaceItem(t, m, item)
			if len(h.requests) != 2 || h.requests[1].ExpectedID != s.ID || len(m.tabs) != 2 || m.focusedTab().id == old.id || !m.focusedTab().attachable() {
				t.Fatalf("surviving session did not receive a fresh client: requests=%v tabs=%d focused=%+v", h.requests, len(m.tabs), m.focusedTab())
			}
		})
	}
}

func TestWorkspaceFastStateDoesNotReviveOldSessionDiscovery(t *testing.T) {
	old := control.Session{Name: control.SessionClaude, ID: "old-default"}
	second := control.Session{Name: "cspace-claude-2", ID: "second"}
	removed := control.Session{Name: "cspace-claude-3", ID: "removed"}
	m, d, _, row := newWorkspaceSessionModel(t, old, second, removed)
	d.states = map[string]control.InteractiveState{
		old.ID: {State: "working"}, second.ID: {State: "needs-input"}, removed.ID: {State: "idle"},
	}
	// The fast command captures the old catalog, then discovery completes
	// first. Its eventual status sample must not undo the newer discovery.
	oldFast := m.liveCmd()
	replacement := control.Session{Name: old.Name, ID: "replacement", State: control.InteractiveState{State: "idle"}}
	d.catalog[keyOf(row)] = []control.Session{replacement, second}
	m = pollWorkspaceSessions(t, m)
	mm, _ := m.Update(oldFast())
	m = mm.(Model)
	got := m.sessions[keyOf(row)]
	if len(got) != 2 || got[0].ID != replacement.ID || got[0].State.State != "idle" || got[1].ID != second.ID || got[1].State.State != "needs-input" {
		t.Fatalf("late fast status replaced discovery or mixed session states: %+v", got)
	}
}

func TestWorkspaceTransientDiscoveryErrorKeepsSessionsWithUnknownState(t *testing.T) {
	s := control.Session{Name: "cspace-claude-2", ID: "known-session", State: control.InteractiveState{State: "working"}}
	m, d, _, row := newWorkspaceSessionModel(t, s)
	previous := m.sessions[keyOf(row)]
	d.states[s.ID] = control.InteractiveState{State: "needs-input"}
	lateFast := m.liveCmd()
	d.err = errors.New("container transport unavailable")
	m = pollWorkspaceSessions(t, m)
	got := m.sessions[keyOf(row)]
	if len(got) != 1 || got[0].ID != s.ID || got[0].State.Known() || m.sessionErrors[keyOf(row)] == nil || m.pollingSessions {
		t.Fatalf("transient failure lost the catalog or left a stale status: sessions=%+v errors=%v polling=%v", got, m.sessionErrors, m.pollingSessions)
	}
	if previous[0].State.State != "working" {
		t.Fatal("failed discovery mutated the previous model's session slice")
	}
	mm, _ := m.Update(lateFast())
	m = mm.(Model)
	if m.sessions[keyOf(row)][0].State.Known() {
		t.Fatal("late hook sample restored a known state while discovery was unavailable")
	}
	d.err = nil
	d.catalog[keyOf(row)] = nil
	m = pollWorkspaceSessions(t, m)
	if len(m.sessions[keyOf(row)]) != 0 || m.sessionErrors[keyOf(row)] != nil {
		t.Fatal("successful empty discovery did not clear stale sessions and errors")
	}
}

func TestWorkspaceRestartRediscoversDetachedSessionsWithoutOpeningClients(t *testing.T) {
	first := control.Session{Name: control.SessionClaude, ID: "default"}
	second := control.Session{Name: "cspace-claude-2", ID: "second"}
	m, d, h, _ := newWorkspaceSessionModel(t, first, second)
	m = activateWorkspaceItem(t, m, workspaceSessionItem(t, m, second.ID))
	m = pump(t, m, m.closeTab(m.focusedTab().id))
	if len(m.tabs) != 0 || len(d.catalog) != 1 {
		t.Fatal("closing a pane should leave the persistent session catalog")
	}
	reopened := New(d, &recordingActor{}, h, nopClipboard{}, NewKeyMap(nil))
	mm, _ := reopened.Update(snapshotMsg{snap: d.snap})
	reopened = pollWorkspaceSessions(t, mm.(Model))
	for _, s := range []control.Session{first, second} {
		if item := workspaceSessionItem(t, reopened, s.ID); item.tabID != 0 {
			t.Fatalf("fresh model invented a local client for detached session: %+v", item)
		}
	}
	if len(reopened.tabs) != 0 || len(h.requests) != 1 {
		t.Fatal("rediscovery opened a client before a user selected a session")
	}
	reopened = activateWorkspaceItem(t, reopened, workspaceSessionItem(t, reopened, second.ID))
	if len(reopened.tabs) != 1 || len(h.requests) != 2 || h.requests[1] != (control.AttachRequest{Session: second.Name, ExpectedID: second.ID}) {
		t.Fatalf("rediscovered session did not rejoin its original identity: %v", h.requests)
	}
}

func TestWorkspaceEarlierAsyncClosePreservesCurrentlyFocusedPane(t *testing.T) {
	m, _, _, row := newWorkspaceSessionModel(t)
	for i := 0; i < 3; i++ {
		m = m.addTab(&tab{kind: KindSupervisor, project: row.Project, sandbox: row.Name})
	}
	firstID, secondID := m.tabs[0].id, m.tabs[1].id
	m = m.focusTab(0)
	closeFirst := m.closeTab(firstID)
	// Focus changes while teardown is in flight; the completion identifies
	// only the closing tab, not the tab currently receiving keyboard input.
	m = m.focusTab(1)
	m = pump(t, m, closeFirst)
	if len(m.tabs) != 2 || m.focusedTab().id != secondID || m.navID != paneNavID(secondID) {
		t.Fatalf("closing an earlier pane changed active identity: focused=%+v nav=%s", m.focusedTab(), m.navID)
	}
}

func TestWorkspaceFocusingHostShellExpandsItsGroup(t *testing.T) {
	m := navigationFixture()
	m.collapsed["host"] = true
	m = m.focusTab(3)
	selected, ok := m.selectedNavigation()
	if !ok || selected.tabID != m.focusedTab().id || m.collapsed["host"] {
		t.Fatal("focusing a host shell left its sidebar entry hidden")
	}
}
