package controlplane

import (
	"context"
	"errors"
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
