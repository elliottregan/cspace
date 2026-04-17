package supervisor

import (
	"fmt"

	"github.com/elliottregan/cspace/internal/config"
	"github.com/elliottregan/cspace/internal/instance"
)

// Supervisor roles.
const (
	RoleAgent       = "agent"
	RoleCoordinator = "coordinator"
)

// Well-known container paths used by the supervisor protocol.
const (
	ContainerPromptPath       = "/tmp/claude-prompt.txt"
	ContainerCoordPromptPath  = "/tmp/coordinator-prompt.txt"
	ContainerAgentStderrLog   = "/tmp/agent-stderr.log"
	ContainerCoordStderrLog   = "/tmp/coordinator-stderr.log"
	ContainerRestartPrompt    = "/tmp/restart-prompt.txt"
	ContainerSystemPromptPath = "/tmp/coordinator-system-prompt.txt"

	cliMjsPath = "/opt/cspace/lib/agent-supervisor/cli.mjs"
)

// Dispatch runs a supervisor CLI command inside a running container.
// Output is discarded; use DispatchWithOutput to capture it.
func Dispatch(composeName string, args ...string) error {
	_, err := DispatchWithOutput(composeName, args...)
	return err
}

// DispatchInteractive runs a supervisor CLI command with full TTY
// passthrough. Used for long-running commands like `watch` that need
// to stream output continuously.
func DispatchInteractive(composeName string, args ...string) error {
	cmdArgs := append([]string{"node", cliMjsPath}, args...)
	return instance.DcExecInteractive(composeName, cmdArgs...)
}

// DispatchWithOutput runs a supervisor CLI command and returns stdout.
func DispatchWithOutput(composeName string, args ...string) (string, error) {
	cmdArgs := append([]string{"node", cliMjsPath}, args...)
	return instance.DcExec(composeName, cmdArgs...)
}

// ResolveDispatchTarget returns the compose name of a running instance
// that can be used for supervisor dispatch. Any running container can
// dispatch to any instance via the shared logs volume.
func ResolveDispatchTarget(cfg *config.Config) (string, error) {
	names, err := instance.GetInstances(cfg)
	if err != nil || len(names) == 0 {
		return "", fmt.Errorf("no running cspace instances; start one with 'cspace up' first")
	}
	return cfg.ComposeName(names[0]), nil
}

// RunDispatch is a convenience helper for CLI dispatch commands.
// It resolves a dispatch target, runs the command, and prints output.
func RunDispatch(cfg *config.Config, args ...string) error {
	target, err := ResolveDispatchTarget(cfg)
	if err != nil {
		return err
	}

	out, err := DispatchWithOutput(target, args...)
	if err != nil {
		return err
	}
	if out != "" {
		fmt.Println(out)
	}
	return nil
}
