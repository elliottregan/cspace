package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/elliottregan/cspace/internal/docker"
	"github.com/elliottregan/cspace/internal/instance"
	"github.com/spf13/cobra"
)

func newRebuildCmd() *cobra.Command {
	var reindex bool
	cmd := &cobra.Command{
		Use:   "rebuild",
		Short: "Rebuild container image",
		Long: `Rebuild the cspace Docker image from scratch (--no-cache).
Uses the resolved Dockerfile (project override or built-in template)
with the cspace home directory as build context.

Before building, ensures a Linux binary is available in the build context.
The binary is obtained by (in order of preference):
  1. Copying the running binary if the host is already Linux
  2. Looking for a pre-built binary in dist/ or bin/
  3. Cross-compiling with the local Go toolchain
  4. Downloading from GitHub Releases (for the current version)

With --reindex, after the image rebuilds, every running instance of the
current project has its semantic search indexes refreshed in the
background. Useful as a single "I pulled new cspace, refresh everything"
step — no need to chase each instance down individually.`,
		GroupID: "instance",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRebuild(reindex)
		},
	}
	cmd.Flags().BoolVar(&reindex, "reindex", false, "after rebuild, refresh semantic search indexes in every running instance of this project")
	return cmd
}

func runRebuild(reindex bool) error {
	dockerfile, err := cfg.ResolveTemplate("Dockerfile")
	if err != nil {
		return fmt.Errorf("resolving Dockerfile: %w", err)
	}

	// Build context is CSPACE_HOME (parent of AssetsDir), matching
	// the context set in docker-compose.core.yml.
	cspaceHome := filepath.Dir(cfg.AssetsDir)

	// Stage the Linux binary at bin/cspace-linux (not bin/cspace) so it never
	// overwrites the host's installed darwin binary at ~/.cspace/bin/cspace.
	linuxBinPath := filepath.Join(cspaceHome, "bin", "cspace-linux")
	if err := ensureLinuxBinary(linuxBinPath); err != nil {
		return fmt.Errorf("preparing Linux binary for container: %w", err)
	}
	defer func() { _ = os.Remove(linuxBinPath) }()

	fmt.Println("Rebuilding container image...")
	if err := docker.Build(cfg.ImageName(), dockerfile, cspaceHome, Version, true); err != nil {
		return err
	}
	fmt.Printf("Image '%s' rebuilt.\n", cfg.ImageName())

	if reindex {
		return reindexRunningInstances()
	}
	return nil
}

// reindexRunningInstances fires `cspace search init --quiet` inside every
// running instance of the current project. Runs the in-container command
// in the background so a slow index doesn't block the rebuild command.
// Failures to dispatch on one instance don't stop the others — this is
// best-effort convenience, not a durable operation.
func reindexRunningInstances() error {
	names, err := instance.GetInstances(cfg)
	if err != nil {
		return fmt.Errorf("listing instances: %w", err)
	}
	if len(names) == 0 {
		fmt.Println("No running instances to reindex. Run `cspace up <name>` to provision one — new instances auto-index on provisioning.")
		return nil
	}

	fmt.Printf("Reindexing %d running instance(s)...\n", len(names))
	for _, name := range names {
		composeName := cfg.ComposeName(name)
		// Launch in the background inside the container so the host doesn't
		// wait on llama-server / qdrant throughput. Output goes to the same
		// log the lefthook hooks use.
		bg := `nohup cspace search init --quiet >> /workspace/.cspace/search-index.log 2>&1 &`
		if _, err := instance.DcExec(composeName, "bash", "-lc", bg); err != nil {
			fmt.Fprintf(os.Stderr, "  %s: dispatch failed (%v)\n", name, err)
			continue
		}
		fmt.Printf("  %s: reindexing in background (tail /workspace/.cspace/search-index.log)\n", name)
	}
	return nil
}

