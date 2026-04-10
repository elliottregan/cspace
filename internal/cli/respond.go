package cli

import "github.com/spf13/cobra"

func newRespondCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "respond <instance> <qid> <text>",
		Short:   "Reply to an agent's question",
		GroupID: "supervisor",
		Args:    cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errNotImplemented("respond")
		},
	}
}
