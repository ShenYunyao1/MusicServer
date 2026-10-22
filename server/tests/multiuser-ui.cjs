const assert=require('node:assert/strict');
const path=require('node:path');

module.exports=async function runMultiuserUI({browser,adminPage,base,audio,directory}) {
 const errors=[];
 const fixtures=[];
 const accounts=[];
 try {
  for(const username of ['browser-alice','browser-bob']) {
   const result=await adminPage.evaluate(username=>api('admin/users','POST',{username,display_name:username}),username);
   const context=await browser.newContext({viewport:{width:1440,height:1100}});const page=await context.newPage();page.on('pageerror',error=>errors.push(error.message));
   await page.goto(base);await page.fill('#username',username);await page.fill('#password',result.temporary_password);await page.click('#loginButton');
   await page.locator('#firstPasswordScreen').waitFor({state:'visible'});
   assert.equal(await page.evaluate(async()=> (await fetch('/api/v2/tracks')).status),403);
   assert.equal(await page.locator('.app').isVisible(),false);
   await page.reload();await page.locator('#firstPasswordScreen').waitFor({state:'visible'});
   const password='personal browser password '+username;
   await page.fill('#firstOldPassword',result.temporary_password);await page.fill('#firstNewPassword',password);await page.fill('#firstConfirmPassword',password);await page.click('#saveFirstPassword');
   await page.locator('#loginScreen').waitFor({state:'visible'});
   await page.fill('#username',username);await page.fill('#password',password);await page.click('#loginButton');
   await page.waitForFunction(()=>document.querySelector('#status').textContent==='已登录');
   assert.equal(await page.locator('[data-page="settings"]').isVisible(),false);
   assert.equal(await page.locator('#fullScan').isVisible(),false);
   fixtures.push({context,page});accounts.push({user:result.user,password,username,page});
  }
  const [a,b]=accounts;
  await a.page.click('[data-page="upload"]');const upload=Buffer.from(audio);upload[44]=7;
  await a.page.setInputFiles('#uploadInput',{name:'Alice song.wav',mimeType:'audio/wav',buffer:upload});
  await a.page.waitForFunction(()=>state.uploads.length===1);
  await a.page.locator('[data-stage-upload]').click();await a.page.waitForFunction(()=>state.uploads[0].stageId);
  await a.page.locator('[data-confirm-upload]').click();await a.page.waitForFunction(()=>state.uploads[0].status==='完成');
  const track=await a.page.evaluate(()=>state.tracks.find(t=>t.title==='Alice song'));
  assert.equal(track.owner_id,a.user.id);assert.equal(track.uploaded_by,a.user.id);assert.equal(track.path,'');
  await a.page.click('[data-page="tracks"]');await a.page.click('[data-filter="mine"]');assert.equal(await a.page.locator('#trackRows tr').count(),1);
  await b.page.evaluate(()=>refreshCore());
  await b.page.click('[data-page="playlists"]');await b.page.click('#newPlaylist');await b.page.fill('[data-field="name"]','Bob private');await b.page.click('#saveModal');
  await b.page.waitForFunction(()=>state.playlists.some(p=>p.name==='Bob private'));
  await b.page.evaluate(()=>openPlaylistTrackPicker(state.playlists.find(p=>p.name==='Bob private')));
  await b.page.locator('#modalBody input[value="'+track.id+'"]').check();await b.page.click('#saveModal');await b.page.waitForFunction(()=>state.playlists.some(p=>p.name==='Bob private'&&p.track_ids.length===1));
  const playlist=await b.page.evaluate(()=>state.playlists.find(p=>p.name==='Bob private'));
  await b.page.evaluate(()=>{const p=state.playlists.find(p=>p.name==='Bob private');editPlaylistTrackNote(p,state.tracks.find(t=>t.id===p.track_ids[0]))});
  await b.page.fill('[data-field="comment"]','Bob personal cue');await b.page.click('#saveModal');await b.page.waitForFunction(()=>state.playlists.some(p=>p.track_notes&&Object.values(p.track_notes).includes('Bob personal cue')));
  await b.page.click('[data-page="tracks"]');
  await b.page.evaluate(id=>{const t=state.tracks.find(t=>t.id===id);prepareTrackContext(t);showTrackMenu({clientX:200,clientY:200},t)},track.id);
  assert.equal(await b.page.locator('#contextMenu').getByText('编辑曲目',{exact:true}).count(),0);assert.equal(await b.page.locator('#contextMenu').getByText('移除曲目',{exact:true}).count(),0);
  await b.page.evaluate(()=>hideContextMenu());
  await a.page.evaluate(()=>refreshCore());assert.equal(await a.page.evaluate(()=>state.playlists.some(p=>p.name==='Bob private')),false);
  a.page.once('dialog',dialog=>dialog.accept());await a.page.evaluate(id=>removeTrack(state.tracks.find(t=>t.id===id)),track.id);
  await a.page.waitForFunction(()=>document.querySelector('#status').textContent.includes('409'));
  assert.ok(await a.page.evaluate(id=>state.tracks.some(t=>t.id===id),track.id));
  await b.page.click('[data-page="keys"]');await b.page.click('#newKey');await b.page.fill('#policyName','Bob player');await b.page.locator('[data-playlist-check][value="'+playlist.id+'"]').check();await b.page.click('#saveModal');await b.page.locator('#secretDialog').waitFor({state:'visible'});const secret=await b.page.locator('#secretValue').textContent();await b.page.click('#doneSecret');
  assert.equal(await b.page.evaluate(async secret=>(await fetch('/api/v2/tracks',{headers:{'X-API-Key':secret}})).status,secret),200);
  await b.page.click('[data-page="stats"]');await b.page.waitForFunction(()=>state.stats?.total.total_requests>=1);assert.equal(await b.page.locator('#statsTable tbody tr').count(),1);
  await b.page.reload();await b.page.waitForFunction(()=>document.querySelector('#status').textContent==='已登录');assert.equal(await b.page.locator('[data-page="settings"]').isVisible(),false);
  await b.page.screenshot({path:path.join(directory,'member-overview.png'),fullPage:true});
  // Administrator removal preserves a visible placeholder and Bob's note.
  await adminPage.evaluate(()=>refreshCore());await adminPage.evaluate(async id=>{const t=state.tracks.find(t=>t.id===id);await api('tracks/'+id,'DELETE',undefined,undefined,t.version)},track.id);
  await b.page.evaluate(()=>refreshCore());await b.page.click('[data-page="playlists"]');await b.page.evaluate(()=>{state.playlistDetailId=state.playlists.find(p=>p.name==='Bob private').id;renderPlaylists()});
  assert.ok((await b.page.locator('#playlistDetail').textContent()).includes('不可用曲目'));assert.ok((await b.page.locator('#playlistDetail').textContent()).includes('Bob personal cue'));
  await adminPage.evaluate(async id=>{const removed=await api('admin/removed');const t=removed.find(t=>t.id===id);await api('admin/removed/'+id,'POST',undefined,undefined,t.version)},track.id);
  await b.page.evaluate(()=>refreshCore());assert.ok((await b.page.locator('#playlistDetail').textContent()).includes('Alice song'));
  await a.page.click('#logout');await a.page.waitForFunction(()=>!state.user);assert.equal(await a.page.evaluate(()=>state.tracks.length+state.playlists.length+state.keys.length),0);
  assert.deepEqual(errors,[]);
  console.log('Member UI passed: forced password/reload, global catalogue, owner upload, private notes, own key/stats, reference protection, unavailable placeholder and logout cleanup.');
  return {accounts:accounts.map(({page,...account})=>account),track,playlist,secret};
 }finally {for(const {context} of fixtures)await context.close()}
};
