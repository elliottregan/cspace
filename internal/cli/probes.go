package cli

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
)

// ProbeStatus is the per-check verdict.
type ProbeStatus int

const (
	ProbePass ProbeStatus = iota
	ProbeWarn
	ProbeFail
)

// ProbeCheck is one line in a doctor report.
type ProbeCheck struct {
	Status  ProbeStatus
	Title   string
	Details []string
}

// ProbeResult groups checks by subsystem.
type ProbeResult struct {
	Subsystem string
	Checks    []ProbeCheck
}

// ProbeAppleContainer reports whether the Apple Container CLI is installed
// and whether the apiserver is running. On non-darwin hosts the result is a
// single "not applicable" pass — Linux uses a different substrate.
func ProbeAppleContainer(ctx context.Context) ProbeResult {
	r := ProbeResult{Subsystem: "Apple Container CLI"}
	if runtime.GOOS != "darwin" {
		r.Checks = append(r.Checks, ProbeCheck{
			Status: ProbePass,
			Title:  "not applicable on " + runtime.GOOS,
		})
		return r
	}
	a := applecontainer.New()
	if !a.Available() {
		r.Checks = append(r.Checks, ProbeCheck{
			Status:  ProbeFail,
			Title:   "container CLI not on PATH",
			Details: []string{"install Apple's `container` (https://github.com/apple/container)"},
		})
		return r
	}
	// Version probe: report installed + supported.
	rawVersion, supported, err := a.VersionStatus(ctx)
	switch {
	case err != nil:
		r.Checks = append(r.Checks, ProbeCheck{
			Status:  ProbeWarn,
			Title:   "container --version failed",
			Details: []string{err.Error()},
		})
	case supported:
		r.Checks = append(r.Checks, ProbeCheck{
			Status: ProbePass,
			Title:  fmt.Sprintf("%s installed (supported: %s.x)", trimVersion(rawVersion), applecontainer.SupportedMinorVersion()),
		})
	default:
		r.Checks = append(r.Checks, ProbeCheck{
			Status: ProbeWarn,
			Title:  fmt.Sprintf("%s installed (untested; cspace tested with %s.x)", trimVersion(rawVersion), applecontainer.SupportedMinorVersion()),
		})
	}
	// Apiserver health.
	if err := a.HealthCheck(ctx); err != nil {
		r.Checks = append(r.Checks, ProbeCheck{
			Status:  ProbeFail,
			Title:   "apiserver not running",
			Details: []string{"run `container system start`"},
		})
	} else {
		r.Checks = append(r.Checks, ProbeCheck{
			Status: ProbePass,
			Title:  "apiserver running",
		})
	}
	return r
}

// trimVersion returns the version string truncated to the leading "container
// X.Y.Z" prefix when present, falling back to the raw input. Apple's CLI
// emits "container CLI version 0.12.3 (build: release, commit: ...)"; the
// suffix is noisy in a one-liner.
func trimVersion(raw string) string {
	if m := versionWithPrefixRE.FindStringSubmatch(raw); len(m) > 0 {
		return "container " + m[1]
	}
	return raw
}

// versionWithPrefixRE captures the X.Y.Z portion of any Apple Container
// --version output.
var versionWithPrefixRE = regexp.MustCompile(`(\d+\.\d+\.\d+)`)

// ProbeDaemon reports whether the cspace daemon is responding on its HTTP
// port. The DNS check is in ProbeDns.
func ProbeDaemon(ctx context.Context) ProbeResult {
	r := ProbeResult{Subsystem: "cspace daemon (registry HTTP + DNS)"}

	// HTTP /health.
	httpURL := "http://127.0.0.1:" + daemonHTTPPort + "/health"
	httpClient := &http.Client{Timeout: 1 * time.Second}
	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, httpURL, nil)
	httpResp, httpErr := httpClient.Do(httpReq)
	switch {
	case httpErr != nil:
		r.Checks = append(r.Checks, ProbeCheck{
			Status:  ProbeFail,
			Title:   "HTTP not responding on 127.0.0.1:" + daemonHTTPPort,
			Details: []string{"daemon auto-starts via `cspace cspace2-up`; or run `cspace daemon serve` manually"},
		})
	case httpResp.StatusCode != 200:
		_ = httpResp.Body.Close()
		r.Checks = append(r.Checks, ProbeCheck{
			Status: ProbeWarn,
			Title:  fmt.Sprintf("HTTP responded with status %d on 127.0.0.1:%s", httpResp.StatusCode, daemonHTTPPort),
		})
	default:
		_ = httpResp.Body.Close()
		r.Checks = append(r.Checks, ProbeCheck{
			Status: ProbePass,
			Title:  "HTTP responding on 127.0.0.1:" + daemonHTTPPort,
		})
	}

	// DNS UDP — share probeDnsDaemon from cmd_dns.go.
	if probeDnsDaemon(1 * time.Second) {
		r.Checks = append(r.Checks, ProbeCheck{
			Status: ProbePass,
			Title:  "DNS responding on 127.0.0.1:" + dnsLocalPort + "/udp",
		})
	} else {
		// Differentiate "port closed" vs "port open but not answering".
		if c, err := net.DialTimeout("udp", "127.0.0.1:"+dnsLocalPort, 500*time.Millisecond); err == nil {
			_ = c.Close()
			r.Checks = append(r.Checks, ProbeCheck{
				Status:  ProbeWarn,
				Title:   "DNS port " + dnsLocalPort + "/udp open but not answering queries",
				Details: []string{"another process may hold UDP/" + dnsLocalPort + "; check `lsof -nP -iUDP:" + dnsLocalPort + "`"},
			})
		} else {
			r.Checks = append(r.Checks, ProbeCheck{
				Status: ProbeFail,
				Title:  "DNS not responding on 127.0.0.1:" + dnsLocalPort + "/udp",
			})
		}
	}
	return r
}

