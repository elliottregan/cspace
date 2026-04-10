package cli

import (
	"fmt"

	"github.com/elliottregan/cspace/internal/supervisor"
	"github.com/spf13/cobra"
)

func newSendCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "send <instance> <text>",
		Short:   "Inject a user turn into a session",
		GroupID: "supervisor",
		Args:    cobra.ExactArgs(2),
		RunE:    runSend,
	}
}

func runSend(cmd *cobra.Command, args []string) error {
	target, err := supervisor.ResolveDispatchTarget(cfg)
	if err != nil {
		return err
	}

	out, err := supervisor.DispatchWithOutput(target, "send", args[0], args[1])
	if err != nil {
		return fmt.Errorf("send failed: %w", err)
	}
	if out != "" {
		fmt.Println(out)
	}
	return nil
}
