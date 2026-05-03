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
		// Migrate existing clones whose origin still points at the host
		// project's filesystem path — without this, gh inside the
		// sandbox can't recognize the repo. New clones get this wiring
		// at creation; this branch handles clones from before that fix.
		fixupOrigin(clonePath, projectRoot)
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

	fixupOrigin(clonePath, projectRoot)

	// Create the cspace/<sandbox> branch (off whatever the clone now points at).
	if err := runGit(clonePath, "checkout", "-b", branch); err != nil {
		return "", fmt.Errorf("create branch %s: %w", branch, err)
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

// fixupOrigin ensures the per-sandbox clone's "origin" points at the
// host project's upstream remote (typically a github.com URL) rather
// than the host project's filesystem path. Without this, gh inside the
// sandbox can't tell what GitHub repo this is and `gh pr list` errors
// out with "none of the git remotes point to a known GitHub host".
//
// Idempotent and best-effort: if the host has no origin, or the clone's
// origin is already the host's upstream, or any individual git command
// fails, we leave the clone alone. Callers should not depend on this
// succeeding — gh failure inside the sandbox is the real signal and
// the user can fix manually.
//
// The pre-fix filesystem-path origin is preserved as a sibling remote
// called "host" so round-trip workflows (`git push host`, `git fetch
// host`) still work.
func fixupOrigin(clonePath, projectRoot string) {
	hostUpstream, err := readOrigin(projectRoot)
	if err != nil || hostUpstream == "" {
		return
	}
	cloneOrigin, err := readOrigin(clonePath)
	if err != nil {
		return
	}
	if cloneOrigin == hostUpstream {
		// Already migrated; nothing to do.
		return
	}
	if cloneOrigin != projectRoot {
		// Origin was set to something else (user customization?).
		// Leave it alone rather than surprise them.
		return
	}
	// Origin still points at the host project's filesystem path —
	// migrate it. Rename the existing origin to "host" first so the
	// filesystem path stays reachable as a sibling remote.
	_ = runGit(clonePath, "remote", "rename", "origin", "host")
	_ = runGit(clonePath, "remote", "add", "origin", hostUpstream)
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
