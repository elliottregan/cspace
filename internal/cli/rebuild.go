package cli

import "github.com/spf13/cobra"

func newRebuildCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "rebuild",
		Short:   "Rebuild container image",
		GroupID: "instance",
		RunE: func(cmd *cobra.Command, args []string) error {
			return errNotImplemented("rebuild")
		},
	}
}
