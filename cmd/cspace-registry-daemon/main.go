// cspace-registry-daemon: HTTP endpoint over the sandbox registry, intended
// to be reached from inside sandboxes via the host gateway IP.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
)

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

	addr := bindAddr + ":" + port
	log.Printf("cspace-registry-daemon: listening on %s (idle timeout %s)", addr, idleTimeout)
	if err := http.ListenAndServe(addr, bumpActivity(mux)); err != nil {
		log.Fatal(err)
	}
}
