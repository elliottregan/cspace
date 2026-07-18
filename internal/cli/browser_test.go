package cli

import (
	"context"
	"net"
	"slices"
	"testing"
	"time"
)

// TestBrowserSidecarRunArgsSetsResourceCaps guards against the sidecar
// falling back to Apple Container's default 1 GiB, which OOM-wedged the
// shared browser under e2e load (cs-finding
// 2026-07-17-browser-sidecar-runs-on-default-1gib-and-ooms-under-e2e-load).
func TestBrowserSidecarRunArgsSetsResourceCaps(t *testing.T) {
	args := browserSidecarRunArgs("cspace-resume-redux-browser", "1.60.0")

	imageIdx := slices.Index(args, browserImage("1.60.0"))
	if imageIdx < 0 {
		t.Fatalf("image ref missing from args: %v", args)
	}

	for flag, want := range map[string]string{
		"--memory": "4096MiB",
		"--cpus":   "4",
	} {
		i := slices.Index(args, flag)
		if i < 0 {
			t.Fatalf("%s flag missing from args: %v", flag, args)
		}
		if i > imageIdx {
			t.Errorf("%s must precede the image ref (flag at %d, image at %d)", flag, i, imageIdx)
		}
		if got := args[i+1]; got != want {
			t.Errorf("%s = %q, want %q", flag, got, want)
		}
	}

	// Extraction must preserve the existing invocation shape.
	for _, want := range []string{"run", "-d", "--name", "cspace-resume-redux-browser",
		"--label", "cspace.playwright-version=1.60.0", "--dns"} {
		if !slices.Contains(args, want) {
			t.Errorf("expected %q in args: %v", want, args)
		}
	}
}

func TestBrowserSingletonName(t *testing.T) {
	if got := browserSingletonName("resume-redux"); got != "cspace-resume-redux-browser" {
		t.Errorf("got %q, want cspace-resume-redux-browser", got)
	}
}

// startFakeWS returns addr of a listener that, per connection, sends
// `response` after reading the request (empty response = accept then hang).
func startFakeWS(t *testing.T, response string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				buf := make([]byte, 1024)
				_, _ = c.Read(buf)
				if response != "" {
					_, _ = c.Write([]byte(response))
				}
				// hang until test cleanup closes the listener
				time.Sleep(10 * time.Second)
				_ = c.Close()
			}(c)
		}
	}()
	return l.Addr().String()
}

func TestWaitForRunServerWS(t *testing.T) {
	ok := startFakeWS(t, "HTTP/1.1 101 Switching Protocols\r\n\r\n")
	if err := waitForRunServerWS(context.Background(), ok, 3*time.Second); err != nil {
		t.Errorf("101 fixture: want nil, got %v", err)
	}
	bad := startFakeWS(t, "HTTP/1.1 400 Bad Request\r\n\r\n")
	if err := waitForRunServerWS(context.Background(), bad, 2*time.Second); err == nil {
		t.Error("400 fixture: want error, got nil")
	}
	hang := startFakeWS(t, "") // accepts TCP, never answers — the incident shape
	if err := waitForRunServerWS(context.Background(), hang, 2*time.Second); err == nil {
		t.Error("hang fixture: want error, got nil")
	}
}

func TestWorkspaceFriendlyHost(t *testing.T) {
	cases := map[string][2]string{
		"mercury.resume-redux.cspace.test": {"mercury", "resume-redux"},
		"venus.demo.cspace.test":           {"Venus", "Demo"}, // lowercased
	}
	for want, in := range cases {
		if got := workspaceFriendlyHost(in[0], in[1]); got != want {
			t.Errorf("workspaceFriendlyHost(%q,%q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}
