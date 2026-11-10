#!/usr/bin/env node
// Executable contract for the Pikmelo Server incremental sync protocol.
//
//   node contracts/verify.mjs
//
// 1. validates every schema/example pair,
// 2. starts the dependency-free mock server,
// 3. replays library sync, batch resolution, realtime delta and SSE scenarios,
// 4. validates every live response against the same schemas.

import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { loadSchema, validate } from './lib/validate.mjs';
import { parseSSE } from './lib/sse.mjs';
import { createMockServer } from './mock-server.mjs';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const SCHEMAS = path.join(HERE, 'schemas');
const EXAMPLES = path.join(HERE, 'examples');
const LIBRARY_HEADERS = { Authorization: 'Bearer mock-key' };
const COMMUNITY_HEADERS = { Cookie: 'session=mock-session' };
const CSRF_HEADERS = { ...COMMUNITY_HEADERS, 'X-CSRF-Token': 'mock-csrf' };

let checks = 0;
function ok(label, detail = '') {
  checks += 1;
  console.log(`PASS ${label}${detail ? ` (${detail})` : ''}`);
}

function schemaCheck(label, value, schemaFile) {
  const schemaPath = path.join(SCHEMAS, schemaFile);
  const errors = validate(value, loadSchema(schemaPath), { file: schemaPath });
  assert.equal(errors.length, 0, `${label} violates ${schemaFile}:\n${errors.join('\n')}`);
  ok(label, schemaFile);
}

function exampleSchema(name) {
  if (name.startsWith('sync-tracks-request')) return 'library-sync-tracks-request.schema.json';
  if (name.startsWith('sync-tracks-response')) return 'library-sync-tracks-response.schema.json';
  if (name.startsWith('sync-')) return 'library-sync-response.schema.json';
  if (name.startsWith('realtime-sync-request')) return 'realtime-sync-request.schema.json';
  if (name.startsWith('realtime-reset') || name.startsWith('realtime-delta') || name.startsWith('realtime-noop')) return 'realtime-sync-response.schema.json';
  if (name.startsWith('realtime-view')) return 'realtime-view.schema.json';
  if (name.startsWith('error-')) return 'error.schema.json';
  throw new Error(`no schema mapping for ${name}`);
}

for (const file of fs.readdirSync(EXAMPLES).filter((name) => name.endsWith('.json')).sort()) {
  const value = JSON.parse(fs.readFileSync(path.join(EXAMPLES, file), 'utf8'));
  schemaCheck(`example ${file}`, value, exampleSchema(file));
}

const sseSchema = path.join(SCHEMAS, 'sse-frame.schema.json');
for (const frame of parseSSE(fs.readFileSync(path.join(EXAMPLES, 'sse-session.txt'), 'utf8'))) {
  const errors = validate(frame, loadSchema(sseSchema), { file: sseSchema });
  assert.equal(errors.length, 0, `SSE example frame invalid:\n${errors.join('\n')}`);
}
ok('example sse-session.txt', 'sse-frame.schema.json');

for (const value of [
  { id:'track', title:'Title', version:1, detail_loaded:false, artist:null, album:null, cover_id:null, duration_ms:null },
  { id:'missing', title:'Unavailable', version:1, detail_loaded:false, missing:true, removed:false },
]) {
  const errors = validate(value, { $ref:'common.schema.json#/$defs/browse-track' }, { file:sseSchema });
  assert.deepEqual(errors, [], 'real detail projection must satisfy the schema');
}
ok('real detail projection including unavailable and nullable metadata');

const mock = createMockServer({ heartbeatMs: 60 });
await new Promise((resolve) => mock.server.listen(0, '127.0.0.1', resolve));
const base = `http://127.0.0.1:${mock.server.address().port}`;

