// Package cli implements the cspace command-line interface using cobra.
package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/elliottregan/cspace/internal/assets"
	"github.com/elliottregan/cspace/internal/config"
	"github.com/elliottregan/cspace/internal/sandboxmode"
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
			case "version", "help", "completion", "self-update":
				return nil
			}

			// In-sandbox: env vars (CSPACE_PROJECT, CSPACE_SANDBOX_NAME,
			// CSPACE_REGISTRY_URL) carry project context. Skip the host-style
			// git-repo / .cspace.json discovery so commands like
			// `cspace send` work even when /workspace isn't a git repo at
			// the cspace level.
			if sandboxmode.IsInSandbox() {
				return nil
			}

			// For the root command (no subcommand), attempt config loading
			// but tolerate failure — the TUI falls back to help when cfg is
			// nil. `cspace doctor` is informational and runnable from any
			// directory; the per-credential probes degrade gracefully when
			// cfg is nil (no project secrets file is checked).
			tolerateErr := (cmd.Name() == "cspace" && cmd.Parent() == nil) || cmd.Name() == "doctor"

			if err := loadConfig(); err != nil {
				if tolerateErr {
					return nil
				}
				return err
			}

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// No subcommand given — print help.
			return cmd.Help()
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Register all subcommands
	root.AddCommand(
		// Instance Management
		newUpCmd(),
		newDownCmd(),
		newAttachCmd(),
		newPortsCmd(),

		// Supervisor
		newSendCmd(),

		// Project Setup
		newKeychainCmd(),

		// Other
		newImageCmd(),
		newDoctorCmd(),
		newSelfUpdateCmd(),
		newVersionCmd(),
		newCompletionCmd(),
		newRegistryCmd(),
		newDaemonCmd(),
		newDnsCmd(),
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
