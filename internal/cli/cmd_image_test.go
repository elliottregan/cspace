package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v2 "github.com/elliottregan/cspace/internal/compose/v2"
	"github.com/elliottregan/cspace/internal/devcontainer"
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

// fakeInspector returns a canned inspect result for any image.
func fakeInspector(version string, hasLabel, present bool) imageInspector {
	return func(string) (string, bool, bool) { return version, hasLabel, present }
}

// recordingBuilder counts local image builds and remembers the tag.
func recordingBuilder(builds *int, tag *string) func(string) error {
	return func(t string) error { *builds++; *tag = t; return nil }
}

// TestEnsureSandboxImageMissingBuildsWithoutAsking — an absent image is not a
// preference. There is nothing to boot from, so cspace up builds it even when
// it cannot prompt (overlay up, CI, piped stdin), rather than failing the boot
// with "image not found" as it used to.
func TestEnsureSandboxImageMissingBuildsWithoutAsking(t *testing.T) {
	var builds int
	var tag string
	cmd := &cobra.Command{}
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})

	acted, err := ensureSandboxImage(cmd, "cspace:latest", "1.0.0-rc.46", false, false,
		fakeInspector("", false, false), recordingBuilder(&builds, &tag))
	if err != nil {
		t.Fatalf("ensureSandboxImage: %v", err)
	}
	if !acted || builds != 1 || tag != "cspace:latest" {
		t.Errorf("acted=%v builds=%d tag=%q, want acted=true builds=1 tag=cspace:latest", acted, builds, tag)
	}
}

// TestEnsureSandboxImageCurrentIsNoop keeps the common boot free of any
// registry or builder work.
func TestEnsureSandboxImageCurrentIsNoop(t *testing.T) {
	var builds int
	var tag string
	cmd := &cobra.Command{}
	cmd.SetErr(&bytes.Buffer{})

	acted, err := ensureSandboxImage(cmd, "cspace:latest", "1.0.0-rc.46", false, true,
		fakeInspector("1.0.0-rc.46", true, true), recordingBuilder(&builds, &tag))
	if err != nil || acted || builds != 0 {
		t.Errorf("acted=%v err=%v builds=%d, want a no-op", acted, err, builds)
	}
}

// TestEnsureSandboxImageStaleWithoutPromptWarnsOnly preserves the existing
// non-blocking contract: a stale-but-usable image boots, with a warning naming
// how to refresh it. Silently updating an image mid-boot when the user cannot
// answer would change what they are running without consent.
func TestEnsureSandboxImageStaleWithoutPromptWarnsOnly(t *testing.T) {
	var builds int
	var tag string
	var errBuf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&errBuf)

	acted, err := ensureSandboxImage(cmd, "cspace:latest", "1.0.0-rc.46", false, false,
		fakeInspector("1.0.0-rc.45", true, true), recordingBuilder(&builds, &tag))
	if err != nil || acted || builds != 0 {
		t.Errorf("acted=%v err=%v builds=%d, want warn-only", acted, err, builds)
	}
	if !strings.Contains(errBuf.String(), "1.0.0-rc.45") {
		t.Errorf("warning does not name the stale version:\n%s", errBuf.String())
	}
}

// TestEnsureSandboxImageStaleDeclinedKeepsImage — answering no at the prompt
// keeps the escape hatch that lets a user boot a deliberately older image.
func TestEnsureSandboxImageStaleDeclinedKeepsImage(t *testing.T) {
	var builds int
	var tag string
	cmd := &cobra.Command{}
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(strings.NewReader("n\n"))

	acted, err := ensureSandboxImage(cmd, "cspace:latest", "1.0.0-rc.46", false, true,
		fakeInspector("1.0.0-rc.45", true, true), recordingBuilder(&builds, &tag))
	if err != nil || acted || builds != 0 {
		t.Errorf("acted=%v err=%v builds=%d, want the image left alone", acted, err, builds)
	}
}

// TestEnsureSandboxImageStaleAcceptedRebuilds — the default answer refreshes.
func TestEnsureSandboxImageStaleAcceptedRebuilds(t *testing.T) {
	var builds int
	var tag string
	cmd := &cobra.Command{}
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(strings.NewReader("\n")) // empty line takes the default

	acted, err := ensureSandboxImage(cmd, "cspace:latest", "1.0.0-rc.46", false, true,
		fakeInspector("1.0.0-rc.45", true, true), recordingBuilder(&builds, &tag))
	if err != nil {
		t.Fatalf("ensureSandboxImage: %v", err)
	}
	if !acted || builds != 1 {
		t.Errorf("acted=%v builds=%d, want a rebuild", acted, builds)
	}
}

// TestEnsureSandboxImageRebuildFlagForcesBuild — `--rebuild` skips the prompt
// and rebuilds, the escape hatch for testing local Dockerfile or supervisor
// changes.
func TestEnsureSandboxImageRebuildFlagForcesBuild(t *testing.T) {
	var builds int
	var tag string
	cmd := &cobra.Command{}
	cmd.SetErr(&bytes.Buffer{})

	acted, err := ensureSandboxImage(cmd, "cspace:latest", "1.0.0-rc.46", true, false,
		fakeInspector("1.0.0-rc.45", true, true), recordingBuilder(&builds, &tag))
	if err != nil {
		t.Fatalf("ensureSandboxImage: %v", err)
	}
	if !acted || builds != 1 {
		t.Errorf("acted=%v builds=%d, want a local build", acted, builds)
	}
}

