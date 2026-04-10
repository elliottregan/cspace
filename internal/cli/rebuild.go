package cli

import (
	"fmt"
	"path/filepath"

	"github.com/elliottregan/cspace/internal/docker"
	"github.com/spf13/cobra"
)

func newRebuildCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rebuild",
		Short: "Rebuild container image",
		Long: `Rebuild the cspace Docker image from scratch (--no-cache).
Uses the resolved Dockerfile (project override or built-in template)
with the cspace home directory as build context.`,
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

	fmt.Println("Rebuilding container image...")
	if err := docker.Build(cfg.ImageName(), dockerfile, cspaceHome, true); err != nil {
		return err
	}

	fmt.Printf("Image '%s' rebuilt.\n", cfg.ImageName())
	return nil
}
