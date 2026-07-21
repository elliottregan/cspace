// Package applecontainer implements substrate.Substrate against Apple's
// `container` CLI (github.com/apple/container).
//
// VERSION COUPLING: tested against 1.1.x. The JSON shape changed at the 1.0
// boundary — runtime state (state word, startedDate, networks) that 0.12.x
// emitted flat now nests under a `status` object in both `container inspect`
// and `container ls --format json` (see inspectRecord / listRecord). Known
// quirks:
//
//   - `container inspect` does NOT support a --format flag. We parse JSON.
//   - `container inspect` of a missing container exits non-zero on 1.x
//     ("Error: container not found"); 0.12.x instead exited 0 with body
//     "[]". The registry-prune `containerExists` helper handles both.
//   - The DNS port 5353/udp conflicts with macOS's mDNSResponder, so the
//     daemon binds on 5354 (see internal/cli/cmd_daemon.go).
//   - `container system kernel set --recommended` must be run by hand on
//     fresh installs (the apiserver's first start tries to read stdin).
//
// VersionStatus() reports whether the installed CLI matches the tested
// minor version. cspace up logs a one-line warning when out of range.
package applecontainer

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/elliottregan/cspace/internal/substrate"
)

// supportedMinorVersion is the Apple Container CLI MAJOR.MINOR version cspace
// has been tested against. Versions outside this range trigger a warning
// (non-fatal) at cspace up time. Bumping this is a deliberate act: verify
// the JSON shape of `container inspect` and the other quirks listed in the
// package doc still hold.
const supportedMinorVersion = "1.1"

// SupportedMinorVersion returns the Apple Container CLI MAJOR.MINOR version
// cspace has been tested against. Exposed as a function (rather than the raw
// const) so the cli package can format warning messages without coupling to
// the constant's name.
func SupportedMinorVersion() string { return supportedMinorVersion }

// Adapter is the substrate.Substrate implementation backed by `container`.
type Adapter struct{}

// New returns a ready-to-use Adapter. Call Available() to check whether the
// underlying CLI is on PATH before exercising the other methods.
func New() *Adapter { return &Adapter{} }

// Available reports whether the `container` binary is on PATH. It does not
// check apiserver health; callers that need that should run
// `container system status` separately.
func (a *Adapter) Available() bool {
	_, err := exec.LookPath("container")
	return err == nil
}

// Version returns the raw output of `container --version`. The output shape
// is unstable across pre-1.0 releases (currently "container CLI version
// 0.12.3 (build: release, commit: ...)"); callers that need a parsed minor
// version should use VersionStatus instead.
func (a *Adapter) Version(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "container", "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("container --version: %w (output: %s)",
			err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// versionRE matches a semver-like X.Y.Z anywhere in a version string. It is
// deliberately permissive so it survives format churn ("container 0.12.3",
// "container CLI version 0.12.3", "container CLI version 0.12.3 (build:
// release, commit: ...)" all work).
var versionRE = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// VersionStatus reports whether `container --version` is within cspace's
// tested range. Returns the raw version string, an "ok" tag, and any error
// encountered probing. A version that can't be parsed is reported as
// supported=false rather than as an error so callers can degrade to a
// warning rather than failing closed on a format change.
func (a *Adapter) VersionStatus(ctx context.Context) (rawVersion string, supported bool, err error) {
	raw, err := a.Version(ctx)
	if err != nil {
		return "", false, err
	}
	m := versionRE.FindStringSubmatch(raw)
	if len(m) < 3 {
		return raw, false, nil
	}
	minor := m[1] + "." + m[2]
	return raw, minor == supportedMinorVersion, nil
}

// HealthCheck verifies the Apple Container apiserver is running. Returns a
// clear, user-actionable error when not — usually the user just needs to
// run `container system start`.
func (a *Adapter) HealthCheck(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "container", "system", "status")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("container system status: %w (output: %s)",
			err, strings.TrimSpace(string(out)))
	}
	return parseSystemStatus(string(out))
}

