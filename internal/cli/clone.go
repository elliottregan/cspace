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
// branched as cspace/<sandbox> off baseBranch (or current HEAD of projectRoot
// when baseBranch is ""). Returns the absolute path to the clone, suitable for
// bind-mounting as /workspace.
//
// If the sandbox's clone already exists, this function is idempotent: it does
// not re-clone; it just ensures the cspace/<sandbox> branch is checked out and
// returns the path. If projectRoot is empty or not a git repo, returns
// ("", nil) — the caller should treat that as "no workspace mount" with a
// warning. See finding 2026-05-01-per-sandbox-git-clone-bind-mounted-as-
// workspace-works-as-des for the locked design.
func provisionClone(projectRoot, projectName, sandboxName, baseBranch string) (string, error) {
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
	branch := "cspace/" + sandboxName

	if _, err := os.Stat(clonePath); err == nil {
		// Clone already exists; ensure the cspace/<sandbox> branch is checked out.
		if err := runGit(clonePath, "checkout", branch); err != nil {
			// Branch may not exist yet (e.g. clone was created by a different
			// workflow). Try to create it from current HEAD as a fallback.
			if err2 := runGit(clonePath, "checkout", "-b", branch); err2 != nil {
				return "", fmt.Errorf("checkout %s in %s: %w / %w", branch, clonePath, err, err2)
			}
		}
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

	// Create the cspace/<sandbox> branch (off whatever the clone now points at).
	if err := runGit(clonePath, "checkout", "-b", branch); err != nil {
		return "", fmt.Errorf("create branch %s: %w", branch, err)
	}

	return clonePath, nil
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
