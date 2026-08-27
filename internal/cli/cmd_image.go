package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/elliottregan/cspace/internal/assets"
	"github.com/spf13/cobra"
)

func newImageCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "image",
		Short: "Manage the cspace sandbox image (cspace:latest)",
		Long: `The cspace sandbox image is built locally on each host; there is
no published image to pull (see the ghcr push finding — Apple
Container's push to ghcr.io fails, and publishing was dropped
rather than worked around).

` + "`cspace image build`" + ` extracts the embedded Dockerfile (plus the
supervisor source, scripts, etc.) to a temp dir and runs
` + "`container build`" + ` against it. ` + "`cspace up`" + ` runs it for you when the
image is missing, and offers to when it was built by a different
cspace version. Idempotent — the extracted tree carries a .version
marker so repeat builds reuse the existing extraction.`,
	}
	parent.AddCommand(newImageBuildCmd())
	return parent
}

func newImageBuildCmd() *cobra.Command {
	var tag string
	var noCache bool
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build cspace:latest from the embedded Dockerfile + library",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runImageBuild(cmd, tag, noCache)
		},
	}
	cmd.Flags().StringVar(&tag, "tag", "cspace:latest",
		"image tag to build (default cspace:latest, which cspace up reads)")
	cmd.Flags().BoolVar(&noCache, "no-cache", false,
		"build with no layer cache — re-fetches apt packages and the latest Claude Code CLI for a fully fresh image")
	return cmd
}

// describeSuffix matches `git describe`'s "-<commits>-g<sha>" tail, which marks
// a build made past the last tag — no release exists for it.
var describeSuffix = regexp.MustCompile(`-\d+-g[0-9a-f]+$`)

// releaseTag normalizes a CLI version into the git tag of the release it came
// from. goreleaser strips the leading "v" when stamping the binary
// ("1.0.0-rc.46") while the maintainer Makefile's `git describe` keeps it; both
// name the same tag. Returns ok=false for anything that isn't exactly a tagged
// commit — dev builds, dirty trees, commits past the tag — because no release
// (and so no published linux binary) exists for those.
func releaseTag(version string) (string, bool) {
	if version == "" || version == "dev" ||
		strings.Contains(version, "-dirty") || describeSuffix.MatchString(version) {
		return "", false
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	return version, true
}

// parseImageInspect pulls the cspace.version label out of a
// `container image inspect` payload and reports whether the image exists at
// all. Apple Container returns a JSON array of image descriptors; each has
// variants[].config.config.Labels (the outer `config` is the OCI image config,
// the inner the runtime config holding Env / Cmd / Labels). Every key is walked
// defensively — a missing one means "no label", not "no image", and the two
// lead to different actions.
func parseImageInspect(out []byte) (version string, hasLabel bool, present bool) {
	var parsed []struct {
		Variants []struct {
			Config struct {
				Config struct {
					Labels map[string]string `json:"Labels"`
				} `json:"config"`
			} `json:"config"`
		} `json:"variants"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil || len(parsed) == 0 {
		return "", false, false
	}
	for _, img := range parsed {
		for _, v := range img.Variants {
			if val, ok := v.Config.Config.Labels["cspace.version"]; ok && val != "" {
				return val, true, true
			}
		}
	}
	return "", false, true
}

// inspectSandboxImage reports the named image's baked cspace.version and
// whether it is present locally. A failed inspect means absent — that is how
// Apple Container reports an unknown image (non-zero exit).
func inspectSandboxImage(image string) (version string, hasLabel bool, present bool) {
	out, err := exec.Command("container", "image", "inspect", image).Output()
	if err != nil {
		return "", false, false
	}
	return parseImageInspect(out)
}

func runImageBuild(cmd *cobra.Command, tag string, noCache bool) error {
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
			return runContainerBuild(cmd, tag, srcDockerfile, cwd, Version, noCache)
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
	return runContainerBuild(cmd, tag, dockerfile, tmp, Version, noCache)
}

// fetchReleaseBinary downloads cspace_linux_arm64.tar.gz from the GitHub
// release matching `version` and extracts the inner `cspace` binary to
// `dst` (mode 0755). Returns a clear error for dev/dirty versions where
// no release exists — those builds belong in the maintainer fast-path.
func fetchReleaseBinary(cmd *cobra.Command, version, dst string) error {
	tag, ok := releaseTag(version)
	if !ok {
		return fmt.Errorf("no published release for version %q. Build from the cspace source tree (cd into the cspace repo, run `make build`, then `cspace image build` from there) or tag and push a release first", version)
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
func runContainerBuild(cmd *cobra.Command, tag, dockerfile, ctxDir, version string, noCache bool) error {
	buildArgs := []string{"build",
		"--platform", "linux/arm64",
		"--tag", tag,
		"--file", dockerfile,
		"--build-arg", "CSPACE_VERSION=" + version,
	}
	if noCache {
		buildArgs = append(buildArgs, "--no-cache")
	}
	buildArgs = append(buildArgs, ctxDir) // context dir must stay last (positional)
	build := exec.Command("container", buildArgs...)
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
