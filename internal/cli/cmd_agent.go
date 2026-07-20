package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// agentClientTimeout bounds each /status and /interrupt round trip to a
// sandbox's own control port. Both are near-instant control-channel calls
// (unlike, say, browserRestartClientTimeout's ladder wait), so a short bound
// is enough.
const agentClientTimeout = 10 * time.Second

// newAgentCmd groups the sandbox agent's steering operators: status and
// interrupt (spec §4). Both resolve the target sandbox's control URL +
// token via resolveEntry (cmd_send.go) — which is itself already
// dual-context (host: reads ~/.cspace/sandbox-registry.json directly;
// in-sandbox: queries the host daemon over CSPACE_REGISTRY_URL) — so unlike
// cmd_browser.go's restart command (whose ACTION differs by context: direct
// call vs. daemon round-trip), status/interrupt run the identical HTTP call
// against the sandbox's own control port in both contexts.
func newAgentCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "agent",
		Short: "Steer a sandbox's agent session (status, interrupt)",
	}
	parent.AddCommand(newAgentStatusCmd())
	parent.AddCommand(newAgentInterruptCmd())
	return parent
}

func newAgentStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <sandbox>",
		Short: "Print a sandbox agent's steering status (session, state, queue depth, last event)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = cmd.Root().Context()
			}
			return runAgentStatus(ctx, cmd.OutOrStdout(), projectName(), args[0])
		},
	}
}

func newAgentInterruptCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "interrupt <sandbox>",
		Short: "Interrupt a sandbox agent's in-flight task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = cmd.Root().Context()
			}
			return runAgentInterrupt(ctx, cmd.OutOrStdout(), projectName(), args[0])
		},
	}
}

// agentStatusResponse mirrors GET /status's success body
// (lib/agent-supervisor-bun/src/main.ts).
type agentStatusResponse struct {
	OK               bool   `json:"ok"`
	Session          string `json:"session"`
	State            string `json:"state"`
	LastEventTs      string `json:"lastEventTs"`
	LastEventType    string `json:"lastEventType"`
	LastEventSubtype string `json:"lastEventSubtype"`
	QueueDepth       int    `json:"queueDepth"`
}

// runAgentStatus resolves the sandbox's control URL + token (resolveEntry,
// cmd_send.go) and prints one line per GET /status field.
func runAgentStatus(ctx context.Context, out io.Writer, project, sandbox string) error {
	entry, err := resolveEntry(project, sandbox)
	if err != nil {
		return fmt.Errorf("look up sandbox registry entry: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, entry.ControlURL+"/status", nil)
	if err != nil {
		return err
	}
	if entry.Token != "" {
		req.Header.Set("Authorization", "Bearer "+entry.Token)
	}

	client := &http.Client{Timeout: agentClientTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("get %s/status: %w", entry.ControlURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("status failed: status %d: %s", resp.StatusCode, agentErrorText(body))
	}

	var status agentStatusResponse
	if err := json.Unmarshal(body, &status); err != nil {
		return fmt.Errorf("decode status response: %w", err)
	}

	_, _ = fmt.Fprintf(out, "session:          %s\n", status.Session)
	_, _ = fmt.Fprintf(out, "state:            %s\n", status.State)
	_, _ = fmt.Fprintf(out, "lastEventTs:      %s\n", status.LastEventTs)
	_, _ = fmt.Fprintf(out, "lastEventType:    %s\n", status.LastEventType)
	_, _ = fmt.Fprintf(out, "lastEventSubtype: %s\n", status.LastEventSubtype)
	_, _ = fmt.Fprintf(out, "queueDepth:       %d\n", status.QueueDepth)
	return nil
}

// runAgentInterrupt resolves the sandbox's control URL + token
// (resolveEntry, cmd_send.go) and POSTs /interrupt, printing "ok" on
// success. A non-2xx response (e.g. 409 {"ok":false,"error":"no active
// task"} when nothing is running) surfaces the server's error text and
// returns a non-nil error so the cobra layer exits non-zero.
func runAgentInterrupt(ctx context.Context, out io.Writer, project, sandbox string) error {
	entry, err := resolveEntry(project, sandbox)
	if err != nil {
		return fmt.Errorf("look up sandbox registry entry: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, entry.ControlURL+"/interrupt", nil)
	if err != nil {
		return err
	}
	if entry.Token != "" {
		req.Header.Set("Authorization", "Bearer "+entry.Token)
	}

	client := &http.Client{Timeout: agentClientTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("post %s/interrupt: %w", entry.ControlURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("interrupt failed: status %d: %s", resp.StatusCode, agentErrorText(body))
	}

	_, _ = fmt.Fprintln(out, "ok")
	return nil
}

// agentErrorText extracts the meaningful error text from a non-2xx
// /status or /interrupt response body. main.ts's /interrupt 409 shape is
// {"ok":false,"error":"no active task"}; when the body parses as that
// shape, return just the error field so the CLI doesn't dump a raw JSON
// envelope at the user (mirrors cmd_browser.go's restartErrorText).
// Anything else (e.g. a plain-text http.Error body) falls back to the
// trimmed raw body.
func agentErrorText(body []byte) string {
	var parsed struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Error != "" {
		return parsed.Error
	}
	return strings.TrimSpace(string(body))
}
