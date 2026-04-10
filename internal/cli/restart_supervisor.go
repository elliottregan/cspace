package cli

import "github.com/spf13/cobra"

func newRestartSupervisorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "restart-supervisor <instance>",
		Short:   "Restart agent supervisor (preserves workspace)",
		GroupID: "supervisor",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errNotImplemented("restart-supervisor")
		},
	}

	cmd.Flags().String("reason", "", "Why the restart is needed")

	return cmd
}
