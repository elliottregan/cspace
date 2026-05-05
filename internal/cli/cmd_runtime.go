package cli

import (
	"fmt"

	"github.com/elliottregan/cspace/internal/runtime"
	"github.com/spf13/cobra"
)

func newRuntimeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runtime",
		Short: "Manage cspace runtime overlay versions",
	}
	cmd.AddCommand(newRuntimeListCmd())
	cmd.AddCommand(newRuntimePruneCmd())
	return cmd
}

func newRuntimeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List installed runtime overlay versions",
		RunE: func(c *cobra.Command, _ []string) error {
			versions, err := runtime.List()
			if err != nil {
				return err
			}
			for _, v := range versions {
				marker := " "
				if v == Version {
					marker = "*"
				}
				fmt.Fprintf(c.OutOrStdout(), "%s %s\n", marker, v)
			}
			return nil
		},
	}
}

func newRuntimePruneCmd() *cobra.Command {
	var keep int
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove old runtime overlay versions",
		RunE: func(c *cobra.Command, _ []string) error {
			if err := runtime.Prune(Version, keep); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "kept active=%s + %d previous\n", Version, keep)
			return nil
		},
	}
	cmd.Flags().IntVar(&keep, "keep", 1, "previous versions to keep")
	return cmd
}
