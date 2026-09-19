package cli

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elliottregan/cspace/internal/controlplane"
)

// These run against the real pasteboard, because a stubbed osascript would
// only prove that the stub works. They skip rather than fail where
// osascript is missing: everything else in this package builds and tests on
// any host, and cspace itself is macOS-only for other reasons.
func requireOsascript(t *testing.T) {
	t.Helper()
	if os.Getenv("CSPACE_CLIPBOARD_TESTS") == "" {
		// Opt-in, because these overwrite the REAL pasteboard and
		// keepClipboard can only put text back. `make check` runs
		// `go test ./...`, and `scripts/release.sh` runs `make check` —
		// so without this gate, cutting a release destroys whatever
		// image the developer had on their clipboard.
		t.Skip("set CSPACE_CLIPBOARD_TESTS=1: these tests overwrite the real pasteboard and cannot restore an image")
	}
	for _, bin := range []string{"osascript", "pbcopy", "pbpaste"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s is not on PATH; the clipboard tests are macOS-only", bin)
		}
	}
}

// keepClipboard saves the clipboard's TEXT and puts it back afterwards.
//
// It cannot save an image. pbcopy writes text and nothing else, and there
// is no supported way to restore an arbitrary pasteboard flavour from a
// shell — so if the clipboard held a picture when this test started, that
// picture is gone once it finishes. That is a known and accepted cost of
// testing against the real pasteboard; there is no second pasteboard to
// use instead.
func keepClipboard(t *testing.T) {
	t.Helper()
	saved, err := exec.Command("pbpaste").Output()
	if err != nil {
		t.Fatalf("pbpaste: %v", err)
	}
	t.Cleanup(func() {
		c := exec.Command("pbcopy")
		c.Stdin = bytes.NewReader(saved)
		_ = c.Run()
	})
}

// putPNG writes a tiny PNG and puts it on the clipboard as «class PNGf».
func putPNG(t *testing.T, dir string) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	path := filepath.Join(dir, "src.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	out, err := exec.Command("osascript",
		"-e", "on run argv",
		"-e", "set the clipboard to (read (POSIX file (item 1 of argv)) as «class PNGf»)",
		"-e", "end run",
		path).CombinedOutput()
	if err != nil {
		t.Fatalf("put the png on the clipboard: %v: %s", err, out)
	}
	return path
}

