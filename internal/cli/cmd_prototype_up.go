package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
	"github.com/spf13/cobra"
)

const supervisorPort = 6201

func newPrototypeUpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prototype-up <name>",
		Short: "P0: launch a prototype sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			project := projectName()

			parent := cmd.Context()
			if parent == nil {
				parent = context.Background()
			}
			ctx, cancel := context.WithTimeout(parent, 90*time.Second)
			defer cancel()

			a := applecontainer.New()
			if !a.Available() {
				return fmt.Errorf("apple `container` CLI not on PATH; install per Task 1")
			}

			token := randHex(16)

			env := map[string]string{
				"CSPACE_CONTROL_PORT":  fmt.Sprintf("%d", supervisorPort),
				"CSPACE_CONTROL_TOKEN": token,
				"CSPACE_PROJECT":       project,
				"CSPACE_SANDBOX_NAME":  name,
				"CSPACE_CLAUDE_PATH":   "/usr/local/bin/claude",
			}
			// If the host has an Anthropic API key, propagate it so Claude can authenticate.
			if k := os.Getenv("ANTHROPIC_API_KEY"); k != "" {
				env["ANTHROPIC_API_KEY"] = k
			}

			containerName := fmt.Sprintf("cspace-%s-%s", project, name)

			spec := substrate.RunSpec{
				Name:  containerName,
				Image: "cspace-prototype:latest",
				Env:   env,
			}
			if err := a.Run(ctx, spec); err != nil {
				return fmt.Errorf("substrate run: %w", err)
			}

			// Container IP is assigned at run time; poll briefly until non-empty.
			ip, err := waitForIP(ctx, a, containerName, 10*time.Second)
			if err != nil {
				_ = a.Stop(context.Background(), containerName)
				return fmt.Errorf("acquire container IP: %w", err)
			}

			ctlURL := fmt.Sprintf("http://%s:%d", ip, supervisorPort)

			path, err := registry.DefaultPath()
			if err != nil {
				return err
			}
			r := &registry.Registry{Path: path}
			if err := r.Register(registry.Entry{
				Project:    project,
				Name:       name,
				ControlURL: ctlURL,
				Token:      token,
				IP:         ip,
				StartedAt:  time.Now().UTC(),
			}); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(),
				"sandbox %s up: control %s  ip %s  token %s…\n",
				name, ctlURL, ip, token[:8])
			return nil
		},
	}
}

// waitForIP polls a.IP until it returns a non-empty value or the deadline passes.
func waitForIP(ctx context.Context, a *applecontainer.Adapter, name string, max time.Duration) (string, error) {
	deadline := time.Now().Add(max)
	for {
		ip, err := a.IP(ctx, name)
		if err == nil && ip != "" {
			return ip, nil
		}
		if time.Now().After(deadline) {
			if err == nil {
				err = fmt.Errorf("ip empty")
			}
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// projectName returns the current project's name from loaded config, or
// "default" when no .cspace.json has been loaded (e.g. invoked outside an
// initialized project). It is the single fix-up point for the prototype-*
// commands.
func projectName() string {
	if cfg != nil && cfg.Project.Name != "" {
		return cfg.Project.Name
	}
	return "default"
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
