package cli

import (
	"github.com/elliottregan/cspace/internal/supervisor"
	"github.com/spf13/cobra"
)

func newWatchCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "watch [instance]",
		Short:             "Stream agent notifications and questions",
		GroupID:           "supervisor",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeInstanceNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := supervisor.ResolveDispatchTarget(cfg)
			if err != nil {
				return err
			}
			dispatchArgs := append([]string{"watch"}, args...)
			return supervisor.DispatchInteractive(target, dispatchArgs...)
		},
	}
}