// ensureLinuxBinary places a static Linux binary at targetPath for the
// Docker image build. It tries multiple strategies in order of preference.
func ensureLinuxBinary(targetPath string) error {
	// Detect target architecture. Default to the host's architecture since
	// Docker builds for the host platform unless --platform is specified.
	targetArch := detectDockerArch()

	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return fmt.Errorf("creating bin directory: %w", err)
	}

	// Strategy 1: If host is already Linux with matching arch, copy self.
	if runtime.GOOS == "linux" && runtime.GOARCH == targetArch {
		execPath, err := os.Executable()
		if err == nil {
			execPath, err = filepath.EvalSymlinks(execPath)
			if err == nil {
				fmt.Println("Host is Linux — copying running binary to build context...")
				return copyBinary(execPath, targetPath)
			}
		}
	}

	// Strategy 2: Look for a pre-built Linux binary in common locations.
	// These are produced by `make build-linux` during development.
	binaryName := fmt.Sprintf("cspace-linux-%s", targetArch)
	searchPaths := []string{
		// dist/ directory (make build-linux output)
		filepath.Join(cfg.ProjectRoot, "dist", binaryName),
		// Legacy bin/ output names
		filepath.Join(cfg.ProjectRoot, "bin", fmt.Sprintf("cspace-go-linux-%s", targetArch)),
	}
	for _, p := range searchPaths {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			fmt.Printf("Found pre-built binary at %s\n", p)
			return copyBinary(p, targetPath)
		}
	}

	// Strategy 3: Cross-compile with local Go toolchain if available.
	if goPath, err := exec.LookPath("go"); err == nil {
		// Find Go module root (where go.mod lives) by walking up from project root
		modRoot := findGoModRoot(cfg.ProjectRoot)
		if modRoot != "" {
			fmt.Printf("Cross-compiling Linux/%s binary...\n", targetArch)
			ldflags := fmt.Sprintf("-s -w -X github.com/elliottregan/cspace/internal/cli.Version=%s", Version)
			cmd := exec.Command(goPath, "build",
				"-ldflags", ldflags,
				"-o", targetPath,
				"./cmd/cspace",
			)
			cmd.Dir = modRoot
			cmd.Env = append(os.Environ(),
				"GOOS=linux",
				"GOARCH="+targetArch,
				"CGO_ENABLED=0",
			)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err == nil {
				fmt.Println("Cross-compilation successful.")
				return nil
			}
			fmt.Fprintf(os.Stderr, "Cross-compilation failed, falling back to download...\n")
		}
	}

	// Strategy 4: Download from GitHub Releases.
	if Version != "" && Version != "dev" {
		fmt.Printf("Downloading Linux/%s binary (version %s)...\n", targetArch, Version)
		if err := downloadLinuxBinary(targetPath, targetArch, Version); err == nil {
			return nil
		} else {
			fmt.Fprintf(os.Stderr, "Download failed: %v\n", err)
		}
	}

	return fmt.Errorf("no Linux/%s binary available for container image\n\n"+
		"Options:\n"+
		"  1. Run 'make build-linux' from the cspace source directory\n"+
		"  2. Install Go and rebuild (cross-compilation will be automatic)\n"+
		"  3. Use a released version of cspace (enables download from GitHub Releases)",
		targetArch)
}

// detectDockerArch determines the target architecture for the Docker build.
// It checks DOCKER_DEFAULT_PLATFORM first, then falls back to the host arch.
func detectDockerArch() string {
	if platform := os.Getenv("DOCKER_DEFAULT_PLATFORM"); platform != "" {
		// DOCKER_DEFAULT_PLATFORM is like "linux/arm64" or "linux/amd64"
		parts := strings.Split(platform, "/")
		if len(parts) >= 2 {
			return parts[1]
		}
	}
	return runtime.GOARCH
}

// findGoModRoot walks up from the given directory looking for go.mod.
// Returns the directory containing go.mod, or "" if not found.
func findGoModRoot(dir string) string {
	current, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

// downloadLinuxBinary downloads a Linux binary for the given arch and version
// from GitHub Releases. Uses fetchReleaseByTag to get the exact version rather
// than always fetching latest. Reuses downloadReleaseAsset from self_update.go.
func downloadLinuxBinary(targetPath, arch, version string) error {
	// Normalize version tag (ensure "v" prefix)
	tag := version
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}

	// Fetch the release matching our exact version
	release, err := fetchReleaseByTag(releaseRepo, tag)
	if err != nil {
		// Fall back to latest if the exact tag isn't found
		release, err = fetchLatestRelease(releaseRepo)
		if err != nil {
			return fmt.Errorf("fetching release info: %w", err)
		}
	}

	assetName := fmt.Sprintf("cspace-linux-%s", arch)
	if err := downloadReleaseAsset(release, assetName, targetPath); err != nil {
		return err
	}

	fmt.Printf("Downloaded %s from %s.\n", assetName, release.TagName)
	return nil
}

// copyBinary atomically copies a file from src to dst with executable
// permissions. It writes to a temp file first, syncs, then renames into place
// to avoid leaving a truncated binary if the copy is interrupted.
func copyBinary(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening source: %w", err)
	}
	defer func() { _ = in.Close() }()

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".cspace-copy-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("copying: %w", err)
	}

	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("syncing: %w", err)
	}

	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("closing temp file: %w", err)
	}

	if err := os.Chmod(tmpPath, 0755); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("setting permissions: %w", err)
	}

	if err := os.Rename(tmpPath, dst); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("renaming into place: %w", err)
	}

	return nil
}
