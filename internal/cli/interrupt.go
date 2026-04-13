package cli

import (
	"github.com/elliottregan/cspace/internal/supervisor"
	"github.com/spf13/cobra"
)

func newInterruptCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "interrupt <instance>",
		Short:             "Interrupt a running session",
		GroupID:           "supervisor",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeInstanceNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			return supervisor.RunDispatch(cfg, "interrupt", args[0])
		},
	}
}
