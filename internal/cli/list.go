package cli

import (
	"fmt"

	"github.com/elliottregan/cspace/internal/instance"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List running instances for this project",
		Aliases: []string{"ls"},
		GroupID: "instance",
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")

			if all {
				return listAll()
			}
			return listProject()
		},
	}

	cmd.Flags().Bool("all", false, "List instances across ALL projects")

	return cmd
}

func listAll() error {
	details, err := instance.GetAllInstanceDetails()
	if err != nil {
		return err
	}

	if len(details) == 0 {
		fmt.Println("(no instances found across any project)")
		return nil
	}

	fmt.Printf("%-16s %-20s %-30s %s\n", "INSTANCE", "PROJECT", "BRANCH", "AGE")
	fmt.Printf("%-16s %-20s %-30s %s\n", "--------", "-------", "------", "---")
	for _, d := range details {
		fmt.Printf("%-16s %-20s %-30s %s\n", d.Name, d.Project, d.Branch, d.Age)
	}
	fmt.Println()
	fmt.Println("Run 'cspace ports <name>' for port mappings.")
	return nil
}

func listProject() error {
	details, err := instance.GetInstanceDetails(cfg)
	if err != nil {
		return err
	}

	if len(details) == 0 {
		fmt.Println("(no instances found for this project \u2014 try 'cspace list --all')")
		return nil
	}

	fmt.Printf("%-20s %-30s %s\n", "INSTANCE", "BRANCH", "AGE")
	fmt.Printf("%-20s %-30s %s\n", "--------", "------", "---")
	for _, d := range details {
		fmt.Printf("%-20s %-30s %s\n", d.Name, d.Branch, d.Age)
	}
	fmt.Println()
	fmt.Println("Run 'cspace ports <name>' for port mappings.")
	return nil
}
