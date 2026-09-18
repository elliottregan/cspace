package control

import (
	"strings"
	"testing"
)

// TestAttachArgv pins the exec argv both attach paths build. `-A` on
// new-session is the whole persistence story: it attaches when the session
// exists and ignores the command, so the command runs only on creation and a
// second attach shares the first one's screen.
func TestAttachArgv(t *testing.T) {
	cases := []struct {
		name string
		spec AttachSpec
		want []string
	}{
		{
			name: "tmux session, TERM forwarded",
			spec: AttachSpec{
				Container: "cspace-demo-mercury",
				Session:   SessionClaude,
				Command:   []string{"claude", "--dangerously-skip-permissions"},
				TERM:      "xterm-256color",
			},
			want: []string{
				"container", "exec", "-it",
				"-e", "TERM=xterm-256color",
				"cspace-demo-mercury",
				"tmux", "-f", "/usr/local/etc/cspace-tmux.conf",
				"new-session", "-A", "-s", "cspace-claude", "-c", "/workspace",
				"claude", "--dangerously-skip-permissions",
			},
		},
		{
			// The fallback for an image built before cspace shipped tmux:
			// exactly the argv attach used before this change.
			name: "no session falls back to a direct exec",
			spec: AttachSpec{
				Container: "cspace-demo-mercury",
				Command:   []string{"claude", "--dangerously-skip-permissions"},
				TERM:      "xterm-256color",
			},
			want: []string{
				"container", "exec", "-it",
				"-e", "TERM=xterm-256color",
				"cspace-demo-mercury",
				"claude", "--dangerously-skip-permissions",
			},
		},
		{
			// An exotic TERM is substituted (Debian terminfo has no
			// xterm-ghostty) and COLORTERM is forwarded as-is.
			name: "exotic TERM substituted, COLORTERM forwarded",
			spec: AttachSpec{
				Container: "cspace-demo-venus",
				Session:   SessionShell,
				Command:   []string{"bash", "-l"},
				TERM:      "xterm-ghostty",
				COLORTERM: "truecolor",
			},
			want: []string{
				"container", "exec", "-it",
				"-e", "COLORTERM=truecolor",
				"-e", "TERM=xterm-256color",
				"cspace-demo-venus",
				"tmux", "-f", "/usr/local/etc/cspace-tmux.conf",
				"new-session", "-A", "-s", "cspace-shell", "-c", "/workspace",
				"bash", "-l",
			},
		},
		{
			name: "no terminal at all adds no -e flags",
			spec: AttachSpec{
				Container: "cspace-demo-mercury",
				Session:   SessionClaude,
				Command:   []string{"claude", "--dangerously-skip-permissions"},
			},
			want: []string{
				"container", "exec", "-it",
				"cspace-demo-mercury",
				"tmux", "-f", "/usr/local/etc/cspace-tmux.conf",
				"new-session", "-A", "-s", "cspace-claude", "-c", "/workspace",
				"claude", "--dangerously-skip-permissions",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bin, argv, err := AttachArgv(tc.spec)
			if err != nil {
				// container may not be on PATH in CI.
				t.Skipf("container CLI not resolvable: %v", err)
			}
			if !strings.HasSuffix(bin, "container") {
				t.Errorf("bin = %q, want it to resolve the container binary", bin)
			}
			if len(argv) != len(tc.want) {
				t.Fatalf("argv = %v, want %v", argv, tc.want)
			}
			for i := range tc.want {
				if argv[i] != tc.want[i] {
					t.Errorf("argv[%d] = %q, want %q", i, argv[i], tc.want[i])
				}
			}
		})
	}
}

func TestAttachArgvRejectsEmptyInput(t *testing.T) {
	if _, _, err := AttachArgv(AttachSpec{Command: []string{"claude"}}); err == nil {
		t.Error("empty container name was accepted")
	}
	if _, _, err := AttachArgv(AttachSpec{Container: "c"}); err == nil {
		t.Error("empty command was accepted")
	}
}

// TestClaudeAttach — the interactive path forwards the terminal that is
// actually attaching, and only asks for tmux when the sandbox has it.
func TestClaudeAttach(t *testing.T) {
	t.Setenv("TERM", "xterm-ghostty")
	t.Setenv("COLORTERM", "truecolor")

	withTmux := ClaudeAttach("cspace-demo-mercury", true)
	if withTmux.Session != SessionClaude {
		t.Errorf("Session = %q, want %q", withTmux.Session, SessionClaude)
	}
	if withTmux.TERM != "xterm-ghostty" || withTmux.COLORTERM != "truecolor" {
		t.Errorf("terminal not read from the environment: %+v", withTmux)
	}
	wantCmd := []string{"claude", "--dangerously-skip-permissions"}
	if len(withTmux.Command) != len(wantCmd) {
		t.Fatalf("Command = %v, want %v", withTmux.Command, wantCmd)
	}
	for i := range wantCmd {
		if withTmux.Command[i] != wantCmd[i] {
			t.Errorf("Command[%d] = %q, want %q", i, withTmux.Command[i], wantCmd[i])
		}
	}

	without := ClaudeAttach("cspace-demo-mercury", false)
	if without.Session != "" {
		t.Errorf("Session = %q, want empty when the image has no tmux", without.Session)
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
			got := TerminalEnv(tc.term, tc.colorterm)
			if len(got) != len(tc.want) {
				t.Fatalf("TerminalEnv(%q, %q) = %v, want %v", tc.term, tc.colorterm, got, tc.want)
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
	got := TerminalEnvArgs("xterm-ghostty", "truecolor")
	want := []string{"-e", "COLORTERM=truecolor", "-e", "TERM=xterm-256color"}
	if len(got) != len(want) {
		t.Fatalf("TerminalEnvArgs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if len(TerminalEnvArgs("dumb", "")) != 0 {
		t.Error("dumb terminal produced -e flags")
	}
}

// TestApplyTerminalEnvNeverOverridesExisting — the baked values are a default
// for a container that would otherwise be told TERM=xterm. Anything the
// project or the user set explicitly (devcontainer containerEnv, --env) is a
// deliberate choice and outranks it.
func TestApplyTerminalEnvNeverOverridesExisting(t *testing.T) {
	env := map[string]string{"TERM": "screen"}
	ApplyTerminalEnv(env, "xterm-ghostty", "truecolor")

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
	ApplyTerminalEnv(env, "xterm-ghostty", "truecolor")

	if env["TERM"] != "xterm-256color" || env["COLORTERM"] != "truecolor" {
		t.Errorf("env = %v, want TERM=xterm-256color COLORTERM=truecolor", env)
	}
}

// TestApplyTerminalEnvHeadlessBootAddsNothing — `cspace up` from a cron job or
// a pipe has no terminal to describe, and inventing one would put escape codes
// into captured output.
func TestApplyTerminalEnvHeadlessBootAddsNothing(t *testing.T) {
	env := map[string]string{}
	ApplyTerminalEnv(env, "", "")

	if len(env) != 0 {
		t.Errorf("env = %v, want it untouched", env)
	}
}
