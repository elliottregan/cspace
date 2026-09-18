package control

import (
	"context"
	"errors"
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
