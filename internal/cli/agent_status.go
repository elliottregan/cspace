package cli

import (
	"github.com/elliottregan/cspace/internal/supervisor"
	"github.com/spf13/cobra"
)

func newAgentStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "agent-status <instance>",
		Short:   "Show supervisor status JSON",
		GroupID: "supervisor",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return supervisor.RunDispatch(cfg, "status", args[0])
		},
	}
}
