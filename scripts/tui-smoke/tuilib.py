"""A pty harness for driving `cspace tui` headlessly, Mac-only, stdlib only.

Not run by `make check`: it starts the real binary against the real host, so
it belongs to a person (or an agent) verifying a change, not to CI.

Two pieces. Screen is a small VT interpreter — enough of one for what Bubble
Tea paints — so `display()` is what a human would have seen rather than a
byte stream, and it is resumable, because pump feeds it one pty read at a
time and escape sequences straddle read boundaries. So do multi-byte
glyphs, one level down, which is why the bytes go through a single
incremental UTF-8 decoder rather than being decoded per read. Tui forks a pty, runs the
binary in it, answers the terminal queries Bubble Tea makes at startup, and
keeps the stream drained.

The pump never stops while keys are being typed. The dashboard repaints a
120x40 screen on a one-second ticker — several KB a frame, far more than a
pty buffer holds — so a script that stops reading while it writes wedges the
child in write() and never gets its keystrokes read.
"""

import codecs
import fcntl
import os
import pty
import re
import select
import signal
import struct
import termios
import time

# The parameter class carries the intermediates as well as the digits: Bubble
# Tea v2 opens with CSI ? 2026 $ p and CSI ? 2027 $ p, and a class without `$`
# does not match them — feed then falls through and paints "?2026$p" onto the
# grid. The final-byte class is widened for the same reason. A private-prefix
# sequence still draws nothing; _csi returns early on it.
CSI = re.compile(r"""\x1b\[([0-9;:?<>=!$"' ]*)([A-Za-z@`{|}~])""")

# PARTIAL_CSI is the same head with no final byte and nothing after it: an
# escape sequence cut in half by a read boundary. feed stashes those instead
# of printing them.
PARTIAL_CSI = re.compile(r"""\x1b\[[0-9;:?<>=!$"' ]*$""")


