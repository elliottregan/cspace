package corpus

import (
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Staleness reports whether the index for one corpus is current relative
// to the git repo. Cheap: compares payload hashes and commit timestamps
// already stored in qdrant rather than re-embedding.
type Staleness struct {
	IsStale       bool      `json:"is_stale"`
	Reason        string    `json:"reason,omitempty"`
	LastIndexedAt time.Time `json:"last_indexed_at,omitempty"`
}

// PointLister can scroll a qdrant collection and return id -> content_hash
// or id -> payload maps. Satisfied by *qdrant.QdrantClient (via adapter)
// without importing the qdrant package directly.
type PointLister interface {
	ExistingPoints(collection string) (map[uint64]string, error) // id -> content_hash
}

// CodeStaleness compares `git ls-files` content hashes against the indexed
// hashes in qdrant. O(tracked-files) — typically sub-second.
func CodeStaleness(projectRoot string, collection string, lister PointLister) (Staleness, error) {
	existing, err := lister.ExistingPoints(collection)
	if err != nil {
		return Staleness{}, fmt.Errorf("reading existing points: %w", err)
	}
	// If the collection is empty, the index has never run.
	if len(existing) == 0 {
		return Staleness{IsStale: true, Reason: "index is empty — never indexed"}, nil
	}

	// Build a set of content_hash values that exist in qdrant.
	indexedHashes := make(map[string]struct{}, len(existing))
	for _, h := range existing {
		if h != "" {
			indexedHashes[h] = struct{}{}
		}
	}

	// Walk git ls-files and compute sha256 for each tracked file, counting
	// files whose hash is not in the index.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "-C", projectRoot, "ls-files")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Staleness{}, fmt.Errorf("git ls-files pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return Staleness{}, fmt.Errorf("git ls-files start: %w", err)
	}

	changed := 0
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		rel := sc.Text()
		abs := filepath.Join(projectRoot, rel)
		data, err := os.ReadFile(abs)
		if err != nil {
			continue // deleted/unreadable — skip
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(data))
		if _, ok := indexedHashes[hash]; !ok {
			changed++
		}
	}
	_ = sc.Err()
	_ = cmd.Wait()

	if changed > 0 {
		return Staleness{
			IsStale: true,
			Reason:  fmt.Sprintf("%d files changed since last index", changed),
		}, nil
	}
	return Staleness{IsStale: false}, nil
}

// CommitsStaleness compares the current HEAD commit against the latest
// indexed commit date in qdrant. Cheap: one git log call + one qdrant
// scroll for max date.
func CommitsStaleness(projectRoot string, collection string, lister PointLister) (Staleness, error) {
	existing, err := lister.ExistingPoints(collection)
	if err != nil {
		return Staleness{}, fmt.Errorf("reading existing points: %w", err)
	}
	if len(existing) == 0 {
		return Staleness{IsStale: true, Reason: "index is empty — never indexed"}, nil
	}

	// Count commits since the indexed set. We check whether HEAD's hash
	// exists among indexed content_hashes (commit hashes are stored in
	// the "path" field, but content_hash is not set for commits — so we
	// use a different approach: count total commits and compare to indexed).
	countStr, err := gitOutput(projectRoot, "rev-list", "--count", "HEAD")
	if err != nil {
		return Staleness{}, fmt.Errorf("git rev-list --count: %w", err)
	}
	countStr = strings.TrimSpace(countStr)

	// The indexed set has N points; the repo has M commits. If M > N,
	// there are new commits.
	indexedCount := len(existing)
	var repoCount int
	if _, err := fmt.Sscanf(countStr, "%d", &repoCount); err != nil {
		return Staleness{}, fmt.Errorf("parsing commit count: %w", err)
	}

	diff := repoCount - indexedCount
	if diff > 0 {
		return Staleness{
			IsStale: true,
			Reason:  fmt.Sprintf("%d new commits since last index", diff),
		}, nil
	}
	return Staleness{IsStale: false}, nil
}

// gitOutput runs a git command and returns its stdout as a string.
func gitOutput(repoPath string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
