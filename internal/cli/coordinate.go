package cli

import "github.com/spf13/cobra"

func newCoordinateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "coordinate [instructions]",
		Short:   "Multi-task coordinator (reads coordinator.md playbook)",
		Long:    `Launch a multi-task coordinator agent that reads the coordinator.md playbook and orchestrates multiple agents.`,
		GroupID: "agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			return errNotImplemented("coordinate")
		},
	}

	cmd.Flags().String("prompt-file", "", "Load prompt from file instead of inline")
	cmd.Flags().String("name", "", "Use a specific instance name (resumable)")

	return cmd
}
