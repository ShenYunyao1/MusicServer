'use strict';

const resumeChunkBytes = 4 * 1048576;
const resumeRuns = new Map();
let resumeWorkspaceEpoch = 0, resumeSaveTimer, resumeDatabasePromise, resumeQueueWrite = Promise.resolve();

function resumeDatabase() {
  if (!resumeDatabasePromise) resumeDatabasePromise = new Promise((resolve, reject) => {
    const request = indexedDB.open('MusicServerUploadQueue', 1);
    request.onupgradeneeded = () => request.result.createObjectStore('queues');
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error || new Error('无法保存上传队列元数据'));
    request.onblocked = () => reject(new Error('上传队列存储被其他页面占用'));
  }).catch(error => { resumeDatabasePromise = null; throw error; });
  return resumeDatabasePromise;
}

const resumeQueueStorage = {
  async read(owner) {
    const db = await resumeDatabase();
    return new Promise((resolve, reject) => {
      const request = db.transaction('queues').objectStore('queues').get(owner);
      request.onsuccess = () => resolve(Array.isArray(request.result) ? request.result : []);
      request.onerror = () => reject(request.error);
    });
  },
  async write(owner, records) {
    const db = await resumeDatabase();
    return new Promise((resolve, reject) => {
      const transaction = db.transaction('queues', 'readwrite');
      transaction.objectStore('queues').put(records, owner);
      transaction.oncomplete = resolve;
      transaction.onerror = transaction.onabort = () => reject(transaction.error || new Error('无法保存上传队列元数据'));
    });
  },
};

function resumeFile(upload) {
  return typeof upload.sourceFile?.slice === 'function' ? upload.sourceFile : typeof upload.file?.slice === 'function' ? upload.file : null;
}

function resumeDraft(upload) {
  // No File, Blob, buffer, cookie, CSRF token, or server credential enters IDB.
  const attachments = { ...(upload.resumeMissingAttachments || {}) };
  for (const kind of ['lyrics', 'cover']) if (upload[kind + 'File']?.file) attachments[kind] = upload[kind + 'File'].file.name;
  return {
    id: upload.id, resumeId: upload.resumeId || null,
    name: upload.file.name, size: upload.file.size, lastModified: upload.file.lastModified || 0,
    path: upload.path || '', directory: upload.directory || '',
    edits: { ...upload.edits }, attachments,
    tagDraft: upload.tagDraft ? JSON.parse(JSON.stringify(upload.tagDraft)) : undefined,
    ignoreAutomaticCover: Boolean(upload.ignoreAutomaticCover), ignoreAutomaticLyrics: Boolean(upload.ignoreAutomaticLyrics),
    detailsOpen: Boolean(upload.detailsOpen), editorOpen: Boolean(upload.editorOpen),
  };
}

function persistResumeQueue() {
  clearTimeout(resumeSaveTimer);
  const owner = state.user?.id;
  if (!owner) return Promise.resolve(false);
  // A cancellation flag interrupts the sender before DELETE is acknowledged.
  // Keep its draft until the row is actually removed, including slow/failed DELETEs.
  const records = state.uploads.filter(upload => !upload.resumeCompleted && !['完成', '已去重'].includes(upload.status)).map(resumeDraft);
  resumeQueueWrite = resumeQueueWrite.catch(() => {}).then(() => resumeQueueStorage.write(owner, records)).then(() => true, error => {
    if (state.user?.id === owner) setStatus('队列元数据未能保存；刷新后编辑和附件需重新选择：' + error.message, 'error');
    return false;
  });
  return resumeQueueWrite;
}

function scheduleResumeQueueSave() {
  clearTimeout(resumeSaveTimer);
  if (state.user) resumeSaveTimer = setTimeout(() => { void persistResumeQueue(); }, 180);
}

function resumeCurrent(owner, epoch, upload) {
  return state.user?.id === owner && resumeWorkspaceEpoch === epoch && (!upload || state.uploads.includes(upload));
}

function resumeGuard(owner, epoch, upload) {
  if (!resumeCurrent(owner, epoch, upload) || upload?.resumePaused || upload?.resumeCanceled) {
    const error = new Error('上传已暂停'); error.resumePaused = true; throw error;
  }
}

function resumeError(status, payload) {
  const error = new Error((status ? 'HTTP ' + status + ' · ' : '') + (payload?.error?.message || '上传连接中断，请检查网络后继续'));
  error.status = status; error.code = payload?.error?.code || '';
  return error;
}

