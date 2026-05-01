package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

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
	return &cobra.Command{
		Use:   "init",
		Short: "Seed cspace credentials into macOS Keychain",
		RunE: func(cmd *cobra.Command, args []string) error {
			if runtime.GOOS != "darwin" {
				fmt.Fprintln(cmd.OutOrStdout(),
					"`cspace keychain init` is macOS-only. On Linux, put credentials in ~/.cspace/secrets.env.")
				return nil
			}
			return runKeychainInit(cmd.OutOrStdout(), os.Stdin)
		},
	}
}

func runKeychainInit(out io.Writer, in io.Reader) error {
	reader := bufio.NewReader(in)

	fmt.Fprintln(out, "cspace keychain init — store credentials in macOS Keychain")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "For each prompt: paste a value and press ENTER, or press ENTER alone to skip.")
	fmt.Fprintln(out, "Existing entries display \"already set\" — typing a new value replaces them.")
	fmt.Fprintln(out, "")

	// Anthropic credential — strongly recommend an API key (long-lived).
	{
		existing, err := secrets.ReadKeychain("cspace-ANTHROPIC_API_KEY")
		if err != nil {
			return fmt.Errorf("read keychain cspace-ANTHROPIC_API_KEY: %w", err)
		}
		oauth, expires, _ := secrets.DiscoverClaudeOauthToken()

		fmt.Fprintln(out, "ANTHROPIC API KEY")
		fmt.Fprintln(out, "  Recommended: paste a long-lived API key (sk-ant-api-...) from")
		fmt.Fprintln(out, "  https://console.anthropic.com/settings/keys. Stable across sessions,")
		fmt.Fprintln(out, "  no refresh dance. Use this for any multi-day or autonomous run.")
		fmt.Fprintln(out, "  An OAuth token (sk-ant-oat-...) works too but is short-lived;")
		fmt.Fprintln(out, "  reserve it for short / casual sessions.")
		switch {
		case existing != "":
			fmt.Fprintln(out, "  Status: already set in Keychain (cspace-ANTHROPIC_API_KEY).")
		case oauth != "":
			expiry := "an unknown time (older Claude Code build — no expiresAt field)"
			if !expires.IsZero() {
				expiry = expires.Local().Format("2006-01-02 15:04 MST")
			}
			fmt.Fprintln(out, "  Status: not set in cspace Keychain. Auto-discovery will fall back to your")
			fmt.Fprintf(out, "          Claude Code OAuth token, which expires %s.\n", expiry)
			fmt.Fprintln(out, "  Recommendation: paste a long-lived API key here for stable, multi-day work.")
			fmt.Fprintln(out, "  The OAuth token is fine for short / casual sessions, but long-running")
			fmt.Fprintln(out, "  sandboxes will lose auth mid-task when it expires.")
		default:
			fmt.Fprintln(out, "  Status: not set, and no auto-discoverable host credential found.")
		}
		fmt.Fprintf(out, "  ANTHROPIC_API_KEY > ")
		val, err := readLine(reader)
		if err != nil {
			return err
		}
		switch {
		case val == "":
			fmt.Fprintln(out, "  (skipped)")
		case !strings.HasPrefix(val, "sk-ant-"):
			fmt.Fprintln(out, "  (input does not start with `sk-ant-`; skipping to avoid storing a typo)")
		default:
			if err := secrets.WriteKeychain("cspace-ANTHROPIC_API_KEY", val); err != nil {
				return fmt.Errorf("write keychain cspace-ANTHROPIC_API_KEY: %w", err)
			}
			fmt.Fprintln(out, "  stored at Keychain service \"cspace-ANTHROPIC_API_KEY\".")
		}
		fmt.Fprintln(out, "")
	}

	// GitHub credential — auto-discovery from gh CLI is fine for most users.
	{
		existing, err := secrets.ReadKeychain("cspace-GH_TOKEN")
		if err != nil {
			return fmt.Errorf("read keychain cspace-GH_TOKEN: %w", err)
		}
		gh, _ := secrets.DiscoverGhAuthToken()

		fmt.Fprintln(out, "GITHUB TOKEN (PAT)")
		fmt.Fprintln(out, "  Most users don't need to set this — gh CLI auth is auto-discovered.")
		fmt.Fprintln(out, "  Set this to lock a specific PAT for cspace's use (e.g. project")
		fmt.Fprintln(out, "  scoped, narrower permissions than your gh login).")
		switch {
		case existing != "":
			fmt.Fprintln(out, "  Status: already set in Keychain (cspace-GH_TOKEN).")
		case gh != "":
			fmt.Fprintf(out, "  Status: not set. Auto-discovery will use `gh auth token` (gho_..., len %d).\n", len(gh))
		default:
			fmt.Fprintln(out, "  Status: not set; `gh auth token` returned nothing. Sandboxes won't")
			fmt.Fprintln(out, "  have GitHub auth unless you set this or run `gh auth login` on the host.")
		}
		fmt.Fprintf(out, "  GH_TOKEN > ")
		val, err := readLine(reader)
		if err != nil {
			return err
		}
		switch {
		case val == "":
			fmt.Fprintln(out, "  (skipped)")
		default:
			if err := secrets.WriteKeychain("cspace-GH_TOKEN", val); err != nil {
				return fmt.Errorf("write keychain cspace-GH_TOKEN: %w", err)
			}
			fmt.Fprintln(out, "  stored at Keychain service \"cspace-GH_TOKEN\".")
		}
		fmt.Fprintln(out, "")
	}

	fmt.Fprintln(out, "Done. Run `cspace keychain status` to see what's reachable.")
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
	fmt.Fprintln(out, "cspace credential sources (highest precedence first):")
	fmt.Fprintln(out, "")

	// Find the project root (cfg may be nil if not in a project).
	projectRoot := ""
	userHome, _ := os.UserHomeDir()
	if cfg != nil && cfg.ProjectRoot != "" {
		projectRoot = cfg.ProjectRoot
	}

	families := []struct {
		label   string
		members []string
	}{
		{"Anthropic", []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"}},
		{"GitHub", []string{"GH_TOKEN", "GITHUB_TOKEN", "GITHUB_PERSONAL_ACCESS_TOKEN"}},
	}

	for _, fam := range families {
		fmt.Fprintf(out, "%s:\n", fam.label)
		for _, key := range fam.members {
			source, hint := credentialSource(projectRoot, userHome, key)
			fmt.Fprintf(out, "  %s\n    source: %s\n", key, source)
			if hint != "" {
				fmt.Fprintf(out, "    note:   %s\n", hint)
			}
		}
		fmt.Fprintln(out, "")
	}
	return nil
}

