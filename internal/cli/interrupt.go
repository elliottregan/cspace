package cli

import (
	"fmt"

	"github.com/elliottregan/cspace/internal/supervisor"
	"github.com/spf13/cobra"
)

func newInterruptCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "interrupt <instance>",
		Short:   "Interrupt a running session",
		GroupID: "supervisor",
		Args:    cobra.ExactArgs(1),
		RunE:    runInterrupt,
	}
}

func runInterrupt(cmd *cobra.Command, args []string) error {
	target, err := supervisor.ResolveDispatchTarget(cfg)
	if err != nil {
		return err
	}

	out, err := supervisor.DispatchWithOutput(target, "interrupt", args[0])
	if err != nil {
		return fmt.Errorf("interrupt failed: %w", err)
	}
	if out != "" {
		fmt.Println(out)
	}
	return nil
}
