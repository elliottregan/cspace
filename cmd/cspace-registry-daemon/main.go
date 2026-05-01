// cspace-registry-daemon: HTTP endpoint over the sandbox registry, intended
// to be reached from inside sandboxes via the host gateway IP.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/miekg/dns"
)

const (
	// dnsListenAddr: 127.0.0.1:5354 rather than the mDNS-conventional :5353,
	// because macOS mDNSResponder owns UDP/5353 wildcard and we can't share
	// it. 5354 is the well-known "alt-mdns" port and is unclaimed on macOS.
	// DNS B (cspace dns install) writes /etc/resolver/cspace2.local with
	// `port 5354` to match.
	dnsListenAddr = "127.0.0.1:5354"
	dnsDomain     = "cspace2.local." // trailing dot is canonical
	dnsTTL        = 5                // seconds; sandbox IPs change across restarts
)

// dnsHandler answers A queries for <sandbox>.cspace2.local from the live
// registry. Unknown names and the wrong domain return NXDOMAIN; AAAA and
// other qtypes return NOERROR with no answer so IPv6-aware clients fall
// back to A instead of caching a "no such name".
func dnsHandler(r *registry.Registry, lastActivity *atomic.Int64) dns.Handler {
	return dns.HandlerFunc(func(w dns.ResponseWriter, msg *dns.Msg) {
		lastActivity.Store(time.Now().Unix())

		reply := new(dns.Msg)
		reply.SetReply(msg)
		reply.Authoritative = true

		for _, q := range msg.Question {
			name := strings.ToLower(q.Name)
			if !strings.HasSuffix(name, "."+dnsDomain) && name != dnsDomain {
				reply.Rcode = dns.RcodeNameError
				continue
			}
			// Strip the suffix to get the sandbox name. For now we only
			// support a single label before the domain (e.g.
			// "mercury.cspace2.local"). Multi-label or empty names are
			// not sandbox lookups.
			sandbox := strings.TrimSuffix(name, "."+dnsDomain)
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
						Ttl:    dnsTTL,
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

func main() {
	port := os.Getenv("CSPACE_REGISTRY_DAEMON_PORT")
	if port == "" {
		port = "6280"
	}
	bindAddr := os.Getenv("CSPACE_REGISTRY_DAEMON_ADDR")
	if bindAddr == "" {
		bindAddr = "0.0.0.0" // bind on all interfaces so sandboxes can reach via gateway IP
	}

	path, err := registry.DefaultPath()
	if err != nil {
		log.Fatalf("registry path: %v", err)
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

	idleTimeout := 30 * time.Minute
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
			log.Printf("cspace-registry-daemon: idle %s with no entries; exiting", idleSince)
			os.Exit(0)
		}
	}()

	// DNS listener for *.cspace2.local. Bound to 127.0.0.1 so it's host-only;
	// sandboxes use their own resolver via the substrate adapter's --dns
	// flag. macOS resolver(5) `port` directive (installed by `cspace dns
	// install`, DNS B's job) routes per-domain queries here. Both UDP and
	// TCP are served — standard practice and macOS may use either.
	dh := dnsHandler(r, &lastActivity)
	go func() {
		server := &dns.Server{Addr: dnsListenAddr, Net: "udp", Handler: dh}
		log.Printf("cspace-registry-daemon: DNS listening on %s/udp", dnsListenAddr)
		if err := server.ListenAndServe(); err != nil {
			log.Printf("dns udp: %v", err)
		}
	}()
	go func() {
		server := &dns.Server{Addr: dnsListenAddr, Net: "tcp", Handler: dh}
		log.Printf("cspace-registry-daemon: DNS listening on %s/tcp", dnsListenAddr)
		if err := server.ListenAndServe(); err != nil {
			log.Printf("dns tcp: %v", err)
		}
	}()

	addr := bindAddr + ":" + port
	log.Printf("cspace-registry-daemon: listening on %s (idle timeout %s)", addr, idleTimeout)
	if err := http.ListenAndServe(addr, bumpActivity(mux)); err != nil {
		log.Fatal(err)
	}
}
