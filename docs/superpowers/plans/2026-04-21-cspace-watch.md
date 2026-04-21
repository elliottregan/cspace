# cspace watch TUI Visualizer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `cspace watch`, a live split-pane terminal dashboard showing running agents, ports, shared service health, and a mixed activity stream.

**Architecture:** `cspace watch` on the host exec's into a running container via `instance.DcExecInteractive`, then `cspace watch --inside` (running inside the container) connects to the diagnostics server at `localhost:8384`, auto-starting it if needed, and launches a Bubbletea TUI. The TUI left panel shows agents and services; the right panel streams live tool-use and assistant-text events.

**Tech Stack:** `github.com/charmbracelet/bubbletea`, `github.com/charmbracelet/lipgloss` (already in go.mod), `golang.org/x/net/websocket` (already in go.mod), `docker` CLI via `os/exec` (existing project pattern), `net/http` for diagnostics health + agent polling.

---

## File Map

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/tui/event.go` | Create | `EventRow` type + `ParseEnvelope` — converts `diagnostics.Envelope` to display rows |
| `internal/tui/event_test.go` | Create | Unit tests for `ParseEnvelope` |
| `internal/tui/docker.go` | Create | `ServiceStatus` type + `probeSharedServices()` — Docker CLI health checks |
| `internal/tui/docker_test.go` | Create | Unit tests for Docker output parsing |
| `internal/tui/messages.go` | Create | All `tea.Msg` types |
| `internal/tui/styles.go` | Create | Lipgloss style definitions |
| `internal/tui/wsclient.go` | Create | WebSocket client with auto-reconnect |
| `internal/tui/left.go` | Create | Left panel renderer (agents + services) |
| `internal/tui/left_test.go` | Create | Left panel rendering tests |
| `internal/tui/right.go` | Create | Right panel renderer (activity stream) |
| `internal/tui/right_test.go` | Create | Right panel rendering tests |
| `internal/tui/model.go` | Create | Root Bubbletea model (Init, Update, View) |
| `internal/tui/model_test.go` | Create | Model Update logic tests |
| `internal/tui/helpers.go` | Create | Small stdlib wrappers (getenv, jsonDecode) |
| `internal/cli/watch.go` | Create | `newWatchCmd()` — host wrapper + `--inside` mode |
| `internal/cli/watch_test.go` | Create | Command structure test |
| `internal/cli/root.go` | Modify | Register `newWatchCmd()` in supervisor group |

---

## Task 1: EventRow type and ParseEnvelope

**Files:**
- Create: `internal/tui/event.go`
- Create: `internal/tui/event_test.go`

The `Envelope.SDK` field is a `StreamEvent` JSON (from `internal/supervisor/stream.go`).
`StreamEvent` fields: `type` (string), `subtype` (string), `message` (raw JSON message), `cost_usd` (json.Number), `num_turns` (json.Number).
For `type="assistant"`, `message` is a Claude SDK message with a `content` array of objects with `type`, `text`, `name`, `input` fields — same shape as `messageContent` in supervisor/stream.go but redefined locally.

- [ ] **Step 1.1: Write the failing tests**

```go
// internal/tui/event_test.go
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
```

- [ ] **Step 1.2: Run tests to confirm they fail**

```
go test ./internal/tui/ -run TestParseEnvelope -v 2>&1 | head -20
```

Expected: FAIL — package `internal/tui` does not exist yet.

- [ ] **Step 1.3: Implement event.go**

```go
// internal/tui/event.go
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
```

- [ ] **Step 1.4: Run tests to confirm they pass**

```
cd /workspace && go test ./internal/tui/ -run TestParseEnvelope -v
```

Expected: all `TestParseEnvelope_*` PASS.

- [ ] **Step 1.5: Commit**

```
git add internal/tui/event.go internal/tui/event_test.go
git commit -m "Add tui EventRow type and ParseEnvelope"
```

---

## Task 2: ServiceStatus and Docker health checks

**Files:**
- Create: `internal/tui/docker.go`
- Create: `internal/tui/docker_test.go`

Docker is queried via `os/exec` + `docker inspect --format {{.State.Running}} <name>` — same pattern used throughout `internal/docker/docker.go` and `internal/instance/instance.go`.

- [ ] **Step 2.1: Write the failing tests**

```go
// internal/tui/docker_test.go
package tui

