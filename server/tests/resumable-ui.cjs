'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { webcrypto, createHash } = require('node:crypto');

function harness() {
  const tasks = new Map(), drafts = new Map(), sent = [], reads = [], finishBodies = [];
  const owner = { id: 'owner-a' };
  const state = { user: owner, csrf: 'csrf-a', uploads: [], attachments: [], batch: 0 };
  let nextID = 1, loseAck = false, loseFinishAck = false, hold = false, held = null;
  const clone = value => JSON.parse(JSON.stringify(value));
  async function request(url, options = {}) {
    const method = options.method || 'GET', parts = url.split('/resumable')[1].split('/').filter(Boolean);
    const body = options.body ? JSON.parse(options.body) : {};
    if (!parts.length && method === 'POST') {
      const task = { id: (nextID++).toString(16).padStart(32, '0'), name: body.name, size: body.size, offset: 0, chunk_size: 4194304, chunks: [], state: 'uploading', fields: body.fields, attachments: body.attachments || {}, expires_at: new Date(Date.now() + 86400000).toISOString() };
      tasks.set(task.id, task); return response(201, task);
    }
    if (!parts.length) return response(200, { items: [...tasks.values()].filter(t => t.state !== 'canceled') });
    const task = tasks.get(parts[0]);
    if (!task) return response(404, { error: { code: 'NOT_FOUND', message: 'not found' } });
    if (method === 'DELETE') { task.state = 'canceled'; return response(204, null); }
    if (parts[1] === 'finish') {
      finishBodies.push(body);
      task.state = 'ready'; task.stage_id = task.id;
      if (loseFinishAck) { loseFinishAck = false; throw new Error('lost finish acknowledgement'); }
      return response(200, { id: task.id, track: { title: 'Verified audio', lyrics: body.fields?.lyrics || '' }, validation: 'audio_stream_verified', expires_at: new Date(Date.now() + 1800000).toISOString() });
    }
    return response(200, task);
  }
  function response(status, value) { return { ok: status < 300, status, text: async () => value == null ? '' : JSON.stringify(value) }; }
  class XHR {
    constructor() { this.upload = {}; this.headers = {}; }
    open(method, url) { this.url = url; this.method = method; }
    setRequestHeader(name, value) { this.headers[name] = value; }
    abort() { this.aborted = true; this.onabort?.(); this.onloadend?.(); }
    send(body) {
      const send = () => {
        if (this.aborted) return;
        const bytes = Buffer.from(body), task = tasks.get(this.url.split('/').pop()), offset = Number(this.headers['Upload-Offset']);
        sent.push({ offset, size: bytes.length, task: task.id });
        const digest = createHash('sha256').update(bytes).digest('hex');
        assert.equal(this.headers['Upload-Checksum'], 'sha256 ' + digest);
        assert.equal(offset, task.offset);
        task.chunks.push({ offset, size: bytes.length, sha256: digest }); task.offset += bytes.length;
        this.upload.onprogress?.({ loaded: bytes.length, total: bytes.length, lengthComputable: true });
        this.upload.onload?.();
        if (loseAck) { loseAck = false; this.onerror?.(); } else { this.status = 200; this.responseText = JSON.stringify(task); this.onload?.(); }
        this.onloadend?.();
      };
      if (hold) { held = send; } else queueMicrotask(send);
    }
  }
  const context = vm.createContext({
    state, console, crypto: webcrypto, Uint8Array, ArrayBuffer, Blob, TextEncoder, TextDecoder,
    setTimeout, clearTimeout, setInterval, clearInterval, performance, XMLHttpRequest: XHR,
    fetch: request, btoa: text => Buffer.from(text, 'binary').toString('base64'),
    document: { querySelector: () => null, getElementById: () => null },
    window: { addEventListener() {} }, navigator: { onLine: true },
    renderUploads() {}, updateUploadProgress() {}, setStatus() {},
    browserUUID: () => 'queue-' + (nextID++), normalizedFileText: value => value.toLowerCase(),
    describeFile(file, batch) { return { id: 'queue-' + (nextID++), file, directory: 'batch:' + batch, base: file.name, extension: 'wav' }; },
    matchingAttachment: () => null, invalidateLocalParse() {}, releaseLocalPreviewURL() {}, releaseAttachmentURL() {},
    setLoggedOut() { state.user = null; }, formatBytes: value => value + ' B', txt: value => String(value),
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname, '../internal/music/upload-resume.js'), 'utf8'), context);
  context.mockDrafts = drafts;
  vm.runInContext("resumeQueueStorage.read = async owner => mockDrafts.get(owner) || []; resumeQueueStorage.write = async (owner, rows) => mockDrafts.set(owner, JSON.parse(JSON.stringify(rows))); resumeWait = async () => {};", context);
  function file(bytes, name = 'audio.wav') {
    return { name, size: bytes.length, lastModified: 12, slice(start, end) { reads.push({ start, end }); const copy = bytes.subarray(start, end); return { arrayBuffer: async () => Uint8Array.from(copy).buffer }; } };
  }
  function upload(bytes) {
    const source = file(bytes), upload = { id: 'queue-' + (nextID++), file: source, edits: {}, status: '待暂存', lyricsFile: null, coverFile: null };
    state.uploads.push(upload); return upload;
  }
  return { context, state, tasks, drafts, sent, reads, finishBodies, file, upload, clone, loseAck: () => { loseAck = true; }, loseFinishAck: () => { loseFinishAck = true; }, hold: () => { hold = true; }, release: () => { hold = false; held?.(); }, waiting: () => Boolean(held) };
}

