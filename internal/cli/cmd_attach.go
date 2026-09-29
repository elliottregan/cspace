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
	var request control.AttachRequest

	cmd := &cobra.Command{
		Use:   "attach <name>",
		Short: "Open an interactive Claude Code session inside a running sandbox",
		Long: `Drop into an interactive ` + "`claude`" + ` session running inside the named
sandbox. Workspace is /workspace; your turns and the agent's output
appear in your terminal directly.

The session runs inside a tmux session in the sandbox, so closing this
window leaves it running and the next ` + "`cspace attach`" + ` rejoins it
with its screen intact.

Use --new to create another independent Claude session in this sandbox.
Use --session cspace-claude-2 to join that session; an existing session
must still be running. The default session is named cspace-claude.

This is independent of the supervisor's autonomous session — they
share the same /workspace but are separate Claude Code sessions
with separate context. Use ` + "`cspace send`" + ` to inject turns into the
supervisor's session non-interactively; use ` + "`cspace attach`" + ` for
hands-on work.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := request.Validate(); err != nil {
				return err
			}
			if cmd.Flags().Changed("session") && request.Session == "" {
				return fmt.Errorf("--session requires a session name")
			}
			if err := ensureRegistryDaemon(); err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: cspace daemon not reachable: %v\n", err)
			}

			name := args[0]
			project := projectName()
			containerName := fmt.Sprintf("cspace-%s-%s", project, name)
			return attachInteractiveRequest(cmd.Context(), cmd.ErrOrStderr(), project, name, containerName, !noTmux, request)
		},
	}

	// Undocumented on purpose (the design's open question 3): it exists only
	// until no sandbox image without tmux is in use, and then it goes.
	cmd.Flags().BoolVar(&noTmux, "no-tmux", false,
		"attach without tmux; the session does not survive this window closing")
	_ = cmd.Flags().MarkHidden("no-tmux")
	cmd.Flags().BoolVar(&request.New, "new", false, "create a new independent Claude session")
	cmd.Flags().StringVar(&request.Session, "session", "", "join an existing Claude session by name")
	cmd.MarkFlagsMutuallyExclusive("new", "session")
	cmd.MarkFlagsMutuallyExclusive("new", "no-tmux")
	cmd.MarkFlagsMutuallyExclusive("session", "no-tmux")
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
	return attachInteractiveRequest(ctx, warn, project, sandbox, containerName, wantTmux, control.AttachRequest{})
}

func attachInteractiveRequest(ctx context.Context, warn io.Writer, project, sandbox, containerName string, wantTmux bool, request control.AttachRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	managed := request.New || request.Session != ""
	if managed && !wantTmux {
		return control.ErrTmuxRequired
	}
	useTmux := false
	warned := false
	if wantTmux {
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		present, presentErr := defaultTmux.Present(probeCtx, containerName)
		cancel()
		if presentErr != nil {
			// A transport error means we could not learn whether tmux is
			// there, not that it isn't — falling back to a direct exec
			// would just hit the same transport failure a moment later, so
			// there is nothing useful to fall back to.
			return fmt.Errorf("cannot reach sandbox %s to probe for tmux: %w", sandbox, presentErr)
		}
		useTmux = present
		if managed && !useTmux {
			return control.ErrTmuxRequired
		}
		if !useTmux {
			_, _ = fmt.Fprintf(warn,
				"warning: this sandbox has no tmux, so the session will not survive this window closing — and `claude` will keep running inside the sandbox when it does. Rebuild the image with `cspace image build`, then `cspace down %s && cspace up %s`.\n",
				sandbox, sandbox)
			warned = true
		}
	}

	spec := control.ClaudeAttach(containerName, useTmux)
	bin, argv, err := control.AttachArgv(spec)
	if err != nil {
		return err
	}

	home, homeErr := os.UserHomeDir()
	var att *control.Attachment
	if managed {
		if homeErr != nil {
			return fmt.Errorf("resolve home directory: %w", homeErr)
		}
		prepareCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		prepared, prepareErr := control.PrepareClaudeAttach(prepareCtx, defaultTmux, home, project, sandbox, containerName, request)
		cancel()
		if prepareErr != nil {
			return prepareErr
		}
		att = prepared.Attachment
		bin, argv, err = control.AttachArgv(prepared.Spec)
		if err != nil {
			_ = att.Close(ctx)
			return err
		}
	} else {
		var bookkeepingWarned bool
		att, bookkeepingWarned, err = beginAttachOrWarn(ctx, warn, defaultTmux, home, homeErr, project, sandbox, containerName, spec.Session)
		if err != nil {
			return err
		}
		warned = warned || bookkeepingWarned
	}

	// Skip the reset whenever this attach has already printed a warning to
	// `warn` (the no-tmux fallback above, or beginAttachOrWarn's bookkeeping
	// warning): \033c clears scrollback too, and claude's immediate repaint
	// would erase the warning before the user has a chance to read it. Still
	// reset when nothing was printed — the ordinary tmux path, and the
	// explicit --no-tmux path (wantTmux is already false, so nothing above
	// runs).
	if isStdoutTTY() && !warned {
		_, _ = os.Stdout.WriteString("\033c")
	}

	// SIGINT/SIGTERM/SIGHUP must not kill cspace between here and the end of
	// Close: runAttachChild has its own signal.Notify covering only the
	// child's lifetime, and its deferred signal.Stop fires the moment the
	// child exits — exactly when Close's up-to-15s detach exec starts.
	// Without an overlapping registration here, one of those signals
	// arriving during that window would fall back to Go's default
	// disposition (terminate) and the tmux-client detach would never run.
	// This registration does not need to act on anything: runAttachChild's
	// own handler already forwards what needs forwarding while the child is
	// alive, and Close does not respond to host signals at all — it just
	// has to survive one. Multiple signal.Notify registrations for the same
	// signal coexist fine; this one is never drained, which is deliberate.
	sigs := make(chan os.Signal, 8)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)

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

// beginAttachOrWarn opens the attach's control-plane bookkeeping
// (control.BeginAttach) under the given home directory, downgrading two
// classes of failure to a one-line warning plus an inert attachment instead
// of refusing the whole attach:
//
//   - homeErr non-nil (home is then ignored) — resolving the host home
//     directory failed, so there is nowhere to put the lock/records at all.
//     Callers that already know their home directory (the dashboard resolves
//     it once at startup and refuses to launch if that fails) pass nil here.
//   - BeginAttach itself reporting control.ErrBookkeepingUnavailable — the
//     control-plane directory or its lock file could not be created/opened
//     (a permissions problem, a full disk).
//
// A busy lock (another attach's window still open) is not downgraded:
// BeginAttach reports that as a plain error and this still refuses, since
// guessing the wrong tty out from under a concurrent attach is exactly what
// the lock exists to prevent.
//
// tm is the tmux driver to book the attach against. `cspace attach` passes
// this package's process-wide defaultTmux; the dashboard passes its control
// Client's own driver, so the memoized presence probe and the exec transport
// are shared with every other query that Client makes rather than duplicated.
//
// The returned bool reports whether a warning was written to warn, so the
// caller can skip its post-attach screen reset rather than erase it.
func beginAttachOrWarn(ctx context.Context, warn io.Writer, tm *control.Tmux, home string, homeErr error, project, sandbox, container, session string) (*control.Attachment, bool, error) {
	const bookkeepingWarning = "warning: attach bookkeeping unavailable: %v; this session's tmux client will not be detached automatically\n"

	if homeErr != nil {
		_, _ = fmt.Fprintf(warn, bookkeepingWarning, homeErr)
		att, err := control.BeginAttach(ctx, tm, container, "", "")
		return att, true, err
	}

	dir := control.ControlPlaneDir(home, project, sandbox)
	att, err := control.BeginAttach(ctx, tm, container, dir, session)
	if err != nil {
		if errors.Is(err, control.ErrBookkeepingUnavailable) {
			_, _ = fmt.Fprintf(warn, bookkeepingWarning, err)
			att, err := control.BeginAttach(ctx, tm, container, "", "")
			return att, true, err
		}
		return nil, false, err
	}
	return att, false, nil
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
// otherwise. Both SIGTERM and SIGHUP request shutdown: the child is signalled
// and, after a grace period, killed, so Wait returns and the caller's detach
// of the tmux client it left behind still runs while the container is
// reachable. Apple Container's exec transport can ignore host SIGTERM.
func runAttachChild(bin string, argv []string) (int, error) {
	// `container exec -it` leaves O_NONBLOCK on the descriptors it was given,
	// and they are the shell's as much as ours — hand them back blocking so
	// nothing written to this terminal afterwards is silently truncated.
	defer restoreBlockingStreams(os.Stdin, os.Stdout, os.Stderr)

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
	signalsDone := make(chan struct{})
	go func() {
		defer close(signalsDone)
		var deadline *time.Timer
		var forceKill <-chan time.Time
		defer func() {
			if deadline != nil {
				deadline.Stop()
			}
		}()
		for {
			select {
			case <-done:
				return
			case <-forceKill:
				_ = child.Process.Kill()
				forceKill = nil
			case sig := <-sigs:
				switch sig {
				case syscall.SIGHUP, syscall.SIGTERM:
					// Bound host transport shutdown so its guest tmux client
					// can be detached. Repeated signals must not extend the
					// deadline or start additional timers.
					_ = child.Process.Signal(sig)
					if deadline == nil {
						deadline = time.NewTimer(2 * time.Second)
						forceKill = deadline.C
					}
				case syscall.SIGINT:
					// The kernel already delivered this to the child
					// directly (same process group, same controlling tty).
					// Nothing to relay — this case exists only to keep
					// receiving it above from killing cspace.
				}
			}
		}
	}()

	err := child.Wait()
	close(done)
	<-signalsDone

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if err != nil {
		return 0, fmt.Errorf("attach to sandbox: %w", err)
	}
	return 0, nil
}
