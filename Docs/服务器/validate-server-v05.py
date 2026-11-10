"""Isolated v0.5 backend checks. Uses only Python's standard library.

python validate-server-v05.py --exe PATH --output EMPTY_DIRECTORY
Music files and test keys remain outside the source tree. SQLite starts empty.
"""

import argparse
import contextlib
import json
import secrets
import shutil
import socket
import sqlite3
import subprocess
import time
import urllib.error
import urllib.request
from pathlib import Path


class Server:
    def __init__(self, exe, database, media, log, no_scan=False):
        self.key = secrets.token_hex(24)
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        self.url = f"http://127.0.0.1:{port}"
        self.log = open(log, "ab")
        command = [str(exe), "-addr", f"127.0.0.1:{port}", "-data", str(database),
                   "-media-root", str(media), "-api-key", self.key]
        if no_scan:
            command.append("-no-scan")
        self.process = subprocess.Popen(command, stdout=self.log, stderr=self.log,
                                        creationflags=subprocess.CREATE_NO_WINDOW)
        deadline = time.monotonic() + 30
        try:
            while time.monotonic() < deadline:
                if self.process.poll() is not None:
                    raise RuntimeError(f"Server exited; inspect {log}")
                try:
                    assert self.call("health")[1]["version"] == "0.5.0"
                    return
                except (OSError, urllib.error.URLError):
                    time.sleep(0.1)
            raise RuntimeError("Server startup timed out")
        except BaseException:
            self.close()
            raise

    def call(self, path, method="GET", body=None, key=None, headers=None, status=200):
        request_headers = {"X-API-Key": self.key if key is None else key}
        request_headers.update(headers or {})
        if body is not None and not isinstance(body, bytes):
            body = json.dumps(body, ensure_ascii=False).encode()
            request_headers["Content-Type"] = "application/json"
        request = urllib.request.Request(self.url + "/api/v1/" + path, body,
                                         request_headers, method=method)
        try:
            response = urllib.request.urlopen(request, timeout=30)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            content = response.read()
            assert response.status == status, (path, response.status, content[:500])
            if "application/json" in response.headers.get("Content-Type", ""):
                content = json.loads(content)
            return response.status, content, dict(response.headers)

    def close(self):
        if self.process.poll() is None:
            self.process.terminate()
            self.process.wait(timeout=10)
        self.log.close()

    def __enter__(self):
        return self

    def __exit__(self, *_):
        self.close()