async function resumeJSON(path, method = 'GET', body, owner = state.user?.id, epoch = resumeWorkspaceEpoch) {
  if (!resumeCurrent(owner, epoch)) { const error = new Error('账户已变化'); error.resumePaused = true; throw error; }
  const headers = { 'X-CSRF-Token': state.csrf };
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  const response = await fetch('/api/v2/uploads/resumable' + path, { method, credentials: 'same-origin', cache: 'no-store', headers, body: body === undefined ? undefined : JSON.stringify(body) });
  const text = await response.text();
  let payload;
  try { payload = text ? JSON.parse(text) : null; } catch (_) { throw new Error('服务端返回了无效的续传响应'); }
  if (!resumeCurrent(owner, epoch)) { const error = new Error('账户已变化'); error.resumePaused = true; throw error; }
  if (!response.ok) {
    if (response.status === 401) setLoggedOut();
    throw resumeError(response.status, payload);
  }
  return payload;
}

function validateResumeTask(task) {
  if (!task || !/^[0-9a-f]{32}$/.test(task.id) || !Number.isSafeInteger(task.size) || task.size <= 0 || !Number.isSafeInteger(task.offset) || task.offset < 0 || task.offset > task.size || !Array.isArray(task.chunks) || task.chunks.length > 4096) throw new Error('续传检查点无效，请取消任务后重新上传');
  let offset = 0;
  for (const chunk of task.chunks) {
    if (chunk.offset !== offset || !Number.isSafeInteger(chunk.size) || chunk.size <= 0 || chunk.size > resumeChunkBytes || !/^[0-9a-f]{64}$/.test(chunk.sha256)) throw new Error('续传检查点无效，请取消任务后重新上传');
    offset += chunk.size;
  }
  if (offset !== task.offset) throw new Error('续传检查点不完整，请取消任务后重新上传');
  return task;
}

function applyResumeTask(upload, value) {
  const task = validateResumeTask(value);
  upload.resumeId = task.id; upload.resumeTask = task;
  upload.needsSourceFile = !resumeFile(upload);
  upload.progress = { ...(upload.progress || {}), phase: 'paused', loaded: task.offset, total: task.size, rate: 0 };
  return task;
}

// LAN HTTP is supported by the server. WebCrypto digest is unavailable in that
// browser context, so hash the same bounded block locally without a network API.
function resumeSHA256Fallback(buffer) {
  const bytes = new Uint8Array(buffer), words = new Uint32Array(64);
  const h = new Uint32Array([0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19]);
  const k = new Uint32Array([
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
    0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
    0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
    0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
    0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
    0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
  ]);
  const rotate = (value, count) => (value >>> count) | (value << (32 - count));
  const paddedLength = Math.ceil((bytes.length + 9) / 64) * 64;
  const byteAt = index => index < bytes.length ? bytes[index] : index === bytes.length ? 128 : index >= paddedLength - 4 ? (bytes.length * 8) >>> ((paddedLength - 1 - index) * 8) & 255 : 0;
  for (let block = 0; block < paddedLength; block += 64) {
    for (let i = 0; i < 16; i++) { const offset = block + i * 4; words[i] = (byteAt(offset) << 24) | (byteAt(offset + 1) << 16) | (byteAt(offset + 2) << 8) | byteAt(offset + 3); }
    for (let i = 16; i < 64; i++) { const a = words[i - 15], b = words[i - 2]; words[i] = words[i - 16] + (rotate(a, 7) ^ rotate(a, 18) ^ (a >>> 3)) + words[i - 7] + (rotate(b, 17) ^ rotate(b, 19) ^ (b >>> 10)); }
    let [a, b, c, d, e, f, g, v] = h;
    for (let i = 0; i < 64; i++) {
      const first = (v + (rotate(e, 6) ^ rotate(e, 11) ^ rotate(e, 25)) + ((e & f) ^ (~e & g)) + k[i] + words[i]) >>> 0;
      const second = ((rotate(a, 2) ^ rotate(a, 13) ^ rotate(a, 22)) + ((a & b) ^ (a & c) ^ (b & c))) >>> 0;
      v = g; g = f; f = e; e = (d + first) >>> 0; d = c; c = b; b = a; a = (first + second) >>> 0;
    }
    h[0] += a; h[1] += b; h[2] += c; h[3] += d; h[4] += e; h[5] += f; h[6] += g; h[7] += v;
  }
  return Array.from(h, value => value.toString(16).padStart(8, '0')).join('');
}

