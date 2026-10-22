const assert = require('node:assert/strict');
const path = require('node:path');
const fs = require('node:fs');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');

// Run against an isolated empty validation server, never a production library.
async function main() {
  const base = process.env.MUSIC_SERVER_UI_URL;
  const { login } = require('./account-session.cjs');
  const sampleRoot = process.env.MUSIC_SERVER_UI_SAMPLES;
  assert(base && sampleRoot, 'Set MUSIC_SERVER_UI_URL and MUSIC_SERVER_UI_SAMPLES');
  const sample = path.join(sampleRoot, '[1]\u9ed8\u8ba4\u5217\u8868', '\u7231\u6b87');
  const files = ['.mp3', '.lrc', '.jpg'].map(extension => sample + extension);
  files.forEach(file => assert(fs.existsSync(file), `Missing sample ${file}`));
  const browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE });
  try {
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
    const errors = [];
    page.on('pageerror', error => errors.push(String(error)));
    const headers = await login(page, base);
    await page.goto(base);


    await page.waitForFunction(() => state.revision !== null);
    assert.equal(await page.evaluate(() => state.tracks.length), 0, 'Validation library must be empty');
    await page.locator('[data-page="upload"]').click();
    await page.locator('#uploadInput').setInputFiles(files);
    await page.waitForFunction(() => state.uploads.length === 1);
    assert.equal(await page.evaluate(() => Boolean(state.uploads[0].lyricsFile && state.uploads[0].coverFile)), true);
    await page.locator('#uploadAll').click();
    await page.waitForFunction(() => state.uploads[0]?.status === '\u5f85\u786e\u8ba4');
    const preview = await page.evaluate(() => state.uploads[0].preview);
    assert.equal(preview.track.lyrics_source, 'sidecar');
    assert.equal(preview.track.cover_source, 'embedded', 'An automatic external attachment must not replace the real embedded cover');
    assert.equal(await page.evaluate(() => state.tracks.length), 0, 'Preview must not import a track');
    await page.waitForFunction(() => [...document.querySelectorAll('.upload-cover img')].some(img => img.complete && img.naturalWidth > 0));
    await page.locator('[data-upload-field="title"]').fill('UI Regression Title');
    await page.locator('#confirmUploads').click();
    await page.waitForFunction(() => state.tracks.some(track => track.title === 'UI Regression Title'));
    await page.locator('[data-page="tracks"]').click();
    await page.waitForFunction(() => [...document.querySelectorAll('#trackRows img')].some(img => img.complete && img.naturalWidth > 0));

    const scan = await page.request.post(base + '/api/v2/admin/scan', { headers });
    assert.equal(scan.status(), 202);
    await page.waitForFunction(async () => (await api('admin/scan/status')).state !== 'running');
    const stored = (await (await page.request.get(base + '/api/v2/tracks', { headers })).json())[0];
    assert.equal(stored.title, 'UI Regression Title');
    assert.equal(stored.lyrics_source, 'sidecar');
    assert.equal(stored.cover_source, 'embedded');

    await page.locator('#trackRows tr').click({ button: 'right' });
    await page.locator('#contextMenu button').filter({ hasText: '\u66f4\u6362\u5c01\u9762\u6587\u4ef6' }).click();
    await page.locator('#modalBody input[type="file"]').setInputFiles(files[2]);
    await page.locator('#saveModal').click();
    await page.waitForFunction(() => state.tracks[0]?.cover_source === 'manual');
    const manualScan = await page.request.post(base + '/api/v2/admin/scan', { headers });
    assert.equal(manualScan.status(), 202);
    await page.waitForFunction(async () => (await api('admin/scan/status')).state !== 'running');
    const manualTrack = (await (await page.request.get(base + '/api/v2/tracks', { headers })).json())[0];
    assert.equal(manualTrack.cover_source, 'manual');

    await page.locator('[data-page="upload"]').click();
    await page.locator('#uploadInput').setInputFiles(files);
    // Remove the completed queue row before adding the same audio a second time.
    await page.locator('#clearUploads').click();
    await page.waitForFunction(() => state.uploads.length === 0);
    await page.locator('#uploadInput').setInputFiles(files);
    await page.locator('#uploadAll').click();
    await page.waitForFunction(() => Boolean(state.uploads[0]?.preview?.duplicate_of));
    await page.locator('#confirmUploads').click();
    await page.waitForFunction(() => state.uploads[0]?.status === '\u5df2\u53bb\u91cd');
    assert.equal(await page.evaluate(() => state.tracks.length), 1);

    await page.locator('[data-page="playlists"]').click();
    for (const name of ['UI Playlist A', 'UI Playlist B']) {
      await page.locator('#newPlaylist').click();
      await page.locator('[data-field="name"]').fill(name);
      await page.locator('#saveModal').click();
      await page.waitForFunction(name => state.playlists.some(playlist => playlist.name === name), name);
    }
    const playlistIDs = await page.evaluate(() => state.playlists.filter(playlist => playlist.id !== 'library').map(playlist => playlist.id));
    await page.locator(`.playlist-card[data-id="${playlistIDs[1]}"]`).dragTo(page.locator(`.playlist-card[data-id="${playlistIDs[0]}"]`));
    await page.waitForFunction(id => state.playlists.find(playlist => playlist.id !== 'library')?.id === id, playlistIDs[1]);
    await page.waitForFunction(() => !requestActive && requestQueue.length === 0);
    const ordered = await (await page.request.get(base + '/api/v2/playlists', { headers })).json();
    assert.equal(ordered.find(playlist => playlist.id !== 'library').id, playlistIDs[1]);
    await page.locator('[data-page="tracks"]').click();
    await page.locator('#trackRows tr').click({ button: 'right' });
    await page.locator('#contextMenu button').filter({ hasText: '\u6dfb\u52a0\u81f3' }).hover();
    await page.locator('#contextSubmenu button').filter({ hasText: 'UI Playlist A' }).click();
    await page.waitForFunction(() => state.playlists.find(playlist => playlist.name === 'UI Playlist A')?.track_ids.length === 1);
    assert.equal(await page.evaluate(() => state.playlists.find(playlist => playlist.id !== 'library').name), 'UI Playlist B', 'Updating playlist membership must retain its saved order');

    for (const viewport of [{ width: 1440, height: 1000 }, { width: 390, height: 844 }]) {
      await page.setViewportSize(viewport);
      for (const section of ['upload', 'settings', 'stats', 'keys']) {
        await page.locator(`[data-page="${section}"]`).click();
        if (section === 'settings') await page.waitForSelector('[data-setting="media_root"]');
        if (section === 'stats') await page.waitForFunction(() => state.stats !== null);
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), true, `${section} overflows ${viewport.width}px`);
        await page.screenshot({ path: path.join(process.env.MUSIC_SERVER_UI_SCREENSHOTS || process.cwd(), `v06-${section}-${viewport.width}.png`), fullPage: true });
      }
      await page.locator('#newKey').click();
      await page.waitForSelector('#editor[open]');
      assert.equal(await page.locator('[data-scope-check]').count(), 2);
      assert.equal(await page.locator('#policyConcurrency').inputValue(), '1');
      await page.screenshot({ path: path.join(process.env.MUSIC_SERVER_UI_SCREENSHOTS || process.cwd(), `v06-key-dialog-${viewport.width}.png`), fullPage: true });
      await page.locator('#cancelModal').click();
    }

    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.locator('[data-page="settings"]').click();
    await page.waitForSelector('[data-setting="media_root"]');
    assert.equal(await page.locator('[data-setting="media_root"]').evaluate(input => input.readOnly), true);
    await page.locator('[data-setting="scan_external_lyrics"]').uncheck();
    await page.locator('[data-setting="scan_external_covers"]').uncheck();
    await page.locator('[data-setting="max_upload_bytes"]').fill('64');
    await page.locator('#saveSettings').click();
    await page.waitForFunction(() => state.settings.max_upload_bytes === 64 * 1048576 && !state.settings.scan_external_lyrics && !state.settings.scan_external_covers);
    const settings = await (await page.request.get(base + '/api/v2/admin/settings', { headers })).json();
    assert.equal(settings.scan_external_lyrics, false);
    assert.equal(settings.scan_external_covers, false);

    // A player key with restricted permissions must also be usable.
    await page.locator('[data-page="keys"]').click();
    await page.locator('#newKey').click();
    await page.locator('#policyName').fill('UI Single Request Player');
    await page.locator('#policyConcurrency').fill('1');
    assert.equal(await page.locator('[data-scope-check]:checked').count(), 2);
    await page.locator('#saveModal').click();
    await page.waitForSelector('#secretDialog[open]');
    const restrictedAdmin = await page.locator('#secretValue').textContent();
    await page.locator('#doneSecret').click();
    await page.waitForFunction(() => !requestActive && requestQueue.length === 0);
    const failures = [];
    page.on('response', response => { if (response.status() === 429) failures.push(response.url()); });
    assert.equal((await page.request.get(base + '/api/v2/admin/settings', { headers: { 'X-API-Key': restrictedAdmin } })).status(), 403);

    await page.waitForFunction(() => !requestActive && requestQueue.length === 0 && document.getElementById('status').classList.contains('ok'));
    for (let pass = 0; pass < 3; pass++) {
      await page.locator('[data-page="tracks"]').click();
      await page.locator('[data-page="settings"]').click();
      await page.locator('[data-page="stats"]').click();
      await page.locator('[data-page="overview"]').click();
      await page.locator('#refreshOverview').click();
      await page.waitForFunction(() => !requestActive && requestQueue.length === 0);
    }
    assert.deepEqual(failures, [], 'The management queue must complete queued requests');
    assert.deepEqual(errors, [], 'Management page must not throw JavaScript errors');
    console.log('PASS: resource matching, staged preview/confirm, cover rendering, rescan, dedup, playlist drag/submenu, settings persistence, scopes/key UI, concurrency=1, desktop/mobile');
  } finally {
    await browser.close();
  }
}

main().catch(error => { console.error(error); process.exitCode = 1; });
