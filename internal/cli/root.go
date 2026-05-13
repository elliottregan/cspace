// Package cli implements the cspace command-line interface using cobra.
package cli

import (
	"fmt"
	"os"

	"github.com/elliottregan/cspace/internal/config"
	"github.com/elliottregan/cspace/internal/sandboxmode"
	"github.com/spf13/cobra"
)

var (
	// Version is set at build time via -ldflags.
	Version = "dev"

	// cfg holds the loaded configuration, available to all commands.
	cfg *config.Config
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

// loadConfig loads the project configuration. Library assets (Dockerfile,
// runtime scripts, defaults.json, planets.json) are read from the binary's
// embedded FS on demand — nothing is extracted to disk at startup.
func loadConfig() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	cfg, err = config.Load(cwd)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	return nil
}

