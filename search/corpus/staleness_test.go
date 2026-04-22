package corpus

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakePointLister implements PointLister and MaxDateLister for tests.
type fakePointLister struct {
	points  map[uint64]string // id -> content_hash
	maxDate string            // max date returned by MaxPayloadDate
}

func (f *fakePointLister) ExistingPoints(_ string) (map[uint64]string, error) {
	return f.points, nil
}

func (f *fakePointLister) MaxPayloadDate(_ string) (string, error) {
	return f.maxDate, nil
}

// initGitRepo creates a temp dir with a git repo containing the given files.
func initGitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "initial")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func gitOutputHelper(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return string(out)
}

func hashOf(content string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
}

func TestCodeStaleness_EmptyIndex(t *testing.T) {
	dir := initGitRepo(t, map[string]string{"main.go": "package main"})
	lister := &fakePointLister{points: map[uint64]string{}}

	st, err := CodeStaleness(dir, "test-collection", lister)
	if err != nil {
		t.Fatal(err)
	}
	if !st.IsStale {
		t.Error("expected stale for empty index")
	}
	if !strings.Contains(st.Reason, "never indexed") {
		t.Errorf("unexpected reason: %s", st.Reason)
	}
}

func TestCodeStaleness_UpToDate(t *testing.T) {
	content := "package main"
	dir := initGitRepo(t, map[string]string{"main.go": content})

	// Simulate an index that has the file's hash.
	lister := &fakePointLister{points: map[uint64]string{
		1: hashOf(content),
	}}

	st, err := CodeStaleness(dir, "test-collection", lister)
	if err != nil {
		t.Fatal(err)
	}
	if st.IsStale {
		t.Errorf("expected not stale, got reason: %s", st.Reason)
	}
}

func TestCodeStaleness_FilesChanged(t *testing.T) {
	dir := initGitRepo(t, map[string]string{
		"a.go": "package a",
		"b.go": "package b",
	})

	// Index only knows about a.go's hash — b.go's hash is missing.
	lister := &fakePointLister{points: map[uint64]string{
		1: hashOf("package a"),
	}}

	st, err := CodeStaleness(dir, "test-collection", lister)
	if err != nil {
		t.Fatal(err)
	}
	if !st.IsStale {
		t.Error("expected stale")
	}
	if !strings.Contains(st.Reason, "files changed") {
		t.Errorf("unexpected reason: %s", st.Reason)
	}
}

func TestCommitsStaleness_EmptyIndex(t *testing.T) {
	dir := initGitRepo(t, map[string]string{"main.go": "package main"})
	lister := &fakePointLister{points: map[uint64]string{}}

	st, err := CommitsStaleness(dir, "test-collection", lister)
	if err != nil {
		t.Fatal(err)
	}
	if !st.IsStale {
		t.Error("expected stale for empty index")
	}
}

func TestCommitsStaleness_UpToDate(t *testing.T) {
	dir := initGitRepo(t, map[string]string{"main.go": "package main"})

	// Get HEAD's date so we can set maxDate to match.
	headDate := gitOutputHelper(t, dir, "log", "-1", "--format=%aI", "HEAD")
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(headDate))
	if err != nil {
		t.Fatal(err)
	}

	// One commit in repo, one point in index, maxDate matching HEAD.
	lister := &fakePointLister{
		points:  map[uint64]string{1: ""},
		maxDate: parsed.Format("2006-01-02"),
	}

	st, err := CommitsStaleness(dir, "test-collection", lister)
	if err != nil {
		t.Fatal(err)
	}
	if st.IsStale {
		t.Errorf("expected not stale, got reason: %s", st.Reason)
	}
}

func TestCommitsStaleness_NewCommits(t *testing.T) {
	dir := initGitRepo(t, map[string]string{"main.go": "package main"})

	// Record first commit's date as the "indexed" max date.
	firstDate := gitOutputHelper(t, dir, "log", "-1", "--format=%aI", "HEAD")
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(firstDate))
	if err != nil {
		t.Fatal(err)
	}
	indexedDate := parsed.Format("2006-01-02")

	// Add a second commit with a future date so HEAD > indexed.
	runGit(t, dir, "commit", "--allow-empty", "-m", "second",
		"--date=2099-01-01T00:00:00+00:00")

	// Index only knows about the first commit's date.
	lister := &fakePointLister{
		points:  map[uint64]string{1: ""},
		maxDate: indexedDate,
	}

	st, err := CommitsStaleness(dir, "test-collection", lister)
	if err != nil {
		t.Fatal(err)
	}
	if !st.IsStale {
		t.Error("expected stale")
	}
	if !strings.Contains(st.Reason, "newer") {
		t.Errorf("unexpected reason: %s", st.Reason)
	}
}

// TestCommitsStaleness_RespectsLimit is a regression test for PR #61 item #2:
// with commits.limit=2 on a 5-commit repo, CommitsStaleness should report
// stale ONLY if HEAD is newer than the latest indexed, not because total
// commit count != indexed count.
func TestCommitsStaleness_RespectsLimit(t *testing.T) {
	dir := initGitRepo(t, map[string]string{"main.go": "package main"})

	// Add 4 more commits (5 total).
	for i := 2; i <= 5; i++ {
		runGit(t, dir, "commit", "--allow-empty", "-m", fmt.Sprintf("commit %d", i))
	}

	// Get HEAD's date.
	headDate := gitOutputHelper(t, dir, "log", "-1", "--format=%aI", "HEAD")
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(headDate))
	if err != nil {
		t.Fatal(err)
	}

	// Simulate commits.limit=2: index has 2 points, but maxDate matches HEAD.
	// Under the old count-based logic this would be "3 new commits" (5-2=3).
	// Under the new date-based logic, it should be not stale.
	lister := &fakePointLister{
		points:  map[uint64]string{1: "", 2: ""},
		maxDate: parsed.Format("2006-01-02"),
	}

	st, err := CommitsStaleness(dir, "test-collection", lister)
	if err != nil {
		t.Fatal(err)
	}
	if st.IsStale {
		t.Errorf("expected not stale with matching HEAD date, got stale: %s", st.Reason)
	}
}
