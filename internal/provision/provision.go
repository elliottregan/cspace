// Package provision implements cspace devcontainer provisioning.
// It is the Go port of lib/scripts/setup-instance.sh.
package provision

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/elliottregan/cspace/internal/compose"
	"github.com/elliottregan/cspace/internal/config"
	"github.com/elliottregan/cspace/internal/docker"
	"github.com/elliottregan/cspace/internal/instance"
)

var nameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Params holds everything needed to provision an instance.
type Params struct {
	Name            string         // Instance name (validated: alphanumeric + hyphens/underscores)
	Branch          string         // Git branch to checkout (empty = host's current branch)
	Cfg             *config.Config // Merged configuration
	Reporter        Reporter       // Progress reporter; nil → logReporter{}
	Stdout          io.Writer      // Subprocess stdout; nil → os.Stdout
	Stderr          io.Writer      // Subprocess stderr; nil → os.Stderr
	BootstrapSearch bool           // Run `cspace search init` during provisioning (opt-in)
}

func (p Params) reporter() Reporter {
	if p.Reporter != nil {
		return p.Reporter
	}
	return logReporter{}
}

func (p Params) stdout() io.Writer {
	if p.Stdout != nil {
		return p.Stdout
	}
	return os.Stdout
}

func (p Params) stderr() io.Writer {
	if p.Stderr != nil {
		return p.Stderr
	}
	return os.Stderr
}

// Result holds the outcome of a provisioning run.
type Result struct {
	Created bool   // true if container was newly created, false if already running
	Name    string // Instance name
}

