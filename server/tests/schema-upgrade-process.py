"""Exercise real schema-7 -> schema-8 binaries against an isolated populated DB."""
import argparse
from contextlib import closing
import hashlib
import http.cookiejar
import io
import json
import os
from pathlib import Path
import socket
import sqlite3
import subprocess
import tempfile
import time
import urllib.request
import urllib.error
import uuid
import wave


class Account:
    def __init__(self, base):
        self.base, self.csrf = base, ''
        self.http = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def call(self, path, method='GET', body=None):
        req = urllib.request.Request(self.base+'/api/v2/'+path,
            None if body is None else json.dumps(body).encode(),
            {'Origin': self.base,'X-CSRF-Token': self.csrf,'Content-Type': 'application/json'},method=method)
        with self.http.open(req, timeout=10) as response:
            data=response.read()
            return json.loads(data) if data else None

    def login(self, name, password):
        identity=self.call('auth/login','POST',{'username':name,'password':password})
        self.csrf=identity['csrf_token']
        return identity


def wait_for(action, process):
    deadline=time.monotonic()+30
    while time.monotonic()<deadline:
        if process.poll() is not None: raise AssertionError('fixture process exited before readiness')
        try:
            result=action()
            if result: return result
        except (OSError,urllib.error.URLError): pass
        time.sleep(.05)
    raise AssertionError('fixture did not become ready')


def snapshot(database):
    with closing(sqlite3.connect(database)) as db:
        names=[r[0] for r in db.execute("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")]
        result={}
        for name in names:
            if name=='sessions': continue  # Normal startup revokes old sessions; passwords/Keys survive.
            quoted='"'+name.replace('"','""')+'"'
            where=" WHERE key!='schema_version'" if name=='meta' else ''
            result[name]=sorted(db.execute('SELECT * FROM '+quoted+where).fetchall(),key=repr)
        return result


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--old-server',type=Path,required=True)
    parser.add_argument('--new-server',type=Path,required=True)
    args=parser.parse_args()
    old,new=args.old_server.resolve(),args.new_server.resolve()
    terminal=Path(__file__).with_name('admin-terminal.py')
    with tempfile.TemporaryDirectory(prefix='music-schema-upgrade-') as temp:
        root=Path(temp);media=root/'media';media.mkdir();database=root/'library.sqlite'
        audio=io.BytesIO()
        with wave.open(audio,'wb') as wav:
            wav.setparams((1,2,8000,0,'NONE','not compressed'));wav.writeframes(b'\x01\x00'*800)
        content=audio.getvalue();(media/'retained.wav').write_bytes(content)
        password='isolated upgrade administrator'
        init=subprocess.run([os.sys.executable,str(terminal),str(old),str(database),'admin'],input=password+'\n',capture_output=True,text=True,timeout=35)
        assert init.returncode==0,('isolated administrator initialization failed: '+init.stdout+init.stderr).replace(password,'<fixture-password>')
        process=None
        def start(binary,no_scan):
            with socket.socket() as listener:
                listener.bind(('127.0.0.1',0));port=listener.getsockname()[1]
            command=[str(binary),'-addr',f'127.0.0.1:{port}','-data',str(database),'-media-root',str(media)]
            if no_scan: command.append('-no-scan')
            log=(root/'fixture.log').open('ab')
            child=subprocess.Popen(command,stdout=log,stderr=log);log.close()
            base=f'http://127.0.0.1:{port}'
            try:
                health=wait_for(lambda:json.load(urllib.request.urlopen(base+'/api/v2/health',timeout=2)),child)
                return child,Account(base),health
            except BaseException:
                child.terminate();child.wait(timeout=10);raise
        def stop(child):
            child.terminate();child.wait(timeout=10)
        try:
            process,admin,health=start(old,False);assert health['schema']==7
            admin.login('admin',password)
            tracks=wait_for(lambda:admin.call('tracks'),process);assert len(tracks)==1
            track=tracks[0];assert track['content_hash']==hashlib.sha256(content).hexdigest()
            playlist=admin.call('playlists','POST',{'id':str(uuid.uuid4()),'name':'Preserved playlist','track_ids':[track['id']],'track_notes':{track['id']:'preserved cue'}})
            key=admin.call('auth/keys','POST',{'name':'Retained player','scopes':['library:read','media:read'],'allowed_playlist_ids':[playlist['id']]})
            stop(process);process=None
            before=snapshot(database)
            process,upgraded,health=start(new,True);assert health['schema']==8
            after=snapshot(database)
            for table,rows in before.items(): assert after[table]==rows,'old rows changed in '+table
            backups=list(root.glob('.schema-7-to-8-*/snapshot.sqlite'))
            assert len(backups)==1,'startup migration must leave one independent old-format snapshot'
            assert snapshot(backups[0])==before,'pre-migration snapshot lost old table data'
            with closing(sqlite3.connect(backups[0])) as db:
                assert db.execute('PRAGMA user_version').fetchone()[0]==7
            with closing(sqlite3.connect(database)) as db:
                assert db.execute('PRAGMA user_version').fetchone()[0]==8
                assert db.execute('PRAGMA foreign_key_check').fetchall()==[]
                assert db.execute('SELECT count(*) FROM schema_migrations').fetchone()[0]==1
            upgraded.login('admin',password)
            assert any(t['id']==track['id'] for t in upgraded.call('tracks'))
            req=urllib.request.Request(upgraded.base+'/api/v2/media/'+track['id'],headers={'X-API-Key':key['secret']})
            with urllib.request.urlopen(req,timeout=10) as response: assert response.read()==content
            assert upgraded.call('playlists/'+playlist['id'])['playlist']['track_notes'][track['id']]=='preserved cue'
            assert len(upgraded.call('tag-groups'))==6
            stop(process);process=None
            process,restarted,health=start(new,True);assert health['schema']==8
            assert list(root.glob('.schema-7-to-8-*/snapshot.sqlite'))==backups,'schema-8 restart created another migration snapshot'
            restarted.login('admin',password)
            with closing(sqlite3.connect(database)) as db: assert db.execute('SELECT count(*) FROM schema_migrations').fetchone()[0]==1
            print('PASS real binary 7->8: all old table rows, password, Key, media UUID/hash, playlist notes and repeated startup')
        finally:
            if process is not None: stop(process)


if __name__=='__main__': main()