async function resumeHash(bytes) {
  if (!crypto.subtle) return resumeSHA256Fallback(bytes);
  return Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes)), value => value.toString(16).padStart(2, '0')).join('');
}

async function verifyResumePrefix(upload, file, task, owner, epoch) {
  validateResumeTask(task);
  if (!file || file.size !== task.size) throw new Error('所选文件大小不匹配，请选择原文件');
  for (const chunk of task.chunks) {
    resumeGuard(owner, epoch, upload);
    const bytes = await file.slice(chunk.offset, chunk.offset + chunk.size).arrayBuffer();
    if (bytes.byteLength !== chunk.size || await resumeHash(bytes) !== chunk.sha256) throw new Error('所选文件的已上传前缀不匹配，请选择原文件');
    resumeGuard(owner, epoch, upload);
  }
}

async function reselectResumeFile(upload, file) {
  const owner = state.user?.id, epoch = resumeWorkspaceEpoch;
  upload.resumePaused = false; upload.error = ''; upload.status = '校验原文件'; renderUploads();
  try {
    let task = upload.resumeTask;
    if (upload.resumeId) task = await resumeJSON('/' + upload.resumeId, 'GET', undefined, owner, epoch);
    resumeGuard(owner, epoch, upload);
    if (task) await verifyResumePrefix(upload, file, task, owner, epoch);
    else if (file.size !== upload.file.size) throw new Error('所选文件大小不匹配，请选择原文件');
    resumeGuard(owner, epoch, upload);
    upload.file = file; upload.sourceFile = file; upload.needsSourceFile = false;
    if (task) applyResumeTask(upload, task);
    upload.status = '已暂停'; upload.resumePaused = true;
    await persistResumeQueue(); return true;
  } catch (error) {
    if (resumeCurrent(owner, epoch, upload)) { upload.error = error.message; upload.status = '选择原文件继续'; upload.resumePaused = true; }
    throw error;
  } finally { if (resumeCurrent(owner, epoch, upload)) renderUploads(); }
}

function resumeChunkRequest(upload, task, bytes, digest, owner, epoch) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest(), started = performance.now();
    upload.transfer = xhr; upload.progress = { phase: 'sending', loaded: task.offset, total: task.size, rate: 0 }; upload.status = '上传中'; updateUploadProgress(upload);
    xhr.open('PATCH', '/api/v2/uploads/resumable/' + task.id); xhr.withCredentials = true; xhr.timeout = 120000;
    xhr.setRequestHeader('X-CSRF-Token', state.csrf); xhr.setRequestHeader('Content-Type', 'application/octet-stream');
    xhr.setRequestHeader('Upload-Offset', String(task.offset)); xhr.setRequestHeader('Upload-Checksum', 'sha256 ' + digest);
    xhr.upload.onprogress = event => {
      if (!resumeCurrent(owner, epoch, upload) || upload.resumePaused) return;
      upload.progress.loaded = task.offset + Math.min(bytes.byteLength, event.loaded);
      upload.progress.rate = event.loaded / Math.max(.001, (performance.now() - started) / 1000); updateUploadProgress(upload);
    };
    xhr.onload = () => {
      let payload;
      try { payload = JSON.parse(xhr.responseText); } catch (_) { reject(new Error('服务端返回了无效的续传响应')); return; }
      if (xhr.status < 200 || xhr.status >= 300) { if (xhr.status === 401 && resumeCurrent(owner, epoch, upload)) setLoggedOut(); reject(resumeError(xhr.status, payload)); return; }
      resolve(payload);
    };
    xhr.onerror = xhr.ontimeout = () => reject(resumeError(0));
    xhr.onabort = () => { const error = new Error('上传已暂停'); error.resumePaused = true; reject(error); };
    xhr.onloadend = () => { if (upload.transfer === xhr) upload.transfer = null; };
    xhr.send(bytes);
  });
}

function resumeWait(milliseconds, upload) {
  return new Promise(resolve => { const timer = setTimeout(() => { upload.resumeWake = null; resolve(); }, milliseconds); upload.resumeWake = () => { clearTimeout(timer); upload.resumeWake = null; resolve(); }; });
}

