// Package provision — teleport path. Moves an in-flight Claude Code
// session from a source cspace instance to a freshly provisioned target
// instance by seeding the workspace from a git bundle, copying the
// transcript into the target, and launching the supervisor in resume mode.
package provision

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/elliottregan/cspace/internal/compose"
	"github.com/elliottregan/cspace/internal/config"
	"github.com/elliottregan/cspace/internal/docker"
	"github.com/elliottregan/cspace/internal/instance"
	"github.com/elliottregan/cspace/internal/supervisor"
)

// TeleportParams holds inputs for a teleport-driven provision.
type TeleportParams struct {
	Name         string         // Target instance name
	TeleportFrom string         // Host path to the session transfer dir
	Cfg          *config.Config // Merged cspace config
}

// teleportManifest is the JSON shape written by lib/scripts/teleport-prepare.sh.
type teleportManifest struct {
	Source          string `json:"source"`
	Target          string `json:"target"`
	SessionID       string `json:"session_id"`
	CreatedAt       string `json:"created_at"`
	SourceHead      string `json:"source_head"`
	SourceBranch    string `json:"source_branch"`
	SourceRemoteURL string `json:"source_remote_url"`
}

func readTeleportManifest(dir string) (teleportManifest, error) {
	path := filepath.Join(dir, "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return teleportManifest{}, fmt.Errorf("reading manifest: %w", err)
	}
	var m teleportManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return teleportManifest{}, fmt.Errorf("parsing manifest: %w", err)
	}
	if m.SessionID == "" {
		return teleportManifest{}, fmt.Errorf("manifest missing session_id")
	}
	return m, nil
}

// TeleportRun provisions a new target instance seeded from a teleport
// transfer directory. Steps:
//  1. Validate name, read manifest, verify bundle + transcript exist,
//     reject running targets, and clear any stopped orphan containers
//  2. Ensure volumes and networks (same as Run)
//  3. docker compose up -d the target
//  4. Copy bundle into target, run init-workspace.sh against it
//  5. Copy transcript into target's ~/.claude/projects/-workspace/
//  6. Launch supervisor with ResumeSessionID; session comes up idle
//  7. Clean up the transfer directory
func TeleportRun(p TeleportParams) error {
	if err := validateName(p.Name); err != nil {
		return err
	}

	manifest, err := readTeleportManifest(p.TeleportFrom)
	if err != nil {
		return err
	}

	bundle := filepath.Join(p.TeleportFrom, "workspace.bundle")
	transcript := filepath.Join(p.TeleportFrom, "session.jsonl")
	for _, f := range []string{bundle, transcript} {
		if _, err := os.Stat(f); err != nil {
			return fmt.Errorf("teleport transfer missing %s: %w", f, err)
		}
	}

	cfg := p.Cfg
	composeName := cfg.ComposeName(p.Name)

	if instance.IsRunning(composeName) {
		return fmt.Errorf("target instance '%s' is already running; choose a different name", p.Name)
	}

	fmt.Printf("Teleporting session %s from %s to %s...\n", manifest.SessionID, manifest.Source, p.Name)

	// Refuse to overwrite an existing stopped container with the same name.
	for _, suffix := range []string{"", ".browser"} {
		if err := docker.RemoveOrphanContainer(composeName + suffix); err != nil {
			return fmt.Errorf("refusing to teleport into '%s': %w", p.Name, err)
		}
	}

	if err := ensureVolumes(cfg); err != nil {
		return fmt.Errorf("creating volumes: %w", err)
	}

	if err := docker.NetworkCreate(cfg.ProjectNetwork(), cfg.InstanceLabel()); err != nil {
		return fmt.Errorf("creating project network: %w", err)
	}

	if err := docker.EnsureProxy(cfg.AssetsDir); err != nil {
		fmt.Fprintf(os.Stderr, "warning: proxy: %v\n", err)
	}
	if err := docker.NetworkConnect(cfg.ProjectNetwork(), docker.ProxyContainerName); err != nil {
		fmt.Fprintf(os.Stderr, "warning: connecting proxy to project network: %v\n", err)
	}

	// Ensure the teleport host dir is set for the target's bind mount too,
	// even though the target doesn't read from it — the compose env var must
	// be defined or compose-up fails to expand the volume line.
	os.Setenv("CSPACE_TELEPORT_DIR", teleportHostDir())

	if err := compose.Run(p.Name, cfg, "up", "-d"); err != nil {
		return fmt.Errorf("starting container: %w", err)
	}

	if err := WaitForReady(composeName, 120*time.Second); err != nil {
		return err
	}
	if err := docker.InjectHosts(composeName, cfg.ProjectNetwork()); err != nil {
		fmt.Fprintf(os.Stderr, "warning: hosts injection: %v\n", err)
	}
	if _, err := instance.DcExecRoot(composeName, "chown", "-R", "dev:dev", "/workspace", "/home/dev/.claude"); err != nil {
		return fmt.Errorf("fixing ownership: %w", err)
	}

	// Seed workspace from the teleport bundle. init-workspace.sh takes
	// (bundle, branch, remote-url); branch defaults to the source HEAD's
	// branch; remote-url comes from the source container's origin remote
	// and is carried in the manifest.
	if err := initWorkspace(composeName, bundle, manifest.SourceBranch, manifest.SourceRemoteURL); err != nil {
		return fmt.Errorf("initializing workspace: %w", err)
	}
	configureGit(composeName, cfg.ProjectRoot)

	// Copy the transcript into the target's projects dir so Claude Code
	// can find it when the supervisor resumes.
	targetTranscript := "/home/dev/.claude/projects/-workspace/" + manifest.SessionID + ".jsonl"
	if _, err := instance.DcExecRoot(composeName, "mkdir", "-p", "/home/dev/.claude/projects/-workspace"); err != nil {
		return fmt.Errorf("creating projects dir in target: %w", err)
	}
	if err := instance.DcCp(composeName, transcript, targetTranscript); err != nil {
		return fmt.Errorf("copying transcript into target: %w", err)
	}
	if _, err := instance.DcExecRoot(composeName, "chown", "dev:dev", targetTranscript); err != nil {
		return fmt.Errorf("chown transcript: %w", err)
	}

	// Idempotent tail stages (plugins, post-setup) — same as Run().
	if err := ensureMarketplace(composeName); err != nil {
		fmt.Fprintf(os.Stderr, "warning: marketplace setup: %v\n", err)
	}
	if err := installPlugins(composeName, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "warning: plugin installation: %v\n", err)
	}
	if err := runPostSetup(composeName, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "warning: post-setup: %v\n", err)
	}

	instance.SkipOnboarding(composeName)

	// Launch the supervisor in resume mode. The session is "resumed idle":
	// no initial prompt is played, so the user can reconnect and continue.
	if err := supervisor.LaunchSupervisor(supervisor.LaunchParams{
		Name:            p.Name,
		Role:            supervisor.RoleAgent,
		StderrLog:       supervisor.ContainerAgentStderrLog,
		ResumeSessionID: manifest.SessionID,
	}, cfg); err != nil {
		return fmt.Errorf("launching resume supervisor: %w", err)
	}

	// Clean up the transfer dir on success.
	if err := os.RemoveAll(p.TeleportFrom); err != nil {
		fmt.Fprintf(os.Stderr, "warning: cleaning transfer dir: %v\n", err)
	}

	fmt.Printf("Teleport complete. Reconnect with: cspace resume %s\n", p.Name)
	return nil
}
