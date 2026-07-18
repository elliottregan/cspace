package cli

import (
	"bufio"
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
	"time"

	"github.com/elliottregan/cspace/internal/config"
	"github.com/elliottregan/cspace/internal/devcontainer"
	"github.com/elliottregan/cspace/internal/orchestrator"
	"github.com/elliottregan/cspace/internal/overlay"
	"github.com/elliottregan/cspace/internal/planets"
	"github.com/elliottregan/cspace/internal/registry"
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
	var workBranch string
	var noBrowser bool
	var noSharedBrowser bool
	var cpus int
	var memoryMiB int
	var noOverlay bool
	var noAttach bool
	var rebuildImage bool
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
				if valErr := validateSandboxName(project, name); valErr != nil {
					return valErr
				}
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
			// 4 minutes covers the heaviest cold path: browser sidecar
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

			// Load secrets and resolve the Anthropic credential carriers
			// BEFORE the overlay starts so we can surface auth warnings
			// to the real terminal. The overlay redirects stdout into a
			// pending buffer (flushed only after boot completes), so any
			// warnings emitted after overlay.Start would be invisible
			// until the session had already started.
			projectRoot := ""
			if cfg != nil {
				projectRoot = cfg.ProjectRoot
			}
			loaded, err := secrets.Load(projectRoot)
			if err != nil {
				return fmt.Errorf("load secrets: %w", err)
			}

			// Build a minimal pre-flight env that mirrors the Anthropic
			// carrier resolution the main env map performs below, so the
			// auth warnings see the same credential state the sandbox will.
			preflightEnv := map[string]string{}
			for k, v := range loaded {
				preflightEnv[k] = v
			}
			if k := os.Getenv("ANTHROPIC_API_KEY"); k != "" {
				preflightEnv["ANTHROPIC_API_KEY"] = k
			}
			if k := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); k != "" {
				preflightEnv["CLAUDE_CODE_OAUTH_TOKEN"] = k
			}
			if preflightEnv["ANTHROPIC_API_KEY"] != "" && preflightEnv["CLAUDE_CODE_OAUTH_TOKEN"] != "" {
				if loaded["CLAUDE_CODE_OAUTH_TOKEN"] != "" && loaded["ANTHROPIC_API_KEY"] == "" {
					delete(preflightEnv, "ANTHROPIC_API_KEY")
				} else if loaded["ANTHROPIC_API_KEY"] != "" && loaded["CLAUDE_CODE_OAUTH_TOKEN"] == "" {
					delete(preflightEnv, "CLAUDE_CODE_OAUTH_TOKEN")
				} else {
					delete(preflightEnv, "CLAUDE_CODE_OAUTH_TOKEN")
				}
			}
			// Surface the Anthropic auth state to the real terminal BEFORE
			// the overlay starts. If auto-discovery found a token but
			// secrets.Load refused it for being expired (leaving no carrier
			// in env), show a specific, always-printed "how to fix" hint.
			// Otherwise fall back to the generic one-time onboarding nudge,
			// which fires once per user when no credential is reachable.
			// Sandbox still boots either way.
			if !warnExpiredAutoDiscoveredAuth(cmd.ErrOrStderr(), preflightEnv) {
				maybeNudgeMissingAnthropicAuth(cmd.ErrOrStderr(), preflightEnv)
			}

			// Validate the GitHub credential BEFORE the overlay, mirroring the
			// Anthropic pre-flight above. A stale GH_TOKEN /
			// GITHUB_PERSONAL_ACCESS_TOKEN (e.g. leaked in from a project .env
			// and picked up as a host-shell override below) otherwise shadows
			// the valid `gh auth token` and silently breaks git/gh in the
			// sandbox. ghTokenOverride carries a validated fallback down to the
			// GitHub env assembly so it wins over the shadowing value.
			ghTokenOverride := ""
			effectiveGH := loaded["GH_TOKEN"]
			if effectiveGH == "" {
				effectiveGH = loaded["GITHUB_TOKEN"]
			}
			if effectiveGH == "" {
				effectiveGH = loaded["GITHUB_PERSONAL_ACCESS_TOKEN"]
			}
			if k := os.Getenv("GH_TOKEN"); k != "" {
				effectiveGH = k
			}
			if reconciled, warn := secrets.ReconcileGitHubToken(effectiveGH); warn != "" {
				if reconciled != effectiveGH {
					ghTokenOverride = reconciled
				}
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", warn)
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
			// Always set, independent of the browser sidecar: agents/docs
			// point at this var as THE address to reach the workspace from
			// outside it, and that must hold even under --no-browser.
			applyWorkspaceHostEnv(env, name, project)

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

			// Merge loaded secrets into the main env map (secrets.Load was
			// already called above before the overlay).
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
					if plan.Compose != nil {
						for _, w := range plan.Compose.Warnings {
							_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "[cspace] warning: compose: %s\n", w)
						}
					}
					sandboxImage = resolveSandboxImage(ctx, plan, "cspace:latest")
					// Snapshot the cspace-delivered secret values (from
					// `loaded`, merged into env above) BEFORE the compose
					// env_file merge below can silently override them. Used
					// only for the fail-loud warning after the merge — it
					// never changes which value wins.
					secretKeys := secrets.SecretKeys()
					secretValues := map[string]string{}
					for _, k := range secretKeys {
						if v, ok := env[k]; ok {
							secretValues[k] = v
						}
					}
					// Compose service.environment (including any env_file
					// content compose-go resolved at parse time) merges
					// FIRST so devcontainer.json containerEnv can override.
					// This is how a project's `.env` flows into the
					// workspace: declare `env_file: ../.env` on the compose
					// app service, compose-go reads the file into
					// service.Environment, and we propagate it here.
					if plan.Compose != nil && plan.Service != "" {
						if svc, ok := plan.Compose.Services[plan.Service]; ok {
							for k, v := range svc.Environment {
								env[k] = v
							}
						}
					}
					// Fail loud: warn (don't block) if the env_file merge
					// just above silently overrode a cspace-delivered
					// secret. This must run BEFORE the containerEnv merge
					// below, so an intentional devcontainer.json
					// containerEnv override doesn't get flagged — only
					// env_file collisions do. See docs/env-cspace.md's
					// "Precedence (stated honestly)" section: env_file
					// wins by design, this only makes the footgun loud.
					for _, key := range envFileSecretCollisions(secretValues, env, secretKeys) {
						_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
							"warning: a project env_file (.env / .env.cspace) overrides the cspace-delivered secret %s — the sandbox will use the env_file value, not the credential cspace loaded. Remove %s from your env_file (or rename it) to use the delivered secret.\n",
							key, key)
					}
					// containerEnv merges into env: devcontainer values override
					// compose env (more specific) and lose to secrets file +
					// --env (see below).
					for k, v := range dc.ContainerEnv {
						env[k] = v
					}
				}
			}

			// Warn if the resolved sandbox image is cspace:latest and its baked
			// cspace.version label drifts from the running CLI. Skipped for
			// project-supplied images (compose image:, devcontainer image:,
			// build:) — those are the user's responsibility, not cspace's.
			if sandboxImage == "cspace:latest" {
				// canPrompt is false while the overlay TUI is up: bubbletea owns
				// stdin in raw mode, so a blocking prompt here would never receive
				// the user's keystrokes and would hang the boot. Only prompt when
				// the overlay is off (--no-overlay / piped stdout) AND stdin is a
				// real terminal.
				rebuilt, rbErr := maybeRebuildStaleImage(cmd, sandboxImage, Version, rebuildImage, !useOverlay && isStdinTTY())
				if rbErr != nil {
					return rbErr
				}
				if rebuilt {
					// The rebuild can take minutes and isn't bound by ctx;
					// refresh the boot deadline so the 4-minute budget covers
					// container launch, not the preceding build.
					cancel()
					ctx, cancel = context.WithTimeout(parent, 4*time.Minute)
					defer cancel()
				}
			}

			// Inherit the host's global git identity so agent commits/rebases
			// attribute correctly. Without this, in-sandbox git fails with
			// "Committer identity unknown" the first time the agent tries to
			// commit. Only user.name / user.email flow — signing keys and
			// includes are intentionally NOT carried over (no GPG/SSH agent
			// in the microVM, and includes point at host paths that don't
			// exist inside).
			if name := hostGitConfig("user.name"); name != "" {
				env["CSPACE_GIT_USER_NAME"] = name
			}
			if email := hostGitConfig("user.email"); email != "" {
				env["CSPACE_GIT_USER_EMAIL"] = email
			}
			if env["CSPACE_GIT_USER_NAME"] == "" || env["CSPACE_GIT_USER_EMAIL"] == "" {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "[cspace] note: host git config missing user.name and/or user.email — agent will hit 'Committer identity unknown' on first commit. Fix on host: `git config --global user.name \"Your Name\" && git config --global user.email you@example.com`")
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
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "[cspace] warning: .cspace.json 'services' is ignored when .devcontainer/devcontainer.json is present. Migrate compose orchestration into devcontainer.json's dockerComposeFile field. See docs/migration-from-cspace-json.md")
				}
				if len(cfg.Container.Environment) > 0 || len(cfg.Container.Ports) > 0 || len(cfg.Container.Packages) > 0 {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "[cspace] warning: .cspace.json 'container' block (ports/environment/packages) is ignored when devcontainer.json is present. Move env to containerEnv, ports to forwardPorts, packages to features. See docs/migration-from-cspace-json.md")
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
			// A validated fallback from the GitHub pre-flight wins last of all:
			// it replaces a token that GitHub definitively rejected (401).
			if ghTokenOverride != "" {
				env["GH_TOKEN"] = ghTokenOverride
			}
			propagateFamily(env, []string{"GH_TOKEN", "GITHUB_TOKEN", "GITHUB_PERSONAL_ACCESS_TOKEN"})

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
			// lives at ~/.cspace/clones/<project>/<sandbox>/ and stays on
			// baseBranch (typically main) unless --branch <name|auto> asks
			// for a fresh per-sandbox branch off that tip. See finding
			// 2026-05-01-per-sandbox-git-clone-bind-mounted-as-workspace-works-as-des
			// for the original clone-based design.
			rep.Phase(overlay.PhaseClone)
			if workspaceMount == "" {
				// Resolve `--branch auto` to the historical cspace/<sandbox>
				// shape so autonomous flows that spawn many sandboxes don't
				// have to invent unique names. An explicit value passes through.
				resolvedBranch := workBranch
				if resolvedBranch == "auto" {
					resolvedBranch = "cspace/" + name
				}
				auto, err := provisionClone(projectRoot, project, name, baseBranch, resolvedBranch)
				if err != nil {
					return fmt.Errorf("provision workspace clone: %w", err)
				}
				if auto != "" {
					workspaceMount = auto
					if resolvedBranch != "" {
						_, _ = fmt.Fprintf(cmd.OutOrStdout(),
							"workspace clone: %s (branch %s)\n", auto, resolvedBranch)
					} else {
						_, _ = fmt.Fprintf(cmd.OutOrStdout(),
							"workspace clone: %s\n", auto)
					}
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

			// Browser sidecar: start a Playwright sidecar before launching the
			// sandbox so we can inject CSPACE_BROWSER_CDP_URL into spec.Env. The
			// supervisor's claude-runner.ts reads this and registers
			// playwright-mcp via --cdp-endpoint, giving the agent browser
			// tools without bundling a browser into the cspace image.
			//
			// On any subsequent error (substrate run, IP capture, registry
			// write, …) we tear the sidecar down via the deferred cleanup
			// below so we don't leak containers.
			//
			// Default ON: every sandbox gets a sidecar so the agent's
			// playwright-mcp / chrome-devtools-mcp work out of the box,
			// regardless of whether the project itself uses Playwright.
			//
			// Precedence (highest first):
			//   1. Project-supplied CSPACE_BROWSER_CDP_URL in env (from
			//      --env, ~/.cspace/secrets.env, .cspace/secrets.env, or
			//      .cspace.json env). The project is pointing at its own
			//      browser — skip our sidecar entirely; the value already
			//      in env flows through to the MCP servers.
			//   2. CLI --no-browser flag (hard opt-out).
			//   3. devcontainer.json customizations.cspace.browser explicit
			//      true/false.
			//   4. Default ON.
			var dcBrowser *bool
			if devcontainerPlan != nil && devcontainerPlan.Devcontainer != nil {
				dcBrowser = devcontainerPlan.Devcontainer.Customizations.Cspace.Browser
			}
			browserDec := resolveBrowserEnabled(dcBrowser, noBrowser, env["CSPACE_BROWSER_CDP_URL"])
			browserEnabled := browserDec.enabled
			if !browserEnabled && browserDec.skipReason != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "browser sidecar: skipped (%s)\n", browserDec.skipReason)
			}
			var browserContainer string
			var browserSidecar *BrowserSidecar
			if browserEnabled {
				rep.Phase(overlay.PhaseBrowserSidecar)
				// Match the sidecar's Playwright run-server version to the
				// project's @playwright/test pin. Playwright's strict
				// cross-version handshake check makes mismatched versions
				// fail with 428 Precondition Required; tracking the
				// project pin avoids that without dictating a version.
				plVersion := detectPlaywrightVersion(cfg.ProjectRoot)
				var b config.BrowserConfig
				if cfg != nil {
					b = cfg.Browser
				}
				shared := resolveSharedBrowser(b, noSharedBrowser)
				var bs *BrowserSidecar
				var startedNew bool
				var berr error
				if shared {
					bs, startedNew, berr = ensureSharedBrowserSidecar(ctx, project, plVersion)
				} else {
					bs, berr = startBrowserSidecar(ctx, project, name, plVersion)
					startedNew = true
				}
				if berr != nil {
					return fmt.Errorf("browser sidecar: %w", berr)
				}
				browserSidecar = bs
				browserContainer = bs.ContainerName
				// Env carries the stable DNS name (browser.<project>.cspace.test),
				// not the sidecar's raw vmnet IP: a restart moves the IP, which
				// would otherwise strand every sandbox already running with the
				// old address baked into its env (cs-finding
				// 2026-07-17-sidecar-addressed-by-boot-baked-ip-no-recovery-path).
				cdpURL, wsURL := browserEnvURLs(project)
				env["CSPACE_BROWSER_CDP_URL"] = cdpURL
				// @playwright/mcp respects PLAYWRIGHT_MCP_CDP_ENDPOINT
				// to connect to an existing CDP browser instead of
				// launching its own Chromium. Setting this redirects
				// the @claude-plugins-official `playwright` plugin
				// (which runs `npx @playwright/mcp@latest` with no
				// connect args) at our sidecar so the lean sandbox
				// image doesn't need a local browser.
				env["PLAYWRIGHT_MCP_CDP_ENDPOINT"] = cdpURL
				env["PW_TEST_CONNECT_WS_ENDPOINT"] = wsURL
				// CSPACE_WORKSPACE_HOST is set unconditionally above (near
				// env's construction), not here — it must hold even under
				// --no-browser. Hosts entry written below once the
				// workspace IP is known.
				_, _ = fmt.Fprintf(cmd.OutOrStdout(),
					"browser sidecar: %s (cdp %s [raw ip %s], run-server %s)\n",
					bs.ContainerName, cdpURL, bs.CDPURL, wsURL)
				defer func() {
					// Only tear down a sidecar THIS up created. A reused
					// shared singleton is left for the instances still using it.
					if err != nil && startedNew {
						stopBrowserSidecar(context.Background(), bs.ContainerName)
					}
				}()
			}

			// Apply tmpfs mounts and named volumes declared on the workspace's
			// own compose service (e.g. node_modules tmpfs, shared pnpm-store
			// volume) to the workspace sandbox. orchestrator.Up handles these
			// for sidecars, but the workspace is spawned here.
			if devcontainerPlan != nil && devcontainerPlan.Compose != nil {
				if wsSvc, ok := devcontainerPlan.Compose.Services[devcontainerPlan.Service]; ok && wsSvc != nil {
					for _, t := range wsSvc.Tmpfs {
						spec.TmpfsMounts = append(spec.TmpfsMounts, substrate.TmpfsMount{
							ContainerPath: t.Target,
							SizeMiB:       t.SizeMiB,
						})
					}
					composeDir := filepath.Dir(devcontainerPlan.Compose.SourcePath)
					for _, v := range wsSvc.Volumes {
						external := false
						externalName := ""
						if nv, ok := devcontainerPlan.Compose.NamedVolumes[v.Source]; ok {
							external = nv.External
							externalName = nv.Name
						}
						rv, vErr := orchestrator.ResolveVolume(v, project, name, composeDir, external, externalName)
						if vErr != nil {
							return fmt.Errorf("resolve workspace volume %q: %w", v.Target, vErr)
						}
						if rv.Bind != nil {
							spec.Mounts = append(spec.Mounts, substrate.Mount{
								HostPath:      rv.Bind.HostPath,
								ContainerPath: rv.Bind.GuestPath,
								ReadOnly:      rv.Bind.ReadOnly,
							})
						}
						if rv.Named != nil {
							spec.Volumes = append(spec.Volumes, substrate.NamedVolume{
								Name:          rv.Named.Name,
								ContainerPath: rv.Named.GuestPath,
								ReadOnly:      rv.Named.ReadOnly,
								// Workspace tooling (pnpm, vite, etc.) runs as
								// `dev` (UID 1000 — see lib/templates/Dockerfile).
								// Adapter chowns + removes lost+found at first
								// boot so non-root tools can write a fresh
								// ext4 mount.
								OwnerUID: 1000,
							})
						}
					}
				}
			}

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

			// If a browser sidecar is running, inject `workspace` into
			// /etc/hosts inside the workspace container so
			// http://workspace:<port> resolves to 127.0.0.1 (the
			// preview server running locally). Best-effort.
			if browserSidecar != nil {
				if hErr := InjectWorkspaceHost(ctx, containerName, "127.0.0.1"); hErr != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
						"[cspace] warning: inject workspace host into workspace: %v\n", hErr)
				}
			}

			// Compose sidecars: spawn and wait for healthchecks, inject
			// /etc/hosts, extract credentials. Only runs when
			// devcontainer.json is present with a dockerComposeFile.
			// Sidecars are torn down on any subsequent error.
			var orch *orchestrator.Orchestration
			if devcontainerPlan != nil && devcontainerPlan.Compose != nil {
				rep.Phase(overlay.PhaseSidecars)
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

			// Gate: confirm the friendly .cspace.test hostname actually
			// resolves from inside the sandbox (and the browser sidecar,
			// if one is running) before we hand off to the agent. This
			// catches a cspace daemon DNS outage or dnsmasq misconfig
			// early — with a clear warning — instead of silently falling
			// back to raw IPs deep into a session. Warn, don't fail: a
			// broken friendly hostname degrades convenience, not
			// correctness, so it must never block the boot.
			containerExecAdapter := func(execCtx context.Context, container string, argv ...string) ([]byte, error) {
				res, execErr := a.Exec(execCtx, container, argv, substrate.ExecOpts{})
				return []byte(res.Stdout), execErr
			}
			if wh := workspaceFriendlyHost(name, project); wh != "" {
				if rErr := verifyInContainerResolution(ctx, containerExecAdapter, containerName, wh); rErr != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
						"warning: %v — sidecar browser and host will fall back to raw IPs; run `cspace doctor`\n", rErr)
				}
				if browserSidecar != nil {
					if rErr := verifyInContainerResolution(ctx, containerExecAdapter, browserContainer, wh); rErr != nil {
						_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
							"warning: %v — sidecar browser and host will fall back to raw IPs; run `cspace doctor`\n", rErr)
					}
				}
			}

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
		"base branch the workspace clone is checked out on (defaults to the upstream repo's default branch)")
	cmd.Flags().StringVar(&workBranch, "branch", "",
		"create a fresh branch off baseBranch for the sandbox's work (use `auto` for the historical cspace/<sandbox> shape, or pass an explicit name like `issue/538-fix`); empty = stay on baseBranch")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false,
		"skip the default Playwright browser sidecar (the agent's playwright-mcp / chrome-devtools-mcp will fall back to launching their own browser, which fails on ARM64)")
	cmd.Flags().BoolVar(&noSharedBrowser, "no-shared-browser", false,
		"use a per-sandbox browser sidecar instead of the shared project browser (default: shared)")
	cmd.Flags().IntVar(&cpus, "cpus", 0,
		"CPU cap for this sandbox (overrides .cspace.json resources.cpus and the default of 4)")
	cmd.Flags().IntVar(&memoryMiB, "memory", 0,
		"memory cap in MiB for this sandbox (overrides .cspace.json resources.memoryMiB and the default of 4096)")
	cmd.Flags().BoolVar(&noOverlay, "no-overlay", false,
		"skip the planet boot animation; phases print as plain '[N/5] phase' lines (auto-disabled when stdout is not a TTY)")
	cmd.Flags().BoolVar(&noAttach, "no-attach", false,
		"don't drop into an interactive `claude` session after the sandbox is ready (auto-disabled when stdout is not a TTY)")
	cmd.Flags().BoolVar(&rebuildImage, "rebuild", false,
		"rebuild cspace:latest before launching if it was built by a different cspace version (otherwise you're prompted when the image is stale)")
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
//
//	plugins                       — entry into the plugins phase
//	plugins:<i>/<N>:<plugin>      — per-plugin progress within phase
//	supervisor                    — entry into the supervisor phase
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