// Run provisions a cspace devcontainer instance. Idempotent — safe to
// re-run on a partially configured instance.
//
// Progress is reported through p.Reporter (defaults to logReporter{}), which
// receives one Phase call per entry in the Phases slice plus a final Done or
// Error call. When the container is already running, phases 2–14 are skipped
// and phase 1 is reported as "Reusing running container"; phase 15 (the
// idempotent tail: marketplace, plugins, post-setup) always runs.
//
// Phases (see the Phases slice for labels):
//  1. Validating name
//  2. Removing orphans
//  3. Bundling repo
//  4. Creating volumes
//  5. Creating network
//  6. Starting search stack (project-scoped sidecars, idempotent)
//  7. Starting reverse proxy
//  8. Setting up directories (teleport, memory, sessions, context)
//  9. Starting containers (docker compose up -d)
//  10. Waiting for container
//  11. Configuring hosts (inject cspace.local → Traefik)
//  12. Setting permissions (chown /workspace, /home/dev/.claude, /teleport)
//  13. Initializing workspace (bundle unpack + init-workspace.sh)
//  14. Configuring git & env (git identity, .env files, GH_TOKEN)
//  15. Installing plugins (marketplace + plugins + post-setup)
//  16. Syncing workspace (git fetch / checkout / pull, skip Claude onboarding)
//  17. Bootstrapping search (cspace search init in the background)
func Run(p Params) (result Result, err error) {
	reporter := p.reporter()
	currentPhase := ""
	reportPhase := func(num int, label string) {
		currentPhase = label
		reporter.Phase(label, num, len(Phases))
	}
	reportWarn := func(msg string) { reporter.Warn(msg) }
	defer func() {
		if err != nil {
			reporter.Error(currentPhase, err)
			return
		}
		reporter.Done()
	}()

	name := p.Name
	cfg := p.Cfg

	// Pre-phase validation (no reporter event — failure here is a user
	// input bug, not a provisioning phase).
	if err = validateName(name); err != nil {
		return Result{}, err
	}

	composeName := cfg.ComposeName(name)
	created := false

	if instance.IsRunning(composeName) {
		reportPhase(1, "Reusing running container")
	} else {
		created = true

		// Phase 1: validate (spec label) — note: actual validation
		// already ran above.
		reportPhase(1, Phases[0])

		// Phase 2: remove orphaned containers from a previous instance with
		// the same name. Handles partial teardowns where docker compose
		// down didn't fully clean up. Refuses to remove running containers
		// to prevent accidentally destroying a live instance. Runs before
		// the expensive git bundle so we fail fast.
		reportPhase(2, Phases[1])
		for _, suffix := range []string{"", ".browser"} {
			if err = docker.RemoveOrphanContainer(composeName + suffix); err != nil {
				return Result{}, fmt.Errorf("refusing to provision '%s': %w", name, err)
			}
		}

		// Phase 3: detect branch, remote URL, and create git bundle.
		branch := p.Branch
		if branch == "" {
			branch = gitCurrentBranch(cfg.ProjectRoot)
		}
		remoteURL := gitRemoteURL(cfg.ProjectRoot)
		bundlePath := filepath.Join(os.TempDir(), fmt.Sprintf("cspace-%s.bundle", name))
		reportPhase(3, Phases[2])
		if err = gitBundleCreate(cfg.ProjectRoot, bundlePath, p.stdout(), p.stderr()); err != nil {
			return Result{}, fmt.Errorf("creating git bundle: %w", err)
		}
		defer func() { _ = os.Remove(bundlePath) }()

		// Phase 4: create shared volumes.
		reportPhase(4, Phases[3])
		if err = ensureVolumesReported(cfg, reportWarn); err != nil {
			return Result{}, fmt.Errorf("creating volumes: %w", err)
		}

		// Phase 5: ensure the shared project network exists before
		// starting the Compose stack. The cspace service declares this as
		// an external network in docker-compose.core.yml, so it must exist
		// before compose up.
		reportPhase(5, Phases[4])
		if err = docker.NetworkCreate(cfg.ProjectNetwork(), cfg.InstanceLabel()); err != nil {
			return Result{}, fmt.Errorf("creating project network: %w", err)
		}

		// Phase 6: start the project-scoped search sidecar stack
		// (qdrant, llama-server, etc.). Idempotent — if the stack is
		// already running from a prior instance's boot, this is a no-op.
		// Runs before the instance compose up so sidecars are reachable
		// via network aliases when the instance starts.
		reportPhase(6, Phases[5])
		if perr := compose.ProjectStackUp(cfg); perr != nil {
			reportWarn(fmt.Sprintf("project search stack: %v", perr))
		}

		// Phase 7: start the global reverse proxy and connect it to the
		// project network so Traefik can route traffic to instance
		// containers.
		reportPhase(7, Phases[6])
		if perr := docker.EnsureProxy(cfg.AssetsDir); perr != nil {
			reportWarn(fmt.Sprintf("proxy: %v", perr))
		}
		if perr := docker.NetworkConnect(cfg.ProjectNetwork(), docker.ProxyContainerName); perr != nil {
			reportWarn(fmt.Sprintf("connecting proxy to project network: %v", perr))
		}

		// Phase 8: set up host-side directories that compose will bind
		// mount (teleport, memory, sessions, context). Docker auto-creates
		// missing bind-mount paths as root-owned — we create them first so
		// they're writable by the invoking user.
		reportPhase(8, Phases[7])
		tpDir := teleportHostDir()
		if err = ensureTeleportDir(tpDir); err != nil {
			return Result{}, err
		}
		if err = os.Setenv("CSPACE_TELEPORT_DIR", tpDir); err != nil {
			return Result{}, fmt.Errorf("exporting CSPACE_TELEPORT_DIR: %w", err)
		}
		if err = ensureMemoryDir(cfg.ProjectRoot); err != nil {
			return Result{}, err
		}
		if err = ensureSessionsDir(cfg.SessionsDir()); err != nil {
			return Result{}, err
		}
		if err = ensureContextDir(cfg.ProjectRoot); err != nil {
			return Result{}, err
		}

		// Phase 9: start instance containers.
		// OrbStack's runc occasionally fails the first container start with a
		// transient "read-only file system" error when creating nested bind-mount
		// points (e.g. .cspace/context inside /workspace). The containers get
		// created fine; a follow-up `docker compose start` starts them cleanly.
		reportPhase(9, Phases[8])
		if out, upErr := compose.RunCapture(name, cfg, "up", "-d"); upErr != nil {
			_, _ = fmt.Fprint(os.Stderr, out)
			if !strings.Contains(out, "read-only file system") {
				return Result{}, fmt.Errorf("starting container: %w", upErr)
			}
			reportWarn("orbstack/runc mount race on first start — retrying via docker compose start")
			if err = compose.Run(name, cfg, "start"); err != nil {
				return Result{}, fmt.Errorf("starting container (after retry): %w", err)
			}
		} else {
			_, _ = fmt.Fprint(os.Stdout, out)
		}

		// Phase 10: wait for container readiness.
		reportPhase(10, Phases[9])
		if err = WaitForReady(composeName, 120*time.Second); err != nil {
			return Result{}, err
		}

		// Phase 11: inject /etc/hosts entries so cspace.local hostnames
		// resolve to Traefik's Docker IP inside all containers.
		reportPhase(11, Phases[10])
		if herr := docker.InjectHosts(composeName, cfg.ProjectNetwork()); herr != nil {
			reportWarn(fmt.Sprintf("hosts injection: %v", herr))
		}

		// Phase 12: fix volume ownership. /teleport is a host bind mount
		// that Docker auto-creates as root when the host path doesn't
		// already exist (common in nested DinD setups), so explicitly fix
		// it alongside the named volumes to ensure the dev user can write
		// bundles there.
		reportPhase(12, Phases[11])
		if _, err = instance.DcExecRoot(composeName, "chown", "-R", "dev:dev", "/workspace", "/home/dev/.claude", "/teleport"); err != nil {
			return Result{}, fmt.Errorf("fixing ownership: %w", err)
		}

		// Phase 13: copy bundle and init workspace.
		reportPhase(13, Phases[12])
		if err = initWorkspace(composeName, bundlePath, branch, remoteURL); err != nil {
			return Result{}, fmt.Errorf("initializing workspace: %w", err)
		}

		// Phase 14: configure git identity, copy .env files, setup
		// GH_TOKEN + gh auth.
		reportPhase(14, Phases[13])
		configureGit(composeName, cfg.ProjectRoot)
		copyEnvFile(composeName, cfg.ProjectRoot, ".env")
		copyEnvFile(composeName, cfg.ProjectRoot, ".env.local")
		if gerr := setupGHAuth(composeName, cfg.ProjectRoot); gerr != nil {
			reportWarn(gerr.Error())
		}
	}

	// Port mappings are stable once the container is up, but cheap enough
	// to probe and visually satisfying during the overlay's plugin-install
	// phase (`Installing plugins` takes the longest of any step). Emit
	// them via the reporter so the overlay streams them into the HUD and
	// the logReporter prints them inline — matching what ShowPorts used
	// to dump after provision.Run returned.
	for _, b := range instance.ProbePorts(name, cfg) {
		reporter.Port(b.Label, b.URL)
	}

	// Phase 15: idempotent tail (marketplace + plugins + post-setup). Runs
	// for both new and reused containers.
	reportPhase(15, Phases[14])
	if merr := ensureMarketplace(composeName); merr != nil {
		reportWarn(fmt.Sprintf("marketplace setup: %v", merr))
	}
	if ierr := installPlugins(composeName, cfg, reporter); ierr != nil {
		reportWarn(fmt.Sprintf("plugin installation: %v", ierr))
	}
	if perr := runPostSetup(composeName, cfg); perr != nil {
		reportWarn(fmt.Sprintf("post-setup: %v", perr))
	}

	// Phase 16: last-mile workspace sync. Folded into provision.Run so
	// callers can exec Claude immediately after Run returns, with no
	// post-overlay shell commands flashing the main terminal buffer.
	// SkipOnboarding pre-accepts Claude Code's first-run prompts; the
	// git ops bring /workspace up to date before handoff.
	reportPhase(16, Phases[15])
	if sErr := instance.SkipOnboarding(composeName); sErr != nil {
		reportWarn(fmt.Sprintf("skip onboarding: %v", sErr))
	}
	_, _ = instance.DcExec(composeName, "git", "fetch", "--prune", "--quiet")
	if p.Branch != "" {
		if _, err := instance.DcExec(composeName, "git", "checkout", p.Branch); err != nil {
			_, _ = instance.DcExec(composeName, "git", "checkout", "-b", p.Branch, "origin/"+p.Branch)
		}
		_, _ = instance.DcExec(composeName, "git", "reset", "--hard", "origin/"+p.Branch)
	} else {
		_, _ = instance.DcExec(composeName, "git", "pull", "--ff-only", "--quiet")
	}

	// Phase 17: bootstrap semantic search — opt-in. Fires only when
	// BootstrapSearch is true (advisors, coordinators, or explicit --index).
	// Runs `cspace search init` in the background so the Claude handoff
	// isn't blocked. Content-hash check makes re-runs a near no-op.
	if p.BootstrapSearch {
		reportPhase(17, Phases[16])
		if _, err := instance.DcExec(composeName, "bash", "-c",
			"mkdir -p /workspace/.cspace && nohup cspace search init --quiet >>/workspace/.cspace/search-index.log 2>&1 &"); err != nil {
			reportWarn(fmt.Sprintf("search bootstrap: %v", err))
		}
	}

	return Result{Created: created, Name: name}, nil
}

