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

	matches, _ := filepath.Glob(filepath.Join(root, ".cspace/context/decisions/*.md"))
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

	// log_finding (bug)
	logFind, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "log_finding",
		Arguments: map[string]any{
			"title":    "Signup button sometimes unresponsive",
			"category": "bug",
			"summary":  "Click does nothing; user stuck on step 2",
			"details":  "Repro via Playwright in 3/4 persona runs",
			"tags":     []string{"ux", "signup", "persona-eval"},
			"author":   "persona-coord",
		},
	})
	if err != nil {
		t.Fatalf("log_finding: %v", err)
	}
	findingMatches, _ := filepath.Glob(filepath.Join(root, ".cspace/context/findings/*.md"))
	if len(findingMatches) != 1 {
		t.Fatalf("want 1 finding file, got %d", len(findingMatches))
	}
	findingSlug := strings.TrimSuffix(filepath.Base(findingMatches[0]), ".md")
	// The create result should include the slug for the caller to pass back in.
	if !strings.Contains(toText(logFind), findingSlug) {
		t.Errorf("log_finding text result missing slug:\n%s", toText(logFind))
	}

	// append_to_finding — add two updates with a status transition on the second.
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "append_to_finding",
		Arguments: map[string]any{
			"slug":   findingSlug,
			"note":   "bisected to the onSubmit handler",
			"author": "implementer-1",
		},
	}); err != nil {
		t.Fatalf("append_to_finding #1: %v", err)
	}
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "append_to_finding",
		Arguments: map[string]any{
			"slug":   findingSlug,
			"note":   "patch at owner/repo#42",
			"status": "acknowledged",
			"author": "coord",
		},
	}); err != nil {
		t.Fatalf("append_to_finding #2: %v", err)
	}
	findingBody, _ := os.ReadFile(findingMatches[0])
	for _, want := range []string{
		"status: acknowledged",
		"bisected to the onSubmit handler",
		"patch at owner/repo#42",
	} {
		if !strings.Contains(string(findingBody), want) {
			t.Errorf("finding file missing %q:\n%s", want, findingBody)
		}
	}

	// list_findings — should return the one we created.
	listFind, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_findings",
		Arguments: map[string]any{"status": []string{"acknowledged"}},
	})
	if err != nil {
		t.Fatalf("list_findings: %v", err)
	}
	if !strings.Contains(toText(listFind), "1 findings") {
		t.Errorf("list_findings should report 1 finding:\n%s", toText(listFind))
	}

	// read_finding — full body including Updates section.
	readFind, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "read_finding",
		Arguments: map[string]any{"slug": findingSlug},
	})
	if err != nil {
		t.Fatalf("read_finding: %v", err)
	}
	if !strings.Contains(toText(readFind), "acknowledged") {
		t.Errorf("read_finding should report current status:\n%s", toText(readFind))
	}

	// read_context — findings section is present and shows the entry.
	readRes2, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "read_context", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("read_context (post-finding): %v", err)
	}
	text2 := toText(readRes2)
	for _, must := range []string{
		"# findings (open + acknowledged)",
		"Signup button sometimes unresponsive",
		"status: acknowledged",
		"Recent Updates",
	} {
		if !strings.Contains(text2, must) {
			t.Errorf("read_context (post-finding) missing %q:\n%s", must, text2)
		}
	}

	// remove_entry — works for findings too.
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "remove_entry",
		Arguments: map[string]any{"kind": "finding", "slug": findingSlug},
	}); err != nil {
		t.Fatalf("remove_entry (finding): %v", err)
	}
	if got, _ := filepath.Glob(filepath.Join(root, ".cspace/context/findings/*.md")); len(got) != 0 {
		// .lock file is OK to leave behind; only real .md should be gone.
		for _, g := range got {
			if strings.HasSuffix(g, ".md") {
				t.Errorf("finding not removed: %v", g)
			}
		}
	}

	// remove_entry (discovery) — last, because the discovery was created
	// earlier in the test flow.
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "remove_entry",
		Arguments: map[string]any{
			"kind": "discovery",
			"slug": strings.TrimSuffix(filepath.Base(mustGlobOne(t, root, "discoveries")), ".md"),
		},
	}); err != nil {
		t.Fatalf("remove_entry: %v", err)
	}
	if got, _ := filepath.Glob(filepath.Join(root, ".cspace/context/discoveries/*.md")); len(got) != 0 {
		t.Errorf("discovery not removed: %v", got)
	}
}

func TestTruncateUpdates(t *testing.T) {
	body := strings.Join([]string{
		"### 2026-04-13T00:00:00Z — @a — status: open",
		"first",
		"",
		"### 2026-04-14T00:00:00Z — @b — status: open",
		"second",
		"",
		"### 2026-04-15T00:00:00Z — @c — status: acknowledged",
		"third",
		"",
		"### 2026-04-16T00:00:00Z — @d — status: resolved",
		"fourth",
	}, "\n")

	got := truncateUpdates(body, 2)
	if !strings.Contains(got, "third") || !strings.Contains(got, "fourth") {
		t.Errorf("last 2 should contain 'third' and 'fourth':\n%s", got)
	}
	if strings.Contains(got, "first") || strings.Contains(got, "second") {
		t.Errorf("oldest 2 should have been dropped:\n%s", got)
	}

	// n larger than block count → return everything (as-is).
	all := truncateUpdates(body, 10)
	if !strings.Contains(all, "first") {
		t.Errorf("n=10 with 4 blocks should include 'first':\n%s", all)
	}

	// Empty body → empty string.
	if truncateUpdates("", 3) != "" {
		t.Error("empty body should return empty")
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
	got, err := filepath.Glob(filepath.Join(root, ".cspace/context", subdir, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 file in %s, got %d", subdir, len(got))
	}
	return got[0]
}