// validateSandboxName checks if an explicit sandbox name is allowed. The name
// "browser" is reserved for the shared browser sidecar container, which uses
// the naming pattern cspace-<project>-browser and is referenced as
// browser.<project>.cspace.test in DNS. Returns an error for "browser";
// nil otherwise.
func validateSandboxName(project, name string) error {
	if name == "browser" {
		return fmt.Errorf(
			`"browser" is reserved for the shared browser sidecar (browser.%s.cspace.test)`,
			project)
	}
	return nil
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
	if v, ok := daemonHealthVersion(time.Second); ok {
		if v == Version {
			return nil
		}
		// A daemon is up but from a different cspace build/version. Stop
		// it and fall through to respawn a version-matched one.
		// stopRegistryDaemon blocks until the loopback ports are actually
		// free, so the respawn below doesn't lose the fatal DNS/HTTP bind
		// race against the dying process.
		_ = stopRegistryDaemon()
	}
	// Use os.Executable() so the daemon spawns the SAME cspace binary the
	// user just invoked, not whatever "cspace" might exist on PATH from an
	// older install.
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate cspace binary: %w", err)
	}
	cmd, logFile, err := newDaemonCommand(self)
	if err != nil {
		return fmt.Errorf("prepare daemon: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("spawn cspace daemon: %w", err)
	}
	_ = logFile.Close()            // detached daemon keeps its own handle on the log file
	go func() { _ = cmd.Wait() }() // reap; does NOT couple lifetimes (child is Setsid-detached)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c, derr := net.DialTimeout("tcp", daemonHTTPAddr(), 250*time.Millisecond); derr == nil {
			_ = c.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if p, perr := daemonLogPath(); perr == nil {
		if b, rerr := os.ReadFile(p); rerr == nil && len(b) > 0 {
			tail := string(b)
			if len(tail) > 800 {
				tail = tail[len(tail)-800:]
			}
			return fmt.Errorf("daemon not accepting connections within 3s; ~/.cspace/daemon.log tail:\n%s", tail)
		}
	}
	return fmt.Errorf("daemon started but not accepting connections within 3s")
}

