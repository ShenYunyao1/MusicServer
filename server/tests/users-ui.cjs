const assert=require('node:assert/strict');
const path=require('node:path');
module.exports=async function runUsersUI({browser,page,base,directory,accounts,track,alicePlaylist,aliceSecret}){
 const [a,b]=accounts;
 const errors=[];const onError=error=>errors.push(error.message);page.on('pageerror',onError);
 await page.click('[data-page="users"]');await page.locator('#userList tr').filter({hasText:'browser-alice'}).waitFor();
 assert.equal(await page.locator('#userList tr').filter({has:page.locator('strong',{hasText:/^admin$/})}).locator('[data-user-action="reset"]').isDisabled(),true);
 await page.click('#createUser');await page.fill('#editor-field-username','browser-charlie');await page.check('[name="createQuotaMode"][value="unlimited"]');await page.uncheck('#createRequireEmail');await page.click('#saveModal');await page.locator('#temporaryPasswordDialog').waitFor({state:'visible'});const firstPassword=await page.locator('#temporaryPasswordValue').textContent();assert.ok(firstPassword);await page.click('#closeTemporaryPassword');assert.equal(await page.locator('#temporaryPasswordValue').textContent(),'');
 const charlie=page.locator('#userList tr').filter({hasText:'browser-charlie'});
 await charlie.locator('[data-user-action="reset"]').click();await page.click('#saveModal');await page.locator('#temporaryPasswordDialog').waitFor({state:'visible'});const resetPassword=await page.locator('#temporaryPasswordValue').textContent();assert.ok(resetPassword&&resetPassword!==firstPassword);await page.click('#closeTemporaryPassword');assert.equal(await page.locator('#temporaryPasswordValue').textContent(),'');
 // Conflict keeps the stale operation from silently overwriting a newer account.
 await page.evaluate(()=>loadUsers());
 const aliceRow=page.locator('[data-user-id="'+a.user.id+'"]');
 await aliceRow.locator('[data-user-action="display"]').click();
 const stale=await page.evaluate(id=>state.users.find(u=>u.id===id),a.user.id);
 await page.evaluate(async user=>api('admin/users/'+user.id,'PATCH',{display_name:'Alice updated'},undefined,user.version),stale);
 await page.fill('[data-field="display_name"]','Stale display name');await page.click('#saveModal');await page.waitForFunction(()=>document.querySelector('#status').textContent.includes('409'));
 await page.click('#cancelModal');await page.click('#refreshUsers');await page.waitForFunction(id=>state.users.some(user=>user.id===id&&user.display_name==='Alice updated'),a.user.id);
 assert.ok((await aliceRow.textContent()).includes('已启用'));
 const before=await page.evaluate(id=>api('admin/stats?user_id='+id),a.user.id);assert.equal(before.keys.length,1);
 page.once('dialog',dialog=>dialog.accept());await aliceRow.locator('[data-user-action="enabled"]').click();await page.waitForFunction(id=>state.users.some(u=>u.id===id&&!u.enabled),a.user.id);
 assert.equal((await fetch(base+'/api/v2/tracks',{headers:{'X-API-Key':aliceSecret}})).status,401);
 const disabled=await page.evaluate(id=>api('admin/stats?user_id='+id),a.user.id);assert.equal(disabled.total.total_requests,before.total.total_requests);assert.equal(disabled.keys[0].account_enabled,false);
 await aliceRow.locator('[data-user-action="resources"]').click();await page.locator('#userResourcesPanel').waitFor({state:'visible'});
 await page.click('[data-resource-kind="keys"]');await page.waitForFunction(()=>document.querySelector('#userResources').textContent.includes('账户已停用'));
 await page.click('[data-resource-kind="owned"]');await page.waitForFunction(()=>state.userResources.some(t=>t.title==='Alice song'));
 await page.selectOption('#transferTarget',b.user.id);page.once('dialog',dialog=>dialog.accept());await page.click('#transferAll');await page.waitForFunction(()=>state.resourceTotal===0);
 const moved=await page.evaluate(id=>api('tracks/'+id),track.id);assert.equal(moved.owner_id,b.user.id);assert.equal(moved.uploaded_by,a.user.id);
 await page.click('[data-resource-kind="uploaded"]');await page.waitForFunction(()=>state.userResources.some(t=>t.title==='Alice song'));
 await page.screenshot({path:path.join(directory,'users-takeover.png'),fullPage:true});
 await page.locator('[data-account-action="enabled"]').click();await page.waitForFunction(id=>state.users.some(u=>u.id===id&&u.enabled),a.user.id);
 const resumed=await fetch(base+'/api/v2/tracks',{headers:{'X-API-Key':aliceSecret}});assert.equal(resumed.status,200);assert.deepEqual(await resumed.json(),[]);
 assert.equal((await fetch(base+'/api/v2/playlists/'+alicePlaylist.id,{headers:{'X-API-Key':aliceSecret}})).status,404);
 const after=await page.evaluate(id=>api('admin/stats?user_id='+id),a.user.id);assert.equal(after.keys[0].key_id,before.keys[0].key_id);assert.ok(after.total.total_requests>before.total.total_requests);
 await page.click('[data-page="keys"]');await page.waitForFunction(()=>state.page==='keys');await page.click('[data-page="stats"]');await page.waitForFunction(()=>state.statsScope==='self'&&state.stats.keys.length===0);
 assert.equal(await page.locator('#statsUser').isVisible(),false);
 await page.click('[data-page="overview"]');await page.locator('#adminStatsLink').click();await page.selectOption('#statsUser',a.user.id);await page.waitForFunction(id=>state.stats.keys.every(key=>key.user_id===id),a.user.id);
 const context=await browser.newContext();try{
  const member=await context.newPage();member.on('pageerror',onError);await member.goto(base);await member.fill('#username',a.username);await member.fill('#password',a.password);await member.click('#loginButton');await member.waitForFunction(()=>document.querySelector('#status').textContent==='已登录');
  assert.equal(await member.locator('[data-page="users"]').isVisible(),false);assert.equal(await member.locator('#statsUser').isVisible(),false);
  assert.equal(await member.evaluate(id=>state.playlists.some(p=>p.id===id),alicePlaylist.id),false);
  assert.equal(await member.evaluate(id=>state.tracks.find(t=>t.id===id).owner_id,track.id),b.user.id);
 }finally{await context.close()}
 assert.deepEqual(errors,[]);page.off('pageerror',onError);
 console.log('Administrator UI passed: create/reset one-time passwords, conflict refresh, disable/enable same key/statistics, account resources/keys, atomic takeover and immutable uploader.');
};