import (
	"testing"
)

func TestParseRunning(t *testing.T) {
	tests := []struct {
		output string
		want   bool
	}{
		{"true\n", true},
		{"false\n", false},
		{"", false},
		{"error\n", false},
	}
	for _, tt := range tests {
		got := parseRunning(tt.output)
		if got != tt.want {
			t.Errorf("parseRunning(%q) = %v, want %v", tt.output, got, tt.want)
		}
	}
}

func TestSharedContainerDefs(t *testing.T) {
	if len(sharedContainerDefs) == 0 {
		t.Fatal("sharedContainerDefs must not be empty")
	}
	for _, d := range sharedContainerDefs {
		if d.name == "" {
			t.Error("container def has empty name")
		}
		if d.label == "" {
			t.Error("container def has empty label")
		}
	}
}
```

- [ ] **Step 2.2: Run tests to confirm they fail**

```
cd /workspace && go test ./internal/tui/ -run "TestParseRunning|TestSharedContainerDefs" -v 2>&1 | head -10
```

Expected: FAIL — functions not defined yet.

- [ ] **Step 2.3: Implement docker.go**

```go
// internal/tui/docker.go
package tui

import (
	"os/exec"
	"strings"
)

// ServiceStatus represents the health of one shared cspace service.
type ServiceStatus struct {
	Name    string
	Port    string // host-bound port string e.g. ":80"; empty if not published
	Label   string // "proxy", "dns", "browser", "cdp"
	Running bool
	Slow    bool // running but degraded (reserved for future use)
}

type sharedContainerDef struct {
	name  string
	label string
	port  string
}

// sharedContainerDefs lists the global shared cspace services to health-check.
// These run as part of cspace-proxy (docker-compose.shared.yml) and the
// per-project shared sidecars.
var sharedContainerDefs = []sharedContainerDef{
	{"cspace-proxy", "proxy", ":80"},
	{"cspace-dns", "dns", ":53"},
	{"cs.playwright", "browser", ""},
	{"cs.chromium-cdp", "cdp", ":9222"},
}

// ProbeSharedServices queries Docker for the running state of each shared service.
// Called from inside a container where /var/run/docker.sock is bind-mounted.
func ProbeSharedServices() []ServiceStatus {
	return probeSharedServices()
}

func probeSharedServices() []ServiceStatus {
	out := make([]ServiceStatus, 0, len(sharedContainerDefs))
	for _, d := range sharedContainerDefs {
		raw, err := exec.Command(
			"docker", "inspect", "--format", "{{.State.Running}}", d.name,
		).Output()
		out = append(out, ServiceStatus{
			Name:    d.name,
			Port:    d.port,
			Label:   d.label,
			Running: err == nil && parseRunning(string(raw)),
		})
	}
	return out
}

// parseRunning returns true when docker inspect output is "true".
func parseRunning(output string) bool {
	return strings.TrimSpace(output) == "true"
}
```

- [ ] **Step 2.4: Run tests to confirm they pass**

```
cd /workspace && go test ./internal/tui/ -run "TestParseRunning|TestSharedContainerDefs" -v
```

Expected: PASS.

- [ ] **Step 2.5: Commit**

```
git add internal/tui/docker.go internal/tui/docker_test.go
git commit -m "Add tui ServiceStatus and Docker health probe"
```

---

## Task 3: Messages and styles

**Files:**
- Create: `internal/tui/messages.go`
- Create: `internal/tui/styles.go`
- Create: `internal/tui/helpers.go`

No unit tests for pure type/style definitions.

- [ ] **Step 3.1: Create messages.go**

```go
// internal/tui/messages.go
package tui

