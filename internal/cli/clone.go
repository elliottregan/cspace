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
// off baseBranch (defaulting to the upstream's default when empty).
// Returns the absolute path to the clone, suitable for bind-mounting as
// /workspace.
//
// The clone is sourced from `origin`'s URL on projectRoot, not from
// projectRoot itself. Effect: every sandbox starts from a reproducible
// upstream tip rather than from whatever the host's working tree happens
// to have committed locally. Round-tripping WIP from host to sandbox
// goes through `git push` to the upstream, which is the predictable
// path users already expect.
//
// When branch is non-empty, the clone is checked out on a fresh branch
// of that name created off the cloned tip — used by autonomous flows
// that want each sandbox's work isolated under a meaningful branch
// (e.g. "issue/538-fix"). When branch is empty (the default for
// interactive `cspace up`), the clone stays on whatever baseBranch
// landed on, which matches the muscle memory of agents and tutorials
// that assume `main`.
//
// If the sandbox's clone already exists, this function is idempotent:
// it does not re-clone or touch the checked-out branch. The caller's
// `--keep-state` semantics own that lifecycle.
//
// If projectRoot is empty or not a git repo, returns ("", nil) — the
// caller treats that as "no workspace mount" with a warning. If the
// project is a git repo but has no `origin` remote, returns an error;
// see the inline comment for the deliberate no-fallback choice.
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

	// Cloning from the upstream URL (origin), not from projectRoot. This
	// keeps the sandbox starting state reproducible across hosts and
	// avoids a class of confusing failures (`gh pr list` not finding the
	// repo, split-brain `host`/`origin` remotes, etc.). See issue #80.
	//
	// Deliberately no fallback to cloning from projectRoot for repos
	// without an `origin` — that path is what got us into the
	// split-brain remote mess. If a pure-local-repo workflow ever
	// becomes a real need, this is where the fallback would go.
	upstream, _ := readOrigin(projectRoot)
	if upstream == "" {
		return "", fmt.Errorf("project has no `origin` git remote; cspace clones from the upstream URL. " +
			"Add one with `git remote add origin <url>` and push at least the base branch")
	}

	if err := os.MkdirAll(filepath.Dir(clonePath), 0o755); err != nil {
		return "", err
	}

	cloneArgs := []string{"clone"}
	if baseBranch != "" {
		cloneArgs = append(cloneArgs, "--branch", baseBranch)
	}
	cloneArgs = append(cloneArgs, upstream, clonePath)
	out, err := exec.Command("git", cloneArgs...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git clone %s: %w (%s)", upstream, err, strings.TrimSpace(string(out)))
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
