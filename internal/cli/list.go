package cli

import "github.com/spf13/cobra"

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List running instances for this project",
		Aliases: []string{"ls"},
		GroupID: "instance",
		RunE: func(cmd *cobra.Command, args []string) error {
			return errNotImplemented("list")
		},
	}

	cmd.Flags().Bool("all", false, "List instances across ALL projects")

	return cmd
}
