#!/usr/bin/env node
// Dependency-free mock of the MusicServer incremental sync protocol. It is a
// reference implementation for clients and the counterpart of verify.mjs; it
// is not the production server.

import http from 'node:http';
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const FIXTURE = path.join(HERE, 'fixtures', 'mock-state.json');
const SESSION_COOKIE = 'session=mock-session';
const CSRF = 'mock-csrf';
const LIBRARY_KEY = 'mock-key';
const CURSOR_TTL_MS = Number(process.env.MOCK_CURSOR_TTL_MS ?? 300000);
const HEARTBEAT_MS = Number(process.env.MOCK_HEARTBEAT_MS ?? 15000);
const VIEW_FIELDS = new Set([
  'catalog', 'query', 'offset', 'subscriptions', 'subscription_detail', 'subscription_offset',
  'subscription_limit', 'subscription_query', 'invitations', 'invitation_id', 'invitation_offset',
  'owner_playlist', 'owner_offset', 'public_detail', 'public_offset', 'public_limit', 'public_query',
]);
const ID = /^[A-Za-z0-9_-]{1,128}$/;

function hash(value) {
  return crypto.createHash('sha256').update(JSON.stringify(value)).digest('hex');
}

function clone(value) {
  return JSON.parse(JSON.stringify(value));
}

function loadState() {
  const raw = JSON.parse(fs.readFileSync(FIXTURE, 'utf8'));
  return { ...raw, revision: raw.revision ?? 1, changes: raw.changes ?? [] };
}

function encodeCursor(value) {
  return 'mock.' + Buffer.from(JSON.stringify(value)).toString('base64url');
}

function decodeCursor(token) {
  if (typeof token !== 'string' || !token.startsWith('mock.')) return null;
  try {
    return JSON.parse(Buffer.from(token.slice(5), 'base64url').toString('utf8'));
  } catch {
    return null;
  }
}

function error(res, status, code, message, headers = {}) {
  res.writeHead(status, { 'Content-Type': 'application/json; charset=utf-8', 'X-Error-Code': code, ...headers });
  res.end(JSON.stringify({ error: { code, message } }));
}

function json(res, status, value) {
  res.writeHead(status, { 'Content-Type': 'application/json; charset=utf-8', 'Cache-Control': 'no-store' });
  res.end(JSON.stringify(value));
}

async function body(req, limit) {
  let size = 0;
  const chunks = [];
  for await (const chunk of req) {
    size += chunk.length;
    if (size > limit) throw new Error('body too large');
    chunks.push(chunk);
  }
  return Buffer.concat(chunks).toString('utf8');
}

function strictObject(raw, allowed) {
  const value = JSON.parse(raw);
  if (value === null || typeof value !== 'object' || Array.isArray(value)) throw new Error('object required');
  for (const key of Object.keys(value)) {
    if (!allowed.includes(key)) throw new Error(`unknown field ${key}`);
  }
  return value;
}

function validView(input) {
  if (input === null || typeof input !== 'object' || Array.isArray(input)) return null;
  for (const key of Object.keys(input)) {
    if (!VIEW_FIELDS.has(key)) return null;
  }
  for (const key of ['catalog', 'subscriptions', 'invitations']) {
    if (input[key] !== undefined && typeof input[key] !== 'boolean') return null;
  }
  for (const key of ['query', 'subscription_detail', 'subscription_query', 'invitation_id', 'owner_playlist', 'public_detail', 'public_query']) {
    if (input[key] === undefined) continue;
    if (typeof input[key] !== 'string' || Buffer.byteLength(input[key], 'utf8') > 1024) return null;
    if (['subscription_detail', 'invitation_id', 'owner_playlist', 'public_detail'].includes(key) && input[key] !== '' && !ID.test(input[key])) return null;
  }
  for (const key of ['offset', 'subscription_offset', 'subscription_limit', 'invitation_offset', 'owner_offset', 'public_offset', 'public_limit']) {
    if (input[key] === undefined) continue;
    if (!Number.isInteger(input[key])) return null;
    if (input[key] < 0 || input[key] > (key.endsWith('limit') ? 100 : 1000000)) return null;
  }
  const normalized = {};
  for (const key of VIEW_FIELDS) {
    if (['catalog', 'subscriptions', 'invitations'].includes(key)) normalized[key] = input[key] ?? false;
    else if (key.endsWith('offset') || key.endsWith('limit')) normalized[key] = input[key] ?? 0;
    else normalized[key] = input[key] ?? '';
  }
  return normalized;
}

