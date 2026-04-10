package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

func newSyncContextCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync-context",
		Short: "Generate milestone context doc from GitHub",
		Long: `Query the GitHub API for the latest open milestone, its issues, and
dependency relationships, then generate a markdown context document.

By default, writes to .cspace/context.md. Use --stdout to print to
stdout instead (useful for prompt injection).`,
		GroupID: "other",
		RunE:    runSyncContext,
	}

	cmd.Flags().Bool("stdout", false, "Print to stdout instead of writing to file")

	return cmd
}

// milestoneData holds parsed milestone information from the GitHub API.
type milestoneData struct {
	Title       string `json:"title"`
	Number      int    `json:"number"`
	Description string `json:"description"`
}

// issueData holds parsed issue information from the GitHub API.
type issueData struct {
	Number      int              `json:"number"`
	Title       string           `json:"title"`
	State       string           `json:"state"`
	Assignee    *issueAssignee   `json:"assignee"`
	Labels      []issueLabel     `json:"labels"`
	Body        string           `json:"body"`
	PullRequest *json.RawMessage `json:"pull_request"`
}

type issueAssignee struct {
	Login string `json:"login"`
}

type issueLabel struct {
	Name string `json:"name"`
}

// parsedIssue holds a processed issue with dependency information.
type parsedIssue struct {
	Number    int
	Title     string
	State     string
	Assignee  string
	Labels    []string
	BlockedBy []int
	Blocks    []int
}

var (
	blockedByRe = regexp.MustCompile(`(?i)^\s*blocked[- ]by:`)
	blocksRe    = regexp.MustCompile(`(?i)^\s*blocks:`)
	issueRefRe  = regexp.MustCompile(`#(\d+)`)
)

func runSyncContext(cmd *cobra.Command, args []string) error {
	stdoutMode, _ := cmd.Flags().GetBool("stdout")

	// Verify gh CLI is available
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("gh CLI is required for sync-context (https://cli.github.com)")
	}

	// Get repository name
	repo := cfg.Project.Repo
	if repo == "" {
		// Try to detect from gh
		out, err := exec.Command("gh", "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner").Output()
		if err != nil {
			return fmt.Errorf("could not determine repository; set project.repo in .cspace.json")
		}
		repo = strings.TrimSpace(string(out))
	}
	if repo == "" {
		return fmt.Errorf("could not determine repository; set project.repo in .cspace.json")
	}

	// Fetch latest open milestone
	milestoneJSON, err := ghAPI(fmt.Sprintf("repos/%s/milestones?state=open&sort=created&direction=desc&per_page=1", repo))
	if err != nil {
		return fmt.Errorf("fetching milestones: %w", err)
	}

	var milestones []milestoneData
	if err := json.Unmarshal(milestoneJSON, &milestones); err != nil {
		return fmt.Errorf("parsing milestones: %w", err)
	}
	if len(milestones) == 0 {
		fmt.Fprintln(os.Stderr, "No open milestones found.")
		return nil
	}

	ms := milestones[0]

	// Parse Goals and Architectural Principles from description
	goals := extractSection(ms.Description, "## Goals")
	principles := extractSection(ms.Description, "## Architectural Principles")

	// Fetch all issues in milestone
	issuesJSON, err := ghAPI(fmt.Sprintf("repos/%s/issues?milestone=%d&state=all&per_page=100", repo, ms.Number))
	if err != nil {
		return fmt.Errorf("fetching issues: %w", err)
	}

	var rawIssues []issueData
	if err := json.Unmarshal(issuesJSON, &rawIssues); err != nil {
		return fmt.Errorf("parsing issues: %w", err)
	}

	// Filter out pull requests and parse dependencies
	var issues []parsedIssue
	for _, ri := range rawIssues {
		if ri.PullRequest != nil {
			continue
		}

		pi := parsedIssue{
			Number: ri.Number,
			Title:  ri.Title,
			State:  ri.State,
		}

		if ri.Assignee != nil {
			pi.Assignee = ri.Assignee.Login
		}

		for _, l := range ri.Labels {
			pi.Labels = append(pi.Labels, l.Name)
		}

		// Parse blocked-by and blocks from issue body
		if ri.Body != "" {
			for _, line := range strings.Split(ri.Body, "\n") {
				if blockedByRe.MatchString(line) {
					pi.BlockedBy = append(pi.BlockedBy, extractIssueRefs(line)...)
				}
				if blocksRe.MatchString(line) {
					pi.Blocks = append(pi.Blocks, extractIssueRefs(line)...)
				}
			}
		}

		issues = append(issues, pi)
	}

	if len(issues) == 0 {
		fmt.Fprintln(os.Stderr, "No issues found in milestone.")
		return nil
	}

	// Generate the markdown document
	doc := generateContextDoc(ms, goals, principles, issues)

	if stdoutMode {
		fmt.Print(doc)
	} else {
		outPath := filepath.Join(cfg.ProjectRoot, ".cspace", "context.md")
		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			return fmt.Errorf("creating output directory: %w", err)
		}
		if err := os.WriteFile(outPath, []byte(doc), 0644); err != nil {
			return fmt.Errorf("writing context file: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Wrote %s\n", outPath)
	}

	return nil
}

