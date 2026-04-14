package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/elliottregan/cspace/internal/instance"
	"github.com/elliottregan/cspace/internal/ports"
	"github.com/elliottregan/cspace/internal/provision"
	"github.com/elliottregan/cspace/internal/supervisor"
	"github.com/spf13/cobra"
)

func newUpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "up [name|branch]",
		Short: "Create/reconnect instance and launch Claude",
		Long: `Create or reconnect to a devcontainer instance, then launch Claude Code.

If no name is given, the next available planet name is auto-assigned.
If a branch path (containing /) is given, it becomes the instance name
with slashes replaced by hyphens.

Use --no-claude to provision the container without launching Claude.`,
		GroupID: "instance",
		Args:    cobra.MaximumNArgs(1),
		RunE:    runUp,
	}

	cmd.Flags().Bool("no-claude", false, "Create instance without launching Claude")
	cmd.Flags().String("prompt", "", "Inline prompt text for autonomous agent")
	cmd.Flags().String("prompt-file", "", "Path to a prompt file for autonomous agent")
	cmd.Flags().String("base", "", "Override base branch")
	cmd.Flags().String("teleport-from", "", "Seed the instance from a teleport session dir (internal; used by /cspace-teleport)")

	return cmd
}

func runUp(cmd *cobra.Command, args []string) error {
	noClaude, _ := cmd.Flags().GetBool("no-claude")
	prompt, _ := cmd.Flags().GetString("prompt")
	promptFile, _ := cmd.Flags().GetString("prompt-file")
	baseOverride, _ := cmd.Flags().GetString("base")
	teleportFrom, _ := cmd.Flags().GetString("teleport-from")

	// Validate flags
	if prompt != "" && promptFile != "" {
		return fmt.Errorf("--prompt and --prompt-file are mutually exclusive")
	}
	if teleportFrom != "" && (prompt != "" || promptFile != "") {
		return fmt.Errorf("--teleport-from cannot be combined with --prompt or --prompt-file")
	}
	if teleportFrom != "" && noClaude {
		return fmt.Errorf("--teleport-from implies launching Claude in resume mode; --no-claude is incompatible")
	}
	if promptFile != "" {
		if _, err := os.Stat(promptFile); err != nil {
			return fmt.Errorf("prompt file not found: %s", promptFile)
		}
	}
	if teleportFrom != "" {
		if _, err := os.Stat(teleportFrom); err != nil {
			return fmt.Errorf("teleport-from dir not found: %s", teleportFrom)
		}
	}

	// Parse positional arg: could be name, branch (contains /), or empty
	var name, branch string
	if len(args) > 0 {
		arg := args[0]
		if strings.Contains(arg, "/") {
			branch = arg
			name = strings.ReplaceAll(arg, "/", "-")
			fmt.Printf("Branch: %s -> instance: %s\n", branch, name)
		} else {
			name = arg
		}
	}

	// Auto-assign planet name if none given
	if name == "" {
		var err error
		name, err = ports.NextPlanet(cfg.InstanceLabel())
		if err != nil {
			return err
		}
		fmt.Printf("Instance name: %s\n", name)
	}

	// --base overrides any branch derived from the positional arg
	if baseOverride != "" {
		branch = baseOverride
	}

	return runUpWithArgs(name, branch, noClaude, prompt, promptFile, teleportFrom)
}

// runUpWithArgs is the shared implementation for the up command, callable from
// both the CLI handler and the TUI menu.
func runUpWithArgs(name, branch string, noClaude bool, prompt, promptFile, teleportFrom string) error {
	if teleportFrom != "" {
		return provision.TeleportRun(provision.TeleportParams{
			Name:         name,
			TeleportFrom: teleportFrom,
			Cfg:          cfg,
		})
	}

	// Provision the instance
	_, err := provision.Run(provision.Params{
		Name:   name,
		Branch: branch,
		Cfg:    cfg,
	})
	if err != nil {
		return err
	}

	// Skip Claude onboarding
	composeName := cfg.ComposeName(name)
	if err := instance.SkipOnboarding(composeName); err != nil {
		fmt.Fprintf(os.Stderr, "warning: skip onboarding: %v\n", err)
	}

	// Show port mappings
	instance.ShowPorts(name, cfg)

	// Git operations — fetch and checkout/pull
	_, _ = instance.DcExec(composeName, "git", "fetch", "--prune", "--quiet")
	if branch != "" {
		// Try checkout existing branch, then create tracking branch
		if _, err := instance.DcExec(composeName, "git", "checkout", branch); err != nil {
			_, _ = instance.DcExec(composeName, "git", "checkout", "-b", branch, "origin/"+branch)
		}
		_, _ = instance.DcExec(composeName, "git", "reset", "--hard", "origin/"+branch)
	} else {
		_, _ = instance.DcExec(composeName, "git", "pull", "--ff-only", "--quiet")
	}

	if noClaude {
		fmt.Printf("Instance '%s' is ready. Run 'cspace ssh %s' to connect.\n", name, name)
		return nil
	}

	if prompt == "" && promptFile == "" {
		return supervisor.LaunchInteractive(name, cfg)
	}

	// Autonomous path — stage the prompt in the container, then run through
	// the supervisor for structured event logging and control socket.
	if promptFile != "" {
		if err := supervisor.StagePromptFile(composeName, promptFile, supervisor.ContainerPromptPath); err != nil {
			return err
		}
	} else {
		if err := supervisor.StagePromptText(composeName, prompt, supervisor.ContainerPromptPath); err != nil {
			return err
		}
	}

	return supervisor.LaunchSupervisor(supervisor.LaunchParams{
		Name:       name,
		Role:       supervisor.RoleAgent,
		PromptFile: supervisor.ContainerPromptPath,
		StderrLog:  supervisor.ContainerAgentStderrLog,
	}, cfg)
}
