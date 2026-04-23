package tui

import (
	"strings"
	"testing"
	"time"
)

func makeEventRow(instance string, kind EventKind, tool, content string) EventRow {
	return EventRow{
		Time:     time.Date(2026, 4, 21, 14, 33, 1, 0, time.UTC),
		Instance: instance,
		Kind:     kind,
		ToolName: tool,
		Content:  content,
	}
}

func TestRenderRight_ShowsEvents(t *testing.T) {
	events := []EventRow{
		makeEventRow("mercury", KindTool, "Bash", "npm test"),
		makeEventRow("venus", KindText, "", "Looking at the code."),
	}
	out := RenderRight(events, "", 60, 20)
	if !strings.Contains(out, "mercury") {
		t.Errorf("expected 'mercury' in output:\n%s", out)
	}
	if !strings.Contains(out, "npm test") {
		t.Errorf("expected 'npm test' in output:\n%s", out)
	}
}

func TestRenderRight_FilterByInstance(t *testing.T) {
	events := []EventRow{
		makeEventRow("mercury", KindTool, "Read", "file.go"),
		makeEventRow("venus", KindText, "", "Thinking..."),
	}
	out := RenderRight(events, "mercury", 60, 20)
	if strings.Contains(out, "venus") {
		t.Errorf("expected venus to be filtered out:\n%s", out)
	}
}

func TestRenderRight_EmptyState(t *testing.T) {
	out := RenderRight(nil, "", 60, 20)
	if out == "" {
		t.Error("expected non-empty output even with no events")
	}
}

func TestRenderRight_ResultRowShown(t *testing.T) {
	events := []EventRow{
		makeEventRow("earth", KindResult, "", "success · 8 turns · $0.021"),
	}
	out := RenderRight(events, "", 60, 20)
	if !strings.Contains(out, "success") {
		t.Errorf("expected result row in output:\n%s", out)
	}
}
