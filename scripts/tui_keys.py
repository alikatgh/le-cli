#!/usr/bin/env python3
"""Drive the real `le` TUI in a pty and dump the frame after a key sequence.

Built to live-fire RELEASING.md step 2 for TUI keybindings: `go test` drives
model.Update() directly, which proves the state machine but NOT that a real
terminal, a real scan of real listeners, and the real renderer agree. This
starts an actual listener, runs the actual binary under a pty, sends actual
keystrokes, and prints what a human would see.

    scripts/tui_keys.py --keys $'\\r' --expect '›'

What it does NOT catch: anything past the first repaint it captures (it reads
for a fixed settle window, not until quiescence), colour fidelity, or mouse
input. And it deliberately never sends a key sequence that would RUN a pane
field action — `enter` twice reaches openURL and would launch a real browser
tab. Keep live-fire sequences to navigation only.
"""

import argparse
import os
import pty
import re
import select
import socket
import subprocess
import sys
import time

ANSI = re.compile(rb"\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b[()][B0]|\x1b[=><]|\x1b\][^\x07]*\x07")


def strip_ansi(data: bytes) -> str:
    return ANSI.sub(b"", data).decode("utf-8", "replace")


def hold_port() -> socket.socket:
    """A real listening socket, so `le` has a real row to show."""
    s = socket.socket()
    s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind(("127.0.0.1", 0))
    s.listen(8)
    return s


def drive(binary: str, keys: str, settle: float, cols: int, rows: int) -> str:
    pid, fd = pty.fork()
    if pid == 0:  # child
        os.environ["TERM"] = "xterm-256color"
        os.environ["LINES"], os.environ["COLUMNS"] = str(rows), str(cols)
        os.execv(binary, [binary])
        os._exit(127)

    # Size the pty before the TUI's first measurement.
    import fcntl
    import struct
    import termios

    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))

    out = bytearray()

    def pump(seconds: float) -> None:
        end = time.time() + seconds
        while time.time() < end:
            r, _, _ = select.select([fd], [], [], 0.05)
            if fd in r:
                try:
                    chunk = os.read(fd, 65536)
                except OSError:
                    return
                if not chunk:
                    return
                out.extend(chunk)
                # Lipgloss asks the terminal for its background colour (OSC 11)
                # and BLOCKS on the answer to pick light/dark styling. A real
                # terminal replies; a bare pty never does, so without this the
                # TUI renders nothing and the harness "passes" on an empty
                # frame. Answer as a dark terminal.
                if b"\x1b]11;?" in chunk:
                    os.write(fd, b"\x1b]11;rgb:1e1e/1e1e/2e2e\x1b\\")
                if b"\x1b]10;?" in chunk:
                    os.write(fd, b"\x1b]10;rgb:cccc/cccc/cccc\x1b\\")
                # Same trap, second query: DSR (cursor position report). Both
                # must be answered or the TUI never reaches its first frame.
                if b"\x1b[6n" in chunk:
                    os.write(fd, b"\x1b[1;1R")

    try:
        pump(settle)  # let the first scan land
        for ch in keys:
            os.write(fd, ch.encode())
            pump(settle)
        frame = strip_ansi(bytes(out))
        os.write(fd, b"q")  # quit cleanly so the TUI restores the terminal
        pump(0.4)
        return frame
    finally:
        try:
            os.kill(pid, 15)
        except ProcessLookupError:
            pass
        os.close(fd)
        try:
            os.waitpid(pid, 0)
        except ChildProcessError:
            pass


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--binary", default="./le")
    ap.add_argument("--keys", default="", help="literal keystrokes, e.g. $'\\r' or 'jj'")
    ap.add_argument("--expect", action="append", default=[], help="substring that must appear")
    ap.add_argument("--reject", action="append", default=[], help="substring that must NOT appear")
    ap.add_argument("--settle", type=float, default=1.2)
    ap.add_argument("--cols", type=int, default=140)
    ap.add_argument("--rows", type=int, default=40)
    ap.add_argument("--quiet", action="store_true")
    args = ap.parse_args()

    sock = hold_port()
    try:
        frame = drive(args.binary, args.keys, args.settle, args.cols, args.rows)
    finally:
        sock.close()

    if not args.quiet:
        print(frame)
        print("=" * 70)

    failed = False
    for want in args.expect:
        ok = want in frame
        print(f"{'PASS' if ok else 'FAIL'}  expect {want!r}")
        failed |= not ok
    for bad in args.reject:
        ok = bad not in frame
        print(f"{'PASS' if ok else 'FAIL'}  reject {bad!r}")
        failed |= not ok
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
