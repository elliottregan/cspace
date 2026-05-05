package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/elliottregan/cspace/internal/devcontainer"
	"github.com/elliottregan/cspace/internal/orchestrator"
	"github.com/elliottregan/cspace/internal/overlay"
	"github.com/elliottregan/cspace/internal/planets"
	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/runtime"
	"github.com/elliottregan/cspace/internal/secrets"
	"github.com/elliottregan/cspace/internal/substrate"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
	"github.com/spf13/cobra"
)

const supervisorPort = 6201

func newUpCmd() *cobra.Command {
	var workspaceMount string
	var extraEnv []string
	var baseBranch string
	var withBrowser bool
	var cpus int
	var memoryMiB int
	var noOverlay bool
	var noAttach bool
	cmd := &cobra.Command{
		Use:   "up [<name>]",
		Short: "Launch a sandbox (Apple Container substrate)",
		Long: `Launch a sandbox.

If <name> is omitted, cspace assigns the first unused planet name in
solar order (mercury, venus, earth, mars, jupiter, saturn, uranus,
neptune) for this project. Pass an explicit name for anything outside
that 8-deep convention — e.g. "issue-123" or "agent-alice".`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			project := projectName()
			var name string
			if len(args) == 1 {
				name = args[0]
			} else {
				picked, err := pickPlanetName(project)
				if err != nil {
					return err
				}
				name = picked
			}

			parent := cmd.Context()
			if parent == nil {
				parent = context.Background()
			}
			// 4 minutes covers the heaviest cold path: --browser sidecar
			// (apt-get install socat in the playwright image, ~30–60s)
			// + clone provision + plugin install (~30s for ~12 plugins)
			// + supervisor /health. Faster paths complete well under
			// this; this is just the worst-case ceiling.
			ctx, cancel := context.WithTimeout(parent, 4*time.Minute)
			defer cancel()

			a := applecontainer.New()
			if !a.Available() {
				return fmt.Errorf("apple `container` CLI not on PATH; install per Task 1")
			}
			// Version drift warning — non-fatal. The CLI is pre-1.0 with
			// occasional shape changes; if the user is on a version we
			// haven't tested, surface that BEFORE the more opaque parse
			// errors that may follow. If err is non-nil, Available() and
			// HealthCheck() will surface clearer messages, so we ignore.
			if version, supported, vErr := a.VersionStatus(ctx); vErr == nil && !supported {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"warning: cspace tested with Apple Container %s.x; you have %q. "+
						"Things may behave unexpectedly. File issues at "+
						"https://github.com/elliottregan/cspace/issues with the output of "+
						"`container --version`.\n",
					applecontainer.SupportedMinorVersion(), version)
			}
			if err := a.HealthCheck(ctx); err != nil {
				return fmt.Errorf("apple container: %w. Run `container system start` and try again", err)
			}

			// Boot from here on: route in-flight chatter to a buffer if
			// the planet overlay is going to run, so bubbletea isn't
			// fighting other writes for the terminal. Buffer is flushed
			// to real stdout after the overlay tears down. With
			// --no-overlay or piped stdout, output flows normally and
			// boot phases print as plain "[N/5] phase" lines.
			useOverlay := !noOverlay && isStdoutTTY()
			realOut := cmd.OutOrStdout()
			var pendingOut bytes.Buffer
			var rep overlay.Reporter
			var teaDone <-chan struct{}
			if useOverlay {
				cmd.SetOut(&pendingOut)
				rep, teaDone = overlay.Start(name)
			} else {
				rep = &overlay.LineReporter{Out: realOut}
			}
			// Always tear down the overlay before returning. On
			// success we Done() explicitly below; on error paths a
			// deferred Error() guarantees bubbletea exits.
			defer func() {
				if teaDone != nil {
					select {
					case <-teaDone:
						// already torn down by an earlier Done/Error
					default:
						if err != nil {
							rep.Error(err)
						} else {
							rep.Done()
						}
						<-teaDone
					}
					cmd.SetOut(realOut)
					_, _ = io.Copy(realOut, &pendingOut)
				}
			}()

			rep.Phase(overlay.PhaseDaemon)

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

			// Merged plugins config (defaults.json overlaid with project's
			// .cspace.json) goes into the sandbox as JSON so the in-
			// container install script can iterate it without re-loading
			// cspace's config layer. Schema:
			//   { "enabled": bool, "install": ["plugin", "plugin@market", ...] }
			// Bare plugin names default to @claude-plugins-official.
			if cfg != nil {
				if pluginsJSON, perr := json.Marshal(cfg.Plugins); perr == nil {
					env["CSPACE_PLUGINS_CONFIG"] = string(pluginsJSON)
				}
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

			// Devcontainer: load + validate if .devcontainer/devcontainer.json
			// is present. Resolves sandbox image and merges containerEnv.
			// devcontainerPlan is nil when no devcontainer.json is present
			// (existing .cspace.json-only projects work unchanged).
			var devcontainerPlan *devcontainer.Plan
			sandboxImage := "cspace:latest"
			if projectRoot != "" {
				dcPath := filepath.Join(projectRoot, ".devcontainer", "devcontainer.json")
				if _, statErr := os.Stat(dcPath); statErr == nil {
					dc, loadErr := devcontainer.Load(dcPath)
					if loadErr != nil {
						return fmt.Errorf("devcontainer.json: %w", loadErr)
					}
					plan, mergeErr := devcontainer.Merge(dc, filepath.Dir(dcPath))
					if mergeErr != nil {
						return fmt.Errorf("devcontainer.json: %w", mergeErr)
					}
					devcontainerPlan = plan
					sandboxImage = resolveSandboxImage(ctx, plan, "cspace:latest")
					// containerEnv merges into env: devcontainer values override
					// defaults but lose to secrets file + --env (see below).
					for k, v := range dc.ContainerEnv {
						env[k] = v
					}
				}
			}

			// Devcontainer postCreateCommand and postStartCommand. These are
			// set on the env so the entrypoint can invoke them at boot.
			if devcontainerPlan != nil && devcontainerPlan.Devcontainer != nil {
				if cmd := joinPostCmd(devcontainerPlan.Devcontainer.PostCreateCommand); cmd != "" {
					env["CSPACE_POSTCREATE_CMD"] = cmd
				}
				if cmd := joinPostCmd(devcontainerPlan.Devcontainer.PostStartCommand); cmd != "" {
					env["CSPACE_POSTSTART_CMD"] = cmd
				}
				// Tell the entrypoint to wait for /sessions/extracted.env
				// before running postCreate. cspace's orchestrator-side
				// write of that file races against the entrypoint's early
				// source line; without this signal, postCreate may run with
				// captured creds still unset.
				if len(devcontainerPlan.Devcontainer.Customizations.Cspace.ExtractCredentials) > 0 {
					env["CSPACE_EXTRACT_CREDENTIALS_EXPECTED"] = "1"
				}
			}

			// Devcontainer customizations.cspace overrides. When a devcontainer.json
			// is present, customizations.cspace.{Resources, Plugins, FirewallDomains}
			// override the corresponding .cspace.json fields.
			if devcontainerPlan != nil && devcontainerPlan.Devcontainer != nil {
				cs := devcontainerPlan.Devcontainer.Customizations.Cspace
				if cs.Resources != nil {
					if cs.Resources.CPUs > 0 {
						cfg.Resources.CPUs = cs.Resources.CPUs
					}
					if cs.Resources.MemoryMiB > 0 {
						cfg.Resources.MemoryMiB = cs.Resources.MemoryMiB
					}
				}
				if len(cs.Plugins) > 0 {
					cfg.Plugins.Install = cs.Plugins
				}
				if len(cs.FirewallDomains) > 0 {
					cfg.Firewall.Domains = append(cfg.Firewall.Domains, cs.FirewallDomains...)
				}
			}

			// Deprecation warnings: when devcontainer.json is present AND legacy
			// .cspace.json fields are non-empty, warn on stderr.
			if devcontainerPlan != nil {
				if cfg.Services != "" {
					fmt.Fprintln(cmd.ErrOrStderr(), "[cspace] warning: .cspace.json 'services' is ignored when .devcontainer/devcontainer.json is present. Migrate compose orchestration into devcontainer.json's dockerComposeFile field. See docs/migration-from-cspace-json.md")
				}
				if len(cfg.Container.Environment) > 0 || len(cfg.Container.Ports) > 0 || len(cfg.Container.Packages) > 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "[cspace] warning: .cspace.json 'container' block (ports/environment/packages) is ignored when devcontainer.json is present. Move env to containerEnv, ports to forwardPorts, packages to features. See docs/migration-from-cspace-json.md")
				}
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
			// Anthropic credentials: pass through ONLY what the user set.
			// Setting both ANTHROPIC_API_KEY and CLAUDE_CODE_OAUTH_TOKEN
			// trips claude CLI's "Auth conflict" warning every session,
			// and the SDK + CLI both accept either env var as the
			// carrier. So if the user's secrets file (or host shell) has
			// CLAUDE_CODE_OAUTH_TOKEN set, we leave ANTHROPIC_API_KEY
			// unset in the sandbox, and vice versa.
			if k := os.Getenv("ANTHROPIC_API_KEY"); k != "" {
				env["ANTHROPIC_API_KEY"] = k
			}
			if k := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); k != "" {
				env["CLAUDE_CODE_OAUTH_TOKEN"] = k
			}
			// If the user explicitly set ANTHROPIC_API_KEY in the secrets
			// file or host shell, drop CLAUDE_CODE_OAUTH_TOKEN to avoid
			// the conflict. If only CLAUDE_CODE_OAUTH_TOKEN was set,
			// drop ANTHROPIC_API_KEY for the same reason.
			if env["ANTHROPIC_API_KEY"] != "" && env["CLAUDE_CODE_OAUTH_TOKEN"] != "" {
				// Both are present from auto-discovery + secrets file.
				// Prefer whichever was set by the user explicitly (host
				// shell wins, then secrets file). For the common case
				// where the secrets file set CLAUDE_CODE_OAUTH_TOKEN
				// and auto-discovery filled ANTHROPIC_API_KEY, drop the
				// auto-discovered one.
				if loaded["CLAUDE_CODE_OAUTH_TOKEN"] != "" && loaded["ANTHROPIC_API_KEY"] == "" {
					delete(env, "ANTHROPIC_API_KEY")
				} else if loaded["ANTHROPIC_API_KEY"] != "" && loaded["CLAUDE_CODE_OAUTH_TOKEN"] == "" {
					delete(env, "CLAUDE_CODE_OAUTH_TOKEN")
				} else {
					// Default tiebreak: keep ANTHROPIC_API_KEY, drop OAuth
					// (the SDK and CLI both accept any value format under
					// ANTHROPIC_API_KEY).
					delete(env, "CLAUDE_CODE_OAUTH_TOKEN")
				}
			}

			// GitHub credential family. gh CLI reads GH_TOKEN; the GitHub MCP
			// server reads GITHUB_PERSONAL_ACCESS_TOKEN; Actions ambient is
			// GITHUB_TOKEN. Same value under all three so any tool sees its
			// expected name. (No conflict warning here, so the dual-write
			// pattern is safe.)
			propagateFamily(env, []string{"GH_TOKEN", "GITHUB_TOKEN", "GITHUB_PERSONAL_ACCESS_TOKEN"})

			// Host shell env wins for explicitly-set keys (e.g. one-off override).
			if k := os.Getenv("GH_TOKEN"); k != "" {
				env["GH_TOKEN"] = k
			}
			propagateFamily(env, []string{"GH_TOKEN", "GITHUB_TOKEN", "GITHUB_PERSONAL_ACCESS_TOKEN"})

			// First-run nudge if no Anthropic credential is reachable from any
			// source (secrets file, shell env, alias propagation). Prints once
			// per user via a sentinel in ~/.cspace/. Sandbox still boots.
			maybeNudgeMissingAnthropicAuth(cmd.OutOrStdout(), env)

			containerName := fmt.Sprintf("cspace-%s-%s", project, name)

			// Resource caps. Resolution: --cpus / --memory flag wins,
			// then .cspace.json `resources`, then the substrate adapter's
			// default (Apple Container: 4 CPU / 4096 MiB). Zero in the
			// RunSpec means "fall through to adapter default".
			effCPUs := cpus
			effMemMiB := memoryMiB
			if cfg != nil {
				if effCPUs == 0 {
					effCPUs = cfg.Resources.CPUs
				}
				if effMemMiB == 0 {
					effMemMiB = cfg.Resources.MemoryMiB
				}
			}

			spec := substrate.RunSpec{
				Name:      containerName,
				Image:     sandboxImage,
				Env:       env,
				CPUs:      effCPUs,
				MemoryMiB: effMemMiB,
			}
			// Auto-provision a per-sandbox git clone unless the user supplied
			// --workspace explicitly (which acts as an override). The clone
			// lives at ~/.cspace/clones/<project>/<sandbox>/ and is checked
			// out as branch cspace/<sandbox>. See finding
			// 2026-05-01-per-sandbox-git-clone-bind-mounted-as-workspace-works-as-des
			// for the locked design.
			rep.Phase(overlay.PhaseClone)
			if workspaceMount == "" {
				auto, err := provisionClone(projectRoot, project, name, baseBranch)
				if err != nil {
					return fmt.Errorf("provision workspace clone: %w", err)
				}
				if auto != "" {
					workspaceMount = auto
					_, _ = fmt.Fprintf(cmd.OutOrStdout(),
						"workspace clone: %s (branch cspace/%s)\n", auto, name)
				} else if projectRoot != "" {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(),
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

			// Sessions persistence — supervisor's events.ndjson plus Claude
			// Code's per-session JSONLs both live on the host so they survive
			// cspace down and enable transparent resume on the next
			// cspace up of the same sandbox name.
			//
			//   ~/.cspace/sessions/<project>/<sandbox>/
			//     primary/events.ndjson              <- supervisor stream
			//     claude-projects-workspace/         <- Claude Code SDK store
			//       sessions/<session_id>.jsonl
			//
			// The first dir is mounted at /sessions; the second at
			// /home/dev/.claude/projects/-workspace/. The "-workspace" name
			// is Claude Code's mangled form of the cwd "/workspace".
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("user home dir: %w", err)
			}
			sessionsHostDir := filepath.Join(home, ".cspace", "sessions", project, name)
			claudeProjectsHostDir := filepath.Join(sessionsHostDir, "claude-projects-workspace")
			if err := os.MkdirAll(claudeProjectsHostDir, 0o755); err != nil {
				return fmt.Errorf("create sessions dir: %w", err)
			}
			spec.Mounts = append(spec.Mounts,
				substrate.Mount{HostPath: sessionsHostDir, ContainerPath: "/sessions"},
				substrate.Mount{HostPath: claudeProjectsHostDir, ContainerPath: "/home/dev/.claude/projects/-workspace"},
			)

			// Auto-resume is handled inside the supervisor itself: it reads
			// /sessions/primary/events.ndjson at startup, finds the latest
			// SDK system/init session_id, and passes it to query()'s
			// `resume` option. That makes resume work uniformly across
			// fresh boot, restart-loop respawn, and cspace down +
			// cspace up cycles — no host-side env injection required.
			// See lib/agent-supervisor-bun/src/main.ts:resumeSessionId.

			// --browser: start a Playwright sidecar before launching the sandbox
			// so we can inject CSPACE_BROWSER_CDP_URL into spec.Env. The
			// supervisor's claude-runner.ts reads this and registers
			// playwright-mcp via --cdp-endpoint, giving the agent browser
			// tools without bundling a browser into the cspace image.
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
				_, _ = fmt.Fprintf(cmd.OutOrStdout(),
					"browser sidecar: %s (cdp %s)\n", cName, cdpURL)
				defer func() {
					if err != nil {
						stopBrowserSidecar(context.Background(), cName)
					}
				}()
			}

			// Materialize the runtime overlay tree (scripts, eventually supervisor)
			// to ~/.cspace/runtime/<Version>/ and bind-mount it at /opt/cspace inside
			// the microVM. This decouples cspace runtime upgrades from project image
			// rebuilds — script tweaks ship via the cspace binary, no docker pull.
			_, overlayPath, err := runtime.Extract(Version)
			if err != nil {
				return fmt.Errorf("extract runtime overlay: %w", err)
			}
			spec.RuntimeOverlayPath = overlayPath

			// Early registry write — claim the slot BEFORE substrate Run so any
			// crash between here and MarkReady leaves a state=starting entry
			// that `cspace registry prune` can reap. ControlURL carries the
			// host gateway as a placeholder until the container's IP is known
			// (the real ControlURL is written below once waitForIP succeeds).
			path, pathErr := registry.DefaultPath()
			if pathErr != nil {
				err = pathErr
				return err
			}
			r := &registry.Registry{Path: path}
			startedAt := time.Now().UTC()
			if regErr := r.Register(registry.Entry{
				Project:          project,
				Name:             name,
				ControlURL:       fmt.Sprintf("http://0.0.0.0:%d", supervisorPort),
				Token:            token,
				IP:               "",
				StartedAt:        startedAt,
				BrowserContainer: browserContainer,
				State:            "starting",
			}); regErr != nil {
				err = fmt.Errorf("register entry: %w", regErr)
				return err
			}

			rep.Phase(overlay.PhaseBoot)
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

			// Compose sidecars: spawn and wait for healthchecks, inject
			// /etc/hosts, extract credentials. Only runs when
			// devcontainer.json is present with a dockerComposeFile.
			// Sidecars are torn down on any subsequent error.
			var orch *orchestrator.Orchestration
			if devcontainerPlan != nil && devcontainerPlan.Compose != nil {
				orch = &orchestrator.Orchestration{
					Sandbox:   name,
					Project:   project,
					Plan:      devcontainerPlan,
					Substrate: &substrateRunner{adapter: a},
				}
				if upErr := orch.Up(ctx); upErr != nil {
					_ = orch.Down(context.Background())
					_ = a.Stop(context.Background(), containerName)
					err = fmt.Errorf("orchestrate sidecars: %w", upErr)
					return err
				}
				// Write extracted env vars (e.g. CONVEX_SELF_HOSTED_ADMIN_KEY)
				// to <sessionsHostDir>/extracted.env on the host. The sandbox's
				// /sessions/ bind-mount makes this visible as
				// /sessions/extracted.env; the entrypoint sources it early so
				// the agent sees the values.
				if len(orch.ExtractedEnv) > 0 {
					if writeErr := writeExtractedEnv(sessionsHostDir, orch.ExtractedEnv); writeErr != nil {
						_ = orch.Down(context.Background())
						_ = a.Stop(context.Background(), containerName)
						err = fmt.Errorf("write extracted env: %w", writeErr)
						return err
					}
				}
				// Ensure sidecars are torn down if any later step fails.
				defer func() {
					if err != nil {
						_ = orch.Down(context.Background())
					}
				}()
			}

			ctlURL := fmt.Sprintf("http://%s:%d", ip, supervisorPort)

			// Re-register with the real ControlURL/IP. State stays "starting"
			// until /health responds 200 below.
			if regErr := r.Register(registry.Entry{
				Project:          project,
				Name:             name,
				ControlURL:       ctlURL,
				Token:            token,
				IP:               ip,
				StartedAt:        startedAt,
				BrowserContainer: browserContainer,
				State:            "starting",
			}); regErr != nil {
				_ = a.Stop(context.Background(), containerName)
				err = regErr
				return err
			}

			// Watch the entrypoint's status file (written to
			// /sessions/cspace-init.status inside the sandbox; bind-mounted
			// from sessionsHostDir on the host). Translate single-word
			// states to overlay phases so the user sees "installing claude
			// plugins" while the entrypoint actually is. Stops when ctx
			// cancels — i.e. when this RunE returns.
			statusFile := filepath.Join(sessionsHostDir, "cspace-init.status")
			statusCtx, statusCancel := context.WithCancel(ctx)
			defer statusCancel()
			go pollInitStatus(statusCtx, statusFile, rep)

			// Wait for the supervisor to come up. /health responding 200 is
			// the load-bearing readiness signal — the Bun process is listening
			// and able to serve cspace send. Only then do we flip the entry
			// to state=ready.
			rep.Phase(overlay.PhaseSupervisor)
			healthURL := fmt.Sprintf("%s/health", ctlURL)
			// 60s rather than 10s: the entrypoint installs Claude Code
			// plugins declared in /workspace/.claude/settings.json
			// before exec'ing the supervisor. A project with a dozen
			// plugins takes ~2s/plugin against GitHub on a warm cache,
			// closer to 5s/plugin cold. 60s is enough for ~12 plugins
			// cold or many more warm. The supervisor itself responds
			// in <1 s once it actually starts.
			if hErr := waitForHealth(ctx, healthURL, token, 60*time.Second); hErr != nil {
				err = fmt.Errorf("waiting for sandbox /health: %w", hErr)
				return err
			}
			if mErr := r.MarkReady(project, name); mErr != nil {
				err = fmt.Errorf("mark ready: %w", mErr)
				return err
			}
			rep.Phase(overlay.PhaseReady)

			_, _ = fmt.Fprintf(cmd.OutOrStdout(),
				"%ssandbox %s up: control %s  ip %s  token %s…\n",
				glyphPrefix(name), name, ctlURL, ip, token[:8])

			// Friendly-URL hint. Always print the browse line when DNS is
			// installed (it's helpful, not nagging). Otherwise, suggest
			// `cspace dns install` once per user via a sentinel.
			if dnsInstalled() {
				// Project-qualified hostname so two projects can run a
				// sandbox with the same name simultaneously.
				_, _ = fmt.Fprintf(cmd.OutOrStdout(),
					"browse:  http://%s.%s.%s/  (friendly URL via cspace dns)\n",
					name, project, dnsDomain)
			} else {
				maybeNudgeMissingDnsInstall(cmd.OutOrStdout())
			}

			// Auto-attach: drop the user into an interactive claude
			// session inside the sandbox. The defer above will fire
			// during return and tear down the overlay + flush buffered
			// output to realOut BEFORE attachInteractive replaces the
			// process with `container exec -it ... claude`. Skipped
			// when --no-attach, when stdout isn't a TTY (CI / piped),
			// or after any error path.
			if !noAttach && isStdoutTTY() {
				// Tear down overlay + flush captured output now so the
				// success line lands on the real terminal before we
				// hand control to the in-sandbox claude.
				if teaDone != nil {
					rep.Done()
					<-teaDone
					cmd.SetOut(realOut)
					_, _ = io.Copy(realOut, &pendingOut)
					teaDone = nil // mark cleanup as done so the defer no-ops
				}
				return attachInteractive(containerName)
			}
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
	cmd.Flags().IntVar(&cpus, "cpus", 0,
		"CPU cap for this sandbox (overrides .cspace.json resources.cpus and the default of 4)")
	cmd.Flags().IntVar(&memoryMiB, "memory", 0,
		"memory cap in MiB for this sandbox (overrides .cspace.json resources.memoryMiB and the default of 4096)")
	cmd.Flags().BoolVar(&noOverlay, "no-overlay", false,
		"skip the planet boot animation; phases print as plain '[N/5] phase' lines (auto-disabled when stdout is not a TTY)")
	cmd.Flags().BoolVar(&noAttach, "no-attach", false,
		"don't drop into an interactive `claude` session after the sandbox is ready (auto-disabled when stdout is not a TTY)")
	return cmd
}