// nudgeSentinelName is the per-user marker file that records "the no-auth
// nudge has already been shown". Lives in ~/.cspace/. Once it exists the
// nudge stays silent forever — the message has done its job.
const nudgeSentinelName = ".no-claude-auth-nudge-shown"

// browserDecision is the outcome of resolveBrowserEnabled: whether to start
// the cspace browser sidecar and, when not, a human-readable reason for the
// "skipped" log line.
type browserDecision struct {
	enabled    bool
	skipReason string // non-empty only when enabled is false
}

// resolveBrowserEnabled implements the browser-sidecar precedence (highest
// first): a project-supplied CSPACE_BROWSER_CDP_URL (the project points at its
// own browser) > the --no-browser opt-out flag > devcontainer
// customizations.cspace.browser (tristate *bool; nil = unset) > default ON.
// projectCDPURL is the CSPACE_BROWSER_CDP_URL already present in env. The
// returned skipReason names the deciding factor(s) accurately so the printed
// line never misattributes the skip (e.g. crediting the project browser when
// the user explicitly passed --no-browser).
func resolveBrowserEnabled(dcBrowser *bool, noBrowser bool, projectCDPURL string) browserDecision {
	enabled := true // default ON
	if dcBrowser != nil {
		enabled = *dcBrowser // devcontainer explicit true/false
	}
	if noBrowser {
		enabled = false // hard opt-out
	}
	if projectCDPURL != "" {
		enabled = false // project's own browser wins
	}
	if enabled {
		return browserDecision{enabled: true}
	}
	// A project-supplied CDP URL means a browser endpoint still exists (the
	// project's own), so it leads the reason; an explicit --no-browser is
	// appended so the user's choice is acknowledged when both apply.
	switch {
	case projectCDPURL != "" && noBrowser:
		return browserDecision{skipReason: fmt.Sprintf("using project-supplied CSPACE_BROWSER_CDP_URL=%s; --no-browser also set", projectCDPURL)}
	case projectCDPURL != "":
		return browserDecision{skipReason: fmt.Sprintf("using project-supplied CSPACE_BROWSER_CDP_URL=%s", projectCDPURL)}
	case noBrowser:
		return browserDecision{skipReason: "--no-browser"}
	default:
		return browserDecision{skipReason: "customizations.cspace.browser=false"}
	}
}

