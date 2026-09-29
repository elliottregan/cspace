package control

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type headerFixture struct {
	mu      sync.Mutex
	now     time.Time
	git     map[string]string
	remotes map[string]string
	pr      map[pullRequestKey]string
	prErr   error
	gitErr  error
	calls   map[pullRequestKey]int
}

func newHeaderFixture(t *testing.T) (*Client, *headerFixture) {
	t.Helper()
	f := &headerFixture{
		now: time.Unix(1_000_000, 0), git: map[string]string{}, remotes: map[string]string{},
		pr: map[pullRequestKey]string{}, calls: map[pullRequestKey]int{},
	}
	c := New(Options{Home: t.TempDir(), Now: func() time.Time {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.now
	}})
	c.runCommand = f.run
	return c, f
}

func (f *headerFixture) workspace(c *Client, project, sandbox, branch, remote string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	dir := CloneDir(c.home, project, sandbox)
	f.git[dir], f.remotes[dir] = "# branch.head "+branch+"\n", remote
}

func (f *headerFixture) run(_ context.Context, dir, bin string, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case bin == "git" && reflect.DeepEqual(args, []string{"--no-optional-locks", "status", "--porcelain=v2", "--branch", "--untracked-files=normal"}):
		return f.git[dir], f.gitErr
	case bin == "git" && reflect.DeepEqual(args, []string{"remote", "get-url", "origin"}):
		return f.remotes[dir], nil
	case bin == "gh":
		if len(args) != 12 || args[0] != "pr" || args[1] != "list" || args[2] != "--repo" || args[4] != "--state" || args[5] != "open" || args[6] != "--head" {
			return "", fmt.Errorf("unexpected gh args: %v", args)
		}
		key := pullRequestKey{args[3], args[7]}
		f.calls[key]++
		if f.prErr != nil {
			return "", f.prErr
		}
		if out, ok := f.pr[key]; ok {
			return out, nil
		}
		return "[]", nil
	default:
		return "", fmt.Errorf("unexpected command: %s %v", bin, args)
	}
}

func prFixture(number int, merge, checks string) string {
	return fmt.Sprintf(`[{"number":%d,"url":"https://github.com/owner/repo/pull/%d","mergeStateStatus":%q,"statusCheckRollup":%s}]`, number, number, merge, checks)
}

func TestHeaderStatusCacheAndForce(t *testing.T) {
	c, f := newHeaderFixture(t)
	f.workspace(c, "alpha", "mercury", "feature", "git@github.com:owner/repo.git")
	key := pullRequestKey{"github.com/owner/repo", "feature"}
	f.pr[key] = prFixture(12, "CLEAN", "[]")
	read := func(force bool, want int) HeaderStatus {
		t.Helper()
		status, err := c.HeaderStatus(context.Background(), "alpha", "mercury", force)
		if err != nil || status.PR == nil || status.PR.Number != want {
			t.Fatalf("HeaderStatus = %+v, %v; want PR %d", status, err, want)
		}
		return status
	}
	status := read(false, 12)
	status.PR.Number = 999 // must not mutate the cached value
	f.pr[key] = prFixture(13, "CLEAN", "[]")
	f.git[CloneDir(c.home, "alpha", "mercury")] += "? new-file\n"
	if status = read(false, 12); !status.Dirty || status.Branch != "feature" {
		t.Fatalf("git status should be fresh even on a PR cache hit: %+v", status)
	}
	f.now = f.now.Add(pullRequestTTL - time.Second)
	read(false, 12)
	f.now = f.now.Add(time.Second)
	read(false, 13)
	f.pr[key] = prFixture(14, "CLEAN", "[]")
	read(true, 14)
	if f.calls[key] != 3 {
		t.Fatalf("gh calls = %d, want initial + TTL + force", f.calls[key])
	}
}