// waitForHealth polls a /health URL with the supervisor's bearer token until
// it returns 200 or the deadline passes. Used by cspace up to gate the
// state=starting → state=ready transition on a real readiness signal: the
// Bun supervisor process being up and able to serve requests.
//
// The 1s per-request timeout keeps the loop responsive on transient network
// blips during boot. ctx cancellation short-circuits the poll cleanly.
func waitForHealth(ctx context.Context, url, token string, max time.Duration) error {
	deadline := time.Now().Add(max)
	client := &http.Client{Timeout: 1 * time.Second}
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("/health did not respond in %s", max)
}

// pollInitStatus watches the entrypoint's status file and emits
// overlay phase advances + per-step status sub-labels. The file is
// at /sessions/cspace-init.status inside the sandbox, written by
// cspace-entrypoint.sh and cspace-install-plugins.sh — bind-mounted
// from sessionsHostDir on the host.
//
// Status formats:
//   plugins                       — entry into the plugins phase
//   plugins:<i>/<N>:<plugin>      — per-plugin progress within phase
//   supervisor                    — entry into the supervisor phase
//
// Phase advances are one-way (later phases never regress). Returns
// when ctx cancels.
func pollInitStatus(ctx context.Context, statusFile string, rep overlay.Reporter) {
	var currentPhase overlay.Phase
	var lastStatus string
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		data, err := os.ReadFile(statusFile)
		if err != nil {
			continue
		}
		raw := strings.TrimSpace(string(data))
		var phase overlay.Phase
		var status string
		switch {
		case raw == "plugins":
			phase = overlay.PhasePlugins
		case strings.HasPrefix(raw, "plugins:"):
			phase = overlay.PhasePlugins
			// e.g. "plugins:3/12:github" → "installing 3/12: github"
			rest := strings.TrimPrefix(raw, "plugins:")
			parts := strings.SplitN(rest, ":", 2)
			if len(parts) == 2 {
				status = fmt.Sprintf("installing %s: %s", parts[0], parts[1])
			}
		case raw == "supervisor":
			phase = overlay.PhaseSupervisor
		default:
			continue
		}
		if phase > currentPhase {
			rep.Phase(phase)
			currentPhase = phase
		}
		if status != "" && status != lastStatus {
			rep.Status(status)
			lastStatus = status
		}
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

// projectName returns the current project's name. Resolution order:
//  1. $CSPACE_PROJECT env var (set inside sandboxes by cspace up so the
//     in-sandbox cspace binary resolves the same project key the host used
//     when it registered the sibling).
//  2. cfg.Project.Name from a loaded .cspace.json.
//  3. "default" when neither is available.
//
// It is the single fix-up point for the cspace command-suite.
func projectName() string {
	if p := os.Getenv("CSPACE_PROJECT"); p != "" {
		return p
	}
	if cfg != nil && cfg.Project.Name != "" {
		return cfg.Project.Name
	}
	return "default"
}

// planetOrder is the canonical solar-system order cspace draws from when
// auto-naming sandboxes. Eight names is enough that running out is a strong
// signal you should be passing explicit task-shaped names instead.
var planetOrder = []string{
	"mercury", "venus", "earth", "mars",
	"jupiter", "saturn", "uranus", "neptune",
}

// glyphPrefix returns "<colored-symbol> " when name is a known planet and
// stdout is a TTY, "<plain-symbol> " when it's a planet but stdout is piped
// (so the symbol still survives `cspace up | tee log`), and "" for custom
// names. Trailing space is included so call sites can blindly concatenate.
func glyphPrefix(name string) string {
	p, ok := planets.Get(name)
	if !ok {
		return ""
	}
	if !isStdoutTTY() {
		return p.Symbol + " "
	}
	r, g, b := p.Color[0], p.Color[1], p.Color[2]
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm%s\x1b[0m ", r, g, b, p.Symbol)
}

// isStdoutTTY returns true when os.Stdout is a terminal — used to gate ANSI
// color so piped output stays clean.
func isStdoutTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// pickPlanetName returns the first planet not currently registered for this
// project. Errors if all eight are taken — at that point the user should be
// using descriptive names anyway.
func pickPlanetName(project string) (string, error) {
	path, err := registry.DefaultPath()
	if err != nil {
		return "", fmt.Errorf("registry path: %w", err)
	}
	r := &registry.Registry{Path: path}
	entries, err := r.List()
	if err != nil {
		return "", fmt.Errorf("registry list: %w", err)
	}
	taken := map[string]bool{}
	for _, e := range entries {
		if e.Project == project {
			taken[e.Name] = true
		}
	}
	for _, name := range planetOrder {
		if !taken[name] {
			return name, nil
		}
	}
	return "", fmt.Errorf("all 8 planet names are in use for project %q; pass an explicit name (e.g. `cspace up issue-42`)", project)
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

// ensureRegistryDaemon starts the cspace daemon on 127.0.0.1:6280 if it is
// not already accepting connections. It re-execs the running cspace binary
// itself (`cspace daemon serve`) rather than a separate binary, so the
// daemon and the cspace up that spawned it are version-locked.
//
// Idempotent — concurrent cspace up calls that race here will at most spawn
// one extra daemon, and only one will manage to bind the port; the others
// exit immediately.
func ensureRegistryDaemon() error {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:6280", time.Second)
	if err == nil {
		_ = conn.Close()
		return nil
	}
	// Use os.Executable() so the daemon spawns the SAME cspace binary the
	// user just invoked, not whatever "cspace" might exist on PATH from an
	// older install.
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate cspace binary: %w", err)
	}
	c := exec.Command(self, "daemon", "serve")
	// Capture daemon stderr so a fast-fail (e.g. DNS UDP bind failure on a
	// squatted port 5354) bubbles up as a useful error instead of "not
	// accepting connections" with no context. The buffer is bounded by
	// limitedBuffer so a misbehaving daemon can't grow it without bound.
	stderrBuf := &limitedBuffer{max: 8 * 1024}
	c.Stdout = nil
	c.Stderr = stderrBuf
	if err := c.Start(); err != nil {
		return fmt.Errorf("spawn cspace daemon: %w", err)
	}
	// Daemon takes ~250ms to bind in practice. Wait for the port to actually
	// accept connections, but also short-circuit if the daemon process exits
	// (which it does on DNS bind failure — see runDaemonServe).
	exited := make(chan error, 1)
	go func() { exited <- c.Wait() }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, derr := net.DialTimeout("tcp", "127.0.0.1:6280", 250*time.Millisecond)
		if derr == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case waitErr := <-exited:
			// Daemon already exited. Surface its stderr.
			msg := strings.TrimSpace(stderrBuf.String())
			if msg != "" {
				return fmt.Errorf("daemon exited before accepting connections: %w\nstderr:\n%s", waitErr, msg)
			}
			return fmt.Errorf("daemon exited before accepting connections: %w", waitErr)
		case <-time.After(100 * time.Millisecond):
		}
	}

	// Deadline expired without HTTP coming up and without the process
	// exiting. Kill the orphan child, drain Wait, and surface whatever
	// stderr we collected.
	_ = c.Process.Kill()
	<-exited
	msg := strings.TrimSpace(stderrBuf.String())
	if msg != "" {
		return fmt.Errorf("daemon failed to start within 3s; stderr:\n%s", msg)
	}
	return fmt.Errorf("daemon started but not accepting connections")
}

