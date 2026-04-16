// Package diagnostics provides real-time agent monitoring by tailing event
// logs, probing supervisor sockets, and maintaining per-agent state. It serves
// both an MCP interface (for coordinator agents) and a WebSocket interface
// (for TUI/web dashboards).
package diagnostics

import (
	"encoding/json"
	"sync"
	"time"
)

// AgentStatus represents the lifecycle state of an agent.
type AgentStatus string

const (
	StatusActive  AgentStatus = "active"
	StatusIdle    AgentStatus = "idle"
	StatusStuck   AgentStatus = "stuck"
	StatusExited  AgentStatus = "exited"
	StatusUnknown AgentStatus = "unknown"
)

// Envelope is the NDJSON event log shape written by the supervisor.
type Envelope struct {
	Ts       string          `json:"ts"`
	Instance string          `json:"instance"`
	Role     string          `json:"role"`
	SDK      json.RawMessage `json:"sdk"`
}

// SDKMessage is the minimal fields we parse from the sdk payload.
type SDKMessage struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Message   *struct {
		Content []ContentBlock `json:"content,omitempty"`
	} `json:"message,omitempty"`
	// Result fields
	NumTurns     *int     `json:"num_turns,omitempty"`
	TotalCostUsd *float64 `json:"total_cost_usd,omitempty"`
	CostUsd      *float64 `json:"cost_usd,omitempty"`
	DurationMs   *int     `json:"duration_ms,omitempty"`
}

// ContentBlock is a minimal representation of a content block (tool_use, text, etc).
type ContentBlock struct {
	Type      string `json:"type"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
}

// PendingToolCall tracks a tool call that hasn't received its result yet.
type PendingToolCall struct {
	Tool      string    `json:"tool"`
	ToolID    string    `json:"tool_id"`
	StartedAt time.Time `json:"started_at"`
	AgeMs     int64     `json:"age_ms"`
}

// AgentState is the in-memory state for a single agent.
type AgentState struct {
	mu sync.RWMutex

	Instance     string           `json:"instance"`
	Role         string           `json:"role"`
	Status       AgentStatus      `json:"status"`
	SessionID    string           `json:"session_id,omitempty"`
	Turns        int              `json:"turns"`
	CostUsd      float64          `json:"cost_usd"`
	DurationMs   int              `json:"duration_ms"`
	LastActivity time.Time        `json:"last_activity"`
	SocketAlive  bool             `json:"socket_alive"`
	PendingTool  *PendingToolCall `json:"pending_tool,omitempty"`
	ToolCounts   map[string]int   `json:"tool_counts"`

	// Ring buffer of recent events.
	recentEvents []Envelope
	maxRecent    int
	// Track pending tool calls by ID for pairing.
	pendingTools map[string]PendingToolCall
}

// NewAgentState creates a new agent state with the given ring buffer size.
func NewAgentState(instance, role string, maxRecent int) *AgentState {
	return &AgentState{
		Instance:     instance,
		Role:         role,
		Status:       StatusUnknown,
		LastActivity: time.Now(),
		ToolCounts:   make(map[string]int),
		recentEvents: make([]Envelope, 0, maxRecent),
		maxRecent:    maxRecent,
		pendingTools: make(map[string]PendingToolCall),
	}
}

// IngestEvent processes a new event envelope and updates agent state.
func (a *AgentState) IngestEvent(env Envelope) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.LastActivity = time.Now()
	a.Status = StatusActive

	// Add to ring buffer.
	if len(a.recentEvents) >= a.maxRecent {
		a.recentEvents = a.recentEvents[1:]
	}
	a.recentEvents = append(a.recentEvents, env)

	// Parse SDK message for metrics.
	var msg SDKMessage
	if err := json.Unmarshal(env.SDK, &msg); err != nil {
		return
	}

	if msg.SessionID != "" {
		a.SessionID = msg.SessionID
	}

	switch msg.Type {
	case "assistant":
		a.Turns++
		// Track tool_use blocks.
		if msg.Message != nil {
			for _, block := range msg.Message.Content {
				if block.Type == "tool_use" && block.ID != "" {
					a.ToolCounts[block.Name]++
					ts, _ := time.Parse(time.RFC3339Nano, env.Ts)
					if ts.IsZero() {
						ts = time.Now()
					}
					a.pendingTools[block.ID] = PendingToolCall{
						Tool:      block.Name,
						ToolID:    block.ID,
						StartedAt: ts,
					}
				}
				if block.Type == "tool_result" && block.ToolUseID != "" {
					delete(a.pendingTools, block.ToolUseID)
				}
			}
		}

	case "result":
		if msg.TotalCostUsd != nil {
			a.CostUsd = *msg.TotalCostUsd
		} else if msg.CostUsd != nil {
			a.CostUsd = *msg.CostUsd
		}
		if msg.DurationMs != nil {
			a.DurationMs = *msg.DurationMs
		}
		if msg.NumTurns != nil {
			a.Turns = *msg.NumTurns
		}
		if msg.Subtype == "success" {
			a.Status = StatusIdle
		} else {
			a.Status = StatusExited
		}
		// Clear pending tools on result.
		a.pendingTools = make(map[string]PendingToolCall)
	}

	// Update pending tool pointer (most recent unmatched).
	a.PendingTool = nil
	for _, pt := range a.pendingTools {
		pt.AgeMs = time.Since(pt.StartedAt).Milliseconds()
		if a.PendingTool == nil || pt.StartedAt.After(a.PendingTool.StartedAt) {
			copied := pt
			a.PendingTool = &copied
		}
	}
}

// AgentSnapshot is a read-only copy of AgentState, safe to marshal/share.
type AgentSnapshot struct {
	Instance     string           `json:"instance"`
	Role         string           `json:"role"`
	Status       AgentStatus      `json:"status"`
	SessionID    string           `json:"session_id,omitempty"`
	Turns        int              `json:"turns"`
	CostUsd      float64          `json:"cost_usd"`
	DurationMs   int              `json:"duration_ms"`
	LastActivity time.Time        `json:"last_activity"`
	SocketAlive  bool             `json:"socket_alive"`
	PendingTool  *PendingToolCall `json:"pending_tool,omitempty"`
	ToolCounts   map[string]int   `json:"tool_counts"`
}

// Snapshot returns a read-only copy of the current state.
func (a *AgentState) Snapshot() AgentSnapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()

	snap := AgentSnapshot{
		Instance:     a.Instance,
		Role:         a.Role,
		Status:       a.Status,
		SessionID:    a.SessionID,
		Turns:        a.Turns,
		CostUsd:      a.CostUsd,
		DurationMs:   a.DurationMs,
		LastActivity: a.LastActivity,
		SocketAlive:  a.SocketAlive,
		ToolCounts:   make(map[string]int, len(a.ToolCounts)),
	}
	for k, v := range a.ToolCounts {
		snap.ToolCounts[k] = v
	}
	if a.PendingTool != nil {
		copied := *a.PendingTool
		copied.AgeMs = time.Since(copied.StartedAt).Milliseconds()
		snap.PendingTool = &copied
	}
	return snap
}

// RecentEvents returns a copy of the ring buffer.
func (a *AgentState) RecentEvents(limit int) []Envelope {
	a.mu.RLock()
	defer a.mu.RUnlock()

	events := a.recentEvents
	if limit > 0 && limit < len(events) {
		events = events[len(events)-limit:]
	}
	result := make([]Envelope, len(events))
	copy(result, events)
	return result
}
