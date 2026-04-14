package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elliottregan/cspace/internal/contextstore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestContextServerE2E(t *testing.T) {
	root := t.TempDir()
	store := &contextstore.Store{Root: root}

	server := mcp.NewServer(&mcp.Implementation{Name: "cspace-context-test"}, nil)
	registerContextTools(server, store)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client"}, nil)
	serverT, clientT := mcp.NewInMemoryTransports()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = server.Run(ctx, serverT) }()

	session, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer session.Close()

	// log_decision
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "log_decision",
		Arguments: map[string]any{
			"title":        "Use Go MCP SDK",
			"context":      "needed a server",
			"alternatives": "Node SDK",
			"decision":     "Go SDK",
			"consequences": "Go module dep",
		},
	}); err != nil {
		t.Fatalf("log_decision: %v", err)
	}

	matches, _ := filepath.Glob(filepath.Join(root, "docs/context/decisions/*.md"))
	if len(matches) != 1 {
		t.Fatalf("want 1 decision file, got %d", len(matches))
	}
	body, _ := os.ReadFile(matches[0])
	if !strings.Contains(string(body), "kind: decision") {
		t.Errorf("missing kind: %s", body)
	}

	// log_discovery
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "log_discovery",
		Arguments: map[string]any{
			"title": "Firewall", "finding": "blocked", "impact": "allowlist",
		},
	}); err != nil {
		t.Fatalf("log_discovery: %v", err)
	}

	// list_entries
	listRes, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "list_entries", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("list_entries: %v", err)
	}
	if len(listRes.Content) == 0 {
		t.Error("list_entries returned no content")
	}

	// read_context
	readRes, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "read_context", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("read_context: %v", err)
	}
	text := toText(readRes)
	for _, must := range []string{"# direction.md", "# decisions", "# discoveries", "Use Go MCP SDK", "Firewall"} {
		if !strings.Contains(text, must) {
			t.Errorf("read_context output missing %q:\n%s", must, text)
		}
	}

	// remove_entry
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "remove_entry",
		Arguments: map[string]any{
			"kind": "discovery",
			"slug": strings.TrimSuffix(filepath.Base(mustGlobOne(t, root, "discoveries")), ".md"),
		},
	}); err != nil {
		t.Fatalf("remove_entry: %v", err)
	}
	if got, _ := filepath.Glob(filepath.Join(root, "docs/context/discoveries/*.md")); len(got) != 0 {
		t.Errorf("discovery not removed: %v", got)
	}
}

func toText(r *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func mustGlobOne(t *testing.T, root, subdir string) string {
	t.Helper()
	got, err := filepath.Glob(filepath.Join(root, "docs/context", subdir, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 file in %s, got %d", subdir, len(got))
	}
	return got[0]
}
