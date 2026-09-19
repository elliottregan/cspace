#!/usr/bin/env python3
"""Drive `cspace tui`'s mouse and image paste under a pty, Mac-only.

    make build
    bin/cspace-go up mouse-smoke
    python3 scripts/tui-smoke/mouse.py mouse-smoke
    bin/cspace-go down mouse-smoke

Not run by `make check`: it needs a real sandbox and the real pasteboard.
Each step prints what it did and the screen it produced; read them.

Mouse events are SGR sequences, which the harness can send raw. Columns and
rows are ONE-based on the wire and zero-based in bubbletea, which halves
every off-by-one argument if you keep it in mind:

    press    \\x1b[<0;COL;ROWM
    release  \\x1b[<0;COL;ROWm
    wheel up \\x1b[<64;COL;ROWM
    wheel dn \\x1b[<65;COL;ROWM
"""

import os
import subprocess
import sys
import tempfile
import zlib
import struct

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from tuilib import Tui  # noqa: E402

ROWS, COLS = 40, 120

# The layout at 40x120, from internal/controlplane/geometry.go:
#   sidebar   columns 1..24 (1-based), the 24th is the rule
#   list      rows 1..25   (bodyHeight 39 -> band 13, list 39-13-1)
#   tabs row  row 1, columns 25..120
#   main      rows 2..39,  columns 25..120
#   footer    row 40
SIDEBAR_COL = 6
TABS_ROW = 1
TAB_COL = 32
MAIN_COL = 60
MAIN_ROW = 20


def press(t, col, row):
    t.send(("\x1b[<0;%d;%dM" % (col, row)).encode(), then=0.8)
    t.send(("\x1b[<0;%d;%dm" % (col, row)).encode(), then=0.8)


def wheel(t, col, row, up=True):
    code = 64 if up else 65
    t.send(("\x1b[<%d;%d;%dM" % (code, col, row)).encode(), then=0.8)


def make_png(path):
    def chunk(tag, data):
        body = tag + data
        return struct.pack(">I", len(data)) + body + struct.pack(">I", zlib.crc32(body) & 0xFFFFFFFF)

    raw = b"".join(b"\x00" + b"\xff\x00\x00" * 2 for _ in range(2))
    png = (b"\x89PNG\r\n\x1a\n"
           + chunk(b"IHDR", struct.pack(">IIBBBBB", 2, 2, 8, 2, 0, 0, 0))
           + chunk(b"IDAT", zlib.compress(raw))
           + chunk(b"IEND", b""))
    with open(path, "wb") as f:
        f.write(png)


def clipboard_png(path):
    subprocess.check_call(["osascript",
                           "-e", "on run argv",
                           "-e", "set the clipboard to (read (POSIX file (item 1 of argv)) as «class PNGf»)",
                           "-e", "end run",
                           path])


def clipboard_text(text):
    p = subprocess.Popen(["pbcopy"], stdin=subprocess.PIPE)
    p.communicate(text.encode())


def step(n, what, t):
    print("\n=== %d. %s ===" % (n, what))
    print(t.display())


def row_line(t, name):
    """The 1-based screen row whose sidebar cell holds `name`.

    `cspace tui` is host-wide — it lists every project on the machine — so
    which line this sandbox lands on depends on what else is up. A fixed
    row number would quietly drive somebody else's sandbox and the rest of
    the run would prove nothing.
    """
    for i, line in enumerate(t.display().split("\n"), start=1):
        if name in line[:24]:
            return i
    raise SystemExit("%s is not in the sidebar; is it up?" % name)


