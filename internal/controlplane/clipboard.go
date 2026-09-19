package controlplane

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"
)

// ErrNoImage is what a Clipboard reports when the clipboard holds no image.
//
// It is not a failure. It is the branch the design names: "an empty or
// text-only clipboard falls back to a text paste" — so the dispatcher tests
// for it with errors.Is and goes on to Text, rather than putting it in the
// footer.
var ErrNoImage = errors.New("clipboard holds no image")

// Clipboard is the host's clipboard.
//
// Declared here and implemented in internal/cli, exactly like Data, Actor
// and PaneHost: this package stays free of the host it runs on, and the
// tests get a stub instead of a pasteboard.
//
// Neither method may be called from Update. Both shell out to a host
// binary, and a blocked exec on the UI goroutine is a frozen window. Every
// caller goes through a tea.Cmd with a bounded context.
type Clipboard interface {
	// Image writes the clipboard's image as a PNG somewhere the named
	// sandbox's pane can read it, and returns the path to TYPE INTO that
	// pane: the in-sandbox /sessions path for a sandbox pane, and a host
	// path when project and sandbox are both empty, which is how a host
	// shell asks (it has no sandbox and no bind mount).
	//
	// It returns ErrNoImage, possibly wrapped, when the clipboard holds no
	// image. Every other error is a real failure and reaches the footer.
	Image(ctx context.Context, project, sandbox string) (string, error)

	// Text is the clipboard's text, byte for byte, and empty when it holds
	// none.
	Text(ctx context.Context) (string, error)
}

// errNoClipboard is what both halves of nopClipboard answer with.
var errNoClipboard = errors.New("no clipboard configured")

// nopClipboard is what a Model built without one gets: the same fail-closed
// rule nopPaneHost applies, so a missing seam is an explained footer error
// rather than a nil dereference.
//
// Both methods fail, Text included. An empty string and no error would be
// indistinguishable from an empty clipboard, so a paste with no clipboard
// wired up would type nothing and report success — a silent no-op is the
// one outcome an operator cannot diagnose.
type nopClipboard struct{}

func (nopClipboard) Image(context.Context, string, string) (string, error) {
	return "", errNoClipboard
}

func (nopClipboard) Text(context.Context) (string, error) { return "", errNoClipboard }

// LabelPasteImage is what the footer calls an image paste while it is in
// flight and when it fails.
const LabelPasteImage = "paste image"

// clipboardTimeout bounds one image paste: the probe, the write, and the
// text read the fallback needs. osascript is fast, but it is a host binary
// that can be slow to start under load, and the UI must never wait on it.
const clipboardTimeout = 10 * time.Second

// pasteMsg carries one clipboard read's outcome back to the tab it was
// started for. text is what to type into the pane: the path of the written
// PNG, or the clipboard's own text when it held no image.
type pasteMsg struct {
	id   int
	text string
	err  error
}

// pasteImageCmd reads the clipboard for one tab, off the UI goroutine.
//
// The fallback is here rather than in a second round trip because it is one
// decision: "an empty or text-only clipboard falls back to a text paste"
// (the design). Two commands would also mean two deadlines and a window in
// which the tab could close between them.
//
// The tab's identity travels by value. The command outlives nothing, but it
// must not read the model it was built from — that Model is a copy, and by
// the time this runs the real one has moved on. It must not touch the pane
// either: the write happens in the pasteMsg arm, on the UI goroutine, which
// is the only place a *pane.Pane is driven from.
func (m Model) pasteImageCmd(t *tab) tea.Cmd {
	clip, id, project, sandbox := m.clip, t.id, t.project, t.sandbox
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), clipboardTimeout)
		defer cancel()

		path, err := clip.Image(ctx, project, sandbox)
		if err == nil {
			return pasteMsg{id: id, text: path}
		}
		if !errors.Is(err, ErrNoImage) {
			return pasteMsg{id: id, err: err}
		}
		text, err := clip.Text(ctx)
		if err != nil {
			return pasteMsg{id: id, err: err}
		}
		return pasteMsg{id: id, text: text}
	}
}