// WaitForReady polls the container until docker exec succeeds, with timeout.
func WaitForReady(composeName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := instance.DcExecRoot(composeName, "true"); err == nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("container did not become ready within %v", timeout)
}

// validateName checks that the instance name matches ^[a-zA-Z0-9_-]+$.
func validateName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("instance name must be alphanumeric (hyphens and underscores allowed), got: %s", name)
	}
	return nil
}

// gitCurrentBranch returns the current branch of the project repo.
func gitCurrentBranch(projectRoot string) string {
	out, err := exec.Command("git", "-C", projectRoot, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "main"
	}
	return strings.TrimSpace(string(out))
}

// gitRemoteURL returns the origin remote URL with embedded credentials stripped.
func gitRemoteURL(projectRoot string) string {
	out, err := exec.Command("git", "-C", projectRoot, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return stripCredentials(strings.TrimSpace(string(out)))
}

// stripCredentials removes embedded auth credentials from a URL.
// Converts "https://user:pass@host/path" to "https://host/path".
func stripCredentials(url string) string {
	if idx := strings.Index(url, "://"); idx >= 0 {
		rest := url[idx+3:]
		if atIdx := strings.Index(rest, "@"); atIdx >= 0 {
			url = url[:idx+3] + rest[atIdx+1:]
		}
	}
	return url
}

// ensureTeleportDir creates the host-side teleport transfer directory if
// it does not already exist. This path is bind-mounted into every cspace
// container at /teleport so teleport can move bundles and transcripts
// between source and target without going through docker cp.
func ensureTeleportDir(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating teleport dir %s: %w", dir, err)
	}
	return nil
}