// limitedBuffer is a goroutine-safe bytes.Buffer wrapper that caps its size,
// dropping any writes that would exceed `max`. Used to capture daemon stderr
// without unbounded growth in pathological cases (e.g. log spam loop).
type limitedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.max - b.buf.Len()
	if remaining <= 0 {
		// Pretend the write succeeded; we just drop overflow.
		return len(p), nil
	}
	if len(p) > remaining {
		b.buf.Write(p[:remaining])
		return len(p), nil
	}
	b.buf.Write(p)
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
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
	_, _ = fmt.Fprintln(out, "note: no Anthropic credential reachable. Run `cspace keychain init` to set one up")
	_, _ = fmt.Fprintln(out, "      (or set ANTHROPIC_API_KEY in ~/.cspace/secrets.env). Sandbox will boot,")
	_, _ = fmt.Fprintln(out, "      but Claude SDK calls will fail until auth is configured.")
	if err := os.MkdirAll(cspaceDir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(sentinel, []byte("shown\n"), 0o644)
}

// dnsInstallNudgeSentinel is the per-user marker for "the dns-install nudge
// has already been shown". Lives in ~/.cspace/. Once it exists the nudge
// stays silent forever.
const dnsInstallNudgeSentinel = ".no-dns-install-nudge-shown"

