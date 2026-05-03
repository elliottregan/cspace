package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Aggregate health check across all cspace subsystems",
		Long: `cspace doctor consolidates the status output from every cspace subsystem
into a single overview report:

  - Apple Container CLI (darwin only): installed, supported version, apiserver running
  - cspace daemon: HTTP responding, DNS responding
  - DNS routing for the cspace dns domain: resolver file, scutil routing, end-to-end
  - Anthropic credentials: source per alias, OAuth-expiry warnings
  - GitHub credentials: source per alias
  - Sandboxes: alive count, stuck-booting count, dead registry entries

Symbols: ✓ pass, ! advisory (actionable but not broken), ✗ failure.

Always exits 0 — doctor is informational, not a CI gate.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			parent := cmd.Context()
			if parent == nil {
				parent = context.Background()
			}
			ctx, cancel := context.WithTimeout(parent, 15*time.Second)
			defer cancel()
			return runDoctor(ctx, cmd.OutOrStdout())
		},
	}
}

func runDoctor(ctx context.Context, out io.Writer) error {
	probes := []ProbeResult{
		ProbeAppleContainer(ctx),
		ProbeDaemon(ctx),
		ProbeDns(ctx),
		ProbeAnthropicCredentials(ctx),
		ProbeGitHubCredentials(ctx),
		ProbeSandboxes(ctx),
	}

	warnings, failures := 0, 0
	for _, p := range probes {
		_, _ = fmt.Fprintf(out, "%s\n", p.Subsystem)
		for _, c := range p.Checks {
			mark := "✓"
			switch c.Status {
			case ProbeWarn:
				mark = "!"
				warnings++
			case ProbeFail:
				mark = "✗"
				failures++
			}
			_, _ = fmt.Fprintf(out, "  %s %s\n", mark, c.Title)
			for _, d := range c.Details {
				_, _ = fmt.Fprintf(out, "    %s\n", d)
			}
		}
		_, _ = fmt.Fprintln(out)
	}

	switch {
	case failures > 0:
		issueWord := "issues need"
		if failures == 1 {
			issueWord = "issue needs"
		}
		_, _ = fmt.Fprintf(out, "Overall: %d %s attention", failures, issueWord)
		if warnings > 0 {
			advisoryWord := "advisory items"
			if warnings == 1 {
				advisoryWord = "advisory item"
			}
			_, _ = fmt.Fprintf(out, " (and %d %s)", warnings, advisoryWord)
		}
		_, _ = fmt.Fprintln(out)
	case warnings > 0:
		advisoryWord := "advisory items"
		if warnings == 1 {
			advisoryWord = "advisory item"
		}
		_, _ = fmt.Fprintf(out, "Overall: %d %s; otherwise healthy.\n", warnings, advisoryWord)
	default:
		_, _ = fmt.Fprintln(out, "Overall: healthy")
	}
	return nil
}
