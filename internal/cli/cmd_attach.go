package cli

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
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
			if err := ensureRegistryDaemon(); err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: cspace daemon not reachable: %v\n", err)
			}

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
	bin, argv, err := attachArgs(containerName)
	if err != nil {
		return err
	}
	// Clear the terminal before claude takes over so the user gets a
	// clean screen instead of opening claude on top of their pre-
	// cspace-up shell history. \033c is the full reset (clear screen +
	// scrollback + cursor home + reset attributes); claude immediately
	// repaints over it. Stdout-only — stderr stays usable for diagnostics.
	if isStdoutTTY() {
		_, _ = os.Stdout.WriteString("\033c")
	}
	return syscall.Exec(bin, argv, os.Environ())
}

// attachArgs resolves the container binary and builds the exec argv shared by
// both attach paths (CLI syscall.Exec and TUI tea.ExecProcess), so they stay
// identical. argv[0] is the literal "container" per exec convention.
//
// --dangerously-skip-permissions matches the v0 default: sandboxes are
// isolated, so the per-tool confirmation prompts that protect host-shell
// users just get in the way. The supervisor's non-interactive runner already
// passes bypassPermissions; this makes the interactive path consistent.
func attachArgs(containerName string) (bin string, argv []string, err error) {
	bin, err = exec.LookPath("container")
	if err != nil {
		return "", nil, fmt.Errorf("apple `container` CLI not on PATH: %w", err)
	}
	argv = []string{"container", "exec", "-it"}
	argv = append(argv, terminalEnvArgs(os.Getenv("TERM"), os.Getenv("COLORTERM"))...)
	argv = append(argv, containerName, "claude", "--dangerously-skip-permissions")
	return bin, argv, nil
}

// terminalEnv reports the TERM/COLORTERM a sandbox should see, given the
// host terminal's own values.
//
// Programs pick a palette by reading these two variables — there is no way to
// ask a terminal what it supports. Apple Container injects a bare TERM=xterm
// when it allocates a TTY and never sets COLORTERM, which Node's color
// detection reads as 16 colors. Measured in a real sandbox: TERM=xterm alone
// yields 16, xterm-256color yields 256, and adding COLORTERM=truecolor yields
// 16.7M. That is why Claude's palette flattens inside a sandbox while the same
// terminal renders it fully outside — nothing in the PTY strips color, the
// program simply chooses fewer colors.
//
// TERM is not forwarded verbatim. The sandbox's terminfo database is Debian's,
// and an entry it lacks (xterm-ghostty, say) breaks every ncurses program in
// there with "unknown terminal type" — Claude survives it, `less` and `vim` do
// not. Anything unrecognized is mapped to xterm-256color, which Debian ships.
// COLORTERM is forwarded as-is: claiming truecolor the host never claimed
// would be inventing capability.
func terminalEnv(term, colorterm string) map[string]string {
	// "dumb" and unset both mean "not an interactive terminal" — output is
	// being captured, and escape codes would be noise in whatever captures it.
	if term == "" || term == "dumb" {
		return map[string]string{}
	}
	out := map[string]string{"TERM": sandboxTERM(term)}
	if colorterm != "" {
		out["COLORTERM"] = colorterm
	}
	return out
}

// sandboxTERM maps a host TERM onto one the sandbox's terminfo database
// actually carries.
func sandboxTERM(term string) string {
	if strings.HasSuffix(term, "-256color") {
		return term
	}
	return "xterm-256color"
}

// applyTerminalEnv seeds the container's env with the terminal description,
// leaving any value already there alone. Baking it at create time covers the
// paths attach's per-exec flags don't: a hand-rolled `container exec`, and the
// container's own main process. Apple Container only injects its TERM=xterm
// default when nothing is set, so a baked value survives a TTY exec.
func applyTerminalEnv(env map[string]string, term, colorterm string) {
	for k, v := range terminalEnv(term, colorterm) {
		if _, exists := env[k]; !exists {
			env[k] = v
		}
	}
}

// terminalEnvArgs renders terminalEnv as `container exec` flags, ordered by
// key so argv is deterministic.
func terminalEnvArgs(term, colorterm string) []string {
	env := terminalEnv(term, colorterm)
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	args := make([]string, 0, len(keys)*2)
	for _, k := range keys {
		args = append(args, "-e", k+"="+env[k])
	}
	return args
}
