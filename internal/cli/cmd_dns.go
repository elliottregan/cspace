package cli

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/miekg/dns"
	"github.com/spf13/cobra"
)

// dnsDomain is the suffix cspace's daemon answers DNS queries for. Each
// sandbox is reachable from the host browser at
// http://<sandbox>.<project>.cspace.test:<port>/ — see
// https://github.com/elliottregan/cspace for the routing model.
const (
	dnsResolverFile = "/etc/resolver/cspace.test"
	dnsLocalPort    = "5354"
	dnsDomain       = "cspace.test"
	dnsResolverBody = `# Written by ` + "`cspace dns install`" + `. Routes *.cspace.test lookups to the
# cspace daemon's local DNS server on 127.0.0.1. Safe to delete; uninstall
# via ` + "`cspace dns uninstall`" + `.
nameserver 127.0.0.1
port 5354
`
)

func newDnsCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "dns",
		Short: "Manage local DNS routing for *.cspace.test",
		Long: `cspace up registers each sandbox in ~/.cspace/sandbox-registry.json. The
cspace daemon serves that registry over DNS at 127.0.0.1:5354, answering
A queries for <sandbox>.cspace.test with the sandbox's IP.

` + "`cspace dns install`" + ` writes /etc/resolver/cspace.test so macOS routes
those queries through the daemon. After install, sandboxes are reachable
at http://<sandbox>.cspace.test:<port>/ from any browser on the host.`,
	}
	parent.AddCommand(newDnsInstallCmd())
	parent.AddCommand(newDnsUninstallCmd())
	parent.AddCommand(newDnsStatusCmd())
	return parent
}

func newDnsInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install /etc/resolver/cspace.test (requires sudo)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if runtime.GOOS != "darwin" {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "cspace dns install is macOS-only.")
				return nil
			}
			return runDnsInstall(cmd.OutOrStdout())
		},
	}
}

// legacyResolverFiles is the list of pre-rename paths the cspace DNS
// resolver lived at. Tracking each rename here lets `cspace dns install`
// clean up after a brew upgrade across any number of suffix changes:
//   - /etc/resolver/cspace2.local: cspace2 staging-phase name (v1.0.0-rc.0 through rc.20)
//   - /etc/resolver/cspace.local:  cspace.local during rc.21–rc.23 — abandoned because
//     .local is reserved for Multicast DNS and macOS's resolver routing under .local
//     is unreliable (per RFC 6762). Switched to .test (RFC 6761 reserved-for-testing).
var legacyResolverFiles = []string{
	"/etc/resolver/cspace2.local",
	"/etc/resolver/cspace.local",
}

