package cli

import "github.com/spf13/cobra"

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "init",
		Short:   "Scaffold .cspace.json and .cspace/ directory",
		GroupID: "setup",
		RunE: func(cmd *cobra.Command, args []string) error {
			return errNotImplemented("init")
		},
	}

	cmd.Flags().Bool("full", false, "Also copy all templates for customization")

	return cmd
}