async function resumeBase64(file, limit, label) {
  if (file.size > limit) throw new Error(label + '超过大小限制');
  const bytes = new Uint8Array(await file.arrayBuffer()); let binary = '';
  for (let offset = 0; offset < bytes.length; offset += 32768) binary += String.fromCharCode(...bytes.subarray(offset, offset + 32768));
  return btoa(binary);
}

async function applyResumePreview(upload, preview, owner, epoch) {
  resumeGuard(owner, epoch, upload);
  upload.preview = preview; upload.stageId = preview.id;
  for (const name of ['title', 'artist', 'album', 'genre', 'comment', 'lyrics']) if (!Object.hasOwn(upload.edits, name)) upload[name] = preview.track?.[name] || '';
  if (!upload.coverFile?.manual && !upload.ignoreAutomaticCover) {
    const taggedCover = matchingAttachment(upload, 'cover', preview.track);
    if (taggedCover && taggedCover.id !== upload.coverFile?.id) {
      invalidateLocalParse(upload); releaseLocalPreviewURL(upload); releaseAttachmentURL(upload.coverFile, upload);
      upload.localPreview = null; upload.localStatus = ''; upload.coverFile = taggedCover;
      const attachment = new FormData(); attachment.append('cover', taggedCover.file); attachment.append('cover_source', 'external');
      const updated = await uploadRequest('uploads/stage/' + encodeURIComponent(upload.stageId) + '/cover', 'PUT', attachment, upload);
      resumeGuard(owner, epoch, upload); updated.timings = preview.timings; upload.preview = updated;
    }
  }
  upload.resumeMissingAttachments = {}; upload.progress.phase = 'complete'; upload.progress.loaded = upload.file.size;
  upload.status = '待确认'; await persistResumeQueue(); return upload.preview;
}

async function resumeFinishRequest(upload, task, input, owner, epoch) {
  let body = input;
  for (let retries = 0; ; retries++) {
    resumeGuard(owner, epoch, upload);
    try { return await resumeJSON('/' + task.id + '/finish', 'POST', body, owner, epoch); }
    catch (error) {
      resumeGuard(owner, epoch, upload);
      if (retries >= 3 || error.status && ![408, 429, 500, 502, 503, 504].includes(error.status)) throw error;
      upload.status = '恢复服务器校验结果'; renderUploads(); await resumeWait(300 * 2 ** retries, upload);
      resumeGuard(owner, epoch, upload);
      task = await resumeJSON('/' + task.id, 'GET', undefined, owner, epoch);
      resumeGuard(owner, epoch, upload); applyResumeTask(upload, task);
      if (task.state === 'ready') body = {};
      else if (task.state !== 'uploading' || task.offset !== task.size) throw new Error('服务器校验检查点发生变化，请重新继续上传');
    }
  }
}