// parseSystemStatus interprets `container system status` output, returning
// nil only when the apiserver is affirmatively reported running. 0.12.x
// prints a FIELD/VALUE table with a `status` row; older builds print prose.
// Anything unrecognized is treated as not running — a bare substring test
// is unsafe because "apiserver is not running" contains "running".
func parseSystemStatus(output string) error {
	text := strings.ToLower(output)
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "status" {
			if fields[1] == "running" {
				return nil
			}
			return fmt.Errorf("apple container apiserver not running (status: %s)", fields[1])
		}
	}
	if strings.Contains(text, "not running") {
		return fmt.Errorf("apple container apiserver not running: %s",
			strings.TrimSpace(output))
	}
	if strings.Contains(text, "running") {
		return nil
	}
	return fmt.Errorf("unrecognized `container system status` output (treating apiserver as not running): %s",
		strings.TrimSpace(output))
}

// Default resource caps for cspace sandboxes when the caller hasn't
// asked for anything specific. Apple Container's own defaults
// (4 CPU / 1024 MiB) OOM-kill modern JS builds — Nuxt + Vite +
// Rollup on a moderate project, with claude-code and a dozen plugin
// MCP servers running alongside, peaks past 4 GiB during chunk
// generation. 16 GiB leaves the workspace generous headroom on a
// typical Apple Silicon dev machine (16-32 GiB total), and the
// host's container-runtime-linux process only physically allocates
// as the guest dirties pages — so idle sandboxes don't actually
// consume the cap.
//
// CPU stays at 4: builds are I/O-bound past that, and the host
// scheduler can multiplex 4 vCPUs against its own cores fine.
const (
	defaultCPUs      = 4
	defaultMemoryMiB = 16384
)

// Run starts a sandbox in detached mode. Returns when the CLI exits, which
// happens after the container is started but is not guaranteed to coincide
// with the container being ready to accept exec.
func (a *Adapter) Run(ctx context.Context, spec substrate.RunSpec) error {
	// Materialize substrate-managed volumes before the run so the CLI
	// can attach them. Idempotent: pre-existing volumes are reused
	// (Apple Container errors on duplicate create — we swallow that).
	for _, v := range spec.Volumes {
		if err := a.ensureVolume(ctx, v); err != nil {
			return fmt.Errorf("ensure volume %q: %w", v.Name, err)
		}
	}
	args := []string{"run", "-d", "--name", spec.Name}
	cpus := spec.CPUs
	if cpus == 0 {
		cpus = defaultCPUs
	}
	memMiB := spec.MemoryMiB
	if memMiB == 0 {
		memMiB = defaultMemoryMiB
	}
	args = append(args, "--cpus", fmt.Sprintf("%d", cpus))
	args = append(args, "--memory", fmt.Sprintf("%dMiB", memMiB))
	// CAP_NET_ADMIN: required for the entrypoint's PREROUTING DNAT
	// rule that NATs external-IP traffic onto loopback so dev servers
	// bound to 127.0.0.1 (vite, next dev, …) are reachable from the
	// host browser without project-side --host=0.0.0.0 changes.
	// Apple Container strips this capability by default even for root
	// inside the microVM.
	args = append(args, "--cap-add", "NET_ADMIN")
	for k, v := range spec.Env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}
	for _, m := range spec.Mounts {
		mount := fmt.Sprintf("%s:%s", m.HostPath, m.ContainerPath)
		if m.ReadOnly {
			mount += ":ro"
		}
		args = append(args, "-v", mount)
	}
	for _, v := range spec.Volumes {
		mount := fmt.Sprintf("%s:%s", v.Name, v.ContainerPath)
		if v.ReadOnly {
			mount += ":ro"
		}
		args = append(args, "-v", mount)
	}
	for _, t := range spec.TmpfsMounts {
		// Apple Container's --tmpfs syntax: --tmpfs <path>[:<options>].
		// We only set size= today; defaults (mode=1777, etc.) match what
		// every project needs. If size is unset, omit it and let the
		// adapter pick its default.
		spec := t.ContainerPath
		if t.SizeMiB > 0 {
			spec = fmt.Sprintf("%s:size=%dm", t.ContainerPath, t.SizeMiB)
		}
		args = append(args, "--tmpfs", spec)
	}
	for _, p := range spec.PublishPort {
		args = append(args, "--publish",
			fmt.Sprintf("%d:%d", p.HostPort, p.ContainerPort))
	}
	// DNS: only honor an explicit spec.DNS list. The cspace sandbox
	// image runs dnsmasq at 127.0.0.1:53 (configured by the
	// entrypoint) which forwards *.cspace.test to the daemon on
	// the gateway and everything else to public resolvers — so the
	// container picks up name resolution on its own without the
	// substrate adapter needing to inject anything. Apple Container
	// only writes the --dns values into /etc/resolv.conf at boot;
	// dnsmasq overwrites that file moments later with `nameserver
	// 127.0.0.1`, so any --dns we pass here gets discarded anyway.
	for _, ns := range spec.DNS {
		args = append(args, "--dns", ns)
	}
	args = append(args, spec.Image)
	args = append(args, spec.Command...)

	cmd := exec.CommandContext(ctx, "container", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("container run %s: %w (stderr: %s)",
			spec.Name, err, stderr.String())
	}
	// Init substrate-managed volumes that need a non-root owner. mkfs.ext4
	// hands us a root-owned mount root and a `lost+found` directory at
	// mode 700 — both trip non-root tools (pnpm walks lost+found and
	// hits EACCES). Idempotent: chown is a no-op on a warm volume,
	// `rm -rf lost+found` is a no-op when it's already gone.
	for _, v := range spec.Volumes {
		if v.OwnerUID == 0 {
			continue
		}
		init := fmt.Sprintf("chown %d:%d %q && rm -rf %q/lost+found",
			v.OwnerUID, v.OwnerUID, v.ContainerPath, v.ContainerPath)
		if _, err := a.Exec(ctx, spec.Name,
			[]string{"sh", "-c", init},
			substrate.ExecOpts{User: "0"}); err != nil {
			_ = a.Stop(context.Background(), spec.Name)
			return fmt.Errorf("init volume %s: %w", v.ContainerPath, err)
		}
	}
	return nil
}

