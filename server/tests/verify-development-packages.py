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
    families = [('release-manifest.json', 'musicserver'), ('pikmelo-server-release-manifest.json', 'pikmelo-server')]
    expected_assets = {name for name, _ in families}
    for _, prefix in families:
        expected_assets.update({prefix + '-windows-amd64.zip', prefix + '-linux-amd64.tar.gz'})
    require(not any(path.suffix == '.sig' for path in directory.iterdir()), 'development artifacts must be unsigned')
    require({p.name for p in directory.iterdir()} == expected_assets, 'unexpected development assets')
    manifests = [verify_manifest(directory, name, prefix, commit, version, schema) for name, prefix in families]
    require(manifests[0].get('migrate_from') == manifests[1].get('migrate_from'), 'manifest migration identity mismatch')
    print('Pikmelo Server unsigned distribution and compatibility packages verified.')


def verify_manifest(directory, manifest_name, prefix, commit, version, schema):
    manifest = json.loads((directory / manifest_name).read_text(encoding='utf-8'))
    require(manifest['format'] == 1 and manifest['commit'] == commit and manifest['version'] == version and manifest['schema'] == schema, 'manifest identity mismatch')
    expected = {'windows': prefix + '-windows-amd64.zip', 'linux': prefix + '-linux-amd64.tar.gz'}
    packages = manifest['packages']
    require(len(packages) == 2 and {p['os'] for p in packages} == set(expected), 'platform set mismatch')
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
                start = archive.read('start.bat').decode('utf-8')
                require('%~dp0' + prefix + '.exe' in start and '%~dp0build' not in start and 'pause' in start, 'packaged startup entry mismatch')
            binaries = {prefix + '.exe'}
        else:
            with tarfile.open(directory / name, 'r:gz') as archive:
                members = archive.getmembers()
                names = [m.name for m in members]
                require(all(m.isfile() for m in members), 'nonregular TAR member')
                identity = json.load(archive.extractfile('build.json'))
            binaries = {prefix, prefix + '-updater'}
        require(len(names) == len(set(names)), 'duplicate archive member')
        for member in names:
            parts = PurePosixPath(member).parts
            require(parts and not member.startswith('/') and '\\' not in member and ':' not in member and '..' not in parts, 'unsafe archive path')
            require(member in {'README.md', 'build.json', *binaries} or package['os'] == 'windows' and member == 'start.bat' or member.startswith('deploy/'), 'unexpected package member')
        require(binaries <= set(names), 'missing binary')
        for key in ['version', 'commit', 'schema', 'migrate_from']:
            require(identity.get(key) == manifest.get(key), 'archive build identity mismatch: ' + key)
    return manifest


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('directory')
    parser.add_argument('--commit', required=True)
    parser.add_argument('--version', required=True)
    args = parser.parse_args()
    source = Path('internal/version/version.go').read_text(encoding='utf-8')
    schema = int(re.search(r'(?m)^\s*Schema\s*=\s*(\d+)', source)[1])
    verify(args.directory, args.commit, args.version, schema)
