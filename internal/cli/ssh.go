package cli

import "github.com/spf13/cobra"

func newSSHCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ssh <name>",
		Short:   "Shell into running instance",
		GroupID: "instance",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errNotImplemented("ssh")
		},
	}
}
