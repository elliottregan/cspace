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
	}
	wg.Wait()

	for _, p := range ps {
		if err := p.Close(ctx); err != nil {
			t.Errorf("Close: %v", err)
		}
	}
	// One last render after teardown must not panic and must still show the
	// child's last screen: an exited pane keeps what it painted.
	if got := stripANSI(ps[0].Render()); !strings.Contains(got, "tick") {
		t.Errorf("screen after close = %q, want the last frame", got)
	}
}
