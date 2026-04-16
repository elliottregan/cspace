package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/elliottregan/cspace/internal/diagnostics"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func newDiagnosticsServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diagnostics-server",
		Short: "Run the real-time agent diagnostics server",
		Long: `Start a server that tails agent event logs, probes supervisor sockets,
and exposes real-time agent state over WebSocket (for dashboards) and
optionally MCP stdio (for coordinator agents).

The WebSocket endpoint is at ws://<host>:<port>/ws. Connect from a browser,
TUI, or any WebSocket client to receive live agent events.

Typically launched automatically by cspace or via .mcp.json, not invoked
directly by humans.`,
		GroupID: "other",
		RunE:    runDiagnosticsServer,
	}
	cmd.Flags().String("event-log-dir", "/logs/events", "Root directory for agent NDJSON event logs")
	cmd.Flags().String("msg-dir", "/logs/messages", "Root directory for supervisor sockets")
	cmd.Flags().Int("port", 8384, "HTTP/WebSocket listen port")
	cmd.Flags().Bool("mcp", false, "Also run MCP stdio server (for coordinator agents)")
	return cmd
}

func runDiagnosticsServer(cmd *cobra.Command, _ []string) error {
	eventLogDir, _ := cmd.Flags().GetString("event-log-dir")
	msgDir, _ := cmd.Flags().GetString("msg-dir")
	port, _ := cmd.Flags().GetInt("port")
	mcpMode, _ := cmd.Flags().GetBool("mcp")

	hub := diagnostics.NewHub(diagnostics.HubConfig{})

	// Backfill from existing event logs.
	tailer := diagnostics.NewTailer(hub, eventLogDir, 1*time.Second)
	tailer.LoadExisting()
	go tailer.Run()

	// Start socket prober.
	prober := diagnostics.NewProber(hub, msgDir, 5*time.Second, 2*time.Second)
	go prober.Run()

	// HTTP + WebSocket server.
	mux := http.NewServeMux()
	mux.Handle("/ws", diagnostics.NewWSHandler(hub, msgDir))
	mux.HandleFunc("/agents", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		agents := hub.Agents()
		if err := json.NewEncoder(w).Encode(agents); err != nil {
			log.Printf("[diagnostics] /agents encode: %v", err)
		}
	})
	mux.HandleFunc("/agents/", func(w http.ResponseWriter, r *http.Request) {
		instance := r.URL.Path[len("/agents/"):]
		if instance == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		agent := hub.Agent(instance)
		if agent == nil {
			http.NotFound(w, r)
			return
		}
		if err := json.NewEncoder(w).Encode(agent); err != nil {
			log.Printf("[diagnostics] /agents/ encode: %v", err)
		}
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	addr := fmt.Sprintf(":%d", port)
	httpServer := &http.Server{Addr: addr, Handler: mux}

	// If --mcp is set, run both MCP stdio and HTTP concurrently.
	// MCP blocks on stdin; HTTP blocks on ListenAndServe.
	if mcpMode {
		go func() {
			log.Printf("[diagnostics] HTTP/WS listening on %s", addr)
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("[diagnostics] HTTP server error: %v", err)
			}
		}()

		mcpServer := mcp.NewServer(&mcp.Implementation{
			Name:    "cspace-diagnostics",
			Version: Version,
		}, nil)
		registerDiagnosticsTools(mcpServer, hub)

		return mcpServer.Run(context.Background(), &mcp.StdioTransport{})
	}

	// HTTP-only mode.
	log.Printf("[diagnostics] HTTP/WS listening on %s", addr)

	// Graceful shutdown on SIGTERM/SIGINT.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		log.Println("[diagnostics] shutting down...")
		tailer.Stop()
		prober.Stop()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
	}()

	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// --- MCP tool registration ---

type agentHealthArgs struct {
	Instance string `json:"instance" jsonschema:"description=The agent instance name"`
}

type agentRecentArgs struct {
	Instance string `json:"instance" jsonschema:"description=The agent instance name"`
	Count    int    `json:"count,omitempty" jsonschema:"description=Number of events to return (max 50),minimum=1,maximum=50"`
}

type agentStreamArgs struct {
	Instance string `json:"instance" jsonschema:"description=Agent instance name"`
	Limit    int    `json:"limit,omitempty" jsonschema:"description=Max envelopes to return (default 100),minimum=1,maximum=500"`
	Since    string `json:"since,omitempty" jsonschema:"description=ISO timestamp — return only events after this"`
}

func registerDiagnosticsTools(server *mcp.Server, hub *diagnostics.Hub) {
	mcp.AddTool[agentHealthArgs, any](server, &mcp.Tool{
		Name:        "agent_health",
		Description: "Check an agent's liveness, last activity, and whether it is stuck mid-tool-call.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in agentHealthArgs) (*mcp.CallToolResult, any, error) {
		snap := hub.Agent(in.Instance)
		if snap == nil {
			return textResult(fmt.Sprintf("No state found for instance %q. The agent may not have started yet.", in.Instance)), nil, nil
		}
		data, _ := json.MarshalIndent(snap, "", "  ")
		return textResult(string(data)), nil, nil
	})

	mcp.AddTool[agentRecentArgs, any](server, &mcp.Tool{
		Name:        "agent_recent_activity",
		Description: "Return the last N event-log envelopes from an agent's session.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in agentRecentArgs) (*mcp.CallToolResult, any, error) {
		count := in.Count
		if count <= 0 {
			count = 10
		}
		if count > 50 {
			count = 50
		}
		events := hub.RecentEvents(in.Instance, count)
		if events == nil {
			return textResult(fmt.Sprintf("No events found for instance %q.", in.Instance)), nil, nil
		}
		data, _ := json.MarshalIndent(events, "", "  ")
		return textResult(string(data)), nil, nil
	})

	mcp.AddTool[agentStreamArgs, any](server, &mcp.Tool{
		Name:        "read_agent_stream",
		Description: "Read the event stream for an agent instance. Returns recent events from the hub's in-memory buffer. Use `since` for incremental polling.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in agentStreamArgs) (*mcp.CallToolResult, any, error) {
		limit := in.Limit
		if limit <= 0 {
			limit = 100
		}
		if limit > 500 {
			limit = 500
		}
		events := hub.RecentEvents(in.Instance, limit)
		if events == nil {
			return textResult(fmt.Sprintf("No events found for instance %q.", in.Instance)), nil, nil
		}

		// Apply since filter if provided.
		if in.Since != "" {
			filtered := make([]diagnostics.Envelope, 0, len(events))
			for _, e := range events {
				if e.Ts > in.Since {
					filtered = append(filtered, e)
				}
			}
			events = filtered
		}

		result := map[string]interface{}{
			"envelopes": events,
			"returned":  len(events),
		}
		if len(events) > 0 {
			result["last_ts"] = events[len(events)-1].Ts
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		return textResult(string(data)), nil, nil
	})
}
