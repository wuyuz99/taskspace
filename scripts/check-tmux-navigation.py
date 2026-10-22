#!/usr/bin/env python3
"""Exercise the documented pickers on an isolated tmux server (Unix only)."""
import fcntl
import os
import pty
import select
import struct
import subprocess
import tempfile
import termios
import time

with tempfile.TemporaryDirectory(prefix="tspace-picker-") as tmp:
    prefix = ["tmux", "-S", tmp + "/socket", "-f", "/dev/null"]

    def tmux(*args):
        return subprocess.check_output(prefix + list(args), text=True).strip()

    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 140, 0, 0))
    client = None
    def screen_output():
        chunks = []
        while select.select([master], [], [], 0.2)[0]:
            chunks.append(os.read(master, 65536))
        return b"".join(chunks).decode(errors="replace")
    try:
        for n in range(10):
            session = "tspace@task" + str(n)
            tmux("new-session", "-d", "-s", session, "-n", "_root", "sleep 120")
            for window in ["backend", "_logs"]:
                tmux("new-window", "-d", "-t", session, "-n", window, "sleep 120")
        client = subprocess.Popen(prefix + ["attach", "-t", "tspace@task0"],
                                  stdin=slave, stdout=slave, stderr=slave,
                                  env={**os.environ, "TERM": "xterm-256color", "TMUX": ""})
        time.sleep(0.2)
        pane = tmux("display-message", "-p", "-t", "tspace@task0:_root", "#{pane_id}")
        before = tmux("display-message", "-p", "-t", pane, "#{window_id}")
        screen_output()
        session_id = tmux("display-message", "-p", "-t", pane, "#{session_id}")
        scoped = "#{==:#{session_id}," + session_id + "}"
        tmux("choose-tree", "-w", "-f", scoped, "-t", pane)
        time.sleep(0.1)
        screen = screen_output()
        assert "task0" in screen and "_logs" in screen and "backend" in screen, screen
        assert all("task" + str(n) not in screen for n in range(1, 10)), screen
        tmux("send-keys", "-t", pane, "q")
        assert tmux("display-message", "-p", "-t", pane, "#{window_id}") == before
        assert tmux("display-message", "-p", "-t", pane, "#{pane_in_mode}") == "0"
        screen_output()
        # The binding captures the invocation's pane and session before the
        # picker evaluates its filter for every candidate.
        tmux("bind-key", "W", "run-shell",
             "tmux -S '#{socket_path}' choose-tree -w -t '#{pane_id}' -f '##{==:##{session_id},#{session_id}}'")
        os.write(master, b"\x02W")
        time.sleep(0.1)
        screen = screen_output()
        assert "_logs" in screen and all("task" + str(n) not in screen for n in range(1, 10)), screen
        tmux("send-keys", "-t", pane, "q")
        screen_output()
        tmux("choose-tree", "-s", "-t", pane)
        time.sleep(0.1)
        screen = screen_output()
        assert all("task" + str(n) in screen for n in range(10)), screen
        tmux("send-keys", "-t", pane, "q")
        print(tmux("-V") + ": 10 tasks, scoped windows, extra windows, cancel, and cross-task picker PASS")
        print(tmux("list-keys", "-T", "prefix", "w"))
        print(tmux("list-keys", "-T", "prefix", "s"))
    finally:
        subprocess.run(prefix + ["kill-server"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        if client is not None:
            client.wait(timeout=5)
        os.close(master)
        os.close(slave)
