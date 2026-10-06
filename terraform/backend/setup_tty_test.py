"""Runs setup.sh in a pseudo-terminal, which is where the colours, the banner's
animation and the spinner exist (a pipe or a file gets none of them), against the
stand-in gcloud and terraform that setup_test.sh builds:

    python3 setup_tty_test.py <setup.sh> <dir with the stand-ins> <work dir>

Prints one "ok" or "FAIL" line per check and exits 1 if any failed. Needs the pty
module, so it runs on Linux and macOS; setup_test.sh skips it elsewhere.
"""

import os
import pty
import re
import sys

setup, bindir, work = sys.argv[1:4]
log = os.path.join(work, "setup.log")
calls = os.path.join(work, "calls")

env = dict(os.environ)
env.pop("NO_COLOR", None)
env.update(
    PATH=bindir + os.pathsep + env["PATH"],
    HOME=work,
    TERM="xterm-256color",
    CLOUD_SHELL="true",
    FAKE_CALLS=calls,
    FAKE_DIR=work,
    FAKE_APPLY_SLEEP="1",
    SHAREPI_SETUP_LOG=log,
    SP_PROGRESS_INTERVAL="0.05",
)
os.environ.clear()
os.environ.update(env)

captured = bytearray()


def read(fd):
    data = os.read(fd, 4096)
    captured.extend(data)
    return data


status = pty.spawn(["bash", setup, "hoge-vfltdc", "ea"], read)
exit_code = os.waitstatus_to_exitcode(status)
text = captured.decode("utf-8", "replace")

failed = False


def check(name, condition):
    global failed
    print(("ok   " if condition else "FAIL ") + name)
    failed = failed or not condition


check("tty: setup.sh succeeds", exit_code == 0)
check("tty: the banner is coloured with 24-bit colour", "\x1b[38;2;" in text)
check("tty: the banner is redrawn in place (cursor moves up)", text.count("\x1b[5A") >= 20)

label = "[7/8] Creating your backend (this takes a few minutes) ... "
frames = set(re.findall(re.escape("\r" + label) + r"([|/\\-])", text))
check("tty: the spinner shows more than one frame (%s)" % "".join(sorted(frames)), len(frames) >= 2)
check("tty: the step line ends on 'ok' after the spinner", ("\r" + label + "ok") in text)

lines = [line.strip("\r") for line in text.split("\n")]
check("tty: the URL is on a line of its own",
      "https://sharepi-backend-123.asia-northeast1.run.app" in lines)

try:
    first = open(log, encoding="utf-8").readline()
except OSError:
    first = ""
check("tty: the log says it was a terminal (%s)" % first.strip(),
      "tty=1" in first and "colour=truecolor" in first)

sys.exit(1 if failed else 0)
