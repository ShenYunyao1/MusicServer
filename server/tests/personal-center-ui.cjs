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
    const openProfile=async p=>{await p.click('#editProfile');try{await p.locator('#profileName').waitFor({state:'visible'})}catch(error){console.error('Profile dialog diagnostic:',await p.evaluate(()=>({text:document.querySelector('#personalEditBody')?.textContent,open:Boolean(document.querySelector('#personalEditDialog')),queued:requestQueue.length,active:requestActive,owner:state.user?.id})),errors);throw error}};
    const closeEdit=async p=>{await p.click('#cancelPersonalEdit');if(await p.locator('#personalDiscard').isVisible())await p.click('#discardPersonalEdit');await p.locator('#personalEditDialog').waitFor({state:'detached'})};
    const saveName=async(p,name)=>{await openProfile(p);await p.fill('#profileName',name);await p.click('#saveProfile');await p.waitForFunction(name=>state.user.display_name===name&&!document.querySelector('#personalEditDialog'),name)};
    await login(page,base,'admin',password);
    await page.locator('#editProfile').waitFor();
    assert.equal(await page.locator('#page-account h1').count(),1);
    assert.equal(await page.locator('#profileUsername').textContent(),'admin');
    assert.equal(await page.locator('#page-account form').count(),0,'Read-only account pages do not retain editable forms');
    let nativeDialogs=0;page.on('dialog',()=>nativeDialogs++);
    for(const tab of ['storage','security','sessions','profile']){await page.click('[data-account-tab="'+tab+'"]');await page.waitForFunction(tab=>location.hash==='#/account/'+tab,tab)}
    assert.equal(nativeDialogs,0,'Normal tab navigation must not prompt for unsaved input');
    await page.screenshot({path:path.join(directory,'personal-profile-readonly.png'),fullPage:true,animations:'disabled'});
    await openProfile(page);await page.fill('#profileName','弹窗资料预览');await page.screenshot({path:path.join(directory,'personal-profile-editor.png'),fullPage:true,animations:'disabled'});await closeEdit(page);
    await saveName(page,'音乐朋友 🎵');
    assert.equal(await page.locator('#accountName').textContent(),'音乐朋友 🎵');
    await page.reload();await page.locator('#editProfile').waitFor();await openProfile(page);
    assert.equal(await page.inputValue('#profileName'),'音乐朋友 🎵');
    await page.fill('#profileName','未保存的输入');await page.click('#cancelPersonalEdit');
    await page.locator('#personalDiscard').waitFor({state:'visible'});await page.click('#continuePersonalEdit');
    assert.equal(await page.inputValue('#profileName'),'未保存的输入');
    await page.click('#cancelPersonalEdit');await page.click('#discardPersonalEdit');
    assert.equal(await page.locator('#profileName').count(),0,'Discard destroys the draft controls');
    await page.click('[data-page="tracks"]');await page.waitForFunction(()=>state.page==='tracks');
    assert.equal(nativeDialogs,0,'Discarded drafts do not leave a native navigation guard');
    await page.locator('#accountMenu summary').click();await page.click('#personalCenter');await page.locator('#editProfile').waitFor();
    await page.click('#accountBack');await page.waitForFunction(()=>location.hash.split('?')[0]==='#/library');
    await page.goto(base+'/#/playlists/not-accessible');await page.locator('#routeError').waitFor({state:'visible'});
    await page.locator('#accountMenu summary').click();await page.click('#personalCenter');await page.locator('#editProfile').waitFor();await page.click('#accountBack');
    assert.equal(await page.evaluate(()=>location.hash.split('?')[0]),'#/library','Return avoids an inaccessible detail');
    await page.goto(base+'/#/account/profile');await openProfile(page);
    const current=await page.evaluate(()=>state.user);
    await page.evaluate(async user=>api('auth/me','PATCH',{display_name:'并发更新'},undefined,user.version),current);
    await page.fill('#profileName','过期表单');await page.click('#saveProfile');
    await page.waitForFunction(()=>/409|变化|冲突/.test(document.querySelector('#profileError').textContent));
    assert.equal(await page.inputValue('#profileName'),'过期表单');
    await closeEdit(page);await openProfile(page);assert.equal(await page.inputValue('#profileName'),'并发更新');
    await closeEdit(page);
    await page.screenshot({path:path.join(directory,'profile-desktop.png'),fullPage:true,animations:'disabled'});

    // A delayed fresh read belongs to the old popup, never to a newly opened editor.
    let releaseIdentity,identityFetched,identityFinished;
    const released=new Promise(resolve=>releaseIdentity=resolve),fetched=new Promise(resolve=>identityFetched=resolve),finished=new Promise(resolve=>identityFinished=resolve);
    await page.route('**/api/v2/auth/me',async route=>{
      if(route.request().method()!=='GET')return route.continue();
      const response=await route.fetch();identityFetched();await released;await route.fulfill({response});identityFinished();
    },{times:1});
    await page.click('#editProfile');await fetched;
    await page.click('#cancelPersonalEdit');await page.locator('#personalEditDialog').waitFor({state:'detached'});
    releaseIdentity();await finished;await page.unroute('**/api/v2/auth/me');
    await openProfile(page);assert.equal(await page.inputValue('#profileName'),'并发更新','A late old read cannot install stale fields');
    await page.fill('#profileName','响应顺序测试');await page.click('#saveProfile');await page.waitForFunction(()=>state.user.display_name==='响应顺序测试'&&!document.querySelector('#personalEditDialog'));
    assert.equal(await page.locator('#accountName').textContent(),'响应顺序测试');await saveName(page,'并发更新');

    await page.goto(base+'/#/admin/users');await page.locator('#createUser').click();
    await page.fill('#editor-field-username','Profile-Member');await page.check('[name="createQuotaMode"][value="unlimited"]');await page.click('#saveModal');
    await page.locator('#temporaryPasswordDialog').waitFor({state:'visible'});
    assert.equal(await page.locator('#temporaryPasswordAccount').textContent(),'账号：Profile-Member');
    const created={user:await page.evaluate(()=>state.users.find(user=>user.username==='Profile-Member')),temporary_password:await page.locator('#temporaryPasswordValue').textContent()};
    assert.ok(created.user);await page.click('#closeTemporaryPassword');
    await page.goto(base+'/#/account/profile');await page.locator('#editProfile').waitFor();
    const memberContext=await browser.newContext();const member=await memberContext.newPage();member.setDefaultTimeout(10000);member.on('pageerror',e=>errors.push(e.message));
    await member.goto(base+'/#/account/profile');await member.fill('#username','profile-member');await member.fill('#password',created.temporary_password);await member.click('#loginButton');
    await member.locator('#firstPasswordScreen').waitFor({state:'visible'});assert.equal(await member.locator('.app').isVisible(),false);
    const memberPassword='personal center member password';
    await member.fill('#firstOldPassword',created.temporary_password);await member.fill('#firstNewPassword',memberPassword);await member.fill('#firstConfirmPassword',memberPassword);await member.click('#saveFirstPassword');
    await member.locator('#loginScreen').waitFor({state:'visible'});await login(member,base,'profile-member',memberPassword);
    await member.locator('#editProfile').waitFor();assert.equal(await member.locator('[data-page="users"]').isVisible(),false);await saveName(member,'并发更新');
    const shared=await page.evaluate(async username=>{
      const p=await api('playlists','POST',{id:browserUUID(),name:'Nickname sharing fixture'});
      const source=await api('playlists/'+p.id+'/visibility','PUT',{visibility:'shared'},undefined,p.version);
      await api('playlists/'+p.id+'/invitations','POST',{username},undefined,source.version);
      await refreshCore();return state.playlists.find(item=>item.id===p.id);
    },created.user.username);
    await page.evaluate(p=>openSharingSettings(p),shared);
    await page.locator('#ownerInvitations').filter({hasText:'并发更新 (@Profile-Member)'}).waitFor();await page.click('#closeSharing');
    await member.goto(base+'/#/invitations');await member.locator('#receivedInvitations').filter({hasText:'并发更新 (@admin)'}).waitFor();
    await page.goto(base+'/#/admin/users');const memberRow=page.locator('[data-user-id="'+created.user.id+'"]');await memberRow.waitFor();
    assert.equal(await memberRow.locator('td').first().locator('strong').textContent(),'并发更新');assert.match(await memberRow.textContent(),/@Profile-Member/);
    await page.goto(base+'/#/account/profile');await page.locator('#editProfile').waitFor();
    const secondContext=await browser.newContext();const second=await secondContext.newPage();await login(second,base,'admin',password);
    const key=await page.evaluate(()=>api('auth/keys','POST',{name:'profile-fixture'}));
    await page.locator('#accountMenu summary').click();await page.click('#manageSessions');await page.locator('.session-card[data-current="true"]').waitFor();
    assert.equal(await page.locator('.session-card').first().getAttribute('data-current'),'true');
    page.once('dialog',d=>d.accept());await page.click('#revokeOtherSessions');await page.waitForFunction(()=>document.querySelectorAll('.session-card').length===1);
    assert.equal(await second.evaluate(async()=> (await fetch('/api/v2/auth/me')).status),401);
    assert.equal(await member.evaluate(async()=> (await fetch('/api/v2/auth/me')).status),200);
    assert.equal((await fetch(base+'/api/v2/playlists',{headers:{'X-API-Key':key.secret}})).status,200);
    await page.screenshot({path:path.join(directory,'sessions-desktop.png'),fullPage:true});
    await login(second,base,'admin',password);await page.click('#refreshAccountSessions');
    const otherLogin=page.locator('.session-card[data-current="false"] [data-revoke-session]');await otherLogin.waitFor();
    page.once('dialog',d=>d.dismiss());await otherLogin.click();assert.equal(await otherLogin.count(),1);
    page.once('dialog',d=>d.accept());await otherLogin.click();await page.waitForFunction(()=>document.querySelectorAll('.session-card').length===1);
    assert.equal(await second.evaluate(async()=> (await fetch('/api/v2/auth/me')).status),401);await login(second,base,'admin',password);
    await page.locator('#accountMenu summary').click();await page.click('#changePassword');await page.locator('#accountOldPassword').waitFor();
    await page.fill('#accountOldPassword',password);await page.fill('#accountNewPassword','new personal center password');await page.fill('#accountConfirmPassword','different confirmation');await page.click('#saveAccountPassword');
    await page.locator('#accountPasswordError').filter({hasText:'不一致'}).waitFor();
    await page.fill('#accountConfirmPassword','new personal center password');await page.fill('#accountOldPassword','incorrect password');await page.click('#saveAccountPassword');
    await page.waitForFunction(()=>document.querySelector('#accountPasswordError').textContent.includes('HTTP 400'));
    assert.equal(await page.inputValue('#accountNewPassword'),'new personal center password');await page.click('#toggleAccountPassword');assert.equal(await page.locator('#accountNewPassword').getAttribute('type'),'text');
    await page.fill('#accountOldPassword',password);await page.click('#saveAccountPassword');await page.locator('#loginScreen').waitFor({state:'visible'});
    assert.match(await page.locator('#loginError').textContent(),/密码已修改/);assert.equal(await page.locator('#accountNewPassword').count(),0);
    assert.equal(await second.evaluate(async()=> (await fetch('/api/v2/auth/me')).status),401);
    assert.equal((await fetch(base+'/api/v2/playlists',{headers:{'X-API-Key':key.secret}})).status,200);
    await login(page,base,'admin','new personal center password');await openProfile(page);assert.equal(await page.inputValue('#profileName'),'并发更新');await closeEdit(page);
    await member.setViewportSize({width:375,height:812});await member.goto(base+'/#/account/security');await member.reload();await member.locator('#editPassword').click();await member.locator('#accountOldPassword').waitFor({state:'visible'});
    await member.waitForFunction(()=>parseFloat(getComputedStyle(document.querySelector('.main')).marginLeft)===0);
    assert.equal(await member.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
    await member.screenshot({path:path.join(directory,'account-mobile.png'),fullPage:true,animations:'disabled'});await closeEdit(member);
    await member.screenshot({path:path.join(directory,'personal-security-mobile.png'),fullPage:true,animations:'disabled'});
    await member.goto(base+'/#/account/storage');await member.locator('#personalStorageStatus').waitFor({state:'visible'});
    await member.screenshot({path:path.join(directory,'personal-storage-mobile.png'),fullPage:true,animations:'disabled'});
    await member.goto(base+'/#/account/sessions');await member.locator('.session-card[data-current="true"]').waitFor();
    member.once('dialog',d=>d.accept());await member.locator('.session-card[data-current="true"] button').click();await member.locator('#loginScreen').waitFor({state:'visible'});
    assert.equal(await member.locator('#profileName').count(),0);assert.equal(await member.evaluate(async()=> (await fetch('/api/v2/auth/me')).status),401);
    assert.deepEqual(errors,[]);
    console.log('PASS personal center: nickname/persistence/conflict, read-only navigation/modal discard/late reads, first-password gate, current/other sessions, password logout with retained keys, mobile layout');
    console.log('Artifacts: '+directory);
  } finally {
    if(browser)await browser.close();server.kill();
    if(server.exitCode===null)await new Promise(r=>server.once('exit',r));
    await fs.writeFile(path.join(directory,'server.log'),serverLog);
  }
}
main().catch(error=>{console.error(error);process.exitCode=1});
