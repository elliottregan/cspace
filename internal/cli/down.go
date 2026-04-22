package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/elliottregan/cspace/internal/compose"
	"github.com/elliottregan/cspace/internal/docker"
	"github.com/elliottregan/cspace/internal/instance"
	"github.com/spf13/cobra"
)

func newDownCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "down <name>",
		Short:             "Destroy instance and volumes",
		GroupID:           "instance",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeInstanceNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			everywhere, _ := cmd.Flags().GetBool("everywhere")
			if everywhere {
				return downEverywhere()
			}

			projectFlag, _ := cmd.Flags().GetBool("project")
			if projectFlag {
				return runDownProject()
			}

			allFlag, _ := cmd.Flags().GetBool("all")
			if allFlag {
				return runDownAll()
			}

			if len(args) == 0 {
				return fmt.Errorf("usage: cspace down <name> | --all | --project | --everywhere")
			}

			return runDownInstance(args[0])
		},
	}

	cmd.Flags().Bool("all", false, "Destroy all instances for this project")
	cmd.Flags().Bool("project", false, "Destroy all instances AND the shared search stack for this project")
	cmd.Flags().Bool("everywhere", false, "Destroy ALL cspace instances across all projects")

	return cmd
}

// runDownInstance tears down a single instance by name.
func runDownInstance(name string) error {
	if err := compose.Run(name, cfg, "down", "--volumes"); err != nil {
		return err
	}
	fmt.Printf("Instance '%s' removed.\n", name)
	return nil
}

// runDownAll tears down all instances for the current project.
func runDownAll() error {
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

	// Remove the project network now that all instances are gone.
	docker.NetworkRemove(cfg.ProjectNetwork())

	return nil
}

// runDownProject tears down all instances AND the project-scoped search
// sidecar stack for the current project.
func runDownProject() error {
	// First tear down all instances (same as --all).
	names, err := instance.GetInstances(cfg)
	if err != nil {
		return err
	}

	for _, name := range names {
		fmt.Printf("Tearing down instance: %s\n", name)
		if err := compose.Run(name, cfg, "down", "--volumes"); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: %v\n", err)
		}
	}

	// Then tear down the project search stack + its volumes.
	stackName := cfg.ProjectStackName()
	fmt.Printf("Tearing down project search stack: %s\n", stackName)
	if err := compose.ProjectStackDown(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "  warning: %v\n", err)
	}

	// Remove the project network last.
	docker.NetworkRemove(cfg.ProjectNetwork())

	fmt.Println("Project fully torn down.")
	return nil
}

// downEverywhere tears down ALL cspace instances across all projects.
// Requires user confirmation before proceeding.
func downEverywhere() error {
	infos, err := instance.GetAllInstances()
	if err != nil {
		return err
	}

	// Discover project stacks to tear down.
	projectStacks := discoverProjectStacks()

	if len(infos) == 0 && len(projectStacks) == 0 {
		fmt.Println("No cspace instances or project stacks running anywhere.")
		return nil
	}

	// Show what's about to be destroyed
	fmt.Println("About to tear down ALL cspace instances across ALL projects:")
	fmt.Println()
	if len(infos) > 0 {
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
	}
	if len(projectStacks) > 0 {
		fmt.Println("  Project search stacks:")
		for _, stack := range projectStacks {
			fmt.Printf("    %s\n", stack)
		}
		fmt.Println()
	}

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

	// Tear down project stacks
	for _, stack := range projectStacks {
		fmt.Printf("Tearing down project stack: %s\n", stack)
		if err := compose.ProjectStackDownDirect(stack); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: %v\n", err)
		}
	}

	// Remove project networks for each unique project
	seen := make(map[string]bool)
	for _, info := range infos {
		if info.Project != "" && !seen[info.Project] {
			seen[info.Project] = true
			docker.NetworkRemove("cspace-" + info.Project)
		}
	}

	fmt.Println("Done.")
	return nil
}

// discoverProjectStacks finds all running project-scoped search sidecar
// stacks by querying Docker for compose projects matching the naming
// convention "cspace-*-stack".
func discoverProjectStacks() []string {
	out, err := docker.Exec("ps", "--filter", "label=com.docker.compose.project",
		"--format", `{{.Label "com.docker.compose.project"}}`)
	if err != nil {
		return nil
	}

	seen := make(map[string]bool)
	var stacks []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "cspace-") && strings.HasSuffix(line, "-stack") && !seen[line] {
			seen[line] = true
			stacks = append(stacks, line)
		}
	}
	return stacks
}
