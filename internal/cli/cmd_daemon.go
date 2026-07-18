package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/miekg/dns"
	"github.com/spf13/cobra"
)

const (
	// daemonHTTPPort is the loopback HTTP port the daemon binds for registry
	// lookups. cspace up injects http://192.168.64.1:<port> as
	// CSPACE_REGISTRY_URL into sandboxes so they can resolve siblings.
	daemonHTTPPort = "6280"

	// daemonDNSListenAddr: 127.0.0.1:5354 rather than the mDNS-conventional
	// :5353, because macOS mDNSResponder owns UDP/5353 wildcard and we can't
	// share it. 5354 is the well-known "alt-mdns" port and is unclaimed on
	// macOS. `cspace dns install` writes /etc/resolver/cspace.test with
	// `port 5354` to match.
	daemonDNSListenAddr = "127.0.0.1:5354"
	// daemonDNSGatewayAddr exposes the same DNS handler on the Apple
	// Container vmnet gateway IP so containers can resolve sibling
	// hostnames the same way the host does. Bind is best-effort —
	// failure (e.g. running outside Apple Container) logs a warning
	// and the host-only loopback path keeps working.
	daemonDNSGatewayAddr = "192.168.64.1:5354"
	daemonDNSDomain      = "cspace.test." // trailing dot is canonical
	daemonDNSTTL         = 5              // seconds; sandbox IPs change across restarts

	// daemonIdleDefault is the idle-shutdown threshold when
	// CSPACE_REGISTRY_DAEMON_IDLE is unset.
	daemonIdleDefault = 30 * time.Minute
)

// daemonDNSAddrs returns the DNS listen/gateway addresses, allowing tests to
// override the well-known defaults via env so they don't collide with a
// developer's real daemon.
func daemonDNSAddrs() (listen, gateway string) {
	listen = daemonDNSListenAddr
	if v := os.Getenv("CSPACE_DAEMON_DNS_ADDR"); v != "" {
		listen = v
	}
	gateway = daemonDNSGatewayAddr
	if v := os.Getenv("CSPACE_DAEMON_GATEWAY_ADDR"); v != "" {
		gateway = v
	}
	return
}

// daemonHTTPAddr returns the loopback HTTP address the daemon binds,
// allowing tests to override the well-known port via env (mirrors
// daemonDNSAddrs) so they don't collide with a developer's real daemon.
func daemonHTTPAddr() string {
	port := daemonHTTPPort
	if v := os.Getenv("CSPACE_REGISTRY_DAEMON_PORT"); v != "" {
		port = v
	}
	return "127.0.0.1:" + port
}

// daemonHealthVersion queries the daemon's /health endpoint and returns the
// version it reports. ok is false when the daemon isn't reachable, doesn't
// respond 200, or the body doesn't decode — callers treat that as "no
// version-matched daemon running" rather than an error.
func daemonHealthVersion(timeout time.Duration) (string, bool) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get("http://" + daemonHTTPAddr() + "/health")
	if err != nil {
		return "", false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", false
	}
	return body.Version, true
}

func newDaemonCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the cspace background daemon (registry HTTP + DNS)",
		Long: `cspace up auto-spawns the cspace daemon (HTTP registry lookup on
:6280, DNS for *.cspace.test on :5354). The daemon idle-exits after 30
minutes of no requests AND no active sandboxes. Most users never run
these subcommands; they're for debugging and manual cleanup.`,
	}
	parent.AddCommand(newDaemonServeCmd())
	parent.AddCommand(newDaemonStatusCmd())
	parent.AddCommand(newDaemonStopCmd())
	return parent
}

func newDaemonServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "serve",
		Short:  "Run the cspace daemon (auto-spawned by cspace up; rarely run by hand)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonServe()
		},
	}
}

