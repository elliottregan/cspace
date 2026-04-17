package cli

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/elliottregan/cspace/internal/config"
	"github.com/elliottregan/cspace/internal/docker"
)

// warnStyle renders version-mismatch warnings in yellow; dim to avoid
// drowning out the rest of the TUI header.
var warnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))

// checkImageVersion returns a human-readable warning string when the
// project's Docker image was built with a different cspace CLI version
// than the one invoking it now. Returns "" when the versions match, the
// image doesn't exist (first run), or the image predates version-stamping.
//
// Baked-in artifacts (supervisor.mjs, init scripts, agent playbooks) only
// update on `cspace rebuild`, so a host CLI upgrade without a rebuild
// silently runs old code inside the container. This surfaces that state
// so the user isn't debugging "unknown argument --foo" from the supervisor.
func checkImageVersion(c *config.Config) string {
	imgVersion := docker.ImageVersion(c.ImageName())
	if imgVersion == "" || imgVersion == Version {
		return ""
	}
	return fmt.Sprintf(
		"⚠  Container image was built with cspace %s — host CLI is %s. Run `cspace rebuild` to refresh baked-in scripts.",
		imgVersion, Version,
	)
}

// printImageVersionWarning writes the mismatch warning to stdout if there
// is one. Shared by the TUI header and the `cspace up` pre-launch hook.
func printImageVersionWarning(c *config.Config) {
	if msg := checkImageVersion(c); msg != "" {
		fmt.Println(warnStyle.Render(msg))
	}
}