async function request(pathname, { method = 'GET', body, headers = {} } = {}) {
  const response = await fetch(base + pathname, {
    method,
    headers: {
      ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
      ...headers,
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await response.text();
  let value;
  try {
    value = text === '' ? null : JSON.parse(text);
  } catch {
    value = null;
  }
  return { status: response.status, headers: response.headers, text, value };
}

function sortIds(values) {
  return [...values].sort();
}

// ---------------------------------------------------------------------------
// Library sync: paginated snapshot, then a delta after a commit.
// ---------------------------------------------------------------------------
const library = { tracks: [], playlists: [], cursor: '' };
let page = await request('/api/v2/sync?limit=2', { headers: LIBRARY_HEADERS });
assert.equal(page.status, 200);
schemaCheck('library snapshot page 1', page.value, 'library-sync-response.schema.json');
assert.equal(page.value.full_snapshot, true);
assert.equal(page.value.has_more, true);
assert.equal(page.value.reset, true);
for (let guard = 0; guard < 10; guard += 1) {
  library.tracks.push(...page.value.changed_tracks.map((track) => track.id));
  library.playlists.push(...page.value.playlists.map((playlist) => playlist.id));
  library.cursor = page.value.cursor;
  if (!page.value.has_more) break;
  page = await request(`/api/v2/sync?limit=2&cursor=${encodeURIComponent(page.value.cursor)}`, { headers: LIBRARY_HEADERS });
  assert.equal(page.status, 200);
  schemaCheck('library snapshot page', page.value, 'library-sync-response.schema.json');
}
assert.deepEqual(sortIds(library.tracks), ['t-alpha', 't-beta', 't-missing']);
assert.deepEqual(sortIds(library.playlists), ['p-focus', 'p-radio']);
ok('library snapshot pagination', `${library.tracks.length} tracks, ${library.playlists.length} playlists`);

const firstCommit = await request('/__mock/commit', { method: 'POST' });
assert.equal(firstCommit.status, 200);
const delta = await request(`/api/v2/sync?cursor=${encodeURIComponent(library.cursor)}`, { headers: LIBRARY_HEADERS });
assert.equal(delta.status, 200);
schemaCheck('library delta', delta.value, 'library-sync-response.schema.json');
assert.equal(delta.value.reset, false);
assert.equal(delta.value.full_snapshot, false);
assert.deepEqual(sortIds(delta.value.changed_tracks.map((track) => track.id)), ['t-beta']);
assert.deepEqual(sortIds(delta.value.playlists.map((playlist) => playlist.id)), ['p-focus']);
ok('library delta after commit');

// ---------------------------------------------------------------------------
// Object sync protocol 2 and batch resolution.
// ---------------------------------------------------------------------------
const object = await request('/api/v2/sync?protocol=2&limit=100', { headers: LIBRARY_HEADERS });
assert.equal(object.status, 200);
schemaCheck('object snapshot', object.value, 'library-sync-response.schema.json');
assert.equal(object.value.object_delta, true);
assert.deepEqual(sortIds(object.value.changed_track_ids), ['t-alpha', 't-beta', 't-missing']);
assert.deepEqual(object.value.changed_tracks, []);
ok('object snapshot (protocol=2)');

const secondCommit = await request('/__mock/commit', { method: 'POST' });
assert.equal(secondCommit.status, 200);
const objectDelta = await request(`/api/v2/sync?protocol=2&cursor=${encodeURIComponent(object.value.cursor)}`, { headers: LIBRARY_HEADERS });
assert.equal(objectDelta.status, 200);
schemaCheck('object delta', objectDelta.value, 'library-sync-response.schema.json');
assert.deepEqual(sortIds(objectDelta.value.changed_track_ids), ['t-beta']);
assert.equal(objectDelta.value.changed_tracks.length, 0);
ok('object delta after commit');

const resolved = await request('/api/v2/sync/tracks', {
  method: 'POST',
  headers: LIBRARY_HEADERS,
  body: { cursor: objectDelta.value.cursor, ids: ['t-alpha', 't-beta'] },
});
assert.equal(resolved.status, 200);
schemaCheck('batch track resolution', resolved.value, 'library-sync-tracks-response.schema.json');
assert.deepEqual(sortIds(resolved.value.tracks.map((track) => track.id)), ['t-alpha', 't-beta']);

const duplicate = await request('/api/v2/sync/tracks', {
  method: 'POST',
  headers: LIBRARY_HEADERS,
  body: { cursor: objectDelta.value.cursor, ids: ['t-alpha', 't-alpha'] },
});
assert.equal(duplicate.status, 400);
assert.equal(duplicate.headers.get('x-error-code'), 'INVALID_SYNC_BATCH');
schemaCheck('batch error envelope', duplicate.value, 'error.schema.json');

const hidden = await request('/api/v2/sync/tracks', {
  method: 'POST',
  headers: LIBRARY_HEADERS,
  body: { cursor: objectDelta.value.cursor, ids: ['t-unknown'] },
});
assert.equal(hidden.status, 404);
assert.equal(hidden.headers.get('x-error-code'), 'TRACK_NOT_FOUND');
schemaCheck('batch hidden track error', hidden.value, 'error.schema.json');
ok('batch resolution validation');

// ---------------------------------------------------------------------------
// Realtime delta sync: reset, no-op, delta, reset-on-expiry, errors.
// ---------------------------------------------------------------------------
const view = { catalog: true, subscriptions: true };
let realtime = await request('/api/v2/community/sync', {
  method: 'POST',
  headers: CSRF_HEADERS,
  body: { cursor: '', view },
});
assert.equal(realtime.status, 200);
schemaCheck('realtime reset', realtime.value, 'realtime-sync-response.schema.json');
assert.equal(realtime.value.reset, true);
assert.equal(realtime.value.sections.badge.meta.pending, 2 + 2);
assert.equal(realtime.value.sections.catalog.changed[0].track_count, 12 + 2);
assert.equal(realtime.value.sections.catalog.reset, true);
const realtimeCursor = realtime.value.cursor;

const normalized = await request('/api/v2/community/sync', {
  method: 'POST', headers: CSRF_HEADERS,
  body: { cursor: realtimeCursor, view: { subscriptions: true, offset: 0, catalog: true } },
});
assert.equal(normalized.value.reset, false);
assert.equal(normalized.value.cursor, realtimeCursor);
for (const key of ['query', 'subscription_query', 'public_query']) {
  const oversized = await request('/api/v2/community/sync', {
    method: 'POST', headers: CSRF_HEADERS, body: { view: { [key]: '日'.repeat(400) } },
  });
  assert.equal(oversized.status, 400, `${key} must enforce UTF-8 bytes`);
}
const search = await request('/api/v2/community/sync', {
  method: 'POST', headers: CSRF_HEADERS,
  body: { view: { catalog: true, query: 'no-such-playlist', public_detail: 'p-radio', public_query: 'no-such-track', subscription_detail: 'p-radio', subscription_query: 'no-such-track' } },
});
for (const name of ['catalog', 'detail_tracks', 'subscription_tracks']) {
  assert.equal(search.value.sections[name].meta.total, 0);
  assert.deepEqual(search.value.sections[name].changed, []);
}
ok('realtime canonical view, UTF-8 limits and search filtering');

const noop = await request('/api/v2/community/sync', {
  method: 'POST',
  headers: CSRF_HEADERS,
  body: { cursor: realtimeCursor, view },
});
assert.equal(noop.status, 200);
schemaCheck('realtime no-op', noop.value, 'realtime-sync-response.schema.json');
assert.equal(noop.value.reset, false);
assert.deepEqual(noop.value.sections, {});
assert.equal(noop.value.cursor, realtimeCursor);
ok('realtime no-op keeps cursor');

await request('/__mock/commit', { method: 'POST' });
const realtimeDelta = await request('/api/v2/community/sync', {
  method: 'POST',
  headers: CSRF_HEADERS,
  body: { cursor: realtimeCursor, view },
});
assert.equal(realtimeDelta.status, 200);
schemaCheck('realtime delta', realtimeDelta.value, 'realtime-sync-response.schema.json');
assert.equal(realtimeDelta.value.reset, false);
assert.equal(realtimeDelta.value.sections.badge.meta.pending, 2 + 3);
assert.equal(realtimeDelta.value.sections.catalog.changed[0].track_count, 12 + 3);
assert.equal(realtimeDelta.value.sections.subscriptions, undefined);
ok('realtime delta after commit');

await request('/__mock/expire-cursors', { method: 'POST' });
const expired = await request('/api/v2/community/sync', {
  method: 'POST',
  headers: CSRF_HEADERS,
  body: { cursor: realtimeDelta.value.cursor, view },
});
assert.equal(expired.status, 200);
schemaCheck('realtime expired cursor reset', expired.value, 'realtime-sync-response.schema.json');
assert.equal(expired.value.reset, true);

const hiddenDetail = await request('/api/v2/community/sync', {
  method: 'POST',
  headers: CSRF_HEADERS,
  body: { view: { subscription_detail: 'p-missing' } },
});
assert.equal(hiddenDetail.status, 200);
schemaCheck('realtime unavailable detail', hiddenDetail.value, 'realtime-sync-response.schema.json');
assert.deepEqual(hiddenDetail.value.sections.subscription_tracks.meta, {});
assert.deepEqual(hiddenDetail.value.sections.subscription_tracks.changed, []);

const invalidView = await request('/api/v2/community/sync', {
  method: 'POST',
  headers: CSRF_HEADERS,
  body: { view: { catalog: true, bogus: true } },
});
assert.equal(invalidView.status, 400);
assert.equal(invalidView.headers.get('x-error-code'), 'INVALID_REALTIME_VIEW');
schemaCheck('invalid realtime view error', invalidView.value, 'error.schema.json');

const noCsrf = await request('/api/v2/community/sync', {
  method: 'POST',
  headers: COMMUNITY_HEADERS,
  body: { view: {} },
});
assert.equal(noCsrf.status, 403);
assert.equal(noCsrf.headers.get('x-error-code'), 'CSRF_DENIED');
ok('realtime errors');

// ---------------------------------------------------------------------------
// SSE: ready, change after commit, heartbeat, reconnect, expired, auth/limits.
// ---------------------------------------------------------------------------
async function openStream(query) {
  const response = await fetch(`${base}/api/v2/community/events?view=${encodeURIComponent(JSON.stringify(query))}`, {
    headers: COMMUNITY_HEADERS,
  });
  if (response.status !== 200) return { response };
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  return {
    response,
    reader,
    async nextFrame(timeoutMs = 3000) {
      const deadline = Date.now() + timeoutMs;
      while (Date.now() < deadline) {
        const index = buffer.indexOf('\n\n');
        if (index !== -1) {
          const block = buffer.slice(0, index + 2);
          buffer = buffer.slice(index + 2);
          const frames = parseSSE(block);
          for (const parsed of frames) {
            const errors = validate(parsed, loadSchema(sseSchema), { file: sseSchema });
            assert.equal(errors.length, 0, `live SSE frame invalid:\n${errors.join('\n')}`);
          }
          if (frames.length > 0) return frames[0];
          continue;
        }
        const chunk = await Promise.race([
          reader.read(),
          new Promise((_, reject) => setTimeout(() => reject(new Error('SSE read timeout')), deadline - Date.now())),
        ]);
        if (chunk.done) throw new Error('SSE stream closed');
        buffer += decoder.decode(chunk.value, { stream: true });
      }
      throw new Error('SSE frame timeout');
    },
    async nextEvent(name) {
      for (;;) {
        const frame = await this.nextFrame();
        if (frame.type === 'heartbeat') continue;
        assert.equal(frame.type, 'event');
        assert.equal(frame.event, name, `expected ${name}, got ${frame.event}`);
        return frame;
      }
    },
    close() {
      reader.cancel().catch(() => {});
    },
  };
}

const stream = await openStream({ catalog: true });
assert.equal(stream.response.status, 200);
assert.match(stream.response.headers.get('content-type'), /^text\/event-stream/);
let frame = await stream.nextFrame();
assert.equal(frame.type, 'event');
assert.equal(frame.event, 'ready');
ok('SSE ready frame');

await request('/__mock/commit', { method: 'POST' });
frame = await stream.nextEvent('change');
assert.deepEqual(frame.data, {});

frame = await stream.nextFrame();
assert.equal(frame.type, 'heartbeat');
ok('SSE change + heartbeat frames');

await request('/__mock/reconnect', { method: 'POST' });
frame = await stream.nextEvent('reconnect');
assert.deepEqual(frame.data, {});
stream.close();
ok('SSE reconnect frame');

const expiredStream = await openStream({ subscriptions: true });
frame = await expiredStream.nextFrame();
assert.equal(frame.event, 'ready');
await request('/__mock/expire-auth', { method: 'POST' });
frame = await expiredStream.nextEvent('expired');
assert.deepEqual(frame.data, {});
expiredStream.close();
ok('SSE expired frame');


const sseNoAuth = await request('/api/v2/community/events?view=%7B%7D');
assert.equal(sseNoAuth.status, 403);
assert.equal(sseNoAuth.headers.get('x-error-code'), 'SESSION_REQUIRED');

const sseBadView = await request('/api/v2/community/events?view=%7B%22bogus%22%3Atrue%7D', { headers: COMMUNITY_HEADERS });
assert.equal(sseBadView.status, 400);
assert.equal(sseBadView.headers.get('x-error-code'), 'INVALID_REALTIME_VIEW');
ok('SSE pre-stream errors');

mock.server.close();
console.log(`\n${checks} contract checks passed`);
