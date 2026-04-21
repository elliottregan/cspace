package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/elliottregan/cspace/internal/instance"
	"github.com/elliottregan/cspace/internal/tui"
)

func newWatchCmd() *cobra.Command {
	var addr string
	var inside bool

	cmd := &cobra.Command{
		Use:     "watch [name]",
		Short:   "Live TUI dashboard for running agents and services",
		GroupID: "supervisor",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if inside {
				return runWatchInside(addr)
			}
			return runWatchHost(args)
		},
	}

	cmd.Flags().StringVar(&addr, "addr", "localhost:8384", "Diagnostics server address")
	cmd.Flags().BoolVar(&inside, "inside", false, "Run TUI inside container (set by host wrapper)")
	_ = cmd.Flags().MarkHidden("inside")
	return cmd
}

func runWatchHost(args []string) error {
	var target string
	if len(args) > 0 {
		target = args[0]
	} else {
		names, _ := instance.GetInstances(cfg)
		for _, name := range names {
			if instance.IsRunning(cfg.ComposeName(name)) {
				target = name
				break
			}
		}
	}
	if target == "" {
		return fmt.Errorf("no running instances for project %q — start one with: cspace up", cfg.Project.Name)
	}
	composeName := cfg.ComposeName(target)
	if err := instance.RequireRunning(composeName, target); err != nil {
		return err
	}
	return instance.DcExecInteractive(composeName, "cspace", "watch", "--inside")
}

func runWatchInside(addr string) error {
	if err := ensureDiagnosticsServer(addr); err != nil {
		return err
	}
	m := tui.NewModel(addr)
	p := tea.NewProgram(m, tea.WithAltScreen())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tui.NewWSClient(addr, p).Run(ctx)

	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				p.Send(tui.ServicesUpdatedMsg(tui.ProbeSharedServices()))
			}
		}
	}()

	_, err := p.Run()
	return err
}

func ensureDiagnosticsServer(addr string) error {
	if serverAlive(addr) {
		return nil
	}
	bin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not locate cspace binary: %w", err)
	}
	srv := exec.Command(bin, "diagnostics-server")
	srv.Stdout = io.Discard
	srv.Stderr = io.Discard
	if err := srv.Start(); err != nil {
		return fmt.Errorf("could not start diagnostics server: %w", err)
	}
	// Deliberately not tracking srv — the diagnostics server outlives this TUI
	// session so subsequent `cspace watch` invocations can reuse it.
	_ = srv.Process.Release()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if serverAlive(addr) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("diagnostics server at %s did not start in time", addr)
}

var healthClient = &http.Client{Timeout: time.Second}

func serverAlive(addr string) bool {
	resp, err := healthClient.Get("http://" + addr + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}
