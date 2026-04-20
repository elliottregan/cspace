package search

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// CommitRecord holds the extracted data for a single commit.
type CommitRecord struct {
	Hash        string
	Date        time.Time
	Subject     string
	Body        string
	DiffSummary string
}

// maxEmbedChars is the character limit for embed text. The Jina retrieval model
// supports 8K tokens; 12000 chars ≈ 3000 tokens leaving headroom for the prefix.
const maxEmbedChars = 12000

// EmbedText returns the text to embed for this commit, truncated to fit the model's context.
func (c CommitRecord) EmbedText() string {
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

// ListCommits extracts up to limit commits from the git repo at repoPath.
func ListCommits(repoPath string, limit int) ([]CommitRecord, error) {
	// Get hashes, dates, subjects, and bodies in one pass.
	// Use a rare delimiter to safely split multi-line bodies.
	// Use a delimiter unlikely to appear in commit messages.
	const sep = "|||COMMIT|||"
	logOut, err := git(repoPath, "log",
		fmt.Sprintf("-n%d", limit),
		"--format="+sep+"%H|%aI|%s|%b|||END|||",
	)
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}

	var records []CommitRecord
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

		records = append(records, CommitRecord{
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
	stat, err := git(repoPath, "show", "--stat", "--no-patch", hash)
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

	diff, err := git(repoPath, "diff", hash+"^", hash, "--", ".")
	if err != nil {
		return strings.Join(statBody, "\n"), nil
	}
	snippet := diff
	if len(snippet) > 2000 {
		snippet = snippet[:2000]
	}

	return strings.Join(statBody, "\n") + "\n" + snippet, nil
}

func git(repoPath string, args ...string) (string, error) {
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
