package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// provisionClone creates or reuses a per-sandbox git clone at
//
//	~/.cspace/clones/<project>/<sandbox>/
//
// off baseBranch (or current HEAD of projectRoot when baseBranch is "").
// Returns the absolute path to the clone, suitable for bind-mounting as
// /workspace.
//
// When branch is non-empty, the clone is checked out on a fresh branch of
// that name created off the cloned tip — used by autonomous flows that
// want each sandbox's work isolated under a meaningful branch (e.g.
// "issue/538-fix"). When branch is empty (the default for interactive
// `cspace up`), the clone stays on whatever baseBranch landed on, which
// matches the muscle memory of agents and tutorials that assume `main`.
//
// If the sandbox's clone already exists, this function is idempotent: it
// does not re-clone or touch the checked-out branch. The caller's
// `--keep-state` semantics own that lifecycle.
//
// If projectRoot is empty or not a git repo, returns ("", nil) — the
// caller should treat that as "no workspace mount" with a warning.
func provisionClone(projectRoot, projectName, sandboxName, baseBranch, branch string) (string, error) {
	if projectRoot == "" {
		return "", nil
	}
	if _, err := os.Stat(filepath.Join(projectRoot, ".git")); err != nil {
		// Not a git repo (or no access). Fall through to "no workspace".
		return "", nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	clonePath := filepath.Join(home, ".cspace", "clones", projectName, sandboxName)

	if _, err := os.Stat(clonePath); err == nil {
		// Clone already exists; honor whatever branch is checked out.
		// `cspace down --keep-state` exists for callers who want the
		// previous session's branch and uncommitted state preserved;
		// don't surprise them by force-switching here.
		return clonePath, nil
	}

	// Need to clone. Make parent dir.
	if err := os.MkdirAll(filepath.Dir(clonePath), 0o755); err != nil {
		return "", err
	}

	cloneArgs := []string{"clone"}
	if baseBranch != "" {
		cloneArgs = append(cloneArgs, "--branch", baseBranch)
	}
	cloneArgs = append(cloneArgs, projectRoot, clonePath)
	out, err := exec.Command("git", cloneArgs...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git clone: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	// Rewrite the clone's "origin" to the host project's upstream when one
	// exists (typically https://github.com/<owner>/<repo>.git). Without
	// this, origin points at projectRoot's filesystem path — which means
	// gh can't recognize the repo, `gh pr list` errors out with "none of
	// the git remotes ... point to a known GitHub host", and the agent
	// can't open PRs from inside the sandbox. Keep the local path under
	// a sibling remote called "host" so users can still `git push host`
	// or `git fetch host` for round-trips that don't go through GitHub.
	if upstream, err := readOrigin(projectRoot); err == nil && upstream != "" {
		_ = runGit(clonePath, "remote", "rename", "origin", "host")
		_ = runGit(clonePath, "remote", "add", "origin", upstream)
	}

	// Optional named branch — autonomous flows pass `--branch issue/N-foo`
	// (or `--branch auto` which the caller resolves to cspace/<sandbox>)
	// when they want each sandbox's work isolated under a meaningful name
	// ready to push as a PR. Interactive callers leave this empty so the
	// clone stays on baseBranch.
	if branch != "" {
		if err := runGit(clonePath, "checkout", "-b", branch); err != nil {
			return "", fmt.Errorf("create branch %s: %w", branch, err)
		}
	}

	return clonePath, nil
}

// readOrigin returns the URL of `origin` in the given git repo, or "" if
// origin is missing or not configured. Errors only on transport failures
// running the command itself; callers that just want "best-effort
// upstream URL" can ignore the error.
func readOrigin(dir string) (string, error) {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// runGit executes `git <args...>` with cwd set to the given directory and
// wraps any error with combined stdout+stderr for diagnostics.
func runGit(cwd string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %v in %s: %w (%s)", args, cwd, err, strings.TrimSpace(string(out)))
	}
	return nil
}