// maybeNudgeMissingDnsInstall prints a one-time hint when DNS is not
// installed, suggesting the user run `cspace dns install` for friendly
// URLs. Gated by ~/.cspace/.no-dns-install-nudge-shown so it fires at
// most once per user. Failure to write the sentinel is swallowed — the
// nudge already printed and a future re-print is harmless.
func maybeNudgeMissingDnsInstall(out io.Writer) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	cspaceDir := filepath.Join(home, ".cspace")
	sentinel := filepath.Join(cspaceDir, dnsInstallNudgeSentinel)
	if _, err := os.Stat(sentinel); err == nil {
		return
	}
	_, _ = fmt.Fprintf(out, "note: friendly URLs disabled. Run `cspace dns install` once to enable http://<sandbox>.%s/.\n", dnsDomain)
	if err := os.MkdirAll(cspaceDir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(sentinel, []byte("shown\n"), 0o644)
}

// resolveSandboxImage selects the sandbox image for a cspace up run.
//
// Precedence (highest first):
//  1. plan.Compose.Services[plan.Service].Image — compose-driven image.
//  2. plan.Devcontainer.Image — bare devcontainer.json image field.
//  3. BuildProjectImage(ctx, plan) — build result when dockerFile/build: set.
//  4. defaultImage — caller-supplied fallback (e.g. "cspace:latest").
//
// A build error is logged and the function falls back to defaultImage so that
// a broken Dockerfile does not silently produce a cryptic "image not found"
// error from the substrate; instead the container boots with the default and
// the user sees the build error.
func resolveSandboxImage(ctx context.Context, plan *devcontainer.Plan, defaultImage string) string {
	if plan == nil {
		return defaultImage
	}
	// 1. Compose service image.
	if plan.Compose != nil && plan.Service != "" {
		if svc, ok := plan.Compose.Services[plan.Service]; ok && svc.Image != "" {
			return svc.Image
		}
	}
	// 2. Devcontainer image field.
	if plan.Devcontainer != nil && plan.Devcontainer.Image != "" {
		return plan.Devcontainer.Image
	}
	// 3. Build via Apple Container.
	if plan.Devcontainer != nil && (plan.Devcontainer.DockerFile != "" || plan.Devcontainer.Build != nil) {
		tag, err := orchestrator.BuildProjectImage(ctx, plan)
		if err == nil && tag != "" {
			return tag
		}
		// Fall through to default; caller will see a boot failure if the
		// default image doesn't satisfy the project's needs.
	}
	return defaultImage
}

