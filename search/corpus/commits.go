package corpus

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// commitRecord holds the extracted data for a single commit.
// This is a package-private type used by CommitCorpus.
type commitRecord struct {
	Hash        string
	Date        time.Time
	Subject     string
	Body        string
	DiffSummary string
}

// maxEmbedChars is the character limit for embed text. The Jina retrieval model
// supports 8K tokens; 12000 chars ≈ 3000 tokens leaving headroom for the prefix.
const maxEmbedChars = 12000

// commitEmbedText returns the text to embed for a commit, truncated to fit the
// model's context.
func commitEmbedText(c commitRecord) string {
	parts := []string{c.Subject}
	if c.Body != "" {
		parts = append(parts, c.Body)
	}
	if c.DiffSummary != "" {
		parts = append(parts, "---", c.DiffSummary)
	}
	text := strings.Join(parts, "\n\n")
	if len(text) > maxEmbedChars {
		text = text[:maxEmbedChars]
	}
	return text
}

// CommitCorpus indexes git commit history (subject + body + diff summary).
type CommitCorpus struct {
	// Limit caps the number of commits enumerated; 0 means use a default.
	Limit int
}

// ID returns the stable corpus identifier.
func (c *CommitCorpus) ID() string { return "commits" }

// Collection returns the Qdrant collection name for this corpus + project.
func (c *CommitCorpus) Collection(projectRoot string) string {
	return "commits-" + ProjectHash(projectRoot)
}

// Enumerate emits Records for each commit, one per unit of content. The channel
// is closed when enumeration completes. Errors are reported via the errs channel.
func (c *CommitCorpus) Enumerate(projectRoot string) (<-chan Record, <-chan error) {
	out := make(chan Record)
	errs := make(chan error, 4)
	go func() {
		defer close(out)
		defer close(errs)
		limit := c.Limit
		if limit <= 0 {
			limit = 500
		}
		commits, err := listCommits(projectRoot, limit)
		if err != nil {
			errs <- err
			return
		}
		for _, cm := range commits {
			out <- Record{
				Path:      cm.Hash,
				Kind:      "commit",
				EmbedText: commitEmbedText(cm),
				Extra: map[string]any{
					"hash":    cm.Hash,
					"date":    cm.Date.Format("2006-01-02"),
					"subject": cm.Subject,
				},
			}
		}
	}()
	return out, errs
}

// listCommits extracts up to limit commits from the git repo at repoPath.
func listCommits(repoPath string, limit int) ([]commitRecord, error) {
	// Get hashes, dates, subjects, and bodies in one pass.
	// Use a rare delimiter to safely split multi-line bodies.
	// Use a delimiter unlikely to appear in commit messages.
	const sep = "|||COMMIT|||"
	logOut, err := gitCmd(repoPath, "log",
		fmt.Sprintf("-n%d", limit),
		"--format="+sep+"%H|%aI|%s|%b|||END|||",
	)
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}

	var records []commitRecord
	for _, block := range strings.Split(logOut, "|||END|||") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		block = strings.TrimPrefix(block, sep)
		// Split on first three pipes only (body may contain pipes)
		parts := strings.SplitN(block, "|", 4)
		if len(parts) < 3 {
			continue
		}
		hash := strings.TrimSpace(parts[0])
		dateStr := strings.TrimSpace(parts[1])
		subject := strings.TrimSpace(parts[2])
		body := ""
		if len(parts) == 4 {
			body = strings.TrimSpace(parts[3])
		}

		date, _ := time.Parse(time.RFC3339, dateStr)

		diffSummary, err := commitDiffSummary(repoPath, hash)
		if err != nil {
			// Non-fatal: first commit has no parent
			diffSummary = ""
		}

		records = append(records, commitRecord{
			Hash:        hash,
			Date:        date,
			Subject:     subject,
			Body:        body,
			DiffSummary: diffSummary,
		})
	}
	return records, nil
}

// commitDiffSummary returns the --stat output plus the first 300 bytes of the diff.
func commitDiffSummary(repoPath, hash string) (string, error) {
	stat, err := gitCmd(repoPath, "show", "--stat", "--no-patch", hash)
	if err != nil {
		return "", err
	}
	// Trim the commit header lines from --stat (everything before the file list)
	statLines := strings.Split(strings.TrimSpace(stat), "\n")
	var statBody []string
	for _, l := range statLines {
		// The file list starts after the empty line following the commit header
		if strings.Contains(l, "|") || strings.Contains(l, "changed") {
			statBody = append(statBody, l)
		}
	}

	diff, err := gitCmd(repoPath, "diff", hash+"^", hash, "--", ".")
	if err != nil {
		return strings.Join(statBody, "\n"), nil
	}
	snippet := diff
	if len(snippet) > 2000 {
		snippet = snippet[:2000]
	}

	return strings.Join(statBody, "\n") + "\n" + snippet, nil
}

func gitCmd(repoPath string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, errBuf.String())
	}
	return out.String(), nil
}
