package cli

import (
	"fmt"
	"os"

	"github.com/elliottregan/cspace/internal/config"
	"github.com/elliottregan/cspace/internal/instance"
	"github.com/spf13/cobra"
)

func newCompletionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion scripts",
		Long: `Generate shell completion scripts for cspace.

To load completions:

  Bash:
    source <(cspace completion bash)

  Zsh:
    cspace completion zsh > "${fpath[1]}/_cspace"

  Fish:
    cspace completion fish | source

  PowerShell:
    cspace completion powershell | Out-String | Invoke-Expression`,
		GroupID:               "other",
		DisableFlagsInUseLine: true,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletionV2(os.Stdout, true)
			case "zsh":
				return cmd.Root().GenZshCompletion(os.Stdout)
			case "fish":
				return cmd.Root().GenFishCompletion(os.Stdout, true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletionWithDesc(os.Stdout)
			default:
				return fmt.Errorf("unsupported shell: %s", args[0])
			}
		},
	}
}

// completeInstanceNames provides dynamic completion of running instance names.
// It is used as ValidArgsFunction on commands that take an instance name argument.
func completeInstanceNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// If this is not the first positional arg, no completions
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// Try to load config for the current directory
	cwd, err := os.Getwd()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	projectRoot, err := config.FindProjectRoot(cwd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	tmpCfg, err := config.Load(projectRoot, "")
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	names, err := instance.GetInstances(tmpCfg)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return names, cobra.ShellCompDirectiveNoFileComp
}
