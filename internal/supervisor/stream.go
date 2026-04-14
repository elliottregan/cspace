// Package supervisor handles launching and managing the agent-supervisor
// process inside cspace containers. It provides NDJSON stream processing,
// supervisor lifecycle management, and command dispatch.
package supervisor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// StreamEvent represents a parsed NDJSON event from the supervisor.
type StreamEvent struct {
	Type      string          `json:"type"`
	SessionID string          `json:"session_id,omitempty"`
	Subtype   string          `json:"subtype,omitempty"`
	CostUSD   json.Number     `json:"cost_usd,omitempty"`
	NumTurns  json.Number     `json:"num_turns,omitempty"`
	Result    string          `json:"result,omitempty"`
	Message   json.RawMessage `json:"message,omitempty"`
}

// messageContent is a partial parse of the message.content array items.
type messageContent struct {
	Type  string          `json:"type"`
	Name  string          `json:"name,omitempty"`
	Text  string          `json:"text,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// messageEnvelope is the minimal structure of a message field.
type messageEnvelope struct {
	Content []messageContent `json:"content"`
}

// toolInput is used to extract specific fields from tool_use inputs.
type toolInput struct {
	Command     string `json:"command,omitempty"`
	FilePath    string `json:"file_path,omitempty"`
	Pattern     string `json:"pattern,omitempty"`
	Description string `json:"description,omitempty"`
	Skill       string `json:"skill,omitempty"`
	Subject     string `json:"subject,omitempty"`
	Status      string `json:"status,omitempty"`
}

// StreamResult holds the final outcome after processing a supervisor stream.
type StreamResult struct {
	SessionID string
	Success   bool
}

// ProcessStream reads NDJSON events from r and renders status updates
// to stderr. It blocks until the reader returns EOF or an error.
func ProcessStream(r io.Reader) StreamResult {
	scanner := bufio.NewScanner(r)
	// Allow large NDJSON lines (SDK messages can be big); max 2MB per line.
	scanner.Buffer(make([]byte, 0, 8*1024), 2*1024*1024)

	var (
		turn      int
		sessionID string
		resultOK  = true
	)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var ev StreamEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}

		// Capture session_id from the first event that carries one.
		// Write it to /tmp/claude-session-id.txt so the EXIT trap
		// (copy-transcript-on-exit.sh) can find it on crash/kill.
		if sessionID == "" && ev.SessionID != "" {
			sessionID = ev.SessionID
			fmt.Fprintf(os.Stderr, "  Session: %s\n", sessionID)
			_ = os.WriteFile("/tmp/claude-session-id.txt", []byte(sessionID), 0644)
		}

		switch ev.Type {
		case "assistant":
			turn++
			renderAssistantEvent(turn, ev)

		case "result":
			if sessionID == "" && ev.SessionID != "" {
				sessionID = ev.SessionID
			}

			fmt.Fprintln(os.Stderr)
			if ev.Subtype == "success" {
				fmt.Fprintf(os.Stderr, "Done (%s turns, $%s)\n", ev.NumTurns.String(), ev.CostUSD.String())
			} else {
				resultOK = false
				fmt.Fprintf(os.Stderr, "FAILED \u2014 status: %s (%s turns, $%s)\n",
					ev.Subtype, ev.NumTurns.String(), ev.CostUSD.String())
				if ev.Result != "" {
					fmt.Fprintf(os.Stderr, "  %s\n", ev.Result)
				}
			}

			if ev.Result != "" {
				fmt.Println(ev.Result)
			}
		}
	}

	return StreamResult{
		SessionID: sessionID,
		Success:   resultOK,
	}
}

// renderAssistantEvent prints tool use or text summaries for an assistant turn.
func renderAssistantEvent(turn int, ev StreamEvent) {
	if ev.Message == nil {
		return
	}

	var msg messageEnvelope
	if err := json.Unmarshal(ev.Message, &msg); err != nil {
		return
	}

	hasTools := false
	for _, c := range msg.Content {
		if c.Type == "tool_use" {
			hasTools = true
			renderToolUse(turn, c)
		}
	}

	if !hasTools {
		var texts []string
		for _, c := range msg.Content {
			if c.Type == "text" && c.Text != "" {
				texts = append(texts, c.Text)
			}
		}
		if len(texts) > 0 {
			combined := strings.Join(texts, "")
			if len(combined) > 120 {
				combined = combined[:120]
			}
			fmt.Fprintf(os.Stderr, "  [%d] %s\n", turn, combined)
		}
	}
}

// renderToolUse prints a one-line summary of a tool invocation.
func renderToolUse(turn int, c messageContent) {
	var input toolInput
	if c.Input != nil {
		_ = json.Unmarshal(c.Input, &input)
	}

	// File-operation tools share a common render pattern
	fileVerbs := map[string]string{
		"Read":  "Reading",
		"Edit":  "Editing",
		"Write": "Writing",
	}

	switch {
	case c.Name == "Bash":
		cmd := input.Command
		if cmd != "" {
			if idx := strings.IndexByte(cmd, '\n'); idx >= 0 {
				cmd = cmd[:idx]
			}
			if len(cmd) > 80 {
				cmd = cmd[:80]
			}
			fmt.Fprintf(os.Stderr, "  [%d] -> %s: %s\n", turn, c.Name, cmd)
		} else {
			fmt.Fprintf(os.Stderr, "  [%d] -> %s\n", turn, c.Name)
		}

	case fileVerbs[c.Name] != "":
		if input.FilePath != "" {
			fmt.Fprintf(os.Stderr, "  [%d] -> %s %s\n", turn, fileVerbs[c.Name], filepath.Base(input.FilePath))
		} else {
			fmt.Fprintf(os.Stderr, "  [%d] -> %s\n", turn, c.Name)
		}

	case c.Name == "Glob" || c.Name == "Grep":
		if input.Pattern != "" {
			fmt.Fprintf(os.Stderr, "  [%d] -> %s: %s\n", turn, c.Name, input.Pattern)
		} else {
			fmt.Fprintf(os.Stderr, "  [%d] -> %s\n", turn, c.Name)
		}

	case c.Name == "Agent":
		if input.Description != "" {
			fmt.Fprintf(os.Stderr, "  [%d] -> Agent: %s\n", turn, input.Description)
		} else {
			fmt.Fprintf(os.Stderr, "  [%d] -> %s\n", turn, c.Name)
		}

	case c.Name == "Skill":
		if input.Skill != "" {
			fmt.Fprintf(os.Stderr, "  [%d] -> Skill: %s\n", turn, input.Skill)
		} else {
			fmt.Fprintf(os.Stderr, "  [%d] -> %s\n", turn, c.Name)
		}

	case c.Name == "TaskCreate" || c.Name == "TaskUpdate":
		summary := input.Subject
		if summary == "" {
			summary = input.Status
		}
		if summary != "" {
			fmt.Fprintf(os.Stderr, "  [%d] -> %s: %s\n", turn, c.Name, summary)
		} else {
			fmt.Fprintf(os.Stderr, "  [%d] -> %s\n", turn, c.Name)
		}

	default:
		fmt.Fprintf(os.Stderr, "  [%d] -> %s\n", turn, c.Name)
	}
}