def inspect_database(path):
    assert path.read_bytes()[:16] == b"SQLite format 3\x00"
    with contextlib.closing(sqlite3.connect(path)) as db:
        assert db.execute("PRAGMA integrity_check").fetchall() == [("ok",)]
        assert db.execute("PRAGMA foreign_key_check").fetchall() == []
        assert db.execute("PRAGMA user_version").fetchone()[0] == 1
        tables = [r[0] for r in db.execute("SELECT name FROM sqlite_master WHERE type='table'")]
        return {"schema": 1, "tables": tables, "integrity": "ok", "foreign_key_violations": 0}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--exe", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--music", type=Path, default=Path(r"D:\Download\音乐下载\MusicPlayer目录"))
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    assert not list(args.output.iterdir()), "Use an empty output directory"
    report = {"version": "0.5.0", "checks": []}

    fresh_dir = args.output / "fresh"
    fresh_dir.mkdir()
    # A JSON next to the database is deliberately invalid. The current version
    # must neither read it nor import any old library data.
    ignored_json = fresh_dir / "library.json"
    ignored_json.write_text("old JSON is unsupported", encoding="utf-8")
    fresh_db = fresh_dir / "library.sqlite"
    with Server(args.exe, fresh_db, fresh_dir, fresh_dir / "server.log", True) as server:
        assert server.call("tracks")[1] == []
        assert server.call("auth/keys")[1] == []
        assert server.call("health")[1]["revision"] == 0
    assert ignored_json.read_text(encoding="utf-8") == "old JSON is unsupported"
    report["fresh_database"] = inspect_database(fresh_db)
    report["checks"].append("fresh SQLite starts empty and never reads adjacent old JSON")

    live_dir = args.output / "live"
    media = live_dir / "media"
    media.mkdir(parents=True)
    names = ["麻雀 - 李荣浩.mp3", "她是什么样的 - 傅梦彤、陈麒名.flac", "Reply.wav"]
    for name in names:
        matches = list(args.music.rglob(name))
        assert matches, f"Missing real sample: {name}"
        shutil.copy2(matches[0], media / name)
    live_db = live_dir / "library.sqlite"
    with Server(args.exe, live_db, media, live_dir / "server.log") as server:
        tracks = server.call("tracks")[1]
        assert len(tracks) == 3
        by_format = {t["format"]: t for t in tracks}
        for track in tracks:
            assert track.get("artist") and track.get("embedded_lyrics") and track.get("lyrics")
            assert track.get("album") != media.name
        assert by_format["mp3"]["title"] == "麻雀"
        assert by_format["mp3"]["artist"] == "李荣浩"
        report["real_samples"] = [{k: t.get(k) for k in ("title", "artist", "album", "format", "lyrics_source", "embedded_lyrics")} |
                                  {"lyrics_characters": len(t["lyrics"])} for t in tracks]
        order = [by_format["wav"]["id"], by_format["mp3"]["id"]]
        server.call("playlists", "POST", {"id": "validation", "name": "验证歌单", "track_ids": order,
                                           "track_notes": {order[0]: "验证备注"}}, status=201)
        key = server.call("auth/keys", "POST", {"name": "验证播放器", "allowed_playlist_ids": ["validation"]}, status=201)[1]
        assert "revoked_at" not in key and "last_used_at" not in key and "expires_at" not in key
        assert len(server.call("tracks", key=key["secret"])[1]) == 2
        server.call("media/" + by_format["flac"]["id"], key=key["secret"], status=403)
        empty = server.call("auth/keys", "POST", {"name": "无授权"}, status=201)[1]
        assert server.call("tracks", key=empty["secret"])[1] == []
        server.call("playlists", key="invalid-key", status=401)
        _, data, headers = server.call("media/" + order[1], key=key["secret"], headers={"Range": "bytes=0-15"}, status=206)
        assert data == (media / names[0]).read_bytes()[:16]
        assert headers["Content-Range"].startswith("bytes 0-15/")
        before = server.call("health")[1]["revision"]
        boundary = "validation" + secrets.token_hex(8)
        body = (f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="different.mp3"\r\n'
                'Content-Type: audio/mpeg\r\n\r\n').encode() + (media / names[0]).read_bytes()
        body += (f'\r\n--{boundary}\r\nContent-Disposition: form-data; name="artist"\r\n\r\n上传修订歌手'
                 f'\r\n--{boundary}--\r\n').encode()
        uploaded = server.call("uploads", "POST", body, headers={"Content-Type": f"multipart/form-data; boundary={boundary}"}, status=201)[1]
        assert uploaded["title"] == "麻雀" and uploaded["artist"] == "上传修订歌手"
        assert uploaded["album"] == "麻雀" and uploaded["embedded_lyrics"]
        assert server.call("health")[1]["revision"] == before + 1
        server.call("tracks/" + order[1], "PATCH", {"artist": "验证人工歌手", "lyrics": "[00:01.00]验证手工歌词"})
        server.call("admin/scan", "POST")
        edited = server.call("tracks/" + order[1])[1]
        assert edited["artist"] == "验证人工歌手" and edited["lyrics_source"] == "manual"
        revision = server.call("health")[1]["revision"]
        unchanged = server.call(f"sync?since={revision}", key=key["secret"])[1]
        assert unchanged["changed_tracks"] == [] and unchanged["playlists"] == []
    with Server(args.exe, live_db, media, live_dir / "server.log", True) as server:
        assert server.call("health")[1]["revision"] == revision
        playlist = server.call("playlists/validation", key=key["secret"])[1]["playlist"]
        assert playlist["track_ids"] == order and playlist["track_notes"][order[0]] == "验证备注"
        edited = server.call("tracks/" + order[1], key=key["secret"])[1]
        assert edited["artist"] == "验证人工歌手" and edited["lyrics_source"] == "manual"
        assert server.call("tracks/" + uploaded["id"])[1]["artist"] == "上传修订歌手"
        assert "secret_hash" not in json.dumps(server.call("auth/keys")[1])
    report["live_database"] = inspect_database(live_db)
    report["checks"].extend(["real MP3/FLAC/WAV tags and embedded lyrics", "playlist grants and unauthorized media rejection",
                              "HTTP Range bytes", "atomic upload and original embedded title", "manual overrides after rescan",
                              "unchanged revision sync", "restart preserves key, playlist order/notes and track edits"])
    (args.output / "result.json").write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps(report, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
