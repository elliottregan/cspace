package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/elliottregan/cspace/internal/advisor"
	"github.com/elliottregan/cspace/internal/overlay"
	"github.com/elliottregan/cspace/internal/planets"
	"github.com/elliottregan/cspace/internal/ports"
	"github.com/elliottregan/cspace/internal/provision"
	"github.com/elliottregan/cspace/internal/supervisor"
	"github.com/spf13/cobra"
	"golang.org/x/term"
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
	cmd.Flags().Bool("verbose", false,
		"Stream raw provisioning output instead of showing the planet overlay. "+
			"Use when debugging provisioning failures or when piping output.")
	cmd.Flags().String("prompt", "", "Inline prompt text for autonomous agent")
	cmd.Flags().String("prompt-file", "", "Path to a prompt file for autonomous agent")
	cmd.Flags().Bool("persistent", false,
		"Keep the agent alive between responses so `cspace send <name>` can "+
			"drive multi-turn conversations. Without this flag, the agent exits "+
			"after its first result (the default one-shot behavior).")
	cmd.Flags().String("base", "", "Override base branch")
	cmd.Flags().String("teleport-from", "", "Seed the instance from a teleport session dir (internal; used by /cspace-teleport)")
	cmd.Flags().Bool("index", false, "Bootstrap search indexes during provisioning (auto-enabled for advisors and coordinators)")

	return cmd
}

func runUp(cmd *cobra.Command, args []string) error {
	noClaude, _ := cmd.Flags().GetBool("no-claude")
	verbose, _ := cmd.Flags().GetBool("verbose")
	prompt, _ := cmd.Flags().GetString("prompt")
	promptFile, _ := cmd.Flags().GetString("prompt-file")
	persistent, _ := cmd.Flags().GetBool("persistent")
	baseOverride, _ := cmd.Flags().GetString("base")
	teleportFrom, _ := cmd.Flags().GetString("teleport-from")
	indexFlag, _ := cmd.Flags().GetBool("index")

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
	if persistent && prompt == "" && promptFile == "" {
		return fmt.Errorf("--persistent requires an initial --prompt or --prompt-file")
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

	return runUpWithArgs(name, branch, noClaude, verbose, prompt, promptFile, teleportFrom, persistent, indexFlag)
}

// runUpWithArgs is the shared implementation for the up command, callable from
// both the CLI handler and the TUI menu.
func runUpWithArgs(name, branch string, noClaude, verbose bool, prompt, promptFile, teleportFrom string, persistent, bootstrapSearch bool) error {
	if teleportFrom != "" {
		return provision.TeleportRun(provision.TeleportParams{
			Name:         name,
			TeleportFrom: teleportFrom,
			Cfg:          cfg,
		})
	}

	if err := provisionWithUI(name, branch, verbose, bootstrapSearch); err != nil {
		return err
	}

	// provision.Run's final phase handles SkipOnboarding + git sync, so
	// there's nothing between overlay close and exec claude that could
	// flash the terminal.
	composeName := cfg.ComposeName(name)

	if noClaude {
		fmt.Printf("Instance '%s' is ready. Run 'cspace ssh %s' to connect.\n", name, name)
		return nil
	}

	// Warn before handing off to Claude/supervisor when the image is older
	// than the host CLI — new supervisor flags or prompts won't be honored
	// until the user runs `cspace rebuild`.
	printImageVersionWarning(cfg)

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
		Name:         name,
		Role:         supervisor.RoleAgent,
		PromptFile:   supervisor.ContainerPromptPath,
		StderrLog:    supervisor.ContainerAgentStderrLog,
		Persistent:   persistent,
		AdvisorNames: advisor.SortedAdvisorNames(cfg),
	}, cfg)
}

// provisionWithUI dispatches to the overlay or the raw log stream based on
// TTY, --verbose, and terminal size. The overlay path runs provision in a
// goroutine while a bubbletea Program consumes a buffered event channel;
// returns the provisioning error (if any) after the overlay exits.
func provisionWithUI(name, branch string, verbose, bootstrapSearch bool) error {
	if shouldUseOverlay(verbose) {
		return provisionWithOverlay(name, branch, bootstrapSearch)
	}
	_, err := provision.Run(provision.Params{
		Name:            name,
		Branch:          branch,
		Cfg:             cfg,
		BootstrapSearch: bootstrapSearch,
	})
	return err
}

func shouldUseOverlay(verbose bool) bool {
	if verbose {
		return false
	}
	if !isInteractive() {
		return false
	}
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return false
	}
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w < 96 || h < 32 {
		return false
	}
	return true
}

func provisionWithOverlay(name, branch string, bootstrapSearch bool) error {
	events := make(chan overlay.ProvisionEvent, 16)
	reporter := overlay.NewChannelReporter(events)

	// provision.Run delegates to subprocesses (docker compose, git,
	// docker exec) and helpers (configureGit, runPostSetup) that write
	// directly to os.Stdout/os.Stderr, bypassing Params.Stdout/Stderr.
	// Redirect the process's stdout/stderr to /dev/null for the lifetime
	// of the overlay so none of that leaks into the alt-screen. We hand
	// the original stdout to bubbletea explicitly so the overlay still
	// renders on the real terminal.
	origStdout := os.Stdout
	origStderr := os.Stderr
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("opening /dev/null: %w", err)
	}
	os.Stdout = devNull
	os.Stderr = devNull
	defer func() {
		os.Stdout = origStdout
		os.Stderr = origStderr
		_ = devNull.Close()
	}()

	done := make(chan error, 1)
	go func() {
		_, err := provision.Run(provision.Params{
			Name:            name,
			Branch:          branch,
			Cfg:             cfg,
			Reporter:        reporter,
			Stdout:          io.Discard,
			Stderr:          io.Discard,
			BootstrapSearch: bootstrapSearch,
		})
		close(events)
		done <- err
	}()

	planet := planets.MustGet(name)
	model := overlay.ModelConfig{
		Name:   name,
		Planet: planet,
		Total:  len(provision.Phases),
		Events: events,
	}
	if err := overlay.RunOn(model, origStdout); err != nil {
		// Drain the channel so the provision goroutine doesn't block on
		// its buffered reporter sends when the overlay couldn't render.
		go func() {
			for range events {
			}
		}()
		return err
	}
	return <-done
}
