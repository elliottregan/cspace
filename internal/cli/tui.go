package cli

import (
	"fmt"
	"os"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/elliottregan/cspace/internal/instance"
	"github.com/elliottregan/cspace/internal/ports"
	"github.com/spf13/cobra"
)

// TUI styles
var (
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("99")).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("99")).
			PaddingLeft(1).
			PaddingRight(1)

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("78"))

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))
)

// TUI menu action constants
const (
	tuiActionNew        = "new"
	tuiActionConnect    = "connect"
	tuiActionSSH        = "ssh"
	tuiActionPorts      = "ports"
	tuiActionDown       = "down"
	tuiActionCoordinate = "coordinate"
	tuiActionDownAll    = "down-all"
	tuiActionList       = "list"
	tuiActionRebuild    = "rebuild"
)

// runTUI displays the interactive main menu when cspace is run with no args.
func runTUI(cmd *cobra.Command) error {
	// Display styled header
	projectDisplay := cfg.Project.Name
	if projectDisplay == "" {
		cwd, _ := os.Getwd()
		projectDisplay = cwd
	}
	fmt.Println(headerStyle.Render("cspace: " + projectDisplay))
	fmt.Println()

	// Query running instances
	details, _ := instance.GetInstanceDetails(cfg)
	hasInstances := len(details) > 0

	// Show running instances
	if hasInstances {
		for _, d := range details {
			fmt.Printf("  %s  %s  %s\n",
				lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("%-12s", d.Name)),
				dimStyle.Render(fmt.Sprintf("%-30s", d.Branch)),
				dimStyle.Render(d.Age),
			)
		}
		fmt.Println()
	}

	// Build menu options dynamically based on running instances
	var options []huh.Option[string]

	options = append(options, huh.NewOption("New instance        Launch a new Claude instance", tuiActionNew))
	if hasInstances {
		options = append(options,
			huh.NewOption("Connect             Open Claude in a running instance", tuiActionConnect),
			huh.NewOption("SSH                 Shell into an instance", tuiActionSSH),
			huh.NewOption("Ports               Show port mappings", tuiActionPorts),
			huh.NewOption("Down                Tear down an instance", tuiActionDown),
		)
	}
	options = append(options, huh.NewOption("Coordinate          Run a coordinator on a multi-task instruction", tuiActionCoordinate))
	if hasInstances {
		options = append(options,
			huh.NewOption("Down all            Tear down all instances", tuiActionDownAll),
			huh.NewOption("List                Show all running instances", tuiActionList),
		)
	}
	options = append(options, huh.NewOption("Rebuild             Rebuild the container image", tuiActionRebuild))

	var choice string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("What do you want to do?").
				Options(options...).
				Value(&choice),
		),
	)

	if err := form.Run(); err != nil {
		// User pressed Ctrl+C or Escape
		return nil
	}

	// Dispatch to the chosen action
	switch choice {
	case tuiActionNew:
		return tuiNew()
	case tuiActionConnect:
		return tuiConnect(cmd)
	case tuiActionSSH:
		name, err := tuiPickInstance("SSH into")
		if err != nil {
			return err
		}
		composeName := cfg.ComposeName(name)
		return instance.DcExecInteractive(composeName, "bash")
	case tuiActionPorts:
		name, err := tuiPickInstance("View ports for")
		if err != nil {
			return err
		}
		instance.ShowPorts(name, cfg)
		return nil
	case tuiActionDown:
		return tuiDown()
	case tuiActionCoordinate:
		return tuiCoordinate()
	case tuiActionDownAll:
		return runDownAll()
	case tuiActionList:
		return listProject()
	case tuiActionRebuild:
		return tuiRebuild()
	}

	return nil
}

// tuiNew prompts for an optional instance name and launches cspace up.
func tuiNew() error {
	var name string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Instance name").
				Description("Leave empty for auto planet name").
				Value(&name),
		),
	)

	if err := form.Run(); err != nil {
		return nil // User cancelled
	}

	// Reuse the up command logic
	if name == "" {
		var err error
		name, err = ports.NextPlanet(cfg.InstanceLabel())
		if err != nil {
			return err
		}
		fmt.Printf("Instance name: %s\n", name)
	}

	return runUpWithArgs(name, "", false, "", "")
}

