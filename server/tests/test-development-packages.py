import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
import zipfile

spec = importlib.util.spec_from_file_location('development_packages', Path(__file__).with_name('verify-development-packages.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class DevelopmentPackages(unittest.TestCase):
    def fixture(self, root, wrong_identity=False):
        for manifest_name, prefix in [('release-manifest.json', 'musicserver'), ('pikmelo-server-release-manifest.json', 'pikmelo-server')]:
            self.fixture_family(root, manifest_name, prefix, wrong_identity)

    def fixture_family(self, root, manifest_name, prefix, wrong_identity):
        identity = dict(format=1, version='0.0.1', commit='a'*40, schema=9, migrate_from=[8])
        packaged = dict(identity, commit='b'*40) if wrong_identity else identity
        packages = []
        for platform, name, binaries in [('windows', prefix + '-windows-amd64.zip', [prefix + '.exe']), ('linux', prefix + '-linux-amd64.tar.gz', [prefix, prefix + '-updater'])]:
            entries = {'build.json': json.dumps(packaged).encode(), **{b: b'compiled fixture' for b in binaries}}
            if platform == 'windows':
                entries['start.bat'] = ('@echo off\n"%~dp0' + prefix + '.exe" %*\npause\n').encode()
                with zipfile.ZipFile(root/name, 'w') as archive:
                    for entry, data in entries.items(): archive.writestr(entry, data)
            else:
                with tarfile.open(root/name, 'w:gz') as archive:
                    for entry, data in entries.items():
                        info = tarfile.TarInfo(entry); info.size = len(data)
                        archive.addfile(info, io.BytesIO(data))
            data = (root/name).read_bytes()
            packages.append(dict(os=platform, arch='amd64', name=name, size=len(data), sha256=hashlib.sha256(data).hexdigest()))
        (root/manifest_name).write_text(json.dumps(dict(identity, packages=packages)), encoding='utf-8')

    def test_valid_and_tampered(self):
        for prefix in ['musicserver', 'pikmelo-server']:
            with self.subTest(prefix=prefix), tempfile.TemporaryDirectory() as directory:
                root = Path(directory); self.fixture(root)
                module.verify(root, 'a'*40, '0.0.1', 9)
                with (root/(prefix + '-windows-amd64.zip')).open('ab') as output: output.write(b'tamper')
                with self.assertRaisesRegex(ValueError, 'hash/size'): module.verify(root, 'a'*40, '0.0.1', 9)

    def test_wrong_archive_identity(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory); self.fixture(root, True)
            with self.assertRaisesRegex(ValueError, 'build identity'): module.verify(root, 'a'*40, '0.0.1', 9)

    def test_development_signature_is_forbidden(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory); self.fixture(root)
            (root/'release-manifest.json.sig').write_text('unexpected', encoding='utf-8')
            with self.assertRaisesRegex(ValueError, 'unsigned'): module.verify(root, 'a'*40, '0.0.1', 9)


if __name__ == '__main__': unittest.main()