import (
	"time"

	"github.com/elliottregan/cspace/internal/diagnostics"
)

// AgentsUpdatedMsg carries a fresh snapshot of all agents from GET /agents.
type AgentsUpdatedMsg []diagnostics.AgentSnapshot

// EventReceivedMsg carries one live event from the WebSocket stream.
type EventReceivedMsg diagnostics.Envelope

// ServicesUpdatedMsg carries fresh shared-service health from Docker.
type ServicesUpdatedMsg []ServiceStatus

// TickMsg fires on the 2-second HTTP poll interval.
type TickMsg time.Time

// WSStatusMsg signals a WebSocket connect or disconnect.
type WSStatusMsg struct{ Connected bool }
```

- [ ] **Step 3.2: Create styles.go**

```go
// internal/tui/styles.go
package tui

import "github.com/charmbracelet/lipgloss"

var (
	colorActive  = lipgloss.Color("#3fb950")
	colorIdle    = lipgloss.Color("#e3b341")
	colorStuck   = lipgloss.Color("#f85149")
	colorExited  = lipgloss.Color("#6e7681")
	colorBlue    = lipgloss.Color("#58a6ff")
	colorDim     = lipgloss.Color("#8b949e")
	colorBorder  = lipgloss.Color("#21262d")
	colorBgLight = lipgloss.Color("#161b22")

	styleSection   = lipgloss.NewStyle().Foreground(colorDim).PaddingLeft(1)
	styleAgentName = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#e6edf3"))
	styleRole      = lipgloss.NewStyle().Foreground(colorDim)

	styleDotActive = lipgloss.NewStyle().Foreground(colorActive)
	styleDotIdle   = lipgloss.NewStyle().Foreground(colorIdle)
	styleDotStuck  = lipgloss.NewStyle().Foreground(colorStuck)
	styleDotExited = lipgloss.NewStyle().Foreground(colorExited)

	styleStuckWarn = lipgloss.NewStyle().Foreground(colorStuck)
	styleContent   = lipgloss.NewStyle().Foreground(lipgloss.Color("#c9d1d9"))
	styleMeta      = lipgloss.NewStyle().Foreground(colorDim)
	styleBorder    = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, true, false, false).
			BorderForeground(colorBorder)

	styleEvTime     = lipgloss.NewStyle().Foreground(colorDim).Width(8)
	styleEvInstance = lipgloss.NewStyle().Width(10)
	styleEvType     = lipgloss.NewStyle().Width(8)

	styleEvTypeTool   = styleEvType.Copy().Foreground(colorIdle)
	styleEvTypeText   = styleEvType.Copy().Foreground(colorDim)
	styleEvTypeResult = styleEvType.Copy().Foreground(colorActive)

	styleTitle = lipgloss.NewStyle().
			Background(colorBgLight).
			Foreground(colorBlue).
			Bold(true)
	styleStatusBar = lipgloss.NewStyle().
			Background(colorBgLight).
			Foreground(colorDim)
	styleKeyHint   = lipgloss.NewStyle().Background(colorBgLight).Foreground(lipgloss.Color("#8b949e"))
	styleFutureKey = lipgloss.NewStyle().Background(colorBgLight).Foreground(lipgloss.Color("#30363d"))
)

// instanceColor returns a stable per-instance color from a fixed palette.
func instanceColor(name string) lipgloss.Color {
	palette := []lipgloss.Color{
		"#58a6ff", "#bc8cff", "#f0883e", "#f85149", "#3fb950", "#e3b341",
	}
	h := 0
	for _, r := range name {
		h = (h*31 + int(r)) % len(palette)
	}
	return palette[h]
}
```

- [ ] **Step 3.3: Create helpers.go**

```go
// internal/tui/helpers.go
package tui

