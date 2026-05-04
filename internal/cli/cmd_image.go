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
	// Extract the embedded library tree into a temp dir. The extracted
	// path layout matches the source repo's lib/ directory, so the
	// COPY paths inside the Dockerfile (lib/templates/Dockerfile,
	// lib/scripts/cspace-entrypoint.sh, …) resolve relative to the
	// build context.
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

	// Build context is the temp root (parent of lib/) so COPY
	// directives like `COPY lib/scripts/cspace-entrypoint.sh ...`
	// resolve correctly. The arm64 platform is hard-coded for now —
	// Apple Container is arm64-only on Apple Silicon, and that's the
	// only substrate cspace supports today.
	_, _ = fmt.Fprintf(cmd.OutOrStdout(),
		"building %s from %s ...\n", tag, dockerfile)
	build := exec.Command("container", "build",
		"--platform", "linux/arm64",
		"--tag", tag,
		"--file", dockerfile,
		tmp,
	)
	build.Stdin, build.Stdout, build.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("container build: %w", err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(),
		"built %s. Run `cspace up` to launch a sandbox.\n", tag)
	return nil
}
