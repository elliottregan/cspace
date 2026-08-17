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

	"github.com/elliottregan/cspace/internal/credentials"
	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
	"github.com/miekg/dns"
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
			Details: []string{"daemon auto-starts via `cspace up`; or run `cspace daemon serve` manually"},
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

	// DNS UDP — loopback listener, then the container-facing gateway
	// listener and an actual in-container resolution, all via the shared
	// probeDnsAt helper.
	r.Checks = append(r.Checks, probeDnsAt("127.0.0.1:"+dnsLocalPort, "DNS", false /*failIsWarn*/))
	r.Checks = append(r.Checks, probeGatewayDNS())
	r.Checks = append(r.Checks, probeInContainerDNS())
	return r
}

// probeDnsAnswering issues a synthetic UDP DNS query at addr and reports
// whether anything answered. NXDOMAIN/NOERROR both count as "answering" —
// this proves the listener is bound and alive, not that the record itself
// resolves correctly.
func probeDnsAnswering(addr string, timeout time.Duration) bool {
	msg := new(dns.Msg)
	msg.SetQuestion("status-probe.cspace.test.", dns.TypeA)
	c := &dns.Client{Net: "udp", Timeout: timeout}
	resp, _, err := c.Exchange(msg, addr)
	return err == nil && resp != nil
}

// probeDnsAt checks a single UDP DNS listener and reports a ProbeCheck.
//
// When failIsWarn is true, any failure to get an answer degrades to
// ProbeWarn rather than ProbeFail — used for the container-facing gateway
// listener (192.168.64.1:5354), which simply doesn't exist until a sandbox
// has booted and claimed a vmnet gateway IP; a hard Fail there would fail
// `doctor` in CI where no vmnet bridge exists at all.
//
// When failIsWarn is false (the always-on loopback daemon), a failure is
// further split: if the UDP port itself refuses a connection, that's a
// Fail; if the port accepts a connection but the synthetic query goes
// unanswered, that's a Warn (something is listening but not the cspace
// daemon).
func probeDnsAt(addr, label string, failIsWarn bool) ProbeCheck {
	if probeDnsAnswering(addr, 1*time.Second) {
		return ProbeCheck{
			Status: ProbePass,
			Title:  label + " responding on " + addr + "/udp",
		}
	}
	if failIsWarn {
		return ProbeCheck{
			Status:  ProbeWarn,
			Title:   label + " not responding on " + addr + "/udp",
			Details: []string{"expected until a sandbox has booted and claimed a vmnet gateway IP"},
		}
	}
	// Differentiate "port closed" vs "port open but not answering".
	if c, err := net.DialTimeout("udp", addr, 500*time.Millisecond); err == nil {
		_ = c.Close()
		return ProbeCheck{
			Status:  ProbeWarn,
			Title:   label + " port open but not answering queries at " + addr,
			Details: []string{"another process may hold that UDP port; check `lsof -nP -iUDP:" + dnsLocalPort + "`"},
		}
	}
	return ProbeCheck{
		Status: ProbeFail,
		Title:  label + " not responding on " + addr + "/udp",
	}
}

// probeGatewayDNS checks the container-facing DNS listener at the vmnet
// gateway that sandboxes query from inside their devcontainer. The daemon
// binds the handler on 0.0.0.0:5354, but the gateway IP itself (derived from
// `container network inspect default`, which Apple moved 192.168.64.1 ->
// 192.168.65.1 at 1.0) only exists once the vmnet bridge is up — i.e. after at
// least one container has booted — so a failure here always degrades to Warn.
func probeGatewayDNS() ProbeCheck {
	gw := resolveHostGateway(context.Background())
	return probeDnsAt(gw+":"+dnsLocalPort, "container-facing DNS (gateway)", true /*failIsWarn*/)
}

