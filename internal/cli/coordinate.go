package cli

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/elliottregan/cspace/internal/advisor"
	"github.com/elliottregan/cspace/internal/instance"
	"github.com/elliottregan/cspace/internal/provision"
	"github.com/elliottregan/cspace/internal/supervisor"
	"github.com/spf13/cobra"
)

func newCoordinateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "coordinate [instructions]",
		Short: "Multi-task coordinator (reads coordinator.md playbook)",
		Long: `Launch a multi-task coordinator agent that reads the coordinator.md
playbook and orchestrates multiple agents.

The coordinator prompt is built from the playbook template plus user
instructions, either inline or from a file.`,
		GroupID: "agents",
		RunE:    runCoordinate,
	}

	cmd.Flags().String("prompt-file", "", "Load prompt from file instead of inline")
	cmd.Flags().String("name", "", "Use a specific instance name (resumable)")
	cmd.Flags().String("system-prompt-file", "",
		"Override the coordinator's system prompt and skip the coordinator.md playbook. "+
			"Lets the coordinator role run an ad-hoc task (e.g. an interactive chat test) "+
			"without inheriting the default orchestration framing.")

	return cmd
}

func runCoordinate(cmd *cobra.Command, args []string) error {
	promptFile, _ := cmd.Flags().GetString("prompt-file")
	name, _ := cmd.Flags().GetString("name")
	systemPromptFile, _ := cmd.Flags().GetString("system-prompt-file")

	var prompt string
	if len(args) > 0 {
		prompt = args[0]
	}

	if prompt == "" && promptFile == "" {
		return fmt.Errorf("usage: cspace coordinate \"<instructions>\" [--name <name>] [--system-prompt-file <path>]\n       cspace coordinate --prompt-file <path> [--name <name>] [--system-prompt-file <path>]")
	}
	if prompt != "" && promptFile != "" {
		return fmt.Errorf("pass either an inline prompt or --prompt-file, not both")
	}
	if promptFile != "" {
		if _, err := os.Stat(promptFile); err != nil {
			return fmt.Errorf("prompt file not found: %s", promptFile)
		}
	}
	if systemPromptFile != "" {
		if _, err := os.Stat(systemPromptFile); err != nil {
			return fmt.Errorf("system prompt file not found: %s", systemPromptFile)
		}
	}

	return runCoordinateWithArgs(prompt, promptFile, name, systemPromptFile)
}

// coordinatorIsAlive checks whether a coordinator supervisor is already
// running by probing the well-known _coordinator socket with a status
// command. Returns true only if the socket responds successfully.
func coordinatorIsAlive() bool {
	logsPath := supervisor.ResolveLogsVolumePath(cfg)
	if logsPath == "" {
		return false
	}
	sockPath := filepath.Join(logsPath, "_coordinator", "supervisor.sock")
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	req, _ := json.Marshal(map[string]string{"cmd": "status"})
	_, _ = conn.Write(append(req, '\n'))
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		return false
	}
	var reply struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(buf[:n], &reply); err != nil {
		return false
	}
	return reply.OK
}

