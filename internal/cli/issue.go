package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/elliottregan/cspace/internal/instance"
	"github.com/elliottregan/cspace/internal/ports"
	"github.com/elliottregan/cspace/internal/provision"
	"github.com/elliottregan/cspace/internal/supervisor"
	"github.com/spf13/cobra"
)

func newIssueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "issue <number>",
		Short: "Run an agent against a GitHub issue",
		Long: `Provision an instance and run the full implementer workflow against
a GitHub issue. Renders the implementer.md template with issue-specific
variables and launches an autonomous agent.`,
		GroupID: "agents",
		Args:    cobra.ExactArgs(1),
		RunE:    runIssue,
	}

	cmd.Flags().String("base", "", "Override base branch (default: current branch)")
	cmd.Flags().String("name", "", "Use a specific instance name")

	return cmd
}

func runIssue(cmd *cobra.Command, args []string) error {
	number := args[0]
	baseBranch, _ := cmd.Flags().GetString("base")
	name, _ := cmd.Flags().GetString("name")

	// Auto-assign instance name
	if name == "" {
		var err error
		name, err = ports.NextPlanet(cfg.InstanceLabel())
		if err != nil {
			return err
		}
	}
	fmt.Printf("Instance: %s, Issue: #%s\n", name, number)

	// Detect base branch if not specified
	if baseBranch == "" {
		baseBranch = detectCurrentBranch(cfg.ProjectRoot)
		if baseBranch == "" {
			baseBranch = "main"
		}
	}

	// Resolve and render the implementer template
	templateFile := cfg.ResolveAgent("implementer.md")
	templateBytes, err := os.ReadFile(templateFile)
	if err != nil {
		return fmt.Errorf("reading implementer template: %w", err)
	}

	// Substitute template variables
	prompt := string(templateBytes)
	prompt = strings.ReplaceAll(prompt, "${NUMBER}", number)
	prompt = strings.ReplaceAll(prompt, "${BASE_BRANCH}", baseBranch)

	// Verify and E2E commands from config
	verifyCmd := cfg.Verify.All
	if verifyCmd == "" {
		verifyCmd = "echo 'No verify command configured'"
	}
	e2eCmd := cfg.Verify.E2E
	if e2eCmd == "" {
		e2eCmd = "echo 'No E2E command configured'"
	}
	prompt = strings.ReplaceAll(prompt, "${VERIFY_COMMAND}", verifyCmd)
	prompt = strings.ReplaceAll(prompt, "${E2E_COMMAND}", e2eCmd)

	// Strategic context preamble (empty by default, populated by coordinator)
	prompt = strings.ReplaceAll(prompt, "${STRATEGIC_CONTEXT_PREAMBLE}", "")

	// Provision the instance
	_, err = provision.Run(provision.Params{
		Name:   name,
		Branch: baseBranch,
		Cfg:    cfg,
	})
	if err != nil {
		return err
	}

	composeName := cfg.ComposeName(name)
	_ = instance.SkipOnboarding(composeName)
	instance.ShowPorts(name, cfg)

	// Git operations
	_, _ = instance.DcExec(composeName, "git", "fetch", "--prune", "--quiet")
	if baseBranch != "" {
		if _, err := instance.DcExec(composeName, "git", "checkout", baseBranch); err != nil {
			_, _ = instance.DcExec(composeName, "git", "checkout", "-b", baseBranch, "origin/"+baseBranch)
		}
		_, _ = instance.DcExec(composeName, "git", "reset", "--hard", "origin/"+baseBranch)
	}

	if err := supervisor.StagePromptText(composeName, prompt, supervisor.ContainerPromptPath); err != nil {
		return err
	}

	return supervisor.LaunchSupervisor(supervisor.LaunchParams{
		Name:       name,
		Role:       supervisor.RoleAgent,
		PromptFile: supervisor.ContainerPromptPath,
		StderrLog:  supervisor.ContainerAgentStderrLog,
	}, cfg)
}

// detectCurrentBranch returns the current git branch in the project root.
func detectCurrentBranch(projectRoot string) string {
	out, err := exec.Command("git", "-C", projectRoot, "branch", "--show-current").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
