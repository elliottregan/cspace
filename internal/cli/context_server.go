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
		Description: "Read the project brain: direction, principles, roadmap, and recent decisions/discoveries. Returns everything by default; filter with `sections`. On a fresh repo where the context files have not yet been seeded, human-owned sections return empty strings and agent-owned sections return empty arrays — read calls do not create files. The first `log_decision` or `log_discovery` call seeds the templates.",
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
		Description: "Delete a decision, discovery, or finding by slug. For human curation passes.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in removeEntryArgs) (*mcp.CallToolResult, any, error) {
		kind := contextstore.Kind(in.Kind)
		if kind != contextstore.KindDecision && kind != contextstore.KindDiscovery && kind != contextstore.KindFinding {
			return nil, nil, fmt.Errorf("kind must be 'decision', 'discovery', or 'finding'")
		}
		if err := store.RemoveEntry(kind, in.Slug); err != nil {
			return nil, nil, err
		}
		return textResult("removed " + in.Kind + "/" + in.Slug), nil, nil
	})

	mcp.AddTool[logFindingArgs, logFindingOut](server, &mcp.Tool{
		Name: "log_finding",
		Description: "Open a new finding. Findings are lifecycle-aware entries for bug reports, observations, and refactor proposals — things that need tracking and follow-up, unlike decisions/discoveries which are terminal learnings. `category` must be one of: bug, observation, refactor. `status` defaults to 'open' and is one of: open, acknowledged, resolved, wontfix. The Updates section is seeded with the opening subheading; later `append_to_finding` calls add more.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in logFindingArgs) (*mcp.CallToolResult, logFindingOut, error) {
		path, err := store.LogFinding(contextstore.LogFindingInput{
			Title: in.Title, Category: in.Category, Summary: in.Summary, Details: in.Details,
			Status: in.Status, Tags: in.Tags, Related: in.Related, Author: in.Author,
		})
		if err != nil {
			return nil, logFindingOut{}, err
		}
		slug := slugFromPath(path)
		return textResult("logged finding: " + path), logFindingOut{Path: path, Slug: slug}, nil
	})

	mcp.AddTool[appendFindingArgs, appendFindingOut](server, &mcp.Tool{
		Name: "append_to_finding",
		Description: "Append a timestamped update to an existing finding and optionally transition its status. Slug is required (list_findings first if you don't know it). Prior updates are preserved; this is append-only. Status must be one of: open, acknowledged, resolved, wontfix.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in appendFindingArgs) (*mcp.CallToolResult, appendFindingOut, error) {
		path, newStatus, err := store.AppendToFinding(contextstore.AppendFindingInput{
			Slug: in.Slug, Note: in.Note, Status: in.Status, Author: in.Author,
		})
		if err != nil {
			return nil, appendFindingOut{}, err
		}
		return textResult(fmt.Sprintf("appended to %s (status: %s)", in.Slug, newStatus)),
			appendFindingOut{Path: path, Status: newStatus}, nil
	})

	mcp.AddTool[listFindingsArgs, listEntriesOut](server, &mcp.Tool{
		Name:        "list_findings",
		Description: "List findings, optionally filtered by status, category, tags, or date range. Returns metadata only (no bodies). Use read_finding to fetch a single entry's full content.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listFindingsArgs) (*mcp.CallToolResult, listEntriesOut, error) {
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
		return textResult(fmt.Sprintf("%d findings", len(out.Entries))), out, nil
	})

	mcp.AddTool[readFindingArgs, findingDTO](server, &mcp.Tool{
		Name:        "read_finding",
		Description: "Read a single finding by slug, including its full Updates history. Returns { date, slug, title, status, category, tags, related, sections }.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in readFindingArgs) (*mcp.CallToolResult, findingDTO, error) {
		e, err := store.ReadFinding(in.Slug)
		if err != nil {
			return nil, findingDTO{}, err
		}
		dto := findingToDTO(e)
		return textResult(fmt.Sprintf("finding %s (status: %s)", e.Slug, e.Status)), dto, nil
	})
}

// slugFromPath extracts "2026-04-15-foo" from "/path/to/findings/2026-04-15-foo.md".
func slugFromPath(p string) string {
	base := p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		base = p[i+1:]
	}
	return strings.TrimSuffix(base, ".md")
}

func findingToDTO(e contextstore.Entry) findingDTO {
	return findingDTO{
		Date:     e.Date.Format("2006-01-02"),
		Slug:     e.Slug,
		Title:    e.Title,
		Status:   e.Status,
		Category: e.Category,
		Tags:     e.Tags,
		Related:  e.Related,
		Sections: e.Sections,
	}
}

// --- Tool input / output types ---

type readContextArgs struct {
	Sections []string `json:"sections,omitempty" jsonschema:"subset of: direction, principles, roadmap, decisions, discoveries, findings. Omit for all."`
	Since    string   `json:"since,omitempty" jsonschema:"ISO date (YYYY-MM-DD). Filters decisions, discoveries, and findings."`
	Limit    int      `json:"limit,omitempty" jsonschema:"max entries per kind. Default 20."`
}

type readContextOut struct {
	Direction   string       `json:"direction,omitempty"`
	Principles  string       `json:"principles,omitempty"`
	Roadmap     string       `json:"roadmap,omitempty"`
	Decisions   []entryDTO   `json:"decisions,omitempty"`
	Discoveries []entryDTO   `json:"discoveries,omitempty"`
	Findings    []findingDTO `json:"findings,omitempty"`
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
	if len(o.Findings) > 0 {
		b.WriteString("# findings (open + acknowledged)\n\n")
		for _, e := range o.Findings {
			meta := fmt.Sprintf("status: %s · category: %s", e.Status, e.Category)
			if len(e.Tags) > 0 {
				meta += " · tags: " + strings.Join(e.Tags, ", ")
			}
			fmt.Fprintf(&b, "## %s — %s (%s)\n\n", e.Title, e.Slug, e.Date)
			fmt.Fprintf(&b, "_%s_\n\n", meta)
			if v := e.Sections["Summary"]; v != "" {
				fmt.Fprintf(&b, "### Summary\n%s\n\n", v)
			}
			if v := truncateUpdates(e.Sections["Updates"], 3); v != "" {
				fmt.Fprintf(&b, "### Recent Updates\n%s\n\n", v)
			}
		}
	}
	return b.String()
}