func runDnsInstall(out io.Writer) error {
	// One-time legacy cleanup: prior versions installed under different
	// suffixes (cspace2.local, then cspace.local). Remove any of those so
	// the system stops routing the old suffixes (otherwise stale sandbox
	// URLs keep "working" against a no-longer-running domain).
	for _, legacy := range legacyResolverFiles {
		if _, err := os.Stat(legacy); err == nil {
			_, _ = fmt.Fprintf(out, "removing legacy %s ...\n", legacy)
			rm := exec.Command("sudo", "rm", "-f", legacy)
			rm.Stdin, rm.Stdout, rm.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := rm.Run(); err != nil {
				return fmt.Errorf("sudo rm %s: %w", legacy, err)
			}
		}
	}

	// Idempotency check.
	if existing, err := os.ReadFile(dnsResolverFile); err == nil {
		if string(existing) == dnsResolverBody {
			_, _ = fmt.Fprintln(out, "already installed; resolver file content matches.")
			return nil
		}
		_, _ = fmt.Fprintln(out, "warning: existing /etc/resolver/cspace.test has DIFFERENT content:")
		_, _ = fmt.Fprintln(out, "----")
		_, _ = fmt.Fprint(out, string(existing))
		_, _ = fmt.Fprintln(out, "----")
		_, _ = fmt.Fprintln(out, "Continuing will OVERWRITE this file. (Press Ctrl-C now to abort; sudo password prompt provides a natural pause.)")
	}

	// Stage the new file in tmp so we can `sudo install` it into place.
	tmpFile, err := os.CreateTemp("", "cspace-dns-resolver-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()
	if _, err := tmpFile.WriteString(dnsResolverBody); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	// Ensure /etc/resolver exists.
	mkdir := exec.Command("sudo", "mkdir", "-p", filepath.Dir(dnsResolverFile))
	mkdir.Stdin, mkdir.Stdout, mkdir.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := mkdir.Run(); err != nil {
		return fmt.Errorf("sudo mkdir /etc/resolver: %w", err)
	}

	// Copy the staged file into place with the right ownership/mode in one shot.
	cp := exec.Command("sudo", "install", "-m", "0644", "-o", "root", "-g", "wheel",
		tmpFile.Name(), dnsResolverFile)
	cp.Stdin, cp.Stdout, cp.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cp.Run(); err != nil {
		return fmt.Errorf("sudo install %s: %w", dnsResolverFile, err)
	}

	_, _ = fmt.Fprintf(out, "installed %s\n", dnsResolverFile)

	// Flush macOS's DNS cache so any negative-cached NXDOMAINs from the
	// previous suffix (or from before the daemon was running) get evicted.
	// Two-step flush because both layers maintain caches:
	//   - dscacheutil -flushcache: clears DirectoryService cache
	//   - killall mDNSResponder: full restart (NOT -HUP, which only reloads
	//     config and preserves the negative cache)
	// Sudo is already authorized from the file install above, so the
	// follow-on commands don't re-prompt.
	if err := exec.Command("sudo", "dscacheutil", "-flushcache").Run(); err != nil {
		_, _ = fmt.Fprintf(out, "[cspace] warning: flush DNS cache: %v\n", err)
	}
	if err := exec.Command("sudo", "killall", "mDNSResponder").Run(); err != nil {
		_, _ = fmt.Fprintf(out, "[cspace] warning: restart mDNSResponder: %v\n", err)
	}
	_, _ = fmt.Fprintln(out, "DNS cache flushed; lookups will resolve via the cspace daemon on next request.")
	_, _ = fmt.Fprintln(out, "Run `cspace dns status` to verify.")
	return nil
}

func newDnsUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove /etc/resolver/cspace.test (requires sudo)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if runtime.GOOS != "darwin" {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "cspace dns uninstall is macOS-only.")
				return nil
			}
			return runDnsUninstall(cmd.OutOrStdout())
		},
	}
}

func runDnsUninstall(out io.Writer) error {
	if _, err := os.Stat(dnsResolverFile); err != nil {
		_, _ = fmt.Fprintln(out, "already uninstalled (no resolver file present).")
		return nil
	}
	rm := exec.Command("sudo", "rm", "-f", dnsResolverFile)
	rm.Stdin, rm.Stdout, rm.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := rm.Run(); err != nil {
		return fmt.Errorf("sudo rm %s: %w", dnsResolverFile, err)
	}
	_, _ = fmt.Fprintf(out, "removed %s\n", dnsResolverFile)
	return nil
}

func newDnsStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report DNS routing for *.cspace.test",
		RunE: func(cmd *cobra.Command, args []string) error {
			if runtime.GOOS != "darwin" {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "cspace dns status is macOS-only.")
				return nil
			}
			return runDnsStatus(cmd.OutOrStdout())
		},
	}
}