import (
	"encoding/json"
	"io"
	"os"
)

func getenv(key string) string { return os.Getenv(key) }

func jsonDecode(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}
```

- [ ] **Step 3.4: Verify package compiles**

```
cd /workspace && go build ./internal/tui/...
```

Expected: no errors.

- [ ] **Step 3.5: Commit**

```
git add internal/tui/messages.go internal/tui/styles.go internal/tui/helpers.go
git commit -m "Add tui message types, Lipgloss styles, and helpers"
```

---

## Task 4: WebSocket client

**Files:**
- Create: `internal/tui/wsclient.go`

Uses `golang.org/x/net/websocket` (already in go.mod). No unit tests — verified manually against a running diagnostics server.

- [ ] **Step 4.1: Implement wsclient.go**

```go
// internal/tui/wsclient.go
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/net/websocket"

	"github.com/elliottregan/cspace/internal/diagnostics"
)

// WSClient manages the WebSocket connection to the diagnostics server.
type WSClient struct {
	addr    string
	program *tea.Program
}

// NewWSClient constructs a WebSocket client for the given address.
func NewWSClient(addr string, program *tea.Program) *WSClient {
	return &WSClient{addr: addr, program: program}
}

// Run connects and pumps events into the Bubbletea program. Reconnects
// automatically on disconnect. Blocks until ctx is cancelled.
func (c *WSClient) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		_ = c.connect(ctx)
		if ctx.Err() != nil {
			return
		}

		c.program.Send(WSStatusMsg{Connected: false})
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (c *WSClient) connect(ctx context.Context) error {
	url := fmt.Sprintf("ws://%s/ws", c.addr)
	origin := fmt.Sprintf("http://%s/", c.addr)

	ws, err := websocket.Dial(url, "", origin)
	if err != nil {
		return err
	}
	defer ws.Close()

	c.program.Send(WSStatusMsg{Connected: true})

	sub, _ := json.Marshal(diagnostics.WSMessage{Subscribe: []string{"*"}})
	if _, err := ws.Write(sub); err != nil {
		return err
	}

	go func() {
		<-ctx.Done()
		ws.Close()
	}()

	for {
		var raw []byte
		if err := websocket.Message.Receive(ws, &raw); err != nil {
			return err
		}
		var reply diagnostics.WSReply
		if err := json.Unmarshal(raw, &reply); err != nil {
			continue
		}
		if reply.Type == "event" && reply.Event != nil {
			var env diagnostics.Envelope
			if err := json.Unmarshal(reply.Event, &env); err != nil {
				continue
			}
			c.program.Send(EventReceivedMsg(env))
		}
	}
}
```

- [ ] **Step 4.2: Verify package compiles**

```
cd /workspace && go build ./internal/tui/...
```

Expected: no errors.

- [ ] **Step 4.3: Commit**

```
git add internal/tui/wsclient.go
git commit -m "Add tui WebSocket client with auto-reconnect"
```

---

## Task 5: Left panel renderer

**Files:**
- Create: `internal/tui/left.go`
- Create: `internal/tui/left_test.go`

- [ ] **Step 5.1: Write the failing tests**

```go
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
```

- [ ] **Step 5.2: Run tests to confirm they fail**

```
cd /workspace && go test ./internal/tui/ -run TestRenderLeft -v 2>&1 | head -15
```

Expected: FAIL — `RenderLeft` undefined.

- [ ] **Step 5.3: Implement left.go**

```go
// internal/tui/left.go
package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/elliottregan/cspace/internal/diagnostics"
)

func statusPriority(s diagnostics.AgentStatus) int {
	switch s {
	case diagnostics.StatusStuck:
		return 0
	case diagnostics.StatusActive:
		return 1
	case diagnostics.StatusIdle:
		return 2
	default:
		return 3
	}
}

