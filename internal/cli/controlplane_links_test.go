package cli

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestLinkOpenerPassesWebURLAsLiteralArgument(t *testing.T) {
	for _, raw := range []string{
		"http://mercury.resume-redux.cspace.test:3000",
		"https://github.com/org/repo/pull/42",
		"https://example.com/?literal=$(touch%20/tmp/not-run)&value=`id`",
	} {
		t.Run(raw, func(t *testing.T) {
			var command string
			var argv []string
			opener := &linkOpener{run: func(_ context.Context, name string, args ...string) ([]byte, error) {
				command, argv = name, args
				return nil, nil
			}}
			if err := opener.OpenURL(context.Background(), raw); err != nil {
				t.Fatal(err)
			}
			if command != "open" || !reflect.DeepEqual(argv, []string{"--", raw}) {
				t.Fatalf("command = %q %q, want literal URL passed to open", command, argv)
			}
		})
	}
}

func TestLinkOpenerRejectsNonWebTargetsBeforeExecution(t *testing.T) {
	opener := &linkOpener{run: func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("invalid target reached host opener")
		return nil, nil
	}}
	for _, raw := range []string{"", "-a Calculator", "file:///tmp/a", "javascript:alert(1)", "mailto:hi@example.com", "//example.com", "http:///missing-host", "https://example.com/\narg"} {
		if err := opener.OpenURL(context.Background(), raw); err == nil {
			t.Errorf("OpenURL(%q) accepted invalid target", raw)
		}
	}
}

func TestLinkOpenerReportsHostFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		err  error
		want string
	}{
		{"missing", "", exec.ErrNotFound, "opening links needs macOS"},
		{"stderr", "No application can open this URL\n", errors.New("exit status 1"), "No application can open this URL"},
		{"exit", "", errors.New("exit status 1"), "exit status 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opener := &linkOpener{run: func(context.Context, string, ...string) ([]byte, error) {
				return []byte(tc.out), tc.err
			}}
			err := opener.OpenURL(context.Background(), "https://example.com")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLinkOpenerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	opener := &linkOpener{run: func(got context.Context, _ string, _ ...string) ([]byte, error) {
		if got != ctx {
			t.Fatal("context did not reach opener")
		}
		cancel()
		return nil, errors.New("signal: killed")
	}}
	if err := opener.OpenURL(ctx, "https://example.com"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	opener.run = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("pre-canceled request reached host opener")
		return nil, nil
	}
	if err := opener.OpenURL(ctx, "https://example.com"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
