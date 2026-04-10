package cli

import "github.com/spf13/cobra"

func newDownCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "down <name>",
		Short:   "Destroy instance and volumes",
		GroupID: "instance",
		RunE: func(cmd *cobra.Command, args []string) error {
			return errNotImplemented("down")
		},
	}

	cmd.Flags().Bool("all", false, "Destroy all instances for this project")
	cmd.Flags().Bool("everywhere", false, "Destroy ALL cspace instances across all projects")

	return cmd
}
