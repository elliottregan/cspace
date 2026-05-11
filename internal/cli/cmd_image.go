package cli

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/elliottregan/cspace/internal/assets"
	"github.com/spf13/cobra"
)

func newImageCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "image",
		Short: "Manage the cspace sandbox image (cspace:latest)",
		Long: `Until v1 ships a published image at ghcr.io (issue #68), the cspace
sandbox image must be built locally on each host before the first
` + "`cspace up`" + `. ` + "`cspace image build`" + ` extracts the embedded Dockerfile
(plus the supervisor source, scripts, planet visuals, etc.) to a
temp dir and runs ` + "`container build`" + ` against it. Idempotent — the
extracted tree carries a .version marker so repeat builds reuse the
existing extraction.`,
	}
	parent.AddCommand(newImageBuildCmd())
	return parent
}

func newImageBuildCmd() *cobra.Command {
	var tag string
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build cspace:latest from the embedded Dockerfile + library",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runImageBuild(cmd, tag)
		},
	}
	cmd.Flags().StringVar(&tag, "tag", "cspace:latest",
		"image tag to build (default cspace:latest, which cspace up reads)")
	return cmd
}

func runImageBuild(cmd *cobra.Command, tag string) error {
	// Maintainer fast-path: if the cwd looks like the cspace source
	// repo (has lib/templates/Dockerfile and bin/cspace-linux-arm64),
	// build directly against it. The Dockerfile's
	// `COPY bin/cspace-linux-arm64 /usr/local/bin/cspace` step needs
	// the freshly-cross-compiled linux/arm64 binary, which only exists
	// in a source checkout — the embedded assets are lib/ only.
	// Without this fast-path, maintainers hit a confusing "calculate
	// checksum of ref ... /bin/cspace-linux-arm64: not found" error
	// and have to fall back to `make cspace-image`.
	cwd, _ := os.Getwd()
	if cwd != "" {
		srcDockerfile := filepath.Join(cwd, "lib", "templates", "Dockerfile")
		srcBinary := filepath.Join(cwd, "bin", "cspace-linux-arm64")
		if statOK(srcDockerfile) && statOK(srcBinary) {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(),
				"building %s from source tree at %s ...\n", tag, cwd)
			return runContainerBuild(cmd, tag, srcDockerfile, cwd, Version)
		}
	}

	// Otherwise extract the embedded library tree into a temp dir and
	// build there. The extracted path layout matches the source
	// repo's lib/ directory so COPY paths inside the Dockerfile
	// (lib/templates/Dockerfile, lib/runtime/scripts/…) resolve
	// relative to the build context. The Dockerfile also needs
	// bin/cspace-linux-arm64; for brew installs that file isn't in
	// the embedded tree, so we fetch the matching release tarball
	// from GitHub and stage it into the build context.
	tmp, err := os.MkdirTemp("", "cspace-image-build-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	libRoot, err := assets.ExtractTo(tmp, Version)
	if err != nil {
		return fmt.Errorf("extract embedded assets: %w", err)
	}
	dockerfile := filepath.Join(libRoot, "templates", "Dockerfile")
	if _, err := os.Stat(dockerfile); err != nil {
		return fmt.Errorf("embedded Dockerfile missing at %s: %w", dockerfile, err)
	}

	// Stage the linux/arm64 host binary into <ctx>/bin/cspace-linux-arm64
	// so the Dockerfile's COPY step resolves. The maintainer fast-path
	// above already had this from the source tree; this path fetches
	// from the matching GitHub release.
	binDst := filepath.Join(tmp, "bin", "cspace-linux-arm64")
	if err := fetchReleaseBinary(cmd, Version, binDst); err != nil {
		return fmt.Errorf("fetch linux binary: %w", err)
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(),
		"building %s from %s ...\n", tag, dockerfile)
	return runContainerBuild(cmd, tag, dockerfile, tmp, Version)
}

// fetchReleaseBinary downloads cspace_linux_arm64.tar.gz from the GitHub
// release matching `version` and extracts the inner `cspace` binary to
// `dst` (mode 0755). Returns a clear error for dev/dirty versions where
// no release exists — those builds belong in the maintainer fast-path.
func fetchReleaseBinary(cmd *cobra.Command, version, dst string) error {
	if version == "dev" || version == "" || strings.Contains(version, "-dirty") {
		return fmt.Errorf("no published release for version %q. Build from the cspace source tree (cd into the cspace repo, run `make build`, then `cspace image build` from there) or tag and push a release first", version)
	}
	// goreleaser strips the leading "v" from {{ .Version }} when embedding the
	// version into the binary (-X .Version=1.0.0-rc.X), but the GitHub release
	// tag is `v1.0.0-rc.X`. The maintainer fast-path's `git describe` keeps
	// the `v`. Normalize so the URL matches the tag regardless of which build
	// path produced the running binary.
	tag := version
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	url := fmt.Sprintf("https://github.com/elliottregan/cspace/releases/download/%s/cspace_linux_arm64.tar.gz", tag)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "fetching %s ...\n", url)

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s (release tarball not published yet?)", url, resp.Status)
	}

	tarPath := dst + ".tar.gz"
	defer func() { _ = os.Remove(tarPath) }()
	tarFile, err := os.Create(tarPath)
	if err != nil {
		return err
	}
	if _, err := io.Copy(tarFile, resp.Body); err != nil {
		_ = tarFile.Close()
		return fmt.Errorf("write tarball: %w", err)
	}
	_ = tarFile.Close()

	// Extract just the `cspace` member into the same directory, then
	// rename to the Dockerfile-expected filename.
	tarExtract := exec.Command("tar", "-xzf", tarPath, "-C", filepath.Dir(dst), "cspace")
	tarExtract.Stderr = os.Stderr
	if err := tarExtract.Run(); err != nil {
		return fmt.Errorf("extract tarball: %w", err)
	}
	tmpExtracted := filepath.Join(filepath.Dir(dst), "cspace")
	if err := os.Rename(tmpExtracted, dst); err != nil {
		return fmt.Errorf("rename extracted binary: %w", err)
	}
	return os.Chmod(dst, 0o755)
}

// runContainerBuild invokes `container build --platform linux/arm64
// --tag <tag> --file <dockerfile> --build-arg CSPACE_VERSION=<ver> <ctxDir>`.
// The arm64 platform is hard-coded — Apple Container is arm64-only on Apple
// Silicon, and that's the only substrate cspace supports today. CSPACE_VERSION
// bakes into the image as the `cspace.version` label so `cspace up` can detect
// CLI/image drift.
func runContainerBuild(cmd *cobra.Command, tag, dockerfile, ctxDir, version string) error {
	build := exec.Command("container", "build",
		"--platform", "linux/arm64",
		"--tag", tag,
		"--file", dockerfile,
		"--build-arg", "CSPACE_VERSION="+version,
		ctxDir,
	)
	build.Stdin, build.Stdout, build.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("container build: %w", err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(),
		"built %s. Run `cspace up` to launch a sandbox.\n", tag)
	return nil
}

func statOK(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