// writeExtractedEnv writes credential key=value pairs extracted from sidecars
// to <sessionsHostDir>/extracted.env on the host. The sandbox has this
// directory bind-mounted as /sessions, so the entrypoint can source
// /sessions/extracted.env early — before user-visible env consumption.
//
// Values are shell-escaped (single-quote wrapping with internal ' replaced by
// '\'') so arbitrary credential strings don't break the sourced file.
func writeExtractedEnv(sessionsHostDir string, env map[string]string) error {
	var b strings.Builder
	// Deterministic ordering for diffability.
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	// sort keys for determinism
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	for _, k := range keys {
		v := env[k]
		// Shell-escape the value: wrap in single quotes, escape embedded '.
		escaped := "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(escaped)
		b.WriteByte('\n')
	}
	dst := filepath.Join(sessionsHostDir, "extracted.env")
	return os.WriteFile(dst, []byte(b.String()), 0o600)
}

// substrateRunner adapts *applecontainer.Adapter to the orchestrator.Substrate
// interface. It bridges the orchestrator's ServiceSpec (compose-oriented) to
// the substrate's RunSpec (sandbox-oriented). Sidecars started via Run are
// started without a runtime overlay — they use their own image's entrypoint.
type substrateRunner struct {
	adapter *applecontainer.Adapter
}