def main():
    sandbox = sys.argv[1] if len(sys.argv) > 1 else "mouse-smoke"

    # Save the text clipboard and put it back at the end. An IMAGE on the
    # clipboard when this starts cannot be restored — pbcopy writes text
    # only — and is lost. Say so rather than pretend otherwise.
    saved = subprocess.run(["pbpaste"], capture_output=True).stdout

    tmp = tempfile.mkdtemp(prefix="cspace-mouse-smoke-")
    src = os.path.join(tmp, "src.png")
    make_png(src)

    t = Tui(cwd=os.getcwd(), rows=ROWS, cols=COLS)
    try:
        t.pump(12)
        step(0, "booted", t)

        press(t, SIDEBAR_COL, row_line(t, sandbox))
        step(1, "clicked %s's sidebar row (the ▸ marker should be on it)" % sandbox, t)

        t.send(b"\r", then=6.0)   # enter: open a claude pane on it
        step(2, "opened a claude pane", t)

        t.send(b"\x00", then=0.5)  # ctrl+space, the leader
        t.send(b"t", then=1.0)     # the new-pane picker
        t.send(b"\x1b[B" * 3, then=0.5)
        t.send(b"\r", then=4.0)    # host shell
        step(3, "opened a host shell (two tabs now)", t)

        press(t, TAB_COL, TABS_ROW)
        step(4, "clicked the first tab (focus should be back on it)", t)

        press(t, MAIN_COL, MAIN_ROW)
        step(5, "clicked the pane area", t)

        wheel(t, SIDEBAR_COL, 6, up=False)
        wheel(t, SIDEBAR_COL, 6, up=False)
        step(6, "wheeled down over the sidebar (selection should have moved)", t)

        wheel(t, MAIN_COL, MAIN_ROW, up=True)
        step(7, "wheeled up over the CLAUDE pane (expect the no-scrollback notice)", t)

        t.send(b"\x00", then=0.3)
        t.send(b"n", then=1.0)     # next tab: the host shell
        t.send(b"seq 1 200\r", then=2.0)
        wheel(t, MAIN_COL, MAIN_ROW, up=True)
        step(8, "wheeled up over the HOST SHELL (expect 'scroll · N lines back')", t)
        wheel(t, MAIN_COL, MAIN_ROW, up=False)
        step(9, "wheeled back down", t)

        t.send(b"\x00", then=0.3)
        t.send(b"g", then=0.5)     # back to live
        t.send(b"\x00", then=0.3)
        t.send(b"p", then=1.0)     # back to the claude pane

        clipboard_png(src)
        t.send(b"\x00", then=0.3)
        t.send(b"v", then=6.0)
        step(10, "leader v with a PNG on the clipboard "
                 "(expect /sessions/paste/<stamp>.png in Claude's input box, NOT sent)", t)

        clipboard_text("hello from the smoke test")
        t.send(b"\x00", then=0.3)
        t.send(b"v", then=4.0)
        step(11, "leader v with TEXT on the clipboard (expect the text pasted)", t)

        t.send(b"\x00", then=0.3)
        t.send(b"?", then=1.0)
        step(12, "the help overlay (expect the mouse line and the shift-drag note)", t)
        t.send(b"?", then=0.5)

        t.send(b"x", then=1.0)
        step(13, "typed 'x' into the pane after all that "
                 "(expect it in Claude's input box: keys still reach the child)", t)

        t.send(b"\x00", then=0.3)
        t.send(b"x", then=4.0)     # close the pane
        t.send(b"\x00", then=0.3)
        t.send(b"x", then=4.0)     # close the host shell
        t.send(b"\x00", then=0.3)
        t.quit(key=b"q")
        print("\nexit: %s" % t.exit_code())
    finally:
        p = subprocess.Popen(["pbcopy"], stdin=subprocess.PIPE)
        p.communicate(saved)
        print("restored the TEXT clipboard; an image that was on it is gone")

    print("\npaste files written:")
    home = os.path.expanduser("~")
    for root in (os.path.join(home, ".cspace", "sessions"), os.path.join(home, ".cspace", "paste")):
        for dirpath, _, files in os.walk(root):
            if os.path.basename(dirpath) == "paste" or dirpath == root:
                for f in files:
                    print("  %s" % os.path.join(dirpath, f))


if __name__ == "__main__":
    main()