// TestEnsureSandboxImageDevBuildsWhenMissing — a dev/dirty CLI builds like any
// other; the version only decides whether the label reads as stale.
func TestEnsureSandboxImageDevBuildsWhenMissing(t *testing.T) {
	var builds int
	var tag string
	cmd := &cobra.Command{}
	cmd.SetErr(&bytes.Buffer{})

	acted, err := ensureSandboxImage(cmd, "cspace:latest", "v1.0.0-rc.46-2-gabc1234", false, false,
		fakeInspector("", false, false), recordingBuilder(&builds, &tag))
	if err != nil {
		t.Fatalf("ensureSandboxImage: %v", err)
	}
	if !acted || builds != 1 {
		t.Errorf("acted=%v builds=%d, want a local build", acted, builds)
	}
}

// TestPlannedImage covers the pure half of image resolution: what a project
// pins directly, versus what would make cspace build one. Kept separate from
// resolveSandboxImage because that one can *build* a project image, so it
// cannot be called twice — and the boot needs a cheap answer before the
// overlay starts.
func TestPlannedImage(t *testing.T) {
	t.Run("compose service image wins", func(t *testing.T) {
		plan := &devcontainer.Plan{
			Compose: &v2.Project{Services: map[string]*v2.Service{"app": {Name: "app", Image: "node:24"}}},
			Service: "app",
		}
		img, needsBuild := plannedImage(plan)
		if img != "node:24" || needsBuild {
			t.Errorf("got (%q, %v), want (node:24, false)", img, needsBuild)
		}
	})

	t.Run("devcontainer image field", func(t *testing.T) {
		plan := &devcontainer.Plan{Devcontainer: &devcontainer.Config{Image: "custom:tag"}}
		img, needsBuild := plannedImage(plan)
		if img != "custom:tag" || needsBuild {
			t.Errorf("got (%q, %v), want (custom:tag, false)", img, needsBuild)
		}
	})

	t.Run("dockerfile means cspace builds one", func(t *testing.T) {
		plan := &devcontainer.Plan{Devcontainer: &devcontainer.Config{DockerFile: "Dockerfile"}}
		img, needsBuild := plannedImage(plan)
		if img != "" || !needsBuild {
			t.Errorf("got (%q, %v), want (\"\", true)", img, needsBuild)
		}
	})

	t.Run("nothing pinned means the default image", func(t *testing.T) {
		img, needsBuild := plannedImage(&devcontainer.Plan{Devcontainer: &devcontainer.Config{}})
		if img != "" || needsBuild {
			t.Errorf("got (%q, %v), want (\"\", false)", img, needsBuild)
		}
	})

	t.Run("nil plan", func(t *testing.T) {
		if img, needsBuild := plannedImage(nil); img != "" || needsBuild {
			t.Errorf("got (%q, %v), want (\"\", false)", img, needsBuild)
		}
	})
}

// TestPreflightImageGateBuildsForDefaultImage — the whole point of the
// pre-flight: it runs before overlay.Start, where stdin is still free, so the
// stale-image question can actually be asked. After overlay.Start bubbletea
// holds stdin in raw mode and the gate could only ever warn.
func TestPreflightImageGateBuildsForDefaultImage(t *testing.T) {
	var builds int
	var tag string
	cmd := &cobra.Command{}
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})

	// An empty project root: no devcontainer.json, so the boot uses
	// cspace:latest and a missing image must be built.
	acted, err := preflightImageGate(cmd, t.TempDir(), false, false,
		fakeInspector("", false, false), recordingBuilder(&builds, &tag))
	if err != nil {
		t.Fatalf("preflightImageGate: %v", err)
	}
	if !acted || builds != 1 || tag != "cspace:latest" {
		t.Errorf("acted=%v builds=%d tag=%q, want a build of cspace:latest", acted, builds, tag)
	}
}

// TestPreflightImageGateSkipsProjectOwnedImages — a project that pins its own
// image never boots cspace:latest, so nagging about its version would be noise.
func TestPreflightImageGateSkipsProjectOwnedImages(t *testing.T) {
	for _, tc := range []struct {
		name string
		json string
	}{
		{"image field", `{"name":"x","image":"ghcr.io/acme/dev:1"}`},
		{"dockerfile", `{"name":"x","build":{"dockerfile":"Dockerfile"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dcDir := filepath.Join(root, ".devcontainer")
			if err := os.MkdirAll(dcDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(tc.json), 0o644); err != nil {
				t.Fatal(err)
			}

			var builds int
			var tag string
			cmd := &cobra.Command{}
			cmd.SetErr(&bytes.Buffer{})

			acted, err := preflightImageGate(cmd, root, false, false,
				fakeInspector("", false, false), recordingBuilder(&builds, &tag))
			if err != nil {
				t.Fatalf("preflightImageGate: %v", err)
			}
			if acted || builds != 0 {
				t.Errorf("acted=%v builds=%d, want the gate skipped for a project-owned image", acted, builds)
			}
		})
	}
}

// TestIsTerminalRejectsNonTTYCharDevices — /dev/null is a character device, so
// the old os.ModeCharDevice test called it a terminal. Every non-interactive
// caller runs with stdin </dev/null, and there the stale-image gate must warn
// and boot, not "ask", read EOF, take its default and rebuild for ten minutes.
func TestIsTerminalRejectsNonTTYCharDevices(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = devNull.Close() }()

	regular, err := os.Create(filepath.Join(t.TempDir(), "piped"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = regular.Close() }()

	for _, tc := range []struct {
		name string
		f    *os.File
	}{
		{os.DevNull, devNull},
		{"regular file", regular},
		{"nil", nil},
	} {
		if isTerminal(tc.f) {
			t.Errorf("isTerminal(%s) = true, want false", tc.name)
		}
	}
}