// runDaemonServe is the daemon main loop. Behavior is identical to the old
// standalone cspace-registry-daemon binary: HTTP on :6280, DNS on
// 127.0.0.1:5354, idle-exit after 30m of no traffic AND no registry entries.
func runDaemonServe() error {
	port := os.Getenv("CSPACE_REGISTRY_DAEMON_PORT")
	if port == "" {
		port = daemonHTTPPort
	}
	bindAddr := os.Getenv("CSPACE_REGISTRY_DAEMON_ADDR")
	if bindAddr == "" {
		bindAddr = "0.0.0.0" // bind on all interfaces so sandboxes can reach via gateway IP
	}

	path, err := registry.DefaultPath()
	if err != nil {
		return fmt.Errorf("registry path: %w", err)
	}
	r := &registry.Registry{Path: path}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /lookup/", func(w http.ResponseWriter, req *http.Request) {
		// /lookup/<project>/<name>
		rest := strings.TrimPrefix(req.URL.Path, "/lookup/")
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			http.Error(w, "expected /lookup/<project>/<name>", 400)
			return
		}
		entry, err := r.Lookup(parts[0], parts[1])
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(entry)
	})

	mux.HandleFunc("GET /list", func(w http.ResponseWriter, _ *http.Request) {
		entries, err := r.List()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(entries)
	})

	mux.HandleFunc("GET /health", healthHandler)

	mux.HandleFunc("POST /browser/restart/{project}", browserRestartHandler(r))

	// Idle-shutdown: track time of last HTTP request via an atomic, and
	// have a background ticker exit the daemon once it has been idle past
	// idleTimeout AND the registry has no live entries. The "no entries"
	// gate is critical — a coordinator may idle for hours but still have
	// sandboxes registered, and those sandboxes need this daemon to
	// resolve siblings.
	var lastActivity atomic.Int64
	lastActivity.Store(time.Now().Unix())

	bumpActivity := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			lastActivity.Store(time.Now().Unix())
			next.ServeHTTP(w, req)
		})
	}

	idleTimeout := daemonIdleDefault
	if s := os.Getenv("CSPACE_REGISTRY_DAEMON_IDLE"); s != "" {
		if d, err := time.ParseDuration(s); err == nil && d > 0 {
			idleTimeout = d
		}
	}

	// Tick at most once per minute, but cap at idleTimeout/2 so short
	// timeouts (used in tests) don't have to wait up to a full minute
	// past the deadline before we notice.
	tickInterval := time.Minute
	if half := idleTimeout / 2; half > 0 && half < tickInterval {
		tickInterval = half
	}
	go func() {
		tick := time.NewTicker(tickInterval)
		defer tick.Stop()
		for range tick.C {
			idleSince := time.Since(time.Unix(lastActivity.Load(), 0))
			if idleSince < idleTimeout {
				continue
			}
			entries, err := r.List()
			if err != nil || len(entries) > 0 {
				continue
			}
			log.Printf("cspace daemon: idle %s with no entries; exiting", idleSince)
			os.Exit(0)
		}
	}()

	// DNS listener for *.cspace.test. Bound to 127.0.0.1 so it's host-only;
	// sandboxes use their own resolver via the substrate adapter's --dns
	// flag. macOS resolver(5) `port` directive (installed by `cspace dns
	// install`) routes per-domain queries here. Both UDP and TCP are served —
	// standard practice and macOS may use either.
	//
	// Either listener failing to bind is fatal: with no DNS, sandboxes can't
	// resolve <name>.cspace.test even though HTTP /lookup still works, and
	// `cspace dns status` would (today) report "running" via the HTTP probe
	// while users see broken name resolution. Exit non-zero so the parent
	// (cspace up's ensureRegistryDaemon, which tails ~/.cspace/daemon.log on
	// timeout) can surface the real error.
	dh := daemonDNSHandler(r, &lastActivity)
	listenAddr, gatewayAddr := daemonDNSAddrs()
	dnsPort := listenAddr
	if i := strings.LastIndex(listenAddr, ":"); i >= 0 {
		dnsPort = listenAddr[i+1:]
	}
	// Loopback bind is fatal — it's how the host's /etc/resolver/
	// cspace.test routes name lookups. Without it, friendly URLs
	// don't work from the host browser at all.
	go func() {
		server := &dns.Server{Addr: listenAddr, Net: "udp", Handler: dh}
		log.Printf("cspace daemon: DNS listening on %s/udp", listenAddr)
		if err := server.ListenAndServe(); err != nil {
			log.Printf("FATAL: cspace daemon DNS UDP bind on %s failed: %v", listenAddr, err)
			log.Printf("       another process may be using this port; check with `lsof -nP -iUDP:%s`", dnsPort)
			log.Printf("       common culprits: another cspace daemon process, or mDNSResponder if 5353 was mistakenly chosen")
			log.Printf("       cspace daemon cannot serve DNS without UDP; exiting")
			os.Exit(1)
		}
	}()
	go func() {
		server := &dns.Server{Addr: listenAddr, Net: "tcp", Handler: dh}
		log.Printf("cspace daemon: DNS listening on %s/tcp", listenAddr)
		if err := server.ListenAndServe(); err != nil {
			log.Printf("FATAL: cspace daemon DNS TCP bind on %s failed: %v", listenAddr, err)
			log.Printf("       another process may be using this port; check with `lsof -nP -iTCP:%s`", dnsPort)
			log.Printf("       common culprits: another cspace daemon process holding the port")
			log.Printf("       cspace daemon cannot serve DNS without TCP; exiting")
			os.Exit(1)
		}
	}()
	// Gateway bind — best-effort WITH RETRY. The vmnet bridge that owns
	// 192.168.64.1 doesn't exist until the first container boots, and the
	// daemon is spawned by `cspace up` BEFORE that — so the initial bind
	// loses a startup race. Retry until it binds so in-container
	// *.cspace.test resolution (which the shared browser sidecar depends on)
	// comes up shortly after the first sandbox starts. The host-loopback
	// listener above is unaffected.
	go func() {
		for {
			server := &dns.Server{Addr: gatewayAddr, Net: "udp", Handler: dh}
			log.Printf("cspace daemon: DNS listening on %s/udp (containers)", gatewayAddr)
			err := server.ListenAndServe()
			log.Printf("WARN: cspace daemon DNS UDP bind on %s failed: %v; retrying in 3s "+
				"(in-container *.cspace.test lookups NXDOMAIN until this binds)", gatewayAddr, err)
			time.Sleep(3 * time.Second)
		}
	}()
	go func() {
		for {
			server := &dns.Server{Addr: gatewayAddr, Net: "tcp", Handler: dh}
			if err := server.ListenAndServe(); err != nil {
				log.Printf("WARN: cspace daemon DNS TCP bind on %s failed: %v; retrying in 3s", gatewayAddr, err)
			}
			time.Sleep(3 * time.Second)
		}
	}()

	addr := bindAddr + ":" + port
	log.Printf("cspace daemon: listening on %s (idle timeout %s)", addr, idleTimeout)
	if err := http.ListenAndServe(addr, bumpActivity(mux)); err != nil {
		return err
	}
	return nil
}