func (s *substrateRunner) Run(ctx context.Context, spec orchestrator.ServiceSpec) (string, error) {
	rspec := substrate.RunSpec{
		Name:    spec.Name,
		Image:   spec.Image,
		Env:     spec.Environment,
		Command: spec.Command,
	}
	for _, v := range spec.Volumes {
		rspec.Mounts = append(rspec.Mounts, substrate.Mount{
			HostPath:      v.HostPath,
			ContainerPath: v.GuestPath,
			ReadOnly:      v.ReadOnly,
		})
	}
	if err := s.adapter.Run(ctx, rspec); err != nil {
		return "", err
	}
	return spec.Name, nil
}

func (s *substrateRunner) Exec(ctx context.Context, name string, cmd []string) (string, error) {
	res, err := s.adapter.Exec(ctx, name, cmd, substrate.ExecOpts{})
	if err != nil {
		return res.Stdout, err
	}
	if res.ExitCode != 0 {
		return res.Stdout, fmt.Errorf("exec in %s exited %d: %s", name, res.ExitCode, res.Stderr)
	}
	return res.Stdout, nil
}

func (s *substrateRunner) Stop(ctx context.Context, name string) error {
	return s.adapter.Stop(ctx, name)
}

func (s *substrateRunner) IP(ctx context.Context, name string) (string, error) {
	return s.adapter.IP(ctx, name)
}

// joinPostCmd renders a devcontainer postCreateCommand / postStartCommand
// (which may be a string or []string) into a single shell-runnable
// command line. Empty input returns "".
func joinPostCmd(cmd devcontainer.StringOrSlice) string {
	if len(cmd) == 0 {
		return ""
	}
	if len(cmd) == 1 {
		return cmd[0]
	}
	// Multi-element form treats the slice as exec-form: shell-quote each
	// element so the joined string is safe to pass to `bash -c`.
	parts := make([]string, len(cmd))
	for i, p := range cmd {
		parts[i] = shellSingleQuote(p)
	}
	return strings.Join(parts, " ")
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
