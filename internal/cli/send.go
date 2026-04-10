package cli

import (
	"github.com/elliottregan/cspace/internal/supervisor"
	"github.com/spf13/cobra"
)

func newSendCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "send <instance> <text>",
		Short:   "Inject a user turn into a session",
		GroupID: "supervisor",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return supervisor.RunDispatch(cfg, "send", args[0], args[1])
		},
	}
}
