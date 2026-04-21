// internal/tui/left_test.go
package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/elliottregan/cspace/internal/diagnostics"
)

func makeSnapshot(instance, role string, status diagnostics.AgentStatus, turns int, cost float64) diagnostics.AgentSnapshot {
	return diagnostics.AgentSnapshot{
		Instance: instance,
		Role:     role,
		Status:   status,
		Turns:    turns,
		CostUsd:  cost,
	}
}

func TestRenderLeft_ShowsAgentName(t *testing.T) {
	agents := []diagnostics.AgentSnapshot{
		makeSnapshot("mercury", "advisor", diagnostics.StatusActive, 12, 0.043),
	}
	out := RenderLeft(agents, nil, 36, 20)
	if !strings.Contains(out, "mercury") {
		t.Errorf("expected agent name 'mercury' in left panel output:\n%s", out)
	}
}

func TestRenderLeft_ShowsStuckWarning(t *testing.T) {
	snap := makeSnapshot("earth", "coordinator", diagnostics.StatusStuck, 31, 0.19)
	snap.PendingTool = &diagnostics.PendingToolCall{
		Tool:      "Bash",
		StartedAt: time.Now().Add(-72 * time.Second),
		AgeMs:     72000,
	}
	out := RenderLeft([]diagnostics.AgentSnapshot{snap}, nil, 36, 30)
	if !strings.Contains(out, "Bash") {
		t.Errorf("expected pending tool 'Bash' in stuck warning:\n%s", out)
	}
}

func TestRenderLeft_ShowsServiceSection(t *testing.T) {
	services := []ServiceStatus{
		{Name: "cspace-proxy", Label: "proxy", Port: ":80", Running: true},
	}
	out := RenderLeft(nil, services, 36, 30)
	if !strings.Contains(out, "cspace-proxy") {
		t.Errorf("expected service 'cspace-proxy' in output:\n%s", out)
	}
}

func TestRenderLeft_SortOrder(t *testing.T) {
	agents := []diagnostics.AgentSnapshot{
		makeSnapshot("venus", "worker", diagnostics.StatusIdle, 5, 0.01),
		makeSnapshot("earth", "coordinator", diagnostics.StatusStuck, 10, 0.1),
		makeSnapshot("mercury", "advisor", diagnostics.StatusActive, 3, 0.02),
	}
	out := RenderLeft(agents, nil, 36, 40)
	earthIdx := strings.Index(out, "earth")
	mercuryIdx := strings.Index(out, "mercury")
	venusIdx := strings.Index(out, "venus")
	if earthIdx == -1 || mercuryIdx == -1 || venusIdx == -1 {
		t.Fatal("not all agents in output")
	}
	if !(earthIdx < mercuryIdx && mercuryIdx < venusIdx) {
		t.Errorf("sort order wrong: stuck=%d active=%d idle=%d", earthIdx, mercuryIdx, venusIdx)
	}
}
