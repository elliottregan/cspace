package cli

import "github.com/spf13/cobra"

func newWarmCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "warm <name...>",
		Short:   "Pre-provision one or more containers",
		GroupID: "instance",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errNotImplemented("warm")
		},
	}
}
