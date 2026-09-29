package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

// HeaderStatus describes the selected sandbox's workspace. A query can return
// useful git/PR data alongside an error; PRStale marks a failed PR refresh.
type HeaderStatus struct {
	Branch   string
	Dirty    bool
	Detached bool
	PR       *PullRequestStatus
	PRStale  bool
}

type PullRequestStatus struct {
	Number      int
	URL         string
	CheckStatus string // success, failure, running, none, or blocked
}

const pullRequestTTL = 2 * time.Minute

type pullRequestKey struct{ repository, branch string }

type pullRequestCache struct {
	mu      sync.Mutex
	entries map[pullRequestKey]*pullRequestCacheEntry
}

type pullRequestCacheEntry struct {
	mu        sync.Mutex
	checkedAt time.Time
	attempted bool
	pr        *PullRequestStatus
	err       error
}

func (c *pullRequestCache) entry(key pullRequestKey) *pullRequestCacheEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[pullRequestKey]*pullRequestCacheEntry)
	}
	if c.entries[key] == nil {
		c.entries[key] = &pullRequestCacheEntry{}
	}
	return c.entries[key]
}

// HeaderStatus reads git from the sandbox's host-side clone, independently of
// the in-agent statusline. PR results (including no open PR and failed probes)
// are cached by repository and branch for two minutes. Force bypasses that
// cache. A failed refresh preserves the last successful result for this key.
func (c *Client) HeaderStatus(ctx context.Context, project, sandbox string, force bool) (HeaderStatus, error) {
	if c.home == "" {
		return HeaderStatus{}, ErrNoHome
	}
	dir := CloneDir(c.home, project, sandbox)
	out, err := c.runCommand(ctx, dir, "git", "--no-optional-locks", "status", "--porcelain=v2", "--branch", "--untracked-files=normal")
	if err != nil {
		return HeaderStatus{}, headerCommandError("read workspace git status", out, err)
	}
	status, err := parseHeaderGitStatus(out)
	if err != nil || status.Detached {
		return status, err
	}
	out, err = c.runCommand(ctx, dir, "git", "remote", "get-url", "origin")
	if err != nil {
		return status, headerCommandError("read workspace origin", out, err)
	}
	repository, err := pullRequestRepository(strings.TrimSpace(out))
	if err != nil {
		return status, err
	}
	status.PR, err = c.pullRequest(ctx, dir, repository, status.Branch, force)
	status.PRStale = err != nil
	return status, err
}

func parseHeaderGitStatus(out string) (HeaderStatus, error) {
	var status HeaderStatus
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			status.Branch = strings.TrimPrefix(line, "# branch.head ")
			if status.Branch == "(detached)" {
				status.Branch, status.Detached = "HEAD", true
			}
		case strings.HasPrefix(line, "1 "), strings.HasPrefix(line, "2 "), strings.HasPrefix(line, "u "), strings.HasPrefix(line, "? "):
			status.Dirty = true
		}
	}
	if status.Branch == "" {
		return status, fmt.Errorf("workspace git status did not report a branch")
	}
	return status, nil
}

// Normalize HTTPS and SSH remotes to gh's [HOST/]OWNER/REPO syntax. Credentials
// in a remote URL must never enter the cache key or a displayed error.
func pullRequestRepository(remote string) (string, error) {
	var host, path string
	if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err != nil {
			return "", fmt.Errorf("workspace origin is not a supported repository URL")
		}
		host, path = u.Hostname(), u.Path
	} else if before, after, ok := strings.Cut(remote, ":"); ok {
		host, path = before, after
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	parts := strings.Split(path, "/")
	if host == "" || len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(host+path, "\n\r\t ") {
		return "", fmt.Errorf("workspace origin is not a supported repository URL")
	}
	return strings.ToLower(host) + "/" + path, nil
}

func (c *Client) pullRequest(ctx context.Context, dir, repository, branch string, force bool) (*PullRequestStatus, error) {
	entry := c.prCache.entry(pullRequestKey{repository: repository, branch: branch})
	// Serialize only this repository/branch. Concurrent sandbox polls share one
	// network request, while unrelated projects can refresh independently.
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if force || !entry.attempted || c.now().Sub(entry.checkedAt) >= pullRequestTTL {
		out, err := c.runCommand(ctx, dir, "gh", "pr", "list", "--repo", repository,
			"--state", "open", "--head", branch, "--limit", "1", "--json", "number,url,mergeStateStatus,statusCheckRollup")
		var pr *PullRequestStatus
		if err != nil {
			err = headerCommandError("read open pull request", out, err)
		} else {
			pr, err = parsePullRequest(out)
		}
		entry.checkedAt, entry.attempted, entry.err = c.now(), true, err
		if err == nil {
			entry.pr = pr
		}
	}
	// Callers own their result, so mutating a displayed PR cannot race with
	// another reader or poison a later cache hit.
	if entry.pr == nil {
		return nil, entry.err
	}
	pr := *entry.pr
	return &pr, entry.err
}

type pullRequestJSON struct {
	Number           int    `json:"number"`
	URL              string `json:"url"`
	MergeStateStatus string `json:"mergeStateStatus"`
	Checks           []struct {
		Conclusion string `json:"conclusion"`
		Status     string `json:"status"`
		State      string `json:"state"`
	} `json:"statusCheckRollup"`
}

func parsePullRequest(out string) (*PullRequestStatus, error) {
	var prs []pullRequestJSON
	if err := json.Unmarshal([]byte(out), &prs); err != nil {
		return nil, fmt.Errorf("decode open pull request: %w", err)
	}
	if prs == nil {
		return nil, fmt.Errorf("open pull request response is not a list")
	}
	if len(prs) == 0 {
		return nil, nil
	}
	pr := prs[0]
	if pr.Number <= 0 || pr.URL == "" {
		return nil, fmt.Errorf("open pull request response is missing its number or URL")
	}
	return &PullRequestStatus{Number: pr.Number, URL: pr.URL, CheckStatus: pullRequestCheckStatus(pr)}, nil
}

// Match the statusline's ordering: conflicts/failures outrank pending checks;
// BLOCKED alone does not mean failure, and CLEAN with no checks is success.
func pullRequestCheckStatus(pr pullRequestJSON) string {
	failed, running := pr.MergeStateStatus == "DIRTY", false
	for _, check := range pr.Checks {
		switch check.Conclusion {
		case "FAILURE", "TIMED_OUT", "STARTUP_FAILURE", "ACTION_REQUIRED":
			failed = true
		}
		switch check.State {
		case "FAILURE", "ERROR":
			failed = true
		case "PENDING":
			running = true
		}
		switch check.Status {
		case "QUEUED", "IN_PROGRESS", "WAITING", "PENDING":
			running = true
		}
	}
	switch {
	case failed:
		return "failure"
	case running:
		return "running"
	case pr.MergeStateStatus == "CLEAN":
		return "success"
	case len(pr.Checks) == 0:
		return "none"
	default:
		return "blocked"
	}
}

func headerCommandError(action, out string, err error) error {
	if out = strings.TrimSpace(out); out != "" {
		return fmt.Errorf("%s: %w (%s)", action, err, out)
	}
	return fmt.Errorf("%s: %w", action, err)
}
