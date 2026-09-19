package pane

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestPanesUnderConcurrentUse is the port of the design spike's race driver:
// several panes, input and resizes and renders interleaved, then teardown —
// which is the shape that produced every data race the spike found. It is an
// ordinary test; `make test-race` is what makes it mean something.
func TestPanesUnderConcurrentUse(t *testing.T) {
	const panes = 3
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var ps []*Pane
	for i := 0; i < panes; i++ {
		ps = append(ps, openTestPane(t, `while :; do printf 'tick '; sleep 0.05; done`, 60, 10))
	}

	var wg sync.WaitGroup
	for _, p := range ps {
		p := p
		wg.Add(1)
		go func() { // input
			defer wg.Done()
			for i := 0; i < 50; i++ {
				p.SendKey(KeyEvent{Code: 'a', Text: "a"})
				p.SendKey(KeyEvent{Code: KeyUp, Mod: ModCtrl})
				p.Paste("pasted text")
				time.Sleep(time.Millisecond)
			}
		}()
		wg.Add(1)
		go func() { // resize
			defer wg.Done()
			for i := 0; i < 20; i++ {
				_ = p.Resize(60+i%5, 10+i%3)
				time.Sleep(2 * time.Millisecond)
			}
		}()
		wg.Add(1)
		go func() { // the UI goroutine's job
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_ = p.Render()
				_, _ = p.Cursor()
				_ = p.ScrollbackView(i%3, 10)
				time.Sleep(time.Millisecond)
			}
		}()
		wg.Add(1)
		go func() { // Close arriving mid-flight, from its own goroutine (as
			// in the control plane, where it runs on the UI goroutine while
			// input/resize/render keep coming from elsewhere) is the
			// interleaving the handshake exists for. Closing only after
			// every other goroutine has already stopped, as this used to,
			// never let -race see Close race a live SendKey/Render/Resize
			// at all.
			defer wg.Done()
			time.Sleep(20 * time.Millisecond)
			if err := p.Close(ctx); err != nil {
				t.Errorf("Close: %v", err)
			}
		}()
	}
	wg.Wait()

	// Every input/resize/render call above ran, and kept running, against a
	// pane that Close was concurrently tearing down or had already torn
	// down; none of them may block or panic doing it.

	// One last render after teardown must not panic and must still show the
	// child's last screen: an exited pane keeps what it painted.
	if got := stripANSI(ps[0].Render()); !strings.Contains(got, "tick") {
		t.Errorf("screen after close = %q, want the last frame", got)
	}
}
