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
	"sync"
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

// MaxDateLister extends PointLister with the ability to retrieve the maximum
// "date" payload field from a collection. Used by CommitsStaleness to compare
// HEAD's commit date against the latest indexed commit, avoiding the false-
// positive caused by comparing raw counts under a commits.limit.
type MaxDateLister interface {
	MaxPayloadDate(collection string) (string, error) // "2006-01-02" or ""
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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

// CommitsStaleness compares HEAD's commit date against the latest indexed
// commit date in qdrant. Uses date identity (not count) so repos with more
// commits than commits.limit don't produce permanent false-positive warnings.
func CommitsStaleness(projectRoot string, collection string, lister PointLister) (Staleness, error) {
	existing, err := lister.ExistingPoints(collection)
	if err != nil {
		return Staleness{}, fmt.Errorf("reading existing points: %w", err)
	}
	if len(existing) == 0 {
		return Staleness{IsStale: true, Reason: "index is empty — never indexed"}, nil
	}

	// Get HEAD's commit date.
	headDate, err := gitOutput(projectRoot, "log", "-1", "--format=%aI", "HEAD")
	if err != nil {
		return Staleness{}, fmt.Errorf("git log HEAD date: %w", err)
	}
	headDate = strings.TrimSpace(headDate)
	headTime, err := time.Parse(time.RFC3339, headDate)
	if err != nil {
		return Staleness{}, fmt.Errorf("parsing HEAD date %q: %w", headDate, err)
	}

	// Get the max indexed commit date. If the lister supports MaxDateLister,
	// use it for a single-pass scroll; otherwise fall back to counting (which
	// is wrong under limits but at least gives something).
	if dl, ok := lister.(MaxDateLister); ok {
		maxDate, err := dl.MaxPayloadDate(collection)
		if err != nil {
			return Staleness{}, fmt.Errorf("max payload date: %w", err)
		}
		if maxDate == "" {
			return Staleness{IsStale: true, Reason: "no indexed commit dates found"}, nil
		}
		maxTime, err := time.Parse("2006-01-02", maxDate)
		if err != nil {
			return Staleness{}, fmt.Errorf("parsing max indexed date %q: %w", maxDate, err)
		}
		// Truncate HEAD to date precision to match the indexed format.
		headDay := headTime.Truncate(24 * time.Hour)
		maxDay := maxTime.Truncate(24 * time.Hour)
		if headDay.After(maxDay) {
			return Staleness{
				IsStale: true,
				Reason:  fmt.Sprintf("HEAD (%s) is newer than latest indexed commit (%s)", headDay.Format("2006-01-02"), maxDate),
			}, nil
		}
		return Staleness{IsStale: false}, nil
	}

	// Fallback for listers that don't support MaxDateLister: compare HEAD's
	// commit hash against the indexed set via point ID lookup.
	headHash, err := gitOutput(projectRoot, "rev-parse", "HEAD")
	if err != nil {
		return Staleness{}, fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	headHash = strings.TrimSpace(headHash)
	headRec := Record{Path: headHash, Kind: "commit"}
	headID := headRec.ID()
	if _, ok := existing[headID]; ok {
		return Staleness{IsStale: false}, nil
	}
	return Staleness{
		IsStale: true,
		Reason:  "HEAD commit is not in the index",
	}, nil
}

// --------------- staleness cache ---------------
//
// Staleness checks run git I/O + qdrant scrolls that cost 100-500ms. Caching
// the result for a short TTL avoids paying that cost on every MCP query while
// keeping the signal fresh enough for advisory use.

// StalenessCache TTL — configurable for tests via CacheTTL.
var CacheTTL = 30 * time.Second

type cachedStaleness struct {
	at  time.Time
	st  Staleness
	err error
}

var (
	staleCache = map[string]cachedStaleness{}
	staleMu    sync.Mutex
)

func staleCacheKey(corpus, projectRoot string) string {
	return corpus + "\x00" + projectRoot
}

// CodeStalenessCached wraps CodeStaleness with an in-process cache (TTL = CacheTTL).
func CodeStalenessCached(projectRoot, collection string, lister PointLister) (Staleness, error) {
	key := staleCacheKey("code", projectRoot)
	staleMu.Lock()
	if c, ok := staleCache[key]; ok && time.Since(c.at) < CacheTTL {
		staleMu.Unlock()
		return c.st, c.err
	}
	staleMu.Unlock()

	st, err := CodeStaleness(projectRoot, collection, lister)

	staleMu.Lock()
	staleCache[key] = cachedStaleness{at: time.Now(), st: st, err: err}
	staleMu.Unlock()
	return st, err
}

// CommitsStalenessCached wraps CommitsStaleness with an in-process cache (TTL = CacheTTL).
func CommitsStalenessCached(projectRoot, collection string, lister PointLister) (Staleness, error) {
	key := staleCacheKey("commits", projectRoot)
	staleMu.Lock()
	if c, ok := staleCache[key]; ok && time.Since(c.at) < CacheTTL {
		staleMu.Unlock()
		return c.st, c.err
	}
	staleMu.Unlock()

	st, err := CommitsStaleness(projectRoot, collection, lister)

	staleMu.Lock()
	staleCache[key] = cachedStaleness{at: time.Now(), st: st, err: err}
	staleMu.Unlock()
	return st, err
}

// ResetStalenessCache clears the cache. Exported for tests.
func ResetStalenessCache() {
	staleMu.Lock()
	staleCache = map[string]cachedStaleness{}
	staleMu.Unlock()
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