if (require.main === module) {
  const test = require('node:test');
  test('resumable upload reads bounded chunks and reports cumulative confirmed progress', async () => {
    const h = harness(), upload = h.upload(Buffer.alloc(9 * 1048576, 5));
    const preview = await h.context.resumeStageUpload(upload);
    assert.equal(preview.validation, 'audio_stream_verified'); assert.equal(upload.status, '待确认');
    assert.deepEqual(h.sent.map(x => x.size), [4194304, 4194304, 1048576]);
    assert.equal(upload.progress.loaded, upload.file.size); assert.equal(upload.resumeTask.offset, upload.file.size);
    assert.ok(h.reads.every(x => x.end - x.start <= 4194304));
  });
  test('LAN HTTP fallback SHA-256 matches native digest across padding and chunk boundaries', async () => {
    const h = harness();
    for (const length of [0, 1, 3, 55, 56, 63, 64, 65, 127, 128, 129, 4194304]) {
      const bytes = Buffer.alloc(length, 19), buffer = Uint8Array.from(bytes).buffer;
      assert.equal(h.context.resumeSHA256Fallback(buffer), createHash('sha256').update(bytes).digest('hex'), 'digest length ' + length);
    }
  });
  test('lost acknowledgement queries the checkpoint and never appends a duplicate block', async () => {
    const h = harness(), upload = h.upload(Buffer.alloc(5 * 1048576, 6)); h.loseAck();
    await h.context.resumeStageUpload(upload);
    assert.deepEqual(h.sent.map(x => x.offset), [0, 4194304]); assert.equal(upload.status, '待确认');
  });
  test('pause preserves durable task; continue reuses the same task', async () => {
    const h = harness(), upload = h.upload(Buffer.alloc(1024, 7)); h.hold();
    const running = h.context.resumeStageUpload(upload);
    for (let i = 0; i < 30 && !h.waiting(); i++) await new Promise(resolve => setTimeout(resolve, 5));
    assert.ok(h.waiting()); const id = upload.resumeId;
    h.context.pauseResumeUpload(upload); await running; assert.equal(upload.status, '已暂停');
    h.release(); await h.context.resumeStageUpload(upload); assert.equal(upload.resumeId, id); assert.equal(h.tasks.size, 1);
  });
  test('reload restores metadata only; wrong source prefix is rejected before transmission', async () => {
    const h = harness(), bytes = Buffer.alloc(5 * 1048576, 8), upload = h.upload(bytes); h.loseAck();
    await h.context.resumeStageUpload(upload);
    const task = h.tasks.get(upload.resumeId); task.state = 'uploading'; delete task.stage_id; task.offset = 4194304; task.chunks.length = 1;
    upload.stageId = null; upload.resumeTask = h.clone(task); upload.edits = { title: 'Durable edited title' };
    upload.tagDraft = { personal: { add_ids: ['personal-tag'], remove_ids: [] } };
    await h.context.persistResumeQueue(); h.context.clearResumeWorkspace(); h.state.uploads = [];
    await h.context.restoreResumeQueue(); const restored = h.state.uploads[0];
    assert.equal(restored.needsSourceFile, true); assert.equal(restored.edits.title, 'Durable edited title'); assert.equal(typeof restored.file.slice, 'undefined');
    assert.deepEqual(h.clone(restored.tagDraft), upload.tagDraft);
    const before = h.sent.length;
    await assert.rejects(h.context.reselectResumeFile(restored, h.file(Buffer.alloc(bytes.length, 9))), /不匹配|原文件/);
    assert.equal(h.sent.length, before); assert.equal(restored.needsSourceFile, true);
    await h.context.reselectResumeFile(restored, h.file(bytes)); await h.context.resumeStageUpload(restored);
    assert.equal(restored.status, '待确认'); assert.equal(h.sent.at(-1).offset, 4194304);
  });
  test('logout discards stale responses and does not delete durable uploads', async () => {
    const h = harness(), upload = h.upload(Buffer.alloc(1024, 10)); h.hold();
    const running = h.context.resumeStageUpload(upload);
    for (let i = 0; i < 30 && !h.waiting(); i++) await new Promise(resolve => setTimeout(resolve, 5));
    const id = upload.resumeId; h.context.clearResumeWorkspace(); h.state.user = { id: 'owner-b' }; h.state.uploads = [];
    h.release(); await running; assert.equal(h.tasks.get(id).state, 'uploading'); assert.equal(h.state.uploads.length, 0);
  });
  test('finish acknowledgement loss reuses the same durable preview', async () => {
    const h = harness(), upload = h.upload(Buffer.alloc(1024, 11)); h.loseFinishAck();
    await h.context.resumeStageUpload(upload);
    assert.equal(upload.stageId, upload.resumeId); assert.equal(upload.status, '待确认');
    assert.equal(h.tasks.size, 1); assert.equal(h.sent.length, 1);
  });
  test('metadata storage excludes blobs and requires missing attachments to be selected again', async () => {
    const h = harness(), bytes = Buffer.alloc(1024, 12), upload = h.upload(bytes);
    const lyrics = Object.assign(new Blob(['[00:01]Durable lyric']), { name: 'song.lrc' });
    upload.lyricsFile = { file: lyrics, manual: true }; upload.edits = { title: 'Keep this title' }; h.hold();
    const running = h.context.resumeStageUpload(upload);
    for (let i = 0; i < 30 && !h.waiting(); i++) await new Promise(resolve => setTimeout(resolve, 5));
    h.context.pauseResumeUpload(upload); await running; await h.context.persistResumeQueue();
    const stored = h.drafts.get('owner-a')[0]; assert.deepEqual(h.clone(stored.attachments), { lyrics: 'song.lrc' });
    assert.equal('file' in stored, false); assert.equal('sourceFile' in stored, false); assert.ok(!JSON.stringify(stored).includes('csrf-a'));
    h.context.clearResumeWorkspace(); h.state.uploads = []; h.release(); await h.context.restoreResumeQueue();
    const restored = h.state.uploads[0]; await h.context.reselectResumeFile(restored, h.file(bytes));
    await assert.rejects(h.context.resumeStageUpload(restored), /附件需要重新选择/);
    assert.equal(restored.resumeTask.offset, bytes.length); assert.equal(h.finishBodies.length, 0);
    restored.lyricsFile = { file: lyrics, manual: true }; await h.context.resumeStageUpload(restored);
    assert.equal(h.sent.length, 1, 'reselecting the missing attachment preserves acknowledged audio');
    assert.equal(Buffer.from(h.finishBodies[0].lyrics, 'base64').toString(), '[00:01]Durable lyric');
  });
  test('a slow failed cancellation preserves persisted edits and attachment names', async () => {
    const h = harness(), upload = h.upload(Buffer.alloc(1024, 13));
    await h.context.resumeStageUpload(upload);
    upload.edits.title = 'Latest unsaved-to-server title';
    upload.lyricsFile = { file: Object.assign(new Blob(['[00:01]Keep lyric']), { name: 'keep.lrc' }), manual: true };
    await h.context.persistResumeQueue();
    let release, announce;
    const gate = new Promise(resolve => { release = resolve; }), started = new Promise(resolve => { announce = resolve; });
    h.context.fetch = async () => { announce(); await gate; return { ok: false, status: 503, text: async () => JSON.stringify({ error: { message: 'temporary delete failure' } }) }; };
    const cancellation = h.context.cancelResumeUpload(upload); await started;
    await new Promise(resolve => setTimeout(resolve, 220));
    assert.equal(h.drafts.get('owner-a')[0]?.edits.title, 'Latest unsaved-to-server title');
    assert.equal(h.drafts.get('owner-a')[0]?.attachments.lyrics, 'keep.lrc');
    release(); await assert.rejects(cancellation, /503/);
    assert.equal(upload.resumeCanceled, false);
    assert.equal(h.drafts.get('owner-a')[0]?.edits.title, 'Latest unsaved-to-server title');
    assert.equal(h.tasks.get(upload.resumeId).state, 'ready');
  });
  test('a delayed cancellation failure cannot change a replacement account queue', async () => {
    const h = harness(), upload = h.upload(Buffer.alloc(1024, 16));
    await h.context.resumeStageUpload(upload);
    let release, announce;
    const gate = new Promise(resolve => { release = resolve; }), started = new Promise(resolve => { announce = resolve; });
    h.context.fetch = async () => { announce(); await gate; return { ok: false, status: 503, text: async () => '{}' }; };
    const cancellation = h.context.cancelResumeUpload(upload); await started;
    h.context.clearResumeWorkspace(); h.state.user = { id: 'owner-b' }; h.state.uploads = [];
    const newer = h.upload(Buffer.alloc(32, 17)); newer.edits.title = 'Account B draft'; await h.context.persistResumeQueue();
    release(); await assert.rejects(cancellation);
    assert.equal(upload.resumeCanceled, true, 'old cancellation catch must not mutate an abandoned workspace');
    assert.equal(h.state.uploads[0], newer); assert.equal(h.drafts.get('owner-b')[0].edits.title, 'Account B draft');
  });
  for (const action of ['pause', 'cancel', 'switch-account']) test('delayed creation acknowledgement respects ' + action, async () => {
    const h = harness(), upload = h.upload(Buffer.alloc(1024, 14)), originalFetch = h.context.fetch;
    let release, announce;
    const gate = new Promise(resolve => { release = resolve; }), started = new Promise(resolve => { announce = resolve; });
    h.context.fetch = async (url, options) => {
      const response = await originalFetch(url, options);
      if (options?.method === 'POST' && url.endsWith('/resumable')) { announce(); await gate; }
      return response;
    };
    const running = h.context.resumeStageUpload(upload); await started;
    let cancellation, newer;
    if (action === 'pause') h.context.pauseResumeUpload(upload);
    else if (action === 'cancel') cancellation = h.context.cancelResumeUpload(upload);
    else { h.context.clearResumeWorkspace(); h.state.user = { id: 'owner-b' }; h.state.uploads = []; newer = h.upload(Buffer.alloc(32, 15)); }
    release(); await running; if (cancellation) await cancellation;
    const task = [...h.tasks.values()][0];
    if (action === 'pause') {
      assert.equal(upload.resumeId, task.id); assert.equal(upload.status, '已暂停'); assert.equal(h.sent.length, 0);
      assert.equal(h.drafts.get('owner-a')[0].resumeId, task.id);
      await h.context.resumeStageUpload(upload); assert.equal(h.tasks.size, 1); assert.equal(upload.status, '待确认');
    } else if (action === 'cancel') { assert.equal(task.state, 'canceled'); assert.equal(h.sent.length, 0); }
    else { assert.equal(h.state.uploads.length, 1); assert.equal(h.state.uploads[0], newer); assert.equal(newer.resumeId, undefined); assert.equal(task.state, 'uploading'); }
  });
}

