// Isolated personal-center acceptance. Never connects to an existing server.
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const net = require('node:net');
const {spawn} = require('node:child_process');
const {chromium} = require(process.env.MUSIC_SERVER_PLAYWRIGHT || 'playwright');

async function login(page, base, username, password, route = '#/account/profile') {
  await page.goto(base + '/' + route);
  await page.reload(); // A fragment-only navigation does not revalidate a revoked cookie.
  await page.fill('#username', username);
  await page.fill('#password', password);
  await page.click('#loginButton');
  await page.waitForFunction(() => document.querySelector('#status').textContent === '已登录');
}

async function main() {
  const binary = process.env.MUSIC_SERVER_TEST_BINARY;
  assert.ok(binary, 'Set MUSIC_SERVER_TEST_BINARY');
  const directory = await fs.mkdtemp(path.join(path.dirname(binary), 'personal-center-'));
  const media = path.join(directory, 'media'); await fs.mkdir(media);
  const listener = net.createServer(); await new Promise(r => listener.listen(0, '127.0.0.1', r));
  const port = listener.address().port; await new Promise(r => listener.close(r));
  const base = 'http://127.0.0.1:' + port;
  const server = spawn(binary, ['-addr', '127.0.0.1:' + port, '-data', path.join(directory, 'library.sqlite'), '-media-root', media, '-no-scan'], {windowsHide:true});
  let serverLog=''; server.stderr.on('data', data => serverLog += data);
  let browser;
  const password = 'personal center admin password';
  const errors=[];
  try {
    let ready=false;
    for(let i=0;i<100;i++) {
      try { if((await fetch(base+'/api/v2/health')).ok) {ready=true;break;} } catch {}
      await new Promise(r=>setTimeout(r,100));
    }
    assert.ok(ready, serverLog);
    const token=(await fs.readFile(path.join(directory,'setup-token'),'utf8')).trim();
    const setup=await fetch(base+'/api/v2/setup',{method:'POST',headers:{Origin:base,'Content-Type':'application/json'},body:JSON.stringify({token,username:'admin',password,confirm_password:password})});
    assert.equal(setup.status,201);
    browser=await chromium.launch({headless:true,...(process.env.MUSIC_SERVER_BROWSER?{executablePath:process.env.MUSIC_SERVER_BROWSER}:{})});
    const context=await browser.newContext({viewport:{width:1365,height:1000}});
    const page=await context.newPage();page.setDefaultTimeout(10000);page.on('pageerror',e=>errors.push(e.message));
    await require('./bootstrap-login-ui.cjs')(page,base);
    if(process.env.MUSIC_SERVER_TEST_BOOTSTRAP_ONLY==='1')return;
    await login(page,base,'admin',password);
    await page.locator('#profileName').waitFor();
    assert.equal(await page.locator('#page-account h1').count(),1);
    assert.equal(await page.inputValue('#profileUsername'),'admin');
    assert.equal(await page.locator('#profileUsername').getAttribute('readonly'),'');
    await page.fill('#profileName','音乐朋友 🎵');await page.click('#saveProfile');
    await page.waitForFunction(()=>state.user.display_name==='音乐朋友 🎵');
    assert.equal(await page.locator('#accountName').textContent(),'音乐朋友 🎵');
    await page.locator('#notifications .ok').filter({hasText:'昵称已保存'}).waitFor();
    await page.locator('#notifications .ok').filter({hasText:'昵称已保存'}).waitFor({state:'detached'});
    await page.reload();await page.locator('#profileName').waitFor();
    assert.equal(await page.inputValue('#profileName'),'音乐朋友 🎵');
    await page.fill('#profileName','未保存的输入');page.once('dialog',d=>d.dismiss());
    await page.click('[data-page="tracks"]');await page.waitForTimeout(100);
    assert.equal(await page.evaluate(()=>location.hash),'#/account/profile');
    assert.equal(await page.inputValue('#profileName'),'未保存的输入');
    page.once('dialog',d=>d.accept());await page.click('[data-page="tracks"]');
    await page.waitForFunction(()=>state.page==='tracks');
    await page.locator('#accountMenu summary').click();await page.click('#personalCenter');await page.locator('#profileName').waitFor();
    await page.click('#accountBack');await page.waitForFunction(()=>location.hash==='#/library');
    await page.goto(base+'/#/playlists/not-accessible');await page.locator('#routeError').waitFor({state:'visible'});
    await page.locator('#accountMenu summary').click();await page.click('#personalCenter');await page.locator('#profileName').waitFor();
    await page.click('#accountBack');
    assert.equal(await page.evaluate(()=>location.hash),'#/library','Return should use the previous valid page, not an unavailable detail');
    await page.goto(base+'/#/account/profile');await page.locator('#profileName').waitFor();
    // A concurrent edit must not silently discard the unsaved form.
    const current=await page.evaluate(()=>state.user);
    await page.evaluate(async user=>api('auth/me','PATCH',{display_name:'并发更新'},undefined,user.version),current);
    await page.fill('#profileName','过期表单');await page.click('#saveProfile');
    await page.waitForFunction(()=>document.querySelector('#profileError').textContent.includes('409'));
    assert.equal(await page.inputValue('#profileName'),'过期表单');
    page.once('dialog',d=>d.accept());await page.click('#reloadProfile');
    await page.waitForFunction(()=>document.querySelector('#profileName').value==='并发更新');
    await page.screenshot({path:path.join(directory,'profile-desktop.png'),fullPage:true});

    // Typing and saving during a slow identity read must preserve the draft.
    await page.goto(base+'/#/library');
    let releaseIdentity,identityFetched;
    const release=new Promise(resolve=>releaseIdentity=resolve),fetched=new Promise(resolve=>identityFetched=resolve);
    await page.route('**/api/v2/auth/me',async route=>{
      if(route.request().method()!=='GET')return route.continue();
      const response=await route.fetch();identityFetched();await release;await route.fulfill({response});
    });
    await page.goto(base+'/#/account/profile');await fetched;
    await page.fill('#profileName','响应顺序测试');await page.click('#saveProfile');
    await page.waitForFunction(()=>personalBusy&&requestQueue.length>0);
    assert.equal(await page.inputValue('#profileName'),'响应顺序测试');
    const delayed=page.waitForResponse(r=>r.url().endsWith('/auth/me')&&r.request().method()==='GET');
    releaseIdentity();await delayed;await page.waitForFunction(()=>state.user.display_name==='响应顺序测试'&&!personalBusy);await page.unroute('**/api/v2/auth/me');
    assert.equal(await page.inputValue('#profileName'),'响应顺序测试');
    assert.equal(await page.locator('#accountName').textContent(),'响应顺序测试');
    await page.fill('#profileName','并发更新');await page.click('#saveProfile');await page.waitForFunction(()=>state.user.display_name==='并发更新');

    await page.goto(base+'/#/admin/users');await page.locator('#createUser').click();
    await page.fill('#editor-field-username','Profile-Member');await page.check('[name="createQuotaMode"][value="unlimited"]');await page.click('#saveModal');
    await page.locator('#temporaryPasswordDialog').waitFor({state:'visible'});
    assert.equal(await page.locator('#temporaryPasswordAccount').textContent(),'账号：Profile-Member');
    const created={user:await page.evaluate(()=>state.users.find(user=>user.username==='Profile-Member')),temporary_password:await page.locator('#temporaryPasswordValue').textContent()};
    assert.ok(created.user);await page.click('#closeTemporaryPassword');
    await page.goto(base+'/#/account/profile');await page.locator('#profileName').waitFor();
    const memberContext=await browser.newContext();const member=await memberContext.newPage();member.setDefaultTimeout(10000);member.on('pageerror',e=>errors.push(e.message));
    await member.goto(base+'/#/account/profile');await member.fill('#username','profile-member');await member.fill('#password',created.temporary_password);await member.click('#loginButton');
    await member.locator('#firstPasswordScreen').waitFor({state:'visible'});
    assert.equal(await member.locator('.app').isVisible(),false);
    const memberPassword='personal center member password';
    await member.fill('#firstOldPassword',created.temporary_password);await member.fill('#firstNewPassword',memberPassword);await member.fill('#firstConfirmPassword',memberPassword);await member.click('#saveFirstPassword');
    await member.locator('#loginScreen').waitFor({state:'visible'});await login(member,base,'profile-member',memberPassword);
    await member.locator('#profileName').waitFor();assert.equal(await member.locator('[data-page="users"]').isVisible(),false);
    await member.fill('#profileName','并发更新');await member.click('#saveProfile');await member.waitForFunction(()=>state.user.display_name==='并发更新');

    // Duplicate nicknames must still identify the exact invitee and author.
    const shared=await page.evaluate(async username=>{
      const p=await api('playlists','POST',{id:browserUUID(),name:'Nickname sharing fixture'});
      const source=await api('playlists/'+p.id+'/visibility','PUT',{visibility:'shared'},undefined,p.version);
      await api('playlists/'+p.id+'/invitations','POST',{username},undefined,source.version);
      await refreshCore();return state.playlists.find(item=>item.id===p.id);
    },created.user.username);
    await page.evaluate(p=>openSharingSettings(p),shared);
    await page.locator('#ownerInvitations').filter({hasText:'并发更新 (@Profile-Member)'}).waitFor();await page.click('#closeSharing');
    await member.goto(base+'/#/invitations');
    await member.locator('#receivedInvitations').filter({hasText:'并发更新 (@admin)'}).waitFor();
    await page.goto(base+'/#/admin/users');
    const memberRow=page.locator('[data-user-id="'+created.user.id+'"]');await memberRow.waitFor();
    assert.equal(await memberRow.locator('td').first().locator('strong').textContent(),'并发更新');
    assert.match(await memberRow.textContent(),/@Profile-Member/);
    await page.goto(base+'/#/account/profile');await page.locator('#profileName').waitFor();

    const secondContext=await browser.newContext();const second=await secondContext.newPage();await login(second,base,'admin',password);
    const key=await page.evaluate(()=>api('auth/keys','POST',{name:'profile-fixture'}));
    await page.locator('#accountMenu summary').click();await page.click('#manageSessions');await page.locator('.session-card[data-current="true"]').waitFor();
    assert.equal(await page.locator('.session-card').first().getAttribute('data-current'),'true');
    page.once('dialog',d=>d.accept());await page.click('#revokeOtherSessions');
    await page.waitForFunction(()=>document.querySelectorAll('.session-card').length===1);
    assert.equal(await second.evaluate(async()=> (await fetch('/api/v2/auth/me')).status),401);
    assert.equal(await member.evaluate(async()=> (await fetch('/api/v2/auth/me')).status),200);
    assert.equal((await fetch(base+'/api/v2/playlists',{headers:{'X-API-Key':key.secret}})).status,200);
    await page.screenshot({path:path.join(directory,'sessions-desktop.png'),fullPage:true});
    // Single-login revocation supports cancellation and preserves this browser.
    await login(second,base,'admin',password);await page.click('#refreshAccountSessions');
    const otherLogin=page.locator('.session-card[data-current="false"] [data-revoke-session]');await otherLogin.waitFor();
    page.once('dialog',d=>d.dismiss());await otherLogin.click();assert.equal(await otherLogin.count(),1);
    page.once('dialog',d=>d.accept());await otherLogin.click();
    await page.waitForFunction(()=>document.querySelectorAll('.session-card').length===1);
    assert.equal(await second.evaluate(async()=> (await fetch('/api/v2/auth/me')).status),401);
    await login(second,base,'admin',password);
    await page.locator('#accountMenu summary').click();await page.click('#changePassword');
    await page.fill('#accountOldPassword',password);await page.fill('#accountNewPassword','new personal center password');await page.fill('#accountConfirmPassword','different confirmation');await page.click('#saveAccountPassword');
    await page.locator('#accountPasswordError').filter({hasText:'不一致'}).waitFor();
    await page.fill('#accountConfirmPassword','new personal center password');await page.fill('#accountOldPassword','incorrect password');await page.click('#saveAccountPassword');
    await page.waitForFunction(()=>document.querySelector('#accountPasswordError').textContent.includes('HTTP 400'));
    assert.equal(await page.inputValue('#accountNewPassword'),'new personal center password');
    await page.click('#toggleAccountPassword');assert.equal(await page.locator('#accountNewPassword').getAttribute('type'),'text');
    await page.fill('#accountOldPassword',password);await page.click('#saveAccountPassword');await page.locator('#loginScreen').waitFor({state:'visible'});
    assert.match(await page.locator('#loginError').textContent(),/密码已修改/);
    assert.equal(await page.inputValue('#accountNewPassword'),'');
    assert.equal(await second.evaluate(async()=> (await fetch('/api/v2/auth/me')).status),401);
    assert.equal((await fetch(base+'/api/v2/playlists',{headers:{'X-API-Key':key.secret}})).status,200);
    await login(page,base,'admin','new personal center password');await page.locator('#profileName').waitFor();
    assert.equal(await page.inputValue('#profileName'),'并发更新');

    await member.setViewportSize({width:375,height:812});await member.goto(base+'/#/account/security');await member.reload();await member.locator('#accountOldPassword').waitFor({state:'visible'});
    await member.waitForFunction(()=>parseFloat(getComputedStyle(document.querySelector('.main')).marginLeft)===0);
    assert.equal(await member.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
    await member.locator('#notifications .ok').waitFor({state:'detached'});
    await member.screenshot({path:path.join(directory,'account-mobile.png'),fullPage:true,animations:'disabled'});
    await member.goto(base+'/#/account/sessions');await member.locator('.session-card[data-current="true"]').waitFor();
    member.once('dialog',d=>d.accept());await member.locator('.session-card[data-current="true"] button').click();
    await member.locator('#loginScreen').waitFor({state:'visible'});
    assert.equal(await member.inputValue('#profileName'),'');
    assert.equal(await member.evaluate(async()=> (await fetch('/api/v2/auth/me')).status),401);
    assert.deepEqual(errors,[]);
    console.log('PASS personal center: nickname/persistence/conflict, dirty navigation, first-password gate, current/other sessions, password logout with retained keys, mobile layout');
    console.log('Artifacts: '+directory);
  } finally {
    if(browser)await browser.close();server.kill();
    if(server.exitCode===null)await new Promise(r=>server.once('exit',r));
    await fs.writeFile(path.join(directory,'server.log'),serverLog);
  }
}
main().catch(error=>{console.error(error);process.exitCode=1});
