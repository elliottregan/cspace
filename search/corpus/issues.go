package corpus

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os/exec"
	"strings"
)

// ghIssue holds the extracted data for a single GitHub issue or PR.
type ghIssue struct {
	Number    int      `json:"number"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	State     string   `json:"state"`
	Author    string   `json:"user"`
	Labels    []string `json:"labels"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
	HTMLURL   string   `json:"html_url"`
	PRURL     string   `json:"pull_request"`
}

// ghComment holds a single issue comment.
type ghComment struct {
	Author    string `json:"user"`
	CreatedAt string `json:"created_at"`
	Body      string `json:"body"`
}

// ghFetcher abstracts the GitHub API calls so tests can inject a fake.
type ghFetcher interface {
	ListIssues(owner, repo string, limit int) ([]ghIssue, error)
	ListComments(owner, repo string, number int) ([]ghComment, error)
}

// ghCLIFetcher is the production implementation that shells out to `gh api`.
type ghCLIFetcher struct{}

func (f *ghCLIFetcher) ListIssues(owner, repo string, limit int) ([]ghIssue, error) {
	var all []ghIssue
	perPage := 100
	for page := 1; len(all) < limit; page++ {
		jqExpr := `.[] | {number, title, body, state, user: .user.login, labels: [.labels[].name], created_at, updated_at, html_url, pull_request: (.pull_request.html_url // null)}`
		endpoint := fmt.Sprintf("repos/%s/%s/issues?state=all&sort=updated&direction=desc&per_page=%d&page=%d", url.PathEscape(owner), url.PathEscape(repo), perPage, page)
		out, err := ghAPI(endpoint, jqExpr)
		if err != nil {
			return nil, fmt.Errorf("gh api issues page %d: %w", page, err)
		}
		out = strings.TrimSpace(out)
		if out == "" {
			break
		}
		// gh --jq emits one JSON object per line (NDJSON).
		var batch []ghIssue
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var issue ghIssue
			if err := json.Unmarshal([]byte(line), &issue); err != nil {
				return nil, fmt.Errorf("parse issue JSON: %w\nline: %s", err, line)
			}
			batch = append(batch, issue)
		}
		if len(batch) == 0 {
			break
		}
		all = append(all, batch...)
		if len(batch) < perPage {
			break // last page
		}
	}
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

func (f *ghCLIFetcher) ListComments(owner, repo string, number int) ([]ghComment, error) {
	jqExpr := `.[] | {user: .user.login, created_at, body}`
	endpoint := fmt.Sprintf("repos/%s/%s/issues/%d/comments?per_page=100", url.PathEscape(owner), url.PathEscape(repo), number)
	out, err := ghAPI(endpoint, jqExpr)
	if err != nil {
		return nil, fmt.Errorf("gh api comments for #%d: %w", number, err)
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	var comments []ghComment
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var c ghComment
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, fmt.Errorf("parse comment JSON: %w", err)
		}
		comments = append(comments, c)
	}
	return comments, nil
}

// ghAPI executes `gh api <endpoint> --jq <jq>` and returns stdout.
func ghAPI(endpoint, jqExpr string) (string, error) {
	cmd := exec.Command("gh", "api", endpoint, "--jq", jqExpr)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, errBuf.String())
	}
	return out.String(), nil
}

// parseOwnerRepo extracts (owner, repo) from a git remote URL.
// Supports both HTTPS and SSH forms:
//
//	https://github.com/foo/bar.git -> (foo, bar)
//	git@github.com:foo/bar.git    -> (foo, bar)
func parseOwnerRepo(remoteURL string) (string, string, error) {
	u := strings.TrimSpace(remoteURL)
	u = strings.TrimSuffix(u, ".git")

	// SSH: git@github.com:owner/repo
	if strings.Contains(u, ":") && strings.HasPrefix(u, "git@") {
		parts := strings.SplitN(u, ":", 2)
		if len(parts) != 2 {
			return "", "", fmt.Errorf("cannot parse SSH remote: %s", remoteURL)
		}
		ownerRepo := strings.Split(parts[1], "/")
		if len(ownerRepo) != 2 {
			return "", "", fmt.Errorf("cannot parse owner/repo from SSH remote: %s", remoteURL)
		}
		return ownerRepo[0], ownerRepo[1], nil
	}

	// HTTPS: https://github.com/owner/repo
	if strings.Contains(u, "://") {
		parts := strings.Split(u, "/")
		if len(parts) < 5 {
			return "", "", fmt.Errorf("cannot parse HTTPS remote: %s", remoteURL)
		}
		return parts[len(parts)-2], parts[len(parts)-1], nil
	}

	return "", "", fmt.Errorf("unrecognized remote URL format: %s", remoteURL)
}

