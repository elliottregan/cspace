package cli

import (
	"strings"
	"testing"
)

func TestAttachArgs(t *testing.T) {
	// Pin the terminal env: argv now carries it, and the developer's own
	// terminal must not decide what this test expects.
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "")

	bin, argv, err := attachArgs("cspace-demo-mercury")
	if err != nil {
		// container may not be on PATH in CI; only assert argv shape then.
		t.Skipf("container CLI not resolvable: %v", err)
	}
	if !strings.HasSuffix(bin, "container") {
		t.Errorf("bin = %q, want it to resolve the container binary", bin)
	}
	want := []string{
		"container", "exec", "-it",
		"-e", "TERM=xterm-256color",
		"cspace-demo-mercury", "claude", "--dangerously-skip-permissions",
	}
	if len(argv) != len(want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q", i, argv[i], want[i])
		}
	}
}

// TestTerminalEnv covers the color-support signal cspace hands a sandbox.
// Apple Container injects a bare TERM=xterm when it allocates a TTY and never
// sets COLORTERM, which Node's color detection reads as 16 colors — so Claude
// inside a sandbox paints with 16 while the same terminal gives it 16.7M
// outside. These values are what close that gap.
func TestTerminalEnv(t *testing.T) {
	cases := []struct {
		name      string
		term      string
		colorterm string
		want      map[string]string
	}{
		{
			// The sandbox's terminfo database is Debian's; xterm-ghostty is
			// not in it, and ncurses tools (vim, less) fail outright on an
			// unknown terminal type. Substitute an entry it does have.
			name: "exotic TERM is substituted, COLORTERM forwarded",
			term: "xterm-ghostty", colorterm: "truecolor",
			want: map[string]string{"TERM": "xterm-256color", "COLORTERM": "truecolor"},
		},
		{
			name: "a terminfo entry the sandbox has passes through",
			term: "screen-256color", colorterm: "truecolor",
			want: map[string]string{"TERM": "screen-256color", "COLORTERM": "truecolor"},
		},
		{
			// Claiming truecolor the host never claimed would be inventing
			// capability; 256 colors is still a 16x improvement on xterm.
			name: "no COLORTERM on the host means none in the sandbox",
			term: "xterm-256color", colorterm: "",
			want: map[string]string{"TERM": "xterm-256color"},
		},
		{
			// TERM=dumb means "emit no escape codes at all" — dressing that
			// up would put escape sequences into whatever is capturing output.
			name: "dumb terminal is left alone",
			term: "dumb", colorterm: "truecolor",
			want: map[string]string{},
		},
		{
			name: "no TERM at all (cron, pipe) is left alone",
			term: "", colorterm: "",
			want: map[string]string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := terminalEnv(tc.term, tc.colorterm)
			if len(got) != len(tc.want) {
				t.Fatalf("terminalEnv(%q, %q) = %v, want %v", tc.term, tc.colorterm, got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("%s = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

// TestTerminalEnvArgs pins the flag form and its ordering, since argv is what
// attach actually execs.
func TestTerminalEnvArgs(t *testing.T) {
	got := terminalEnvArgs("xterm-ghostty", "truecolor")
	want := []string{"-e", "COLORTERM=truecolor", "-e", "TERM=xterm-256color"}
	if len(got) != len(want) {
		t.Fatalf("terminalEnvArgs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if len(terminalEnvArgs("dumb", "")) != 0 {
		t.Error("dumb terminal produced -e flags")
	}
}

// TestAttachArgsCarriesTerminalEnv — attach is the interactive path, so it
// forwards the terminal actually attaching rather than whatever was current
// when the sandbox booted.
func TestAttachArgsCarriesTerminalEnv(t *testing.T) {
	t.Setenv("TERM", "xterm-ghostty")
	t.Setenv("COLORTERM", "truecolor")

	_, argv, err := attachArgs("cspace-demo-mercury")
	if err != nil {
		t.Skipf("container CLI not resolvable: %v", err)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "-e TERM=xterm-256color") {
		t.Errorf("argv does not set TERM: %v", argv)
	}
	if !strings.Contains(joined, "-e COLORTERM=truecolor") {
		t.Errorf("argv does not forward COLORTERM: %v", argv)
	}
	// The command being run must still come last, after every flag.
	if argv[len(argv)-1] != "--dangerously-skip-permissions" || argv[len(argv)-2] != "claude" {
		t.Errorf("argv does not end with the claude invocation: %v", argv)
	}
}

// TestApplyTerminalEnvNeverOverridesExisting — the baked values are a default
// for a container that would otherwise be told TERM=xterm. Anything the
// project or the user set explicitly (devcontainer containerEnv, --env) is a
// deliberate choice and outranks it.
func TestApplyTerminalEnvNeverOverridesExisting(t *testing.T) {
	env := map[string]string{"TERM": "screen"}
	applyTerminalEnv(env, "xterm-ghostty", "truecolor")

	if env["TERM"] != "screen" {
		t.Errorf("TERM = %q, want the pre-existing \"screen\" to survive", env["TERM"])
	}
	if env["COLORTERM"] != "truecolor" {
		t.Errorf("COLORTERM = %q, want it seeded alongside", env["COLORTERM"])
	}
}

// TestApplyTerminalEnvSeedsAnEmptyMap is the ordinary boot: nothing set, so
// both land and the sandbox stops reporting 16 colors.
func TestApplyTerminalEnvSeedsAnEmptyMap(t *testing.T) {
	env := map[string]string{}
	applyTerminalEnv(env, "xterm-ghostty", "truecolor")

	if env["TERM"] != "xterm-256color" || env["COLORTERM"] != "truecolor" {
		t.Errorf("env = %v, want TERM=xterm-256color COLORTERM=truecolor", env)
	}
}

// TestApplyTerminalEnvHeadlessBootAddsNothing — `cspace up` from a cron job or
// a pipe has no terminal to describe, and inventing one would put escape codes
// into captured output.
func TestApplyTerminalEnvHeadlessBootAddsNothing(t *testing.T) {
	env := map[string]string{}
	applyTerminalEnv(env, "", "")

	if len(env) != 0 {
		t.Errorf("env = %v, want it untouched", env)
	}
}
