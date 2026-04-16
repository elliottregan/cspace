package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// newMemoryCmd is the parent `cspace memory` command. It has no run logic
// of its own — its only purpose is to namespace subcommands (migrate, and
// potentially more over time).
func newMemoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "memory",
		Short:   "Manage project-shared Claude memory",
		GroupID: "setup",
	}
	cmd.AddCommand(newMemoryMigrateCmd())
	return cmd
}

func newMemoryMigrateCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Migrate legacy Docker-volume memory into .cspace/memory/",
		Long: `Copy Claude agent memory out of the legacy per-project Docker volume
(cspace-<project>-memory) into the repo's .cspace/memory/ directory, so it
gets bind-mounted and committed to git instead of living in a volume that
can be wiped.

Idempotent: refuses to overwrite an already-populated .cspace/memory/.
Safe to re-run. Use --dry-run to see what would be copied.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMemoryMigrate(dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print what would be copied without changing anything")
	return cmd
}

func runMemoryMigrate(dryRun bool) error {
	volume := cfg.MemoryVolume()
	target := filepath.Join(cfg.ProjectRoot, ".cspace", "memory")

	// Check the legacy volume exists. If not, there's nothing to migrate.
	if !dockerVolumeExists(volume) {
		fmt.Printf("No legacy volume %q found — nothing to migrate.\n", volume)
		fmt.Printf("New memory will persist to %s automatically on the next `cspace up`.\n", target)
		return nil
	}

	// Check target directory state. Refuse to overwrite if non-empty
	// (except for MEMORY.md's default stub, which provision writes).
	existing, err := nonStubEntries(target)
	if err != nil {
		return fmt.Errorf("inspecting %s: %w", target, err)
	}
	if len(existing) > 0 {
		return fmt.Errorf("%s already contains %d item(s); refusing to overwrite (move or delete them first)", target, len(existing))
	}

	if dryRun {
		fmt.Printf("Would copy contents of Docker volume %q → %s\n\n", volume, target)
		fmt.Println("Volume contents:")
		if err := runDockerVolumeLs(volume); err != nil {
			return err
		}
		return nil
	}

	// Ensure target exists with the right ownership.
	if err := os.MkdirAll(target, 0755); err != nil {
		return fmt.Errorf("creating %s: %w", target, err)
	}

	fmt.Printf("Copying %q → %s ...\n", volume, target)
	if err := runDockerVolumeCopy(volume, target); err != nil {
		return fmt.Errorf("copying volume contents: %w", err)
	}
	fmt.Println("Done.")
	fmt.Println()
	fmt.Println("Commit the new files with:")
	fmt.Println("  git add .cspace/memory && git commit -m 'Persist agent memory to repo'")
	fmt.Println()
	fmt.Printf("The legacy Docker volume %q is still present; remove it with:\n", volume)
	fmt.Printf("  docker volume rm %s\n", volume)
	return nil
}

// dockerVolumeExists returns true if a Docker volume with this name exists.
func dockerVolumeExists(name string) bool {
	out, err := exec.Command("docker", "volume", "inspect", "--format", "{{.Name}}", name).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == name
}

// nonStubEntries lists files in dir that aren't the default MEMORY.md stub
// written by provision. Returns names (not paths). An empty or missing
// directory is treated as empty. The stub is identified by exact content
// match so user-edited MEMORY.md files count as "non-stub."
func nonStubEntries(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.Name() == "MEMORY.md" && isStubMemory(filepath.Join(dir, e.Name())) {
			continue
		}
		out = append(out, e.Name())
	}
	return out, nil
}

// isStubMemory returns true if the file at path is the default MEMORY.md
// stub written by provision.ensureMemoryDir. Any edit (even whitespace)
// disqualifies it, matching the cautious "don't overwrite user edits" rule.
func isStubMemory(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	// Match the first line of the stub written by provision.ensureMemoryDir.
	// We don't import the const to avoid pulling provision into cli; the
	// marker line is stable and project-local.
	return strings.HasPrefix(string(data), "<!--\nThis directory holds project-shared Claude Code memory.")
}

// runDockerVolumeCopy copies contents of a Docker volume into a host path
// by invoking alpine via `docker run`. Uses `cp -a /src/. /dst/` so hidden
// files and attributes are preserved and the target dir is not replaced.
func runDockerVolumeCopy(volume, hostDst string) error {
	absDst, err := filepath.Abs(hostDst)
	if err != nil {
		return err
	}
	cmd := exec.Command("docker", "run", "--rm",
		"-v", volume+":/src:ro",
		"-v", absDst+":/dst",
		"alpine",
		"sh", "-c", "cp -a /src/. /dst/",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runDockerVolumeLs prints the top-level contents of a Docker volume.
func runDockerVolumeLs(volume string) error {
	cmd := exec.Command("docker", "run", "--rm",
		"-v", volume+":/src:ro",
		"alpine",
		"sh", "-c", "ls -la /src",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
