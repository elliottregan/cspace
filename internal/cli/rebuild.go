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
	"github.com/spf13/cobra"
)

func newRebuildCmd() *cobra.Command {
	return &cobra.Command{
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
  4. Downloading from GitHub Releases (for the current version)`,
		GroupID: "instance",
		RunE:    runRebuild,
	}
}

func runRebuild(cmd *cobra.Command, args []string) error {
	dockerfile, err := cfg.ResolveTemplate("Dockerfile")
	if err != nil {
		return fmt.Errorf("resolving Dockerfile: %w", err)
	}

	// Build context is CSPACE_HOME (parent of AssetsDir), matching
	// the context set in docker-compose.core.yml.
	cspaceHome := filepath.Dir(cfg.AssetsDir)

	// Ensure a Linux binary is available in the build context at bin/cspace.
	// The Dockerfile COPYs bin/cspace into /opt/cspace/bin/cspace.
	linuxBinPath := filepath.Join(cspaceHome, "bin", "cspace")
	if err := ensureLinuxBinary(linuxBinPath); err != nil {
		return fmt.Errorf("preparing Linux binary for container: %w", err)
	}

	fmt.Println("Rebuilding container image...")
	if err := docker.Build(cfg.ImageName(), dockerfile, cspaceHome, true); err != nil {
		return err
	}

	fmt.Printf("Image '%s' rebuilt.\n", cfg.ImageName())
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
// from GitHub Releases. Reuses the release API functions from self_update.go.
func downloadLinuxBinary(targetPath, arch, version string) error {
	// Fetch the release matching our version
	release, err := fetchLatestRelease(releaseRepo)
	if err != nil {
		return fmt.Errorf("fetching release info: %w", err)
	}

	// Look for the raw binary asset (produced by GoReleaser "binaries" archive)
	assetName := fmt.Sprintf("cspace-linux-%s", arch)
	var downloadURL string
	for _, asset := range release.Assets {
		if asset.Name == assetName {
			downloadURL = asset.BrowserDownloadURL
			break
		}
	}
	if downloadURL == "" {
		return fmt.Errorf("no asset named %s found in release %s", assetName, release.TagName)
	}

	// Download to a temp file then move into place
	tmpPath, err := downloadToTemp(downloadURL, filepath.Dir(targetPath))
	if err != nil {
		return fmt.Errorf("downloading %s: %w", assetName, err)
	}
	defer os.Remove(tmpPath) // clean up on error

	if err := os.Chmod(tmpPath, 0755); err != nil {
		return fmt.Errorf("setting permissions: %w", err)
	}

	if err := os.Rename(tmpPath, targetPath); err != nil {
		return fmt.Errorf("moving binary into place: %w", err)
	}

	fmt.Printf("Downloaded %s successfully.\n", assetName)
	return nil
}

// copyBinary copies a file from src to dst, preserving executable permissions.
func copyBinary(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening source: %w", err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("creating destination: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copying: %w", err)
	}

	return nil
}