function section(rows, meta, order = rows.map((row) => row.id)) {
  return { rows, order, meta };
}

function page(rows, offset, limit) {
  const start = Math.max(0, Number(offset) || 0);
  return { rows: rows.slice(start, start + limit), total: rows.length, offset: start, limit };
}

function snapshotFor(view, state) {
  const sections = {
    badge: section([], { pending: state.community.pending }, []),
  };
  if (view.catalog) {
    const rows = state.community.catalog.filter(row => [row.name, row.description, row.source_owner_name].join(' ').toLowerCase().includes(view.query.toLowerCase()));
    const slice = page(rows, view.offset, 30);
    sections.catalog = section(slice.rows, { total: slice.total, offset: slice.offset, limit: slice.limit });
  }
  if (view.subscriptions) {
    sections.subscriptions = section(state.community.subscriptions, {});
  }
  if (view.invitations) {
    const all = view.invitation_id
      ? state.community.invitations.filter((row) => row.id === view.invitation_id)
      : state.community.invitations;
    const slice = page(all, view.invitation_offset, 30);
    sections.invitations = section(slice.rows, { total: slice.total, offset: slice.offset, limit: slice.limit });
  }
  if (view.subscription_detail) {
    const detail = view.subscription_detail === 'p-radio' ? state.community.subscription_detail : null;
    if (!detail) {
      sections.subscription_tracks = section([], {});
    } else {
      const rows = filterTracks(detail.tracks, view.subscription_query, state.tracks);
      const slice = page(rows, view.subscription_offset, view.subscription_limit || 50);
      sections.subscription_tracks = section(slice.rows, { total: slice.total, offset: slice.offset, limit: slice.limit, playlist: detailPlaylist(detail, slice, view.subscription_query) });
    }
  }
  if (view.owner_playlist) {
    const available = view.owner_playlist === 'p-focus';
    sections.owner_playlist = section(available ? state.community.owner_playlist : [], { available });
    const all = available ? state.community.owner_invitations : [];
    const slice = page(all, view.owner_offset, 30);
    sections.owner_invitations = section(slice.rows, { total: slice.total, offset: slice.offset, limit: slice.limit });
  }
  if (view.public_detail) {
    const detail = view.public_detail === 'p-radio' ? state.community.public_detail : null;
    sections.detail_playlist = section(detail ? [detail.playlist] : [], { available: detail !== null });
    if (detail) {
      const rows = filterTracks(detail.tracks, view.public_query, state.tracks);
      const slice = page(rows, view.public_offset, view.public_limit || 50);
      sections.detail_playlist.rows = [detailPlaylist(detail, slice, view.public_query)];
      sections.detail_tracks = section(slice.rows, { total: slice.total, offset: slice.offset, limit: slice.limit });
    } else {
      sections.detail_tracks = section([], {});
    }
  }
  return sections;
}

function detailPlaylist(detail, slice, query) {
  return { ...detail.playlist, track_ids:slice.rows.map(track => track.id), track_count:detail.tracks.length, members_complete:query === '' && slice.rows.length === detail.tracks.length };
}

function filterTracks(tracks, query, originals) {
  return tracks.filter(track => {
    const metadata = originals.find(item => item.id === track.id) || track;
    return [metadata.title, metadata.artist, metadata.album, metadata.genre].join(' ').toLowerCase().includes(query.toLowerCase());
  });
}

function indexesFor(sections) {
  const indexes = {};
  for (const [name, value] of Object.entries(sections)) {
    const rows = {};
    for (const row of value.rows) rows[row.id] = hash(row);
    indexes[name] = { digest: hash([rows, value.order, value.meta]), rows, order: value.order, meta: value.meta };
  }
  return indexes;
}

