package cli

import (
	"github.com/elliottregan/cspace/internal/supervisor"
	"github.com/spf13/cobra"
)

func newRespondCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "respond <instance> <qid> <text>",
		Short:             "Reply to an agent's question",
		GroupID:           "supervisor",
		Args:              cobra.ExactArgs(3),
		ValidArgsFunction: completeInstanceNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			return supervisor.RunDispatch(cfg, "respond", args[0], args[1], args[2])
		},
	}
}
