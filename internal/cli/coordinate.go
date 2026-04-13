package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/elliottregan/cspace/internal/instance"
	"github.com/elliottregan/cspace/internal/provision"
	"github.com/elliottregan/cspace/internal/supervisor"
	"github.com/spf13/cobra"
)

func newCoordinateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "coordinate [instructions]",
		Short: "Multi-task coordinator (reads coordinator.md playbook)",
		Long: `Launch a multi-task coordinator agent that reads the coordinator.md
playbook and orchestrates multiple agents.

The coordinator prompt is built from the playbook template plus user
instructions, either inline or from a file.`,
		GroupID: "agents",
		RunE:    runCoordinate,
	}

	cmd.Flags().String("prompt-file", "", "Load prompt from file instead of inline")
	cmd.Flags().String("name", "", "Use a specific instance name (resumable)")

	return cmd
}

func runCoordinate(cmd *cobra.Command, args []string) error {
	promptFile, _ := cmd.Flags().GetString("prompt-file")
	name, _ := cmd.Flags().GetString("name")

	var prompt string
	if len(args) > 0 {
		prompt = args[0]
	}

	if prompt == "" && promptFile == "" {
		return fmt.Errorf("usage: cspace coordinate \"<instructions>\" [--name <name>]\n       cspace coordinate --prompt-file <path> [--name <name>]")
	}
	if prompt != "" && promptFile != "" {
		return fmt.Errorf("pass either an inline prompt or --prompt-file, not both")
	}
	if promptFile != "" {
		if _, err := os.Stat(promptFile); err != nil {
			return fmt.Errorf("prompt file not found: %s", promptFile)
		}
	}

	return runCoordinateWithArgs(prompt, promptFile, name)
}

// runCoordinateWithArgs is the shared implementation for the coordinate command,
// callable from both the CLI handler and the TUI menu.
func runCoordinateWithArgs(prompt, promptFile, name string) error {
	if name == "" {
		name = "coord-" + strconv.FormatInt(time.Now().Unix(), 10)
	}

	_, err := provision.Run(provision.Params{
		Name: name,
		Cfg:  cfg,
	})
	if err != nil {
		return err
	}

	composeName := cfg.ComposeName(name)
	instance.SkipOnboarding(composeName)

	// Re-copy host .env so the coordinator inherits GH_TOKEN, etc.
	envFile := filepath.Join(cfg.ProjectRoot, ".env")
	if _, err := os.Stat(envFile); err == nil {
		instance.DcCp(composeName, envFile, "/workspace/.env")
		instance.DcExecRoot(composeName, "chown", "dev:dev", "/workspace/.env")
	}

	// Build the full coordinator prompt: playbook + user instructions
	playbookFile := cfg.ResolveAgent("coordinator.md")
	playbookBytes, err := os.ReadFile(playbookFile)
	if err != nil {
		return fmt.Errorf("reading coordinator playbook: %w", err)
	}

	var userBody string
	if promptFile != "" {
		bodyBytes, err := os.ReadFile(promptFile)
		if err != nil {
			return fmt.Errorf("reading prompt file: %w", err)
		}
		userBody = string(bodyBytes)
	} else {
		userBody = prompt
	}

	fullPrompt := string(playbookBytes) + "\n\nUSER INSTRUCTIONS:\n\n" + userBody

	if err := supervisor.StagePromptText(composeName, fullPrompt, supervisor.ContainerCoordPromptPath); err != nil {
		return err
	}

	return supervisor.LaunchSupervisor(supervisor.LaunchParams{
		Name:       name,
		Role:       supervisor.RoleCoordinator,
		PromptFile: supervisor.ContainerCoordPromptPath,
		StderrLog:  supervisor.ContainerCoordStderrLog,
	}, cfg)
}
