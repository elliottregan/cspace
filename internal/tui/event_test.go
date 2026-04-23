package tui

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/elliottregan/cspace/internal/diagnostics"
)

func envelope(ts, instance, sdkJSON string) diagnostics.Envelope {
	return diagnostics.Envelope{
		Ts:       ts,
		Instance: instance,
		SDK:      json.RawMessage(sdkJSON),
	}
}

func TestParseEnvelope_ToolUse(t *testing.T) {
	env := envelope("2026-04-21T14:33:01Z", "mercury", `{
		"type": "assistant",
		"message": {
			"content": [
				{"type": "tool_use", "name": "Bash", "input": {"command": "npm test"}}
			]
		}
	}`)
	rows := ParseEnvelope(env)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.Kind != KindTool {
		t.Errorf("expected KindTool, got %q", r.Kind)
	}
	if r.ToolName != "Bash" {
		t.Errorf("expected tool Bash, got %q", r.ToolName)
	}
	if r.Instance != "mercury" {
		t.Errorf("expected instance mercury, got %q", r.Instance)
	}
	if r.Content != "npm test" {
		t.Errorf("unexpected content %q", r.Content)
	}
}

func TestParseEnvelope_AssistantText(t *testing.T) {
	env := envelope("2026-04-21T14:33:09Z", "earth", `{
		"type": "assistant",
		"message": {
			"content": [
				{"type": "text", "text": "Running the auth test suite."}
			]
		}
	}`)
	rows := ParseEnvelope(env)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.Kind != KindText {
		t.Errorf("expected KindText, got %q", r.Kind)
	}
	if r.Content != "Running the auth test suite." {
		t.Errorf("unexpected content %q", r.Content)
	}
}

func TestParseEnvelope_Result(t *testing.T) {
	env := envelope("2026-04-21T14:32:48Z", "venus", `{
		"type": "result",
		"subtype": "success",
		"cost_usd": "0.021",
		"num_turns": "8"
	}`)
	rows := ParseEnvelope(env)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Kind != KindResult {
		t.Errorf("expected KindResult, got %q", rows[0].Kind)
	}
}

func TestParseEnvelope_ResultFailure(t *testing.T) {
	env := envelope("2026-04-21T14:32:48Z", "mars", `{
		"type": "result",
		"subtype": "error_max_turns"
	}`)
	rows := ParseEnvelope(env)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Kind != KindResult {
		t.Errorf("expected KindResult")
	}
}

func TestParseEnvelope_EmptyText(t *testing.T) {
	env := envelope("2026-04-21T14:33:00Z", "mercury", `{
		"type": "assistant",
		"message": {"content": [{"type": "text", "text": ""}]}
	}`)
	rows := ParseEnvelope(env)
	if len(rows) != 0 {
		t.Errorf("expected 0 rows for empty text, got %d", len(rows))
	}
}

func TestParseEnvelope_Truncation(t *testing.T) {
	long := ""
	for i := 0; i < 100; i++ {
		long += "a"
	}
	env := envelope("2026-04-21T14:33:00Z", "mercury", `{"type":"assistant","message":{"content":[{"type":"text","text":"`+long+`"}]}}`)
	rows := ParseEnvelope(env)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row")
	}
	if len([]rune(rows[0].Content)) > 80 {
		t.Errorf("content not truncated: len=%d", len([]rune(rows[0].Content)))
	}
}

func TestParseEnvelope_UnknownType(t *testing.T) {
	env := envelope("2026-04-21T14:33:00Z", "mercury", `{"type": "system"}`)
	rows := ParseEnvelope(env)
	if rows != nil {
		t.Errorf("expected nil for unknown type, got %v", rows)
	}
}

func TestParseEnvelope_InvalidJSON(t *testing.T) {
	env := envelope("2026-04-21T14:33:00Z", "mercury", `not json`)
	rows := ParseEnvelope(env)
	if rows != nil {
		t.Errorf("expected nil for invalid JSON")
	}
}

func TestParseEnvelope_TimeParsing(t *testing.T) {
	env := envelope("2026-04-21T14:33:01Z", "mercury", `{
		"type": "result",
		"subtype": "success",
		"cost_usd": "0.01",
		"num_turns": "1"
	}`)
	rows := ParseEnvelope(env)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row")
	}
	expected := time.Date(2026, 4, 21, 14, 33, 1, 0, time.UTC)
	if !rows[0].Time.Equal(expected) {
		t.Errorf("expected %v, got %v", expected, rows[0].Time)
	}
}

func TestParseEnvelope_ToolInputPriority(t *testing.T) {
	env := envelope("2026-04-21T14:33:01Z", "test", `{
		"type": "assistant",
		"message": {
			"content": [
				{"type": "tool_use", "name": "Read", "input": {"file_path": "f.txt", "pattern": "xyz"}}
			]
		}
	}`)
	rows := ParseEnvelope(env)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Content != "f.txt" {
		t.Errorf("expected file_path over pattern, got %q", rows[0].Content)
	}
}

func TestParseEnvelope_ToolInputEmpty(t *testing.T) {
	env := envelope("2026-04-21T14:33:01Z", "test", `{
		"type": "assistant",
		"message": {
			"content": [
				{"type": "tool_use", "name": "Bash", "input": {}}
			]
		}
	}`)
	rows := ParseEnvelope(env)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Content != "" {
		t.Errorf("expected empty content for empty input, got %q", rows[0].Content)
	}
}
