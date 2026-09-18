package control

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elliottregan/cspace/internal/registry"
)

func TestUpRunsThisBinaryInTheProjectRoot(t *testing.T) {
	var gotDir, gotBin string
	var gotArgs []string
	c := New(Options{Containers: &fakeContainers{}, ProjectRoot: "/Users/x/proj"})
	c.executable = func() (string, error) { return "/usr/local/bin/cspace", nil }
	c.runCommand = func(_ context.Context, dir, bin string, args ...string) (string, error) {
		gotDir, gotBin, gotArgs = dir, bin, args
		return "sandbox issue-42 up\n", nil
	}

	if err := c.Up(context.Background(), "alpha", "issue-42"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if gotDir != "/Users/x/proj" || gotBin != "/usr/local/bin/cspace" {
		t.Errorf("ran %q in %q", gotBin, gotDir)
	}
	if want := []string{"up", "issue-42"}; !reflect.DeepEqual(gotArgs, want) {
		t.Errorf("args = %v, want %v", gotArgs, want)
	}
}

// The registry is the source of truth for a project's root: any sandbox of
// that project names the checkout it was booted from, which is what lets one
// dashboard boot sandboxes for projects the process was never started in.
func TestUpResolvesTheRootFromAnotherProjectsEntry(t *testing.T) {
	reg := &registry.Registry{Path: filepath.Join(t.TempDir(), "reg.json")}
	for _, e := range []registry.Entry{
		{Project: "beta", Name: "venus", ProjectRoot: "/Users/x/code/beta", State: "ready"},
		{Project: "alpha", Name: "mercury", ProjectRoot: "/Users/x/code/alpha", State: "ready"},
	} {
		if err := reg.Register(e); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	c := New(Options{Containers: &fakeContainers{}, Entries: reg,
		Project: "alpha", ProjectRoot: "/Users/x/code/alpha"})
	c.executable = func() (string, error) { return "/usr/local/bin/cspace", nil }
	var gotDir string
	c.runCommand = func(_ context.Context, dir, _ string, _ ...string) (string, error) {
		gotDir = dir
		return "", nil
	}

	if err := c.Up(context.Background(), "beta", "earth"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if gotDir != "/Users/x/code/beta" {
		t.Errorf("ran in %q, want beta's own root", gotDir)
	}
}

// A project with no entry that records a root can still be booted when it is
// the Client's own project — that is the launching process's ProjectRoot.
// Any other project has no answer, and saying so beats booting the wrong
// checkout.
func TestUpRefusesAnUnknownProject(t *testing.T) {
	reg := &registry.Registry{Path: filepath.Join(t.TempDir(), "reg.json")}
	c := New(Options{Containers: &fakeContainers{}, Entries: reg,
		Project: "alpha", ProjectRoot: "/Users/x/code/alpha"})
	c.executable = func() (string, error) { return "/usr/local/bin/cspace", nil }
	ran := false
	c.runCommand = func(context.Context, string, string, ...string) (string, error) {
		ran = true
		return "", nil
	}

	err := c.Up(context.Background(), "gamma", "issue-7")
	if !errors.Is(err, ErrNoProjectRoot) {
		t.Errorf("err = %v, want ErrNoProjectRoot", err)
	}
	if !strings.Contains(fmt.Sprint(err), "gamma") {
		t.Errorf("err = %v, want it to name the project", err)
	}
	if ran {
		t.Error("Up must not run anything without a resolved root")
	}
}

func TestUpSurfacesFailureWithOutput(t *testing.T) {
	c := New(Options{Containers: &fakeContainers{}, ProjectRoot: "/Users/x/proj"})
	c.executable = func() (string, error) { return "/usr/local/bin/cspace", nil }
	c.runCommand = func(context.Context, string, string, ...string) (string, error) {
		return "error: sandbox issue-42 already exists\n", errors.New("exit status 1")
	}
	err := c.Up(context.Background(), "alpha", "issue-42")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("err = %v, want the command's output carried through", err)
	}
}

func TestUpReportsAnUnresolvableBinary(t *testing.T) {
	c := New(Options{Containers: &fakeContainers{}, ProjectRoot: "/Users/x/proj"})
	c.executable = func() (string, error) { return "", errors.New("no such file") }
	if err := c.Up(context.Background(), "alpha", "issue-42"); err == nil {
		t.Error("want an error when the running binary cannot be resolved")
	}
}

// An empty ProjectRoot and no registry answer must fail before Up touches
// the executable or runCommand seams: exec.Cmd treats an empty Dir as
// "inherit this process's cwd", which is wrong for a long-lived
// control-plane caller with no cwd of its own that means anything.
func TestUpRequiresAProjectRoot(t *testing.T) {
	var ranExecutable, ranCommand bool
	c := New(Options{Containers: &fakeContainers{}})
	c.executable = func() (string, error) { ranExecutable = true; return "/usr/local/bin/cspace", nil }
	c.runCommand = func(context.Context, string, string, ...string) (string, error) {
		ranCommand = true
		return "", nil
	}
	err := c.Up(context.Background(), "alpha", "issue-42")
	if !errors.Is(err, ErrNoProjectRoot) {
		t.Errorf("err = %v, want ErrNoProjectRoot", err)
	}
	if ranExecutable || ranCommand {
		t.Error("Up must not touch the executable/runCommand seams without a root")
	}
}

// runHostCommand must not leave the child's stdin nil: exec.Cmd then wires it
// to the opened /dev/null *os.File, a character device, and cmd_up.go's
// isStdinTTY() treats any character device as a terminal — so a headless
// Client.Up caller would look interactive to the stale-image rebuild prompt
// and its EOF-default would silently trigger an unannounced rebuild instead
// of the documented warn-and-boot-stale-image path. This exercises the real
// function, not the Client.runCommand seam, so it would have caught that.
func TestRunHostCommandGivesTheChildAPipeStdin(t *testing.T) {
	out, err := runHostCommand(context.Background(), t.TempDir(), "sh", "-c",
		"if [ -c /dev/stdin ]; then echo chardev; else echo pipe; fi")
	if err != nil {
		t.Fatalf("runHostCommand: %v", err)
	}
	if got := strings.TrimSpace(out); got != "pipe" {
		t.Errorf("child's stdin = %q, want pipe (not a character device)", got)
	}
}

// The exit status of a failed command must survive back to the caller.
func TestRunHostCommandSurfacesExitStatus(t *testing.T) {
	_, err := runHostCommand(context.Background(), t.TempDir(), "sh", "-c", "exit 3")
	if err == nil {
		t.Fatal("want an error for a non-zero exit")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 3 {
		t.Errorf("err = %v, want an *exec.ExitError with exit code 3", err)
	}
}
