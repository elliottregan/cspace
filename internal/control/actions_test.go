package control

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type teardownCall struct {
	project   string
	sandbox   string
	wipeState bool
}

// fakeHost records what control asked the host layer to do and replays a
// canned outcome.
type fakeHost struct {
	teardownCalls []teardownCall
	teardownOut   string

	restartCalls []string
	restartErr   error
}

func (h *fakeHost) Teardown(_ context.Context, project, sandbox string, out io.Writer, wipeState bool) {
	h.teardownCalls = append(h.teardownCalls, teardownCall{project, sandbox, wipeState})
	_, _ = io.WriteString(out, h.teardownOut)
}

func (h *fakeHost) RestartBrowser(_ context.Context, project string) error {
	h.restartCalls = append(h.restartCalls, project)
	return h.restartErr
}

func TestDownReportsTeardownWarningsAsFailure(t *testing.T) {
	// teardownSandbox has no error return: its only failure signal is
	// "[cspace] warning: …" text on the writer. The dashboard has always
	// treated any warning as a failed action (cs-finding
	// 2026-07-20-tui-down-reports-benign-teardown-warnings-as-failure tracks
	// that this over-reports); the move must not change that.
	cases := []struct {
		name    string
		out     string
		wantErr string // "" means the call must succeed
	}{
		{"quiet teardown succeeds", "sandbox mercury down\n", ""},
		{"warning text fails the action", "[cspace] warning: remove volume cspace-alpha-mercury-data: busy\nsandbox mercury down\n", "remove volume"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &fakeHost{teardownOut: tc.out}
			c := New(Options{Containers: &fakeContainers{}, Host: h})
			err := c.Down(context.Background(), "alpha", "mercury")
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("want success, got %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("err = %v, want it to contain %q", err, tc.wantErr)
			}
			want := teardownCall{project: "alpha", sandbox: "mercury", wipeState: true}
			if len(h.teardownCalls) != 1 || h.teardownCalls[0] != want {
				t.Errorf("teardown calls = %+v, want exactly %+v", h.teardownCalls, want)
			}
		})
	}
}

func TestRestartBrowserDelegatesToHost(t *testing.T) {
	h := &fakeHost{}
	c := New(Options{Containers: &fakeContainers{}, Host: h})
	if err := c.RestartBrowser(context.Background(), "alpha"); err != nil {
		t.Fatalf("RestartBrowser: %v", err)
	}
	if len(h.restartCalls) != 1 || h.restartCalls[0] != "alpha" {
		t.Errorf("restart calls = %v, want [alpha]", h.restartCalls)
	}

	h.restartErr = errors.New("ladder gave up")
	if err := c.RestartBrowser(context.Background(), "alpha"); err == nil || !strings.Contains(err.Error(), "ladder gave up") {
		t.Errorf("err = %v, want the host's error carried through", err)
	}
}

func TestHostBackedActionsFailClosedWithoutAHost(t *testing.T) {
	c := New(Options{Containers: &fakeContainers{}})
	if err := c.Down(context.Background(), "alpha", "mercury"); !errors.Is(err, ErrNoHost) {
		t.Errorf("Down err = %v, want ErrNoHost", err)
	}
	if err := c.RestartBrowser(context.Background(), "alpha"); !errors.Is(err, ErrNoHost) {
		t.Errorf("RestartBrowser err = %v, want ErrNoHost", err)
	}
}
