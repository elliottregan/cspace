package control

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// AttachSpec describes one interactive attach into a sandbox.
//
// Session empty means "no tmux": Command is exec'd directly, which is the
// fallback for a sandbox whose image predates tmux. That path cannot survive
// the host terminal closing and leaves the command running inside the
// sandbox when it does — callers warn.
type AttachSpec struct {
	Container string   // full container name, e.g. cspace-demo-mercury
	Session   string   // tmux session name; empty runs Command directly
	Command   []string // what the session runs when it is created
	TERM      string   // the host terminal's TERM
	COLORTERM string   // the host terminal's COLORTERM
}

// ClaudeAttach returns the spec for an interactive Claude session, reading
// the host terminal description from the process environment.
//
// --dangerously-skip-permissions matches the v0 default: sandboxes are
// isolated, so the per-tool confirmation prompts that protect host-shell
// users just get in the way. The supervisor's non-interactive runner already
// passes bypassPermissions; this keeps the interactive path consistent.
func ClaudeAttach(container string, tmux bool) AttachSpec {
	spec := AttachSpec{
		Container: container,
		Command:   []string{"claude", "--dangerously-skip-permissions"},
		TERM:      os.Getenv("TERM"),
		COLORTERM: os.Getenv("COLORTERM"),
	}
	if tmux {
		spec.Session = SessionClaude
	}
	return spec
}

// AttachArgv resolves the container binary and builds the exec argv. argv[0]
// is the literal "container" per exec convention, so callers running it as a
// child pass argv[1:].
//
// With a session it is attach-or-create in one argv: `-A` attaches when the
// session already exists and ignores the command, so the command runs only on
// creation and a second attach shares the first one's screen. When the
// command exits the session ends and every client is dropped.
func AttachArgv(spec AttachSpec) (bin string, argv []string, err error) {
	if spec.Container == "" {
		return "", nil, errors.New("attach: empty container name")
	}
	if len(spec.Command) == 0 {
		return "", nil, errors.New("attach: empty command")
	}
	bin, err = exec.LookPath("container")
	if err != nil {
		return "", nil, fmt.Errorf("apple `container` CLI not on PATH: %w", err)
	}
	argv = []string{"container", "exec", "-it"}
	argv = append(argv, TerminalEnvArgs(spec.TERM, spec.COLORTERM)...)
	argv = append(argv, spec.Container)
	if spec.Session != "" {
		argv = append(argv,
			"tmux", "-f", TmuxConf,
			"new-session", "-A", "-s", spec.Session, "-c", Workspace)
	}
	argv = append(argv, spec.Command...)
	return bin, argv, nil
}

// TerminalEnv reports the TERM/COLORTERM a sandbox should see, given the host
// terminal's own values.
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
func TerminalEnv(term, colorterm string) map[string]string {
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

// ApplyTerminalEnv seeds the container's env with the terminal description,
// leaving any value already there alone. Baking it at create time covers the
// paths attach's per-exec flags don't: a hand-rolled `container exec`, and the
// container's own main process. Apple Container only injects its TERM=xterm
// default when nothing is set, so a baked value survives a TTY exec.
func ApplyTerminalEnv(env map[string]string, term, colorterm string) {
	for k, v := range TerminalEnv(term, colorterm) {
		if _, exists := env[k]; !exists {
			env[k] = v
		}
	}
}

// TerminalEnvArgs renders TerminalEnv as `container exec` flags, ordered by
// key so argv is deterministic.
func TerminalEnvArgs(term, colorterm string) []string {
	env := TerminalEnv(term, colorterm)
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
