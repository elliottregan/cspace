package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

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
	// relative to the build context.
	//
	// NOTE: the embedded tree carries lib/ only — until issue #68
	// ships a published image, end users hitting this path will fail
	// at the COPY bin/cspace-linux-arm64 step. The error surfaces
	// directly from `container build`.
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

	_, _ = fmt.Fprintf(cmd.OutOrStdout(),
		"building %s from %s ...\n", tag, dockerfile)
	return runContainerBuild(cmd, tag, dockerfile, tmp, Version)
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