// memoryStub is written to .cspace/memory/MEMORY.md on first provision so
// agents' first "read MEMORY.md" call finds the expected file. The header
// also explains the convention to humans browsing the repo.
const memoryStub = `<!--
This directory holds project-shared Claude Code memory.

It is bind-mounted into every cspace container at:
  /home/dev/.claude/projects/-workspace/memory

Agents read and write here via the built-in memory system (four types:
user, feedback, project, reference). Committed to git so learnings
survive volume wipes and propagate to fresh clones.

See CLAUDE.md for the full convention.
-->
`

// ensureMemoryDir creates ${ProjectRoot}/.cspace/memory/ with host-user
// ownership before compose bind-mounts it into the container. Docker's
// auto-create on bind-mount makes the dir root-owned, which breaks the
// agent's ability to write. Also seeds MEMORY.md if missing.
func ensureMemoryDir(projectRoot string) error {
	dir := filepath.Join(projectRoot, ".cspace", "memory")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating memory dir %s: %w", dir, err)
	}
	stubPath := filepath.Join(dir, "MEMORY.md")
	if _, err := os.Stat(stubPath); os.IsNotExist(err) {
		if err := os.WriteFile(stubPath, []byte(memoryStub), 0644); err != nil {
			return fmt.Errorf("writing memory stub %s: %w", stubPath, err)
		}
	}
	return nil
}