func TestHeaderStatusCacheIsolationAndBranchChange(t *testing.T) {
	c, f := newHeaderFixture(t)
	workspaces := []struct{ project, sandbox, branch, remote string }{
		{"alpha", "mercury", "feature/a", "https://github.com/owner/alpha.git"},
		{"alpha", "venus", "feature/a", "git@github.com:owner/alpha.git"},
		{"beta", "mercury", "feature/a", "https://github.com/owner/beta.git"},
		{"alpha", "earth", "feature-a", "https://github.com/owner/alpha.git"},
	}
	for _, w := range workspaces {
		f.workspace(c, w.project, w.sandbox, w.branch, w.remote)
	}
	keys := []pullRequestKey{{"github.com/owner/alpha", "feature/a"}, {"github.com/owner/beta", "feature/a"}, {"github.com/owner/alpha", "feature-a"}}
	for i, key := range keys {
		f.pr[key] = prFixture(i+1, "CLEAN", "[]")
	}
	for i, w := range workspaces {
		status, err := c.HeaderStatus(context.Background(), w.project, w.sandbox, false)
		want := []int{1, 1, 2, 3}[i]
		if err != nil || status.PR == nil || status.PR.Number != want {
			t.Fatalf("workspace %d: %+v, %v; want PR %d", i, status, err, want)
		}
	}
	for _, key := range keys {
		if f.calls[key] != 1 {
			t.Fatalf("calls[%+v] = %d, want 1", key, f.calls[key])
		}
	}
	f.workspace(c, "alpha", "mercury", "new-branch", "https://github.com/owner/alpha.git")
	status, err := c.HeaderStatus(context.Background(), "alpha", "mercury", false)
	if err != nil || status.Branch != "new-branch" || status.PR != nil || status.PRStale {
		t.Fatalf("branch change carried another branch's PR: %+v, %v", status, err)
	}
}

func TestHeaderStatusKeepsStalePRAfterFailure(t *testing.T) {
	c, f := newHeaderFixture(t)
	f.workspace(c, "alpha", "mercury", "feature", "https://github.com/owner/repo")
	key := pullRequestKey{"github.com/owner/repo", "feature"}
	f.pr[key] = prFixture(12, "CLEAN", "[]")
	if _, err := c.HeaderStatus(context.Background(), "alpha", "mercury", false); err != nil {
		t.Fatal(err)
	}
	f.prErr = errors.New("network unavailable")
	for _, force := range []bool{true, false} {
		status, err := c.HeaderStatus(context.Background(), "alpha", "mercury", force)
		if !errors.Is(err, f.prErr) || !status.PRStale || status.PR == nil || status.PR.Number != 12 || status.Branch != "feature" {
			t.Fatalf("stale status = %+v, %v", status, err)
		}
	}
	if f.calls[key] != 2 {
		t.Fatalf("failure should be cached too, calls = %d", f.calls[key])
	}
	f.prErr, f.pr[key] = nil, "[]"
	status, err := c.HeaderStatus(context.Background(), "alpha", "mercury", true)
	if err != nil || status.PR != nil || status.PRStale {
		t.Fatalf("successful no-PR refresh should clear stale PR: %+v, %v", status, err)
	}
}

func TestHeaderStatusNoPROrMissingGh(t *testing.T) {
	for _, missingGh := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing-gh=%v", missingGh), func(t *testing.T) {
			c, f := newHeaderFixture(t)
			f.workspace(c, "alpha", "mercury", "main", "https://github.com/owner/repo")
			if missingGh {
				f.prErr = exec.ErrNotFound
			}
			for range 2 {
				status, err := c.HeaderStatus(context.Background(), "alpha", "mercury", false)
				if status.PR != nil || status.PRStale != missingGh || (err != nil) != missingGh {
					t.Fatalf("status = %+v, %v", status, err)
				}
				if missingGh && !errors.Is(err, exec.ErrNotFound) {
					t.Fatalf("missing gh error was lost: %v", err)
				}
			}
			if f.calls[pullRequestKey{"github.com/owner/repo", "main"}] != 1 {
				t.Fatal("no PR and missing gh must both be cached")
			}
		})
	}
}

func TestHeaderStatusConcurrentCacheReaders(t *testing.T) {
	c, f := newHeaderFixture(t)
	f.workspace(c, "alpha", "mercury", "feature", "https://github.com/owner/repo")
	key := pullRequestKey{"github.com/owner/repo", "feature"}
	f.pr[key] = prFixture(12, "CLEAN", "[]")
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			status, err := c.HeaderStatus(context.Background(), "alpha", "mercury", false)
			if err != nil || status.PR == nil || status.PR.Number != 12 {
				t.Errorf("concurrent status = %+v, %v", status, err)
				return
			}
			status.PR.Number = 99
		})
	}
	wg.Wait()
	if f.calls[key] != 1 {
		t.Fatalf("concurrent readers made %d gh calls, want 1", f.calls[key])
	}
}

