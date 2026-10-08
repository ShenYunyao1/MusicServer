'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const crypto = require('node:crypto');

const htmlPath = path.join(__dirname, '../internal/music/index.html');
const vendorPath = path.join(__dirname, '../internal/music/vendor/jsmediatags-3.9.7.min.js');

function harness() {
  const html = fs.readFileSync(htmlPath, 'utf8');
  const script = html.match(/<script>([\s\S]*?)<\/script>/)?.[1];
  assert.ok(script, 'management page must contain its application script');
  const eventOffset = script.indexOf("$('refreshStats').onclick");
  assert.ok(eventOffset > 0, 'application event bindings must follow its functions');
  const urls = new Map();
  const revoked = [];
  const inputs = [];
  const elements = new Map();
  let urlCount = 0;
  let fetchCount = 0;
  const context = vm.createContext({
    TextDecoder,
    Uint8Array,
    Blob,
    FormData,
    FileReader: class {
      readAsArrayBuffer(blob) {
        blob.arrayBuffer().then(result => {
          this.result = result;
          this.onload?.({ target: this });
          this.onloadend?.({ target: this });
        }, error => {
          this.error = error;
          this.onerror?.({ target: this });
          this.onloadend?.({ target: this });
        });
      }
    },
    crypto,
    setTimeout,
    clearTimeout,
    document: {
      getElementById(id) {
        if (!elements.has(id)) elements.set(id, { value: '', textContent: '' });
        return elements.get(id);
      },
      createElement(tag) {
        assert.equal(tag, 'input', 'queue tests should not depend on rendered DOM');
        const element = { files: [], click() {} };
        inputs.push(element);
        return element;
      },
    },
    URL: {
      createObjectURL(blob) {
        const url = `blob:test-${++urlCount}`;
        urls.set(url, blob);
        return url;
      },
      revokeObjectURL(url) {
        revoked.push(url);
        urls.delete(url);
      },
    },
    fetch() {
      fetchCount++;
      throw new Error('local preview unexpectedly requested the server');
    },
  });
  vm.runInContext(fs.readFileSync(vendorPath, 'utf8'), context, { filename: vendorPath });
  vm.runInContext(script.slice(0, eventOffset), context, { filename: htmlPath });
  vm.runInContext(
    'renderUploads = () => {}; localDuration = async () => 0; globalThis.queueState = state;',
    context,
  );
  return { context, state: context.queueState, urls, revoked, inputs, fetchCount: () => fetchCount };
}

function syncsafe(value) {
  return Buffer.from([(value >>> 21) & 127, (value >>> 14) & 127, (value >>> 7) & 127, value & 127]);
}

function frame(id, payload, major = 3) {
  payload = Buffer.from(payload);
  const size = Buffer.alloc(major === 2 ? 3 : 4);
  if (major === 2) size.writeUIntBE(payload.length, 0, 3);
  else if (major === 4) syncsafe(payload.length).copy(size);
  else size.writeUInt32BE(payload.length);
  return Buffer.concat([Buffer.from(id), size, major === 2 ? Buffer.alloc(0) : Buffer.alloc(2), payload]);
}

function tag(frames, major = 3, flags = 0, extension = Buffer.alloc(0)) {
  const payload = Buffer.concat([extension, ...frames]);
  return Buffer.concat([Buffer.from('ID3'), Buffer.from([major, 0, flags]), syncsafe(payload.length), payload]);
}

function encoded(value, encoding) {
  if (encoding === 0) return Buffer.from(value, 'latin1');
  if (encoding === 3) return Buffer.from(value, 'utf8');
  const bytes = Buffer.from(value, 'utf16le');
  return encoding === 1 ? Buffer.concat([Buffer.from([255, 254]), bytes]) : bytes.swap16();
}

function text(value, encoding = 3) {
  return Buffer.concat([Buffer.from([encoding]), encoded(value, encoding)]);
}

function described(value, description = '', encoding = 3, language = true) {
  return Buffer.concat([
    Buffer.from([encoding]),
    language ? Buffer.from('eng') : Buffer.alloc(0),
    encoded(description, encoding),
    Buffer.alloc(encoding === 1 || encoding === 2 ? 2 : 1),
    encoded(value, encoding),
  ]);
}