// ensureSessionsDir creates the shared host-side sessions directory
// (default $HOME/.cspace/sessions/<project>/) with invoking-user
// ownership before compose bind-mounts it. All containers for this
// project share this directory — it holds every Claude Code session
// JSONL transcript, survives volume wipes, and makes teleport a
// trivial "resume by session ID" rather than a JSONL shipping dance.
// No stub file: Claude Code creates its own session files on first run.
func ensureSessionsDir(sessionsDir string) error {
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		return fmt.Errorf("creating sessions dir %s: %w", sessionsDir, err)
	}
	return nil
}

// ensureContextDir creates ${ProjectRoot}/.cspace/context/ with host-user
// ownership before compose bind-mounts it. This is the cspace-context
// MCP server's store; bind-mounting gives every container in the
// project a shared, live view of decisions, discoveries, and findings.
// The directory is committed to git, so agent writes here show up in
// the host's `git status` ready to be committed.
//
// contextstore.ensureSeeded() will lay down the three human-owned
// template files (direction/principles/roadmap) on first write; we
// just need the directory itself to exist and be writable.
func ensureContextDir(projectRoot string) error {
	dir := filepath.Join(projectRoot, "docs", "context")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating context dir %s: %w", dir, err)
	}
	return nil
}

// teleportHostDir returns the host-side path used for the /teleport bind
// mount. Defaults to ~/.cspace/teleport; overridable via CSPACE_TELEPORT_DIR.
func teleportHostDir() string {
	if v := os.Getenv("CSPACE_TELEPORT_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/cspace-teleport"
	}
	return filepath.Join(home, ".cspace", "teleport")
}