function resumeStageUpload(upload) {
  if (resumeRuns.has(upload)) return resumeRuns.get(upload);
  const owner = state.user?.id, epoch = resumeWorkspaceEpoch;
  upload.resumePaused = false; upload.resumeCanceled = false; upload.inFlight = true; upload.error = ''; upload.status = '准备续传';
  const running = (async () => {
    const started = performance.now();
    try {
      resumeGuard(owner, epoch, upload);
      if (upload.stageId) {
        if (!resumeFile(upload)) throw new Error('重新校验需要选择原文件');
        await cancelResumeUpload(upload, true); resumeGuard(owner, epoch, upload);
      }
      let task;
      if (!upload.resumeId) {
        const file = resumeFile(upload);
        if (!file) throw new Error('请选择原文件后继续');
        const attachments = {}; for (const kind of ['cover', 'lyrics']) if (upload[kind + 'File']?.file) attachments[kind] = upload[kind + 'File'].file.name;
        task = await resumeJSON('', 'POST', { name: file.name, size: file.size, fields: upload.edits, attachments }, owner, epoch);
      } else task = await resumeJSON('/' + upload.resumeId, 'GET', undefined, owner, epoch);
      // Pause/cancel may arrive while creation is in flight. Retain the server
      // identity before honoring that flag so continue reuses it and cancel can
      // delete it. An old account's acknowledgement never enters the new queue.
      if (resumeCurrent(owner, epoch, upload)) { applyResumeTask(upload, task); await persistResumeQueue(); }
      resumeGuard(owner, epoch, upload);
      if (task.state === 'ready') {
        const preview = await resumeFinishRequest(upload, task, {}, owner, epoch);
        return await applyResumePreview(upload, preview, owner, epoch);
      }
      if (task.state !== 'uploading') throw new Error('上传检查点已失效，请取消任务后重新上传');
      const file = resumeFile(upload);
      if (task.offset < task.size) {
        if (!file) { upload.needsSourceFile = true; throw new Error('请选择原文件后继续'); }
        upload.status = '校验已上传前缀'; renderUploads(); await verifyResumePrefix(upload, file, task, owner, epoch);
      }
      let retries = 0;
      while (task.offset < task.size) {
        resumeGuard(owner, epoch, upload);
        const end = Math.min(task.size, task.offset + resumeChunkBytes), bytes = await file.slice(task.offset, end).arrayBuffer(), digest = await resumeHash(bytes);
        resumeGuard(owner, epoch, upload);
        if (bytes.byteLength !== end - task.offset) throw new Error('原文件读取长度变化，请重新选择文件');
        try {
          const next = await resumeChunkRequest(upload, task, bytes, digest, owner, epoch);
          resumeGuard(owner, epoch, upload); task = applyResumeTask(upload, next); retries = 0;
          upload.progress.phase = 'sending'; await persistResumeQueue();
        } catch (error) {
          resumeGuard(owner, epoch, upload);
          if (error.status && ![408, 409, 429, 500, 502, 503, 504].includes(error.status)) throw error;
          if (++retries > 3) throw error;
          upload.status = '连接中断，正在恢复检查点'; renderUploads(); await resumeWait(300 * 2 ** (retries - 1), upload);
          resumeGuard(owner, epoch, upload);
          task = await resumeJSON('/' + upload.resumeId, 'GET', undefined, owner, epoch);
          resumeGuard(owner, epoch, upload); applyResumeTask(upload, task); await verifyResumePrefix(upload, file, task, owner, epoch);
        }
      }
      resumeGuard(owner, epoch, upload);
      const missing = Object.keys(upload.resumeMissingAttachments || {}).filter(kind => !upload[kind + 'File']);
      if (missing.length) throw new Error('封面或歌词附件需要重新选择，或明确选择不使用缺失附件');
      const input = { fields: upload.edits };
      if (upload.lyricsFile?.file) input.lyrics = await resumeBase64(upload.lyricsFile.file, 2 * 1048576, '歌词附件');
      if (upload.coverFile?.file) { input.cover = await resumeBase64(upload.coverFile.file, 8 * 1048576, '封面附件'); input.cover_source = upload.coverFile.manual ? 'manual' : 'external'; }
      resumeGuard(owner, epoch, upload); upload.status = '等待校验'; upload.progress = { phase: 'waiting', loaded: task.size, total: task.size, rate: 0, waitStarted: performance.now(), sendMS: performance.now() - started }; renderUploads();
      const preview = await resumeFinishRequest(upload, task, input, owner, epoch);
      resumeGuard(owner, epoch, upload); upload.resumeTask.state = 'ready'; upload.resumeTask.stage_id = preview.id;
      return await applyResumePreview(upload, preview, owner, epoch);
    } catch (error) {
      if (!resumeCurrent(owner, epoch, upload)) return null;
      upload.status = error.resumePaused || upload.resumePaused ? '已暂停' : upload.needsSourceFile ? '选择原文件继续' : '失败';
      upload.error = error.resumePaused ? '' : error.message; if (upload.progress) { upload.progress.phase = 'paused'; upload.progress.loaded = upload.resumeTask?.offset || 0; upload.progress.rate = 0; }
      await persistResumeQueue(); if (error.resumePaused) return null; throw error;
    } finally {
      resumeRuns.delete(upload); upload.inFlight = false;
      if (resumeCurrent(owner, epoch, upload)) renderUploads();
    }
  })();
  resumeRuns.set(upload, running); renderUploads(); return running;
}

function pauseResumeUpload(upload) {
  upload.resumePaused = true; upload.status = '已暂停'; upload.resumeWake?.(); upload.transfer?.abort();
  if (upload.progress) { upload.progress.phase = 'paused'; upload.progress.loaded = upload.resumeTask?.offset || 0; upload.progress.rate = 0; }
  void persistResumeQueue(); renderUploads();
}