// runCoordinateWithArgs is the shared implementation for the coordinate command,
// callable from both the CLI handler and the TUI menu.
//
// When systemPromptFile is non-empty, the coordinator.md playbook is skipped
// and the user's prompt becomes the entire first user turn. The system prompt
// file is copied into the container and passed to supervisor.mjs via
// --system-prompt-file, letting the coordinator role run ad-hoc tasks
// without inheriting the default orchestration framing.
func runCoordinateWithArgs(prompt, promptFile, name, systemPromptFile string) error {
	if name == "" {
		name = "coord-" + strconv.FormatInt(time.Now().Unix(), 10)
	}

	if coordinatorIsAlive() {
		return fmt.Errorf("a coordinator is already running (socket at _coordinator/supervisor.sock is alive).\n" +
			"Only one coordinator can run per project. Use 'cspace send _coordinator' to communicate with it,\n" +
			"or stop it first with 'cspace interrupt _coordinator'")
	}

	// Bring up all configured advisors. A fresh advisor is provisioned and
	// launched persistent; an already-alive advisor is reused so its session
	// continuity is preserved across cspace coordinate calls.
	for adName := range cfg.Advisors {
		if err := advisor.Launch(cfg, adName); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: advisor %s failed to launch: %v\n", adName, err)
		} else if advisor.IsAlive(cfg, adName) {
			fmt.Fprintf(os.Stderr, "Advisor %s ready.\n", adName)
		}
	}

	_, err := provision.Run(provision.Params{
		Name: name,
		Cfg:  cfg,
	})
	if err != nil {
		return err
	}

	composeName := cfg.ComposeName(name)
	// SkipOnboarding is handled inside provision.Run's final phase.

	// Re-copy host .env so the coordinator inherits GH_TOKEN, etc.
	envFile := filepath.Join(cfg.ProjectRoot, ".env")
	if _, err := os.Stat(envFile); err == nil {
		_ = instance.DcCp(composeName, envFile, "/workspace/.env")
		_, _ = instance.DcExecRoot(composeName, "chown", "dev:dev", "/workspace/.env")
	}

	// User instructions (inline or from file).
	var userBody string
	if promptFile != "" {
		bodyBytes, err := os.ReadFile(promptFile)
		if err != nil {
			return fmt.Errorf("reading prompt file: %w", err)
		}
		userBody = string(bodyBytes)
	} else {
		userBody = prompt
	}

	// Build the first user turn. Default: coordinator.md playbook as the
	// framing, then the user's instructions. When a custom system prompt
	// is supplied, the playbook is redundant (and potentially counter to
	// what the user is trying to do), so the user's prompt stands alone.
	var fullPrompt string
	containerSystemPrompt := ""
	if systemPromptFile != "" {
		fullPrompt = userBody
		systemBytes, err := os.ReadFile(systemPromptFile)
		if err != nil {
			return fmt.Errorf("reading system prompt file: %w", err)
		}
		if err := supervisor.StagePromptText(composeName, string(systemBytes), supervisor.ContainerSystemPromptPath); err != nil {
			return err
		}
		containerSystemPrompt = supervisor.ContainerSystemPromptPath
	} else {
		playbookFile := cfg.ResolveAgent("coordinator.md")
		playbookBytes, err := os.ReadFile(playbookFile)
		if err != nil {
			return fmt.Errorf("reading coordinator playbook: %w", err)
		}
		// Render the advisor roster into the coordinator's first user turn so it
		// knows what names are valid for ask_advisor/send_to_advisor.
		var rosterBuilder strings.Builder
		if len(cfg.Advisors) > 0 {
			rosterBuilder.WriteString("\n\n## Advisor roster (available via ask_advisor / send_to_advisor)\n\n")
			for _, adName := range advisor.SortedAdvisorNames(cfg) {
				spec := cfg.Advisors[adName]
				model := spec.Model
				if model == "" {
					model = "(account default)"
				}
				effort := spec.Effort
				if effort == "" {
					effort = "(default)"
				}
				fmt.Fprintf(&rosterBuilder, "- **%s** — model=%s, effort=%s\n", adName, model, effort)
			}
		}
		fullPrompt = string(playbookBytes) + rosterBuilder.String() + "\n\nUSER INSTRUCTIONS:\n\n" + userBody
	}

	if err := supervisor.StagePromptText(composeName, fullPrompt, supervisor.ContainerCoordPromptPath); err != nil {
		return err
	}

	// Coordinator defaults to Sonnet — deep reasoning is delegated to
	// advisors (Opus). User can override via claude.model in .cspace.json.
	coordModel := cfg.Claude.Model
	if coordModel == "" {
		coordModel = "claude-sonnet-4-6"
	}
	coordEffort := cfg.Claude.Effort
	if coordEffort == "" {
		coordEffort = "high"
	}

	return supervisor.LaunchSupervisor(supervisor.LaunchParams{
		Name:             name,
		Role:             supervisor.RoleCoordinator,
		PromptFile:       supervisor.ContainerCoordPromptPath,
		StderrLog:        supervisor.ContainerCoordStderrLog,
		SystemPromptFile: containerSystemPrompt,
		AdvisorNames:     advisor.SortedAdvisorNames(cfg),
		ModelOverride:    coordModel,
		EffortOverride:   coordEffort,
	}, cfg)
}
