package cli

import "github.com/spf13/cobra"

func newUpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "up [name|branch]",
		Short:   "Create/reconnect instance and launch Claude",
		Long:    `Create or reconnect to a devcontainer instance, then launch Claude Code.`,
		GroupID: "instance",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errNotImplemented("up")
		},
	}

	cmd.Flags().Bool("no-claude", false, "Create instance without launching Claude")
	cmd.Flags().String("prompt", "", "Inline prompt text for autonomous agent")
	cmd.Flags().String("prompt-file", "", "Path to a prompt file for autonomous agent")
	cmd.Flags().String("base", "", "Override base branch")

	return cmd
}
