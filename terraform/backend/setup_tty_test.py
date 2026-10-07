"""Runs setup.sh in a pseudo-terminal, which is where the colours, the banner's
animation and the spinner exist (a pipe or a file gets none of them), against the
stand-in gcloud and terraform that setup_test.sh builds:

    python3 setup_tty_test.py <setup.sh> <dir with the stand-ins> <work dir>

Prints one "ok" or "FAIL" line per check and exits 1 if any failed. Needs the pty
module, so it runs on Linux and macOS; setup_test.sh skips it elsewhere.
"""

import fcntl
import os
import pty
import re
import struct
import sys
import termios

setup, bindir, work = sys.argv[1:4]

failed = False


def check(name, condition):
    global failed
    print(("ok   " if condition else "FAIL ") + name)
    failed = failed or not condition


def run(rows, tag):
    """Runs setup.sh on a terminal of `rows` rows; returns (output, exit code, log path)."""
    log = os.path.join(work, "setup-%s.log" % tag)
    env = dict(os.environ)
    env.pop("NO_COLOR", None)
    env.update(
        PATH=bindir + os.pathsep + env["PATH"],
        HOME=work,
        TERM="xterm-256color",
        CLOUD_SHELL="true",
        LANG="C",
        FAKE_CALLS=os.path.join(work, "calls"),
        FAKE_DIR=work,
        FAKE_APPLY_SLEEP="1",
        SHAREPI_SETUP_LOG=log,
        SP_PROGRESS_INTERVAL="0.05",
    )
    pid, fd = pty.fork()
    if pid == 0:
        os.execvpe("bash", ["bash", setup, "hoge-vfltdc", "ea"], env)
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, 120, 0, 0))
    out = bytearray()
    while True:
        try:
            data = os.read(fd, 4096)
        except OSError:  # the child has closed its end
            break
        if not data:
            break
        out.extend(data)
    _, status = os.waitpid(pid, 0)
    return out.decode("utf-8", "replace"), os.waitstatus_to_exitcode(status), log


def rows_after_moving_up(text):
    """Follows only the cursor's vertical moves (newline, up, save, restore).

    Returns (the row where the banner's first line is drawn, the row reached after
    every "move up", the number of saves)."""
    row = 0
    saved = 0
    top = None
    ups = []
    saves = 0
    i = 0
    while i < len(text):
        if text.startswith("\x1b7", i):
            saved = row
            saves += 1
            i += 2
        elif text.startswith("\x1b8", i):
            row = saved
            i += 2
        elif text.startswith("\x1b", i):
            m = re.match(r"\x1b\[([0-9;?]*)([A-Za-z])", text[i:])
            if m is None:
                i += 1
                continue
            if top is None and text.startswith("\x1b[38;", i):
                top = row
            if m.group(2) == "A":
                row -= int(m.group(1) or 1)
                ups.append(row)
            i += m.end()
        else:
            if text[i] == "\n":
                row += 1
            i += 1
    return top, ups, saves


url = "https://sharepi-backend-123.asia-northeast1.run.app"
label = "[7/8] Creating your backend (this takes a few minutes) ... "

# ---------------------------------------------------------------- a tall terminal
text, exit_code, log = run(40, "tall")

check("tty: setup.sh succeeds", exit_code == 0)
check("tty: the banner is coloured with 24-bit colour", "\x1b[38;2;" in text)
check("tty: the banner is redrawn in place (cursor moves up)", text.count("\x1b[5A") >= 20)

frames = set(re.findall(re.escape("\r" + label) + r"([|/\\-])", text))
check("tty: the spinner shows more than one frame (%s)" % "".join(sorted(frames)), len(frames) >= 2)
# (The cursor is shown again between the spinner and the "ok".)
check("tty: the step line ends on 'ok' after the spinner",
      re.search(re.escape("\r" + label) + r"(\x1b\[\?25h)?ok", text) is not None)

# What can be seen: the escape codes (colours, the cursor) take no room on screen.
visible = re.sub(r"\x1b\[[0-9;?]*[A-Za-z]|\x1b[78]", "", text)
lines = [line.strip("\r") for line in visible.split("\n")]
check("tty: the URL is on a line of its own", url in lines)
non_empty = [line for line in lines if line.strip()]
check("tty: the URL is the very last line", bool(non_empty) and non_empty[-1] == url)

# The text cursor is hidden while the banner and the steps are redrawn (it was seen
# jumping about), and it is always shown again at the end.
hide, show = "\x1b[?25l", "\x1b[?25h"
check("tty: the cursor is hidden while things are redrawn", hide in text)
check("tty: the cursor is shown again at the end", show in text and text.rfind(show) > text.rfind(hide))

# The banner keeps moving while a step runs: it is redrawn from wherever the step has
# left the cursor, and every redraw has to start at the banner's own first row.
top, ups, saves = rows_after_moving_up(text)
live = [row for row in ups[24:]] if len(ups) > 24 else []  # the first 24 are the opening animation
check("tty: the banner is redrawn while the steps run (%d redraws)" % saves, saves >= 5)
check("tty: every redraw starts at the banner's top row", top is not None and len(ups) > 24 and all(r == top for r in ups))

try:
    first = open(log, encoding="utf-8").readline()
    second = open(log, encoding="utf-8").readlines()[1]
except (OSError, IndexError):
    first, second = "", ""
check("tty: the log says it was a terminal (%s)" % first.strip(),
      "tty=1" in first and "colour=truecolor" in first)
check("tty: the log says the banner is live (%s)" % second.strip(), "live=1" in second)

# ---------------------------------------------------------------- a short terminal
# Too few rows for "up K rows" to be trustworthy: the opening animation still plays,
# but the banner is not redrawn while the steps run.
text, exit_code, log = run(20, "short")
check("tty: a short terminal still succeeds", exit_code == 0)
check("tty: a short terminal does not redraw the banner during the steps", "\x1b7" not in text)
check("tty: a short terminal still shows the opening animation", text.count("\x1b[5A") >= 20)

sys.exit(1 if failed else 0)