module.exports = async function resumableBrowserChecks({ page, directory }) {
  await page.goto(page.url().split('#')[0] + '#/uploads');
  await page.check('#uploadReview');
  const bytes = Buffer.alloc(5 * 1048576, 0);
  bytes.write('RIFF'); bytes.writeUInt32LE(bytes.length - 8, 4); bytes.write('WAVEfmt ', 8); bytes.writeUInt32LE(16, 16); bytes.writeUInt16LE(1, 20); bytes.writeUInt16LE(1, 22); bytes.writeUInt32LE(8000, 24); bytes.writeUInt32LE(16000, 28); bytes.writeUInt16LE(2, 32); bytes.writeUInt16LE(16, 34); bytes.write('data', 36); bytes.writeUInt32LE(bytes.length - 44, 40);
  await page.locator('#uploadInput').setInputFiles({ name: 'resumable-browser.wav', mimeType: 'audio/wav', buffer: bytes });
  await page.waitForFunction(() => state.uploads.some(x => x.file.name === 'resumable-browser.wav'));
  // Delay the second block, so pause and reload exercise a real persisted prefix.
  let delayed;
  await page.route('**/api/v2/uploads/resumable/*', async route => {
    if (route.request().method() === 'PATCH' && route.request().headers()['upload-offset'] === '4194304') { delayed = route; return; }
    await route.continue();
  });
  await page.locator('[data-stage-upload]').last().click();
  await page.waitForFunction(() => state.uploads.find(x => x.file.name === 'resumable-browser.wav')?.resumeTask?.offset === 4194304);
  await page.locator('[data-resume-pause]').last().click(); await delayed?.abort();
  await page.waitForFunction(() => state.uploads.find(x => x.file.name === 'resumable-browser.wav')?.status === '已暂停');
  await page.evaluate(() => persistResumeQueue()); await page.reload();
  await page.waitForFunction(() => state.uploads.some(x => x.file.name === 'resumable-browser.wav' && x.needsSourceFile));
  const row = page.locator('.upload-item').filter({ hasText: 'resumable-browser.wav' });
  assert.ok((await row.textContent()).includes('选择原文件'));
  await page.unroute('**/api/v2/uploads/resumable/*');
  const wrong = Buffer.from(bytes); wrong[100] ^= 1;
  await row.locator('[data-resume-file]').setInputFiles({ name: 'resumable-browser.wav', mimeType: 'audio/wav', buffer: wrong });
  await page.waitForFunction(() => state.uploads.find(x => x.file.name === 'resumable-browser.wav')?.error.includes('不匹配'));
  await row.locator('[data-resume-file]').setInputFiles({ name: 'resumable-browser.wav', mimeType: 'audio/wav', buffer: bytes });
  await page.waitForFunction(() => !state.uploads.find(x => x.file.name === 'resumable-browser.wav')?.needsSourceFile);
  await row.locator('[data-resume-continue]').click();
  await page.waitForFunction(() => state.uploads.find(x => x.file.name === 'resumable-browser.wav')?.status === '待确认');
  assert.ok((await row.textContent()).includes('服务端已校验'));
  await page.screenshot({ path: path.join(directory, 'resumable-upload.png'), fullPage: true });
  await row.locator('[data-remove-upload]').click();
  console.log('Resumable browser checks passed: confirmed prefix, pause, metadata reload, reselect, finish preview and cancellation.');
};
