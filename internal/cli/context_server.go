package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/elliottregan/cspace/internal/contextstore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func newContextServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context-server",
		Short: "Run the docs/context MCP server over stdio",
		Long: `Expose docs/context/ (direction, principles, roadmap, decisions, discoveries)
as an MCP server over stdio. Typically invoked by Claude Code via .mcp.json
or the container's Claude MCP config, not by humans directly.`,
		GroupID: "other",
		RunE:    runContextServer,
	}
	cmd.Flags().String("root", "", "Project root (default: current working directory)")
	return cmd
}

func runContextServer(cmd *cobra.Command, args []string) error {
	root, _ := cmd.Flags().GetString("root")
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getwd: %w", err)
		}
		root = cwd
	}

	store := &contextstore.Store{Root: root}
	server := mcp.NewServer(&mcp.Implementation{Name: "cspace-context", Version: Version}, nil)
	registerContextTools(server, store)

	return server.Run(context.Background(), &mcp.StdioTransport{})
}

// registerContextTools registers all five context tools on server.
func registerContextTools(server *mcp.Server, store *contextstore.Store) {
	mcp.AddTool[readContextArgs, readContextOut](server, &mcp.Tool{
		Name:        "read_context",
		Description: "Read the project brain: direction, principles, roadmap, and recent decisions/discoveries. Returns everything by default; filter with `sections`.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in readContextArgs) (*mcp.CallToolResult, readContextOut, error) {
		out, err := handleReadContext(store, in)
		if err != nil {
			return nil, readContextOut{}, err
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: out.Render()}},
		}, out, nil
	})

	mcp.AddTool[logDecisionArgs, logEntryOut](server, &mcp.Tool{
		Name:        "log_decision",
		Description: "Record a significant architectural or design decision. Only log things that would save a future session time.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in logDecisionArgs) (*mcp.CallToolResult, logEntryOut, error) {
		path, err := store.LogDecision(contextstore.LogDecisionInput{
			Title: in.Title, Context: in.Context, Alternatives: in.Alternatives,
			Decision: in.Decision, Consequences: in.Consequences,
		})
		if err != nil {
			return nil, logEntryOut{}, err
		}
		return textResult("logged decision: " + path), logEntryOut{Path: path}, nil
	})

	mcp.AddTool[logDiscoveryArgs, logEntryOut](server, &mcp.Tool{
		Name:        "log_discovery",
		Description: "Record something non-obvious learned about the code or infrastructure.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in logDiscoveryArgs) (*mcp.CallToolResult, logEntryOut, error) {
		path, err := store.LogDiscovery(contextstore.LogDiscoveryInput{
			Title: in.Title, Finding: in.Finding, Impact: in.Impact,
		})
		if err != nil {
			return nil, logEntryOut{}, err
		}
		return textResult("logged discovery: " + path), logEntryOut{Path: path}, nil
	})

	mcp.AddTool[listEntriesArgs, listEntriesOut](server, &mcp.Tool{
		Name:        "list_entries",
		Description: "List decisions and/or discoveries with optional date range. Returns metadata, no bodies.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listEntriesArgs) (*mcp.CallToolResult, listEntriesOut, error) {
		opts, err := in.toOptions()
		if err != nil {
			return nil, listEntriesOut{}, err
		}
		summaries, err := store.ListEntries(opts)
		if err != nil {
			return nil, listEntriesOut{}, err
		}
		out := listEntriesOut{Entries: make([]entrySummaryDTO, 0, len(summaries))}
		for _, s := range summaries {
			out.Entries = append(out.Entries, entrySummaryDTO{
				Kind: string(s.Kind), Date: s.Date.Format("2006-01-02"),
				Slug: s.Slug, Title: s.Title, Path: s.Path,
			})
		}
		return textResult(fmt.Sprintf("%d entries", len(out.Entries))), out, nil
	})

	mcp.AddTool[removeEntryArgs, any](server, &mcp.Tool{
		Name:        "remove_entry",
		Description: "Delete a decision or discovery by slug. For human curation passes.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in removeEntryArgs) (*mcp.CallToolResult, any, error) {
		kind := contextstore.Kind(in.Kind)
		if kind != contextstore.KindDecision && kind != contextstore.KindDiscovery {
			return nil, nil, fmt.Errorf("kind must be 'decision' or 'discovery'")
		}
		if err := store.RemoveEntry(kind, in.Slug); err != nil {
			return nil, nil, err
		}
		return textResult("removed " + in.Kind + "/" + in.Slug), nil, nil
	})
}

// --- Tool input / output types ---

type readContextArgs struct {
	Sections []string `json:"sections,omitempty" jsonschema:"subset of: direction, principles, roadmap, decisions, discoveries. Omit for all."`
	Since    string   `json:"since,omitempty" jsonschema:"ISO date (YYYY-MM-DD). Filters decisions and discoveries."`
	Limit    int      `json:"limit,omitempty" jsonschema:"max entries per kind. Default 20."`
}

type readContextOut struct {
	Direction   string     `json:"direction,omitempty"`
	Principles  string     `json:"principles,omitempty"`
	Roadmap     string     `json:"roadmap,omitempty"`
	Decisions   []entryDTO `json:"decisions,omitempty"`
	Discoveries []entryDTO `json:"discoveries,omitempty"`
}

type entryDTO struct {
	Date     string            `json:"date"`
	Slug     string            `json:"slug"`
	Title    string            `json:"title"`
	Sections map[string]string `json:"sections"`
}

