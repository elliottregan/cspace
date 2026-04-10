package supervisor

import (
	"fmt"

	"github.com/elliottregan/cspace/internal/config"
	"github.com/elliottregan/cspace/internal/instance"
)

// cliMjsPath is the path to the supervisor CLI inside containers.
const cliMjsPath = "/opt/cspace/lib/agent-supervisor/cli.mjs"

// Dispatch runs a supervisor CLI command inside a running container.
// It picks the first running instance for the current project as the
// dispatch target (any container can reach any instance's socket via
// the shared cspace-logs volume).
//
// Output is printed to stdout/stderr via DcExecInteractive.
func Dispatch(composeName string, args ...string) error {
	cmdArgs := append([]string{"node", cliMjsPath}, args...)
	_, err := instance.DcExec(composeName, cmdArgs...)
	return err
}

// DispatchInteractive runs a supervisor CLI command with full TTY
// passthrough. Used for long-running commands like `watch` that need
// to stream output continuously.
func DispatchInteractive(composeName string, args ...string) error {
	cmdArgs := append([]string{"node", cliMjsPath}, args...)
	return instance.DcExecInteractive(composeName, cmdArgs...)
}

// DispatchWithOutput runs a supervisor CLI command and returns the output.
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