const jpeg = Buffer.from([255, 216, 255, 224, 0, 16, 74, 70, 73, 70, 255, 217]);

function picture(major = 3, mime = 'image/jpeg') {
  return Buffer.concat([
    Buffer.from([0]),
    Buffer.from(major === 2 ? 'JPG' : mime + '\0'),
    Buffer.from([3, 0]),
    jpeg,
  ]);
}

function file(name, bytes = Buffer.alloc(0), relativePath = '') {
  const blob = new Blob([bytes]);
  blob.name = name;
  blob.lastModified = 1;
  blob.webkitRelativePath = relativePath;
  return blob;
}

function uploadFixture(context, name = 'test.mp3') {
  return {
    ...context.describeFile(file(name), 1),
    title: '', artist: '', album: '', genre: '', comment: '', lyrics: '',
    edits: {}, preview: null, localPreview: null, localPromise: null,
    localStatus: '', stageId: null, status: '待暂存',
    lyricsFile: null, coverFile: null,
  };
}

function audioResult(overrides = {}) {
  return {
    track: {
      title: 'Tagged title', artist: 'Tagged artist', album: 'Tagged album',
      genre: '', comment: '', lyrics: '', embedded_lyrics: false,
      cover_id: '', cover_source: '', ...overrides,
    },
    note: '',
  };
}

function deferred() {
  let resolve;
  const promise = new Promise(done => { resolve = done; });
  return { promise, resolve };
}

async function settle() {
  await new Promise(resolve => setImmediate(resolve));
}

for (const major of [2, 3, 4]) {
  test(`ID3v2.${major} displays the standard metadata fields`, async () => {
    const { context } = harness();
    const ids = major === 2 ? ['TT2', 'TP1', 'TAL', 'TCO'] : ['TIT2', 'TPE1', 'TALB', 'TCON'];
    const values = ['\u6b4c\u66f2\u6807\u9898', '\u827a\u672f\u5bb6', '\u4e13\u8f91', 'Rock'];
    const result = await context.parseLocalID3(tag(ids.map((id, index) => frame(id, text(values[index]), major)), major));
    for (const [index, field] of ['title', 'artist', 'album', 'genre'].entries()) assert.equal(result[field], values[index]);
  });
}

for (const encoding of [0, 1, 2, 3]) {
  test(`text encoding ${encoding} decodes without BOM or terminator artifacts`, async () => {
    const { context } = harness();
    const value = encoding === 0 ? 'Bj\u00f6rk' : '\u5468\u6770\u4f26';
    const result = await context.parseLocalID3(tag([frame('TPE1', text(value, encoding))]));
    assert.equal(result.artist, value);
  });
}

test('UTF-16BE BOM inside the UTF-16 encoding is honored', async () => {
  const { context } = harness();
  const payload = Buffer.concat([Buffer.from([1, 254, 255]), encoded('\u97f3\u4e50', 2)]);
  assert.equal((await context.parseLocalID3(tag([frame('TIT2', payload)]))).title, '\u97f3\u4e50');
});

test('USLT and v2.2 ULT expose embedded lyrics rather than their description', async () => {
  const { context } = harness();
  for (const major of [2, 3, 4]) {
    const body = '[00:01.00]\u6b4c\u8bcd\r\n[00:02.00]\u7b2c\u4e8c\u884c';
    const result = await context.parseLocalID3(tag([frame(major === 2 ? 'ULT' : 'USLT', described(body, 'description', 1), major)], major));
    assert.equal(result.lyrics, body.replace(/\r\n/g, '\n'));
    assert.equal(result.embedded_lyrics, true);
    assert.match(result.lyrics_source, /ID3v2\/(USLT|ULT)/);
  }
});

test('UTF-16BE USLT inherits the description BOM for lyrics without a repeated BOM', async () => {
  const { context } = harness();
  const body = '[00:00.00]\u4f60\u597d';
  const payload = Buffer.concat([
    Buffer.from([1]), Buffer.from('eng'), Buffer.from([254, 255]),
    encoded('description', 2), Buffer.alloc(2), encoded(body, 2),
  ]);
  const result = await context.parseLocalID3(tag([frame('USLT', payload)]));
  assert.equal(result.lyrics, body);
  assert.equal(result.embedded_lyrics, true);
});