// truncateUpdates returns the last n `### ` subheading blocks of an
// Updates body, preserving order. Keeps the brain digest terse even on
// long-lived findings that have accumulated many updates.
func truncateUpdates(body string, n int) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	// Split on the line-start "### " marker. Each chunk (except the
	// first, which is empty if the body starts with `### `) is one
	// update block including its heading. Use a newline-anchored split
	// so ### inside body content doesn't match.
	parts := strings.Split("\n"+body, "\n### ")
	if len(parts) <= 1 {
		return body
	}
	// parts[0] is pre-first-heading content (often empty); parts[1:] are
	// the update blocks (each missing their leading "### " prefix).
	blocks := parts[1:]
	if len(blocks) > n {
		blocks = blocks[len(blocks)-n:]
	}
	var out []string
	for _, b := range blocks {
		out = append(out, "### "+b)
	}
	return strings.Join(out, "\n\n")
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
	Kind string `json:"kind" jsonschema:"decision | discovery | finding"`
	Slug string `json:"slug" jsonschema:"on-disk filename, with or without .md (e.g. 2026-04-13-use-go-mcp-sdk); must contain only [a-z0-9-]"`
}

type logFindingArgs struct {
	Title    string   `json:"title" jsonschema:"short description; becomes the slug"`
	Category string   `json:"category" jsonschema:"one of: bug, observation, refactor"`
	Summary  string   `json:"summary" jsonschema:"one-paragraph problem statement"`
	Details  string   `json:"details,omitempty" jsonschema:"reproduction steps, evidence, impact across contexts"`
	Status   string   `json:"status,omitempty" jsonschema:"open | acknowledged | resolved | wontfix (default open)"`
	Tags     []string `json:"tags,omitempty" jsonschema:"free-form labels for filtering"`
	Related  []string `json:"related,omitempty" jsonschema:"cross-reference slugs (other findings) or URLs (PRs, issues)"`
	Author   string   `json:"author,omitempty" jsonschema:"byline for the opening Updates subheading (default: agent)"`
}

type logFindingOut struct {
	Path string `json:"path"`
	Slug string `json:"slug"`
}

type appendFindingArgs struct {
	Slug   string `json:"slug" jsonschema:"target finding slug (no .md); list_findings first if unknown"`
	Note   string `json:"note" jsonschema:"body of the new timestamped update"`
	Status string `json:"status,omitempty" jsonschema:"optional status transition (open | acknowledged | resolved | wontfix)"`
	Author string `json:"author,omitempty" jsonschema:"byline for this update (default: agent)"`
}

type appendFindingOut struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

type listFindingsArgs struct {
	Status   []string `json:"status,omitempty" jsonschema:"filter to these statuses (any of open, acknowledged, resolved, wontfix)"`
	Category []string `json:"category,omitempty" jsonschema:"filter to these categories (any of bug, observation, refactor)"`
	Tags     []string `json:"tags,omitempty" jsonschema:"match findings whose tags intersect this list"`
	Since    string   `json:"since,omitempty" jsonschema:"ISO date lower bound"`
	Until    string   `json:"until,omitempty" jsonschema:"ISO date upper bound"`
	Limit    int      `json:"limit,omitempty" jsonschema:"max entries (default 50)"`
}

func (a listFindingsArgs) toOptions() (contextstore.ListOptions, error) {
	opts := contextstore.ListOptions{
		Kind:     contextstore.KindFinding,
		Status:   a.Status,
		Category: a.Category,
		Tags:     a.Tags,
	}
	if a.Limit > 0 {
		opts.Limit = a.Limit
	} else {
		opts.Limit = 50
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

type readFindingArgs struct {
	Slug string `json:"slug" jsonschema:"finding slug (no .md); list_findings returns slugs"`
}

type findingDTO struct {
	Date     string            `json:"date"`
	Slug     string            `json:"slug"`
	Title    string            `json:"title"`
	Status   string            `json:"status"`
	Category string            `json:"category"`
	Tags     []string          `json:"tags,omitempty"`
	Related  []string          `json:"related,omitempty"`
	Sections map[string]string `json:"sections"`
}

// handleReadContext builds the readContextOut, honoring section filters and limits.
func handleReadContext(store *contextstore.Store, in readContextArgs) (readContextOut, error) {
	want := map[string]bool{}
	if len(in.Sections) == 0 {
		for _, s := range []string{"direction", "principles", "roadmap", "decisions", "discoveries", "findings"} {
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
	if want["findings"] {
		// read_context shows only actionable findings (open + acknowledged)
		// and caps at 10 regardless of overall Limit, so the brain digest
		// stays terse. Callers needing the full set call list_findings.
		findingLimit := limit
		if findingLimit > 10 {
			findingLimit = 10
		}
		es, err := store.ReadEntries(contextstore.ListOptions{
			Kind:   contextstore.KindFinding,
			Since:  since,
			Limit:  findingLimit,
			Status: []string{contextstore.FindingStatusOpen, contextstore.FindingStatusAcknowledged},
		})
		if err != nil {
			return out, err
		}
		for _, e := range es {
			out.Findings = append(out.Findings, findingToDTO(e))
		}
	}
	return out, nil
}

func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}