func runDnsStatus(out io.Writer) error {
	// Check 1: resolver file present?
	resolverInstalled := false
	resolverContents := ""
	resolverContentsMatch := false
	if data, err := os.ReadFile(dnsResolverFile); err == nil {
		resolverInstalled = true
		resolverContents = string(data)
		resolverContentsMatch = strings.TrimSpace(string(data)) == strings.TrimSpace(dnsResolverBody)
	}

	// Check 2: daemon DNS port answering UDP via a real query?
	daemonAnswering := false
	if c, err := net.DialTimeout("udp", "127.0.0.1:"+dnsLocalPort, 1*time.Second); err == nil {
		_ = c.Close()
		daemonAnswering = probeDnsDaemon(1 * time.Second)
	}

	// Check 3: scutil reports a routing for cspace.test?
	scutilHasRouting := false
	scutilLine := ""
	{
		cmd := exec.Command("scutil", "--dns")
		var buf bytes.Buffer
		cmd.Stdout = &buf
		if cmd.Run() == nil {
			text := buf.String()
			all := strings.Split(text, "\n")
			for i, line := range all {
				// scutil --dns prints lines like "  domain   : cspace.test"
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "domain") &&
					strings.Contains(trimmed, ":") &&
					strings.Contains(line, dnsDomain) {
					scutilHasRouting = true
					stop := i + 6
					if stop > len(all) {
						stop = len(all)
					}
					scutilLine = strings.Join(all[i:stop], "\n")
					break
				}
			}
		}
	}

	mark := func(b bool) string {
		if b {
			return "✓"
		}
		return "✗"
	}

	_, _ = fmt.Fprintln(out, "cspace dns status:")
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintf(out, "  %s /etc/resolver/cspace.test present\n", mark(resolverInstalled))
	if resolverInstalled {
		if resolverContentsMatch {
			_, _ = fmt.Fprintln(out, "      content: matches expected (nameserver 127.0.0.1, port 5354)")
		} else {
			_, _ = fmt.Fprintln(out, "      content: DIFFERS from expected:")
			for _, line := range strings.Split(resolverContents, "\n") {
				_, _ = fmt.Fprintf(out, "        %s\n", line)
			}
		}
	}
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintf(out, "  %s cspace daemon DNS answering on 127.0.0.1:%s/udp\n", mark(daemonAnswering), dnsLocalPort)
	if !daemonAnswering {
		_, _ = fmt.Fprintln(out, "      (start a sandbox with `cspace up` to spawn the daemon)")
		_, _ = fmt.Fprintf(out, "      if the daemon HTTP is up but DNS isn't answering, another process may have UDP/%s; check `lsof -nP -iUDP:%s`\n", dnsLocalPort, dnsLocalPort)
	}
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintf(out, "  %s macOS resolver routes *.cspace.test through 127.0.0.1\n", mark(scutilHasRouting))
	if scutilHasRouting {
		for _, line := range strings.Split(scutilLine, "\n") {
			_, _ = fmt.Fprintf(out, "      %s\n", line)
		}
	}
	_, _ = fmt.Fprintln(out)

	switch {
	case resolverInstalled && resolverContentsMatch && daemonAnswering && scutilHasRouting:
		_, _ = fmt.Fprintln(out, "DNS routing for *.cspace.test: WORKING")
	case !resolverInstalled:
		_, _ = fmt.Fprintln(out, "DNS routing for *.cspace.test: NOT INSTALLED — run `cspace dns install`")
	case resolverInstalled && !resolverContentsMatch:
		_, _ = fmt.Fprintln(out, "DNS routing for *.cspace.test: WRONG CONTENT — run `cspace dns install` to overwrite")
	case resolverInstalled && !daemonAnswering:
		_, _ = fmt.Fprintln(out, "DNS routing for *.cspace.test: INSTALLED BUT DAEMON DOWN")
	default:
		_, _ = fmt.Fprintln(out, "DNS routing for *.cspace.test: PARTIAL")
	}
	return nil
}

// probeDnsDaemon issues a UDP query for a synthetic name and returns true if
// the daemon responds at all (NXDOMAIN/NOERROR both count — we just need a
// reply on the wire, which proves the daemon is bound and listening).
func probeDnsDaemon(timeout time.Duration) bool {
	msg := new(dns.Msg)
	msg.SetQuestion("status-probe.cspace.test.", dns.TypeA)
	c := &dns.Client{Net: "udp", Timeout: timeout}
	resp, _, err := c.Exchange(msg, "127.0.0.1:"+dnsLocalPort)
	return err == nil && resp != nil
}
