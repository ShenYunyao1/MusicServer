"""Exercise real Docker Compose volumes, uploads, upgrades and snapshot rollback."""
import argparse
import http.cookiejar
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid
import wave

SOURCE = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('schema_upgrade', SOURCE / 'tests/schema-upgrade-process.py')
upgrade = importlib.util.module_from_spec(spec)
spec.loader.exec_module(upgrade)


def docker(*args, check=True, env=None):
    result = subprocess.run([DOCKER, *map(str, args)], capture_output=True, text=True,
                            encoding='utf-8', errors='replace', env=env, timeout=240)
    if check and result.returncode:
        raise AssertionError('Docker command failed: ' + result.stderr[-6000:])
    return result


def account(base):
    client = upgrade.Account(base)
    client.http = urllib.request.build_opener(urllib.request.ProxyHandler({}),
                    urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    return client


def patch_user(client, user):
    request = urllib.request.Request(client.base + '/api/v2/admin/users/' + user['id'],
        json.dumps({'email_verification_required': False}).encode(),
        {'Origin': client.base, 'X-CSRF-Token': client.csrf, 'If-Match': str(user['version']),
         'Content-Type': 'application/json'}, method='PATCH')
    with client.http.open(request, timeout=10) as response:
        assert response.status == 200


def upload(client):
    stream = io.BytesIO()
    with wave.open(stream, 'wb') as audio:
        audio.setparams((1, 2, 8000, 0, 'NONE', 'not compressed'))
        audio.writeframes(bytes(16000))
    content = stream.getvalue()
    boundary = 'pikmelo-' + uuid.uuid4().hex
    body = b''
    for field, filename, mime, data in [
        ('file', 'Container.wav', 'audio/wav', content),
        ('lyrics_file', 'Container.lrc', 'text/plain', b'[00:00]Container lyrics\n'),
    ]:
        body += (f'--{boundary}\r\nContent-Disposition: form-data; name="{field}"; filename="{filename}"\r\n'
                 f'Content-Type: {mime}\r\n\r\n').encode() + data + b'\r\n'
    body += f'--{boundary}--\r\n'.encode()
    request = urllib.request.Request(client.base + '/api/v2/uploads/stage', body,
        {'Origin': client.base, 'X-CSRF-Token': client.csrf,
         'Content-Type': 'multipart/form-data; boundary=' + boundary}, method='POST')
    try:
        with client.http.open(request, timeout=30) as response:
            stage = json.load(response)
    except urllib.error.HTTPError as error:
        summary = client.call('admin/storage')
        print('Storage diagnostics', {key: summary.get(key) for key in ['policy', 'data_disk', 'media_disk']})
        raise AssertionError('Upload rejected: ' + error.read(4096).decode('utf-8')) from error
    result = client.call('uploads/stage/' + stage['id'] + '/commit', 'POST', {})
    return result['track'], content


def wait_health(base, schema):
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    deadline = time.monotonic() + 45
    while time.monotonic() < deadline:
        try:
            with opener.open(base + '/api/v2/health', timeout=2) as response:
                health = json.load(response)
            if health['status'] == 'ok' and health['schema'] == schema:
                return health
        except (OSError, urllib.error.URLError):
            pass
        time.sleep(.2)
    raise AssertionError('Container health did not reach expected schema')


def main(args):
    project = 'pikmelo-test-' + uuid.uuid4().hex[:12]
    data, backup, restored = [project + suffix for suffix in ('-data', '-backup', '-restored')]
    volumes = [data, data + '-tmp', backup, restored, restored + '-tmp']
    environment = dict(os.environ, PIKMELO_IMAGE=args.image, PIKMELO_DATA_VOLUME=data, PIKMELO_PORT='0')
    fixture_image = 'pikmelo-server:fixture-' + uuid.uuid4().hex[:12]
    baseline = args.image
    directory = Path(tempfile.mkdtemp(prefix='docker-', dir=SOURCE / 'build'))
    override = directory / 'override.json'
    override.write_text(json.dumps({'services': {'pikmelo': {'healthcheck': {
        'interval': '1s', 'start_period': '1s', 'timeout': '5s', 'retries': 30}}}}), encoding='utf-8')

    def compose(*commands, check=True):
        return docker('compose', '-p', project, '-f', SOURCE / 'compose.yaml', '-f', override,
                      *commands, check=check, env=environment)

    def start(image, schema, check=True):
        environment['PIKMELO_IMAGE'] = image
        result = compose('up', '-d', '--no-build', '--wait', '--wait-timeout', '60', check=check)
        port = compose('port', 'pikmelo', '8080').stdout.strip().rsplit(':', 1)[1]
        base = 'http://127.0.0.1:' + port
        return base, wait_health(base, schema), result

    def helper(*command, mounts):
        arguments = ['run', '--rm', '--network', 'none', '--user', '0']
        for mount in mounts:
            arguments += ['--mount', mount]
        return docker(*arguments, '--entrypoint', command[0], args.image, *command[1:])

    def verify(base, schema, track, playlist, key, content):
        wait_health(base, schema)
        client = account(base)
        client.login('admin', password)
        assert any(item['id'] == track['id'] for item in client.call('tracks'))
        assert client.call('playlists/' + playlist['id'])['playlist']['name'] == 'Container playlist'
        request = urllib.request.Request(base + '/api/v2/media/' + track['id'], headers={'X-API-Key': key})
        with client.http.open(request, timeout=10) as response:
            assert response.read() == content
        return client

    password = 'isolated container administrator password'
    try:
        if args.schema8_binary:
            shutil.copyfile(args.schema8_binary, directory / 'baseline')
            (directory / 'Dockerfile').write_text(
                'ARG BASE_IMAGE\nFROM ${BASE_IMAGE}\nUSER root\n'
                'COPY --chmod=755 baseline /usr/local/bin/pikmelo-server\nUSER 10001:10001\n', encoding='utf-8')
            docker('build', '--build-arg', 'BASE_IMAGE=' + args.image, '-t', fixture_image, directory)
            baseline = fixture_image
        initial_schema = 8 if args.schema8_binary else 9
        base, _, _ = start(baseline, initial_schema)
        container = compose('ps', '-q', 'pikmelo').stdout.strip()
        config = json.loads(docker('inspect', container).stdout)[0]
        assert config['Config']['User'] == '10001:10001'
        assert config['HostConfig']['ReadonlyRootfs']
        assert all(mount['Destination'] != '/var/run/docker.sock' for mount in config['Mounts'])
        assert compose('exec', '-T', 'pikmelo', 'ffprobe', '-version').returncode == 0
        token = compose('exec', '-T', 'pikmelo', 'cat', '/data/data/setup-token').stdout.strip()
        client = account(base)
        client.call('setup', 'POST', {'token': token, 'username': 'admin', 'password': password, 'confirm_password': password})
        identity = client.login('admin', password)
        patch_user(client, identity['user'])
        status = client.call('admin/update')
        assert status['state'] == 'external' and not status['can_install']
        for path in ['admin/update/check', 'admin/update/apply']:
            try:
                client.call(path, 'POST', {'version': '9.0.0'})
                raise AssertionError('Container accepted a native update request')
            except urllib.error.HTTPError as error:
                assert error.code == 409
        if args.browser_test:
            subprocess.run(['node', str(SOURCE / 'tests/docker-ui.cjs')],
                input=json.dumps({'base': base, 'password': password}),
                text=True, encoding='utf-8', check=True, timeout=60)
        track, content = upload(client)
        playlist = client.call('playlists', 'POST', {'id': str(uuid.uuid4()), 'name': 'Container playlist', 'track_ids': [track['id']]})
        key = client.call('auth/keys', 'POST', {'name': 'Container fixture', 'scopes': ['library:read', 'media:read'], 'allowed_playlist_ids': [playlist['id']]})['secret']
        compose('stop')
        compose('rm', '-f')
        base, _, _ = start(baseline, initial_schema)
        verify(base, initial_schema, track, playlist, key, content)
        print('PASS non-root read-only runtime, ffprobe, setup, upload, API boundary and recreation persistence')
        compose('stop')
        helper('tar', '-C', '/source', '-cpf', '/backup/pre-upgrade.tar', '.', mounts=[
            f'type=volume,src={data},dst=/source,readonly', f'type=volume,src={backup},dst=/backup'])
        base, health, _ = start(args.image, 9)
        if args.commit:
            assert health['commit'] == args.commit and health['modified'] is False
        verify(base, 9, track, playlist, key, content)
        print('PASS controlled image recreation and schema upgrade with retained accounts, Key, playlist and media')
        compose('stop')
        override.write_text(json.dumps({'services': {'pikmelo': {'healthcheck': {
            'test': ['CMD', 'false'], 'interval': '1s', 'start_period': '0s', 'timeout': '2s', 'retries': 1}}}}), encoding='utf-8')
        _, _, failed = start(args.image, 9, check=False)
        assert failed.returncode != 0, 'Unhealthy upgrade unexpectedly passed Compose --wait'
        compose('stop')
        helper('tar', '-C', '/restore', '-xpf', '/backup/pre-upgrade.tar', mounts=[
            f'type=volume,src={restored},dst=/restore', f'type=volume,src={backup},dst=/backup,readonly'])
        override.write_text(json.dumps({'services': {'pikmelo': {'healthcheck': {
            'interval': '1s', 'start_period': '1s', 'timeout': '5s', 'retries': 30}}}}), encoding='utf-8')
        environment['PIKMELO_DATA_VOLUME'] = restored
        base, _, _ = start(baseline, initial_schema)
        verify(base, initial_schema, track, playlist, key, content)
        print('PASS failed health gate and rollback using old image plus restored pre-upgrade volume')
    except Exception:
        print(compose('exec', '-T', 'pikmelo', 'df', '-B1', '/data', '/tmp', check=False).stdout)
        print(compose('logs', '--tail', '60', check=False).stdout)
        raise
    finally:
        compose('down', '--remove-orphans', check=False)
        for name in volumes:
            assert name.startswith(project + '-')
            docker('volume', 'rm', name, check=False)
        if args.schema8_binary:
            docker('image', 'rm', fixture_image, check=False)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--docker', default=shutil.which('docker'))
    parser.add_argument('--image', required=True)
    parser.add_argument('--schema8-binary', type=Path)
    parser.add_argument('--commit')
    parser.add_argument('--browser-test', action='store_true')
    arguments = parser.parse_args()
    if not arguments.docker:
        parser.error('Docker CLI must be on PATH or supplied using --docker')
    DOCKER = arguments.docker
    (SOURCE / 'build').mkdir(exist_ok=True)
    main(arguments)