// RenderLeft renders the left panel at the given width and height.
func RenderLeft(agents []diagnostics.AgentSnapshot, services []ServiceStatus, width, _ int) string {
	sorted := make([]diagnostics.AgentSnapshot, len(agents))
	copy(sorted, agents)
	sort.Slice(sorted, func(i, j int) bool {
		pi, pj := statusPriority(sorted[i].Status), statusPriority(sorted[j].Status)
		if pi != pj {
			return pi < pj
		}
		return sorted[i].Instance < sorted[j].Instance
	})

	var sb strings.Builder
	sb.WriteString(styleSection.Render("AGENTS") + "\n")
	for _, a := range sorted {
		sb.WriteString(renderAgentBlock(a, width))
	}

	if len(services) > 0 {
		sb.WriteString(strings.Repeat("─", width-2) + "\n")
		sb.WriteString(styleSection.Render("SHARED SERVICES") + "\n")
		for _, svc := range services {
			sb.WriteString(renderServiceRow(svc) + "\n")
		}
	}

	sb.WriteString(strings.Repeat("─", width-2) + "\n")
	sb.WriteString(styleSection.Render("DIAGNOSTICS") + "\n")
	sb.WriteString(" " + styleDotActive.Render("●") + " " + styleMeta.Render("diagnostics :8384") + "\n")
	return sb.String()
}

func renderAgentBlock(a diagnostics.AgentSnapshot, _ int) string {
	dot, borderColor := agentDotAndColor(a.Status)

	var block strings.Builder
	block.WriteString(dot + " " + styleAgentName.Render(a.Instance) +
		"  " + styleRole.Render(a.Role) + "\n")
	block.WriteString(styleMeta.Render(
		fmt.Sprintf("  %s · t:%d · $%.3f", string(a.Status), a.Turns, a.CostUsd),
	) + "\n")

	if a.Status == diagnostics.StatusStuck && a.PendingTool != nil {
		elapsed := time.Since(a.PendingTool.StartedAt).Round(time.Second)
		block.WriteString(styleStuckWarn.Render(
			fmt.Sprintf("  %s · %s", a.PendingTool.Tool, elapsed),
		) + "\n")
	}

	border := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(borderColor).
		PaddingLeft(1)
	return border.Render(block.String()) + "\n"
}

func agentDotAndColor(status diagnostics.AgentStatus) (string, lipgloss.Color) {
	switch status {
	case diagnostics.StatusActive:
		return styleDotActive.Render("●"), colorActive
	case diagnostics.StatusIdle:
		return styleDotIdle.Render("●"), colorIdle
	case diagnostics.StatusStuck:
		return styleDotStuck.Render("●"), colorStuck
	default:
		return styleDotExited.Render("●"), colorExited
	}
}

func renderServiceRow(svc ServiceStatus) string {
	dot := styleDotStuck.Render("●")
	if svc.Running {
		dot = styleDotActive.Render("●")
	}
	port := ""
	if svc.Port != "" {
		port = " " + lipgloss.NewStyle().Foreground(colorBlue).Render(svc.Port)
	}
	return " " + dot + " " + styleContent.Render(svc.Name) + port + " " + styleMeta.Render(svc.Label)
}
```

- [ ] **Step 5.4: Run tests to confirm they pass**

```
cd /workspace && go test ./internal/tui/ -run TestRenderLeft -v
```

Expected: all PASS.

- [ ] **Step 5.5: Commit**

```
git add internal/tui/left.go internal/tui/left_test.go
git commit -m "Add tui left panel renderer"
```

---

## Task 6: Right panel renderer

**Files:**
- Create: `internal/tui/right.go`
- Create: `internal/tui/right_test.go`

- [ ] **Step 6.1: Write the failing tests**

```go
// internal/tui/right_test.go
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
```

- [ ] **Step 6.2: Run tests to confirm they fail**

```
cd /workspace && go test ./internal/tui/ -run TestRenderRight -v 2>&1 | head -10
```

Expected: FAIL — `RenderRight` undefined.

- [ ] **Step 6.3: Implement right.go**

```go
// internal/tui/right.go
package tui