// gitBundleCreate creates a git bundle of the entire repo.
func gitBundleCreate(projectRoot, bundlePath string, stdout, stderr io.Writer) error {
	cmd := exec.Command("git", "-C", projectRoot, "bundle", "create", bundlePath, "--all")
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// gitConfigValue reads a git config value from the project repo.
func gitConfigValue(projectRoot, key string) string {
	out, _ := exec.Command("git", "-C", projectRoot, "config", key).Output()
	return strings.TrimSpace(string(out))
}

// ensureVolumes creates the shared external volumes if they don't already exist.
// Memory is NOT an external volume — it's bind-mounted from the project's
// .cspace/memory/ directory so learnings persist in git. See ensureMemoryDir.
func ensureVolumes(cfg *config.Config) error {
	for _, vol := range []string{cfg.LogsVolume()} {
		if err := docker.VolumeCreate(vol); err != nil {
			// Log but don't fail — volume may already exist
			fmt.Fprintf(os.Stderr, "warning: %v\n", err)
		}
	}
	return nil
}

// ensureVolumesReported is the Reporter-aware variant of ensureVolumes used
// by provision.Run so warnings flow through the provided reporter instead of
// directly to stderr. teleport.go still uses ensureVolumes for its own path.
func ensureVolumesReported(cfg *config.Config, warn func(string)) error {
	for _, vol := range []string{cfg.LogsVolume()} {
		if err := docker.VolumeCreate(vol); err != nil {
			warn(err.Error())
		}
	}
	return nil
}

// initWorkspace copies the git bundle into the container and runs init-workspace.sh.
func initWorkspace(composeName, bundlePath, branch, remoteURL string) error {
	fmt.Println("Copying repo bundle into container...")
	if err := instance.DcCp(composeName, bundlePath, "/tmp/repo.bundle"); err != nil {
		return fmt.Errorf("copying bundle: %w", err)
	}
	if _, err := instance.DcExecRoot(composeName, "chown", "dev:dev", "/tmp/repo.bundle"); err != nil {
		return fmt.Errorf("chown bundle: %w", err)
	}

	fmt.Println("Initializing workspace...")
	if _, err := instance.DcExec(composeName, "init-workspace.sh", "/tmp/repo.bundle", branch, remoteURL); err != nil {
		return fmt.Errorf("init-workspace.sh: %w", err)
	}
	_, _ = instance.DcExecRoot(composeName, "rm", "-f", "/tmp/repo.bundle")
	return nil
}

// configureGit sets git user.name and user.email inside the container from host config.
// Warns (but does not fail) when the host has no git identity configured, because
// in-container commits will fail later with a cryptic "please tell me who you are".
// Also warns if the in-container `git config` calls themselves fail.
func configureGit(composeName, projectRoot string) {
	name := gitConfigValue(projectRoot, "user.name")
	email := gitConfigValue(projectRoot, "user.email")

	if name == "" && email == "" {
		fmt.Fprintf(os.Stderr,
			"warning: host has no git user.name or user.email configured; "+
				"in-container commits will fail until you set them on the host\n")
		return
	}

	if name != "" {
		if _, err := instance.DcExec(composeName, "git", "config", "--global", "user.name", name); err != nil {
			fmt.Fprintf(os.Stderr, "warning: setting git user.name in container: %v\n", err)
		}
	}
	if email != "" {
		if _, err := instance.DcExec(composeName, "git", "config", "--global", "user.email", email); err != nil {
			fmt.Fprintf(os.Stderr, "warning: setting git user.email in container: %v\n", err)
		}
	}
}

// copyEnvFile copies a single env file from the project root into the container.
func copyEnvFile(composeName, projectRoot, filename string) {
	src := filepath.Join(projectRoot, filename)
	if _, err := os.Stat(src); err != nil {
		return
	}
	if err := instance.DcCp(composeName, src, "/workspace/"+filename); err != nil {
		fmt.Fprintf(os.Stderr, "warning: copying %s: %v\n", filename, err)
		return
	}
	_, _ = instance.DcExecRoot(composeName, "chown", "dev:dev", "/workspace/"+filename)
	fmt.Printf("Copied %s\n", filename)
}

// setupGHAuth configures gh auth inside the container if GH_TOKEN is set.
// Returns a descriptive error if GH_TOKEN is missing or gh auth fails
// (callers should warn, not fail).
func setupGHAuth(composeName, projectRoot string) error {
	// Check token presence separately from auth setup so the error
	// message accurately describes the failure.
	if _, err := instance.DcExec(composeName, "bash", "-c",
		`[ -n "${GH_TOKEN:-}" ]`); err != nil {
		//nolint:staticcheck // Multi-line user-facing error; formatting trumps ST1005.
		return fmt.Errorf(`GH_TOKEN is not set in the container.

Agents in this instance will not be able to push, pull, or open PRs.

Fix:
  1. Create a GitHub token with scopes: repo, workflow, read:org
     https://github.com/settings/tokens/new?scopes=repo,workflow,read:org
  2. Add it to your project .env (or shell env):
     echo 'GH_TOKEN=ghp_...' >> %s/.env
  3. Tear down and recreate this instance:
     cspace down <name> && cspace up <name>

For SSO-protected org repos, also authorize the token for your org.`, projectRoot)
	}

	if _, err := instance.DcExec(composeName, "bash", "-c",
		`gh auth setup-git`); err != nil {
		return fmt.Errorf("gh auth setup-git failed (GH_TOKEN is set but may be invalid): %w", err)
	}

	fmt.Println("gh CLI configured for git push/pull")
	return nil
}

// ensureMarketplace clones the claude-plugins-official marketplace if not present.
func ensureMarketplace(composeName string) error {
	mdir := "/home/dev/.claude/plugins/marketplaces/claude-plugins-official"
	if _, err := instance.DcExec(composeName, "test", "-d", mdir); err == nil {
		fmt.Println("Plugin marketplace already present.")
		return nil
	}

	fmt.Println("Cloning plugin marketplace...")
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	script := fmt.Sprintf(
		`mkdir -p $(dirname %s) && git clone --depth 1 https://github.com/anthropics/claude-plugins-official.git %s && printf '{"claude-plugins-official":{"source":{"source":"github","repo":"anthropics/claude-plugins-official"},"installLocation":"%s","lastUpdated":"%s"}}' > /home/dev/.claude/plugins/known_marketplaces.json`,
		mdir, mdir, mdir, timestamp,
	)
	_, err := instance.DcExec(composeName, "bash", "-c", script)
	return err
}

// installPlugins installs recommended plugins from config, with an idempotency marker.
// Each plugin name is emitted through reporter.Log so overlay callers can
// render a live tail of the install progress; the default logReporter
// prints them as indented lines under the phase header (preserving the
// pre-reporter behavior).
func installPlugins(composeName string, cfg *config.Config, reporter Reporter) error {
	if !cfg.Plugins.Enabled {
		fmt.Println("Plugin installation disabled in config.")
		return nil
	}

	marker := "/home/dev/.claude/plugins/.plugins-installed"
	if _, err := instance.DcExec(composeName, "test", "-f", marker); err == nil {
		fmt.Println("Recommended plugins already installed.")
		return nil
	}

	if len(cfg.Plugins.Install) == 0 {
		return nil
	}

	fmt.Println("Installing recommended plugins...")
	for _, plugin := range cfg.Plugins.Install {
		reporter.Log(plugin)
		// Ignore individual plugin install errors (matching bash behavior)
		_, _ = instance.DcExec(composeName, "claude", "plugin", "install", plugin)
	}

	_, _ = instance.DcExec(composeName, "touch", marker)
	fmt.Println("Plugin installation complete.")
	return nil
}

// runPostSetup copies and executes the post-setup hook if configured.
func runPostSetup(composeName string, cfg *config.Config) error {
	// Resolve post-setup script: explicit config takes priority,
	// then auto-detect from .devcontainer/post-setup.sh.
	var src string
	if cfg.PostSetup != "" {
		src = filepath.Join(cfg.ProjectRoot, cfg.PostSetup)
	} else {
		src = filepath.Join(cfg.ProjectRoot, ".devcontainer", "post-setup.sh")
	}

	if _, err := os.Stat(src); err != nil {
		return nil
	}

	// Marker lives outside /workspace so it doesn't appear as an
	// untracked file in the agent's git status. /home/dev is inside the
	// per-instance claude-home volume, so the marker persists across
	// container restarts but is re-created on a fresh instance (matching
	// the "run once per container lifetime" semantics we want).
	marker := "/home/dev/.cspace-post-setup-done"
	if _, err := instance.DcExec(composeName, "test", "-f", marker); err == nil {
		fmt.Println("Post-setup already completed.")
		return nil
	}

	fmt.Println("Running post-setup hook...")
	if err := instance.DcCp(composeName, src, "/tmp/post-setup.sh"); err != nil {
		return fmt.Errorf("copying post-setup script: %w", err)
	}
	_, _ = instance.DcExecRoot(composeName, "chmod", "+x", "/tmp/post-setup.sh")
	if err := instance.DcExecStream(composeName, "bash", "/tmp/post-setup.sh"); err != nil {
		return fmt.Errorf("running post-setup script: %w", err)
	}
	_, _ = instance.DcExec(composeName, "touch", marker)
	fmt.Println("Post-setup complete.")
	return nil
}