// ProbeDns reports the macOS resolver state for *.cspace2.local. On non-darwin
// hosts this is "not applicable".
func ProbeDns(ctx context.Context) ProbeResult {
	r := ProbeResult{Subsystem: "DNS routing for *." + dnsDomain}
	if runtime.GOOS != "darwin" {
		r.Checks = append(r.Checks, ProbeCheck{
			Status: ProbePass,
			Title:  "not applicable on " + runtime.GOOS,
		})
		return r
	}

	// Check 1: resolver file present and matches expected content.
	if data, err := os.ReadFile(dnsResolverFile); err == nil {
		if strings.TrimSpace(string(data)) == strings.TrimSpace(dnsResolverBody) {
			r.Checks = append(r.Checks, ProbeCheck{
				Status: ProbePass,
				Title:  dnsResolverFile + " installed",
			})
		} else {
			r.Checks = append(r.Checks, ProbeCheck{
				Status:  ProbeWarn,
				Title:   dnsResolverFile + " present but content differs from expected",
				Details: []string{"run `cspace dns install` to overwrite"},
			})
		}
	} else {
		r.Checks = append(r.Checks, ProbeCheck{
			Status:  ProbeFail,
			Title:   dnsResolverFile + " not installed",
			Details: []string{"run `cspace dns install` (sudo prompt)"},
		})
	}

	// Check 2: scutil reports a routing for cspace2.local.
	if scutilHasCspaceRouting() {
		r.Checks = append(r.Checks, ProbeCheck{
			Status: ProbePass,
			Title:  "macOS resolver routes through 127.0.0.1:" + dnsLocalPort,
		})
	} else {
		r.Checks = append(r.Checks, ProbeCheck{
			Status:  ProbeFail,
			Title:   "macOS resolver has no routing for *." + dnsDomain,
			Details: []string{"likely the resolver file isn't installed; run `cspace dns install`"},
		})
	}

	// Check 3: end-to-end resolution working — synthetic query lands on the
	// daemon. Reuses probeDnsDaemon.
	if probeDnsDaemon(1 * time.Second) {
		r.Checks = append(r.Checks, ProbeCheck{
			Status: ProbePass,
			Title:  "end-to-end resolution working",
		})
	} else {
		r.Checks = append(r.Checks, ProbeCheck{
			Status:  ProbeWarn,
			Title:   "daemon DNS not answering — end-to-end resolution will fail",
			Details: []string{"start a sandbox with `cspace cspace2-up` to spawn the daemon"},
		})
	}
	return r
}

// scutilHasCspaceRouting parses `scutil --dns` for a per-domain routing
// entry covering cspace2.local. Returns false on any error.
func scutilHasCspaceRouting() bool {
	cmd := exec.Command("scutil", "--dns")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "domain") &&
			strings.Contains(trimmed, ":") &&
			strings.Contains(line, dnsDomain) {
			return true
		}
	}
	return false
}

// ProbeAnthropicCredentials reports the source for each Anthropic credential
// alias and warns when the auto-discovered Claude Code OAuth token is
// approaching expiry.
func ProbeAnthropicCredentials(ctx context.Context) ProbeResult {
	r := ProbeResult{Subsystem: "Anthropic credentials"}
	projectRoot, userHome := credentialRoots()
	for _, key := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"} {
		r.Checks = append(r.Checks, credentialProbeCheck(projectRoot, userHome, key))
	}
	return r
}

