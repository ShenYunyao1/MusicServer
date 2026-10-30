const assert=require('node:assert/strict');
const path=require('node:path');

module.exports=async function({page,browser,base,directory}){
  const errors=[],onError=e=>errors.push(e.message);page.on('pageerror',onError);
  // A select's intrinsic width includes its longest option, even when admin is
  // selected. Exercise the actual long-display-name layout on every platform.
  await page.evaluate(()=>api('admin/users','POST',{username:'mobile-transfer-target',quota_bytes:0,email_verification_required:false,display_name:'Long target '.repeat(9).trim()}));
  const account=await page.evaluate(()=>api('admin/users','POST',{username:'Delete.User',quota_bytes:0,email_verification_required:false}));
  const context=await browser.newContext({viewport:{width:1440,height:1000}});
  try{
    const member=await context.newPage();member.on('pageerror',onError);member.setDefaultTimeout(10000);
    const password='account deletion member password';
    await member.goto(base);await member.fill('#username','delete.user');await member.fill('#password',account.temporary_password);await member.click('#loginButton');
    await member.locator('#firstPasswordScreen').waitFor({state:'visible'});
    await member.fill('#firstOldPassword',account.temporary_password);await member.fill('#firstNewPassword',password);await member.fill('#firstConfirmPassword',password);await member.click('#saveFirstPassword');
    await member.locator('#loginScreen').waitFor({state:'visible'});await member.fill('#username','DELETE.USER');await member.fill('#password',password);await member.click('#loginButton');
    await member.waitForFunction(()=>document.querySelector('#status').textContent==='已登录');
    const resources=await member.evaluate(async()=>({playlist:await api('playlists','POST',{id:browserUUID(),name:'Retain after account deletion',track_ids:[]}),key:await api('auth/keys','POST',{name:'Member retained while disabled',allowed_playlist_ids:['library']})}));
    const keyStatus=async()=> (await fetch(base+'/api/v2/playlists',{headers:{'X-API-Key':resources.key.secret}})).status;
    assert.equal(await keyStatus(),200);

    await page.goto(base+'/#/player/keys');await page.locator('#revokeAllKeys').waitFor();
    const first=await page.evaluate(async()=>{const key=await api('auth/keys','POST',{name:'Delete this Key'});await loadKeys();return key});
    const keyRow=page.locator('#keyRows tr').filter({hasText:first.id});
    assert.equal(await keyRow.locator('[data-revoke-key]').textContent(),'删除');
    page.once('dialog',d=>d.dismiss());await keyRow.locator('[data-revoke-key]').click();assert.equal(await keyRow.count(),1);
    page.once('dialog',d=>d.accept());await keyRow.locator('[data-revoke-key]').click();await keyRow.waitFor({state:'detached'});
    assert.equal((await fetch(base+'/api/v2/playlists',{headers:{'X-API-Key':first.secret}})).status,401);
    const bulk=await page.evaluate(async()=>{const keys=await Promise.all([api('auth/keys','POST',{name:'Bulk one'}),api('auth/keys','POST',{name:'Bulk two'})]);await loadKeys();return keys});
    assert.equal(await page.locator('#revokeAllKeys').textContent(),'删除我的全部 Key');
    page.once('dialog',d=>d.accept());await page.click('#revokeAllKeys');await page.waitForFunction(()=>state.keys.length===0);
    for(const key of bulk)assert.equal((await fetch(base+'/api/v2/playlists',{headers:{'X-API-Key':key.secret}})).status,401);
    assert.equal(await keyStatus(),200,'Deleting admin keys must preserve another account key');

    await page.goto(base+'/#/admin/users');const row=page.locator('[data-user-id="'+account.user.id+'"]');await row.waitFor();
    assert.equal(await row.locator('[data-user-action="delete"]').count(),0,'Enabled accounts must not show deletion');
    page.once('dialog',d=>d.accept());await row.locator('[data-user-action="enabled"]').click();await row.locator('[data-user-action="delete"]').waitFor();
    assert.equal(await keyStatus(),401);
    page.once('dialog',d=>d.dismiss());await row.locator('[data-user-action="delete"]').click();assert.equal(await row.count(),1);
    page.once('dialog',d=>d.accept());await row.locator('[data-user-action="delete"]').click();
    await page.waitForFunction(()=>document.querySelector('#status').textContent.includes('请先在资源页接管'));
    assert.equal(await row.count(),1,'Owned playlists must block account deletion');
    await row.locator('[data-user-action="enabled"]').click();await row.locator('[data-user-action="delete"]').waitFor({state:'detached'});
    assert.equal(await keyStatus(),200,'Re-enabling must restore the same undeleted Key');
    page.once('dialog',d=>d.accept());await row.locator('[data-user-action="enabled"]').click();await row.locator('[data-user-action="delete"]').waitFor();
    await page.evaluate(async id=>{const user=state.users.find(u=>u.id===id);await api('admin/users/'+id,'PATCH',{display_name:'Changed during deletion'},undefined,user.version)},account.user.id);
    page.once('dialog',d=>d.accept());await row.locator('[data-user-action="delete"]').click();await page.waitForFunction(()=>document.querySelector('#status').textContent.includes('409'));
    await row.locator('[data-user-action="resources"]').click();await page.locator('[data-account-action="delete"]').waitFor();
    await page.selectOption('#transferTarget',await page.evaluate(()=>state.user.id));
    page.once('dialog',d=>d.accept());await page.click('#transferAll');
    await page.waitForFunction(id=>state.playlists.some(p=>p.id===id&&p.owner_id===state.user.id),resources.playlist.id);
    await page.screenshot({path:path.join(directory,'account-delete-ready.png'),fullPage:true});
    await page.setViewportSize({width:390,height:844});await page.waitForTimeout(250);
    const overflow=await page.evaluate(()=>[...document.querySelectorAll('#page-users *')].filter(el=>{const r=el.getBoundingClientRect();return r.width&&r.right>innerWidth+1}).map(el=>({id:el.id,tag:el.tagName,width:el.getBoundingClientRect().width})).slice(0,8));
    await page.screenshot({path:path.join(directory,'account-delete-mobile.png'),fullPage:true});
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'Account deletion detail must fit a mobile viewport: '+JSON.stringify(overflow));
    assert.equal(await page.locator('[data-account-action="delete"]').isVisible(),true);
    await page.screenshot({path:path.join(directory,'account-delete-mobile.png'),fullPage:true});await page.setViewportSize({width:1440,height:1100});
    page.once('dialog',d=>d.accept());await page.locator('[data-account-action="delete"]').click();
    await page.waitForFunction(id=>location.hash==='#/admin/users'&&!state.users.some(u=>u.id===id),account.user.id);
    assert.equal(await keyStatus(),401);
    assert.equal(await page.evaluate(id=>state.playlists.some(p=>p.id===id&&p.owner_id===state.user.id),resources.playlist.id),true);
    assert.equal(await page.evaluate(id=>fetch('/api/v2/admin/users/'+id+'/keys').then(r=>r.status),account.user.id),404);
    await page.reload();await page.locator('#userList tr').first().waitFor();assert.equal(await page.locator('[data-user-id="'+account.user.id+'"]').count(),0);
    // The transferred playlist is no longer owned by this identity, and it
    // never uploaded a song: the retained tombstone can now be fully removed.
    await page.locator('#storageDeleted summary').click();
    const purgeHistorical=page.locator('[data-purge-user="'+account.user.id+'"]');await purgeHistorical.waitFor();
    page.once('dialog',d=>d.dismiss());await purgeHistorical.click();assert.equal(await purgeHistorical.count(),1);
    page.once('dialog',d=>d.accept());await purgeHistorical.click();await purgeHistorical.waitFor({state:'detached'});
    const replacement=await page.evaluate(()=>api('admin/users','POST',{username:'Delete.User',quota_bytes:0,email_verification_required:false}));
    await page.evaluate(async id=>{await loadUsers();const u=state.users.find(u=>u.id===id);await api('admin/users/'+id,'PATCH',{enabled:false},undefined,u.version);await loadUsers()},replacement.user.id);
    const emptyRow=page.locator('[data-user-id="'+replacement.user.id+'"]');
    page.once('dialog',d=>d.accept());await emptyRow.locator('[data-user-action="purge"]').click();await emptyRow.waitFor({state:'detached'});
    // Exercise the actual downlisted-song action, not a direct deletion API.
    const removed=await page.evaluate(async()=>{const p=await api('browse/tracks?limit=1');const t=p.items[0];await api('tracks/'+t.id,'DELETE',undefined,undefined,t.version);return t});
    await page.goto(base+'/#/admin/library/removed');
    const removedRow=page.locator('#removedResults .resource-item').filter({hasText:removed.title});await removedRow.waitFor();
    page.once('dialog',d=>d.dismiss());await removedRow.getByRole('button',{name:'永久删除',exact:true}).click();assert.equal(await removedRow.count(),1);
    page.once('dialog',d=>d.accept());await removedRow.getByRole('button',{name:'永久删除',exact:true}).click();await removedRow.waitFor({state:'detached'});
    assert.equal((await page.request.get(base+'/api/v2/media/'+removed.id)).status(),404);
    assert.deepEqual(errors,[]);
    console.log('Account deletion UI passed: Key isolation, cancel, disable/re-enable, resource guard, stale version, transfer, soft delete/reload, historical and empty account purge, login name reuse, and downlisted track permanent deletion.');
  }finally{await context.close();page.off('pageerror',onError)}
};