func TestClipboardWritesAPNGWhereTheSandboxPaneCanReadIt(t *testing.T) {
	requireOsascript(t)
	keepClipboard(t)
	putPNG(t, t.TempDir())

	home := t.TempDir()
	c := newClipboard(home)
	c.now = func() time.Time { return time.Date(2026, 9, 19, 14, 30, 1, 123_000_000, time.UTC) }

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	typed, _, err := c.Image(ctx, "alpha", "mercury")
	if err != nil {
		t.Fatalf("Image: %v", err)
	}
	// The path typed into the pane is the one the pane can open: cmd_up.go
	// bind-mounts ~/.cspace/sessions/<project>/<sandbox> at /sessions.
	if want := "/sessions/paste/20260919-143001.123.png"; typed != want {
		t.Errorf("typed path = %q, want %q", typed, want)
	}

	hostPath := filepath.Join(home, ".cspace", "sessions", "alpha", "mercury",
		"paste", "20260919-143001.123.png")
	// Byte-identity with the source is what the design measured, but it is
	// the pasteboard's promise and not this code's — so the assertion is
	// that a real PNG of the right size came out the other end.
	f, err := os.Open(hostPath)
	if err != nil {
		t.Fatalf("open the written png: %v", err)
	}
	defer func() { _ = f.Close() }()
	cfg, err := png.DecodeConfig(f)
	if err != nil {
		t.Fatalf("the written file is not a png: %v", err)
	}
	if cfg.Width != 2 || cfg.Height != 2 {
		t.Errorf("png is %dx%d, want 2x2", cfg.Width, cfg.Height)
	}

	info, err := os.Stat(filepath.Dir(hostPath))
	if err != nil {
		t.Fatalf("stat the paste dir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != fs.FileMode(0o700) {
		t.Errorf("paste dir mode = %v, want 0700", perm)
	}
}

func TestClipboardWritesAHostShellsImageOutsideAnySandbox(t *testing.T) {
	requireOsascript(t)
	keepClipboard(t)
	putPNG(t, t.TempDir())

	home := t.TempDir()
	c := newClipboard(home)
	c.now = func() time.Time { return time.Date(2026, 9, 19, 14, 30, 1, 0, time.UTC) }

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	typed, _, err := c.Image(ctx, "", "")
	if err != nil {
		t.Fatalf("Image: %v", err)
	}
	want := filepath.Join(home, ".cspace", "paste", "20260919-143001.000.png")
	if typed != want {
		t.Errorf("typed path = %q, want the host path %q", typed, want)
	}
	if _, err := os.Stat(typed); err != nil {
		t.Errorf("the host path does not exist: %v", err)
	}
}

func TestClipboardReportsErrNoImageForATextClipboard(t *testing.T) {
	requireOsascript(t)
	keepClipboard(t)
	c := exec.Command("pbcopy")
	c.Stdin = strings.NewReader("line1\nline2")
	if err := c.Run(); err != nil {
		t.Fatalf("pbcopy: %v", err)
	}

	clip := newClipboard(t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if _, _, err := clip.Image(ctx, "alpha", "mercury"); !errors.Is(err, controlplane.ErrNoImage) {
		t.Errorf("Image err = %v, want ErrNoImage", err)
	}
	text, err := clip.Text(ctx)
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	// Byte for byte: a pasted diff that gained a trailing newline is a
	// pasted diff that was changed on the way through.
	if text != "line1\nline2" {
		t.Errorf("Text = %q, want %q", text, "line1\nline2")
	}
}

func TestClipboardImageLeavesNoFileWhenThereIsNoImage(t *testing.T) {
	requireOsascript(t)
	keepClipboard(t)
	c := exec.Command("pbcopy")
	c.Stdin = strings.NewReader("no picture here")
	if err := c.Run(); err != nil {
		t.Fatalf("pbcopy: %v", err)
	}

	home := t.TempDir()
	clip := newClipboard(home)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, _, _ = clip.Image(ctx, "alpha", "mercury")
	dir := filepath.Join(home, ".cspace", "sessions", "alpha", "mercury", "paste")
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		t.Errorf("a failed probe left %d file(s) behind in %s", len(entries), dir)
	}
}

// --- the error paths, ungated -------------------------------------------
//
// Everything above needs a real pasteboard. What follows is the Go around
// it — the path mapping, the directory, and every way the two host binaries
// can fail — and it runs on every `make check`, because none of it touches
// the clipboard: PATH points at a directory the test wrote, so "osascript"
// is whatever the case needs it to be, including absent.

// stubPATH replaces PATH with an empty directory of this test's own, so an
// exec lookup finds only what stubBin puts there. t.Setenv puts it back.
func stubPATH(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	return dir
}

// stubBin writes an executable named name into dir. The body gets a PATH of
// its own: it inherits one with nothing but dir on it, and a stub that
// cannot find /bin/sleep is a stub that fails for the wrong reason.
func stubBin(t *testing.T, dir, name, body string) {
	t.Helper()
	script := "#!/bin/sh\nPATH=/usr/bin:/bin\nexport PATH\n" + body
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
		t.Fatalf("write the %s stub: %v", name, err)
	}
}