// ProbeGitHubCredentials reports the source for each GitHub credential alias.
func ProbeGitHubCredentials(ctx context.Context) ProbeResult {
	r := ProbeResult{Subsystem: "GitHub credentials"}
	projectRoot, userHome := credentialRoots()
	for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GITHUB_PERSONAL_ACCESS_TOKEN"} {
		r.Checks = append(r.Checks, credentialProbeCheck(projectRoot, userHome, key))
	}
	return r
}

// credentialRoots returns the project root and user home in the same way
// runKeychainStatus does.
func credentialRoots() (projectRoot, userHome string) {
	userHome, _ = os.UserHomeDir()
	if cfg != nil && cfg.ProjectRoot != "" {
		projectRoot = cfg.ProjectRoot
	}
	return projectRoot, userHome
}

// credentialProbeCheck wraps credentialSource into a ProbeCheck.
//
//   - "not reachable" => Fail
//   - auto-discovered Claude Code OAuth (with or without expiry hint) => Warn,
//     because OAuth expires and may need refresh
//   - other auto-discovered (gh) => Pass with a note
//   - explicit project / user file / Keychain => Pass
func credentialProbeCheck(projectRoot, userHome, key string) ProbeCheck {
	source, hint := credentialSource(projectRoot, userHome, key)
	check := ProbeCheck{
		Title: key + ": " + source,
	}
	switch {
	case source == "not reachable":
		check.Status = ProbeFail
		if hint != "" {
			check.Details = []string{hint}
		}
	case strings.HasPrefix(source, "auto-discovered (Claude Code OAuth)"):
		check.Status = ProbeWarn
		if hint != "" {
			check.Details = []string{hint}
		}
	default:
		check.Status = ProbePass
		// Discard the hint on pass — explicit-source users don't need a note.
	}
	return check
}

// ProbeSandboxes summarizes the sandbox registry: how many alive, how many
// stuck booting, how many dead-but-still-registered.
func ProbeSandboxes(ctx context.Context) ProbeResult {
	r := ProbeResult{Subsystem: "Sandboxes"}

	path, err := registry.DefaultPath()
	if err != nil {
		r.Checks = append(r.Checks, ProbeCheck{
			Status:  ProbeFail,
			Title:   "registry path unavailable",
			Details: []string{err.Error()},
		})
		return r
	}
	reg := &registry.Registry{Path: path}
	entries, err := reg.List()
	if err != nil {
		r.Checks = append(r.Checks, ProbeCheck{
			Status:  ProbeFail,
			Title:   "registry read failed",
			Details: []string{err.Error()},
		})
		return r
	}

	if len(entries) == 0 {
		r.Checks = append(r.Checks, ProbeCheck{
			Status: ProbePass,
			Title:  "0 alive",
		})
		return r
	}

	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var alive, stuckBooting, deadRegistered []string
	for _, e := range entries {
		name := e.Name
		if e.Project != "" {
			name = e.Project + ":" + e.Name
		}
		if !containerExists(probeCtx, containerNameForEntry(e)) {
			deadRegistered = append(deadRegistered, name)
			continue
		}
		if e.State == "starting" {
			stuckBooting = append(stuckBooting, name)
			continue
		}
		alive = append(alive, name)
	}

	// Alive count — always emit (even if 0).
	aliveCheck := ProbeCheck{
		Status: ProbePass,
		Title:  fmt.Sprintf("%d alive%s", len(alive), bracketed(alive)),
	}
	r.Checks = append(r.Checks, aliveCheck)

	// Stuck-booting — warn if any.
	if len(stuckBooting) > 0 {
		r.Checks = append(r.Checks, ProbeCheck{
			Status: ProbeWarn,
			Title:  fmt.Sprintf("%d stuck booting%s", len(stuckBooting), bracketed(stuckBooting)),
			Details: []string{
				"a boot may have crashed mid-flight; if these stay alive, run `cspace registry prune --dry-run`",
			},
		})
	} else {
		r.Checks = append(r.Checks, ProbeCheck{
			Status: ProbePass,
			Title:  "0 stuck booting",
		})
	}

	// Dead-registered (orphan registry entries pointing to nothing).
	if len(deadRegistered) > 0 {
		r.Checks = append(r.Checks, ProbeCheck{
			Status: ProbeWarn,
			Title:  fmt.Sprintf("%d dead registry entries%s", len(deadRegistered), bracketed(deadRegistered)),
			Details: []string{
				"run `cspace registry prune` to remove",
			},
		})
	}
	return r
}

// bracketed formats a name list as " (a, b, c)" or "" when empty.
func bracketed(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return " (" + strings.Join(names, ", ") + ")"
}