// credentialSource returns the first reachable source for a key and an
// optional hint (e.g. "expires soon" for the OAuth fallback). Mirrors the
// resolution order in secrets.Load() but reports source labels instead of
// returning the value itself — values are never printed.
func credentialSource(projectRoot, userHome, key string) (source, hint string) {
	// Layer 1: project secrets file.
	if projectRoot != "" {
		path := filepath.Join(projectRoot, ".cspace", "secrets.env")
		if hasKey(path, key) {
			return "project secrets file (" + path + ")", ""
		}
	}
	// Layer 2: user secrets file.
	if userHome != "" {
		path := filepath.Join(userHome, ".cspace", "secrets.env")
		if hasKey(path, key) {
			return "user secrets file (" + path + ")", ""
		}
	}
	// Layer 3: cspace-<KEY> Keychain entry.
	val, _ := secrets.ReadKeychain("cspace-" + key)
	if val != "" {
		return "macOS Keychain (cspace-" + key + ")", ""
	}
	// Layer 4: auto-discovery — only relevant for canonical names.
	switch key {
	case "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN":
		if oauth, expires, _ := secrets.DiscoverClaudeOauthToken(); oauth != "" {
			hint := "refreshes when host runs `claude`; sandbox may lose auth on long sessions"
			if !expires.IsZero() {
				hint = fmt.Sprintf("expires %s; %s",
					expires.Local().Format("2006-01-02 15:04 MST"), hint)
			}
			return "auto-discovered (Claude Code OAuth)", hint
		}
	case "GH_TOKEN", "GITHUB_TOKEN", "GITHUB_PERSONAL_ACCESS_TOKEN":
		if gh, _ := secrets.DiscoverGhAuthToken(); gh != "" {
			return "auto-discovered (gh auth token)", ""
		}
	}
	return "not reachable", "no credential available; run `cspace keychain init` or set " + key + " in .cspace/secrets.env"
}

// hasKey reports whether the dotenv file at path defines the named key.
// Returns false when the file is missing or doesn't define the key.
func hasKey(path, key string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	prefix := key + "="
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return true
		}
	}
	return false
}
