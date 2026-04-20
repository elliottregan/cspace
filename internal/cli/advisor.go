package cli

import (
	"fmt"

	"github.com/elliottregan/cspace/internal/advisor"
	"github.com/spf13/cobra"
)

func newAdvisorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "advisor",
		Short:   "Manage long-running advisor agents (decision-maker, etc.)",
		GroupID: "agents",
	}
	cmd.AddCommand(newAdvisorListCmd())
	cmd.AddCommand(newAdvisorDownCmd())
	cmd.AddCommand(newAdvisorRestartCmd())
	return cmd
}

func newAdvisorListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured advisors and their liveness",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(cfg.Advisors) == 0 {
				fmt.Println("No advisors configured. See `advisors` in defaults.json.")
				return nil
			}
			for _, name := range advisor.SortedAdvisorNames(cfg) {
				spec := cfg.Advisors[name]
				alive := advisor.IsAlive(cfg, name)
				status := "stopped"
				if alive {
					status = "alive"
				}
				fmt.Printf("%-20s model=%s effort=%s %s\n",
					name,
					fallback(spec.Model, "(default)"),
					fallback(spec.Effort, "(default)"),
					status,
				)
			}
			return nil
		},
	}
}

func newAdvisorDownCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "down [name]",
		Short: "Stop an advisor (or all with --all)",
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			if all {
				for _, name := range advisor.SortedAdvisorNames(cfg) {
					if err := advisor.Teardown(cfg, name); err != nil {
						fmt.Printf("WARN: %s: %v\n", name, err)
					} else {
						fmt.Printf("%s stopped.\n", name)
					}
				}
				return nil
			}
			if len(args) != 1 {
				return fmt.Errorf("usage: cspace advisor down <name> | --all")
			}
			return advisor.Teardown(cfg, args[0])
		},
	}
	cmd.Flags().Bool("all", false, "Tear down every configured advisor")
	return cmd
}

func newAdvisorRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart <name>",
		Short: "Tear down and relaunch an advisor with a fresh session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := advisor.Teardown(cfg, args[0]); err != nil {
				fmt.Printf("WARN during teardown: %v\n", err)
			}
			return advisor.Launch(cfg, args[0])
		},
	}
}

func fallback(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
