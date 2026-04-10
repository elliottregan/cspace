// Package cli implements the cspace command-line interface using cobra.
package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/elliottregan/cspace/internal/assets"
	"github.com/elliottregan/cspace/internal/config"
	"github.com/spf13/cobra"
)

var (
	// Version is set at build time via -ldflags.
	Version = "dev"

	// cfg holds the loaded configuration, available to all commands.
	cfg *config.Config

	// assetsDir holds the path to extracted embedded assets.
	assetsDir string
)

// NewRootCmd creates the root cspace command with all subcommands registered.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "cspace",
		Short: "Portable CLI for managing Claude Code devcontainer instances",
		Long: `cspace — Portable CLI for managing Claude Code devcontainer instances

Spin up Docker containers with independent workspaces, browser sidecars,
and network firewalls, then run autonomous Claude agents against GitHub issues.`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Skip config/asset loading for commands that don't need a project context
			switch cmd.Name() {
			case "version", "help", "completion", "init", "self-update":
				return nil
			}

			// For the root command (no subcommand), attempt config loading
			// but tolerate failure — the TUI falls back to help when cfg is nil.
			tolerateErr := cmd.Name() == "cspace" && cmd.Parent() == nil

			if err := loadConfig(); err != nil {
				if tolerateErr {
					return nil
				}
				return err
			}

			// Commands that modify or create instances require an initialized project
			switch cmd.Name() {
			case "up", "coordinate", "issue", "warm", "rebuild":
				if !cfg.IsInitialized() {
					return fmt.Errorf("no .cspace.json found in %s\nRun 'cspace init' first", cfg.ProjectRoot)
				}
			}

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// No subcommand given — launch TUI if interactive and config loaded
			if cfg != nil && isInteractive() {
				return runTUI(cmd)
			}
			return cmd.Help()
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Define command groups for organized help output
	root.AddGroup(
		&cobra.Group{ID: "instance", Title: "Instance Management:"},
		&cobra.Group{ID: "agents", Title: "Autonomous Agents:"},
		&cobra.Group{ID: "supervisor", Title: "Supervisor:"},
		&cobra.Group{ID: "setup", Title: "Project Setup:"},
		&cobra.Group{ID: "other", Title: "Other:"},
	)

	// Register all subcommands
	root.AddCommand(
		// Instance Management
		newUpCmd(),
		newDownCmd(),
		newSSHCmd(),
		newListCmd(),
		newPortsCmd(),
		newWarmCmd(),
		newRebuildCmd(),

		// Autonomous Agents
		newCoordinateCmd(),
		newIssueCmd(),
		newResumeCmd(),

		// Supervisor
		newSendCmd(),
		newRespondCmd(),
		newAskCmd(),
		newWatchCmd(),
		newInterruptCmd(),
		newAgentStatusCmd(),
		newRestartSupervisorCmd(),

		// Project Setup
		newInitCmd(),

		// Other
		newSyncContextCmd(),
		newSelfUpdateCmd(),
		newVersionCmd(),
		newCompletionCmd(),
	)

	return root
}

// Execute runs the root command.
func Execute() error {
	return NewRootCmd().Execute()
}

// loadConfig extracts embedded assets and loads the project configuration.
func loadConfig() error {
	cspaceHome, err := defaultCspaceHome()
	if err != nil {
		return fmt.Errorf("determining cspace home: %w", err)
	}

	assetsDir, err = assets.ExtractTo(cspaceHome, Version)
	if err != nil {
		return fmt.Errorf("extracting assets: %w", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	cfg, err = config.Load(cwd, assetsDir)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	return nil
}

// defaultCspaceHome returns the default path for cspace data.
// Uses $CSPACE_HOME if set, otherwise ~/.cspace.
func defaultCspaceHome() (string, error) {
	if home := os.Getenv("CSPACE_HOME"); home != "" {
		return home, nil
	}

	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(userHome, ".cspace"), nil
}

// errNotImplemented returns a formatted error for stub commands.
func errNotImplemented(name string) error {
	return fmt.Errorf("cspace %s is not yet implemented in the Go CLI; use the bash version", name)
}