// Exec runs cmdLine inside the named sandbox. A non-zero exit status from
// the command itself is returned via ExecResult.ExitCode with a nil error;
// only transport-level failures (CLI missing, context canceled, etc.)
// produce an error.
func (a *Adapter) Exec(ctx context.Context, name string, cmdLine []string, opts substrate.ExecOpts) (substrate.ExecResult, error) {
	args := []string{"exec"}
	for k, v := range opts.Env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}
	if opts.WorkDir != "" {
		args = append(args, "-w", opts.WorkDir)
	}
	if opts.User != "" {
		args = append(args, "--user", opts.User)
	}
	args = append(args, name)
	args = append(args, cmdLine...)

	cmd := exec.CommandContext(ctx, "container", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exit := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exit = exitErr.ExitCode()
		err = nil
	}
	return substrate.ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exit,
	}, err
}

// Stop terminates and removes the named sandbox. Idempotent: removing a
// container that does not exist is a no-op.
//
// Implementation note: an earlier version ran `container stop` then
// `container rm` in sequence and ignored both errors, but the two
// commands raced — rm sometimes hit a still-running container, failed
// silently, and left it around to break the next cspace up with
// "already exists" (cs-finding:2026-05-03-cspace-down-race-stop-rm-
// sequence-leaves-container-behind-bl). `container rm --force` issues
// a stop-and-remove atomically, which is what we actually want.
func (a *Adapter) Stop(ctx context.Context, name string) error {
	cmd := exec.CommandContext(ctx, "container", "rm", "--force", name)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// "Container not found" is the expected idempotent path —
		// don't propagate that as an error to the caller.
		if strings.Contains(stderr.String(), "notFound") {
			return nil
		}
		return fmt.Errorf("container rm --force %s: %w (stderr: %s)",
			name, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// ensureVolume creates an Apple Container volume if it doesn't exist,
// formatted as ext4 inside a disk image. Idempotent: pre-existing
// volumes are reused as-is — we don't resize them on subsequent runs
// (treat the first create as authoritative). Cspace's per-sandbox
// volume naming makes accidental name collisions impossible.
func (a *Adapter) ensureVolume(ctx context.Context, v substrate.NamedVolume) error {
	args := []string{"volume", "create"}
	if v.SizeBytes > 0 {
		args = append(args, "-s", strconv.FormatInt(v.SizeBytes, 10))
	}
	args = append(args, v.Name)
	cmd := exec.CommandContext(ctx, "container", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(stderr.String(), "already exists") {
			return nil
		}
		return fmt.Errorf("container volume create %s: %w (stderr: %s)",
			v.Name, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// ListVolumes returns the names of substrate-managed volumes whose
// names start with prefix. Used by cspace down to find every volume
// owned by a sandbox (naming convention:
// cspace-<project>-<sandbox>-<compose-volume>) without keeping a
// separate registry of allocations.
func (a *Adapter) ListVolumes(ctx context.Context, prefix string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "container", "volume", "list", "-q")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("container volume list: %w (stderr: %s)",
			err, strings.TrimSpace(stderr.String()))
	}
	var matches []string
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, prefix) {
			matches = append(matches, line)
		}
	}
	return matches, nil
}

// RemoveVolume deletes a substrate-managed volume. Idempotent (missing
// volumes return nil). Used by cspace down to reclaim per-sandbox
// node_modules / build-artifact volumes — the workspace clone gets
// wiped, the volumes have to go too or they leak.
func (a *Adapter) RemoveVolume(ctx context.Context, name string) error {
	cmd := exec.CommandContext(ctx, "container", "volume", "rm", name)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(stderr.String(), "not found") ||
			strings.Contains(stderr.String(), "notFound") {
			return nil
		}
		return fmt.Errorf("container volume rm %s: %w (stderr: %s)",
			name, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// inspectRecord matches the JSON shape returned by `container inspect`. The
// CLI returns a single-element array; on 1.1.x the IPv4 address sits at
// status.networks[].ipv4Address as "<addr>/<cidr>" (e.g. "192.168.64.13/24").
type inspectRecord struct {
	Status struct {
		Networks []struct {
			IPv4Address string `json:"ipv4Address"`
			Network     string `json:"network"`
		} `json:"networks"`
	} `json:"status"`
}

// IP returns the container's IPv4 address (CIDR suffix stripped). Apple's
// `container inspect` does not support a --format flag, so we parse the
// JSON output. The address is assigned at run time and is not stable across
// runs of the same sandbox name; callers should snapshot it once at start.
func (a *Adapter) IP(ctx context.Context, name string) (string, error) {
	cmd := exec.CommandContext(ctx, "container", "inspect", name)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("container inspect %s: %w (stderr: %s)",
			name, err, stderr.String())
	}

	ip, err := ParseInspectIPv4(stdout.Bytes())
	if err != nil {
		return "", fmt.Errorf("parse `container inspect %s` output: %w "+
			"(the Apple Container CLI's JSON shape may have changed; "+
			"cspace tested with %s.x — run `container --version` and file "+
			"an issue at https://github.com/elliottregan/cspace/issues if "+
			"this version differs)", name, err, supportedMinorVersion)
	}
	if ip == "" {
		return "", fmt.Errorf("container %s has no IPv4 address", name)
	}
	return ip, nil
}

// stripCIDR turns "192.168.64.13/24" into "192.168.64.13"; a bare address is
// returned unchanged.
func stripCIDR(addr string) string {
	if i := strings.IndexByte(addr, '/'); i >= 0 {
		return addr[:i]
	}
	return addr
}

// ParseInspectIPv4 extracts the first non-empty IPv4 address (CIDR suffix
// stripped) from `container inspect` JSON output. Returns ("", nil) when the
// output has no records or no IPv4 address (a container not up yet, or gone);
// only malformed JSON yields a non-nil error. Shared by Adapter.IP and the
// daemon's sidecar DNS resolver so the inspect shape lives in one place.
func ParseInspectIPv4(out []byte) (string, error) {
	var records []inspectRecord
	if err := json.Unmarshal(out, &records); err != nil {
		return "", err
	}
	if len(records) == 0 {
		return "", nil
	}
	for _, n := range records[0].Status.Networks {
		if n.IPv4Address != "" {
			return stripCIDR(n.IPv4Address), nil
		}
	}
	return "", nil
}

// networkRecord matches `container network inspect <name>` output. On 1.1.x the
// gateway/subnet sit under status (e.g. status.ipv4Gateway "192.168.65.1").
type networkRecord struct {
	Status struct {
		IPv4Gateway string `json:"ipv4Gateway"`
		IPv4Subnet  string `json:"ipv4Subnet"`
	} `json:"status"`
}

// ParseNetworkGateway extracts the IPv4 gateway of the first network from
// `container network inspect` JSON. Returns ("", nil) when the output has no
// records or no gateway; only malformed JSON yields a non-nil error.
func ParseNetworkGateway(out []byte) (string, error) {
	var records []networkRecord
	if err := json.Unmarshal(out, &records); err != nil {
		return "", err
	}
	if len(records) == 0 {
		return "", nil
	}
	return records[0].Status.IPv4Gateway, nil
}

// NetworkGateway returns the IPv4 gateway of the named container network — the
// vmnet gateway the host serves cspace DNS/registry on, which sandboxes reach
// as their default route. Apple Container moved this from 192.168.64.1 to
// 192.168.65.1 at the 1.0 boundary, so it must be discovered, not hardcoded.
// An empty network name defaults to "default".
func (a *Adapter) NetworkGateway(ctx context.Context, network string) (string, error) {
	if network == "" {
		network = "default"
	}
	cmd := exec.CommandContext(ctx, "container", "network", "inspect", network)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("container network inspect %s: %w (stderr: %s)",
			network, err, strings.TrimSpace(stderr.String()))
	}
	gw, err := ParseNetworkGateway(stdout.Bytes())
	if err != nil {
		return "", fmt.Errorf("parse `container network inspect %s` output: %w "+
			"(cspace tested with %s.x)", network, err, supportedMinorVersion)
	}
	if gw == "" {
		return "", fmt.Errorf("container network %s has no IPv4 gateway", network)
	}
	return gw, nil
}

// ContainerSummary is one row of `container ls --all --format json`, narrowed
// to the fields the TUI renders. See adapter.go's package doc for the version
// cspace tests against; a shape change gives the same drift error as IP().
type ContainerSummary struct {
	Name    string    // configuration.id
	Image   string    // configuration.image.reference
	State   string    // status.state: "running" | "stopped" | ...
	IP      string    // status.networks[0].ipv4Address, CIDR suffix stripped, "" if none
	CPUs    int       // configuration.resources.cpus
	MemoryB int64     // configuration.resources.memoryInBytes
	Started time.Time // status.startedDate (RFC3339) parsed to wall time; zero if absent
}

// listRecord mirrors the `container ls --all --format json` shape. On 1.1.x
// the runtime state (state word, startedDate, networks) is nested under a
// `status` object; only `configuration` stays flat. See IP()/ParseInspectIPv4
// for the matching inspect shape.
type listRecord struct {
	Configuration struct {
		ID    string `json:"id"`
		Image struct {
			Reference string `json:"reference"`
		} `json:"image"`
		Resources struct {
			CPUs        int   `json:"cpus"`
			MemoryBytes int64 `json:"memoryInBytes"`
		} `json:"resources"`
	} `json:"configuration"`
	Status struct {
		State       string `json:"state"`
		StartedDate string `json:"startedDate"` // RFC3339, e.g. "2026-07-21T02:32:47Z"
		Networks    []struct {
			IPv4Address string `json:"ipv4Address"`
		} `json:"networks"`
	} `json:"status"`
}

// parseContainerList turns `container ls --format json` output into summaries.
// Split out from List so it can be unit-tested with canned JSON, no CLI.
func parseContainerList(jsonOutput string) ([]ContainerSummary, error) {
	var records []listRecord
	if err := json.Unmarshal([]byte(jsonOutput), &records); err != nil {
		return nil, fmt.Errorf("parse `container ls --all --format json` output: %w "+
			"(the Apple Container CLI's JSON shape may have changed; cspace tested "+
			"with %s.x — run `container --version` and file an issue at "+
			"https://github.com/elliottregan/cspace/issues if this version differs)",
			err, supportedMinorVersion)
	}
	out := make([]ContainerSummary, 0, len(records))
	for _, r := range records {
		ip := ""
		if len(r.Status.Networks) > 0 {
			ip = stripCIDR(r.Status.Networks[0].IPv4Address)
		}
		// A missing or unparseable startedDate (e.g. a stopped container)
		// leaves Started as the zero time rather than failing the whole list.
		var started time.Time
		if s := r.Status.StartedDate; s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				started = t
			}
		}
		out = append(out, ContainerSummary{
			Name:    r.Configuration.ID,
			Image:   r.Configuration.Image.Reference,
			State:   r.Status.State,
			IP:      ip,
			CPUs:    r.Configuration.Resources.CPUs,
			MemoryB: r.Configuration.Resources.MemoryBytes,
			Started: started,
		})
	}
	return out, nil
}

// List returns every container the CLI reports (all states). The caller filters
// to cspace-* / buildkit. Mirrors IP()'s shell-out + JSON-parse pattern.
func (a *Adapter) List(ctx context.Context) ([]ContainerSummary, error) {
	cmd := exec.CommandContext(ctx, "container", "ls", "--all", "--format", "json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("container ls --all --format json: %w (stderr: %s)",
			err, strings.TrimSpace(stderr.String()))
	}
	return parseContainerList(stdout.String())
}

// randSuffix returns a short hex suffix for unique test names.
func randSuffix() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