async function cancelResumeUpload(upload, restarting = false) {
  const owner = state.user?.id, epoch = resumeWorkspaceEpoch;
  if (!restarting) { upload.resumeCanceled = true; pauseResumeUpload(upload); await resumeRuns.get(upload); }
  if (upload.resumeId) {
    try { await resumeJSON('/' + upload.resumeId, 'DELETE', undefined, owner, epoch); } catch (error) {
      if (error.status !== 404) {
        if (resumeCurrent(owner, epoch, upload)) { upload.resumeCanceled = false; await persistResumeQueue(); renderUploads(); }
        throw error;
      }
    }
  } else if (upload.stageId) {
    try { await api('uploads/stage/' + encodeURIComponent(upload.stageId), 'DELETE'); } catch (error) { if (error.status !== 404) throw error; }
  }
  if (!resumeCurrent(owner, epoch, upload)) return;
  if (restarting) resumeGuard(owner, epoch, upload);
  upload.resumeId = null; upload.resumeTask = null; upload.stageId = null; upload.preview = null; upload.progress = null;
  upload.resumeCanceled = false; upload.resumePaused = false; upload.status = '待暂存'; await persistResumeQueue();
}

async function completeResumeUpload(upload) {
  upload.resumeCompleted = true;
  const owner = state.user?.id, epoch = resumeWorkspaceEpoch, id = upload.resumeId;
  try { if (id) await resumeJSON('/' + id, 'DELETE', undefined, owner, epoch); }
  catch (error) { if (resumeCurrent(owner, epoch, upload) && error.status !== 404) upload.error = '曲目已入库；临时任务将在到期后清理'; }
  if (!resumeCurrent(owner, epoch, upload)) return;
  upload.resumeId = null; upload.resumeTask = null; await persistResumeQueue();
}

function clearResumeWorkspace() {
  void persistResumeQueue(); clearTimeout(resumeSaveTimer); resumeWorkspaceEpoch++;
  for (const upload of state.uploads) { upload.resumePaused = true; upload.resumeWake?.(); upload.transfer?.abort(); }
}

async function restoreResumeQueue() {
  const owner = state.user?.id, epoch = resumeWorkspaceEpoch;
  if (!owner || state.user.must_change_password) return;
  let records = [], remote;
  try { records = await resumeQueueStorage.read(owner); } catch (error) { if (resumeCurrent(owner, epoch)) setStatus('队列编辑恢复失败：' + error.message, 'error'); }
  if (!resumeCurrent(owner, epoch)) return;
  try { remote = await resumeJSON('', 'GET', undefined, owner, epoch); } catch (error) { if (resumeCurrent(owner, epoch)) setStatus('上传任务暂时无法恢复：' + error.message, 'error'); return; }
  if (!resumeCurrent(owner, epoch)) return;
  const items = remote?.items || [], byTask = new Map(records.filter(row => row.resumeId).map(row => [row.resumeId, row]));
  const pending = records.filter(row => !row.resumeId).map(row => ({ row, task: null }));
  for (const task of items) { if (task.state === 'canceled') continue; pending.push({ row: byTask.get(task.id) || { id: browserUUID(), name: task.name, size: task.size, edits: task.fields || {}, attachments: task.attachments || {} }, task }); }
  for (const { row: saved, task } of pending) {
    if (!resumeCurrent(owner, epoch)) return;
    if (state.uploads.some(upload => task ? upload.resumeId === task.id : upload.id === saved.id)) continue;
    const file = { name: saved.name || task.name, size: saved.size || task.size, lastModified: saved.lastModified || 0 };
    const upload = { ...describeFile(file, ++state.batch), id: saved.id || browserUUID(), path: saved.path || '', directory: saved.directory || '', file, sourceFile: null, needsSourceFile: true, edits: saved.edits || {}, preview: null, localPreview: null, localPromise: null, localStatus: '', stageId: null, status: '选择原文件继续', error: '', lyricsFile: null, coverFile: null, resumePaused: true, resumeMissingAttachments: saved.attachments || task?.attachments || {}, ignoreAutomaticCover: Boolean(saved.ignoreAutomaticCover), ignoreAutomaticLyrics: Boolean(saved.ignoreAutomaticLyrics), detailsOpen: saved.detailsOpen, editorOpen: saved.editorOpen };
    for (const name of ['title', 'artist', 'album', 'genre', 'comment', 'lyrics']) upload[name] = upload.edits[name] || '';
    if (saved.tagDraft) upload.tagDraft = JSON.parse(JSON.stringify(saved.tagDraft));
    state.uploads.push(upload);
    if (task) {
      try {
        applyResumeTask(upload, task);
        if (task.state === 'ready') {
          const preview = await resumeJSON('/' + task.id + '/finish', 'POST', {}, owner, epoch);
          if (!resumeCurrent(owner, epoch, upload)) return;
          upload.preview = preview; upload.stageId = preview.id; upload.status = '待确认'; upload.resumeMissingAttachments = {}; upload.progress.phase = 'complete';
          for (const name of ['title', 'artist', 'album', 'genre', 'comment', 'lyrics']) if (!Object.hasOwn(upload.edits, name)) upload[name] = preview.track?.[name] || '';
        } else if (task.state !== 'uploading') { upload.status = '失败'; upload.error = '上传检查点已损坏，请取消后重新上传'; }
      } catch (error) { if (resumeCurrent(owner, epoch, upload)) { upload.status = '失败'; upload.error = error.message; } }
    }
  }
  if (resumeCurrent(owner, epoch)) { renderUploads(); await persistResumeQueue(); }
}

