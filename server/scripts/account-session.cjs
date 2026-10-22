const assert = require('node:assert/strict');

async function login(page, base) {
  const username = process.env.MUSIC_SERVER_UI_USERNAME;
  const password = process.env.MUSIC_SERVER_UI_PASSWORD;
  assert(username && password, 'Set MUSIC_SERVER_UI_USERNAME and MUSIC_SERVER_UI_PASSWORD for the isolated server');
  const origin = new URL(base).origin;
  const response = await page.request.post(base + '/api/v2/auth/login', {
    headers: { Origin: origin }, data: { username, password },
  });
  assert.equal(response.status(), 200, 'Isolated account login failed');
  const identity = await response.json();
  return { Origin: origin, 'X-CSRF-Token': identity.csrf_token };
}

module.exports = { login };
