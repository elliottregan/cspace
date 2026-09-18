package control

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
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

	if err := c.Up(context.Background(), "issue-42"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if gotDir != "/Users/x/proj" || gotBin != "/usr/local/bin/cspace" {
		t.Errorf("ran %q in %q", gotBin, gotDir)
	}
	if want := []string{"up", "issue-42"}; !reflect.DeepEqual(gotArgs, want) {
		t.Errorf("args = %v, want %v", gotArgs, want)
	}
}

func TestUpSurfacesFailureWithOutput(t *testing.T) {
	c := New(Options{Containers: &fakeContainers{}})
	c.executable = func() (string, error) { return "/usr/local/bin/cspace", nil }
	c.runCommand = func(context.Context, string, string, ...string) (string, error) {
		return "error: sandbox issue-42 already exists\n", errors.New("exit status 1")
	}
	err := c.Up(context.Background(), "issue-42")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("err = %v, want the command's output carried through", err)
	}
}

func TestUpReportsAnUnresolvableBinary(t *testing.T) {
	c := New(Options{Containers: &fakeContainers{}})
	c.executable = func() (string, error) { return "", errors.New("no such file") }
	if err := c.Up(context.Background(), "issue-42"); err == nil {
		t.Error("want an error when the running binary cannot be resolved")
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
