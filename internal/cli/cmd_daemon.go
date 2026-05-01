package cli

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
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
	// macOS. `cspace dns install` writes /etc/resolver/cspace2.local with
	// `port 5354` to match. (See dnsDomain in cmd_dns.go for why the domain
	// keeps the "2" suffix even after the cspace2-* → cspace * cutover.)
	daemonDNSListenAddr = "127.0.0.1:5354"
	daemonDNSDomain     = "cspace2.local." // trailing dot is canonical
	daemonDNSTTL        = 5                // seconds; sandbox IPs change across restarts

	// daemonIdleDefault is the idle-shutdown threshold when
	// CSPACE_REGISTRY_DAEMON_IDLE is unset.
	daemonIdleDefault = 30 * time.Minute
)

func newDaemonCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the cspace background daemon (registry HTTP + DNS)",
		Long: `cspace up auto-spawns the cspace daemon (HTTP registry lookup on
:6280, DNS for *.cspace2.local on :5354). The daemon idle-exits after 30
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

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"ok":true}`)
	})

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

	// DNS listener for *.cspace2.local. Bound to 127.0.0.1 so it's host-only;
	// sandboxes use their own resolver via the substrate adapter's --dns
	// flag. macOS resolver(5) `port` directive (installed by `cspace dns
	// install`) routes per-domain queries here. Both UDP and TCP are served —
	// standard practice and macOS may use either.
	//
	// Either listener failing to bind is fatal: with no DNS, sandboxes can't
	// resolve <name>.cspace2.local even though HTTP /lookup still works, and
	// `cspace dns status` would (today) report "running" via the HTTP probe
	// while users see broken name resolution. Exit non-zero so the parent
	// (cspace up's ensureRegistryDaemon, which captures stderr) can surface
	// the real error.
	dh := daemonDNSHandler(r, &lastActivity)
	dnsPort := daemonDNSListenAddr
	if i := strings.LastIndex(daemonDNSListenAddr, ":"); i >= 0 {
		dnsPort = daemonDNSListenAddr[i+1:]
	}
	go func() {
		server := &dns.Server{Addr: daemonDNSListenAddr, Net: "udp", Handler: dh}
		log.Printf("cspace daemon: DNS listening on %s/udp", daemonDNSListenAddr)
		if err := server.ListenAndServe(); err != nil {
			log.Printf("FATAL: cspace daemon DNS UDP bind on %s failed: %v", daemonDNSListenAddr, err)
			log.Printf("       another process may be using this port; check with `lsof -nP -iUDP:%s`", dnsPort)
			log.Printf("       common culprits: another cspace daemon process, or mDNSResponder if 5353 was mistakenly chosen")
			log.Printf("       cspace daemon cannot serve DNS without UDP; exiting")
			os.Exit(1)
		}
	}()
	go func() {
		server := &dns.Server{Addr: daemonDNSListenAddr, Net: "tcp", Handler: dh}
		log.Printf("cspace daemon: DNS listening on %s/tcp", daemonDNSListenAddr)
		if err := server.ListenAndServe(); err != nil {
			log.Printf("FATAL: cspace daemon DNS TCP bind on %s failed: %v", daemonDNSListenAddr, err)
			log.Printf("       another process may be using this port; check with `lsof -nP -iTCP:%s`", dnsPort)
			log.Printf("       common culprits: another cspace daemon process holding the port")
			log.Printf("       cspace daemon cannot serve DNS without TCP; exiting")
			os.Exit(1)
		}
	}()

	addr := bindAddr + ":" + port
	log.Printf("cspace daemon: listening on %s (idle timeout %s)", addr, idleTimeout)
	if err := http.ListenAndServe(addr, bumpActivity(mux)); err != nil {
		return err
	}
	return nil
}

// daemonDNSHandler answers A queries for <sandbox>.cspace2.local from the
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
			// Strip the suffix to get the sandbox name. For now we only
			// support a single label before the domain (e.g.
			// "mercury.cspace2.local"). Multi-label or empty names are
			// not sandbox lookups.
			sandbox := strings.TrimSuffix(name, "."+daemonDNSDomain)
			sandbox = strings.TrimSuffix(sandbox, ".")
			if sandbox == "" || strings.Contains(sandbox, ".") {
				reply.Rcode = dns.RcodeNameError
				continue
			}

			entries, err := r.List()
			if err != nil {
				log.Printf("dns: registry list: %v", err)
				reply.Rcode = dns.RcodeServerFailure
				continue
			}
			var ip string
			for _, e := range entries {
				if e.Name == sandbox && e.IP != "" {
					ip = e.IP
					break
				}
			}
			if ip == "" {
				reply.Rcode = dns.RcodeNameError
				continue
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
				fmt.Fprintln(cmd.OutOrStdout(), "daemon: not running")
				return nil
			}
			defer resp.Body.Close()
			if resp.StatusCode == 200 {
				fmt.Fprintf(cmd.OutOrStdout(), "daemon: running on 127.0.0.1:%s\n", daemonHTTPPort)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "daemon: unexpected status %d\n", resp.StatusCode)
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
			// Match the embedded subcommand's process line. Anchoring on
			// "daemon serve" is broad enough to catch both `cspace daemon
			// serve` and `bin/cspace-go daemon serve` invocations, and
			// narrow enough to skip `cspace daemon stop` itself (different
			// argv tail).
			out, err := exec.Command("pkill", "-f", "daemon serve").CombinedOutput()
			if err != nil {
				// pkill returns 1 when no matches; not an error for our purposes.
				if !strings.Contains(err.Error(), "exit status 1") {
					return fmt.Errorf("pkill: %w (%s)", err, out)
				}
			}
			fmt.Fprintln(cmd.OutOrStdout(), "daemon: stopped")
			return nil
		},
	}
}
