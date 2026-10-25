"""Verify the Windows launcher keeps all default runtime files under build/."""
import http.cookiejar
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request
import wave


def main():
    source = Path(__file__).resolve().parents[1]
    binary = Path(os.environ.get("MUSIC_SERVER_TEST_BINARY", source / "build/musicserver.exe"))
    directory = Path(tempfile.mkdtemp(prefix="musicserver-layout-"))
    runtime = directory / "server with spaces/build"
    runtime.mkdir(parents=True)
    foreign = directory / "foreign"
    foreign.mkdir()
    shutil.copy2(binary, runtime / "musicserver.exe")
    shutil.copy2(source / "start.bat", runtime.parent / "start.bat")
    # This file must never be discovered by the default music scan.
    (runtime.parent / "SourceOnly.mp3").write_bytes(b"source directory fixture")
    (runtime / "media").mkdir()
    with wave.open(str(runtime / "media/Track.wav"), "wb") as audio:
        audio.setparams((1, 2, 8000, 0, "NONE", "not compressed"))
        audio.writeframes(bytes(800))
    password = "isolated layout administrator password"
    initialized = subprocess.run(
        [sys.executable, str(source / "tests/admin-terminal.py"), str(runtime / "musicserver.exe"),
         str(runtime / "data/library.sqlite"), "admin"],
        input=password + "\n", capture_output=True, text=True, timeout=35,
    )
    assert initialized.returncode == 0, initialized.stdout + initialized.stderr
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    base = "http://127.0.0.1:" + str(port)
    environment = os.environ.copy()
    environment["MUSIC_SERVER_NO_PAUSE"] = "1"
    process = subprocess.Popen(
        [os.environ.get("COMSPEC", "cmd.exe"), "/d", "/c", "call", str(runtime.parent / "start.bat"),
         "-addr", "127.0.0.1:" + str(port)], cwd=foreign,
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, env=environment,
    )
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def request(path, payload=None):
        data = None if payload is None else json.dumps(payload).encode()
        req = urllib.request.Request(base + "/api/v2/" + path, data=data,
                                     headers={"Origin": base, "Content-Type": "application/json"})
        with opener.open(req, timeout=2) as response:
            return json.load(response)

    try:
        for attempt in range(100):
            try:
                health = request("health")
                break
            except OSError:
                if process.poll() is not None:
                    raise AssertionError("start.bat exited before the server became ready")
                time.sleep(0.1)
        else:
            raise AssertionError("server startup timed out")
        assert "commit" in health and "modified" in health, health
        expected_commit = os.environ.get("MUSIC_SERVER_EXPECT_COMMIT")
        if expected_commit:
            assert health["commit"] == expected_commit and health["modified"] is False, health
        assert health["version"] == "0.19.1", health
        request("auth/login", {"username": "admin", "password": password})
        for attempt in range(100):
            tracks = request("tracks")
            if len(tracks) == 1:
                break
            time.sleep(0.1)
        assert len(tracks) == 1 and tracks[0]["path"] == "media/Track.wav", tracks
        assert Path(request("admin/settings")["media_root"]) == runtime
        assert (runtime / "data/library.sqlite").is_file()
        assert sorted(p.name for p in runtime.parent.iterdir()) == ["SourceOnly.mp3", "build", "start.bat"]
        assert not list(foreign.iterdir()), "runtime files leaked into the caller's directory"
        print("Runtime layout passed: fixed build executable, foreign working directory, isolated data and media, Git identity.")
        print("Isolated test data:", directory)
    finally:
        if process.poll() is None:
            subprocess.run(["taskkill", "/PID", str(process.pid), "/T", "/F"], capture_output=True)
        process.wait(timeout=10)


if __name__ == "__main__":
    main()