import (
	"fmt"
	"strings"
)

// RenderRight renders the activity stream panel.
// filter, if non-empty, restricts display to rows from that instance.
// events[0] is oldest; only the last height-3 rows are shown.
func RenderRight(events []EventRow, filter string, width, height int) string {
	var sb strings.Builder

	filterLabel := "[all agents]"
	if filter != "" {
		filterLabel = "[" + filter + "]"
	}
	sb.WriteString(styleSection.Render(fmt.Sprintf("Activity stream %s", filterLabel)) + "\n")
	sb.WriteString(strings.Repeat("─", width-1) + "\n")

	var visible []EventRow
	for _, e := range events {
		if filter == "" || e.Instance == filter {
			visible = append(visible, e)
		}
	}

	maxRows := height - 3
	if maxRows < 1 {
		maxRows = 1
	}
	if len(visible) > maxRows {
		visible = visible[len(visible)-maxRows:]
	}

	for _, e := range visible {
		sb.WriteString(renderEventRow(e, width) + "\n")
	}
	return sb.String()
}

func renderEventRow(e EventRow, width int) string {
	evTime := styleEvTime.Render(e.Time.Format("15:04:05"))
	evInst := styleEvInstance.Copy().Foreground(instanceColor(e.Instance)).Render(e.Instance)

	var evType, evContent string
	switch e.Kind {
	case KindTool:
		evType = styleEvTypeTool.Render(e.ToolName)
		evContent = styleContent.Render(e.Content)
	case KindText:
		evType = styleEvTypeText.Render("text")
		evContent = styleContent.Render(e.Content)
	case KindResult:
		evType = styleEvTypeResult.Render("result")
		evContent = styleEvTypeResult.Render(e.Content)
	}

	used := 8 + 10 + 8
	if remaining := width - used - 2; remaining > 0 {
		evContent = styleContent.MaxWidth(remaining).Render(e.Content)
	}

	return evTime + evInst + evType + evContent
}
```

- [ ] **Step 6.4: Run tests to confirm they pass**

```
cd /workspace && go test ./internal/tui/ -run TestRenderRight -v
```

Expected: all PASS.

- [ ] **Step 6.5: Commit**

```
git add internal/tui/right.go internal/tui/right_test.go
git commit -m "Add tui right panel renderer (activity stream)"
```

---

## Task 7: Bubbletea model

**Files:**
- Create: `internal/tui/model.go`
- Create: `internal/tui/model_test.go`

- [ ] **Step 7.1: Write the failing tests**

```go
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
```

- [ ] **Step 7.2: Run tests to confirm they fail**

```
cd /workspace && go test ./internal/tui/ -run "TestModel_" -v 2>&1 | head -15
```

Expected: FAIL — `Model`, `NewModel`, `maxEvents` undefined.

- [ ] **Step 7.3: Implement model.go**

```go
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
```

- [ ] **Step 7.4: Run tests to confirm they pass**

```
cd /workspace && go test ./internal/tui/ -run "TestModel_" -v
```

Expected: all PASS.

- [ ] **Step 7.5: Commit**

```
git add internal/tui/model.go internal/tui/model_test.go
git commit -m "Add tui Bubbletea model with filter cycling and event ring buffer"
```

---

## Task 8: `cspace watch` command

**Files:**
- Create: `internal/cli/watch.go`
- Create: `internal/cli/watch_test.go`
- Modify: `internal/cli/root.go`

**Architecture:**
- On the host (`--inside` not set): find a running container for this project via `instance.GetInstances(cfg)` + `instance.IsRunning(composeName)`, then call `instance.DcExecInteractive(composeName, "cspace", "watch", "--inside")` to run the TUI inside the container.
- Inside the container (`--inside` set): check `GET localhost:8384/health`; if not reachable, start `cspace diagnostics-server` as a detached subprocess (using `os.Executable()` to get the binary path) and wait up to 5s for it to come up; then launch the Bubbletea program with a background WSClient goroutine.

- [ ] **Step 8.1: Write the failing command test**

```go
// internal/cli/watch_test.go
package cli