class Screen:
    """A cell grid fed with terminal output.

    Handles what Bubble Tea actually emits: cursor addressing, the erase
    family, a scroll region plus reverse index (which is how it inserts a
    line), tabs, and printable text. Styling is discarded — this answers
    "what does it say", and the raw stream is kept separately for the
    questions styling answers.

    feed is resumable. Tui.pump hands it one pty read at a time, and an
    escape sequence straddling two reads is the normal case, not an edge
    one: without the pending buffer below, a 512-byte split through the
    footer's SGR run paints "38;2;74;74;74m" onto the screen. pyte, which
    this replaces, is a resumable state machine for the same reason.
    """

    def __init__(self, cols, rows):
        self.cols, self.rows = cols, rows
        self.clear()

    def clear(self):
        self.grid = [self._blank() for _ in range(self.rows)]
        self.x = self.y = 0
        self.top, self.bot = 0, self.rows - 1
        self._pending = ""

    def _blank(self):
        return [" "] * self.cols

    def display(self):
        return "\n".join("".join(row).rstrip() for row in self.grid)

    def _index(self):
        """Line feed, honouring the scroll region."""
        if self.y == self.bot:
            del self.grid[self.top]
            self.grid.insert(self.bot, self._blank())
        elif self.y < self.rows - 1:
            self.y += 1

    def _reverse_index(self):
        """ESC M — the inverse, which is how Bubble Tea inserts a line."""
        if self.y == self.top:
            del self.grid[self.bot]
            self.grid.insert(self.top, self._blank())
        elif self.y > 0:
            self.y -= 1

    def feed(self, data):
        # Anything the previous call could not finish parsing leads this one.
        if self._pending:
            data, self._pending = self._pending + data, ""
        i, n = 0, len(data)
        while i < n:
            ch = data[i]
            if ch == "\x1b":
                if i + 1 >= n:  # a bare ESC at the tail: the rest is coming
                    self._pending = data[i:]
                    return
                if data.startswith("\x1b]", i):  # OSC: skip to BEL or ST
                    bel, st = data.find("\x07", i), data.find("\x1b\\", i)
                    if st != -1 and (bel == -1 or st < bel):
                        i = st + 2
                    elif bel != -1:
                        i = bel + 1
                    else:
                        self._pending = data[i:]  # unterminated OSC
                        return
                    continue
                m = CSI.match(data, i)
                if m:
                    self._csi(m.group(1), m.group(2))
                    i = m.end()
                    continue
                if PARTIAL_CSI.match(data, i):  # CSI cut off before its final
                    self._pending = data[i:]
                    return
                if data.startswith("\x1bM", i):
                    self._reverse_index()
                    i += 2
                    continue
                i += 2
                continue
            if ch == "\n":
                self._index()
            elif ch == "\r":
                self.x = 0
            elif ch == "\b":
                self.x = max(0, self.x - 1)
            elif ch == "\t":
                self.x = min(self.cols - 1, (self.x // 8 + 1) * 8)
            elif ch >= " ":
                if self.x < self.cols and self.y < self.rows:
                    self.grid[self.y][self.x] = ch
                self.x += 1
            i += 1

    def _csi(self, raw, final):
        if raw.startswith(("?", ">", "<", "!", "$", " ")):
            return  # private modes and device attributes draw nothing
        try:
            p = [int(x) if x else 1 for x in raw.split(";")] if raw else [1]
        except ValueError:
            # A colon sub-parameter is not an int: SGR truecolour written as
            # 38:2::74:74:74, or an underline style 4:3. lipgloss v2 emits
            # the semicolon form, so `cspace tui` never produces one — but a
            # pane running another program can, and an unhandled ValueError
            # here kills the harness instead of ignoring a sequence this
            # interpreter draws nothing for anyway.
            return
        if final in "Hf":
            row = p[0] if len(p) > 0 else 1
            col = p[1] if len(p) > 1 else 1
            self.y = min(self.rows - 1, max(0, row - 1))
            self.x = min(self.cols - 1, max(0, col - 1))
        elif final == "A":
            self.y = max(0, self.y - p[0])
        elif final == "B":
            self.y = min(self.rows - 1, self.y + p[0])
        elif final == "C":
            self.x = min(self.cols - 1, self.x + p[0])
        elif final == "D":
            self.x = max(0, self.x - p[0])
        elif final == "d":
            self.y = min(self.rows - 1, max(0, p[0] - 1))
        elif final in "G`":
            self.x = min(self.cols - 1, max(0, p[0] - 1))
        elif final == "r":
            top = p[0] if len(p) > 0 else 1
            bot = p[1] if len(p) > 1 else self.rows
            self.top = max(0, top - 1)
            self.bot = min(self.rows - 1, bot - 1)
            self.x, self.y = 0, self.top
        elif final == "J":
            mode = p[0] if raw else 0
            if mode in (2, 3):
                self.grid = [self._blank() for _ in range(self.rows)]
            elif mode == 0:
                for x in range(self.x, self.cols):
                    self.grid[self.y][x] = " "
                for y in range(self.y + 1, self.rows):
                    self.grid[y] = self._blank()
            else:
                for y in range(0, self.y):
                    self.grid[y] = self._blank()
                for x in range(0, self.x + 1):
                    self.grid[self.y][x] = " "
        elif final == "K":
            mode = p[0] if raw else 0
            span = range(self.x, self.cols) if mode == 0 else \
                range(0, self.x + 1) if mode == 1 else range(0, self.cols)
            for x in span:
                self.grid[self.y][x] = " "
        elif final == "X":
            for x in range(self.x, min(self.cols, self.x + p[0])):
                self.grid[self.y][x] = " "


class Tui:
    """One run of the binary under a pty."""

    def __init__(self, argv=None, cwd=".", env=None, rows=40, cols=120,
                 bin_="./bin/cspace-go"):
        self.raw = b""
        self.rows, self.cols = rows, cols
        self.screen = Screen(cols, rows)
        # One decoder for the whole stream, not one per read. The dashboard
        # paints glyphs the pty hands over in pieces — a single ▸ is
        # b"\xe2\x96\xb8" — and an independent decode of each chunk turns a
        # glyph split across a read boundary into U+FFFD on both sides, which
        # reads in a capture as a rendering bug that is not there. The
        # incremental decoder holds the partial sequence until the rest
        # arrives; reap() flushes whatever is left.
        self.decoder = codecs.getincrementaldecoder("utf-8")("replace")
        self.exited = False
        self.status = None
        argv = argv if argv is not None else ["tui"]
        path = bin_ if bin_.startswith("/") else os.path.join(cwd, bin_)

        pid, fd = pty.fork()
        if pid == 0:
            # Everything in this branch runs in the forked child, and it
            # must never raise: an exception would unwind back into the
            # caller's own code as a SECOND copy of the test script, both
            # halves printing. A missing binary, an unreadable cwd and a
            # failed execv all land here, so the child exits 127 — which
            # reap() then reports — rather than escaping.
            try:
                os.chdir(cwd)
                os.environ["TERM"] = "xterm-256color"
                os.environ["COLORTERM"] = "truecolor"
                for k, v in (env or {}).items():
                    os.environ[k] = v
                os.execv(path, [os.path.basename(path)] + argv)
            except BaseException:
                pass
            os._exit(127)  # execv only returns by failing
        self.pid, self.fd = pid, fd
        # The model renders nothing until it learns the window size.
        fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))

    def pump(self, seconds):
        end = time.time() + seconds
        while time.time() < end:
            r, _, _ = select.select([self.fd], [], [], 0.2)
            if not r:
                continue
            try:
                chunk = os.read(self.fd, 65536)
            except OSError:
                return
            if not chunk:
                return
            self.raw += chunk
            self.screen.feed(self.decoder.decode(chunk))
            self._answer_queries(chunk)

    def _answer_queries(self, chunk):
        """Play terminal.

        Bubble Tea v2 probes at startup — cursor position, background colour,
        synchronized output, kitty keyboard — and holds its first render until
        the answers arrive or a ~5s timeout expires. A real terminal answers in
        microseconds; without this the dashboard looks like it hangs for five
        seconds and every timing assertion is wrong.
        """
        reply = b""
        if b"\x1b]11;?" in chunk:
            reply += b"\x1b]11;rgb:0000/0000/0000\x1b\\"
        if b"\x1b[6n" in chunk:
            reply += b"\x1b[1;1R"
        if b"\x1b[?2026$p" in chunk:
            reply += b"\x1b[?2026;2$y"
        if b"\x1b[?2027$p" in chunk:
            reply += b"\x1b[?2027;0$y"
        if b"\x1b[?u" in chunk:
            reply += b"\x1b[?0u"
        if b"\x1b[c" in chunk:
            reply += b"\x1b[?62;22c"
        if reply:
            try:
                os.write(self.fd, reply)
            except OSError:
                pass

    def send(self, data, then=0.6):
        if isinstance(data, str):
            data = data.encode()
        os.write(self.fd, data)
        self.pump(then)

    def type(self, text, per=0.05):
        for ch in text:
            self.send(ch.encode(), then=per)

    def resize(self, rows, cols):
        self.rows, self.cols = rows, cols
        self.screen = Screen(cols, rows)
        fcntl.ioctl(self.fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))

    def display(self):
        return self.screen.display()

    def save(self, path, label=""):
        with open(path, "w") as f:
            if label:
                f.write("=== %s ===\n" % label)
            f.write(self.display())
            f.write("\n")
        return self.display()

    def save_raw(self, path):
        with open(path, "wb") as f:
            f.write(self.raw)

    def quit(self, key=b"q", wait=3.0):
        try:
            os.write(self.fd, key)
        except OSError:
            pass
        self.pump(wait)
        return self.reap()

    def _flush(self):
        """Feed the decoder's held-back bytes to the screen, once.

        A stream that ends mid-glyph leaves bytes in the incremental decoder;
        final=True turns them into the one U+FFFD they deserve instead of
        dropping them silently. Safe to call again — the decoder resets
        itself and a second call yields nothing.
        """
        tail = self.decoder.decode(b"", final=True)
        if tail:
            self.screen.feed(tail)

    def reap(self, tries=60):
        # Idempotent, on every path. quit() calls reap(), and a script that
        # then calls reap() itself must not re-run the 6s loop and raise
        # ChildProcessError against a pid nobody is left to wait for.
        if self.exited:
            return self.status
        for _ in range(tries):
            try:
                done, st = os.waitpid(self.pid, os.WNOHANG)
            except ChildProcessError:
                # Already reaped by someone else (a SIGCHLD handler, an
                # earlier wait). Nothing to report but "it is gone".
                self.exited = True
                self._flush()
                return self.status
            if done:
                self.exited, self.status = True, st
                self._flush()
                return st
            self.pump(0.1)
        try:
            os.kill(self.pid, signal.SIGKILL)
            os.waitpid(self.pid, 0)
        except OSError:
            pass
        # exited is set here too: the child is gone, status None is what
        # exit_code() renders as HUNG, and a second reap() must return that
        # rather than waiting another six seconds for a corpse.
        self.exited, self.status = True, None
        self._flush()
        return None

    def exit_code(self):
        if self.status is None:
            return "HUNG (killed)"
        return os.waitstatus_to_exitcode(self.status)
