package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
	"github.com/spf13/cobra"
)

func newPrototypeDownCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prototype-down <name>",
		Short: "P0: stop and remove a prototype sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			project := projectName()

			parent := cmd.Context()
			if parent == nil {
				parent = context.Background()
			}
			ctx, cancel := context.WithTimeout(parent, 30*time.Second)
			defer cancel()

			a := applecontainer.New()
			_ = a.Stop(ctx, fmt.Sprintf("cspace-%s-%s", project, name))

			path, _ := registry.DefaultPath()
			r := &registry.Registry{Path: path}
			_ = r.Unregister(project, name)

			fmt.Fprintf(cmd.OutOrStdout(), "sandbox %s down\n", name)
			return nil
		},
	}
}
