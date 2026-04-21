package tui

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/elliottregan/cspace/internal/diagnostics"
)

// EventKind classifies a display row in the activity stream.
type EventKind string

const (
	KindTool   EventKind = "tool"
	KindText   EventKind = "text"
	KindResult EventKind = "result"
)

// EventRow is one display row in the activity stream.
type EventRow struct {
	Time     time.Time
	Instance string
	Kind     EventKind
	ToolName string // non-empty for KindTool
	Content  string // display string, pre-truncated
}

type sdkEvent struct {
	Type     string          `json:"type"`
	Subtype  string          `json:"subtype,omitempty"`
	Message  json.RawMessage `json:"message,omitempty"`
	CostUSD  json.Number     `json:"cost_usd,omitempty"`
	NumTurns json.Number     `json:"num_turns,omitempty"`
}

type sdkMessage struct {
	Content []sdkContent `json:"content"`
}

type sdkContent struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type sdkToolInput struct {
	Command     string `json:"command,omitempty"`
	FilePath    string `json:"file_path,omitempty"`
	Pattern     string `json:"pattern,omitempty"`
	Description string `json:"description,omitempty"`
	Skill       string `json:"skill,omitempty"`
	Query       string `json:"query,omitempty"`
}

// ParseEnvelope converts a diagnostics Envelope into zero or more display rows.
// Returns nil if the envelope produces no displayable output.
func ParseEnvelope(env diagnostics.Envelope) []EventRow {
	ts, err := time.Parse(time.RFC3339Nano, env.Ts)
	if err != nil {
		ts, _ = time.Parse(time.RFC3339, env.Ts)
		if ts.IsZero() {
			ts = time.Now()
		}
	}

	var ev sdkEvent
	if err := json.Unmarshal(env.SDK, &ev); err != nil {
		return nil
	}

	switch ev.Type {
	case "assistant":
		return parseAssistantContent(ts, env.Instance, ev.Message)
	case "result":
		return parseResultContent(ts, env.Instance, ev)
	}
	return nil
}

func parseAssistantContent(ts time.Time, instance string, raw json.RawMessage) []EventRow {
	if raw == nil {
		return nil
	}
	var msg sdkMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil
	}
	var rows []EventRow
	for _, c := range msg.Content {
		switch c.Type {
		case "tool_use":
			rows = append(rows, EventRow{
				Time:     ts,
				Instance: instance,
				Kind:     KindTool,
				ToolName: c.Name,
				Content:  truncate(toolInputSummary(c.Input), 60),
			})
		case "text":
			if c.Text == "" {
				continue
			}
			rows = append(rows, EventRow{
				Time:     ts,
				Instance: instance,
				Kind:     KindText,
				Content:  truncate(c.Text, 80),
			})
		}
	}
	return rows
}

func parseResultContent(ts time.Time, instance string, ev sdkEvent) []EventRow {
	var content string
	if ev.Subtype == "success" {
		content = fmt.Sprintf("success · %s turns · $%s", ev.NumTurns, ev.CostUSD)
	} else {
		content = fmt.Sprintf("failed: %s", ev.Subtype)
	}
	return []EventRow{{
		Time:     ts,
		Instance: instance,
		Kind:     KindResult,
		Content:  content,
	}}
}

// truncate shortens s to at most n runes, appending an ellipsis if truncated.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

func toolInputSummary(raw json.RawMessage) string {
	if raw == nil {
		return ""
	}
	var inp sdkToolInput
	if err := json.Unmarshal(raw, &inp); err != nil {
		return ""
	}
	switch {
	case inp.Command != "":
		return inp.Command
	case inp.FilePath != "":
		return inp.FilePath
	case inp.Pattern != "":
		return inp.Pattern
	case inp.Description != "":
		return inp.Description
	case inp.Skill != "":
		return inp.Skill
	case inp.Query != "":
		return inp.Query
	}
	return ""
}