// stubImageOsascript answers the probe with a PNG flavour and, for any
// other script, writes the last argv item — which is the path writePNG
// passes after its -e lines.
const stubImageOsascript = `case "$*" in
  *"clipboard info"*) echo "«class PNGf», 73, «class 8BPS», 3342" ;;
  *) for a in "$@"; do last=$a; done; printf 'stub-png' > "$last" ;;
esac
`

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestClipboardMapsTheSandboxPasteDirWithoutAPasteboard(t *testing.T) {
	dir := stubPATH(t)
	stubBin(t, dir, "osascript", stubImageOsascript)

	home := t.TempDir()
	c := newClipboard(home)
	c.now = func() time.Time { return time.Date(2026, 9, 19, 14, 30, 1, 123_000_000, time.UTC) }

	typed, host, err := c.Image(testCtx(t), "alpha", "mercury")
	if err != nil {
		t.Fatalf("Image: %v", err)
	}
	if want := "/sessions/paste/20260919-143001.123.png"; typed != want {
		t.Errorf("typed path = %q, want %q", typed, want)
	}
	hostPath := filepath.Join(home, ".cspace", "sessions", "alpha", "mercury",
		"paste", "20260919-143001.123.png")
	if _, err := os.Stat(hostPath); err != nil {
		t.Fatalf("nothing was written where the pane reads: %v", err)
	}
	// The second return is the same file as THIS process can reach it —
	// not the pane's view of it. It is what the dashboard unlinks when a
	// paste never reaches a pane, so a wrong value here would either
	// delete nothing or, worse, delete by a path that means something
	// else on the host.
	if host != hostPath {
		t.Errorf("host path = %q, want %q", host, hostPath)
	}
	info, err := os.Stat(filepath.Dir(hostPath))
	if err != nil {
		t.Fatalf("stat the paste dir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != fs.FileMode(0o700) {
		t.Errorf("paste dir mode = %v, want 0700", perm)
	}
}

func TestClipboardImageReportsAnUnwritablePasteDir(t *testing.T) {
	dir := stubPATH(t)
	stubBin(t, dir, "osascript", stubImageOsascript)

	home := t.TempDir()
	// A file where .cspace has to be a directory: MkdirAll cannot pass
	// through it. The failure has to reach the caller — swallowing it and
	// going on to the write would type the path of a file that was never
	// created into a pane.
	if err := os.WriteFile(filepath.Join(home, ".cspace"), nil, 0o600); err != nil {
		t.Fatalf("write the blocking file: %v", err)
	}

	typed, _, err := newClipboard(home).Image(testCtx(t), "alpha", "mercury")
	if err == nil {
		t.Fatalf("Image returned %q and no error for a directory it could not create", typed)
	}
	if !strings.Contains(err.Error(), "create paste dir") {
		t.Errorf("err = %v, want it to name the directory it could not create", err)
	}
	if errors.Is(err, controlplane.ErrNoImage) {
		t.Error("an unwritable directory is a failure for the footer, not the text fallback")
	}
}

func TestClipboardImageNamesTheMissingBinary(t *testing.T) {
	stubPATH(t) // empty: a host with no osascript at all

	_, _, err := newClipboard(t.TempDir()).Image(testCtx(t), "alpha", "mercury")
	if err == nil {
		t.Fatal("Image must fail where osascript does not exist")
	}
	if !strings.Contains(err.Error(), "osascript not found: image paste needs macOS") {
		t.Errorf("err = %v, want it to name the missing binary", err)
	}
	// A probe that could not run is not a clipboard without an image: the
	// text fallback would silently paste the wrong thing.
	if errors.Is(err, controlplane.ErrNoImage) {
		t.Error("a failed probe reported ErrNoImage")
	}
}

func TestClipboardTextNamesTheMissingBinary(t *testing.T) {
	stubPATH(t) // empty: no pbpaste

	text, err := newClipboard(t.TempDir()).Text(testCtx(t))
	if err == nil {
		t.Fatalf("Text returned %q and no error where pbpaste does not exist", text)
	}
	if !strings.Contains(err.Error(), "pbpaste not found: reading the clipboard needs macOS") {
		t.Errorf("err = %v, want it to name the missing binary", err)
	}
}

func TestOsascriptFoldsTheScriptsOwnErrorTextIntoTheError(t *testing.T) {
	if _, err := exec.LookPath("osascript"); err != nil {
		t.Skipf("osascript is not on PATH: %v", err)
	}
	// A real -1700 — the number a failed clipboard coercion raises —
	// without touching the pasteboard to get one. An operator shown a bare
	// "exit status 1" learns nothing about what went wrong.
	_, err := osascript(testCtx(t),
		[]string{`error "Can't make some data into the expected type." number -1700`})
	if err == nil {
		t.Fatal("a script that raises an error must come back as one")
	}
	if !strings.Contains(err.Error(), "(-1700)") {
		t.Errorf("err = %v, want the script's own message and number", err)
	}
}

func TestOsascriptStopsAtTheContextsDeadline(t *testing.T) {
	dir := stubPATH(t)
	stubBin(t, dir, "osascript", "exec sleep 5\n")

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := osascript(ctx, []string{"clipboard info"})
	if err == nil {
		t.Fatal("a script killed at the deadline must come back as an error")
	}
	// "signal: killed" names the symptom, not the cause: the footer cannot
	// explain it and no caller can test for it.
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want it to carry context.DeadlineExceeded", err)
	}
	// The deadline is what the UI depends on: this call is made from a
	// tea.Cmd, and an osascript that ignores its context is a paste that
	// never finishes.
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("osascript ran for %s; the deadline never reached the process", elapsed)
	}
}

