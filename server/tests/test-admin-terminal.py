"""Transport regression for the hidden-password administrator fixture."""
import importlib.util
import io
from pathlib import Path
import sys
import types
import unittest
from unittest import mock


spec = importlib.util.spec_from_file_location("admin_terminal", Path(__file__).with_name("admin-terminal.py"))
terminal = importlib.util.module_from_spec(spec)
spec.loader.exec_module(terminal)


class Console:
    def __init__(self, newline, echo=False):
        self.newline, self.echo, self.writes, self.closed = newline, echo, [], False

    def write(self, text):
        self.writes.append(text)

    send = write

    def read(self, size):
        if not self.writes:
            return "Password (12+ characters): "
        if len(self.writes) == 1:
            return "Confirm password: "
        if any(not text.endswith(self.newline) or text.endswith("\r\n") != (self.newline == "\r\n") for text in self.writes):
            raise EOFError("password confirmation consumed an extra terminal line")
        return (self.writes[0] if self.echo else "") + "Administrator operation completed."

    read_nonblocking = read

    def close(self, force=True):
        self.closed = True


class HiddenPasswordTransport(unittest.TestCase):
    def exercise(self, platform, echo=False):
        console = Console("\r\n" if platform == "nt" else "\n", echo)
        modules = {
            "pexpect": types.SimpleNamespace(spawn=lambda *a, **kw: console),
            "winpty": types.SimpleNamespace(PtyProcess=types.SimpleNamespace(spawn=lambda *a, **kw: console)),
        }
        with mock.patch.object(terminal.os, "name", platform), \
             mock.patch.object(sys, "argv", ["admin-terminal.py", "fixture-server", "fixture.sqlite", "admin"]), \
             mock.patch.object(sys, "stdin", io.StringIO("isolated fixture password\n")), \
             mock.patch.object(sys, "stdout", io.StringIO()), \
             mock.patch.dict(sys.modules, modules), \
             mock.patch.object(terminal.threading, "Timer"):
            terminal.main()
        self.assertTrue(console.closed)
        self.assertEqual(len(console.writes), 2)

    def test_posix_sends_one_lf_per_password(self):
        self.exercise("posix")

    def test_windows_preserves_console_crlf(self):
        self.exercise("nt")

    def test_echo_is_still_rejected(self):
        with self.assertRaisesRegex(AssertionError, "echoed"):
            self.exercise("posix", echo=True)


if __name__ == "__main__":
    unittest.main()
