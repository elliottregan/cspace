package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// newSessionsCmd is the parent `cspace sessions` command.
func newSessionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "sessions",
		Short:   "Manage project-shared Claude session history",
		GroupID: "setup",
	}
	cmd.AddCommand(newSessionsMigrateCmd())
	return cmd
}

func newSessionsMigrateCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Copy sessions out of legacy claude-home volumes into ~/.cspace/sessions/<project>/",
		Long: `Before the shared-sessions bind mount, each cspace instance kept its
session JSONL files in a per-instance Docker volume (cs-<project>-<instance>_claude-home).
Those transcripts were wiped by 'cspace down' and invisible to siblings.

This command finds all such volumes for the current project, copies their
session JSONL files into ~/.cspace/sessions/<project-name>/, and leaves
the volumes alone (you can remove them yourself after verifying). The
target directory is populated only with JSONL files (not the memory
subdir, which already lives in the repo at .cspace/memory/).

Idempotent: skips files that already exist at the destination. Use
--dry-run to preview.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessionsMigrate(dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print what would be copied without changing anything")
	return cmd
}

func runSessionsMigrate(dryRun bool) error {
	target := cfg.SessionsDir()
	volumes, err := findLegacyClaudeHomeVolumes(cfg.Project.Name)
	if err != nil {
		return fmt.Errorf("listing legacy volumes: %w", err)
	}
	if len(volumes) == 0 {
		fmt.Printf("No legacy claude-home volumes found for project %q — nothing to migrate.\n", cfg.Project.Name)
		fmt.Printf("New sessions will persist to %s automatically on the next `cspace up`.\n", target)
		return nil
	}

	if dryRun {
		fmt.Printf("Would migrate %d legacy claude-home volume(s) → %s\n\n", len(volumes), target)
		for _, v := range volumes {
			fmt.Printf("-- %s --\n", v)
			if err := runDockerVolumeLsSessions(v); err != nil {
				fmt.Fprintf(os.Stderr, "  (could not list: %v)\n", err)
			}
		}
		return nil
	}

	if err := os.MkdirAll(target, 0755); err != nil {
		return fmt.Errorf("creating %s: %w", target, err)
	}

	var copied, skipped int
	for _, v := range volumes {
		c, s, err := copyClaudeHomeSessions(v, target)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s: %v\n", v, err)
			continue
		}
		copied += c
		skipped += s
		fmt.Printf("%s → %d copied, %d skipped\n", v, c, s)
	}

	fmt.Printf("\nMigrated %d session file(s) across %d volume(s) (%d already present).\n",
		copied, len(volumes), skipped)
	fmt.Printf("Target directory: %s\n", target)
	if copied > 0 {
		fmt.Println()
		fmt.Println("Session files are now committed-in-home (not git-tracked). You can remove")
		fmt.Println("the legacy volumes with:")
		for _, v := range volumes {
			fmt.Printf("  docker volume rm %s\n", v)
		}
	}
	return nil
}

// findLegacyClaudeHomeVolumes returns Docker volume names matching the
// per-instance claude-home pattern for the given project:
// cs-<project>-<instance>_claude-home. Volumes without that exact suffix
// are skipped.
func findLegacyClaudeHomeVolumes(project string) ([]string, error) {
	out, err := exec.Command("docker", "volume", "ls", "--format", "{{.Name}}").Output()
	if err != nil {
		return nil, fmt.Errorf("docker volume ls: %w", err)
	}
	prefix := "cs-" + project + "-"
	const suffix = "_claude-home"
	var matched []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, suffix) {
			matched = append(matched, name)
		}
	}
	return matched, nil
}

// copyClaudeHomeSessions copies .jsonl files out of a legacy claude-home
// volume at /projects/-workspace/ into dst. Returns counts of (copied,
// skipped). Skips files that already exist at dst (idempotent). Uses an
// ephemeral alpine container with a shell script; no sensitivity to the
// subdirectory-per-session structure (we only copy top-level JSONL).
func copyClaudeHomeSessions(volume, dst string) (int, int, error) {
	absDst, err := filepath.Abs(dst)
	if err != nil {
		return 0, 0, err
	}
	// `-n` on cp means "no clobber" → skip if dst exists. The shell script
	// counts copies vs skips by checking existence before invoking cp.
	script := `
set -e
src=/src/projects/-workspace
[ -d "$src" ] || { echo "c=0 s=0"; exit 0; }
c=0; s=0
for f in "$src"/*.jsonl; do
  [ -e "$f" ] || continue
  base=$(basename "$f")
  if [ -e "/dst/$base" ]; then
    s=$((s+1))
  else
    cp -a "$f" "/dst/$base"
    c=$((c+1))
  fi
done
echo "c=$c s=$s"
`
	cmd := exec.Command("docker", "run", "--rm",
		"-v", volume+":/src:ro",
		"-v", absDst+":/dst",
		"alpine",
		"sh", "-c", script,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, 0, fmt.Errorf("docker run failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	// Parse the final "c=N s=M" line; other output is informational.
	c, s := 0, 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(line, "c=") {
			fmt.Sscanf(line, "c=%d s=%d", &c, &s)
		}
	}
	return c, s, nil
}

// runDockerVolumeLsSessions prints a short listing of session JSONL files
// in a legacy claude-home volume, for --dry-run.
func runDockerVolumeLsSessions(volume string) error {
	cmd := exec.Command("docker", "run", "--rm",
		"-v", volume+":/src:ro",
		"alpine",
		"sh", "-c", "ls -la /src/projects/-workspace/*.jsonl 2>/dev/null || echo '  (no session files)'",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
