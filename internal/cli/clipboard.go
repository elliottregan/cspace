package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/controlplane"
)

// osaClipboard is controlplane.Clipboard over the macOS pasteboard.
//
// It lives here for the reason every other seam does: internal/controlplane
// must not know what host it is running on, and internal/control is pure Go
// with no business shelling out to AppleScript. Nothing is compiled in —
// osascript and pbpaste are looked up at call time, and a host without them
// gets a footer error rather than a build that will not link.
type osaClipboard struct {
	home string
	// now names the file. A seam because a test cannot assert on a path it
	// cannot predict.
	now func() time.Time
}

var _ controlplane.Clipboard = (*osaClipboard)(nil)

func newClipboard(home string) *osaClipboard {
	return &osaClipboard{home: home, now: time.Now}
}

// pasteStamp is the filename's timestamp, to the millisecond: two images
// pasted in the same second are two files, and the name still sorts.
const pasteStamp = "20060102-150405.000"

// Image probes the clipboard, writes its PNG, and reports the path to type.
//
// The probe is a separate osascript run rather than an attempt-and-catch,
// because "the clipboard holds no image" and "AppleScript failed" arrive
// through the same non-zero exit and must not be confused: one is the
// design's text-paste fallback and the other belongs in the footer.
func (c *osaClipboard) Image(ctx context.Context, project, sandbox string) (string, error) {
	has, err := c.hasImage(ctx)
	if err != nil {
		return "", err
	}
	if !has {
		return "", controlplane.ErrNoImage
	}

	hostDir, paneDir := c.pasteDirs(project, sandbox)
	// 0700: these are whatever was on the person's clipboard, under their
	// home directory, and nothing but this process and the sandbox's own
	// bind mount has business reading them.
	if err := os.MkdirAll(hostDir, 0o700); err != nil {
		return "", fmt.Errorf("create paste dir: %w", err)
	}
	name := c.now().Format(pasteStamp) + ".png"
	if err := c.writePNG(ctx, filepath.Join(hostDir, name)); err != nil {
		return "", err
	}
	return filepath.Join(paneDir, name), nil
}

// pasteDirs is where the file goes on the host, and the directory the pane
// will see it in.
//
// A sandbox pane reads it through the /sessions bind mount — cmd_up.go
// mounts ~/.cspace/sessions/<project>/<sandbox> there — so the path typed
// into it is /sessions/paste/<name> however the host spells the directory.
// A host shell has no mount and belongs to no sandbox: its images go to
// ~/.cspace/paste and the host path is what gets typed.
func (c *osaClipboard) pasteDirs(project, sandbox string) (hostDir, paneDir string) {
	if project == "" && sandbox == "" {
		d := filepath.Join(c.home, ".cspace", "paste")
		return d, d
	}
	return filepath.Join(control.SessionDir(c.home, project, sandbox), "paste"), "/sessions/paste"
}

// hasImage is the probe. `clipboard info` lists one entry per flavour the
// pasteboard can supply, and macOS offers «class PNGf» for anything it can
// hand over as an image.
//
// Measured on this host 2026-09-19:
//
//	an image: «class PNGf», 73, «class AVIF», 375, «class 8BPS», 3342, …
//	text:     «class utf8», 9, «class ut16», 20, string, 9, Unicode text, 18
//	empty:    «class utf8», 0, «class ut16», 2, string, 0, Unicode text, 0
func (c *osaClipboard) hasImage(ctx context.Context) (bool, error) {
	out, err := osascript(ctx, []string{"clipboard info"})
	if err != nil {
		return false, err
	}
	return strings.Contains(out, "«class PNGf»"), nil
}

// writePNG asks AppleScript for the clipboard's PNG flavour and writes it
// to path. The design's decision row measured the round trip byte-identical,
// and so did a re-measurement on 2026-09-19.
//
// The path travels as an argv item read by `on run argv`, never
// interpolated into the script: a sandbox name is operator-supplied and
// AppleScript string escaping is not something to reinvent. `set eof f to 0`
// truncates first, so a retry onto an existing name cannot leave a tail of
// the previous image behind, and the write sits in a `try` whose handler
// closes the file and then RE-RAISES. A bare `try` is not enough: measured
// on 2026-09-19, `osascript -e try -e 'error "boom"' -e 'end try'` exits 0,
// so a failed write would come back as success and leader `v` would type
// the path of a zero-byte file into a pane.
func (c *osaClipboard) writePNG(ctx context.Context, path string) error {
	_, err := osascript(ctx, []string{
		"on run argv",
		"set p to item 1 of argv",
		"set d to (the clipboard as «class PNGf»)",
		"set f to open for access (POSIX file p) with write permission",
		"try",
		"set eof f to 0",
		"write d to f",
		"close access f",
		"on error e",
		"try",
		"close access f",
		"end try",
		"error e",
		"end try",
		"end run",
	}, path)
	if err != nil {
		return fmt.Errorf("write the clipboard's png: %w", err)
	}
	return nil
}

// Text is the clipboard's text, byte for byte.
//
// pbpaste, not osascript. `osascript -e 'the clipboard as «class utf8»'`
// prints the script's RESULT, which arrives with a newline appended —
// measured 2026-09-19, "line1\nline2" came back as "line1\nline2\n" — and a
// pasted diff that gained a trailing newline is a pasted diff that was
// changed on the way through. pbpaste writes the pasteboard's bytes and
// nothing else.
func (c *osaClipboard) Text(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "pbpaste").Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", errors.New("pbpaste not found: reading the clipboard needs macOS")
		}
		return "", fmt.Errorf("pbpaste: %w", err)
	}
	return string(out), nil
}

// osascript runs a script given one line per -e, with args after it, where
// an `on run argv` handler can read them. It returns stdout, and folds
// stderr into the error: osascript reports a failed clipboard coercion
// there ("Can't make some data into the expected type. (-1700)") and a
// naked exit status would tell the operator nothing.
func osascript(ctx context.Context, script []string, args ...string) (string, error) {
	argv := make([]string, 0, len(script)*2+len(args))
	for _, line := range script {
		argv = append(argv, "-e", line)
	}
	argv = append(argv, args...)

	cmd := exec.CommandContext(ctx, "osascript", argv...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", errors.New("osascript not found: image paste needs macOS")
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("osascript: %s", msg)
		}
		return "", fmt.Errorf("osascript: %w", err)
	}
	return string(out), nil
}
