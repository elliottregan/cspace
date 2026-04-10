package cli

import "github.com/spf13/cobra"

func newInterruptCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "interrupt <instance>",
		Short:   "Interrupt a running session",
		GroupID: "supervisor",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errNotImplemented("interrupt")
		},
	}
}
