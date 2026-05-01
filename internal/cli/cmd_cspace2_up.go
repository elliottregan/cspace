package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/secrets"
	"github.com/elliottregan/cspace/internal/substrate"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
	"github.com/spf13/cobra"
)

const supervisorPort = 6201

func newCspace2UpCmd() *cobra.Command {
	var workspaceMount string
	var extraEnv []string
	var baseBranch string
	var withBrowser bool
	cmd := &cobra.Command{
		Use:   "cspace2-up <name>",
		Short: "Launch a sandbox (Apple Container substrate)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) (err error) {
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
			if err := a.HealthCheck(ctx); err != nil {
				return fmt.Errorf("apple container: %w. Run `container system start` and try again", err)
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
			// CLI --env flag wins over secrets file (used for spike-test
			// injection like CSPACE_BROWSER_CDP_URL).
			for _, kv := range extraEnv {
				eq := strings.Index(kv, "=")
				if eq < 1 {
					return fmt.Errorf("--env value %q must be KEY=VALUE", kv)
				}
				env[kv[:eq]] = kv[eq+1:]
			}
			// Anthropic credential family. Claude Code SDK reads ANTHROPIC_API_KEY,
			// but users typically have the value under CLAUDE_CODE_OAUTH_TOKEN
			// (the name `claude /login` writes to Keychain and what Task A's
			// auto-discovery layer fills). Either name works as the carrier.
			propagateFamily(env, []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"})

			// GitHub credential family. gh CLI reads GH_TOKEN; the GitHub MCP
			// server reads GITHUB_PERSONAL_ACCESS_TOKEN; Actions ambient is
			// GITHUB_TOKEN. Same value under all three so any tool sees its
			// expected name.
			propagateFamily(env, []string{"GH_TOKEN", "GITHUB_TOKEN", "GITHUB_PERSONAL_ACCESS_TOKEN"})

			// Host shell env wins for explicitly-set keys (e.g. one-off override).
			if k := os.Getenv("ANTHROPIC_API_KEY"); k != "" {
				env["ANTHROPIC_API_KEY"] = k
			}
			if k := os.Getenv("GH_TOKEN"); k != "" {
				env["GH_TOKEN"] = k
			}
			// Re-propagate after shell-env overrides so the family stays in
			// sync if shell env updated one alias.
			propagateFamily(env, []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"})
			propagateFamily(env, []string{"GH_TOKEN", "GITHUB_TOKEN", "GITHUB_PERSONAL_ACCESS_TOKEN"})

			// First-run nudge if no Anthropic credential is reachable from any
			// source (secrets file, shell env, alias propagation). Prints once
			// per user via a sentinel in ~/.cspace/. Sandbox still boots.
			maybeNudgeMissingAnthropicAuth(cmd.OutOrStdout(), env)

			containerName := fmt.Sprintf("cspace2-%s-%s", project, name)

			spec := substrate.RunSpec{
				Name:  containerName,
				Image: "cspace2:latest",
				Env:   env,
			}
			// Auto-provision a per-sandbox git clone unless the user supplied
			// --workspace explicitly (which acts as an override). The clone
			// lives at ~/.cspace/clones/<project>/<sandbox>/ and is checked
			// out as branch cspace/<sandbox>. See finding
			// 2026-05-01-per-sandbox-git-clone-bind-mounted-as-workspace-works-as-des
			// for the locked design.
			if workspaceMount == "" {
				auto, err := provisionClone(projectRoot, project, name, baseBranch)
				if err != nil {
					return fmt.Errorf("provision workspace clone: %w", err)
				}
				if auto != "" {
					workspaceMount = auto
					fmt.Fprintf(cmd.OutOrStdout(),
						"workspace clone: %s (branch cspace/%s)\n", auto, name)
				} else if projectRoot != "" {
					fmt.Fprintln(cmd.OutOrStdout(),
						"warning: project root is not a git repo; sandbox /workspace will be empty")
				}
			}
			// Bind-mount the resolved workspace (auto-provisioned or explicit
			// --workspace override) as /workspace so the agent sees a normal
			// main worktree of a git clone.
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

			// --browser: start a Playwright sidecar before launching the sandbox
			// so we can inject CSPACE_BROWSER_CDP_URL into spec.Env. The
			// supervisor's claude-runner.ts reads this and registers
			// playwright-mcp via --cdp-endpoint, giving the agent browser
			// tools without bundling a browser into the cspace2 image.
			//
			// On any subsequent error (substrate run, IP capture, registry
			// write, …) we tear the sidecar down via the deferred cleanup
			// below so we don't leak containers.
			var browserContainer string
			if withBrowser {
				cName, cdpURL, berr := startBrowserSidecar(ctx, project, name)
				if berr != nil {
					return fmt.Errorf("browser sidecar: %w", berr)
				}
				browserContainer = cName
				env["CSPACE_BROWSER_CDP_URL"] = cdpURL
				fmt.Fprintf(cmd.OutOrStdout(),
					"browser sidecar: %s (cdp %s)\n", cName, cdpURL)
				defer func() {
					if err != nil {
						stopBrowserSidecar(context.Background(), cName)
					}
				}()
			}

			if runErr := a.Run(ctx, spec); runErr != nil {
				err = fmt.Errorf("substrate run: %w", runErr)
				return err
			}

			// Container IP is assigned at run time; poll briefly until non-empty.
			ip, ipErr := waitForIP(ctx, a, containerName, 10*time.Second)
			if ipErr != nil {
				_ = a.Stop(context.Background(), containerName)
				err = fmt.Errorf("acquire container IP: %w", ipErr)
				return err
			}

			ctlURL := fmt.Sprintf("http://%s:%d", ip, supervisorPort)

			path, pathErr := registry.DefaultPath()
			if pathErr != nil {
				_ = a.Stop(context.Background(), containerName)
				err = pathErr
				return err
			}
			r := &registry.Registry{Path: path}
			if regErr := r.Register(registry.Entry{
				Project:          project,
				Name:             name,
				ControlURL:       ctlURL,
				Token:            token,
				IP:               ip,
				StartedAt:        time.Now().UTC(),
				BrowserContainer: browserContainer,
			}); regErr != nil {
				_ = a.Stop(context.Background(), containerName)
				err = regErr
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(),
				"sandbox %s up: control %s  ip %s  token %s…\n",
				name, ctlURL, ip, token[:8])
			return nil
		},
	}
	cmd.Flags().StringVar(&workspaceMount, "workspace", "",
		"host path to bind-mount as /workspace (overrides auto-provisioned per-sandbox clone)")
	cmd.Flags().StringArrayVar(&extraEnv, "env", nil,
		"extra KEY=VALUE env vars to inject into the sandbox (repeatable)")
	cmd.Flags().StringVar(&baseBranch, "base", "",
		"base branch for the auto-provisioned cspace/<sandbox> branch (defaults to host project's current HEAD)")
	cmd.Flags().BoolVar(&withBrowser, "browser", false,
		"start a Playwright browser sidecar; agent's playwright-mcp connects via CDP")
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
//  1. $CSPACE_PROJECT env var (set inside sandboxes by cspace2-up so the
//     in-sandbox cspace binary resolves the same project key the host used
//     when it registered the sibling).
//  2. cfg.Project.Name from a loaded .cspace.json.
//  3. "default" when neither is available.
//
// It is the single fix-up point for the cspace2-* commands.
func projectName() string {
	if p := os.Getenv("CSPACE_PROJECT"); p != "" {
		return p
	}
	if cfg != nil && cfg.Project.Name != "" {
		return cfg.Project.Name
	}
	return "default"
}

// propagateFamily ensures every name in `family` has the same value as the
// first non-empty entry. If no entry has a value, the family is left empty.
//
// Used to make a single user-supplied credential satisfy all the env-var
// names different tools look for — e.g. one GH_TOKEN supplies gh CLI, the
// GitHub MCP server, and any tool that reads GITHUB_TOKEN ambient.
func propagateFamily(env map[string]string, family []string) {
	var value string
	for _, name := range family {
		if v := env[name]; v != "" {
			value = v
			break
		}
	}
	if value == "" {
		return
	}
	for _, name := range family {
		env[name] = value
	}
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ensureRegistryDaemon starts cspace-registry-daemon on 127.0.0.1:6280 if it
// is not already accepting connections. It is idempotent — concurrent
// cspace2-up calls that race here will at most spawn one extra daemon, and
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

// nudgeSentinelName is the per-user marker file that records "the no-auth
// nudge has already been shown". Lives in ~/.cspace/. Once it exists the
// nudge stays silent forever — the message has done its job.
const nudgeSentinelName = ".no-claude-auth-nudge-shown"

// maybeNudgeMissingAnthropicAuth prints a one-time hint when no Anthropic
// credential is reachable in env. Gated by a sentinel file in ~/.cspace/ so
// it fires at most once per user. Failure to write the sentinel is swallowed
// — the nudge already printed and a future re-print is harmless.
func maybeNudgeMissingAnthropicAuth(out io.Writer, env map[string]string) {
	if env["ANTHROPIC_API_KEY"] != "" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	cspaceDir := filepath.Join(home, ".cspace")
	sentinel := filepath.Join(cspaceDir, nudgeSentinelName)
	if _, err := os.Stat(sentinel); err == nil {
		// Already shown.
		return
	}
	fmt.Fprintln(out, "note: no Anthropic credential reachable. Run `cspace keychain init` to set one up")
	fmt.Fprintln(out, "      (or set ANTHROPIC_API_KEY in ~/.cspace/secrets.env). Sandbox will boot,")
	fmt.Fprintln(out, "      but Claude SDK calls will fail until auth is configured.")
	if err := os.MkdirAll(cspaceDir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(sentinel, []byte("shown\n"), 0o644)
}
