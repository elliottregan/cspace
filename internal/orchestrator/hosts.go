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
		b.WriteString(fmt.Sprintf("%s %s\n", ips[n], n))
	}
	b.WriteString(hostsMarkerEnd + "\n")
	return b.String()
}

// injectHosts removes any prior cspace-injected block from the target
// microVM's /etc/hosts and appends the new content. Idempotent.
func injectHosts(ctx context.Context, sub Substrate, container, content string) error {
	clean := []string{
		"sh", "-c",
		fmt.Sprintf("sudo sed -i '/^%s$/,/^%s$/d' /etc/hosts", hostsMarkerStart, hostsMarkerEnd),
	}
	if _, err := sub.Exec(ctx, container, clean); err != nil {
		return fmt.Errorf("clean hosts in %s: %w", container, err)
	}
	add := []string{
		"sh", "-c",
		fmt.Sprintf("printf '%%s' %s | sudo tee -a /etc/hosts >/dev/null", shellQuote(content)),
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
