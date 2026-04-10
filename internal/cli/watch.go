package cli

import "github.com/spf13/cobra"

func newWatchCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "watch [instance]",
		Short:   "Stream agent notifications and questions",
		GroupID: "supervisor",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errNotImplemented("watch")
		},
	}
}
