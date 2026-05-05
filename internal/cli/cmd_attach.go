package cli

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/spf13/cobra"
)

func newAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach <name>",
		Short: "Open an interactive Claude Code session inside a running sandbox",
		Long: `Drop into an interactive ` + "`claude`" + ` session running inside the named
sandbox. Workspace is /workspace; your turns and the agent's output
appear in your terminal directly.

This is independent of the supervisor's autonomous session — they
share the same /workspace but are separate Claude Code sessions
with separate context. Use ` + "`cspace send`" + ` to inject turns into the
supervisor's session non-interactively; use ` + "`cspace attach`" + ` for
hands-on work.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			project := projectName()
			containerName := fmt.Sprintf("cspace-%s-%s", project, name)
			return attachInteractive(containerName)
		},
	}
}

// attachInteractive replaces the current process with `container exec
// -it <containerName> claude`, so the user's terminal is wired
// directly to the in-sandbox Claude Code TUI. On return, the user has
// dropped back to the host shell.
//
// We use syscall.Exec rather than cmd.Run so signals (Ctrl-C, resize)
// flow uninterrupted — there's no Go process between the terminal and
// the `container exec` child to trap them.
func attachInteractive(containerName string) error {
	bin, err := exec.LookPath("container")
	if err != nil {
		return fmt.Errorf("apple `container` CLI not on PATH: %w", err)
	}
	// Clear the terminal before claude takes over so the user gets a
	// clean screen instead of opening claude on top of their pre-
	// cspace-up shell history. \033c is the full reset (clear screen +
	// scrollback + cursor home + reset attributes); claude immediately
	// repaints over it. Stdout-only — stderr stays usable for diagnostics.
	if isStdoutTTY() {
		_, _ = os.Stdout.WriteString("\033c")
	}
	// --dangerously-skip-permissions matches the v0 default: sandboxes
	// are isolated, so the per-tool confirmation prompts that protect
	// host-shell users just get in the way. The supervisor's
	// non-interactive runner already passes bypassPermissions; this
	// makes the interactive path consistent.
	args := []string{
		"container", "exec", "-it", containerName,
		"claude", "--dangerously-skip-permissions",
	}
	env := os.Environ()
	return syscall.Exec(bin, args, env)
}