// resolveSharedBrowser decides whether a project's sandboxes share one
// browser sidecar. Default true; .cspace.json browser.shared overrides the
// default; --no-shared-browser overrides config (applied last = flag wins).
func resolveSharedBrowser(cfgBrowser config.BrowserConfig, noSharedBrowser bool) bool {
	shared := true
	if cfgBrowser.Shared != nil {
		shared = *cfgBrowser.Shared
	}
	if noSharedBrowser {
		shared = false
	}
	return shared
}

// discoverClaudeOauth is a seam so tests can stub host OAuth discovery without
// touching the real macOS Keychain (mirrors the secrets package's pattern).
var discoverClaudeOauth = secrets.DiscoverClaudeOauthToken

// warnExpiredAutoDiscoveredAuth prints an actionable warning when the only
// Anthropic credential cspace could find was an auto-discovered Claude Code
// OAuth token that had already expired — so secrets.Load refused to inject it
// (see secrets.OAuthExpired) and env carries no Anthropic credential. Returns
// true if it printed. Unlike the generic onboarding nudge this is shown on
// every run, because it is an active misconfiguration the user must act on,
// not a one-time hint. A carrier already present in env short-circuits it,
// preserving the generic-nudge behavior for the truly-absent case.
func warnExpiredAutoDiscoveredAuth(out io.Writer, env map[string]string) bool {
	if env["ANTHROPIC_API_KEY"] != "" || env["CLAUDE_CODE_OAUTH_TOKEN"] != "" {
		return false
	}
	oauth, expires, err := discoverClaudeOauth()
	if err != nil || oauth == "" || !secrets.OAuthExpired(expires) {
		return false
	}
	agoStr := time.Since(expires).Round(time.Minute).String()
	if time.Since(expires) < time.Minute {
		agoStr = "less than a minute"
	}
	_, _ = fmt.Fprintf(out, "warning: auto-discovered Claude Code OAuth token expired %s ago (at %s); not injecting it.\n",
		agoStr, expires.Local().Format("2006-01-02 15:04 MST"))
	_, _ = fmt.Fprintln(out, "  cspace refuses to inject an expired credential. To fix, do one of:")
	_, _ = fmt.Fprintln(out, "    - run `cspace keychain init` and paste a long-lived key (sk-ant-api-… or sk-ant-oat-…)  [recommended]")
	_, _ = fmt.Fprintln(out, "    - or run `claude` on the host to refresh the short-lived token, then `cspace up` again")
	_, _ = fmt.Fprintln(out, "  The sandbox will still boot, but Claude SDK calls will fail until auth is configured.")
	return true
}

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

