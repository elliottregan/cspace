package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/elliottregan/cspace/internal/credentials"
	"github.com/elliottregan/cspace/internal/secrets"
	"github.com/spf13/cobra"
)

func newKeychainCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "keychain",
		Short: "Manage cspace credentials in macOS Keychain",
		Long: `cspace credentials live in four places by precedence:

  1. <project>/.cspace/secrets.env   — project-scoped lock-in
  2. ~/.cspace/secrets.env           — user-global manual entry
  3. macOS Keychain "cspace-<KEY>"   — set via ` + "`cspace keychain init`" + `
  4. Auto-discovery from host state  — Claude Code OAuth blob, gh auth token

Subcommands manage layer 3. Layers 1, 2 take precedence; layer 4 is fallback.`,
	}
	parent.AddCommand(newKeychainInitCmd())
	parent.AddCommand(newKeychainStatusCmd())
	return parent
}

func newKeychainInitCmd() *cobra.Command {
	var projectScoped bool
	var projectNameFlag string
	c := &cobra.Command{
		Use:   "init",
		Short: "Seed cspace credentials into macOS Keychain",
		Long: "Seed cspace credentials into the macOS Keychain.\n\n" +
			"With --project, entries are stored as cspace-<project>-<KEY> and outrank\n" +
			"the global cspace-<KEY> entries for that project only. Use it to give a\n" +
			"project's sandboxes a narrower token than your personal one — a host\n" +
			"`gh` login carries repo scope over every repository you can reach.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if runtime.GOOS != "darwin" {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(),
					"`cspace keychain init` is macOS-only. On Linux, put credentials in ~/.cspace/secrets.env.")
				return nil
			}
			scope := ""
			if projectScoped {
				name := projectNameFlag
				explicit := name != ""
				if !explicit && cfg != nil {
					name = cfg.Project.Name
					// cfg.Project.Name auto-fills from the directory basename,
					// so only treat it as explicit when .cspace.json set it.
					explicit = configDeclaresProjectName()
				}
				if err := validateProjectScope(name, explicit); err != nil {
					return err
				}
				scope = name
			}
			return runKeychainInit(cmd.OutOrStdout(), os.Stdin, scope)
		},
	}
	c.Flags().BoolVar(&projectScoped, "project", false,
		"store project-scoped entries (cspace-<project>-<KEY>) that outrank the global ones")
	c.Flags().StringVar(&projectNameFlag, "project-name", "",
		"explicit project name for --project (defaults to .cspace.json project.name)")
	return c
}

