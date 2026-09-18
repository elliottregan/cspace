package control

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/elliottregan/cspace/internal/devcontainer"
	"github.com/elliottregan/cspace/internal/substrate"
)

// DNSDomain is the suffix cspace's daemon answers DNS queries for; each
// sandbox is reachable at http://<sandbox>.<project>.cspace.test:<port>/.
// ResolverFile is the macOS resolver stanza `sudo cspace dns install` writes;
// its presence is what makes those names resolve on the host.
const (
	DNSDomain    = "cspace.test"
	ResolverFile = "/etc/resolver/" + DNSDomain
)

// Port is one listening TCP port inside a sandbox, with the project's label
// for it (empty when the project declared none) and the URL to reach it.
type Port struct {
	Port  int
	Label string
	URL   string
}

// internalPorts are cspace's own plumbing and never show: 6201 is the
// supervisor's control port, 53 the in-sandbox dnsmasq forwarder for
// *.cspace.test. Mirrors INTERNAL_PORTS in lib/runtime/scripts/statusline.sh.
var internalPorts = map[int]bool{6201: true, 53: true}

// Ports lists the sandbox's labeled listeners, as the in-sandbox statusline
// does, but from the host: labels from the sandbox's own workspace clone,
// live listeners from an `ss -tln` exec, the statusline's curation rule, and
// a URL per port.
func (c *Client) Ports(ctx context.Context, project, sandbox string) ([]Port, error) {
	if c.home == "" {
		return nil, ErrNoHome
	}
	if c.containers == nil {
		return nil, ErrNoContainerCLI
	}
	entry, err := c.lookup(project, sandbox)
	if err != nil {
		return nil, err
	}
	res, err := c.containers.Exec(ctx, containerName(project, sandbox),
		[]string{"ss", "-tln"}, substrate.ExecOpts{})
	if err != nil {
		return nil, fmt.Errorf("exec in %s failed: %w", sandbox, err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("list listeners in %s: ss exited %d: %s",
			sandbox, res.ExitCode, strings.TrimSpace(res.Stderr))
	}

	labels := portLabelsFrom(CloneDir(c.home, project, sandbox))
	ports := curatePorts(parseListeningPorts(res.Stdout), labels)
	resolver := c.resolverInstalled()
	for i := range ports {
		ports[i].URL = portURL(project, sandbox, entry.IP, ports[i].Port, resolver)
	}
	return ports, nil
}

// parseListeningPorts extracts the listening TCP ports from `ss -tln` output:
//
//	State  Recv-Q Send-Q Local Address:Port  Peer Address:Port
//	LISTEN 0      511          0.0.0.0:5173         0.0.0.0:*
//	LISTEN 0      511             [::]:3000            [::]:*
//
// The bind address is deliberately ignored: the entrypoint's PREROUTING DNAT
// rewrites inbound traffic to 127.0.0.1, so a loopback-only listener (Vite's
// default) is still reachable from outside the microVM. Ports are deduped and
// returned ascending. Unlike statusline.sh's `NR>1` awk, this matches on the
// LISTEN state word, which skips the header without assuming it is line one.
func parseListeningPorts(ssOutput string) []int {
	seen := map[int]bool{}
	var ports []int
	for _, line := range strings.Split(ssOutput, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[0] != "LISTEN" {
			continue
		}
		addr := fields[3]
		i := strings.LastIndex(addr, ":")
		if i < 0 {
			continue
		}
		p, err := strconv.Atoi(addr[i+1:])
		if err != nil || seen[p] {
			continue
		}
		seen[p] = true
		ports = append(ports, p)
	}
	sort.Ints(ports)
	return ports
}

// portLabelsFrom resolves a project's port→label map from a sandbox's
// workspace clone, using the same two sources and the same precedence as the
// in-sandbox statusline: devcontainer.json's portsAttributes first (the
// standard format, which survives a future deprecation of .cspace.json's
// container block), falling back to .cspace.json's container.ports only when
// portsAttributes produced no labels at all.
//
// Unreadable or malformed files yield no labels rather than an error: a
// project whose config cannot be parsed has, as far as this query is
// concerned, declared nothing.
func portLabelsFrom(cloneDir string) map[int]string {
	labels := map[int]string{}
	if cfg, err := devcontainer.Load(filepath.Join(cloneDir, ".devcontainer", "devcontainer.json")); err == nil {
		for key, attr := range cfg.PortsAttributes {
			if attr.Label == "" {
				continue
			}
			if p, err := strconv.Atoi(key); err == nil {
				labels[p] = attr.Label
			}
		}
	}
	if len(labels) > 0 {
		return labels
	}

	data, err := os.ReadFile(filepath.Join(cloneDir, ".cspace.json"))
	if err != nil {
		return labels
	}
	var doc struct {
		Container struct {
			Ports map[string]string `json:"ports"`
		} `json:"container"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return labels
	}
	for key, label := range doc.Container.Ports {
		if label == "" {
			continue
		}
		if p, err := strconv.Atoi(key); err == nil {
			labels[p] = label
		}
	}
	return labels
}

// curatePorts applies the statusline's curation rule. cspace's own plumbing
// never shows. Unlabeled ports are hidden ONLY when the project actually
// declared labels — that is the user's explicit "these are the URLs I care
// about" signal. An empty ports object is no signal at all, so with no labels
// every listener shows, noise included. Output is sorted ascending.
func curatePorts(listening []int, labels map[int]string) []Port {
	curate := len(labels) > 0
	out := make([]Port, 0, len(listening))
	for _, p := range listening {
		if internalPorts[p] {
			continue
		}
		label := labels[p]
		if curate && label == "" {
			continue
		}
		out = append(out, Port{Port: p, Label: label})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

// portURL renders one port's address. With the resolver installed the host
// can reach the project-qualified DNS name the daemon answers for, which
// survives the sandbox moving to a new vmnet IP; without it, only the raw IP
// works. Both labels are lowercased to match the daemon's lowercased,
// case-sensitive comparison.
func portURL(project, sandbox, ip string, port int, resolverInstalled bool) string {
	if resolverInstalled {
		return fmt.Sprintf("http://%s.%s.%s:%d/",
			strings.ToLower(sandbox), strings.ToLower(project), DNSDomain, port)
	}
	return fmt.Sprintf("http://%s:%d/", ip, port)
}