// healthHandler reports liveness plus the running binary's version, so
// callers (ensureRegistryDaemon) can distinguish "a daemon is up" from "a
// daemon matching this build is up" and replace a stale one on mismatch.
func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": Version})
}

// browserRestartTimeout bounds each POST /browser/restart/{project} request.
// Set comfortably above restartBrowserSidecar's own browserRestartBudget
// (120s) so the ladder always gets to run its own deadline logic to
// completion rather than being cut off by the HTTP request context first.
const browserRestartTimeout = 150 * time.Second

// browserRestartLocks serializes concurrent restart requests for the same
// project so two overlapping POST /browser/restart/<project> calls can't
// race the restart ladder against itself (e.g. one recreating the
// container while the other is mid stop/start). Keyed by project name,
// values are *sync.Mutex — same LoadOrStore-a-per-key-struct shape as
// sandboxIPMemo above, minus the per-key extra state since here the mutex
// itself is all that's needed.
var browserRestartLocks sync.Map

// browserRestartHandler handles POST /browser/restart/{project}: restarts
// the project's shared browser sidecar via restartBrowserFn (the
// restartBrowserSidecar ladder). Extracted from runDaemonServe (rather than
// registered as an inline closure) so tests can invoke it directly with a
// crafted RemoteAddr and req.SetPathValue, without binding a real listener.
//
// Auth: a loopback caller (RemoteAddr host 127.0.0.1/::1 — i.e. the host's
// own `cspace browser restart` CLI talking to the daemon over loopback) is
// trusted implicitly. A non-loopback caller (a sandbox reaching the daemon
// via the vmnet gateway IP) must present `Authorization: Bearer <tok>`
// matching a registry entry whose Project equals the path's {project} —
// this stops one project's sandboxes from restarting a sibling project's
// shared browser.
func browserRestartHandler(r *registry.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		project := req.PathValue("project")
		if project == "" {
			http.Error(w, "expected /browser/restart/<project>", http.StatusBadRequest)
			return
		}

		if !isLoopbackRemoteAddr(req.RemoteAddr) && !browserRestartAuthorized(r, req, project) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		lockV, _ := browserRestartLocks.LoadOrStore(project, &sync.Mutex{})
		lock := lockV.(*sync.Mutex)
		lock.Lock()
		defer lock.Unlock()

		ctx, cancel := context.WithTimeout(req.Context(), browserRestartTimeout)
		defer cancel()

		bs, err := restartBrowserFn(ctx, project, "")
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":             true,
			"ip":             bs.IP,
			"cdpUrl":         bs.CDPURL,
			"runServerWsUrl": bs.RunServerWSURL,
		})
	}
}

