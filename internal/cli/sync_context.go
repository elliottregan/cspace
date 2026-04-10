package cli

import "github.com/spf13/cobra"

func newSyncContextCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "sync-context",
		Short:   "Generate milestone context doc",
		GroupID: "other",
		RunE: func(cmd *cobra.Command, args []string) error {
			return errNotImplemented("sync-context")
		},
	}
}
