"""Exercise real Windows terminal pause behavior and BAT exit-code propagation."""
import os
from pathlib import Path
import shutil
import tempfile
import threading

from winpty import PtyProcess


def run_case(directory, command, expected, pause=True, output=None):
    runner = directory / "runner.bat"
    runner.write_text(
        '@echo off\r\ncall ' + command + '\r\n'
        'set "observed_exit=%errorlevel%"\r\n'
        'echo BAT_TEST_FINISHED:%observed_exit%\r\n'
        'exit /b %observed_exit%\r\n', encoding="ascii",
    )
    environment = os.environ.copy()
    environment.pop("MUSIC_SERVER_NO_PAUSE", None)
    if not pause:
        environment["MUSIC_SERVER_NO_PAUSE"] = "1"
    process = PtyProcess.spawn([os.environ.get("COMSPEC", "cmd.exe"), "/d", "/c", str(runner)], env=environment)
    timer = threading.Timer(20, lambda: process.close(force=True))
    timer.start()
    transcript = ""
    try:
        checkpoint = "Press any key to close this window" if pause else "BAT_TEST_FINISHED:"
        while checkpoint not in transcript:
            try:
                transcript += process.read(4096)
            except EOFError as error:
                raise AssertionError("BAT exited without the expected terminal checkpoint:\n" + transcript) from error
        if pause:
            assert "BAT_TEST_FINISHED:" not in transcript, transcript
            assert process.isalive(), "BAT should remain open until a key is pressed"
            process.write(" ")
        while "BAT_TEST_FINISHED:" + str(expected) not in transcript:
            transcript += process.read(4096)
        if not pause:
            assert "Press any key to close this window" not in transcript, transcript
        if output:
            assert output in transcript, transcript
    finally:
        timer.cancel()
        process.close(force=True)


def main():
    source = Path(__file__).resolve().parents[1]
    directory = Path(tempfile.mkdtemp(prefix="musicserver-bat-pause-")) / "server with spaces"
    directory.mkdir()
    for name in ["start.bat", "build.bat"]:
        shutil.copy2(source / name, directory / name)
    start = '"' + str(directory / "start.bat") + '"'
    build = '"' + str(directory / "build.bat") + '"'
    run_case(directory, start, 1, output="is missing")
    (directory / "build").mkdir()
    shutil.copy2(source / "build/musicserver.exe", directory / "build/musicserver.exe")
    run_case(directory, start + " -h", 0, output="Usage of")
    run_case(directory, start + " -unknown-option", 2, output="flag provided but not defined")
    run_case(directory, start + " -h", 0, pause=False)
    # Stub the compiler entry to isolate the BAT wrapper's result and pause behavior.
    for expected in [0, 7]:
        (directory / "build.ps1").write_text("Write-Output 'compiler fixture'\nexit " + str(expected), encoding="ascii")
        run_case(directory, build, expected, output="compiler fixture")
    run_case(directory, build, 7, pause=False)
    print("BAT pause passed: missing binary, normal exit, startup failure, build success/failure, exit codes and unattended override.")
    print("Isolated test data:", directory)


if __name__ == "__main__":
    main()
