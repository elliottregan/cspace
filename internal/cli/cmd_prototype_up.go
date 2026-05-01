package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/secrets"
	"github.com/elliottregan/cspace/internal/substrate"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
	"github.com/spf13/cobra"
)

const supervisorPort = 6201

func newPrototypeUpCmd() *cobra.Command {
	var workspaceMount string
	cmd := &cobra.Command{
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

			// Ensure the host registry-daemon is running so in-sandbox cspace can resolve siblings.
			if err := ensureRegistryDaemon(); err != nil {
				return fmt.Errorf("registry daemon: %w", err)
			}

			token := randHex(16)

			env := map[string]string{
				"CSPACE_CONTROL_PORT":  fmt.Sprintf("%d", supervisorPort),
				"CSPACE_CONTROL_TOKEN": token,
				"CSPACE_PROJECT":       project,
				"CSPACE_SANDBOX_NAME":  name,
				"CSPACE_CLAUDE_PATH":   "/usr/local/bin/claude",
				"CSPACE_REGISTRY_URL":  "http://192.168.64.1:6280",
				"CSPACE_HOST_GATEWAY":  "192.168.64.1",
			}

			// Load cspace-owned secrets from ~/.cspace/secrets.env and
			// <project>/.cspace/secrets.env. Project-local overrides global.
			projectRoot := ""
			if cfg != nil {
				projectRoot = cfg.ProjectRoot
			}
			loaded, err := secrets.Load(projectRoot)
			if err != nil {
				return fmt.Errorf("load secrets: %w", err)
			}
			for k, v := range loaded {
				env[k] = v
			}
			// Host shell env wins for explicitly-set keys (e.g. one-off override).
			if k := os.Getenv("ANTHROPIC_API_KEY"); k != "" {
				env["ANTHROPIC_API_KEY"] = k
			}
			// Claude Code reads ANTHROPIC_API_KEY for both API keys (sk-ant-api…)
			// and long-lived OAuth tokens (sk-ant-oat…). Users typically have
			// the OAuth token under the name CLAUDE_CODE_OAUTH_TOKEN (matches
			// `claude setup-token` output). Alias it onto ANTHROPIC_API_KEY
			// when the latter isn't already set, so either name works.
			if env["ANTHROPIC_API_KEY"] == "" {
				if t := env["CLAUDE_CODE_OAUTH_TOKEN"]; t != "" {
					env["ANTHROPIC_API_KEY"] = t
				}
			}

			containerName := fmt.Sprintf("cspace-%s-%s", project, name)

			spec := substrate.RunSpec{
				Name:  containerName,
				Image: "cspace-prototype:latest",
				Env:   env,
			}
			// P0 workspace mount (POC for the per-sandbox-clone design):
			// when --workspace <host-path> is given, bind-mount it as /workspace
			// so the agent sees a normal main worktree of a git clone. P1 will
			// own the clone provisioning end-to-end inside cspace2-up; for now
			// the spike script handles `git clone` outside this command.
			if workspaceMount != "" {
				abs, err := filepath.Abs(workspaceMount)
				if err != nil {
					return fmt.Errorf("resolve --workspace path: %w", err)
				}
				spec.Mounts = append(spec.Mounts, substrate.Mount{
					HostPath:      abs,
					ContainerPath: "/workspace",
				})
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
	cmd.Flags().StringVar(&workspaceMount, "workspace", "",
		"host path to bind-mount as /workspace (typically a per-sandbox git clone)")
	return cmd
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

// projectName returns the current project's name. Resolution order:
//  1. $CSPACE_PROJECT env var (set inside sandboxes by prototype-up so the
//     in-sandbox cspace binary resolves the same project key the host used
//     when it registered the sibling).
//  2. cfg.Project.Name from a loaded .cspace.json.
//  3. "default" when neither is available.
//
// It is the single fix-up point for the prototype-* commands.
func projectName() string {
	if p := os.Getenv("CSPACE_PROJECT"); p != "" {
		return p
	}
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

// ensureRegistryDaemon starts cspace-registry-daemon on 127.0.0.1:6280 if it
// is not already accepting connections. It is idempotent — concurrent
// prototype-up calls that race here will at most spawn one extra daemon, and
// only one will manage to bind the port; the others exit immediately.
//
// P0: the daemon is left running until manually killed (no idle shutdown,
// no stop subcommand). Tracked for P1.
func ensureRegistryDaemon() error {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:6280", time.Second)
	if err == nil {
		conn.Close()
		return nil
	}
	bin, err := exec.LookPath("cspace-registry-daemon")
	if err != nil {
		// Fall back to the local build output.
		bin = "./bin/cspace-registry-daemon"
	}
	c := exec.Command(bin)
	c.Stdout, c.Stderr = nil, nil
	if err := c.Start(); err != nil {
		return err
	}
	// Daemon takes ~250ms to bind in practice. Wait for the port to actually accept connections.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:6280", 250*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("daemon started but not accepting connections")
}
