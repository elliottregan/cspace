// internal/tui/model_test.go
package tui

import (
	"encoding/json"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/elliottregan/cspace/internal/diagnostics"
)

func newTestModel() Model {
	return NewModel("localhost:8384")
}

func TestModel_AgentsUpdated(t *testing.T) {
	m := newTestModel()
	agents := []diagnostics.AgentSnapshot{
		{Instance: "mercury", Role: "advisor", Status: diagnostics.StatusActive, Turns: 5},
	}
	updated, _ := m.Update(AgentsUpdatedMsg(agents))
	m2 := updated.(Model)
	if len(m2.agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(m2.agents))
	}
	if m2.agents[0].Instance != "mercury" {
		t.Errorf("expected mercury, got %q", m2.agents[0].Instance)
	}
}

func TestModel_EventReceived_Appended(t *testing.T) {
	m := newTestModel()
	env := diagnostics.Envelope{
		Ts:       "2026-04-21T14:33:01Z",
		Instance: "mercury",
		SDK:      json.RawMessage(`{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}]}}`),
	}
	updated, _ := m.Update(EventReceivedMsg(env))
	m2 := updated.(Model)
	if len(m2.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(m2.events))
	}
	if m2.events[0].Content != "hello" {
		t.Errorf("expected content 'hello', got %q", m2.events[0].Content)
	}
}

func TestModel_EventCap(t *testing.T) {
	m := newTestModel()
	for i := 0; i < maxEvents+10; i++ {
		env := diagnostics.Envelope{
			Ts:       time.Now().Format(time.RFC3339),
			Instance: "mercury",
			SDK:      json.RawMessage(`{"type":"assistant","message":{"content":[{"type":"text","text":"x"}]}}`),
		}
		updated, _ := m.Update(EventReceivedMsg(env))
		m = updated.(Model)
	}
	if len(m.events) > maxEvents {
		t.Errorf("events should be capped at %d, got %d", maxEvents, len(m.events))
	}
}

func TestModel_WSStatus(t *testing.T) {
	m := newTestModel()
	updated, _ := m.Update(WSStatusMsg{Connected: true})
	m2 := updated.(Model)
	if !m2.wsConnected {
		t.Error("expected wsConnected=true")
	}
}

func TestModel_FilterCycle(t *testing.T) {
	m := newTestModel()
	m.agents = []diagnostics.AgentSnapshot{
		{Instance: "mercury"},
		{Instance: "venus"},
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	m2 := updated.(Model)
	if m2.filter != "mercury" {
		t.Errorf("expected filter 'mercury', got %q", m2.filter)
	}
	updated2, _ := m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	m3 := updated2.(Model)
	if m3.filter != "venus" {
		t.Errorf("expected filter 'venus', got %q", m3.filter)
	}
	updated3, _ := m3.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	m4 := updated3.(Model)
	if m4.filter != "" {
		t.Errorf("expected filter '' (all), got %q", m4.filter)
	}
}

func TestModel_QuitKey(t *testing.T) {
	m := newTestModel()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("expected quit command from 'q' key")
	}
}