func TestPasteDirsPutEachCaseWhereItsPaneCanReadIt(t *testing.T) {
	c := newClipboard("/home/x")
	sessions := filepath.Join("/home/x", ".cspace", "sessions")
	for _, tc := range []struct {
		name             string
		project, sandbox string
		hostDir, paneDir string
	}{
		{"a sandbox pane reads through the /sessions mount",
			"alpha", "mercury",
			filepath.Join(sessions, "alpha", "mercury", "paste"), "/sessions/paste"},
		{"a host shell has no mount and no sandbox",
			"", "",
			filepath.Join("/home/x", ".cspace", "paste"), filepath.Join("/home/x", ".cspace", "paste")},
		// Only *both* empty means "host shell". A row with half a name is
		// still a sandbox, and handing its pane a host path it cannot open
		// would be a paste that silently points at nothing.
		{"half a name is still a sandbox",
			"", "mercury",
			filepath.Join(sessions, "mercury", "paste"), "/sessions/paste"},
		{"half a name is still a sandbox, the other half",
			"alpha", "",
			filepath.Join(sessions, "alpha", "paste"), "/sessions/paste"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hostDir, paneDir := c.pasteDirs(tc.project, tc.sandbox)
			if hostDir != tc.hostDir {
				t.Errorf("host dir = %q, want %q", hostDir, tc.hostDir)
			}
			if paneDir != tc.paneDir {
				t.Errorf("pane dir = %q, want %q", paneDir, tc.paneDir)
			}
		})
	}
}

func TestClipboardImageRejectsANameItWouldJoinIntoAPath(t *testing.T) {
	// No stub PATH: validation has to come before anything is probed,
	// created or written, so these never reach a binary at all.
	for _, tc := range []struct {
		name             string
		project, sandbox string
		wantField        string
	}{
		{"traversal in the sandbox", "alpha", "../../../../tmp/x", "sandbox name"},
		{"traversal in the project", "../../../../tmp", "mercury", "project name"},
		{"a bare .. as the project", "..", "mercury", "project name"},
		{"a slash in the sandbox", "alpha", "a/b", "sandbox name"},
		{"a slash in the project", "a/b", "mercury", "project name"},
		{"a dotted sandbox", "alpha", "mercury.two", "sandbox name"},
		{"the reserved browser name", "alpha", "browser", "sandbox name"},
		{"an empty half", "", "mercury", "project name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			_, _, err := newClipboard(home).Image(testCtx(t), tc.project, tc.sandbox)
			if err == nil {
				t.Fatalf("Image(%q, %q) was accepted", tc.project, tc.sandbox)
			}
			if !strings.Contains(err.Error(), tc.wantField) {
				t.Errorf("err = %v, want it to name the %s", err, tc.wantField)
			}
			// Nothing may be created on the way to that error.
			if entries, err := os.ReadDir(filepath.Join(home, ".cspace")); err == nil && len(entries) > 0 {
				t.Errorf("a rejected name still created %d entry(ies) under .cspace", len(entries))
			}
		})
	}
}

// TestClipboardImageAcceptsOrdinaryProjectNames pins the fix for the
// project half being over-validated as if it were a sandbox name: a
// project name is filepath.Base of a checkout (config.go) and cspace
// validates it nowhere else, so a dot, a space, or the sandbox-only
// "browser" reservation must not turn image paste off for that project.
func TestClipboardImageAcceptsOrdinaryProjectNames(t *testing.T) {
	dir := stubPATH(t)
	stubBin(t, dir, "osascript", stubImageOsascript)

	// None of these is a legal *sandbox* name — sandboxNamePattern rejects
	// the dot and the space, and validateSandboxName reserves "browser" —
	// but every one is an ordinary project name.
	for _, project := range []string{"next.js", "site.com", "My Project", "browser"} {
		t.Run(project, func(t *testing.T) {
			home := t.TempDir()
			paneDir, hostDir, err := newClipboard(home).Image(testCtx(t), project, "mercury")
			if err != nil {
				t.Fatalf("Image(%q, mercury) = %v, want it to succeed", project, err)
			}
			if !strings.HasPrefix(paneDir, "/sessions/paste/") {
				t.Errorf("pane path = %q, want it under /sessions/paste/", paneDir)
			}
			if _, err := os.Stat(hostDir); err != nil {
				t.Errorf("host path %q was not written: %v", hostDir, err)
			}
		})
	}
}