test('TXXX lyrics and COMM descriptions decode independently', async () => {
  const { context } = harness();
  const result = await context.parseLocalID3(tag([
    frame('COMM', described('My comment', 'short description', 1)),
    frame('TXXX', described('[00:00.00]Lyrics', 'LYRICS', 1, false)),
  ]));
  assert.equal(result.comment, 'My comment');
  assert.equal(result.lyrics, '[00:00.00]Lyrics');
  assert.equal(result.embedded_lyrics, true);
});

test('v2.2 TXX user description is recognized as embedded lyrics', async () => {
  const { context } = harness();
  const result = await context.parseLocalID3(tag([
    frame('TXX', described('[00:00.00]Legacy lyrics', 'LYRICS', 1, false), 2),
  ], 2));
  assert.equal(result.lyrics, '[00:00.00]Legacy lyrics');
  assert.equal(result.embedded_lyrics, true);
  assert.equal(result.lyrics_source, 'ID3v2/TXX');
});

test('SYLT/SLT timed text does not claim plain embedded lyrics when USLT is absent', async () => {
  const { context } = harness();
  // SYLT: UTF-8, English, timestamp format 2, content type 1, description, text, timestamp.
  const sylt = Buffer.concat([
    Buffer.from([3]), Buffer.from('eng'), Buffer.from([2, 1]), Buffer.from('LYRICS\0'),
    Buffer.from('Timed lyric\0'), Buffer.from([0, 0, 0, 1]),
  ]);
  for (const id of ['SYLT', 'SLT']) {
    const result = await context.parseLocalID3(tag([frame(id, sylt, id === 'SLT' ? 2 : 3)], id === 'SLT' ? 2 : 3));
    assert.equal(result.lyrics, '', `${id} should require a server-confirmed lyric conversion`);
    assert.equal(result.embedded_lyrics, false);
    assert.equal(result.lyrics_confirmed, false);
    const major = id === 'SLT' ? 2 : 3;
    const withPlainLyrics = await context.parseLocalID3(tag([
      frame(id, sylt, major),
      frame(major === 2 ? 'ULT' : 'USLT', described('Plain lyrics'), major),
    ], major));
    assert.equal(withPlainLyrics.lyrics, 'Plain lyrics');
    assert.equal(withPlainLyrics.lyrics_confirmed, true);
    assert.equal(withPlainLyrics.embedded_lyrics, true);
  }
});

test('APIC and v2.2 PIC return the original JPEG and normalized MIME', async () => {
  const { context } = harness();
  for (const major of [2, 3, 4]) {
    const result = await context.parseLocalID3(tag([frame(major === 2 ? 'PIC' : 'APIC', picture(major, 'image/jpg'), major)], major));
    assert.equal(result.cover.mime, 'image/jpeg');
    assert.deepEqual(Buffer.from(result.cover.data), jpeg);
  }
});

test('v2.4 extended-header size includes its four-byte size field', async () => {
  const { context } = harness();
  const extension = Buffer.from([0, 0, 0, 6, 1, 0]);
  const result = await context.parseLocalID3(tag([frame('TIT2', text('After extension'), 4)], 4, 64, extension));
  assert.equal(result.title, 'After extension');
});

test('v2.3 extended-header size excludes its four-byte size field', async () => {
  const { context } = harness();
  const extension = Buffer.from([0, 0, 0, 6, 0, 0, 0, 0, 0, 0]);
  const result = await context.parseLocalID3(tag([frame('TIT2', text('After extension'))], 3, 64, extension));
  assert.equal(result.title, 'After extension');
});

test('truncated tags fall back to server confirmation without inventing metadata', async () => {
  const { context } = harness();
  const complete = tag([frame('TIT2', text('Keep me')), frame('TPE1', text('Incomplete artist'))]);
  const truncated = complete.subarray(0, complete.length - 7);
  assert.equal(await context.parseLocalID3(truncated), null);
  assert.equal(await context.parseLocalID3(Buffer.from('ID3')), null);
  assert.equal(await context.parseLocalID3(Buffer.alloc(10)), null);
});

test('unknown ID3 frames do not prevent following metadata', async () => {
  const { context } = harness();
  const result = await context.parseLocalID3(tag([frame('PRIV', Buffer.from('private')), frame('TIT2', text('Visible'))]));
  assert.equal(result.title, 'Visible');
});