// isLoopbackRemoteAddr reports whether an http.Request.RemoteAddr's host
// portion is a loopback address — 127.0.0.0/8, ::1, or an IPv4-mapped IPv6
// form of either (e.g. ::ffff:127.0.0.1, which net.IP.IsLoopback correctly
// recognizes). Uses net.SplitHostPort + net.ParseIP rather than a string
// prefix/equality check so a crafted value like "127.0.0.1.evil.com:1234"
// (not a valid IP) can't be mistaken for loopback.
func isLoopbackRemoteAddr(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr // no port present; be permissive about the shape
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// browserRestartAuthorized reports whether req carries a Bearer token
// matching a registry entry for project. Both sides of the comparison must
// be non-empty: an empty/missing Authorization header must never match an
// entry whose Token field happens to be empty (e.g. a sandbox that hasn't
// been issued a token yet) — that would let an unauthenticated request in.
func browserRestartAuthorized(r *registry.Registry, req *http.Request, project string) bool {
	const prefix = "Bearer "
	auth := req.Header.Get("Authorization")
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	tok := strings.TrimPrefix(auth, prefix)
	if tok == "" {
		return false
	}
	entries, err := r.List()
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Project == project && e.Token != "" && e.Token == tok {
			return true
		}
	}
	return false
}

