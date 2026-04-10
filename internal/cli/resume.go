package cli

import "github.com/spf13/cobra"

func newResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "resume <instance>",
		Short:   "Resume a stopped instance",
		GroupID: "agents",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errNotImplemented("resume")
		},
	}
}