test('adding MP3 files previews embedded metadata and assets without network requests', async () => {
  const { context, state, fetchCount, urls } = harness();
  context.addFiles([file('song.mp3', tag([
    frame('TIT2', text('Local song')),
    frame('TPE1', text('Local artist')),
    frame('USLT', described('[00:00.00]Local lyrics')),
    frame('APIC', picture()),
  ]))]);
  const upload = state.uploads[0];
  await upload.localPromise;
  assert.equal(upload.title, 'Local song');
  assert.equal(upload.artist, 'Local artist');
  assert.equal(upload.localPreview.track.embedded_lyrics, true);
  assert.equal(upload.localPreview.track.cover_source, 'embedded');
  assert.equal(urls.get(upload.localPreview.coverURL).type, 'image/jpeg');
  assert.equal(upload.stageId, null);
  assert.equal(fetchCount(), 0);
});

test('non-MP3 files remain local and visibly await server parsing', async () => {
  const { context, state, fetchCount } = harness();
  context.addFiles([file('song.flac', Buffer.from('fLaC'))]);
  const upload = state.uploads[0];
  await upload.localPromise;
  assert.ok(upload.localPreview.note, 'unsupported local formats must carry a server-confirmation note');
  assert.equal(upload.localPreview.track.embedded_lyrics, false);
  assert.equal(upload.stageId, null);
  assert.equal(fetchCount(), 0);
});

test('oversized ID3 tags are deferred before reading their payload', async () => {
  const { context } = harness();
  const payloadSize = 33 * 1024 * 1024;
  const header = Buffer.concat([Buffer.from('ID3'), Buffer.from([3, 0, 0]), syncsafe(payloadSize)]);
  const reads = [];
  const largeFile = {
    name: 'large.mp3', size: payloadSize + 10,
    slice(start, end) {
      reads.push([start, end]);
      assert.equal(end, 10, 'oversized tag payload must not be loaded into memory');
      return new Blob([header]);
    },
  };
  const result = await context.parseLocalAudio(largeFile);
  assert.equal(result.track.tags_confirmed, false);
  assert.ok(result.note);
  assert.deepEqual(reads, [[0, 10]]);
});

test('embedded cover takes priority over automatic external cover', async () => {
  const { context, state, urls } = harness();
  const upload = uploadFixture(context);
  upload.coverFile = context.describeFile(file('cover.jpg', Buffer.from('external')), 1);
  state.uploads.push(upload);
  context.parseLocalAudio = async () => audioResult({ local_cover: jpeg, cover_id: 'embedded', cover_source: 'embedded', cover_mime: 'image/jpeg' });
  await context.parseLocalUpload(upload);
  assert.equal(upload.localPreview.track.cover_source, 'embedded');
  assert.deepEqual(Buffer.from(await urls.get(upload.localPreview.coverURL).arrayBuffer()), jpeg);
});

test('a manually selected external cover takes priority over embedded cover', async () => {
  const { context, state, urls } = harness();
  const upload = uploadFixture(context);
  upload.coverFile = { ...context.describeFile(file('manual.jpg', Buffer.from('manual')), 1), manual: true };
  state.uploads.push(upload);
  context.parseLocalAudio = async () => audioResult({ local_cover: jpeg, cover_id: 'embedded', cover_source: 'embedded', cover_mime: 'image/jpeg' });
  await context.parseLocalUpload(upload);
  assert.equal(upload.localPreview.track.cover_source, 'manual');
  assert.equal(Buffer.from(await urls.get(upload.localPreview.coverURL).arrayBuffer()).toString(), 'manual');
});

test('AlbumCover author and album are matched after local tag parsing', async () => {
  const { context, state } = harness();
  context.parseLocalAudio = async () => audioResult();
  context.addFiles([
    file('test.mp3', Buffer.alloc(0), 'music/test.mp3'),
    file('AlbumCover - Tagged artist - Tagged album.jpg', Buffer.from('cover'), 'music/AlbumCover - Tagged artist - Tagged album.jpg'),
  ]);
  const upload = state.uploads[0];
  await upload.localPromise;
  assert.equal(upload.coverFile.file.name, 'AlbumCover - Tagged artist - Tagged album.jpg');
  assert.equal(upload.localPreview.track.cover_source, 'external');
});

