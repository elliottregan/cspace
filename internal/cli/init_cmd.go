package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/elliottregan/cspace/internal/assets"
	"github.com/elliottregan/cspace/internal/config"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold .cspace.json and .cspace/ directory",
		Long: `Initialize a project for cspace by creating a .cspace.json configuration
file and .cspace/ directory. Auto-detects project name and GitHub repo
from the current directory and git remote.

When running interactively, prompts let you confirm or override the
auto-detected values. In non-interactive mode (piped stdin), auto-detected
defaults are used silently.

Use --full to also copy all templates (Dockerfile, compose files, agent
playbooks) into .cspace/ for customization.`,
		GroupID: "setup",
		RunE:    runInit,
	}

	cmd.Flags().Bool("full", false, "Also copy all templates for customization")

	return cmd
}

func runInit(cmd *cobra.Command, args []string) error {
	full, _ := cmd.Flags().GetBool("full")

	// Find project root (init doesn't go through PersistentPreRunE config loading)
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	projectRoot, err := config.FindProjectRoot(cwd)
	if err != nil {
		return fmt.Errorf("not in a git repository")
	}

	configPath := filepath.Join(projectRoot, ".cspace.json")
	if _, err := os.Stat(configPath); err == nil {
		fmt.Println("Project already initialized (.cspace.json exists).")
		fmt.Println("Edit .cspace.json directly to update configuration.")
		return nil
	}

	// Load embedded defaults so init writes consistent values
	defaultsBytes, err := assets.DefaultsJSON()
	if err != nil {
		return fmt.Errorf("reading defaults: %w", err)
	}
	var initConfig map[string]interface{}
	if err := json.Unmarshal(defaultsBytes, &initConfig); err != nil {
		return fmt.Errorf("parsing defaults: %w", err)
	}

	// Auto-detect project info
	name := filepath.Base(projectRoot)
	repo := config.DetectGitRepo(projectRoot)
	prefix := name
	if len(prefix) >= 2 {
		prefix = prefix[:2]
	}

	var firewallDomains string
	var verifyAll string
	var verifyE2E string

	// Interactive prompts when running in a terminal
	if isInteractive() {
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Project name").
					Value(&name),
				huh.NewInput().
					Title("GitHub repo (owner/repo)").
					Value(&repo),
				huh.NewInput().
					Title("Instance prefix (2-3 chars)").
					Value(&prefix),
				huh.NewInput().
					Title("Extra firewall domains (comma-separated, optional)").
					Value(&firewallDomains),
				huh.NewInput().
					Title("Verification command (e.g. npm run lint && npm test)").
					Value(&verifyAll),
				huh.NewInput().
					Title("E2E test command (optional)").
					Value(&verifyE2E),
			),
		)

		if err := form.Run(); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				fmt.Println("Aborted.")
				return nil
			}
			return fmt.Errorf("prompt error: %w", err)
		}
	} else {
		fmt.Println("Using auto-detected defaults (non-interactive mode)...")
	}

	// Apply user inputs to the defaults-derived config
	projectMap, _ := initConfig["project"].(map[string]interface{})
	if projectMap == nil {
		projectMap = make(map[string]interface{})
	}
	projectMap["name"] = name
	projectMap["repo"] = repo
	projectMap["prefix"] = prefix
	initConfig["project"] = projectMap

	if firewallDomains != "" {
		var domains []string
		for _, d := range strings.Split(firewallDomains, ",") {
			d = strings.TrimSpace(d)
			if d != "" {
				domains = append(domains, d)
			}
		}
		firewallMap, _ := initConfig["firewall"].(map[string]interface{})
		if firewallMap == nil {
			firewallMap = make(map[string]interface{})
		}
		firewallMap["domains"] = domains
		initConfig["firewall"] = firewallMap
	}

	verifyMap, _ := initConfig["verify"].(map[string]interface{})
	if verifyMap == nil {
		verifyMap = make(map[string]interface{})
	}
	if verifyAll != "" {
		verifyMap["all"] = verifyAll
	}
	if verifyE2E != "" {
		verifyMap["e2e"] = verifyE2E
	}
	initConfig["verify"] = verifyMap

	// Remove plugin install list from generated config — users inherit from defaults
	delete(initConfig, "plugins")

	data, err := json.MarshalIndent(initConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := os.WriteFile(configPath, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("writing .cspace.json: %w", err)
	}
	fmt.Println("Created .cspace.json")

	// Create .cspace/ directory
	cspaceDir := filepath.Join(projectRoot, ".cspace")
	if err := os.MkdirAll(cspaceDir, 0755); err != nil {
		return fmt.Errorf("creating .cspace/: %w", err)
	}
	fmt.Println("Created .cspace/")

	// Add .cspace.local.json to .gitignore
	gitignorePath := filepath.Join(projectRoot, ".gitignore")
	if data, err := os.ReadFile(gitignorePath); err == nil {
		if !strings.Contains(string(data), ".cspace.local.json") {
			f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_WRONLY, 0644)
			if err == nil {
				fmt.Fprintln(f, ".cspace.local.json")
				f.Close()
				fmt.Println("Added .cspace.local.json to .gitignore")
			}
		}
	}

	// If --full, copy templates
	if full {
		fmt.Println("Copying templates for customization...")

		subFS, err := fs.Sub(assets.EmbeddedFS, "embedded")
		if err != nil {
			return fmt.Errorf("accessing embedded assets: %w", err)
		}

		// Copy template files
		for _, tmpl := range []string{"Dockerfile", "docker-compose.core.yml"} {
			data, err := fs.ReadFile(subFS, "templates/"+tmpl)
			if err != nil {
				continue
			}
			dst := filepath.Join(cspaceDir, tmpl)
			if err := os.WriteFile(dst, data, 0644); err != nil {
				fmt.Fprintf(os.Stderr, "warning: copying %s: %v\n", tmpl, err)
				continue
			}
			fmt.Printf("  Copied %s\n", tmpl)
		}

		// Copy agent playbooks
		agentsDir := filepath.Join(cspaceDir, "agents")
		os.MkdirAll(agentsDir, 0755)
		for _, agent := range []string{"implementer.md", "coordinator.md"} {
			data, err := fs.ReadFile(subFS, "agents/"+agent)
			if err != nil {
				continue
			}
			dst := filepath.Join(agentsDir, agent)
			if err := os.WriteFile(dst, data, 0644); err != nil {
				fmt.Fprintf(os.Stderr, "warning: copying %s: %v\n", agent, err)
				continue
			}
			fmt.Printf("  Copied agents/%s\n", agent)
		}

		fmt.Println("Templates copied to .cspace/ — edit them to customize.")
	}

	fmt.Println("")
	fmt.Println("Project initialized! Next steps:")
	fmt.Println("  1. Edit .cspace.json to configure ports, verify commands, etc.")
	fmt.Println("  2. (Optional) Add project services in .cspace/docker-compose.yml")
	fmt.Println("  3. (Optional) Add post-setup script in .cspace/post-setup.sh")
	fmt.Println("  4. Run 'cspace up' to launch an instance")

	return nil
}
