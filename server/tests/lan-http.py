"""Verify a real wildcard HTTP listener using isolated accounts and data."""
import argparse
import http.cookiejar
import json
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--lan-ip", required=True)
    args = parser.parse_args()
    binary = args.binary.resolve()
    with tempfile.TemporaryDirectory(prefix="musicserver-lan-") as temporary:
        runtime = Path(temporary)
        password = "isolated LAN administrator password"
        initialize = subprocess.run(
            [sys.executable, str(Path(__file__).with_name("admin-terminal.py")),
             str(binary), str(runtime / "library.sqlite"), "admin"],
            input=password + "\n", capture_output=True, text=True, timeout=35)
        assert initialize.returncode == 0, initialize.stdout + initialize.stderr
        with socket.socket() as listener:
            listener.bind(("0.0.0.0", 0))
            port = listener.getsockname()[1]
        with (runtime / "server.log").open("wb") as log:
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}),
                                               urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
            base = f"http://{args.lan_ip}:{port}"

            def call(path, method="GET", body=None, origin=None, csrf=None, host=None):
                headers = {"Content-Type": "application/json", "Origin": base if origin is None else origin}
                if csrf is not None:
                    headers["X-CSRF-Token"] = csrf
                if host is not None:
                    headers["Host"] = host
                request = urllib.request.Request(base + "/api/v2/" + path,
                                                 None if body is None else json.dumps(body).encode(), headers, method=method)
                try:
                    with opener.open(request, timeout=2) as response:
                        data = response.read()
                        return response.status, json.loads(data) if data else None
                except urllib.error.HTTPError as error:
                    return error.code, json.loads(error.read())

            process = subprocess.Popen([str(binary), "-addr", "0.0.0.0:" + str(port),
                                        "-media-root", str(runtime), "-data", str(runtime / "library.sqlite"), "-no-scan"],
                                       stdout=log, stderr=log, creationflags=subprocess.CREATE_NO_WINDOW)
            try:
                for _ in range(100):
                    try:
                        if call("health")[0] == 200:
                            break
                    except OSError:
                        pass
                    if process.poll() is not None:
                        raise AssertionError((runtime / "server.log").read_text())
                    time.sleep(.1)
                else:
                    raise AssertionError("LAN HTTP startup timed out")
                with opener.open(f"http://127.0.0.1:{port}/api/v2/health", timeout=2) as response:
                    assert response.status == 200
                assert call("tracks")[0] == 401
                assert call("health", host="attacker.example")[0] == 403
                credentials = {"username": "admin", "password": password}
                assert call("auth/login", "POST", credentials, origin="http://attacker.example")[0] == 403
                status, identity = call("auth/login", "POST", credentials)
                assert status == 200, identity
                assert call("auth/me")[0] == 200
                assert call("auth/activity", "POST")[0] == 403
                assert call("auth/activity", "POST", csrf=identity["csrf_token"])[0] == 204
                print("LAN HTTP passed: real 0.0.0.0 listener, loopback/LAN-IP access, authentication, Host/Origin and CSRF protection.")
            finally:
                if process.poll() is None:
                    process.terminate()
                    process.wait(10)


if __name__ == "__main__":
    main()