function createMockServer(options = {}) {
  const heartbeatMs = options.heartbeatMs ?? HEARTBEAT_MS;
  const state = loadState();
  const cursors = new Map();
  const streams = new Set();
  let revision = state.revision;

  function commit() {
    revision += 1;
    const beta = state.tracks.find((track) => track.id === 't-beta');
    if (beta) {
      beta.version += 1;
      beta.modified_at = new Date(Date.UTC(2026, 10, 2, 10, revision)).toISOString().replace('.000Z', 'Z');
      state.changes.push({ revision, kind: 'track', entity_id: beta.id, deleted: false });
    }
    const focus = state.playlists.find((playlist) => playlist.id === 'p-focus');
    if (focus) {
      focus.version += 1;
      focus.updated_at = new Date(Date.UTC(2026, 10, 2, 10, revision)).toISOString().replace('.000Z', 'Z');
      state.changes.push({ revision, kind: 'playlist', entity_id: focus.id, deleted: false });
    }
    state.community.pending += 1;
    if (state.community.catalog[0]) state.community.catalog[0].track_count += 1;
    if (state.community.owner_playlist[0]) state.community.owner_playlist[0].version += 1;
    for (const client of streams) client.res.write('event: change\ndata: {}\n\n');
    return revision;
  }

  function libraryPage(auth, protocol, token, limit) {
    const v2 = protocol === '2';
    const result = {
      revision,
      changed_tracks: [],
      playlists: [],
      full_snapshot: false,
      deleted_tracks: [],
      deleted_playlists: [],
      cursor: '',
      has_more: false,
      reset: token === '',
    };
    if (v2) {
      result.object_delta = true;
      result.changed_track_ids = [];
    }
    let cursor = { mode: 'snapshot', revision, last: '', position: revision, binding: `user:${auth}` };
    if (token !== '') {
      const parsed = decodeCursor(token);
      if (!parsed || parsed.binding !== `user:${auth}`) {
        result.reset = true;
      } else if (parsed.mode === 'snapshot' && parsed.revision !== revision) {
        const err = new Error('sync restart');
        err.syncRestart = true;
        throw err;
      } else {
        cursor = parsed;
      }
    }
    if (cursor.mode === 'snapshot') {
      result.full_snapshot = true;
      const keys = [];
      for (const track of state.tracks) keys.push(`track/${track.id}`);
      for (const playlist of state.playlists) keys.push(`playlist/${playlist.id}`);
      keys.sort();
      const start = keys.findIndex((key) => key > (cursor.last ?? ''));
      const from = start === -1 ? keys.length : start;
      const page = keys.slice(from, from + limit);
      for (const key of page) {
        const [kind, id] = key.split('/');
        if (kind === 'track') {
          if (v2) result.changed_track_ids.push(id);
          else result.changed_tracks.push(state.tracks.find((track) => track.id === id));
        } else {
          result.playlists.push(state.playlists.find((playlist) => playlist.id === id));
        }
        cursor.last = key;
      }
      result.has_more = from + page.length < keys.length;
      if (!result.has_more) {
        cursor.mode = 'delta';
        cursor.position = revision;
        cursor.last = '';
      }
    } else {
      const pending = state.changes.filter((change) => change.revision > cursor.position);
      const batch = pending.slice(0, limit);
      const latest = new Map();
      for (const change of batch) {
        latest.set(`${change.kind}/${change.entity_id}`, change);
        cursor.position = change.revision;
      }
      for (const change of latest.values()) {
        if (change.kind === 'track') {
          if (change.deleted) result.deleted_tracks.push(change.entity_id);
          else if (v2) result.changed_track_ids.push(change.entity_id);
          else result.changed_tracks.push(state.tracks.find((track) => track.id === change.entity_id));
        } else if (!change.deleted) {
          result.playlists.push(state.playlists.find((playlist) => playlist.id === change.entity_id));
        }
      }
      result.has_more = pending.length > limit;
      if (batch.length === 0) cursor.position = revision;
      cursor.revision = revision;
    }
    result.cursor = encodeCursor(cursor);
    result.deleted_tracks.sort();
    result.deleted_playlists.sort();
    if (v2) result.changed_track_ids.sort();
    return result;
  }

  function realtimeSync(view) {
    const sections = snapshotFor(view, state);
    const indexes = indexesFor(sections);
    const digest = hash(indexes);
    return { sections, indexes, digest };
  }

  function serve(req, res) {
    const url = new URL(req.url, 'http://mock.local');
    const pathname = url.pathname;

    if (pathname === '/api/v2/health') {
      return json(res, 200, { status: 'ok', version: 'mock', schema: 9, revision });
    }
    if (pathname === '/__mock/state') {
      return json(res, 200, { revision, pending: state.community.pending });
    }
    if (pathname === '/__mock/commit' && req.method === 'POST') {
      return json(res, 200, { revision: commit() });
    }
    if (pathname === '/__mock/expire-cursors' && req.method === 'POST') {
      cursors.clear();
      return json(res, 200, { expired: true });
    }
    if (pathname === '/__mock/expire-auth' && req.method === 'POST') {
      for (const client of streams) {
        client.res.write('event: expired\ndata: {}\n\n');
        client.res.end();
      }
      streams.clear();
      return json(res, 200, { expired: true });
    }
    if (pathname === '/__mock/reconnect' && req.method === 'POST') {
      for (const client of streams) {
        client.res.write('event: reconnect\ndata: {}\n\n');
        client.res.end();
      }
      streams.clear();
      return json(res, 200, { reconnecting: true });
    }

    const libraryAuth = req.headers.authorization === `Bearer ${LIBRARY_KEY}` || req.headers['x-api-key'] === LIBRARY_KEY;
    if (pathname === '/api/v2/sync' && req.method === 'GET') {
      if (!libraryAuth) return error(res, 401, 'AUTH_INVALID', 'authentication required');
      const limit = Math.min(500, Math.max(1, Number(url.searchParams.get('limit') ?? 100) || 100));
      try {
        return json(res, 200, libraryPage('u-demo', url.searchParams.get('protocol') ?? '1', url.searchParams.get('cursor') ?? '', limit));
      } catch (err) {
        if (err.syncRestart) return error(res, 409, 'SYNC_RESTART', err.message);
        return error(res, 400, 'INVALID_CURSOR', err.message);
      }
    }
    if (pathname === '/api/v2/sync/tracks' && req.method === 'POST') {
      if (!libraryAuth) return error(res, 401, 'AUTH_INVALID', 'authentication required');
      return body(req, 64 << 10).then((raw) => {
        let input;
        try {
          input = strictObject(raw, ['cursor', 'ids']);
        } catch (err) {
          return error(res, 400, 'INVALID_SYNC_BATCH', err.message);
        }
        if (!Array.isArray(input.ids) || input.ids.length < 1 || input.ids.length > 100 || new Set(input.ids).size !== input.ids.length) {
          return error(res, 400, 'INVALID_SYNC_BATCH', 'cursor and 1 to 100 unique track IDs required');
        }
        if (input.ids.some((id) => typeof id !== 'string' || !ID.test(id))) {
          return error(res, 400, 'INVALID_SYNC_BATCH', 'invalid track ID');
        }
        if (input.cursor === '') return error(res, 400, 'INVALID_SYNC_BATCH', 'cursor required');
        const cursor = decodeCursor(input.cursor);
        if (!cursor || cursor.mode !== 'delta' || cursor.last !== '' || cursor.revision !== revision) {
          return error(res, 409, 'SYNC_RESTART', 'sync cursor is stale');
        }
        const tracks = [];
        for (const id of input.ids) {
          const track = state.tracks.find((candidate) => candidate.id === id);
          if (!track) return error(res, 404, 'TRACK_NOT_FOUND', 'track is not available');
          tracks.push(track);
        }
        return json(res, 200, { revision, tracks });
      }).catch((err) => error(res, 400, 'INVALID_SYNC_BATCH', err.message));
    }

    if (pathname === '/api/v2/community/sync' && req.method === 'POST') {
      if (!(req.headers.cookie ?? '').includes(SESSION_COOKIE)) return error(res, 403, 'SESSION_REQUIRED', 'account session required');
      if (req.headers['x-csrf-token'] !== CSRF) return error(res, 403, 'CSRF_DENIED', 'same-origin CSRF token required');
      return body(req, 16 << 10).then((raw) => {
        let input;
        try {
          input = strictObject(raw, ['cursor', 'view']);
        } catch (err) {
          return error(res, 400, 'INVALID_REALTIME_VIEW', err.message);
        }
        if (typeof (input.cursor ?? '') !== 'string' || (input.cursor ?? '').length > 128) {
          return error(res, 400, 'INVALID_REALTIME_VIEW', 'invalid cursor');
        }
        const viewInput = input.view ?? {};
        const view = validView(viewInput);
        if (!view) return error(res, 400, 'INVALID_REALTIME_VIEW', 'invalid realtime view');
        const { sections, indexes, digest } = realtimeSync(view);
        const cursorToken = input.cursor ?? '';
        const now = Date.now();
        const binding = 'mock-binding';
        const viewKey = JSON.stringify(view);
        const before = cursors.get(cursorToken);
        const reset = !before || before.binding !== binding || before.view !== viewKey || before.expires < now;
        const previous = reset ? null : before;
        const reply = { cursor: '', reset, sections: {} };
        for (const [name, value] of Object.entries(sections)) {
          const current = indexes[name];
          const prior = previous?.sections?.[name];
          if (!reset && prior && prior.digest === current.digest) continue;
          const delta = { changed: [], removed: [], order: value.order, meta: value.meta };
          if (reset || !prior) delta.reset = true;
          for (const row of value.rows) {
            if (delta.reset || prior.rows[row.id] !== current.rows[row.id]) delta.changed.push(row);
          }
          for (const id of Object.keys(prior?.rows ?? {})) {
            if (!(id in current.rows)) delta.removed.push(id);
          }
          delta.removed.sort();
          reply.sections[name] = delta;
        }
        if (!reset && previous.digest === digest) {
          reply.cursor = cursorToken;
          previous.expires = now + CURSOR_TTL_MS;
          cursors.set(cursorToken, previous);
        } else {
          const token = 'mock.' + crypto.randomBytes(18).toString('base64url');
          cursors.set(token, { binding, view: viewKey, sections: indexes, digest, expires: now + CURSOR_TTL_MS });
          reply.cursor = token;
        }
        return json(res, 200, reply);
      }).catch((err) => error(res, 400, 'INVALID_REALTIME_VIEW', err.message));
    }

    if (pathname === '/api/v2/community/events' && req.method === 'GET') {
      if (!(req.headers.cookie ?? '').includes(SESSION_COOKIE)) return error(res, 403, 'SESSION_REQUIRED', 'account session required');
      const rawView = url.searchParams.get('view') ?? '{}';
      if (Buffer.byteLength(rawView, 'utf8') > 4096) return error(res, 400, 'INVALID_REALTIME_VIEW', 'invalid realtime view');
      let view;
      try {
        view = JSON.parse(rawView);
      } catch {
        return error(res, 400, 'INVALID_REALTIME_VIEW', 'invalid realtime view');
      }
      if (!validView(view)) return error(res, 400, 'INVALID_REALTIME_VIEW', 'invalid realtime view');
      res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-store', 'X-Accel-Buffering': 'no' });
      res.write('event: ready\ndata: {}\n\n');
      const heartbeat = setInterval(() => res.write(': heartbeat\n\n'), heartbeatMs);
      const client = { res };
      streams.add(client);
      req.on('close', () => {
        clearInterval(heartbeat);
        streams.delete(client);
      });
      return;
    }

    error(res, 404, 'NOT_FOUND', 'no such mock route');
  }

  return {
    server: http.createServer((req, res) => {
      Promise.resolve(serve(req, res)).catch((err) => {
        if (!res.headersSent) error(res, 500, 'MOCK_FAILED', err.message);
        else res.end();
      });
    }),
    state,
    get revision() { return revision; },
    commit,
  };
}

export { createMockServer };

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const flag = process.argv.indexOf('--port');
  const port = Number(flag === -1 ? (process.env.MOCK_PORT ?? 8787) : process.argv[flag + 1]);
  const mock = createMockServer();
  mock.server.listen(port, '127.0.0.1', () => {
    console.log(`mock MusicServer protocol on http://127.0.0.1:${mock.server.address().port}`);
    console.log(`library auth: Authorization: Bearer ${LIBRARY_KEY}`);
    console.log(`community cookie: ${SESSION_COOKIE}; CSRF: ${CSRF}`);
  });
}