import (
	"testing"
)

func TestWatchCmdExists(t *testing.T) {
	cmd := newWatchCmd()
	if cmd.Use == "" {
		t.Fatal("expected newWatchCmd to return a configured command")
	}
	if cmd.Short == "" {
		t.Error("expected Short description to be set")
	}
}

func TestWatchCmdFlags(t *testing.T) {
	cmd := newWatchCmd()
	if f := cmd.Flags().Lookup("addr"); f == nil {
		t.Error("expected --addr flag")
	}
	insideFlag := cmd.Flags().Lookup("inside")
	if insideFlag == nil {
		t.Error("expected --inside flag")
	} else if !insideFlag.Hidden {
		t.Error("expected --inside to be hidden")
	}
}
```

- [ ] **Step 8.2: Run tests to confirm they fail**

```
cd /workspace && go test ./internal/cli/ -run TestWatchCmd -v 2>&1 | head -10
```

Expected: FAIL — `newWatchCmd` undefined.

- [ ] **Step 8.3: Implement watch.go**

The command struct and flag registration follow the same pattern as `internal/cli/ssh.go`.

For `runWatchHost`:
- Call `instance.GetInstances(cfg)` to list instances.
- For each, call `cfg.ComposeName(name)` and `instance.IsRunning(composeName)` to find a running one.
- If `len(args) > 0`, use `args[0]` as the target instance directly.
- Call `instance.RequireRunning(composeName, targetInstance)` to verify.
- Call `instance.DcExecInteractive(composeName, "cspace", "watch", "--inside")` — this gives the container full stdin/stdout/stderr so Bubbletea renders correctly, same mechanism as `cspace ssh`.

For `runWatchInside`:
- Check `GET http://<addr>/health` with `net/http`.
- If not alive: obtain binary path via `os.Executable()`, start `diagnostics-server` as a subprocess with `Stdout` and `Stderr` set to `io.Discard`, call `Start()` (not `Run()`), then poll health every 200ms for up to 5s.
- Create `tui.NewModel(addr)`, create `tea.NewProgram(m, tea.WithAltScreen())`.
- Start `tui.NewWSClient(addr, p).Run(ctx)` in a goroutine with a cancellable context.
- Start a 30s ticker in a goroutine that calls `p.Send(tui.ServicesUpdatedMsg(tui.ProbeSharedServices()))`.
- Call `p.Run()` and return its error.

