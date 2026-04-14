package cli

import (
	"fmt"

	"github.com/elliottregan/cspace/internal/compose"
	"github.com/spf13/cobra"
)

// newStopCmd returns the `cspace stop <name>` command, which stops an
// instance's containers without removing its volumes. The containers can
// be restarted later via `cspace up <name>`, and workspace/logs/memory
// all survive.
//
// Contrast with `cspace down`, which also removes volumes.
func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "stop <name>",
		Short:             "Stop an instance's containers without destroying its volumes",
		GroupID:           "instance",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeInstanceNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := compose.Run(name, cfg, "stop"); err != nil {
				return err
			}
			fmt.Printf("Instance '%s' stopped.\n", name)
			return nil
		},
	}
}
