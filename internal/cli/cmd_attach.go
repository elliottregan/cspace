package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/elliottregan/cspace/internal/control"
	"github.com/spf13/cobra"
)

// defaultTmux is the process-wide tmux driver: it memoizes the per-sandbox
// presence probe, so the first attach pays for it and nothing else does.
var defaultTmux = control.NewTmux()

func newAttachCmd() *cobra.Command {
	var noTmux bool

	cmd := &cobra.Command{
		Use:   "attach <name>",
		Short: "Open an interactive Claude Code session inside a running sandbox",
		Long: `Drop into an interactive ` + "`claude`" + ` session running inside the named
sandbox. Workspace is /workspace; your turns and the agent's output
appear in your terminal directly.

The session runs inside a tmux session in the sandbox, so closing this
window leaves it running and the next ` + "`cspace attach`" + ` rejoins it
with its screen intact.

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
			return attachInteractive(cmd.Context(), cmd.ErrOrStderr(), project, name, containerName, !noTmux)
		},
	}

	// Undocumented on purpose (the design's open question 3): it exists only
	// until no sandbox image without tmux is in use, and then it goes.
	cmd.Flags().BoolVar(&noTmux, "no-tmux", false,
		"attach without tmux; the session does not survive this window closing")
	_ = cmd.Flags().MarkHidden("no-tmux")
	return cmd
}

// attachInteractive runs `container exec -it … tmux new-session -A …` as a
// foreground child and, when it ends, detaches the tmux client it created.
//
// It used to syscall.Exec, which was simpler and wrong. A dead host side
// never reaches the guest: the exec'd `claude` was still alive 30 s after its
// host terminal closed, and with tmux the client it left behind stays
// attached indefinitely. Running the exec as a child is what leaves a process
// alive to do the detach — so cspace stays in place, forwards the terminal's
// signals, and exits with the child's status.
// (cs-finding:2026-09-17-attach-orphans-claude-when-the-host-terminal-closes)
func attachInteractive(ctx context.Context, warn io.Writer, project, sandbox, containerName string, wantTmux bool) error {
	useTmux := false
	if wantTmux {
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		useTmux = defaultTmux.Present(probeCtx, containerName)
		cancel()
		if !useTmux {
			_, _ = fmt.Fprintf(warn,
				"warning: this sandbox has no tmux, so the session will not survive this window closing — and `claude` will keep running inside the sandbox when it does. Rebuild the image with `cspace image build`, then `cspace down %s && cspace up %s`.\n",
				sandbox, sandbox)
		}
	}

	spec := control.ClaudeAttach(containerName, useTmux)
	bin, argv, err := control.AttachArgv(spec)
	if err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	att, err := control.BeginAttach(ctx, defaultTmux, containerName,
		control.ControlPlaneDir(home, project, sandbox), spec.Session)
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

	code, runErr := runAttachChild(bin, argv)

	// The detach gets its own context: the caller's may already be cancelled
	// by whatever ended the session, and this is the one thing that must
	// still run.
	detachCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if closeErr := att.Close(detachCtx); closeErr != nil {
		_, _ = fmt.Fprintf(warn, "warning: detaching this sandbox's tmux client failed: %v\n", closeErr)
	}

	if runErr != nil {
		return runErr
	}
	if code != 0 {
		return ExitError{Code: code}
	}
	return nil
}

// runAttachChild runs the attach argv wired straight to this process's
// terminal and reports the child's exit status.
//
// The child shares stdin/stdout/stderr — the real tty — so `container exec
// -it` puts that terminal into raw mode itself and keystrokes reach the guest
// as bytes rather than as host-side signals. exec.Command is not given a
// Setpgid, so the child stays in cspace's own process group and controlling
// tty: the kernel delivers Ctrl-C (SIGINT) and window resizes (SIGWINCH) to
// that whole foreground process group directly, the child included, so
// relaying either one here would only deliver it twice. SIGINT is still in
// the Notify set below, but only so Go's default handling — which would kill
// cspace outright — doesn't fire before the child exits and Close can run;
// once received it is otherwise ignored. SIGTERM is forwarded because it
// arrives by pid, so only cspace gets it and the child would never see it
// otherwise. SIGHUP means the terminal itself is gone: the child is signalled
// and, after a grace period, killed, so Wait returns and the caller's detach
// of the tmux client it left behind still runs while the container is
// reachable.
func runAttachChild(bin string, argv []string) (int, error) {
	child := exec.Command(bin, argv[1:]...)
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr

	sigs := make(chan os.Signal, 8)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)

	if err := child.Start(); err != nil {
		return 0, fmt.Errorf("start %s: %w", bin, err)
	}

	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case sig := <-sigs:
				switch sig {
				case syscall.SIGHUP:
					// Nothing can be typed into the child any more. Ask it to
					// go, then insist, so Wait returns and the detach runs
					// while the container is still reachable.
					_ = child.Process.Signal(syscall.SIGHUP)
					time.AfterFunc(2*time.Second, func() { _ = child.Process.Kill() })
				case syscall.SIGINT:
					// The kernel already delivered this to the child
					// directly (same process group, same controlling tty).
					// Nothing to relay — this case exists only to keep
					// receiving it above from killing cspace.
				default:
					// SIGTERM: arrives by pid, so cspace has to pass it on.
					_ = child.Process.Signal(sig)
				}
			}
		}
	}()

	err := child.Wait()
	close(done)

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if err != nil {
		return 0, fmt.Errorf("attach to sandbox: %w", err)
	}
	return 0, nil
}
