package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestReleaseTag covers the version -> release tag normalization shared by the
// linux-binary fetch and the ghcr image pull. goreleaser strips the leading "v"
// when it stamps the binary ("1.0.0-rc.46") while the maintainer Makefile's
// `git describe` keeps it; both name the same tag. Anything that isn't exactly
// a tagged commit — dev builds, dirty trees, commits past the tag — has no
// published release and must not produce a URL that 404s later.
func TestReleaseTag(t *testing.T) {
	cases := []struct {
		name    string
		version string
		want    string
		wantOK  bool
	}{
		{"goreleaser version gains the v", "1.0.0-rc.46", "v1.0.0-rc.46", true},
		{"describe version keeps the v", "v1.0.0-rc.46", "v1.0.0-rc.46", true},
		{"dev build", "dev", "", false},
		{"empty version", "", "", false},
		{"dirty tree", "v1.0.0-rc.46-dirty", "", false},
		{"commits past the tag", "v1.0.0-rc.45-2-g5974463", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := releaseTag(tc.version)
			if ok != tc.wantOK || got != tc.want {
				t.Errorf("releaseTag(%q) = (%q, %v), want (%q, %v)",
					tc.version, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestPullImageRef pins the published image coordinates: one ghcr repository,
// tagged with the same tag as the GitHub release the CLI came from.
func TestPullImageRef(t *testing.T) {
	got, ok := pullImageRef("1.0.0-rc.46")
	if !ok {
		t.Fatal("pullImageRef on a release version returned ok=false")
	}
	if want := "ghcr.io/elliottregan/cspace:v1.0.0-rc.46"; got != want {
		t.Errorf("pullImageRef = %q, want %q", got, want)
	}
	if _, ok := pullImageRef("dev"); ok {
		t.Error("pullImageRef on a dev version returned ok=true; dev builds have no published image")
	}
}

// TestPlanImageRefresh covers what `cspace up` must do about cspace:latest
// before launching: nothing when the local image already matches the CLI, pull
// when a matching published image exists, build when it doesn't.
func TestPlanImageRefresh(t *testing.T) {
	cases := []struct {
		name       string
		present    bool
		imgVersion string
		hasLabel   bool
		cliVersion string
		pullable   bool
		want       imageAction
	}{
		{"missing image, release CLI", false, "", false, "1.0.0-rc.46", true, imageActionPull},
		{"missing image, dev CLI", false, "", false, "dev", false, imageActionBuild},
		{"current image", true, "1.0.0-rc.46", true, "1.0.0-rc.46", true, imageActionNone},
		{"current image across v prefix", true, "v1.0.0-rc.46", true, "1.0.0-rc.46", true, imageActionNone},
		{"stale image, release CLI", true, "1.0.0-rc.45", true, "1.0.0-rc.46", true, imageActionPull},
		{"stale image, dev CLI", true, "1.0.0-rc.45", true, "dev", false, imageActionBuild},
		{"unlabeled image, dev CLI", true, "", false, "dev", false, imageActionBuild},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := planImageRefresh(tc.present, tc.imgVersion, tc.hasLabel, tc.cliVersion, tc.pullable)
			if got != tc.want {
				t.Errorf("planImageRefresh(present=%v, img=%q, label=%v, cli=%q, pullable=%v) = %v, want %v",
					tc.present, tc.imgVersion, tc.hasLabel, tc.cliVersion, tc.pullable, got, tc.want)
			}
		})
	}
}

// TestApplyImageRefreshFallsBackToBuild is the offline path: a pull that fails
// (no network, ghcr down, image not published for this tag) must not abort the
// boot when the image can still be built locally.
func TestApplyImageRefreshFallsBackToBuild(t *testing.T) {
	var built string
	var log bytes.Buffer
	ops := imageOps{
		pull:  func(ref, localTag string) error { return errors.New("dial ghcr.io: no route to host") },
		build: func(tag string) error { built = tag; return nil },
	}

	acted, err := applyImageRefresh(imageActionPull, ops, "ghcr.io/elliottregan/cspace:v1.0.0-rc.46", "cspace:latest", &log)
	if err != nil {
		t.Fatalf("applyImageRefresh: %v", err)
	}
	if !acted {
		t.Error("acted = false, want true — the image was refreshed by the build fallback")
	}
	if built != "cspace:latest" {
		t.Errorf("build tag = %q, want cspace:latest", built)
	}
	if !strings.Contains(log.String(), "no route to host") {
		t.Errorf("log does not explain why the pull was abandoned:\n%s", log.String())
	}
}

// TestApplyImageRefreshPullSkipsBuild guards the happy path: a successful pull
// must not also run a multi-minute local build.
func TestApplyImageRefreshPullSkipsBuild(t *testing.T) {
	var pulledRef, pulledTag string
	var log bytes.Buffer
	ops := imageOps{
		pull:  func(ref, localTag string) error { pulledRef, pulledTag = ref, localTag; return nil },
		build: func(tag string) error { t.Fatalf("build called after a successful pull (tag %q)", tag); return nil },
	}

	acted, err := applyImageRefresh(imageActionPull, ops, "ghcr.io/elliottregan/cspace:v1.0.0-rc.46", "cspace:latest", &log)
	if err != nil {
		t.Fatalf("applyImageRefresh: %v", err)
	}
	if !acted {
		t.Error("acted = false, want true")
	}
	if pulledRef != "ghcr.io/elliottregan/cspace:v1.0.0-rc.46" || pulledTag != "cspace:latest" {
		t.Errorf("pull(%q, %q), want (ghcr.io/elliottregan/cspace:v1.0.0-rc.46, cspace:latest)", pulledRef, pulledTag)
	}
}

// TestApplyImageRefreshNoneIsNoop keeps the common case free of side effects.
func TestApplyImageRefreshNoneIsNoop(t *testing.T) {
	var log bytes.Buffer
	ops := imageOps{
		pull:  func(ref, localTag string) error { t.Fatal("pull called for imageActionNone"); return nil },
		build: func(tag string) error { t.Fatal("build called for imageActionNone"); return nil },
	}
	acted, err := applyImageRefresh(imageActionNone, ops, "", "cspace:latest", &log)
	if err != nil || acted {
		t.Errorf("applyImageRefresh(none) = (%v, %v), want (false, nil)", acted, err)
	}
	if log.Len() != 0 {
		t.Errorf("no-op wrote to the log: %s", log.String())
	}
}

// TestApplyImageRefreshReportsBuildFailure — when both paths fail there is no
// image to boot from, so the error must surface rather than be swallowed.
func TestApplyImageRefreshReportsBuildFailure(t *testing.T) {
	var log bytes.Buffer
	ops := imageOps{
		pull:  func(ref, localTag string) error { return errors.New("pull failed") },
		build: func(tag string) error { return errors.New("build failed") },
	}
	if _, err := applyImageRefresh(imageActionPull, ops, "ref", "cspace:latest", &log); err == nil {
		t.Fatal("applyImageRefresh returned nil error when both pull and build failed")
	}
}

// TestParseImageInspect reads a real `container image inspect` payload from
// Apple Container 1.3, so a change in that JSON shape fails here rather than
// silently making every image look unlabeled (which reads as "stale" and would
// re-pull on every boot).
func TestParseImageInspect(t *testing.T) {
	out, err := os.ReadFile(filepath.Join("testdata", "image-inspect-1.3.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	version, hasLabel, present := parseImageInspect(out)
	if !present {
		t.Error("present = false for a real inspect payload")
	}
	if !hasLabel || version != "1.0.0-rc.46" {
		t.Errorf("parseImageInspect = (%q, %v), want (1.0.0-rc.46, true)", version, hasLabel)
	}
}

// TestParseImageInspectDistinguishesAbsentFromUnlabeled is the distinction the
// refresh plan turns on: an absent image must be fetched without asking, while
// an unlabeled one is a stale local build the user is offered a chance to keep.
func TestParseImageInspectDistinguishesAbsentFromUnlabeled(t *testing.T) {
	if _, _, present := parseImageInspect([]byte("[]")); present {
		t.Error("present = true for an empty inspect result (missing image)")
	}
	if _, _, present := parseImageInspect(nil); present {
		t.Error("present = true for unparseable output")
	}
	unlabeled := []byte(`[{"variants":[{"config":{"config":{}}}]}]`)
	version, hasLabel, present := parseImageInspect(unlabeled)
	if !present {
		t.Error("present = false for an image that exists without a cspace.version label")
	}
	if hasLabel || version != "" {
		t.Errorf("parseImageInspect(unlabeled) = (%q, %v), want (\"\", false)", version, hasLabel)
	}
}

// TestImagePullCommands pins the two-step pull: fetch the published ref, then
// point cspace:latest at it. Without the tag step `cspace up` would keep
// pulling on every boot, since nothing local would carry the new version.
func TestImagePullCommands(t *testing.T) {
	got := imagePullCommands("ghcr.io/elliottregan/cspace:v1.0.0-rc.46", "cspace:latest")
	if len(got) != 2 {
		t.Fatalf("got %d commands, want 2: %v", len(got), got)
	}
	pull := strings.Join(got[0], " ")
	if !strings.HasPrefix(pull, "image pull ") || !strings.HasSuffix(pull, "ghcr.io/elliottregan/cspace:v1.0.0-rc.46") {
		t.Errorf("pull command = %q", pull)
	}
	if !strings.Contains(pull, "--platform linux/arm64") {
		t.Errorf("pull command does not pin the arm64 platform: %q", pull)
	}
	if want := "image tag ghcr.io/elliottregan/cspace:v1.0.0-rc.46 cspace:latest"; strings.Join(got[1], " ") != want {
		t.Errorf("tag command = %q, want %q", strings.Join(got[1], " "), want)
	}
}

// fakeInspector returns a canned inspect result for any image.
func fakeInspector(version string, hasLabel, present bool) imageInspector {
	return func(string) (string, bool, bool) { return version, hasLabel, present }
}

// recordingOps records which refresh path ran.
func recordingOps(pulls, builds *int) imageOps {
	return imageOps{
		pull:  func(string, string) error { *pulls++; return nil },
		build: func(string) error { *builds++; return nil },
	}
}

// TestEnsureSandboxImageMissingPullsWithoutAsking — an absent image is not a
// preference. There is nothing to boot from, so cspace up fetches it even when
// it cannot prompt (overlay up, CI, piped stdin).
func TestEnsureSandboxImageMissingPullsWithoutAsking(t *testing.T) {
	var pulls, builds int
	cmd := &cobra.Command{}
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})

	acted, err := ensureSandboxImage(cmd, "cspace:latest", "1.0.0-rc.46", false, false,
		fakeInspector("", false, false), recordingOps(&pulls, &builds))
	if err != nil {
		t.Fatalf("ensureSandboxImage: %v", err)
	}
	if !acted || pulls != 1 || builds != 0 {
		t.Errorf("acted=%v pulls=%d builds=%d, want acted=true pulls=1 builds=0", acted, pulls, builds)
	}
}

// TestEnsureSandboxImageCurrentIsNoop keeps the common boot free of any
// registry or builder work.
func TestEnsureSandboxImageCurrentIsNoop(t *testing.T) {
	var pulls, builds int
	cmd := &cobra.Command{}
	cmd.SetErr(&bytes.Buffer{})

	acted, err := ensureSandboxImage(cmd, "cspace:latest", "1.0.0-rc.46", false, true,
		fakeInspector("1.0.0-rc.46", true, true), recordingOps(&pulls, &builds))
	if err != nil || acted || pulls+builds != 0 {
		t.Errorf("acted=%v err=%v pulls=%d builds=%d, want a no-op", acted, err, pulls, builds)
	}
}

// TestEnsureSandboxImageStaleWithoutPromptWarnsOnly preserves the existing
// non-blocking contract: a stale-but-usable image boots, with a warning naming
// how to refresh it. Silently updating an image mid-boot when the user cannot
// answer would change what they are running without consent.
func TestEnsureSandboxImageStaleWithoutPromptWarnsOnly(t *testing.T) {
	var pulls, builds int
	var errBuf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&errBuf)

	acted, err := ensureSandboxImage(cmd, "cspace:latest", "1.0.0-rc.46", false, false,
		fakeInspector("1.0.0-rc.45", true, true), recordingOps(&pulls, &builds))
	if err != nil || acted || pulls+builds != 0 {
		t.Errorf("acted=%v err=%v pulls=%d builds=%d, want warn-only", acted, err, pulls, builds)
	}
	if !strings.Contains(errBuf.String(), "1.0.0-rc.45") {
		t.Errorf("warning does not name the stale version:\n%s", errBuf.String())
	}
}

// TestEnsureSandboxImageStaleDeclinedKeepsImage — answering no at the prompt
// keeps the escape hatch that lets a user boot a deliberately older image.
func TestEnsureSandboxImageStaleDeclinedKeepsImage(t *testing.T) {
	var pulls, builds int
	cmd := &cobra.Command{}
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(strings.NewReader("n\n"))

	acted, err := ensureSandboxImage(cmd, "cspace:latest", "1.0.0-rc.46", false, true,
		fakeInspector("1.0.0-rc.45", true, true), recordingOps(&pulls, &builds))
	if err != nil || acted || pulls+builds != 0 {
		t.Errorf("acted=%v err=%v pulls=%d builds=%d, want the image left alone", acted, err, pulls, builds)
	}
}

// TestEnsureSandboxImageStaleAcceptedPulls — the default answer refreshes, and
// for a released CLI that means a pull rather than a multi-minute build.
func TestEnsureSandboxImageStaleAcceptedPulls(t *testing.T) {
	var pulls, builds int
	cmd := &cobra.Command{}
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(strings.NewReader("\n")) // empty line takes the default

	acted, err := ensureSandboxImage(cmd, "cspace:latest", "1.0.0-rc.46", false, true,
		fakeInspector("1.0.0-rc.45", true, true), recordingOps(&pulls, &builds))
	if err != nil {
		t.Fatalf("ensureSandboxImage: %v", err)
	}
	if !acted || pulls != 1 || builds != 0 {
		t.Errorf("acted=%v pulls=%d builds=%d, want a pull", acted, pulls, builds)
	}
}

// TestEnsureSandboxImageRebuildFlagForcesBuild — `--rebuild` means "build from
// source", not "get the published image"; it is the escape hatch for testing
// local Dockerfile or supervisor changes, which a pull would silently discard.
func TestEnsureSandboxImageRebuildFlagForcesBuild(t *testing.T) {
	var pulls, builds int
	cmd := &cobra.Command{}
	cmd.SetErr(&bytes.Buffer{})

	acted, err := ensureSandboxImage(cmd, "cspace:latest", "1.0.0-rc.46", true, false,
		fakeInspector("1.0.0-rc.45", true, true), recordingOps(&pulls, &builds))
	if err != nil {
		t.Fatalf("ensureSandboxImage: %v", err)
	}
	if !acted || builds != 1 || pulls != 0 {
		t.Errorf("acted=%v pulls=%d builds=%d, want a local build", acted, pulls, builds)
	}
}

// TestEnsureSandboxImageDevBuildsWhenMissing — a dev/dirty CLI has no published
// image, so the only way to get one is to build it.
func TestEnsureSandboxImageDevBuildsWhenMissing(t *testing.T) {
	var pulls, builds int
	cmd := &cobra.Command{}
	cmd.SetErr(&bytes.Buffer{})

	acted, err := ensureSandboxImage(cmd, "cspace:latest", "v1.0.0-rc.46-2-gabc1234", false, false,
		fakeInspector("", false, false), recordingOps(&pulls, &builds))
	if err != nil {
		t.Fatalf("ensureSandboxImage: %v", err)
	}
	if !acted || builds != 1 || pulls != 0 {
		t.Errorf("acted=%v pulls=%d builds=%d, want a local build", acted, pulls, builds)
	}
}

// TestImagePullCmdRefusesDevVersion — a dev or dirty build has no published
// image, and a registry 404 would be a confusing way to learn that. The error
// must name the command that does work.
func TestImagePullCmdRefusesDevVersion(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	Version = "dev"

	cmd := newImagePullCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("pull on a dev build returned nil error")
	}
	if !strings.Contains(err.Error(), "cspace image build") {
		t.Errorf("error does not point at the working alternative: %v", err)
	}
}