// getRemoteURL reads the origin remote URL from a git repo.
func getRemoteURL(repoPath string) (string, error) {
	out, err := gitCmd(repoPath, "config", "--get", "remote.origin.url")
	if err != nil {
		return "", fmt.Errorf("git config remote.origin.url: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// formatIssueEmbedText produces the text to embed for an issue, following the
// format specified in the corpus design:
//
//	Issue #{number}: {title}
//	State: {state}  Author: {author}  Labels: {labels}
//
//	{body}
//
//	--- Comment by {author}, {created_at} ---
//	{body}
//
// Truncated to maxEmbedChars.
func formatIssueEmbedText(issue ghIssue, comments []ghComment) string {
	var b strings.Builder

	// Header
	fmt.Fprintf(&b, "Issue #%d: %s\n", issue.Number, issue.Title)
	fmt.Fprintf(&b, "State: %s  Author: %s", issue.State, issue.Author)
	if len(issue.Labels) > 0 {
		fmt.Fprintf(&b, "  Labels: %s", strings.Join(issue.Labels, ", "))
	}
	b.WriteString("\n")

	// Body
	if issue.Body != "" {
		b.WriteString("\n")
		b.WriteString(issue.Body)
		b.WriteString("\n")
	}

	// Comments
	for _, c := range comments {
		fmt.Fprintf(&b, "\n--- Comment by %s, %s ---\n", c.Author, c.CreatedAt)
		b.WriteString(c.Body)
		b.WriteString("\n")
	}

	text := b.String()
	if len(text) > maxEmbedChars {
		text = text[:maxEmbedChars]
	}
	return text
}

// IssuesCorpus indexes GitHub issues and PRs for the current repo.
type IssuesCorpus struct {
	// Limit caps the number of issues fetched; 0 means use a default.
	Limit int

	// fetcher is the GitHub API abstraction. Nil means use the real gh CLI.
	fetcher ghFetcher

	// remoteURL overrides the git remote URL for testing. Empty means read
	// from git config.
	remoteURL string
}

// ID returns the stable corpus identifier.
func (c *IssuesCorpus) ID() string { return "issues" }

// Collection returns the Qdrant collection name for this corpus + project.
func (c *IssuesCorpus) Collection(projectRoot string) string {
	return "issues-" + ProjectHash(projectRoot)
}

// Enumerate emits Records for each issue/PR. The channel is closed when
// enumeration completes. Errors are reported via the errs channel.
func (c *IssuesCorpus) Enumerate(projectRoot string) (<-chan Record, <-chan error) {
	out := make(chan Record)
	errs := make(chan error, 4)
	go func() {
		defer close(out)
		defer close(errs)

		// Resolve owner/repo.
		remoteURL := c.remoteURL
		if remoteURL == "" {
			var err error
			remoteURL, err = getRemoteURL(projectRoot)
			if err != nil {
				errs <- err
				return
			}
		}
		owner, repo, err := parseOwnerRepo(remoteURL)
		if err != nil {
			errs <- err
			return
		}

		limit := c.Limit
		if limit <= 0 {
			limit = 500
		}

		fetcher := c.fetcher
		if fetcher == nil {
			fetcher = &ghCLIFetcher{}
		}

		issues, err := fetcher.ListIssues(owner, repo, limit)
		if err != nil {
			errs <- err
			return
		}

		for _, issue := range issues {
			// Fetch comments; log and continue on failure.
			comments, err := fetcher.ListComments(owner, repo, issue.Number)
			if err != nil {
				log.Printf("warn: failed to fetch comments for #%d: %v", issue.Number, err)
				comments = nil
			}

			embedText := formatIssueEmbedText(issue, comments)
			hash := sha256.Sum256([]byte(embedText))

			isPR := issue.PRURL != ""

			out <- Record{
				Path:        fmt.Sprintf("issue#%d", issue.Number),
				Kind:        "issue",
				ContentHash: fmt.Sprintf("%x", hash),
				EmbedText:   embedText,
				Extra: map[string]any{
					"number":     issue.Number,
					"title":      issue.Title,
					"state":      issue.State,
					"author":     issue.Author,
					"labels":     issue.Labels,
					"created_at": issue.CreatedAt,
					"updated_at": issue.UpdatedAt,
					"url":        issue.HTMLURL,
					"is_pr":      isPR,
				},
			}
		}
	}()
	return out, errs
}
