package cli

import (
	"fmt"

	"github.com/elliottregan/cspace/internal/supervisor"
	"github.com/spf13/cobra"
)

func newRespondCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "respond <instance> <qid> <text>",
		Short:   "Reply to an agent's question",
		GroupID: "supervisor",
		Args:    cobra.ExactArgs(3),
		RunE:    runRespond,
	}
}

func runRespond(cmd *cobra.Command, args []string) error {
	target, err := supervisor.ResolveDispatchTarget(cfg)
	if err != nil {
		return err
	}

	out, err := supervisor.DispatchWithOutput(target, "respond", args[0], args[1], args[2])
	if err != nil {
		return fmt.Errorf("respond failed: %w", err)
	}
	if out != "" {
		fmt.Println(out)
	}
	return nil
}
