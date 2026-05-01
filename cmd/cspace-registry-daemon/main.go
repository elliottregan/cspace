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

	addr := bindAddr + ":" + port
	log.Printf("cspace-registry-daemon: listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