test('outside lyrics preview locally while preserving the embedded-lyrics flag', async () => {
  const { context, state, fetchCount } = harness();
  context.parseLocalAudio = async () => audioResult({ lyrics: 'Embedded', embedded_lyrics: true });
  context.addFiles([file('test.mp3'), file('test.lrc', encoded('[00:00.00]\u5916\u7f6e\u6b4c\u8bcd', 1))]);
  const upload = state.uploads[0];
  await upload.localPromise;
  assert.equal(upload.lyrics, '[00:00.00]\u5916\u7f6e\u6b4c\u8bcd');
  assert.equal(upload.localPreview.track.lyrics_source, 'sidecar');
  assert.equal(upload.localPreview.track.embedded_lyrics, true);
  assert.equal(fetchCount(), 0);
});

test('external lyrics decode UTF-8, BOM UTF-16 and GB18030 with normalized line endings', async () => {
  const { context } = harness();
  const value = '[00:00.00]\u4f60\u597d\r\n[00:01.00]Text';
  const gb18030 = Buffer.concat([
    Buffer.from('[00:00.00]'), Buffer.from([0xc4, 0xe3, 0xba, 0xc3]), Buffer.from('\r\n[00:01.00]Text'),
  ]);
  for (const bytes of [
    Buffer.from(value, 'utf8'),
    Buffer.concat([Buffer.from([239, 187, 191]), Buffer.from(value, 'utf8')]),
    encoded(value, 1),
    Buffer.concat([Buffer.from([254, 255]), encoded(value, 2)]),
    gb18030,
  ]) assert.equal(await context.localReadText(file('lyrics.lrc', bytes)), value.replace(/\r\n/g, '\n'));
});

test('external lyrics accept 2 MiB and reject larger files before reading them', async () => {
  const { context } = harness();
  const limit = 2 * 1024 * 1024;
  assert.equal((await context.localReadText(file('limit.lrc', Buffer.alloc(limit, 65)))).length, limit);
  let read = false;
  const oversized = {
    size: limit + 1,
    arrayBuffer() { read = true; return Promise.resolve(new ArrayBuffer(0)); },
  };
  await assert.rejects(context.localReadText(oversized), /2 MiB/);
  assert.equal(read, false);
});

test('adding sidecars later reparses tracks in the same selected folder', async () => {
  const { context, state, fetchCount } = harness();
  context.parseLocalAudio = async () => audioResult();
  context.addFiles([file('test.mp3', Buffer.alloc(0), 'music/test.mp3')]);
  const upload = state.uploads[0];
  await upload.localPromise;
  context.addFiles([
    file('test.lrc', Buffer.from('[00:00.00]Later lyrics'), 'music/test.lrc'),
    file('test.jpg', Buffer.from('later cover'), 'music/test.jpg'),
  ]);
  await upload.localPromise;
  assert.equal(upload.lyrics, '[00:00.00]Later lyrics');
  assert.equal(upload.localPreview.track.cover_source, 'external');
  assert.equal(state.uploads.length, 1);
  assert.equal(fetchCount(), 0);
});

test('an identically named sidecar from another folder is not associated', async () => {
  const { context, state } = harness();
  context.parseLocalAudio = async () => audioResult();
  context.addFiles([
    file('test.mp3', Buffer.alloc(0), 'first/test.mp3'),
    file('test.lrc', Buffer.from('Wrong lyrics'), 'second/test.lrc'),
  ]);
  const upload = state.uploads[0];
  await upload.localPromise;
  assert.equal(upload.lyricsFile, null);
  assert.equal(upload.lyrics, '');
});

test('reparsing an embedded cover releases its previous blob URL', async () => {
  const { context, state, urls, revoked } = harness();
  const upload = uploadFixture(context);
  state.uploads.push(upload);
  context.parseLocalAudio = async () => audioResult({ local_cover: jpeg, cover_id: 'embedded', cover_source: 'embedded' });
  await context.parseLocalUpload(upload);
  const originalURL = upload.localPreview.coverURL;
  await context.parseLocalUpload(upload);
  assert.notEqual(upload.localPreview.coverURL, originalURL);
  assert.ok(revoked.includes(originalURL));
  assert.equal(urls.size, 1);
});

