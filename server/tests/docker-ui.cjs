const assert = require('node:assert/strict');
const fs = require('node:fs');
const {chromium} = require(process.env.MUSIC_SERVER_PLAYWRIGHT || 'playwright');

async function main() {
  const {base, password} = JSON.parse(fs.readFileSync(0, 'utf8'));
  const browser = await chromium.launch({headless: true, ...(process.env.MUSIC_SERVER_BROWSER ? {executablePath: process.env.MUSIC_SERVER_BROWSER} : {})});
  try {
    const page = await browser.newPage();
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    await page.goto(base);
    await page.fill('#username', 'admin');
    await page.fill('#password', password);
    await page.click('#loginButton');
    await page.waitForFunction(() => state.user?.username === 'admin');
    await page.goto(base + '/#/admin/settings/update');
    await page.waitForFunction(() => document.querySelector('#updateStatus').textContent.includes('Docker 管理'));
    assert.equal(await page.locator('#checkUpdate').isVisible(), false);
    assert.equal(await page.locator('#installUpdate').isVisible(), false);
    assert.match(await page.locator('#updateNotes').textContent(), /docker compose pull pikmelo/);
    await page.reload();
    await page.waitForFunction(() => document.querySelector('#updateStatus').textContent.includes('Docker 管理'));
    assert.deepEqual(errors, []);
    console.log('PASS actual container web page shows Docker commands and external update controls');
  } finally {
    await browser.close();
  }
}

main().catch(error => {console.error(error); process.exitCode = 1;});
