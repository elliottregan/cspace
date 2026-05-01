package cli

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/spf13/cobra"
)

// portLabel is a short tag rendered next to a known port. Generic ports
// fall back to "generic" so the column stays aligned. Informational only;
// the user always sees the URL too.
var portLabel = map[int]string{
	3000: "generic dev (Next.js / Express)",
	4173: "Vite preview",
	5173: "Vite dev server",
	6201: "cspace control",
	8000: "generic dev",
	8080: "generic dev",
	8888: "generic dev",
}

// portsToProbe is the static set of well-known TCP ports we probe inside
// a sandbox. Output sorts ascending. Env-var-derived ports (e.g.
// $BRAINSTORMING_PORT) are not threaded through the registry yet — see
// the report on this task.
var portsToProbe = []int{3000, 4173, 5173, 6201, 8000, 8080, 8888}

func newPortsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ports <sandbox>",
		Short: "List ports listening inside a sandbox with friendly URLs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sandbox := args[0]
			project := projectName()

			path, err := registry.DefaultPath()
			if err != nil {
				return err
			}
			r := &registry.Registry{Path: path}
			entry, err := r.Lookup(project, sandbox)
			if err != nil {
				return err
			}

			open := probePorts(entry.IP, portsToProbe, 200*time.Millisecond)
			sort.Ints(open)

			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "ports listening on sandbox %s (%s):\n", sandbox, entry.IP)
			if len(open) == 0 {
				_, _ = fmt.Fprintln(out, "  (none — supervisor /control may not be ready yet)")
				return nil
			}

			useFriendly := dnsInstalled()
			fqdn := fmt.Sprintf("%s.%s", sandbox, dnsDomain)
			for _, port := range open {
				label := portLabel[port]
				if label == "" {
					label = "generic"
				}
				var url string
				if useFriendly {
					url = fmt.Sprintf("http://%s:%d/", fqdn, port)
				} else {
					url = fmt.Sprintf("http://%s:%d/", entry.IP, port)
				}
				_, _ = fmt.Fprintf(out, "  %-6d %-22s %s\n", port, label, url)
			}
			if !useFriendly {
				_, _ = fmt.Fprintln(out, "")
				_, _ = fmt.Fprintf(out, "note: friendly URLs disabled. Run `cspace dns install` once to enable http://<sandbox>.%s/.\n", dnsDomain)
			}
			return nil
		},
	}
}

// probePorts returns the subset of ports that accept a TCP connection at
// host within the given timeout. Probes run concurrently so total elapsed
// is bounded by the per-port timeout, not their sum.
func probePorts(host string, ports []int, timeout time.Duration) []int {
	var (
		mu   sync.Mutex
		open []int
		wg   sync.WaitGroup
	)
	for _, port := range ports {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			addr := net.JoinHostPort(host, strconv.Itoa(p))
			conn, err := net.DialTimeout("tcp", addr, timeout)
			if err != nil {
				return
			}
			_ = conn.Close()
			mu.Lock()
			open = append(open, p)
			mu.Unlock()
		}(port)
	}
	wg.Wait()
	return open
}