// daemonDNSHandler answers A queries for <sandbox>.cspace.test from the
// live registry. Unknown names and the wrong domain return NXDOMAIN; AAAA
// and other qtypes return NOERROR with no answer so IPv6-aware clients fall
// back to A instead of caching a "no such name".
func daemonDNSHandler(r *registry.Registry, lastActivity *atomic.Int64) dns.Handler {
	return dns.HandlerFunc(func(w dns.ResponseWriter, msg *dns.Msg) {
		lastActivity.Store(time.Now().Unix())

		reply := new(dns.Msg)
		reply.SetReply(msg)
		reply.Authoritative = true

		for _, q := range msg.Question {
			name := strings.ToLower(q.Name)
			if !strings.HasSuffix(name, "."+daemonDNSDomain) && name != daemonDNSDomain {
				reply.Rcode = dns.RcodeNameError
				continue
			}
			// Strip the suffix and split remaining labels. We accept:
			//   <sandbox>.cspace.test             — single project shortcut
			//   <sandbox>.<project>.cspace.test   — fully qualified
			// Multi-label names beyond two parts are not sandbox lookups.
			labels := strings.TrimSuffix(name, "."+daemonDNSDomain)
			labels = strings.TrimSuffix(labels, ".")
			if labels == "" {
				reply.Rcode = dns.RcodeNameError
				continue
			}
			parts := strings.Split(labels, ".")
			var sandbox, project, service, ip string
			resolved := false
			switch len(parts) {
			case 1:
				sandbox = parts[0]
			case 2:
				// Labels are emitted closest-first per DNS, so the
				// sandbox label is the leftmost (parts[0]). "browser" is
				// a reserved leftmost label at this 2-label position:
				// browser.<project>.cspace.test resolves the project's
				// shared browser sidecar (browserSingletonName) directly
				// via container lookup, skipping the registry entirely —
				// no registry entry exists for the sidecar. That keeps
				// the name restart-stable: a new IP after a sidecar
				// restart propagates within one lookupSidecarIPFn memo
				// TTL — lookupSidecarIP is bounded (2s ctx, same as
				// inspectContainerIP) and memoized via the shared ipMemo
				// cache, so a hung apiserver costs at most one bounded
				// inspect per sidecar name per TTL window rather than one
				// unbounded `container inspect` per query. A bare
				// 1-label "browser" query is NOT reserved; it falls
				// through to the ordinary sandbox-name path below and
				// NXDOMAINs there since no registry entry is named
				// "browser".
				if parts[0] == "browser" {
					sidecarIP, err := lookupSidecarIPFn(browserSingletonName(parts[1]))
					if err != nil || sidecarIP == "" {
						reply.Rcode = dns.RcodeNameError
						continue
					}
					ip = sidecarIP
					resolved = true
				} else {
					sandbox, project = parts[0], parts[1]
				}
			case 3:
				// <service>.<sandbox>.<project>.cspace.test — resolves
				// to a compose-spawned sidecar microVM. The sandbox/project
				// existence check below confirms the sidecar is plausibly
				// part of an active cspace cluster; the actual sidecar IP
				// is fetched via container inspect.
				service, sandbox, project = parts[0], parts[1], parts[2]
			default:
				reply.Rcode = dns.RcodeNameError
				continue
			}

			if !resolved {
				entries, err := r.List()
				if err != nil {
					log.Printf("dns: registry list: %v", err)
					reply.Rcode = dns.RcodeServerFailure
					continue
				}
				var matches []registry.Entry
				for _, e := range entries {
					if e.Name != sandbox || e.IP == "" {
						continue
					}
					if project != "" && e.Project != project {
						continue
					}
					matches = append(matches, e)
				}
				switch len(matches) {
				case 0:
					reply.Rcode = dns.RcodeNameError
					continue
				case 1:
					ip = liveSandboxIP(matches[0].Project, matches[0].Name, matches[0].IP)
					if service != "" {
						// 4-label query: redirect to the sidecar container
						// `cspace-<project>-<sandbox>-<service>` and resolve
						// via the substrate. The sandbox match above
						// confirmed the cluster exists; the sidecar may or
						// may not be running.
						sidecar := fmt.Sprintf("cspace-%s-%s-%s",
							matches[0].Project, matches[0].Name, service)
						sidecarIP, err := lookupSidecarIPFn(sidecar)
						if err != nil || sidecarIP == "" {
							reply.Rcode = dns.RcodeNameError
							continue
						}
						ip = sidecarIP
					}
				default:
					// Multiple projects have this sandbox name and the user
					// didn't specify which. Force them to disambiguate via
					// <sandbox>.<project>.cspace.test. Logging the
					// ambiguity helps diagnose "why doesn't this resolve?"
					// when two projects collide.
					projects := make([]string, 0, len(matches))
					for _, e := range matches {
						projects = append(projects, e.Project)
					}
					log.Printf("dns: ambiguous %s — sandbox %q exists in %v; use <sandbox>.<project>.cspace.test",
						name, sandbox, projects)
					reply.Rcode = dns.RcodeNameError
					continue
				}
			}

			// Only A queries get a record; AAAA / others get NOERROR
			// with no answer.
			if q.Qtype == dns.TypeA {
				parsed := net.ParseIP(ip).To4()
				if parsed == nil {
					// Registry has a non-IPv4 entry; treat as no answer.
					continue
				}
				reply.Answer = append(reply.Answer, &dns.A{
					Hdr: dns.RR_Header{
						Name:   q.Name,
						Rrtype: dns.TypeA,
						Class:  dns.ClassINET,
						Ttl:    daemonDNSTTL,
					},
					A: parsed,
				})
			}
		}

		if err := w.WriteMsg(reply); err != nil {
			log.Printf("dns: write reply: %v", err)
		}
	})
}

func newDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print whether the cspace daemon is running",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := &http.Client{Timeout: 2 * time.Second}
			resp, err := client.Get("http://127.0.0.1:" + daemonHTTPPort + "/health")
			if err != nil {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "daemon: not running")
				return nil
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode == 200 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "daemon: running on 127.0.0.1:%s\n", daemonHTTPPort)
			} else {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "daemon: unexpected status %d\n", resp.StatusCode)
			}
			return nil
		},
	}
}

func newDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the cspace daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := stopRegistryDaemon(); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "daemon: stopped")
			return nil
		},
	}
}

// stopRegistryDaemon kills any running daemon process and blocks until its
// fatal-to-rebind loopback ports (HTTP + DNS listen addr) are actually free,
// or a timeout elapses. Without this wait, a caller that immediately
// respawns a new daemon (ensureRegistryDaemon on a stale-version reuse
// check) can race the dying process for the loopback DNS bind and lose,
// since that bind is fatal to the new daemon.
func stopRegistryDaemon() error {
	// Match the embedded subcommand's process line. Anchoring on "daemon
	// serve" is broad enough to catch both `cspace daemon serve` and
	// `bin/cspace-go daemon serve` invocations, and narrow enough to skip
	// `cspace daemon stop` itself (different argv tail).
	out, err := exec.Command("pkill", "-f", "daemon serve").CombinedOutput()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return nil // pkill exit 1 == no matches
		}
		return fmt.Errorf("pkill: %w (%s)", err, out)
	}
	listen, _ := daemonDNSAddrs()
	waitPortFree(daemonHTTPAddr(), 3*time.Second)
	waitPortFree(listen, 3*time.Second)
	return nil
}

// waitPortFree polls addr until dialing it is refused (port free) or the
// deadline elapses. It never returns an error — callers treat a still-bound
// port past the deadline as "did our best" and fall through to the caller's
// own respawn-retry loop.
func waitPortFree(addr string, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil { // refused == free
			return
		}
		_ = c.Close()
		time.Sleep(100 * time.Millisecond)
	}
}

// lookupSidecarIPCtx resolves the vmnet IP of a compose-spawned sidecar by
// shelling out to `container inspect`, bounded by ctx. Returns ("", nil)
// when the sidecar isn't running. The DNS handler treats both empty IP and a
// non-nil error as NXDOMAIN.
func lookupSidecarIPCtx(ctx context.Context, name string) (string, error) {
	out, err := exec.CommandContext(ctx, "container", "inspect", name).Output()
	if err != nil {
		// Apple Container exits non-zero only on transport-level error;
		// missing containers come back as exit 0 with body "[]". Treat
		// any error here as "no answer" rather than DNS server failure.
		return "", nil
	}
	var records []struct {
		Networks []struct {
			IPv4Address string `json:"ipv4Address"`
		} `json:"networks"`
	}
	if err := json.Unmarshal(out, &records); err != nil {
		return "", err
	}
	if len(records) == 0 || len(records[0].Networks) == 0 {
		return "", nil
	}
	addr := records[0].Networks[0].IPv4Address
	// Strip the CIDR suffix (e.g. "192.168.64.245/24" -> "192.168.64.245").
	if i := strings.IndexByte(addr, '/'); i > 0 {
		addr = addr[:i]
	}
	return addr, nil
}

// lookupSidecarIP resolves a sidecar container's IP for the DNS handler's
// two sidecar-resolution call sites (the 3-label <service>.<sandbox>.<project>
// branch and the 2-label reserved "browser.<project>" branch). It shares the
// same bounded-and-memoized path as liveSandboxIP below (see
// memoizedContainerIP): each distinct container name pays at most one
// inspectContainerIP call — bounded to a 2s ctx — per daemonDNSTTL seconds,
// with failures negative-cached the same as successes. Before this, it ran
// lookupSidecarIPCtx with an unbounded context.Background(), so a hung Apple
// Container apiserver could spawn one never-exiting `container inspect`
// subprocess per browser/service DNS query with no ceiling and no cache —
// exactly the failure mode liveSandboxIP's memo was built to avoid on the
// higher-frequency plain-sandbox path.
func lookupSidecarIP(name string) (string, error) {
	return memoizedContainerIP(name)
}

