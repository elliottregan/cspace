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
		Long: `Released cspace versions publish a matching sandbox image to
` + ghcrRepository + `, and ` + "`cspace up`" + ` pulls it when the local image is
missing or built by a different cspace. ` + "`cspace image pull`" + ` does
that fetch on demand.

` + "`cspace image build`" + ` builds the image locally instead: it extracts
the embedded Dockerfile (plus the supervisor source, scripts, etc.)
to a temp dir and runs ` + "`container build`" + ` against it. That is the
only path for dev builds, which have no published image, and the
one to use when testing local Dockerfile or supervisor changes.
Idempotent — the extracted tree carries a .version marker so
repeat builds reuse the existing extraction.`,
	}
	parent.AddCommand(newImageBuildCmd())
	parent.AddCommand(newImagePullCmd())
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

func newImagePullCmd() *cobra.Command {
	var tag string
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Pull the published sandbox image matching this cspace version",
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, ok := pullImageRef(Version)
			if !ok {
				return fmt.Errorf("no published image for version %q (dev build, dirty tree, or commits past the tag). Build one instead: `cspace image build`", Version)
			}
			return runImagePull(cmd, ref, tag)
		},
	}
	cmd.Flags().StringVar(&tag, "tag", "cspace:latest",
		"local tag to point at the pulled image (default cspace:latest, which cspace up reads)")
	return cmd
}

// ghcrRepository is where `make release` publishes the sandbox image. Its tags
// are the release tags themselves (v1.0.0-rc.46), so a CLI knows the exact
// image that matches it without a lookup.
const ghcrRepository = "ghcr.io/elliottregan/cspace"

// describeSuffix matches `git describe`'s "-<commits>-g<sha>" tail, which marks
// a build made past the last tag — no release exists for it.
var describeSuffix = regexp.MustCompile(`-\d+-g[0-9a-f]+$`)

// releaseTag normalizes a CLI version into the git tag of the release it came
// from. goreleaser strips the leading "v" when stamping the binary
// ("1.0.0-rc.46") while the maintainer Makefile's `git describe` keeps it; both
// name the same tag. Returns ok=false for anything that isn't exactly a tagged
// commit — dev builds, dirty trees, commits past the tag — because no release
// (and so no published binary or image) exists for those.
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

// pullImageRef returns the published sandbox image matching this CLI, and
// whether one exists at all.
func pullImageRef(version string) (string, bool) {
	tag, ok := releaseTag(version)
	if !ok {
		return "", false
	}
	return ghcrRepository + ":" + tag, true
}

// imageAction is what `cspace up` must do about the default sandbox image
// before it can launch.
type imageAction int

const (
	imageActionNone imageAction = iota
	imageActionPull
	imageActionBuild
)

// planImageRefresh decides between leaving the local image alone, pulling the
// published one, and building from source. A released CLI pulls (seconds, and
// only the changed layers); a dev build has no published image and must build.
func planImageRefresh(present bool, imgVersion string, hasLabel bool, cliVersion string, pullable bool) imageAction {
	if present && !imageIsStale(imgVersion, hasLabel, cliVersion) {
		return imageActionNone
	}
	if pullable {
		return imageActionPull
	}
	return imageActionBuild
}

// imageOps are the two ways to obtain the sandbox image, injected so the
// refresh decision is testable without a container runtime.
type imageOps struct {
	pull  func(ref, localTag string) error
	build func(tag string) error
}

// applyImageRefresh carries out a planned refresh, falling back from pull to
// build so an unreachable registry never blocks a boot that could still build
// locally. Reports whether the image was actually touched.
func applyImageRefresh(action imageAction, ops imageOps, ref, localTag string, log io.Writer) (bool, error) {
	if action == imageActionNone {
		return false, nil
	}
	if action == imageActionPull {
		err := ops.pull(ref, localTag)
		if err == nil {
			return true, nil
		}
		_, _ = fmt.Fprintf(log,
			"[cspace] could not pull %s: %v\n[cspace] falling back to a local build of %s.\n",
			ref, err, localTag)
	}
	if err := ops.build(localTag); err != nil {
		return false, err
	}
	return true, nil
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

// imagePullCommands returns the argv pair that fetches a published image and
// points the local tag at it. The tag step is load-bearing: without it nothing
// local carries the new cspace.version, so every boot would pull again.
func imagePullCommands(ref, localTag string) [][]string {
	return [][]string{
		{"image", "pull", "--platform", "linux/arm64", ref},
		{"image", "tag", ref, localTag},
	}
}

// runImagePull fetches the published sandbox image and retags it locally.
func runImagePull(cmd *cobra.Command, ref, localTag string) error {
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "[cspace] pulling %s ...\n", ref)
	for _, argv := range imagePullCommands(ref, localTag) {
		c := exec.Command("container", argv...)
		c.Stdout, c.Stderr = cmd.OutOrStdout(), cmd.ErrOrStderr()
		if err := c.Run(); err != nil {
			return fmt.Errorf("container %s: %w", strings.Join(argv, " "), err)
		}
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "[cspace] %s is now %s.\n", localTag, ref)
	return nil
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