test('user metadata edits made while parsing remain authoritative', async () => {
  const { context, state } = harness();
  const pending = deferred();
  context.parseLocalAudio = () => pending.promise;
  const upload = uploadFixture(context);
  state.uploads.push(upload);
  const parsing = context.parseLocalUpload(upload);
  upload.edits.title = upload.title = 'User title';
  upload.edits.artist = upload.artist = '';
  upload.edits.lyrics = upload.lyrics = 'User lyrics';
  pending.resolve(audioResult({ lyrics: 'Tagged lyrics', embedded_lyrics: true }));
  await parsing;
  assert.equal(upload.title, 'User title');
  assert.equal(upload.artist, '');
  assert.equal(upload.lyrics, 'User lyrics');
  assert.equal(upload.album, 'Tagged album');
});

test('a superseded parse cannot overwrite a newer preview', async () => {
  const { context, state } = harness();
  const old = deferred();
  const fresh = deferred();
  let calls = 0;
  context.parseLocalAudio = () => (++calls === 1 ? old.promise : fresh.promise);
  const upload = uploadFixture(context);
  state.uploads.push(upload);
  const original = context.parseLocalUpload(upload);
  context.invalidateLocalParse(upload);
  const newer = context.parseLocalUpload(upload);
  fresh.resolve(audioResult({ title: 'New parse' }));
  await newer;
  old.resolve(audioResult({ title: 'Old parse' }));
  await original;
  assert.equal(upload.title, 'New parse');
  assert.equal(upload.localPreview.track.title, 'New parse');
});

test('removed queue rows cannot be restored by pending local parsing', async () => {
  const { context, state, urls } = harness();
  const pending = deferred();
  context.parseLocalAudio = () => pending.promise;
  const upload = uploadFixture(context);
  state.uploads.push(upload);
  const parsing = context.parseLocalUpload(upload);
  await context.removeUpload(upload);
  pending.resolve(audioResult({ local_cover: jpeg, cover_id: 'embedded', cover_source: 'embedded' }));
  await parsing;
  assert.equal(state.uploads.length, 0);
  assert.equal(upload.localPreview, null);
  assert.equal(urls.size, 0);
});

test('clearing a queue cancels pending parsing and releases local cover URLs', async () => {
  const { context, state, urls } = harness();
  const completed = uploadFixture(context, 'completed.mp3');
  const pendingUpload = uploadFixture(context, 'pending.mp3');
  const pending = deferred();
  context.parseLocalAudio = item => item.name === 'completed.mp3'
    ? Promise.resolve(audioResult({ local_cover: jpeg, cover_id: 'embedded', cover_source: 'embedded' }))
    : pending.promise;
  state.uploads.push(completed, pendingUpload);
  await context.parseLocalUpload(completed);
  const parsing = context.parseLocalUpload(pendingUpload);
  assert.equal(urls.size, 1);
  await context.clearUploads();
  pending.resolve(audioResult({ local_cover: jpeg, cover_id: 'embedded', cover_source: 'embedded' }));
  await parsing;
  assert.equal(state.uploads.length, 0);
  assert.equal(urls.size, 0);
});

test('removing one track preserves an external cover URL used by another track', async () => {
  const { context, state, urls, revoked } = harness();
  context.parseLocalAudio = async () => audioResult();
  const cover = context.describeFile(file('folder.jpg', Buffer.from('shared cover')), 1);
  const first = uploadFixture(context, 'first.mp3');
  const second = uploadFixture(context, 'second.mp3');
  first.coverFile = second.coverFile = cover;
  state.attachments.push(cover);
  state.uploads.push(first, second);
  await context.parseLocalUpload(first);
  await context.parseLocalUpload(second);
  const sharedURL = second.localPreview.coverURL;
  await context.removeUpload(first);
  assert.equal(state.uploads.length, 1);
  assert.equal(urls.size, 1);
  assert.equal(second.localPreview.coverURL, sharedURL);
  assert.ok(!revoked.includes(sharedURL));
  await context.clearUploads();
  assert.equal(urls.size, 0);
  assert.equal(revoked.filter(url => url === sharedURL).length, 1);
});

