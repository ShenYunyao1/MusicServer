"""Build an isolated schema-8 acceptance binary; never change release sources.

Go's source overlay applies only to this build. VCS identity is deliberately
disabled, so the result cannot pass the signed release identity gate.
"""
import argparse
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile


def preview_source(source, version=None):
    if len(re.findall(r"(?m)^[ \t]*Schema[ \t]*=[ \t]*7[ \t]*$", source)) != 1:
        raise ValueError("preview requires the unchanged schema-7 bridge source")
    source = re.sub(r"(?m)^([ \t]*Schema[ \t]*=[ \t]*)7([ \t]*)$", r"\g<1>8\2", source)
    if version is not None:
        if not re.fullmatch(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", version):
            raise ValueError("test version must have X.Y.Z format")
        source, count = re.subn(r'(?m)^([ \t]*Version[ \t]*=[ \t]*)"[0-9]+\.[0-9]+\.[0-9]+"', lambda m: m[1]+'"'+version+'"', source)
        if count != 1:
            raise ValueError("cannot replace the test version identity")
    return source


def build_preview(root, go, version=None):
    root = Path(root).resolve()
    destination = root / "build" / "schema-preview"
    destination.mkdir(parents=True, exist_ok=True)
    source = root / "internal" / "version" / "version.go"
    content = preview_source(source.read_text(encoding="utf-8"), version)
    name = "musicserver.exe" if os.environ.get("GOOS", "windows" if os.name == "nt" else "linux") == "windows" else "musicserver"
    binary = destination / name
    with tempfile.TemporaryDirectory(prefix="overlay-", dir=destination) as temporary:
        replacement = Path(temporary) / "version.go"
        replacement.write_text(content, encoding="utf-8")
        overlay = Path(temporary) / "overlay.json"
        overlay.write_text(json.dumps({"Replace": {str(source): str(replacement)}}), encoding="utf-8")
        subprocess.run([go, "build", "-trimpath", "-buildvcs=false", "-overlay", str(overlay),
                        "-o", str(binary), "./cmd/musicserver"], cwd=root, check=True)
    print("TEST PREVIEW ONLY: schema 8, no release VCS identity, " + str(binary))
    return binary


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go", default=shutil.which("go"))
    parser.add_argument("--version", help="optional isolated systemd test version; never a release identity")
    args = parser.parse_args()
    if not args.go:
        parser.error("Go must be on PATH or supplied with --go")
    build_preview(Path(__file__).resolve().parents[1], args.go, args.version)
