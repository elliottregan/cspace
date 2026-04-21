package config

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

//go:embed init_template.yaml
var initYAMLTemplate []byte

// EnsureProjectYAML writes a commented-out search.yaml template at the
// project root unless one already exists. Returns whether a file was
// written. Used by both the cspace search init and cspace-search init
// entry points.
func EnsureProjectYAML(projectRoot string) (bool, error) {
	path := filepath.Join(projectRoot, "search.yaml")
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.WriteFile(path, initYAMLTemplate, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// EnsureLefthookHooks runs `lefthook install` if lefthook is on PATH and
// the project has a lefthook.yml. Returns whether it installed. A missing
// lefthook binary or lefthook.yml is NOT an error — init runs on fresh
// projects that may not have a hook framework set up.
func EnsureLefthookHooks(projectRoot string) (bool, error) {
	if _, err := exec.LookPath("lefthook"); err != nil {
		return false, nil
	}
	if _, err := os.Stat(filepath.Join(projectRoot, "lefthook.yml")); err != nil {
		return false, nil
	}
	cmd := exec.Command("lefthook", "install")
	cmd.Dir = projectRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("lefthook install: %w (%s)", err, out)
	}
	return true, nil
}
