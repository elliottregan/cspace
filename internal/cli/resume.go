package cli

import (
	"fmt"

	"github.com/elliottregan/cspace/internal/instance"
	"github.com/elliottregan/cspace/internal/supervisor"
	"github.com/spf13/cobra"
)

func newResumeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resume <instance> [session-id]",
		Short: "Resume a Claude session in a running instance",
		Long: `Reconnect to a running instance and launch Claude Code interactively.
If a session-id is provided, Claude will attempt to resume that session.`,
		GroupID:           "agents",
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: completeInstanceNames,
		RunE:              runResume,
	}

	cmd.Flags().String("prompt", "", "Inline prompt text for autonomous agent")
	cmd.Flags().String("prompt-file", "", "Path to a prompt file for autonomous agent")

	return cmd
}

func runResume(cmd *cobra.Command, args []string) error {
	name := args[0]
	prompt, _ := cmd.Flags().GetString("prompt")
	promptFile, _ := cmd.Flags().GetString("prompt-file")

	composeName := cfg.ComposeName(name)
	if err := instance.RequireRunning(composeName, name); err != nil {
		return err
	}

	if prompt != "" || promptFile != "" {
		if promptFile != "" {
			if err := supervisor.StagePromptFile(composeName, promptFile, supervisor.ContainerPromptPath); err != nil {
				return err
			}
		} else {
			if err := supervisor.StagePromptText(composeName, prompt, supervisor.ContainerPromptPath); err != nil {
				return err
			}
		}

		return supervisor.LaunchSupervisor(supervisor.LaunchParams{
			Name:       name,
			Role:       supervisor.RoleAgent,
			PromptFile: supervisor.ContainerPromptPath,
			StderrLog:  supervisor.ContainerAgentStderrLog,
		}, cfg)
	}

	// No prompt — launch interactive Claude session
	fmt.Printf("Resuming interactive session on %s...\n", name)
	return supervisor.LaunchInteractive(name, cfg)
}
