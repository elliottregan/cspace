package cli

import (
	"fmt"

	"github.com/elliottregan/cspace/internal/supervisor"
	"github.com/spf13/cobra"
)

func newAgentStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "agent-status <instance>",
		Short:   "Show supervisor status JSON",
		GroupID: "supervisor",
		Args:    cobra.ExactArgs(1),
		RunE:    runAgentStatus,
	}
}

func runAgentStatus(cmd *cobra.Command, args []string) error {
	target, err := supervisor.ResolveDispatchTarget(cfg)
	if err != nil {
		return err
	}

	out, err := supervisor.DispatchWithOutput(target, "status", args[0])
	if err != nil {
		return fmt.Errorf("agent-status failed: %w", err)
	}
	if out != "" {
		fmt.Println(out)
	}
	return nil
}
