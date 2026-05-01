package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/elliottregan/cspace/internal/secrets"
	"github.com/spf13/cobra"
)

func newInitKeychainCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init-keychain",
		Short: "Prompt for cspace credentials and store them in macOS Keychain",
		Long: `Walks through the cspace credentials cspace2-up looks for and writes any
the user supplies into macOS Keychain under service name "cspace-<KEY>".

cspace2-up's secrets resolver reads Keychain first for these keys, so once
stored they flow into every sandbox automatically without going through
~/.cspace/secrets.env.

Currently macOS-only. On Linux, use ~/.cspace/secrets.env instead.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runtime.GOOS != "darwin" {
				fmt.Fprintln(cmd.OutOrStdout(),
					"init-keychain is macOS-only. On Linux, put credentials in ~/.cspace/secrets.env.")
				return nil
			}

			out := cmd.OutOrStdout()
			reader := bufio.NewReader(os.Stdin)

			fmt.Fprintln(out, "cspace init-keychain — store credentials in macOS Keychain")
			fmt.Fprintln(out, "")
			fmt.Fprintln(out, "For each prompt: type/paste the value and hit ENTER, or hit ENTER alone to skip.")
			fmt.Fprintln(out, "An existing Keychain entry will only be replaced if you confirm the overwrite.")
			fmt.Fprintln(out, "")

			// Each entry: env var name, prompt label.
			entries := []struct {
				key   string
				label string
			}{
				{"ANTHROPIC_API_KEY", "Anthropic API key (sk-ant-api…) or long-lived OAuth token (sk-ant-oat…)"},
				{"CLAUDE_CODE_OAUTH_TOKEN", "Claude Code OAuth token (from `claude setup-token`)"},
				{"GH_TOKEN", "GitHub token (gh auth token)"},
			}

			for _, e := range entries {
				if err := promptOne(out, reader, e.key, e.label); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

// promptOne prompts the user once for a single credential. If the user
// supplies a non-empty value AND there's no existing entry (or they confirm
// overwrite), it writes to Keychain. Skips silently on empty input.
func promptOne(out io.Writer, reader *bufio.Reader, key, label string) error {
	service := "cspace-" + key
	existing, err := secrets.ReadKeychain(service)
	if err != nil {
		return fmt.Errorf("read keychain %s: %w", service, err)
	}
	hint := ""
	if existing != "" {
		hint = " [already set; ENTER to keep, type a new value to replace]"
	}
	fmt.Fprintf(out, "%s%s:\n  %s > ", label, hint, key)
	line, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	val := strings.TrimSpace(line)
	if val == "" {
		fmt.Fprintln(out, "  (skipped)")
		return nil
	}
	if err := secrets.WriteKeychain(service, val); err != nil {
		return fmt.Errorf("write keychain %s: %w", service, err)
	}
	fmt.Fprintf(out, "  stored under Keychain service %q\n", service)
	return nil
}