func (o readContextOut) Render() string {
	var b strings.Builder
	if o.Direction != "" {
		b.WriteString("# direction.md\n\n")
		b.WriteString(o.Direction)
		b.WriteString("\n\n")
	}
	if o.Principles != "" {
		b.WriteString("# principles.md\n\n")
		b.WriteString(o.Principles)
		b.WriteString("\n\n")
	}
	if o.Roadmap != "" {
		b.WriteString("# roadmap.md\n\n")
		b.WriteString(o.Roadmap)
		b.WriteString("\n\n")
	}
	if len(o.Decisions) > 0 {
		b.WriteString("# decisions\n\n")
		for _, e := range o.Decisions {
			fmt.Fprintf(&b, "## %s (%s)\n\n", e.Title, e.Date)
			for _, name := range []string{"Context", "Alternatives", "Decision", "Consequences"} {
				if v := e.Sections[name]; v != "" {
					fmt.Fprintf(&b, "### %s\n%s\n\n", name, v)
				}
			}
		}
	}
	if len(o.Discoveries) > 0 {
		b.WriteString("# discoveries\n\n")
		for _, e := range o.Discoveries {
			fmt.Fprintf(&b, "## %s (%s)\n\n", e.Title, e.Date)
			for _, name := range []string{"Finding", "Impact"} {
				if v := e.Sections[name]; v != "" {
					fmt.Fprintf(&b, "### %s\n%s\n\n", name, v)
				}
			}
		}
	}
	return b.String()
}

type logDecisionArgs struct {
	Title        string `json:"title" jsonschema:"short description; becomes the slug"`
	Context      string `json:"context" jsonschema:"why this decision came up"`
	Alternatives string `json:"alternatives" jsonschema:"what else was considered"`
	Decision     string `json:"decision" jsonschema:"what was chosen"`
	Consequences string `json:"consequences" jsonschema:"what follows from this choice"`
}

type logDiscoveryArgs struct {
	Title   string `json:"title" jsonschema:"short description; becomes the slug"`
	Finding string `json:"finding" jsonschema:"what was found"`
	Impact  string `json:"impact" jsonschema:"why it matters for future work"`
}

type logEntryOut struct {
	Path string `json:"path"`
}

type listEntriesArgs struct {
	Kind  string `json:"kind,omitempty" jsonschema:"decisions | discoveries | both. Default: both."`
	Since string `json:"since,omitempty" jsonschema:"ISO date inclusive lower bound"`
	Until string `json:"until,omitempty" jsonschema:"ISO date inclusive upper bound"`
}

type listEntriesOut struct {
	Entries []entrySummaryDTO `json:"entries"`
}

type entrySummaryDTO struct {
	Kind  string `json:"kind"`
	Date  string `json:"date"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
	Path  string `json:"path"`
}

func (a listEntriesArgs) toOptions() (contextstore.ListOptions, error) {
	var opts contextstore.ListOptions
	switch a.Kind {
	case "", "both":
	case "decisions":
		opts.Kind = contextstore.KindDecision
	case "discoveries":
		opts.Kind = contextstore.KindDiscovery
	default:
		return opts, fmt.Errorf("kind must be decisions, discoveries, or both")
	}
	parseDate := func(s string) (*time.Time, error) {
		if s == "" {
			return nil, nil
		}
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return nil, err
		}
		return &t, nil
	}
	var err error
	if opts.Since, err = parseDate(a.Since); err != nil {
		return opts, fmt.Errorf("since: %w", err)
	}
	if opts.Until, err = parseDate(a.Until); err != nil {
		return opts, fmt.Errorf("until: %w", err)
	}
	return opts, nil
}

type removeEntryArgs struct {
	Kind string `json:"kind" jsonschema:"decision | discovery"`
	Slug string `json:"slug" jsonschema:"entry slug (filename without .md)"`
}

// handleReadContext builds the readContextOut, honoring section filters and limits.
func handleReadContext(store *contextstore.Store, in readContextArgs) (readContextOut, error) {
	want := map[string]bool{}
	if len(in.Sections) == 0 {
		for _, s := range []string{"direction", "principles", "roadmap", "decisions", "discoveries"} {
			want[s] = true
		}
	} else {
		for _, s := range in.Sections {
			want[s] = true
		}
	}

	var out readContextOut
	var err error
	if want["direction"] {
		if out.Direction, err = store.ReadHuman("direction"); err != nil {
			return out, err
		}
	}
	if want["principles"] {
		if out.Principles, err = store.ReadHuman("principles"); err != nil {
			return out, err
		}
	}
	if want["roadmap"] {
		if out.Roadmap, err = store.ReadHuman("roadmap"); err != nil {
			return out, err
		}
	}

	limit := in.Limit
	if limit == 0 {
		limit = 20
	}
	var since *time.Time
	if in.Since != "" {
		t, err := time.Parse("2006-01-02", in.Since)
		if err != nil {
			return out, fmt.Errorf("since: %w", err)
		}
		since = &t
	}

	toDTO := func(e contextstore.Entry) entryDTO {
		return entryDTO{
			Date: e.Date.Format("2006-01-02"), Slug: e.Slug,
			Title: e.Title, Sections: e.Sections,
		}
	}

	if want["decisions"] {
		es, err := store.ReadEntries(contextstore.ListOptions{Kind: contextstore.KindDecision, Since: since, Limit: limit})
		if err != nil {
			return out, err
		}
		for _, e := range es {
			out.Decisions = append(out.Decisions, toDTO(e))
		}
	}
	if want["discoveries"] {
		es, err := store.ReadEntries(contextstore.ListOptions{Kind: contextstore.KindDiscovery, Since: since, Limit: limit})
		if err != nil {
			return out, err
		}
		for _, e := range es {
			out.Discoveries = append(out.Discoveries, toDTO(e))
		}
	}
	return out, nil
}

func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}