func TestHeaderStatusGitFailureAndDetachedHead(t *testing.T) {
	c, f := newHeaderFixture(t)
	f.workspace(c, "alpha", "mercury", "(detached)", "")
	status, err := c.HeaderStatus(context.Background(), "alpha", "mercury", false)
	if err != nil || !status.Detached || status.Branch != "HEAD" || status.PR != nil || len(f.calls) != 0 {
		t.Fatalf("detached status = %+v, %v", status, err)
	}
	f.gitErr = errors.New("clone missing")
	if _, err := c.HeaderStatus(context.Background(), "alpha", "mercury", false); !errors.Is(err, f.gitErr) {
		t.Fatalf("git error = %v", err)
	}
	c.home = ""
	if _, err := c.HeaderStatus(context.Background(), "alpha", "mercury", false); !errors.Is(err, ErrNoHome) {
		t.Fatalf("no home error = %v", err)
	}
}

func TestHeaderGitDirtyStates(t *testing.T) {
	for _, record := range []string{"1 M. staged", "1 .M unstaged", "2 R. renamed", "u UU conflict", "? untracked"} {
		status, err := parseHeaderGitStatus("# branch.oid abc\n# branch.head feature\n" + record + "\n")
		if err != nil || !status.Dirty || status.Branch != "feature" {
			t.Errorf("record %q: %+v, %v", record, status, err)
		}
	}
}

func TestPullRequestStatusMatchesStatusline(t *testing.T) {
	cases := []struct{ name, merge, checks, want string }{
		{"clean without checks", "CLEAN", "[]", "success"},
		{"no checks", "UNKNOWN", "null", "none"},
		{"merge conflict", "DIRTY", `[{"status":"IN_PROGRESS"}]`, "failure"},
		{"failing check before pending", "BLOCKED", `[{"conclusion":"FAILURE"},{"status":"QUEUED"}]`, "failure"},
		{"timed out", "BLOCKED", `[{"conclusion":"TIMED_OUT"}]`, "failure"},
		{"startup failure", "BLOCKED", `[{"conclusion":"STARTUP_FAILURE"}]`, "failure"},
		{"action required", "BLOCKED", `[{"conclusion":"ACTION_REQUIRED"}]`, "failure"},
		{"legacy error", "BLOCKED", `[{"state":"ERROR"}]`, "failure"},
		{"legacy pending", "BLOCKED", `[{"state":"PENDING"}]`, "running"},
		{"in progress", "BLOCKED", `[{"status":"IN_PROGRESS"}]`, "running"},
		{"waiting", "BLOCKED", `[{"status":"WAITING"}]`, "running"},
		{"approval blocked", "BLOCKED", `[{"conclusion":"SUCCESS","status":"COMPLETED"}]`, "blocked"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pr, err := parsePullRequest(prFixture(7, tc.merge, tc.checks))
			if err != nil || pr == nil || pr.CheckStatus != tc.want {
				t.Fatalf("PR = %+v, %v; want %s", pr, err, tc.want)
			}
		})
	}
	for _, out := range []string{"broken JSON", "{}", "null", `[{}]`} {
		if _, err := parsePullRequest(out); err == nil {
			t.Errorf("malformed PR %q unexpectedly accepted", out)
		}
	}
}

func TestPullRequestRepository(t *testing.T) {
	for _, remote := range []string{"git@github.com:owner/repo.git", "https://github.com/owner/repo.git", "ssh://git@github.com/owner/repo.git", "https://user:secret@github.com/owner/repo.git"} {
		got, err := pullRequestRepository(remote)
		if err != nil || got != "github.com/owner/repo" {
			t.Errorf("repository(%q) = %q, %v", remote, got, err)
		}
	}
	for _, remote := range []string{"/tmp/repo", "https://secret@localhost/repo", "bad remote"} {
		_, err := pullRequestRepository(remote)
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Errorf("unsupported remote result: %v", err)
		}
	}
}
