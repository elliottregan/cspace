package cli

import (
	"fmt"

	"github.com/elliottregan/cspace/internal/supervisor"
	"github.com/spf13/cobra"
)

func newAskCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ask [instance]",
		Short:   "List pending agent questions",
		GroupID: "supervisor",
		Args:    cobra.MaximumNArgs(1),
		RunE:    runAsk,
	}
}

func runAsk(cmd *cobra.Command, args []string) error {
	target, err := supervisor.ResolveDispatchTarget(cfg)
	if err != nil {
		return err
	}

	// Build dispatch args: "list" followed by optional instance
	dispatchArgs := []string{"list"}
	dispatchArgs = append(dispatchArgs, args...)

	out, err := supervisor.DispatchWithOutput(target, dispatchArgs...)
	if err != nil {
		return fmt.Errorf("ask failed: %w", err)
	}
	if out != "" {
		fmt.Println(out)
	}
	return nil
}
