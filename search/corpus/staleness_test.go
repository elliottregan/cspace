package corpus

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakePointLister implements PointLister for tests.
type fakePointLister struct {
	points map[uint64]string // id -> content_hash
}

func (f *fakePointLister) ExistingPoints(_ string) (map[uint64]string, error) {
	return f.points, nil
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

	// One commit in repo, one point in index.
	lister := &fakePointLister{points: map[uint64]string{
		1: "",
	}}

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
	// Add a second commit.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n// v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "second")

	// Index only knows about one commit.
	lister := &fakePointLister{points: map[uint64]string{
		1: "",
	}}

	st, err := CommitsStaleness(dir, "test-collection", lister)
	if err != nil {
		t.Fatal(err)
	}
	if !st.IsStale {
		t.Error("expected stale")
	}
	if !strings.Contains(st.Reason, "new commits") {
		t.Errorf("unexpected reason: %s", st.Reason)
	}
}
