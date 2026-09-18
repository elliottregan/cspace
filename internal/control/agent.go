package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/elliottregan/cspace/internal/registry"
)

// DefaultSession is the supervisor's own session id (SESSION_ID in
// lib/agent-supervisor-bun/src/main.ts). It is hardcoded there — do not make
// it configurable here.
//
// Unrelated to SessionClaude / SessionShell in control.go: those are tmux
// session names inside the sandbox, this is the headless supervisor's session
// id on disk and over its control port.
const DefaultSession = "primary"

// AgentStatus reports the sandbox supervisor's authenticated GET /status.
//
// An unreachable supervisor is deliberately NOT an error: it yields
// AgentStatus{Reachable: false} with a nil error, matching how Snapshot
// treats a failed probe — the row degrades, the query does not fail. A
// non-nil error means the sandbox could not be resolved at all.
func (c *Client) AgentStatus(ctx context.Context, project, sandbox string) (AgentStatus, error) {
	e, err := c.lookup(project, sandbox)
	if err != nil {
		return AgentStatus{}, err
	}
	st, _ := c.probeStatus(ctx, e)
	return st, nil
}

// Send injects a user turn into the sandbox supervisor's prompt queue. An
// empty session means DefaultSession.
func (c *Client) Send(ctx context.Context, project, sandbox, session, text string) error {
	e, err := c.lookup(project, sandbox)
	if err != nil {
		return err
	}
	if session == "" {
		session = DefaultSession
	}
	payload, err := json.Marshal(map[string]string{"session": session, "text": text})
	if err != nil {
		return fmt.Errorf("encode send payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.ControlURL+"/send", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	authorize(req, e)

	resp, body, err := c.do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("status %d: %s", resp.StatusCode, ErrorText(body))
	}
	return nil
}

// Interrupt cancels the sandbox agent's in-flight task.
//
// A 409 is not a failure: main.ts answers {"ok":false,"error":"no active
// task"} when nothing is running, which means the agent was simply idle. The
// dashboard has always reported that as a benign outcome, and so does this.
func (c *Client) Interrupt(ctx context.Context, project, sandbox string) error {
	e, err := c.lookup(project, sandbox)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.ControlURL+"/interrupt", nil)
	if err != nil {
		return err
	}
	authorize(req, e)

	resp, body, err := c.do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusConflict {
		return nil
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("status %d: %s", resp.StatusCode, ErrorText(body))
	}
	return nil
}

// lookup resolves a sandbox's registry entry, wrapping the failure so the
// caller's error names the sandbox rather than a bare map miss.
func (c *Client) lookup(project, sandbox string) (registry.Entry, error) {
	e, err := c.entries.Lookup(project, sandbox)
	if err != nil {
		return registry.Entry{}, fmt.Errorf("look up sandbox %s/%s: %w", project, sandbox, err)
	}
	return e, nil
}

// authorize attaches the sandbox's bearer token. The supervisor enforces it
// on every route, so an entry with no token can only be a pre-auth sandbox.
func authorize(req *http.Request, e registry.Entry) {
	if e.Token != "" {
		req.Header.Set("Authorization", "Bearer "+e.Token)
	}
}

// do runs an action request and reads its whole body, which every caller
// needs for ErrorText.
func (c *Client) do(req *http.Request) (*http.Response, []byte, error) {
	resp, err := c.actionClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp, body, nil
}

// ErrorText extracts the meaningful error text from a non-2xx supervisor
// response body. main.ts's envelope is {"ok":false,"error":"…"}; when the
// body parses as that shape, return just the error field so callers don't
// print a raw JSON envelope at the user. Anything else (e.g. a plain-text
// http.Error body) falls back to the trimmed raw body.
func ErrorText(body []byte) string {
	var parsed struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Error != "" {
		return parsed.Error
	}
	return strings.TrimSpace(string(body))
}
