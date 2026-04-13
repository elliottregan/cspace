package cli

import (
	"github.com/elliottregan/cspace/internal/instance"
	"github.com/spf13/cobra"
)

func newSSHCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "ssh <name>",
		Short:             "Shell into running instance",
		GroupID:           "instance",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeInstanceNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			composeName := cfg.ComposeName(name)

			if err := instance.RequireRunning(composeName, name); err != nil {
				return err
			}

			return instance.DcExecInteractive(composeName, "bash")
		},
	}
}