// probeInContainerDNS fulfils the "in-container resolution" half of the
// gateway+in-container promise: it picks one alive sandbox from the
// registry and resolves its own friendly hostname from inside its
// devcontainer via a single `getent hosts` exec. Doctor probes must stay
// fast, so — unlike verifyInContainerResolution's 3x2s boot-time retry loop
// — this makes exactly one attempt.
func probeInContainerDNS() ProbeCheck {
	path, err := registry.DefaultPath()
	if err != nil {
		return ProbeCheck{
			Status:  ProbeWarn,
			Title:   "in-container DNS: registry path unavailable",
			Details: []string{err.Error()},
		}
	}
	reg := &registry.Registry{Path: path}
	entries, err := reg.List()
	if err != nil {
		return ProbeCheck{
			Status:  ProbeWarn,
			Title:   "in-container DNS: registry read failed",
			Details: []string{err.Error()},
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	entry, ok := firstAliveEntry(ctx, entries)
	if !ok {
		return ProbeCheck{
			Status: ProbePass,
			Title:  "in-container DNS (no sandbox to test)",
		}
	}

	a := applecontainer.New()
	exec := func(execCtx context.Context, container string, argv ...string) ([]byte, error) {
		res, execErr := a.Exec(execCtx, container, argv, substrate.ExecOpts{})
		return []byte(res.Stdout), execErr
	}
	return checkInContainerDNSOnce(ctx, exec, containerNameForEntry(entry), workspaceFriendlyHost(entry.Name, entry.Project))
}

// firstAliveEntry returns the first registry entry that has an assigned IP,
// a live container, and isn't still mid-boot. Mirrors the aliveness check
// ProbeSandboxes uses.
func firstAliveEntry(ctx context.Context, entries []registry.Entry) (registry.Entry, bool) {
	for _, e := range entries {
		if e.IP == "" {
			continue
		}
		if e.State == "starting" {
			continue
		}
		if !containerExists(ctx, containerNameForEntry(e)) {
			continue
		}
		return e, true
	}
	return registry.Entry{}, false
}

// checkInContainerDNSOnce runs a single `getent hosts` exec inside container
// and maps the outcome to a ProbeCheck: resolves -> Pass; doesn't resolve or
// exec error -> Warn (a dead daemon here is advisory, not a doctor failure).
func checkInContainerDNSOnce(ctx context.Context, exec containerExecFn, container, host string) ProbeCheck {
	out, err := exec(ctx, container, "getent", "hosts", host)
	if err != nil {
		return ProbeCheck{
			Status:  ProbeWarn,
			Title:   "in-container DNS: " + host + " did not resolve inside " + container,
			Details: []string{err.Error()},
		}
	}
	if strings.TrimSpace(string(out)) == "" {
		return ProbeCheck{
			Status: ProbeWarn,
			Title:  "in-container DNS: " + host + " did not resolve inside " + container,
		}
	}
	return ProbeCheck{
		Status: ProbePass,
		Title:  "in-container DNS: " + host + " resolves inside " + container,
	}
}

// ProbeDns reports the macOS resolver state for *.<dnsDomain>. On non-darwin
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

	// Check 2: scutil reports a routing for the dns domain.
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
			Details: []string{"start a sandbox with `cspace up` to spawn the daemon"},
		})
	}
	return r
}

