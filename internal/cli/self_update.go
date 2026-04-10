package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

func newSelfUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "self-update",
		Short: "Update cspace to the latest version",
		Long: `Check for a newer version of cspace on GitHub Releases and install it.

Downloads the correct binary for the current OS/architecture and
atomically replaces the running binary.`,
		GroupID: "other",
		RunE:    runSelfUpdate,
	}

	cmd.Flags().Bool("check", false, "Only check for updates, don't install")

	return cmd
}

// ghRelease represents a GitHub release from the API.
type ghRelease struct {
	TagName string    `json:"tag_name"`
	Name    string    `json:"name"`
	Body    string    `json:"body"`
	Assets  []ghAsset `json:"assets"`
}

// ghAsset represents a release asset.
type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

const releaseRepo = "elliottregan/cspace"

func runSelfUpdate(cmd *cobra.Command, args []string) error {
	checkOnly, _ := cmd.Flags().GetBool("check")

	fmt.Printf("Current version: %s\n", Version)

	// Fetch latest release
	release, err := fetchLatestRelease(releaseRepo)
	if err != nil {
		return fmt.Errorf("checking for updates: %w", err)
	}

	if release.TagName == "" {
		return fmt.Errorf("no releases found for %s", releaseRepo)
	}

	// Compare versions (strip leading "v" for comparison)
	currentClean := strings.TrimPrefix(Version, "v")
	latestClean := strings.TrimPrefix(release.TagName, "v")

	if currentClean == latestClean {
		fmt.Println("Already up to date.")
		return nil
	}

	fmt.Printf("New version available: %s\n", release.TagName)

	if release.Body != "" {
		// Show a brief summary of changes
		lines := strings.Split(release.Body, "\n")
		maxLines := 10
		if len(lines) < maxLines {
			maxLines = len(lines)
		}
		fmt.Println()
		for _, line := range lines[:maxLines] {
			fmt.Printf("  %s\n", line)
		}
		if len(lines) > maxLines {
			fmt.Printf("  ... (%d more lines)\n", len(lines)-maxLines)
		}
		fmt.Println()
	}

	if checkOnly {
		return nil
	}

	// Find matching asset for current platform
	assetName := fmt.Sprintf("cspace-go-%s-%s", runtime.GOOS, runtime.GOARCH)
	var downloadURL string
	for _, asset := range release.Assets {
		if asset.Name == assetName {
			downloadURL = asset.BrowserDownloadURL
			break
		}
	}
	if downloadURL == "" {
		// List available assets for debugging
		var available []string
		for _, a := range release.Assets {
			available = append(available, a.Name)
		}
		return fmt.Errorf("no release asset found for %s/%s (expected %s)\nAvailable assets: %s",
			runtime.GOOS, runtime.GOARCH, assetName, strings.Join(available, ", "))
	}

	// Get the current binary path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("determining binary path: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolving binary path: %w", err)
	}

	// Download to a temporary file in the same directory (for atomic rename)
	fmt.Printf("Downloading %s...\n", assetName)
	tmpPath, err := downloadToTemp(downloadURL, filepath.Dir(execPath))
	if err != nil {
		return fmt.Errorf("downloading update: %w", err)
	}
	defer os.Remove(tmpPath) // Clean up on error

	// Make executable
	if err := os.Chmod(tmpPath, 0755); err != nil {
		return fmt.Errorf("setting permissions: %w", err)
	}

	// Atomic replace
	if err := os.Rename(tmpPath, execPath); err != nil {
		return fmt.Errorf("replacing binary: %w\nDownloaded file is at: %s\nYou may need to run with sudo or manually move the file", err, tmpPath)
	}

	fmt.Printf("Updated to %s\n", release.TagName)
	return nil
}

// fetchLatestRelease queries the GitHub Releases API for the latest release.
// Uses net/http directly so it works without the gh CLI.
func fetchLatestRelease(repo string) (*ghRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)

	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("no releases found (HTTP 404)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}

	var release ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("parsing release JSON: %w", err)
	}

	return &release, nil
}

// downloadToTemp downloads a URL to a temporary file in the given directory.
// Returns the path to the temporary file.
func downloadToTemp(url, dir string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	f, err := os.CreateTemp(dir, "cspace-update-*")
	if err != nil {
		// Fall back to system temp dir if we can't write to the binary's directory
		f, err = os.CreateTemp("", "cspace-update-*")
		if err != nil {
			return "", err
		}
	}

	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}

	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}

	return f.Name(), nil
}
