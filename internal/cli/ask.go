package cli

import (
	"github.com/elliottregan/cspace/internal/supervisor"
	"github.com/spf13/cobra"
)

func newAskCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ask [instance]",
		Short:   "List pending agent questions",
		GroupID: "supervisor",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dispatchArgs := append([]string{"list"}, args...)
			return supervisor.RunDispatch(cfg, dispatchArgs...)
		},
	}
}
