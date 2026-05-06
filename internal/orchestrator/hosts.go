package orchestrator

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

const (
	hostsMarkerStart = "# BEGIN cspace-injected"
	hostsMarkerEnd   = "# END cspace-injected"
)

// renderHosts builds the cspace-managed block for /etc/hosts, sorted
// by service name for deterministic output. The block is delimited by
// BEGIN/END markers so injectHosts can replace it idempotently.
func renderHosts(ips map[string]string) string {
	names := make([]string, 0, len(ips))
	for n := range ips {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString(hostsMarkerStart + "\n")
	for _, n := range names {
		fmt.Fprintf(&b, "%s %s\n", ips[n], n)
	}
	b.WriteString(hostsMarkerEnd + "\n")
	return b.String()
}

// injectHosts removes any prior cspace-injected block from the target
// microVM's /etc/hosts and appends the new content. Idempotent.
//
// Apple Container's `container exec` runs commands as root by default,
// so we don't prefix with sudo — many minimal sidecar images (like
// ghcr.io/get-convex/convex-backend) don't ship sudo at all and a
// sudo-prefixed command would fail with `sh: sudo: not found`.
func injectHosts(ctx context.Context, sub Substrate, container, content string) error {
	clean := []string{
		"sh", "-c",
		fmt.Sprintf("sed -i '/^%s$/,/^%s$/d' /etc/hosts", hostsMarkerStart, hostsMarkerEnd),
	}
	if _, err := sub.Exec(ctx, container, clean); err != nil {
		return fmt.Errorf("clean hosts in %s: %w", container, err)
	}
	add := []string{
		"sh", "-c",
		fmt.Sprintf("printf '%%s' %s >> /etc/hosts", shellQuote(content)),
	}
	if _, err := sub.Exec(ctx, container, add); err != nil {
		return fmt.Errorf("inject hosts in %s: %w", container, err)
	}
	return nil
}

// shellQuote wraps a string in single quotes for safe shell embedding.
// Handles the embedded-single-quote case via the standard '"'"' trick.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
