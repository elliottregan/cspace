package cli

import "github.com/spf13/cobra"

func newPortsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ports <name>",
		Short:   "Show port mappings for an instance",
		GroupID: "instance",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errNotImplemented("ports")
		},
	}
}