// configDeclaresProjectName reports whether .cspace.json actually sets
// project.name, as opposed to config.Load auto-filling it from the directory
// basename. A basename-keyed Keychain entry detaches silently when the
// directory is renamed, dropping resolution to the broader global credential.
func configDeclaresProjectName() bool {
	if cfg == nil || cfg.ProjectRoot == "" {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(cfg.ProjectRoot, ".cspace.json"))
	if err != nil {
		return false
	}
	var probe struct {
		Project struct {
			Name string `json:"name"`
		} `json:"project"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return probe.Project.Name != ""
}

// keychainService returns the Keychain service name for a key, scoped to a
// project when scope is non-empty.
func keychainService(scope, key string) string {
	if scope == "" {
		return "cspace-" + key
	}
	return "cspace-" + scope + "-" + key
}

func runKeychainInit(out io.Writer, in io.Reader, scope string) error {
	reader := bufio.NewReader(in)

	_, _ = fmt.Fprintln(out, "cspace keychain init — store credentials in macOS Keychain")
	_, _ = fmt.Fprintln(out, "")
	_, _ = fmt.Fprintln(out, "For each prompt: paste a value and press ENTER, or press ENTER alone to skip.")
	_, _ = fmt.Fprintln(out, "Existing entries display \"already set\" — typing a new value replaces them.")
	_, _ = fmt.Fprintln(out, "")

	// Anthropic credential — strongly recommend an API key (long-lived).
	{
		existing, err := secrets.ReadKeychain(keychainService(scope, "ANTHROPIC_API_KEY"))
		if err != nil {
			return fmt.Errorf("read keychain cspace-ANTHROPIC_API_KEY: %w", err)
		}
		oauth, expires, _ := secrets.DiscoverClaudeOauthToken()

		_, _ = fmt.Fprintln(out, "ANTHROPIC API KEY")
		_, _ = fmt.Fprintln(out, "  Recommended: paste a long-lived API key (sk-ant-api-...) from")
		_, _ = fmt.Fprintln(out, "  https://console.anthropic.com/settings/keys. Stable across sessions,")
		_, _ = fmt.Fprintln(out, "  no refresh dance. Use this for any multi-day or autonomous run.")
		_, _ = fmt.Fprintln(out, "  An OAuth token (sk-ant-oat-...) from `claude setup-token` is also accepted;")
		_, _ = fmt.Fprintln(out, "  it will be stored under cspace-CLAUDE_CODE_OAUTH_TOKEN automatically.")
		switch {
		case existing != "":
			_, _ = fmt.Fprintln(out, "  Status: already set in Keychain (cspace-ANTHROPIC_API_KEY).")
		case oauth != "":
			expiry := "an unknown time (older Claude Code build — no expiresAt field)"
			if !expires.IsZero() {
				expiry = expires.Local().Format("2006-01-02 15:04 MST")
			}
			_, _ = fmt.Fprintln(out, "  Status: not set in cspace Keychain. Auto-discovery will fall back to your")
			_, _ = fmt.Fprintf(out, "          Claude Code OAuth token, which expires %s.\n", expiry)
			_, _ = fmt.Fprintln(out, "  Recommendation: paste a long-lived API key here for stable, multi-day work.")
			_, _ = fmt.Fprintln(out, "  The OAuth token is fine for short / casual sessions, but long-running")
			_, _ = fmt.Fprintln(out, "  sandboxes will lose auth mid-task when it expires.")
		default:
			_, _ = fmt.Fprintln(out, "  Status: not set, and no auto-discoverable host credential found.")
		}
		_, _ = fmt.Fprintf(out, "  ANTHROPIC_API_KEY > ")
		val, err := readLine(reader)
		if err != nil {
			return err
		}
		switch {
		case val == "":
			_, _ = fmt.Fprintln(out, "  (skipped)")
		case !strings.HasPrefix(val, "sk-ant-"):
			_, _ = fmt.Fprintln(out, "  (input does not start with `sk-ant-`; skipping to avoid storing a typo)")
		case strings.HasPrefix(val, "sk-ant-oat"):
			if err := secrets.WriteKeychain(keychainService(scope, "CLAUDE_CODE_OAUTH_TOKEN"), val); err != nil {
				return fmt.Errorf("write keychain cspace-CLAUDE_CODE_OAUTH_TOKEN: %w", err)
			}
			_, _ = fmt.Fprintln(out, "  stored as OAuth token at Keychain service \"cspace-CLAUDE_CODE_OAUTH_TOKEN\".")
		default:
			if err := secrets.WriteKeychain(keychainService(scope, "ANTHROPIC_API_KEY"), val); err != nil {
				return fmt.Errorf("write keychain cspace-ANTHROPIC_API_KEY: %w", err)
			}
			_, _ = fmt.Fprintln(out, "  stored at Keychain service \"cspace-ANTHROPIC_API_KEY\".")
		}
		_, _ = fmt.Fprintln(out, "")
	}

	// GitHub credential — auto-discovery from gh CLI is fine for most users.
	{
		existing, err := secrets.ReadKeychain(keychainService(scope, "GH_TOKEN"))
		if err != nil {
			return fmt.Errorf("read keychain cspace-GH_TOKEN: %w", err)
		}
		gh, _ := secrets.DiscoverGhAuthToken()

		_, _ = fmt.Fprintln(out, "GITHUB TOKEN (PAT)")
		_, _ = fmt.Fprintln(out, "  Most users don't need to set this — gh CLI auth is auto-discovered.")
		_, _ = fmt.Fprintln(out, "  Set this to lock a specific PAT for cspace's use (e.g. project")
		_, _ = fmt.Fprintln(out, "  scoped, narrower permissions than your gh login).")
		switch {
		case existing != "":
			_, _ = fmt.Fprintln(out, "  Status: already set in Keychain (cspace-GH_TOKEN).")
		case gh != "":
			_, _ = fmt.Fprintf(out, "  Status: not set. Auto-discovery will use `gh auth token` (gho_..., len %d).\n", len(gh))
		default:
			_, _ = fmt.Fprintln(out, "  Status: not set; `gh auth token` returned nothing. Sandboxes won't")
			_, _ = fmt.Fprintln(out, "  have GitHub auth unless you set this or run `gh auth login` on the host.")
		}
		_, _ = fmt.Fprintf(out, "  GH_TOKEN > ")
		val, err := readLine(reader)
		if err != nil {
			return err
		}
		switch val {
		case "":
			_, _ = fmt.Fprintln(out, "  (skipped)")
		default:
			if err := secrets.WriteKeychain(keychainService(scope, "GH_TOKEN"), val); err != nil {
				return fmt.Errorf("write keychain cspace-GH_TOKEN: %w", err)
			}
			_, _ = fmt.Fprintln(out, "  stored at Keychain service \"cspace-GH_TOKEN\".")
		}
		_, _ = fmt.Fprintln(out, "")
	}

	_, _ = fmt.Fprintln(out, "Done. Run `cspace keychain status` to see what's reachable.")
	return nil
}

func readLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func newKeychainStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show where each cspace credential is currently sourced from",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKeychainStatus(cmd.OutOrStdout())
		},
	}
}

func runKeychainStatus(out io.Writer) error {
	_, _ = fmt.Fprintln(out, "cspace credential sources (highest precedence first):")
	_, _ = fmt.Fprintln(out, "")

	// Find the project root (cfg may be nil if not in a project).
	projectRoot := ""
	userHome, _ := os.UserHomeDir()
	if cfg != nil && cfg.ProjectRoot != "" {
		projectRoot = cfg.ProjectRoot
	}

	host := credentials.ProductionHost()
	resolved, err := host.ResolveErr(projectName(), projectRoot, userHome, nil)
	if err != nil {
		return fmt.Errorf("resolve credentials: %w", err)
	}
	selection := host.Select(resolved)

	families := []struct {
		label   string
		members []string
	}{
		{"Anthropic", []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"}},
		{"GitHub", []string{"GH_TOKEN", "GITHUB_TOKEN", "GITHUB_PERSONAL_ACCESS_TOKEN"}},
	}

	for _, fam := range families {
		_, _ = fmt.Fprintf(out, "%s:\n", fam.label)
		for _, key := range fam.members {
			winner, shipping := selection.Winners[key]
			if !shipping {
				_, _ = fmt.Fprintf(out, "  %s\n    source: not shipped\n", key)
				if len(resolved[key].Candidates) > 0 {
					// Resolvable but displaced by group policy: the Anthropic
					// carriers are mutually exclusive, so naming the winner
					// here prevents the "both carriers auto-discovered" row
					// that the old parallel implementation could print.
					_, _ = fmt.Fprintf(out, "    note:   resolvable, but another carrier in this group ships instead\n")
				}
				continue
			}
			_, _ = fmt.Fprintf(out, "  %s\n    source: %s\n", key, describeSource(winner))
			if v, ok := selection.Validities[key]; ok && v != credentials.ValidityUnknown {
				_, _ = fmt.Fprintf(out, "    status: %s\n", v)
			}
			if !winner.ExpiresAt.IsZero() {
				_, _ = fmt.Fprintf(out, "    note:   expires %s; refreshes when host runs `claude`; "+
					"sandbox may lose auth on long sessions\n",
					winner.ExpiresAt.Local().Format("2006-01-02 15:04 MST"))
			}
			for _, c := range resolved[key].Candidates[1:] {
				_, _ = fmt.Fprintf(out, "    also:   %s (outranked)\n", describeSource(c))
			}
		}
		_, _ = fmt.Fprintln(out, "")
	}
	return nil
}

// describeSource renders a credential's origin without ever printing a value.
func describeSource(c credentials.Credential) string {
	if c.Detail == "" {
		return c.Source.String()
	}
	return c.Source.String() + " (" + c.Detail + ")"
}

// validateProjectScope guards `cspace keychain init --project`. The project
// name must be explicit — set in .cspace.json's project.name or passed by
// flag — never derived from the directory basename, which silently detaches
// the scoped entry when a folder is renamed and drops resolution to the
// broader global credential without saying so.
func validateProjectScope(name string, explicit bool) error {
	if name == "" || !explicit || name == "default" {
		return fmt.Errorf("`keychain init --project` needs an explicit project name: " +
			"set project.name in .cspace.json, or pass --project-name")
	}
	return nil
}
