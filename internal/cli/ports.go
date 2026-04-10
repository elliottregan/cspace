package cli

import (
	"github.com/elliottregan/cspace/internal/instance"
	"github.com/spf13/cobra"
)

func newPortsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ports <name>",
		Short:   "Show port mappings for an instance",
		GroupID: "instance",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			composeName := cfg.ComposeName(name)

			if err := instance.RequireRunning(composeName, name); err != nil {
				return err
			}

			instance.ShowPorts(name, cfg)
			return nil
		},
	}
}
