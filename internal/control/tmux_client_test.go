package control

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// The driver itself is covered by tmux_test.go (sandbox-side plan). These two
// cover only the Client's delegation: that it targets the right container and
// carries the driver's failure out.
func TestClientListClientsDelegatesToTheTmuxDriver(t *testing.T) {
	fc := &fakeContainers{execOut: "/dev/ttys004\n"}
	c := New(Options{Containers: fc})

	got, err := c.ListClients(context.Background(), "alpha", "mercury", SessionClaude)
	if err != nil {
		t.Fatalf("ListClients: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"/dev/ttys004"}) {
		t.Errorf("clients = %+v, want [/dev/ttys004]", got)
	}
	want := execCall{name: "cspace-alpha-mercury", cmd: []string{"tmux", "list-clients", "-t", SessionClaude, "-F", "#{client_tty}"}}
	if len(fc.execCalls) != 1 || !reflect.DeepEqual(fc.execCalls[0], want) {
		t.Errorf("exec calls = %+v, want exactly %+v", fc.execCalls, want)
	}
}

// A non-marker failure surfaces the driver's exit-status error unchanged.
func TestClientDetachClientSurfacesNonMarkerFailure(t *testing.T) {
	c := New(Options{Containers: &fakeContainers{execExit: 1, execStderr: "permission denied"}})
	err := c.DetachClient(context.Background(), "alpha", "mercury", "/dev/ttys009")
	if err == nil || !strings.Contains(err.Error(), "exit 1") || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("err = %v, want the driver's non-zero exit surfaced with its output", err)
	}
	if errors.Is(err, ErrClientGone) {
		t.Errorf("err = %v, should not classify as ErrClientGone", err)
	}
}

// A "client already gone" marker classifies as ErrClientGone so callers can
// treat it as a successful detach; Client.DetachClient must return the
// driver's error unwrapped for errors.Is to keep working.
func TestClientDetachClientClassifiesClientGone(t *testing.T) {
	c := New(Options{Containers: &fakeContainers{execExit: 1, execStderr: "can't find client /dev/ttys009"}})
	err := c.DetachClient(context.Background(), "alpha", "mercury", "/dev/ttys009")
	if !errors.Is(err, ErrClientGone) {
		t.Errorf("err = %v, want errors.Is(err, ErrClientGone)", err)
	}
}