func TestClipboardImageRemovesTheFileWhenTheWriteFails(t *testing.T) {
	dir := stubPATH(t)
	// An osascript that gets as far as creating the file and then fails,
	// which is the shape of a write that dies after `open for access`.
	stubBin(t, dir, "osascript", `case "$*" in
  *"clipboard info"*) echo "«class PNGf», 73" ;;
  *) for a in "$@"; do last=$a; done; : > "$last"; echo "simulated write failure" >&2; exit 1 ;;
esac
`)

	home := t.TempDir()
	typed, _, err := newClipboard(home).Image(testCtx(t), "alpha", "mercury")
	if err == nil {
		t.Fatalf("Image returned %q and no error for a write that failed", typed)
	}
	if !strings.Contains(err.Error(), "simulated write failure") {
		t.Errorf("err = %v, want the script's own stderr", err)
	}
	pasteDir := filepath.Join(home, ".cspace", "sessions", "alpha", "mercury", "paste")
	if entries, err := os.ReadDir(pasteDir); err == nil && len(entries) > 0 {
		t.Errorf("a failed write left %d file(s) behind in %s — an agent globbing "+
			"that directory would find a zero-byte png", len(entries), pasteDir)
	}
}

// The AppleScript body cannot be exercised hermetically: making a real
// write fail after `open for access` needs an image on the pasteboard and a
// disk that refuses the write, and the PATH stubs above never read the
// script at all. So its three load-bearing orderings are asserted
// structurally instead — each one was measured, and each one silently
// survives its own deletion without this test.
func TestWritePNGScriptKeepsTheOrderingsItsCommentsClaim(t *testing.T) {
	idx := func(want string) int {
		for i, line := range writePNGScript {
			if line == want {
				return i
			}
		}
		t.Fatalf("writePNGScript has no %q line:\n%s", want, strings.Join(writePNGScript, "\n"))
		return -1
	}

	// Coerce before opening. The one realistic mid-flight failure is the
	// clipboard losing its image between the probe and the write; raising
	// -1700 before any file exists is what keeps paste/ clean.
	if coerce, open := idx("set d to (the clipboard as «class PNGf»)"),
		idx("set f to open for access (POSIX file p) with write permission"); coerce > open {
		t.Error("the script opens the file before it coerces the clipboard: " +
			"a clipboard that lost its image mid-flight would leave an empty file")
	}

	// Truncate before writing, so a retry onto a name that already exists
	// cannot leave a tail of the previous image spliced onto the new one.
	if eof, write := idx("set eof f to 0"), idx("write d to f"); eof > write {
		t.Error("the script writes before it truncates")
	}

	// Re-raise. Measured 2026-09-19: `osascript -e try -e 'error "boom"'
	// -e 'end try'` exits 0, so a handler that only closes the file turns a
	// failed write into a success — and Image would then hand a pane the
	// path of a zero-byte png.
	reRaised := false
	for _, line := range writePNGScript[idx("on error e"):] {
		if line == "error e" {
			reRaised = true
			break
		}
	}
	if !reRaised {
		t.Error("the on-error handler never re-raises: a failed write would exit 0 " +
			"and Image would report the path of a zero-byte file")
	}
}

func TestClipboardTextPinsTheChildsTextEncoding(t *testing.T) {
	dir := stubPATH(t)
	stubBin(t, dir, "pbpaste", `printf 'LC_ALL=[%s] LC_CTYPE=[%s]' "$LC_ALL" "$LC_CTYPE"`)
	// The hostile case: a caller whose own environment pins a non-UTF-8
	// locale. LC_ALL outranks LC_CTYPE, so leaving it in place would put
	// the MacRoman transcoding back.
	t.Setenv("LC_ALL", "C")
	t.Setenv("LC_CTYPE", "C")

	got, err := newClipboard(t.TempDir()).Text(testCtx(t))
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	if want := "LC_ALL=[] LC_CTYPE=[UTF-8]"; got != want {
		t.Errorf("the child saw %q, want %q — pbpaste transcodes to its locale, "+
			"so anything but UTF-8 here is a paste that changed on the way through", got, want)
	}
}

func TestClipboardTextKeepsPbpastesOwnErrorText(t *testing.T) {
	dir := stubPATH(t)
	stubBin(t, dir, "pbpaste", `echo "pbpaste: cannot read the pasteboard" >&2; exit 1`)

	text, err := newClipboard(t.TempDir()).Text(testCtx(t))
	if err == nil {
		t.Fatalf("Text returned %q and no error", text)
	}
	if !strings.Contains(err.Error(), "cannot read the pasteboard") {
		t.Errorf("err = %v, want it to carry pbpaste's own message rather than an exit status", err)
	}
}
