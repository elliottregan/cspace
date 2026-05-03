// Package applecontainer implements substrate.Substrate against Apple's
// `container` CLI (github.com/apple/container).
//
// VERSION COUPLING: tested against 0.12.x. The CLI is pre-1.0 with active
// development; expect occasional breakage. Known quirks:
//
//   - `container inspect` does NOT support a --format flag. We parse JSON.
//   - `container inspect` of a missing container exits 0 with body "[]"
//     (NOT non-zero) on 0.12.x — the registry-prune `containerExists`
//     helper handles this defensively.
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
	"strings"

	"github.com/elliottregan/cspace/internal/substrate"
)

// supportedMinorVersion is the Apple Container CLI MAJOR.MINOR version cspace
// has been tested against. Versions outside this range trigger a warning
// (non-fatal) at cspace up time. Bumping this is a deliberate act: verify
// the JSON shape of `container inspect` and the other quirks listed in the
// package doc still hold.
const supportedMinorVersion = "0.12"

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
	text := strings.ToLower(string(out))
	// `container system status` outputs vary, but contain the word "running"
	// when the apiserver is up. Check for that.
	if !strings.Contains(text, "running") {
		return fmt.Errorf("apple container apiserver not running: %s",
			strings.TrimSpace(string(out)))
	}
	return nil
}

// Run starts a sandbox in detached mode. Returns when the CLI exits, which
// happens after the container is started but is not guaranteed to coincide
// with the container being ready to accept exec.
func (a *Adapter) Run(ctx context.Context, spec substrate.RunSpec) error {
	args := []string{"run", "-d", "--name", spec.Name}
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
	for _, p := range spec.PublishPort {
		args = append(args, "--publish",
			fmt.Sprintf("%d:%d", p.HostPort, p.ContainerPort))
	}
	// DNS injection: Apple Container's default vmnet gateway (192.168.64.1)
	// doesn't answer port 53, so containers can't resolve hostnames out of
	// the box. We inject explicit resolvers via --dns. Default to public
	// resolvers when the caller hasn't specified any. See finding
	// 2026-05-01-apple-container-default-dns-is-broken-...
	dns := spec.DNS
	if len(dns) == 0 {
		dns = []string{"1.1.1.1", "8.8.8.8"}
	}
	for _, ns := range dns {
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

// Stop terminates and removes the named sandbox. Idempotent: stopping or
// removing a container that does not exist is a no-op.
func (a *Adapter) Stop(ctx context.Context, name string) error {
	_ = exec.CommandContext(ctx, "container", "stop", name).Run()
	_ = exec.CommandContext(ctx, "container", "rm", name).Run()
	return nil
}

// inspectRecord matches the JSON shape returned by `container inspect`.
// The CLI returns a single-element array; the IPv4 address sits at
// networks[].ipv4Address as "<addr>/<cidr>" (e.g. "192.168.64.13/24").
type inspectRecord struct {
	Networks []struct {
		IPv4Address string `json:"ipv4Address"`
		Network     string `json:"network"`
	} `json:"networks"`
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

	var records []inspectRecord
	if err := json.Unmarshal(stdout.Bytes(), &records); err != nil {
		return "", fmt.Errorf("parse `container inspect %s` output: %w "+
			"(the Apple Container CLI's JSON shape may have changed; "+
			"cspace tested with %s.x — run `container --version` and file "+
			"an issue at https://github.com/elliottregan/cspace/issues if "+
			"this version differs)", name, err, supportedMinorVersion)
	}
	if len(records) == 0 {
		return "", fmt.Errorf("container inspect %s: no records returned", name)
	}
	for _, n := range records[0].Networks {
		if n.IPv4Address == "" {
			continue
		}
		// Strip CIDR suffix: "192.168.64.13/24" -> "192.168.64.13".
		if i := strings.IndexByte(n.IPv4Address, '/'); i >= 0 {
			return n.IPv4Address[:i], nil
		}
		return n.IPv4Address, nil
	}
	return "", fmt.Errorf("container %s has no IPv4 address", name)
}

// randSuffix returns a short hex suffix for unique test names.
func randSuffix() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
