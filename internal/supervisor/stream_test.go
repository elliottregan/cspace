package supervisor

import (
	"strings"
	"testing"
)

func TestProcessStream_AssistantToolUse(t *testing.T) {
	// Simulate an assistant event with a Bash tool use
	ndjson := `{"type":"assistant","session_id":"sess-123","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"git status"}}]}}
`
	r := strings.NewReader(ndjson)
	result := ProcessStream(r)

	if result.SessionID != "sess-123" {
		t.Errorf("expected session_id 'sess-123', got %q", result.SessionID)
	}
	if !result.Success {
		t.Error("expected Success to be true")
	}
}

func TestProcessStream_ResultSuccess(t *testing.T) {
	ndjson := `{"type":"result","session_id":"sess-456","subtype":"success","cost_usd":"1.23","num_turns":"5","result":"All done"}
`
	r := strings.NewReader(ndjson)
	result := ProcessStream(r)

	if result.SessionID != "sess-456" {
		t.Errorf("expected session_id 'sess-456', got %q", result.SessionID)
	}
	if !result.Success {
		t.Error("expected Success to be true for subtype=success")
	}
}

func TestProcessStream_ResultFailure(t *testing.T) {
	ndjson := `{"type":"result","session_id":"sess-789","subtype":"error_during_execution","cost_usd":"0.50","num_turns":"3","result":"Something broke"}
`
	r := strings.NewReader(ndjson)
	result := ProcessStream(r)

	if result.SessionID != "sess-789" {
		t.Errorf("expected session_id 'sess-789', got %q", result.SessionID)
	}
	if result.Success {
		t.Error("expected Success to be false for subtype=error_during_execution")
	}
}

func TestProcessStream_SessionCapturedFromAssistant(t *testing.T) {
	ndjson := `{"type":"assistant","session_id":"first-sess","message":{"content":[{"type":"text","text":"hello"}]}}
{"type":"assistant","session_id":"second-sess","message":{"content":[{"type":"text","text":"world"}]}}
`
	r := strings.NewReader(ndjson)
	result := ProcessStream(r)

	// Session ID should be captured from the first event
	if result.SessionID != "first-sess" {
		t.Errorf("expected first session_id 'first-sess', got %q", result.SessionID)
	}
}

func TestProcessStream_EmptyStream(t *testing.T) {
	r := strings.NewReader("")
	result := ProcessStream(r)

	if result.SessionID != "" {
		t.Errorf("expected empty session_id, got %q", result.SessionID)
	}
	if !result.Success {
		t.Error("expected Success to be true for empty stream")
	}
}

func TestProcessStream_MultipleToolUses(t *testing.T) {
	ndjson := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/workspace/foo.go"}},{"type":"tool_use","name":"Edit","input":{"file_path":"/workspace/bar.go"}}]}}
`
	r := strings.NewReader(ndjson)
	result := ProcessStream(r)

	if !result.Success {
		t.Error("expected Success to be true")
	}
}

func TestProcessStream_TextOnly(t *testing.T) {
	ndjson := `{"type":"assistant","message":{"content":[{"type":"text","text":"I'll help you with that task now"}]}}
`
	r := strings.NewReader(ndjson)
	result := ProcessStream(r)

	if !result.Success {
		t.Error("expected Success to be true")
	}
}

func TestProcessStream_InvalidJSON(t *testing.T) {
	ndjson := `not json at all
{"type":"result","subtype":"success","cost_usd":"1.0","num_turns":"1"}
`
	r := strings.NewReader(ndjson)
	result := ProcessStream(r)

	// Should skip invalid lines and process valid ones
	if !result.Success {
		t.Error("expected Success to be true")
	}
}

func TestExitCodeFromError(t *testing.T) {
	// nil error -> 0
	if code := exitCodeFromError(nil); code != 0 {
		t.Errorf("expected 0 for nil error, got %d", code)
	}
}