// hostGitConfig returns the trimmed value of `git config --global --get <key>`
// run on the host, or "" if the key is unset or git is unavailable. Used to
// inherit the host's git identity into sandboxes.
func hostGitConfig(key string) string {
	out, err := exec.Command("git", "config", "--global", "--get", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// maybeRebuildStaleImage detects when the resolved cspace:latest image was
// built against a different cspace version than the running CLI. Rather than
// warning and booting a stale image anyway, it offers to rebuild first so the
// sandbox picks up matching scripts and tooling. Returns whether a rebuild
// actually ran so the caller can refresh its boot deadline.
//
// Behavior:
//   - label matches the CLI version: no-op, proceed.
//   - drift + forceRebuild (--rebuild): rebuild, then proceed.
//   - drift + canPrompt: prompt (default yes); rebuild on yes, otherwise warn
//     and proceed (the escape hatch is preserved).
//   - drift + !canPrompt: warn and proceed so the boot isn't blocked; hint that
//     --rebuild forces it. canPrompt is false in CI / piped contexts AND
//     whenever the overlay TUI is running — bubbletea holds stdin in raw mode,
//     so a prompt's stdin read would never return and would hang `cspace up`.
//
// Version comparison normalizes the leading "v": goreleaser strips it from
// {{ .Version }} so brew-installed binaries report "1.0.0-rc.X", while the
// maintainer Makefile's `git describe` keeps it ("v1.0.0-rc.X"). Both refer
// to the same release; the comparison treats them as equal. A missing label
// (older image predating the label) is treated as stale — same fix.
func maybeRebuildStaleImage(cmd *cobra.Command, image, cliVersion string, forceRebuild, canPrompt bool) (bool, error) {
	stderr := cmd.ErrOrStderr()
	imgVersion, hasLabel := readImageCspaceVersion(image)
	if !imageIsStale(imgVersion, hasLabel, cliVersion) {
		return false, nil // already current
	}

	reason := fmt.Sprintf("%s has no cspace.version label (built by an older cspace)", image)
	if hasLabel {
		reason = fmt.Sprintf("%s was built against cspace %s, but you're running %s", image, imgVersion, cliVersion)
	}

	doRebuild := forceRebuild
	if !forceRebuild {
		if !canPrompt {
			_, _ = fmt.Fprintf(stderr, "[cspace] warning: %s. Rebuild to pick up matching scripts and tooling: `cspace image build` (or re-run with --rebuild).\n", reason)
			return false, nil
		}
		_, _ = fmt.Fprintf(stderr, "[cspace] %s.\n", reason)
		doRebuild = promptYesNo(cmd, fmt.Sprintf("Rebuild %s now before launching?", image), true)
	}
	if !doRebuild {
		_, _ = fmt.Fprintf(stderr, "[cspace] continuing with the existing image; rebuild later with `cspace image build`.\n")
		return false, nil
	}

	_, _ = fmt.Fprintf(stderr, "[cspace] rebuilding %s before launch ...\n", image)
	if err := runImageBuild(cmd, image, false); err != nil {
		return false, fmt.Errorf("rebuild stale image: %w", err)
	}
	return true, nil
}

// imageIsStale reports whether an image's baked cspace.version drifts from the
// running CLI. hasLabel=false (no label at all — an image predating the label)
// counts as stale, since it can't be proven current and the fix is the same.
func imageIsStale(imgVersion string, hasLabel bool, cliVersion string) bool {
	if !hasLabel {
		return true
	}
	return normalizeVersion(imgVersion) != normalizeVersion(cliVersion)
}

// isStdinTTY reports whether os.Stdin is a terminal, so an interactive prompt
// can actually read a reply. False in CI / piped contexts.
func isStdinTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// promptYesNo writes a yes/no question to stderr and reads a reply from stdin,
// returning def on an empty line or read error (EOF).
func promptYesNo(cmd *cobra.Command, question string, def bool) bool {
	suffix := "[y/N]"
	if def {
		suffix = "[Y/n]"
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s %s ", question, suffix)
	line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	case "n", "no":
		return false
	default:
		return def
	}
}

// normalizeVersion strips a leading "v" so versions produced by goreleaser
// ("1.0.0-rc.X") compare equal to versions produced by `git describe`
// ("v1.0.0-rc.X"). Both refer to the same release tag.
func normalizeVersion(v string) string {
	return strings.TrimPrefix(v, "v")
}

// readImageCspaceVersion returns the `cspace.version` label from the named
// image's config, plus a found bool. Returns (""/false) on any error (missing
// image, malformed JSON, no labels).
func readImageCspaceVersion(image string) (string, bool) {
	out, err := exec.Command("container", "image", "inspect", image).Output()
	if err != nil {
		return "", false
	}
	// Apple Container's `image inspect` returns a JSON array of image
	// descriptors. Each has variants[].config.config.Labels (the outer
	// `config` is the OCI image config; the inner is the runtime config
	// holding Env / Cmd / Labels / …). Walk that path defensively; any
	// missing key is treated as "no label".
	var parsed []struct {
		Variants []struct {
			Config struct {
				Config struct {
					Labels map[string]string `json:"Labels"`
				} `json:"config"`
			} `json:"config"`
		} `json:"variants"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return "", false
	}
	for _, img := range parsed {
		for _, v := range img.Variants {
			if val, ok := v.Config.Config.Labels["cspace.version"]; ok && val != "" {
				return val, true
			}
		}
	}
	return "", false
}

// writeExtractedEnv writes credential key=value pairs extracted from sidecars
// to <sessionsHostDir>/extracted.env on the host. The sandbox has this
// directory bind-mounted as /sessions, so the entrypoint can source
// /sessions/extracted.env early — before user-visible env consumption.
//
// Values are shell-escaped (single-quote wrapping with internal ' replaced by
// '\”) so arbitrary credential strings don't break the sourced file.
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
// the substrate's RunSpec (sandbox-oriented). Sidecars use their own image's
// entrypoint.
type substrateRunner struct {
	adapter *applecontainer.Adapter
}

func (s *substrateRunner) Run(ctx context.Context, spec orchestrator.ServiceSpec) (string, error) {
	rspec := substrate.RunSpec{
		Name:    spec.Name,
		Image:   spec.Image,
		Env:     spec.Environment,
		Command: spec.Command,
		// Sidecars get full CPU but only 1 GiB RAM by default. The
		// workspace's substrate default is 4 CPU / 16 GiB, sized for
		// bun + tsc + vite peak. A typical compose service running
		// here (Rust convex-backend, Next convex-dashboard) idles
		// well under 500 MB, so we cap them at 1 GiB to keep host
		// memory pressure reasonable — each microVM's allocation
		// reserves a `container-runtime-linux` process of that size,
		// so multiplying the workspace default across 3 idle sidecars
		// wastes ~45 GiB per sandbox on a 24 GiB host.
		//
		// CPU is left equal to the workspace because some sidecars
		// (especially Rust services like convex-backend) burn a full
		// core during indexing or heavy queries; throttling those
		// would surface as flaky e2e timeouts long before the host
		// scheduler complains.
		//
		// Projects that need more memory (heavy Postgres, search
		// engine, ML model server) will get override paths via
		// customizations.cspace.resources in devcontainer.json — see
		// issue #82.
		CPUs:      4,
		MemoryMiB: 1024,
		// Compose-spawned sidecars don't run cspace's entrypoint script,
		// so they have no in-container DNS forwarder. Apple Container's
		// vmnet doesn't reliably propagate the host's resolver to every
		// image (esp. minimal alpine bases), so we explicitly attach
		// public resolvers. Convex actions calling out to third-party
		// APIs (Gemini, OpenAI, Resend, Sentry) need this; without it,
		// the sidecar's libc lookup fails with "Temporary failure in
		// name resolution" before any HTTP traffic leaves.
		//
		// Cluster-internal hostnames are still resolvable via the
		// orchestrator's /etc/hosts injection (which runs after Run).
		DNS: []string{"1.1.1.1", "8.8.8.8"},
	}
	for _, v := range spec.Volumes {
		rspec.Mounts = append(rspec.Mounts, substrate.Mount{
			HostPath:      v.HostPath,
			ContainerPath: v.GuestPath,
			ReadOnly:      v.ReadOnly,
		})
	}
	for _, n := range spec.NamedVolumes {
		rspec.Volumes = append(rspec.Volumes, substrate.NamedVolume{
			Name:          n.Name,
			ContainerPath: n.GuestPath,
			ReadOnly:      n.ReadOnly,
		})
	}
	for _, t := range spec.Tmpfs {
		rspec.TmpfsMounts = append(rspec.TmpfsMounts, substrate.TmpfsMount{
			ContainerPath: t.GuestPath,
			SizeMiB:       t.SizeMiB,
		})
	}
	if err := s.adapter.Run(ctx, rspec); err != nil {
		return "", err
	}
	return spec.Name, nil
}

func (s *substrateRunner) Exec(ctx context.Context, name string, cmd []string) (string, error) {
	// Orchestrator-side exec calls (hosts injection, credential extraction)
	// must run as root: minimal sidecar images (convex-dashboard, etc.) ship
	// a non-root USER, and /etc/hosts isn't writable from there.
	res, err := s.adapter.Exec(ctx, name, cmd, substrate.ExecOpts{User: "0"})
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
