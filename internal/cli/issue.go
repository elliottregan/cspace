package cli

import "github.com/spf13/cobra"

func newIssueCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "issue <number>",
		Short:   "Run an agent against a GitHub issue",
		GroupID: "agents",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errNotImplemented("issue")
		},
	}
}
