package cli

import "github.com/spf13/cobra"

func newAskCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ask [instance]",
		Short:   "List pending agent questions",
		GroupID: "supervisor",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errNotImplemented("ask")
		},
	}
}