// lookupSidecarIPFn is the package seam the DNS handler calls instead of
// lookupSidecarIP directly, so tests can fake sidecar resolution without
// shelling out to the real `container` CLI.
var lookupSidecarIPFn = lookupSidecarIP

// errContainerGone is returned by memoizedContainerIP on any miss (a fresh
// failed inspect or one served from the negative memo), and by test stubs
// of inspectContainerIP to simulate a container that no longer exists (or
// an inspect that failed for any other reason). liveSandboxIP treats any
// non-nil error the same way, by falling back to the registry IP;
// lookupSidecarIP propagates it straight through to the DNS handler as
// NXDOMAIN.
var errContainerGone = errors.New("container not found")

// inspectContainerIP is the package seam liveSandboxIP calls to get a
// container's live IP; it's a var so tests can stub it without shelling
// out. The 2s timeout bounds a single DNS answer's worst case if the Apple
// Container apiserver is hung, rather than blocking indefinitely.
var inspectContainerIP = func(container string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return lookupSidecarIPCtx(ctx, container)
}

// ipMemo holds the last inspected IP for one container plus when it was
// fetched, guarded by its own mutex so concurrent DNS handler goroutines
// resolving the same name serialize on one inspect instead of racing.
type ipMemo struct {
	mu   sync.Mutex
	ip   string
	when time.Time
}

// sandboxIPMemo maps container name -> *ipMemo. sync.Map because the DNS
// handler runs one goroutine per query and this is a read-mostly cache
// across many distinct sandbox names. Both plain-sandbox container names
// (cspace-<project>-<sandbox>, keyed by liveSandboxIP) and sidecar
// container names (cspace-<project>-browser,
// cspace-<project>-<sandbox>-<service>, keyed by lookupSidecarIP) share
// this one map — they're disjoint name shapes in practice (a sidecar
// container claims its name in the substrate, so a sandbox can't also be
// registered under the same name), and reusing one cache is simpler than
// inventing a parallel one per call site.
var sandboxIPMemo sync.Map

// memoizedContainerIP is the shared bound-and-memoize wrapper around
// inspectContainerIP used by both liveSandboxIP and lookupSidecarIP: at
// most one inspectContainerIP call — itself bounded to a 2s ctx — per
// container name per daemonDNSTTL seconds, whether that call succeeds or
// fails. A failing (or timed-out) inspect is negative-cached the same as a
// successful one, so a container that's gone (or an apiserver that's
// hung) doesn't pay the inspect cost — or the full 2s worst case — on
// every query for the rest of the TTL window. Returns errContainerGone on
// any miss, whether freshly inspected or served from the negative memo;
// callers that want a fallback value (liveSandboxIP) supply their own,
// callers that want NXDOMAIN (lookupSidecarIP) propagate the error as-is.
func memoizedContainerIP(container string) (string, error) {
	v, _ := sandboxIPMemo.LoadOrStore(container, &ipMemo{})
	m := v.(*ipMemo)
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.when.IsZero() && time.Since(m.when) < daemonDNSTTL*time.Second {
		// Recently attempted, success or failure: honor the memo without
		// re-inspecting.
		if m.ip != "" {
			return m.ip, nil
		}
		return "", errContainerGone
	}
	m.when = time.Now()
	if ip, err := inspectContainerIP(container); err == nil && ip != "" {
		m.ip = ip
		return ip, nil
	}
	m.ip = "" // negative-cache the failure so we don't re-inspect until TTL expiry
	return "", errContainerGone
}

// liveSandboxIP prefers the container's currently-inspected IP over the
// registry value (which goes stale when a sandbox restarts onto a new vmnet
// IP — vmnet reassigns freed IPs, so a stale registry entry can point at a
// different live container).
// CAVEAT: when inspect can't answer (timeout/apiserver hung/container
// gone) it falls back to the registry IP, which may still be stale — the
// reassigned-IP hazard is reduced, not eliminated, in that failure window.
func liveSandboxIP(project, name, registryIP string) string {
	container := fmt.Sprintf("cspace-%s-%s", project, name)
	if ip, err := memoizedContainerIP(container); err == nil {
		return ip
	}
	return registryIP
}
