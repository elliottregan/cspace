package cli

import (
	"fmt"
	"os"

	"github.com/elliottregan/cspace/internal/instance"
	"github.com/elliottregan/cspace/internal/provision"
	"github.com/spf13/cobra"
)

func newWarmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "warm <name...>",
		Short: "Pre-provision one or more containers",
		Long: `Pre-provision one or more devcontainer instances without launching Claude.
Each named instance is created and configured (workspace, firewall, plugins).
Useful for warming up containers before they're needed.`,
		GroupID: "instance",
		Args:    cobra.MinimumNArgs(1),
		RunE:    runWarm,
	}
}

type warmResult struct {
	Name   string
	Status string
	Detail string
}

func runWarm(cmd *cobra.Command, args []string) error {
	fmt.Printf("Warming %d containers...\n\n", len(args))

	var results []warmResult
	failed := 0

	for _, name := range args {
		fmt.Printf("--- Setting up %s ---\n", name)

		_, err := provision.Run(provision.Params{
			Name:   name,
			Branch: "",
			Cfg:    cfg,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: setup failed for %s: %v\n", name, err)
			results = append(results, warmResult{Name: name, Status: "FAILED", Detail: "setup failed"})
			failed++
			continue
		}

		// Skip onboarding
		composeName := cfg.ComposeName(name)
		_ = instance.SkipOnboarding(composeName)

		// Validate firewall — re-init if not done
		if _, err := instance.DcExecRoot(composeName, "test", "-f", "/tmp/.firewall-init-done"); err != nil {
			fmt.Println("WARNING: Firewall not initialized — reinitializing...")
			if _, fwErr := instance.DcExecRoot(composeName, "init-firewall.sh"); fwErr != nil {
				results = append(results, warmResult{Name: name, Status: "FAILED", Detail: "firewall init failed"})
				failed++
				continue
			}
			_, _ = instance.DcExecRoot(composeName, "touch", "/tmp/.firewall-init-done")
		}

		results = append(results, warmResult{Name: name, Status: "ready"})
		fmt.Println()
	}

	// Print summary
	fmt.Println("=========================================")
	fmt.Printf("%-20s %-10s\n", "INSTANCE", "STATUS")
	fmt.Printf("%-20s %-10s\n", "--------", "------")
	for _, r := range results {
		fmt.Printf("%-20s %-10s\n", r.Name, r.Status)
	}

	if failed > 0 {
		return fmt.Errorf("%d container(s) failed validation", failed)
	}

	fmt.Printf("All %d containers ready.\n", len(args))
	return nil
}