// tuiConnect shows a sub-menu for connecting to a running instance.
func tuiConnect(cmd *cobra.Command) error {
	name, err := tuiPickInstance("Connect to")
	if err != nil {
		return err
	}

	var action string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title(fmt.Sprintf("Connect to %s", name)).
				Options(
					huh.NewOption("Claude       Open Claude Code", "claude"),
					huh.NewOption("SSH          Shell into container", "ssh"),
					huh.NewOption("Ports        Show port mappings", "ports"),
				).
				Value(&action),
		),
	)

	if err := form.Run(); err != nil {
		return nil // User cancelled
	}

	composeName := cfg.ComposeName(name)
	switch action {
	case "claude":
		return runUpWithArgs(name, "", false, "", "")
	case "ssh":
		return instance.DcExecInteractive(composeName, "bash")
	case "ports":
		instance.ShowPorts(name, cfg)
		return nil
	}

	return nil
}

// tuiDown prompts to pick an instance and tears it down after confirmation.
func tuiDown() error {
	name, err := tuiPickInstance("Tear down")
	if err != nil {
		return err
	}

	var confirm bool
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Tear down instance '%s' and delete its volumes?", name)).
				Value(&confirm),
		),
	)

	if err := form.Run(); err != nil {
		return nil
	}

	if !confirm {
		fmt.Println("Aborted.")
		return nil
	}

	composeName := cfg.ComposeName(name)
	if err := instance.RequireRunning(composeName, name); err != nil {
		return err
	}

	// Reuse compose down logic
	if err := runDownInstance(name); err != nil {
		return err
	}

	fmt.Println(statusStyle.Render(fmt.Sprintf("Instance '%s' removed.", name)))
	return nil
}

// tuiCoordinate prompts for multiline coordinator instructions.
func tuiCoordinate() error {
	var prompt string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewText().
				Title("What should the coordinator do?").
				Description("Enter your instructions (Ctrl+J for newline)").
				CharLimit(4000).
				Value(&prompt),
		),
	)

	if err := form.Run(); err != nil {
		return nil // User cancelled
	}

	if prompt == "" {
		return fmt.Errorf("prompt required")
	}

	var name string
	nameForm := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Instance name").
				Description("Leave empty to auto-generate").
				Value(&name),
		),
	)

	if err := nameForm.Run(); err != nil {
		return nil
	}

	// Build args for coordinate command
	args := []string{prompt}
	if name != "" {
		return runCoordinateWithArgs(prompt, "", name)
	}
	return runCoordinateWithArgs(args[0], "", "")
}

// tuiRebuild confirms and triggers a rebuild.
func tuiRebuild() error {
	var confirm bool
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Rebuild the container image?").
				Value(&confirm),
		),
	)

	if err := form.Run(); err != nil {
		return nil
	}

	if !confirm {
		return nil
	}

	return runRebuild(nil, nil)
}

// tuiPickInstance displays a selection menu of running instances and returns
// the chosen instance name.
func tuiPickInstance(action string) (string, error) {
	details, err := instance.GetInstanceDetails(cfg)
	if err != nil {
		return "", err
	}

	if len(details) == 0 {
		return "", fmt.Errorf("no running instances")
	}

	// If only one instance, return it directly
	if len(details) == 1 {
		fmt.Printf("Using instance: %s\n", details[0].Name)
		return details[0].Name, nil
	}

	var options []huh.Option[string]
	for _, d := range details {
		label := fmt.Sprintf("%-16s %-30s %s", d.Name, d.Branch, d.Age)
		options = append(options, huh.NewOption(label, d.Name))
	}

	var choice string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title(fmt.Sprintf("%s which instance?", action)).
				Options(options...).
				Value(&choice),
		),
	)

	if err := form.Run(); err != nil {
		return "", fmt.Errorf("cancelled")
	}

	return choice, nil
}

// isInteractive returns true if stdin is connected to a terminal.
func isInteractive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
