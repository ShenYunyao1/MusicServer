"""Check unsigned CI artifacts; never accepts them for automatic installation."""
import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import re
import tarfile
import zipfile


def require(ok, message):
    if not ok:
        raise ValueError(message)


def verify(directory, commit, version, schema):
    directory = Path(directory)
    require(not (directory / 'release-manifest.json.sig').exists(), 'development artifacts must be unsigned')
    manifest = json.loads((directory / 'release-manifest.json').read_text(encoding='utf-8'))
    require(manifest['format'] == 1 and manifest['commit'] == commit and manifest['version'] == version and manifest['schema'] == schema, 'manifest identity mismatch')
    expected = {'windows': 'musicserver-windows-amd64.zip', 'linux': 'musicserver-linux-amd64.tar.gz'}
    packages = manifest['packages']
    require(len(packages) == 2 and {p['os'] for p in packages} == set(expected), 'platform set mismatch')
    require({p.name for p in directory.iterdir()} == {'release-manifest.json', *expected.values()}, 'unexpected development assets')
    for package in packages:
        name = expected[package['os']]
        require(package['name'] == name and package['arch'] == 'amd64', 'fixed package name mismatch')
        data = (directory / name).read_bytes()
        require(len(data) == package['size'] and hashlib.sha256(data).hexdigest() == package['sha256'], 'package hash/size mismatch')
        if package['os'] == 'windows':
            with zipfile.ZipFile(directory / name) as archive:
                members = archive.infolist()
                names = [m.filename for m in members]
                require(all(not m.is_dir() and (m.external_attr >> 16 & 0o170000) in (0, 0o100000) for m in members), 'nonregular ZIP member')
                identity = json.loads(archive.read('build.json'))
            binaries = {'musicserver.exe'}
        else:
            with tarfile.open(directory / name, 'r:gz') as archive:
                members = archive.getmembers()
                names = [m.name for m in members]
                require(all(m.isfile() for m in members), 'nonregular TAR member')
                identity = json.load(archive.extractfile('build.json'))
            binaries = {'musicserver', 'musicserver-updater'}
        require(len(names) == len(set(names)), 'duplicate archive member')
        for member in names:
            parts = PurePosixPath(member).parts
            require(parts and not member.startswith('/') and '\\' not in member and ':' not in member and '..' not in parts, 'unsafe archive path')
            require(member in {'README.md', 'build.json', *binaries} or member.startswith('deploy/'), 'unexpected package member')
        require(binaries <= set(names), 'missing binary')
        for key in ['version', 'commit', 'schema', 'migrate_from']:
            require(identity.get(key) == manifest.get(key), 'archive build identity mismatch: ' + key)
    print('Unsigned development packages verified; not installable by the production updater.')


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('directory')
    parser.add_argument('--commit', required=True)
    parser.add_argument('--version', required=True)
    args = parser.parse_args()
    source = Path('internal/version/version.go').read_text(encoding='utf-8')
    schema = int(re.search(r'(?m)^\s*Schema\s*=\s*(\d+)', source)[1])
    verify(args.directory, args.commit, args.version, schema)