// ghAPI calls the GitHub API via the gh CLI and returns the raw JSON response.
func ghAPI(endpoint string) ([]byte, error) {
	cmd := exec.Command("gh", "api", endpoint)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh api %s: %w", endpoint, err)
	}
	return out, nil
}

// extractSection extracts the content of a markdown section from a description.
// It looks for a section starting with the given header and ends at the next
// ## heading or end of string.
func extractSection(description, header string) string {
	if description == "" {
		return ""
	}

	lines := strings.Split(description, "\n")
	var result []string
	inSection := false

	for _, line := range lines {
		if line == header || strings.HasPrefix(line, header+"\n") || strings.HasPrefix(line, header+"\r") {
			inSection = true
			continue
		}
		if inSection && strings.HasPrefix(line, "## ") {
			break
		}
		if inSection {
			result = append(result, line)
		}
	}

	return strings.TrimSpace(strings.Join(result, "\n"))
}

// extractIssueRefs extracts issue numbers from #N references in a line.
func extractIssueRefs(line string) []int {
	matches := issueRefRe.FindAllStringSubmatch(line, -1)
	var refs []int
	for _, m := range matches {
		if len(m) >= 2 {
			var n int
			fmt.Sscanf(m[1], "%d", &n)
			refs = append(refs, n)
		}
	}
	return refs
}

// generateContextDoc builds the markdown context document from parsed data.
func generateContextDoc(ms milestoneData, goals, principles string, issues []parsedIssue) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Active Milestone: %s\n\n", ms.Title)

	if goals != "" {
		fmt.Fprintf(&b, "## Goals\n%s\n\n", goals)
	}
	if principles != "" {
		fmt.Fprintf(&b, "## Architectural Principles\n%s\n\n", principles)
	}

	fmt.Fprintln(&b, "## Dependency Graph")
	fmt.Fprintln(&b)

	for _, issue := range issues {
		line := fmt.Sprintf("#%d %s", issue.Number, issue.Title)
		if len(issue.Labels) > 0 {
			line += fmt.Sprintf(" [%s]", strings.Join(issue.Labels, ", "))
		}
		fmt.Fprintln(&b, line)

		if len(issue.BlockedBy) > 0 && len(issue.Blocks) > 0 {
			fmt.Fprintf(&b, "  blocked-by: %s\n", formatIssueRefs(issue.BlockedBy))
			fmt.Fprintf(&b, "  blocks: %s\n", formatIssueRefs(issue.Blocks))
		} else if len(issue.BlockedBy) > 0 {
			fmt.Fprintf(&b, "  blocked-by: %s\n", formatIssueRefs(issue.BlockedBy))
		} else if len(issue.Blocks) > 0 {
			fmt.Fprintf(&b, "  blocks: %s\n", formatIssueRefs(issue.Blocks))
		} else {
			fmt.Fprintln(&b, "  (independent)")
		}
		fmt.Fprintln(&b)
	}

	fmt.Fprintln(&b, "## Issue Status")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "| Issue | Title | Status | Assignee | Labels |")
	fmt.Fprintln(&b, "|-------|-------|--------|----------|--------|")

	for _, issue := range issues {
		assignee := "\u2014"
		if issue.Assignee != "" {
			assignee = issue.Assignee
		}
		labels := "\u2014"
		if len(issue.Labels) > 0 {
			labels = strings.Join(issue.Labels, ", ")
		}
		fmt.Fprintf(&b, "| #%d | %s | %s | %s | %s |\n",
			issue.Number, issue.Title, issue.State, assignee, labels)
	}
	fmt.Fprintln(&b)

	return b.String()
}

// formatIssueRefs formats a list of issue numbers as "#N, #M, ..." string.
func formatIssueRefs(refs []int) string {
	parts := make([]string, len(refs))
	for i, r := range refs {
		parts[i] = fmt.Sprintf("#%d", r)
	}
	return strings.Join(parts, ", ")
}