// scutilHasCspaceRouting parses `scutil --dns` for a per-domain routing
// entry covering dnsDomain. Returns false on any error.
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
	r.Checks = append(r.Checks, credentialProbeChecks([]string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"})...)
	return r
}

// ProbeGitHubCredentials reports the source for each GitHub credential alias.
func ProbeGitHubCredentials(ctx context.Context) ProbeResult {
	r := ProbeResult{Subsystem: "GitHub credentials"}
	r.Checks = append(r.Checks, credentialProbeChecks([]string{"GH_TOKEN", "GITHUB_TOKEN", "GITHUB_PERSONAL_ACCESS_TOKEN"})...)
	return r
}

// credentialProbeChecks reports both sides of the credential story: what
// the host resolves now, and what each running sandbox actually carries.
//
// Reporting only the first is what let doctor show a green check while a
// container held a dead token from an entirely different source. Credentials
// are baked in at create time and never refreshed, so host resolution
// describes what a NEW sandbox would get — never what a running one has.
func credentialProbeChecks(keys []string) []ProbeCheck {
	host := credentials.ProductionHost()
	project := projectName()
	resolved, err := host.ResolveErr(project, nil)
	if err != nil {
		return []ProbeCheck{{
			Status:  ProbeFail,
			Title:   "credential resolution failed",
			Details: []string{err.Error()},
		}}
	}
	selection := host.Select(resolved)

	// Group-level satisfaction, not per-key. The Anthropic carriers are
	// mutually exclusive, so exactly one of them never ships by design —
	// failing the other would leave doctor permanently red on every
	// correctly-configured host.
	groupSatisfied := false
	for _, key := range keys {
		if _, ok := selection.Winners[key]; ok {
			groupSatisfied = true
			break
		}
	}

	var checks []ProbeCheck
	for _, key := range keys {
		winner, shipping := selection.Winners[key]
		if !shipping {
			switch {
			case groupSatisfied:
				checks = append(checks, ProbeCheck{
					Status:  ProbePass,
					Title:   key + ": not shipped",
					Details: []string{"another carrier in this group ships instead"},
				})
			default:
				checks = append(checks, ProbeCheck{
					Status: ProbeFail,
					Title:  key + ": not reachable",
					Details: []string{"no credential available; run `cspace keychain init` or set " +
						key + " in .cspace/secrets.env"},
				})
			}
			continue
		}
		check := ProbeCheck{Title: key + ": " + winner.Source.String()}
		switch {
		case selection.Validities[key] == credentials.ValidityRejected:
			check.Status = ProbeFail
			check.Details = []string{"the provider rejected this credential (401)"}
		case !winner.ExpiresAt.IsZero():
			check.Status = ProbeWarn
			check.Details = []string{fmt.Sprintf(
				"expires %s; refreshes when host runs `claude`; sandbox may lose auth on long sessions",
				winner.ExpiresAt.Local().Format("2006-01-02 15:04 MST"))}
		default:
			check.Status = ProbePass
		}
		checks = append(checks, check)
	}

	return checks
}

// ProbeSandboxCredentials reports what each running sandbox actually carries,
// as its own subsystem so it appears once rather than under both credential
// families.
func ProbeSandboxCredentials(ctx context.Context) ProbeResult {
	r := ProbeResult{Subsystem: "Sandbox credentials"}
	host := credentials.ProductionHost()
	project := projectName()
	resolved, err := host.ResolveErr(project, nil)
	if err != nil {
		r.Checks = append(r.Checks, ProbeCheck{
			Status:  ProbeFail,
			Title:   "credential resolution failed",
			Details: []string{err.Error()},
		})
		return r
	}
	r.Checks = append(r.Checks, credentialContainerChecks(ctx, project, host.Select(resolved))...)
	if len(r.Checks) == 0 {
		r.Checks = append(r.Checks, ProbeCheck{
			Status: ProbePass,
			Title:  "no running sandboxes for this project",
		})
	}
	return r
}

// credentialContainerChecks inspects each registry-tracked sandbox for the
// project and compares its baked credentials against what the host resolves.
//
// Only workspace sandboxes are enumerated. Compose sidecars and the shared
// browser sidecar legitimately carry different environments, so including
// them would report every one as DIVERGED.
func credentialContainerChecks(ctx context.Context, project string, sel credentials.Selection) []ProbeCheck {
	path, err := registry.DefaultPath()
	if err != nil {
		return nil
	}
	entries, err := (&registry.Registry{Path: path}).List()
	if err != nil {
		return []ProbeCheck{{
			Status:  ProbeWarn,
			Title:   "sandbox credentials: unavailable",
			Details: []string{"could not read the sandbox registry: " + err.Error()},
		}}
	}

	a := applecontainer.New()
	if !a.Available() {
		return []ProbeCheck{{
			Status:  ProbeWarn,
			Title:   "sandbox credentials: unavailable",
			Details: []string{"apple `container` CLI not on PATH; host resolution reported above"},
		}}
	}

	var checks []ProbeCheck
	for _, e := range entries {
		if e.Project != project || e.Name == "" {
			continue
		}
		container := fmt.Sprintf("cspace-%s-%s", e.Project, e.Name)
		raw, err := a.InspectRaw(ctx, container)
		if err != nil {
			continue // not running; nothing baked to compare
		}
		baked, err := credentials.ParseBakedEnv(raw)
		if err != nil {
			checks = append(checks, ProbeCheck{
				Status:  ProbeWarn,
				Title:   e.Name + ": baked env unreadable",
				Details: []string{err.Error()},
			})
			continue
		}
		diverged := credentials.Diverged(baked, sel.Winners)
		if len(diverged) == 0 {
			checks = append(checks, ProbeCheck{
				Status: ProbePass,
				Title:  e.Name + ": baked credentials match host resolution",
			})
			continue
		}
		// A short-lived auto-discovered token rotates by design, so a
		// container baked before the last host refresh diverges routinely
		// and may still be perfectly usable. Report it, but do not paint
		// doctor red for the default zero-config setup.
		status := ProbeFail
		rotation := true
		for _, k := range diverged {
			if sel.Winners[k].Source != credentials.SourceAutoDiscovered ||
				sel.Winners[k].ExpiresAt.IsZero() {
				rotation = false
				break
			}
		}
		if rotation {
			status = ProbeWarn
		}
		checks = append(checks, ProbeCheck{
			Status: status,
			Title:  e.Name + ": DIVERGED from host resolution",
			Details: []string{
				"stale keys: " + strings.Join(diverged, ", "),
				"baked env is immutable for a container's life; " +
					"`cspace down " + e.Name + " && cspace up " + e.Name + "` to re-bake",
			},
		})
	}
	return checks
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
