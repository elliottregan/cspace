#!/usr/bin/env python3
"""Boot `cspace tui` under a pty, look at it, quit.

    make build && python3 scripts/tui-smoke/smoke.py [needle ...]

Prints the screen, whether each needle appeared, and the exit code. A HUNG
exit means the quit key never reached tea.Quit.
"""

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from tuilib import Tui  # noqa: E402


def main():
    needles = sys.argv[1:] or ["sandboxes", "daemon"]
    repo = os.getcwd()
    t = Tui(cwd=repo)
    t.pump(12)
    print(t.display())
    print()
    t.send(b"?", then=1.0)
    help_screen = t.display()
    t.send(b"?", then=0.5)
    t.quit()

    text = t.raw.decode("utf-8", "replace")
    for needle in needles:
        print("%-24s %s" % (needle, needle in text))
    print("%-24s %s" % ("help overlay", "keys" in help_screen))
    print("%-24s %s" % ("exit", t.exit_code()))


if __name__ == "__main__":
    main()