test('server rematching an automatic cover preserves other tracks sharing the old URL', async () => {
  const { context, state, urls, revoked } = harness();
  context.parseLocalAudio = async () => audioResult({ artist: '', album: '' });
  context.api = async (_path, method) => method === 'DELETE' ? null : {
    id: 'stage-1', track: { title: 'Server title', artist: 'Server artist', album: 'Server album' },
  };
  const cover = context.describeFile(file('folder.jpg', Buffer.from('shared cover')), 1);
  const albumCover = context.describeFile(file('AlbumCover - Server artist - Server album.jpg', Buffer.from('album cover')), 1);
  const first = uploadFixture(context, 'first.mp3');
  const second = uploadFixture(context, 'second.mp3');
  first.coverFile = second.coverFile = cover;
  state.attachments.push(cover, albumCover);
  state.uploads.push(first, second);
  await context.parseLocalUpload(first);
  await context.parseLocalUpload(second);
  const sharedURL = second.localPreview.coverURL;
  await context.stageUpload(first);
  assert.equal(first.coverFile, albumCover);
  await context.removeUpload(first);
  assert.equal(state.uploads.length, 1);
  assert.ok(urls.has(sharedURL), 'server cover rematching must not revoke another preview');
  assert.ok(!revoked.includes(sharedURL));
  await context.clearUploads();
  assert.equal(urls.size, 0);
});

test('manual attachment selection restores matching after clearing it', async () => {
  const { context, state, inputs } = harness();
  context.parseLocalAudio = async () => audioResult();
  context.run = action => action();
  const upload = uploadFixture(context);
  state.uploads.push(upload);
  await context.clearUploadAttachment(upload, 'cover');
  assert.equal(upload.ignoreAutomaticCover, true);
  context.selectUploadAttachment(upload, 'cover');
  inputs.at(-1).files = [file('manual.jpg', Buffer.from('manual'))];
  inputs.at(-1).onchange();
  await settle();
  assert.equal(upload.ignoreAutomaticCover, false);
  assert.equal(upload.localPreview.track.cover_source, 'manual');
});

test('manual lyrics can be selected after clearing their association', async () => {
  const { context, state, inputs } = harness();
  context.parseLocalAudio = async () => audioResult({ lyrics: 'Embedded lyrics', embedded_lyrics: true });
  context.run = action => action();
  const upload = uploadFixture(context);
  state.uploads.push(upload);
  await context.clearUploadAttachment(upload, 'lyrics');
  context.selectUploadAttachment(upload, 'lyrics');
  inputs.at(-1).files = [file('manual.lrc', Buffer.from('Manual lyrics'))];
  inputs.at(-1).onchange();
  await settle();
  assert.equal(upload.ignoreAutomaticLyrics, false);
  assert.equal(upload.lyrics, 'Manual lyrics');
  assert.equal(upload.localPreview.track.embedded_lyrics, true);
});

test('server staging wins when requested during local parsing', async () => {
  const { context, state } = harness();
  const pending = deferred();
  context.parseLocalAudio = () => pending.promise;
  context.api = async () => ({ id: 'stage-1', track: { title: 'Server title', artist: 'Server artist' } });
  const upload = uploadFixture(context);
  state.uploads.push(upload);
  const parsing = context.parseLocalUpload(upload);
  const staging = context.stageUpload(upload);
  await settle();
  pending.resolve(audioResult());
  await Promise.all([parsing, staging]);
  assert.equal(upload.stageId, 'stage-1');
  assert.equal(upload.status, '\u5f85\u786e\u8ba4');
  assert.equal(upload.title, 'Server title');
  assert.equal(upload.artist, 'Server artist');
});

test('local parsing finishing later cannot hide a failed server upload', async () => {
  const { context, state } = harness();
  const pending = deferred();
  context.parseLocalAudio = () => pending.promise;
  context.api = async () => { throw new Error('Server rejected upload'); };
  const upload = uploadFixture(context);
  state.uploads.push(upload);
  const parsing = context.parseLocalUpload(upload);
  const staging = context.stageUpload(upload);
  const rejected = assert.rejects(staging, /Server rejected upload/);
  await settle();
  pending.resolve(audioResult());
  await Promise.all([parsing, rejected]);
  assert.equal(upload.status, '\u5931\u8d25');
  assert.equal(upload.error, 'Server rejected upload');
});
