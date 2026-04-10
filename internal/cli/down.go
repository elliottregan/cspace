package cli

import (
	"bufio"
	"fmt"
	"os"

	"github.com/elliottregan/cspace/internal/compose"
	"github.com/elliottregan/cspace/internal/instance"
	"github.com/spf13/cobra"
)

func newDownCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "down <name>",
		Short:   "Destroy instance and volumes",
		GroupID: "instance",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			everywhere, _ := cmd.Flags().GetBool("everywhere")
			if everywhere {
				return downEverywhere()
			}

			allFlag, _ := cmd.Flags().GetBool("all")
			if allFlag {
				return downAll()
			}

			if len(args) == 0 {
				return fmt.Errorf("usage: cspace down <name> | --all | --everywhere")
			}

			name := args[0]
			if err := compose.Run(name, cfg, "down", "--volumes"); err != nil {
				return err
			}
			fmt.Printf("Instance '%s' removed.\n", name)
			return nil
		},
	}

	cmd.Flags().Bool("all", false, "Destroy all instances for this project")
	cmd.Flags().Bool("everywhere", false, "Destroy ALL cspace instances across all projects")

	return cmd
}

// downAll tears down all instances for the current project.
func downAll() error {
	names, err := instance.GetInstances(cfg)
	if err != nil {
		return err
	}

	if len(names) == 0 {
		fmt.Println("No instances found for this project.")
		return nil
	}

	for _, name := range names {
		fmt.Printf("Tearing down instance: %s\n", name)
		if err := compose.Run(name, cfg, "down", "--volumes"); err != nil {
			// Log but continue tearing down other instances
			fmt.Fprintf(os.Stderr, "  warning: %v\n", err)
		}
	}
	return nil
}

// downEverywhere tears down ALL cspace instances across all projects.
// Requires user confirmation before proceeding.
func downEverywhere() error {
	infos, err := instance.GetAllInstances()
	if err != nil {
		return err
	}

	if len(infos) == 0 {
		fmt.Println("No cspace instances running anywhere.")
		return nil
	}

	// Show what's about to be destroyed
	fmt.Println("About to tear down ALL cspace instances across ALL projects:")
	fmt.Println()
	fmt.Printf("  %-16s %-20s\n", "INSTANCE", "PROJECT")
	fmt.Printf("  %-16s %-20s\n", "--------", "-------")
	for _, info := range infos {
		project := info.Project
		if project == "" {
			project = "?"
		}
		fmt.Printf("  %-16s %-20s\n", info.ComposeName, project)
	}
	fmt.Println()

	// Prompt for confirmation
	fmt.Print("Type 'yes' to continue: ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() || scanner.Text() != "yes" {
		fmt.Println("Aborted.")
		return nil
	}

	// Tear down each instance using direct compose project name
	for _, info := range infos {
		fmt.Printf("Tearing down instance: %s\n", info.ComposeName)
		if err := compose.RunDirect(info.ComposeName, "down", "--volumes"); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: %v\n", err)
		}
	}

	fmt.Println("Done.")
	return nil
}