```go
// internal/cli/watch.go
package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/elliottregan/cspace/internal/instance"
	"github.com/elliottregan/cspace/internal/tui"
)

func newWatchCmd() *cobra.Command {
	var addr string
	var inside bool

	cmd := &cobra.Command{
		Use:     "watch [name]",
		Short:   "Live TUI dashboard for running agents and services",
		GroupID: "supervisor",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if inside {
				return runWatchInside(addr)
			}
			return runWatchHost(args)
		},
	}

	cmd.Flags().StringVar(&addr, "addr", "localhost:8384", "Diagnostics server address")
	cmd.Flags().BoolVar(&inside, "inside", false, "Run TUI inside container (set by host wrapper)")
	_ = cmd.Flags().MarkHidden("inside")
	return cmd
}

func runWatchHost(args []string) error {
	var target string
	if len(args) > 0 {
		target = args[0]
	} else {
		for _, name := range instance.GetInstances(cfg) {
			if instance.IsRunning(cfg.ComposeName(name)) {
				target = name
				break
			}
		}
	}
	if target == "" {
		return fmt.Errorf("no running instances for project %q — start one with: cspace up", cfg.Project.Name)
	}
	composeName := cfg.ComposeName(target)
	if err := instance.RequireRunning(composeName, target); err != nil {
		return err
	}
	return instance.DcExecInteractive(composeName, "cspace", "watch", "--inside")
}

func runWatchInside(addr string) error {
	if err := ensureDiagnosticsServer(addr); err != nil {
		return err
	}
	m := tui.NewModel(addr)
	p := tea.NewProgram(m, tea.WithAltScreen())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tui.NewWSClient(addr, p).Run(ctx)

	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				p.Send(tui.ServicesUpdatedMsg(tui.ProbeSharedServices()))
			}
		}
	}()

	_, err := p.Run()
	return err
}

func ensureDiagnosticsServer(addr string) error {
	if serverAlive(addr) {
		return nil
	}
	bin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not locate cspace binary: %w", err)
	}
	srv := exec.Command(bin, "diagnostics-server")
	srv.Stdout = io.Discard
	srv.Stderr = io.Discard
	if err := srv.Start(); err != nil {
		return fmt.Errorf("could not start diagnostics server: %w", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if serverAlive(addr) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("diagnostics server at %s did not start in time", addr)
}

func serverAlive(addr string) bool {
	resp, err := http.Get("http://" + addr + "/health")
	return err == nil && resp.StatusCode == http.StatusOK
}
```

- [ ] **Step 8.4: Run command tests to confirm they pass**

```
cd /workspace && go test ./internal/cli/ -run TestWatchCmd -v
```

Expected: PASS.

- [ ] **Step 8.5: Register command in root.go**

In `internal/cli/root.go`, add `newWatchCmd()` to the `root.AddCommand(...)` call alongside the other supervisor commands (`newSendCmd`, `newInterruptCmd`, etc.):

```go
newWatchCmd(),
```

- [ ] **Step 8.6: Build the binary**

```
cd /workspace && make build
```

Expected: success, no errors.

- [ ] **Step 8.7: Verify watch appears in help and --inside is hidden**

```
./bin/cspace-go watch --help
```

Expected: shows `--addr`, no `--inside` in output. GroupID shows under supervisor.

- [ ] **Step 8.8: Commit**

```
git add internal/cli/watch.go internal/cli/watch_test.go internal/cli/root.go
git commit -m "Add cspace watch command (host wrapper + inside mode)"
```

---

## Task 9: Full test suite verification

- [ ] **Step 9.1: Run all tui tests**

```
cd /workspace && go test ./internal/tui/... -v
```

Expected: all PASS.

- [ ] **Step 9.2: Run all cli tests**

```
cd /workspace && go test ./internal/cli/... -v
```

Expected: all PASS including `TestWatchCmd*`.

- [ ] **Step 9.3: Run full suite**

```
cd /workspace && make test
```

Expected: all tests PASS, no regressions in existing packages.

- [ ] **Step 9.4: Fix any regressions, commit**

```
git add -p
git commit -m "Fix any test regressions from cspace watch integration"
```

---

## Notes for Implementers

- `styleEvTypeTool` uses `.Copy()` to avoid mutating the base `styleEvType` — required with Lipgloss v1.x.
- The `diagnostics.WSReply.Event` field is `json.RawMessage`; unmarshal it into `diagnostics.Envelope` before passing to `EventReceivedMsg`.
- `instance.GetInstances(cfg)` returns `[]string` of bare instance names (e.g., `["mercury", "venus"]`).
- The cspace binary is available at `/opt/cspace/bin/cspace` inside containers (symlinked into PATH), so `DcExecInteractive(composeName, "cspace", "watch", "--inside")` works without a full path.
- If `diagnostics.WSReply` struct fields differ from what is shown here, check `internal/diagnostics/ws.go` directly — that file is the source of truth.