function decorateResumableUploads() {
  scheduleResumeQueueSave();
  for (const upload of state.uploads) {
    const row = document.querySelector('[data-upload-id="' + upload.id + '"]'), buttons = row?.querySelector('.upload-buttons');
    if (!row || !buttons || row.querySelector('[data-resume-controls]')) continue;
    row.addEventListener('input', scheduleResumeQueueSave);
    if (['完成', '已去重'].includes(upload.status)) continue;
    const controls = document.createElement('div'); controls.dataset.resumeControls = ''; controls.className = 'upload-attachments';
    const text = document.createElement('span'); text.className = 'status';
    text.textContent = upload.resumeTask ? '已确认 ' + formatBytes(upload.resumeTask.offset) + ' / ' + formatBytes(upload.resumeTask.size) : '分块上传可暂停，刷新后选择原文件继续'; controls.append(text);
    const pause = document.createElement('button'); pause.type = 'button'; pause.className = 'btn small'; pause.dataset.resumePause = ''; pause.textContent = '暂停'; pause.hidden = !upload.inFlight; pause.onclick = () => pauseResumeUpload(upload); controls.append(pause);
    const resume = document.createElement('button'); resume.type = 'button'; resume.className = 'btn small'; resume.dataset.resumeContinue = ''; resume.textContent = '继续上传'; resume.hidden = !upload.resumeId || Boolean(upload.stageId) || upload.inFlight; resume.disabled = upload.needsSourceFile && upload.resumeTask?.offset !== upload.file.size; resume.onclick = () => run(() => resumeStageUpload(upload)); controls.append(resume);
    const chooser = document.createElement('label'); chooser.className = 'btn small'; chooser.textContent = '选择原文件继续'; chooser.hidden = !upload.needsSourceFile || upload.inFlight;
    const input = document.createElement('input'); input.type = 'file'; input.accept = '.aac,.flac,.m4a,.mp3,.oga,.ogg,.opus,.wav,.wma'; input.dataset.resumeFile = ''; input.style.display = 'none'; input.onchange = () => { if (input.files[0]) run(() => reselectResumeFile(upload, input.files[0])); }; chooser.append(input); controls.append(chooser);
    const missing = Object.keys(upload.resumeMissingAttachments || {}).filter(kind => !upload[kind + 'File']);
    if (missing.length) {
      const notice = document.createElement('span'); notice.className = 'status'; notice.textContent = '需重新选择附件：' + missing.map(kind => upload.resumeMissingAttachments[kind]).join('、'); controls.append(notice);
      const omit = document.createElement('button'); omit.type = 'button'; omit.className = 'btn small'; omit.textContent = '不使用缺失附件'; omit.disabled = Boolean(upload.inFlight); omit.onclick = () => { upload.resumeMissingAttachments = {}; void persistResumeQueue(); renderUploads(); }; controls.append(omit);
    }
    buttons.before(controls);
    const stage = row.querySelector('[data-stage-upload]'); if (stage && upload.needsSourceFile && !upload.stageId) stage.disabled = upload.resumeTask?.offset !== upload.file.size;
    // Pause and explicit cancellation remain available during bulk uploads.
    const remove = row.querySelector('[data-remove-upload]'); if (remove) remove.disabled = false;
  }
}

window.addEventListener('pagehide', () => { void persistResumeQueue(); });
