const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');

// This script imports a sample into an isolated empty validation server.
async function main() {
  const base = process.env.MUSIC_SERVER_UI_URL;
  const key = process.env.MUSIC_SERVER_UI_KEY;
  const root = process.env.MUSIC_SERVER_UI_SAMPLES;
  const screenshots = process.env.MUSIC_SERVER_UI_SCREENSHOTS;
  assert(base && key && root && screenshots, 'Set the validation URL, key, sample root and screenshot directory');
  const mp3 = path.join(root, '暂存', '麻雀 - 李荣浩.mp3');
  const song = path.join(root, '[1]默认列表', '爱殇');
  const withAttachments = ['.mp3', '.lrc', '.jpg'].map(extension => song + extension);
  const flac = path.join(root, '[1]默认列表', '她是什么样的 - 傅梦彤、陈麒名.flac');
  [mp3, flac, ...withAttachments].forEach(file => assert(fs.existsSync(file), `Missing sample: ${file}`));
  const browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE });
  try {
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
    const errors = [], stageRequests = [], uploadRequests = [];
    page.on('pageerror', error => errors.push(String(error)));
    page.on('request', request => {
      const pathname = new URL(request.url()).pathname;
      if (['POST', 'PUT', 'PATCH'].includes(request.method()) && pathname.startsWith('/api/v1/uploads')) uploadRequests.push(request.url());
      if (request.method() === 'POST' && /\/uploads\/stage$/.test(pathname)) stageRequests.push(request.url());
    });
    const headers = { 'X-API-Key': key };
    const tracks = await page.request.get(base + '/api/v1/tracks', { headers });
    assert.equal(tracks.status(), 200);
    assert.deepEqual(await tracks.json(), [], 'Validation library must be empty');
    await page.goto(base);
    await page.locator('[data-page="upload"]').click();
    await page.locator('#uploadInput').setInputFiles(mp3);
    await page.waitForFunction(() => state.uploads[0]?.localStatus === '已完成');
    const local = await page.evaluate(() => state.uploads[0].localPreview.track);
    assert.equal(local.title, '麻雀');
    assert.equal(local.artist, '李荣浩');
    assert.equal(local.embedded_lyrics, true);
    assert(local.lyrics.length > 100);
    assert.equal(uploadRequests.length, 0, 'Local preview must work before authentication without uploads');
    await page.locator('.upload-text summary').click();
    assert((await page.locator('[data-upload-field="lyrics"]').inputValue()).length > 100);
    await page.locator('[data-upload-field="comment"]').fill('Local preview comment');
    await page.locator('#clearUploads').click();
    await page.waitForFunction(() => state.uploads.length === 0);

    // Exercise the actual drop handler using a browser File and DataTransfer.
    const data = fs.readFileSync(mp3).toString('base64');
    await page.evaluate(({ data, name }) => {
      const bytes = Uint8Array.from(atob(data), value => value.charCodeAt(0));
      const transfer = new DataTransfer();
      transfer.items.add(new File([bytes], name, { type: 'audio/mpeg' }));
      document.getElementById('page-upload').dispatchEvent(new DragEvent('drop', { bubbles: true, dataTransfer: transfer }));
    }, { data, name: path.basename(mp3) });
    await page.waitForFunction(() => state.uploads[0]?.localStatus === '已完成');
    assert.equal(await page.locator('[data-upload-field="artist"]').inputValue(), '李荣浩');
    assert.equal(uploadRequests.length, 0, 'Dropping an audio file must not upload it');
    await page.locator('#clearUploads').click();
    await page.waitForFunction(() => state.uploads.length === 0);

    await page.locator('#uploadInput').setInputFiles([...withAttachments, flac]);
    await page.waitForFunction(() => state.uploads.length === 2 && state.uploads.every(upload => upload.localStatus === '已完成'));
    const attachment = await page.evaluate(() => state.uploads[0].localPreview.track);
    assert.equal(attachment.lyrics_source, 'sidecar');
    assert.equal(attachment.embedded_lyrics, false);
    assert.equal(attachment.cover_source, 'embedded');
    const pending = await page.evaluate(() => state.uploads[1].localPreview.track);
    assert.equal(pending.tags_confirmed, false);
    assert(await page.locator('.upload-item').nth(1).innerText().then(value => value.includes('等待服务端确认') && value.includes('内嵌歌词待确认')));
    await page.waitForFunction(() => [...document.querySelectorAll('.upload-cover img')].some(image => image.complete && image.naturalWidth > 0));
    assert.equal(uploadRequests.length, 0);
    const count = await page.request.get(base + '/api/v1/tracks', { headers });
    assert.deepEqual(await count.json(), []);
    const first = page.locator('.upload-item').first();
    await first.locator('.upload-text summary').click();
    for (const viewport of [{ width: 1440, height: 1000 }, { width: 390, height: 844 }]) {
      await page.setViewportSize(viewport);
      await page.waitForFunction(() => getComputedStyle(document.querySelector('.main')).marginLeft === (innerWidth <= 900 ? '72px' : '246px'));
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), true, `Upload page overflows at ${viewport.width}px`);
      await page.screenshot({ path: path.join(screenshots, `v061-local-preview-${viewport.width}.png`), fullPage: true });
    }
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.locator('#key').fill(key);
    await page.locator('#connect').click();
    await page.waitForFunction(() => state.revision !== null && !requestActive && requestQueue.length === 0);
    await first.locator('[data-upload-field="title"]').fill('Local Preview Regression');
    let release;
    const gate = new Promise(resolve => { release = resolve; });
    await page.route('**/api/v1/uploads/stage', async route => {
      await gate;
      await route.continue();
    });
    const hit = page.waitForRequest(request => request.method() === 'POST' && /\/uploads\/stage$/.test(new URL(request.url()).pathname), { timeout: 15000 });
    try {
      await Promise.all([first.locator('[data-stage-upload]').click(), hit]);
      assert.equal(await first.locator('[data-upload-field="lyrics"]').isDisabled(), true);
      assert.equal(await first.locator('[data-upload-field="comment"]').isDisabled(), true);
    } finally {
      release();
    }
    await page.waitForFunction(() => state.uploads[0]?.status === '待确认');
    await page.unroute('**/api/v1/uploads/stage');
    assert.equal(stageRequests.length, 1);
    const preview = await page.evaluate(() => state.uploads[0].preview.track);
    assert.equal(preview.title, 'Local Preview Regression');
    assert.equal(preview.lyrics_source, 'sidecar');
    assert.equal(preview.cover_source, 'embedded');
    assert.equal(await first.locator('.upload-text').evaluate(details => details.open), true);
    await first.locator('[data-upload-field="lyrics"]').fill('[00:00.00]Local editor regression');
    await first.locator('[data-upload-field="comment"]').fill('Preserve manual comment');
    await first.locator('[data-confirm-upload]').click();
    await page.waitForFunction(() => state.uploads[0]?.status === '完成');
    const saved = await page.request.get(base + '/api/v1/tracks', { headers });
    const stored = await saved.json();
    assert.equal(stored.length, 1);
    assert.equal(stored[0].lyrics, '[00:00.00]Local editor regression');
    assert.equal(stored[0].lyrics_source, 'manual');
    assert.equal(stored[0].comment, 'Preserve manual comment');
    assert.equal(Boolean(stored[0].embedded_lyrics), false);
    assert.equal(stored[0].cover_source, 'embedded');
    assert.equal(await first.locator('[data-upload-field="lyrics"]').isDisabled(), true);

    const other = page.locator('.upload-item').nth(1);
    await other.locator('[data-stage-upload]').click();
    await page.waitForFunction(() => state.uploads[1]?.status === '待确认');
    assert.equal(await other.locator('[data-upload-field="artist"]').inputValue(), '傅梦彤、陈麒名');
    assert.equal(await page.evaluate(() => state.uploads[1].preview.track.embedded_lyrics), true);
    await page.locator('#clearUploads').click();
    await page.waitForFunction(() => state.uploads.length === 0);
    await page.locator('#uploadInput').setInputFiles(withAttachments);
    await page.waitForFunction(() => state.uploads[0]?.localStatus === '已完成');
    const requestsBeforeDuplicate = stageRequests.length;
    assert.equal(await page.evaluate(() => state.uploads[0].stageId), null);
    await page.locator('[data-stage-upload]').click();
    await page.waitForFunction(() => Boolean(state.uploads[0]?.preview?.duplicate_of));
    assert(stageRequests.length > requestsBeforeDuplicate);
    await page.locator('[data-confirm-upload]').click();
    await page.waitForFunction(() => state.uploads[0]?.status === '已去重');
    const unchanged = await (await page.request.get(base + '/api/v1/tracks', { headers })).json();
    assert.equal(unchanged.length, 1);
    assert.equal(unchanged[0].lyrics, stored[0].lyrics);
    await page.locator('#clearUploads').click();
    await page.waitForFunction(() => state.uploads.length === 0);
    assert.deepEqual(errors, [], 'Browser must not report JavaScript errors');
    console.log('PASS: real MP3 selection/drop, zero automatic uploads, lyrics editor, embedded/external assets, FLAC confirmation, locked fields, commit/dedup and desktop/mobile layout');
  } finally {
    await browser.close();
  }
}

main().catch(error => { console.error(error); process.exitCode = 1; });
