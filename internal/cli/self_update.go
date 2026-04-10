package cli

import "github.com/spf13/cobra"

func newSelfUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "self-update",
		Short:   "Update cspace to latest version",
		GroupID: "other",
		RunE: func(cmd *cobra.Command, args []string) error {
			return errNotImplemented("self-update")
		},
	}
}
