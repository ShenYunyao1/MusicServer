const assert=require('node:assert/strict');
const path=require('node:path');
const {spawnSync}=require('node:child_process');

module.exports=async function runMultiuserUI({browser,adminPage,base,audio,directory}) {
 const errors=[];
 const fixtures=[];
 const accounts=[];
 try {
  for(const username of ['browser-alice','browser-bob']) {
   const result=await adminPage.evaluate(username=>api('admin/users','POST',{username,display_name:username,quota_bytes:0,email_verification_required:false}),username);
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
   assert.equal(await page.evaluate(()=>location.hash.split('?')[0]),'#/library');assert.equal(await page.evaluate(()=>new URLSearchParams(location.hash.split('?')[1]).get('page_size')),'50');
   await page.evaluate(()=>location.hash='#/admin/users');await page.locator('#routeError').waitFor({state:'visible'});
   await page.locator('#routeError a').click();await page.waitForFunction(()=>state.page==='tracks'&&document.querySelector('#routeError').hidden);
   assert.equal(await page.locator('[data-page="settings"]').isVisible(),false);
   assert.equal(await page.locator('#fullScan').isVisible(),false);
   fixtures.push({context,page});accounts.push({user:result.user,password,username,page});
  }
  const [a,b]=accounts;
  // No private playlist is required to create and authenticate a player key.
  await a.page.click('[data-page="keys"]');await a.page.click('#newKey');await a.page.fill('#policyName','Empty player fixture');await a.page.click('#saveModal');await a.page.locator('#secretDialog').waitFor({state:'visible'});
  const emptySecret=await a.page.locator('#secretValue').textContent();assert.ok(emptySecret);
  await a.page.waitForFunction(()=>state.keys.some(k=>k.name==='Empty player fixture'));
  const emptyKey=await a.page.evaluate(()=>state.keys.find(k=>k.name==='Empty player fixture'));
  assert.equal((await a.page.evaluate(()=>state.playlists.filter(p=>!p.system))).length,0);
  if(await a.page.evaluate(()=>!navigator.clipboard?.writeText)){
   // Inspect the selected value without changing the user's OS clipboard.
   await a.page.evaluate(()=>{window.fixtureClipboard='';document.execCommand=command=>{if(command!=='copy')return false;window.fixtureClipboard=document.activeElement.value;return true}});
   let confirmationDialogs=0;const onCopyDialog=dialog=>{confirmationDialogs++;dialog.dismiss()};a.page.on('dialog',onCopyDialog);
   await a.page.click('#copySecret');await a.page.locator('#copyFeedback').waitFor({state:'visible'});assert.equal(await a.page.locator('#copyFeedback').textContent(),'已复制到剪贴板');
   assert.ok(await a.page.evaluate(secret=>window.fixtureClipboard===secret,emptySecret),'copy fallback did not select the full secret inside the modal');
   await a.page.locator('#copyFeedback').waitFor({state:'hidden',timeout:5000});assert.equal(await a.page.locator('#secretDialog').isVisible(),true);assert.equal(confirmationDialogs,0);a.page.off('dialog',onCopyDialog);
   await a.page.evaluate(()=>{document.execCommand=()=>false});await a.page.click('#copySecret');await a.page.locator('#copyFeedback').waitFor({state:'visible'});assert.ok((await a.page.locator('#copyFeedback').textContent()).includes('请手动复制'));
   assert.ok(await a.page.evaluate(secret=>window.getSelection().toString()===secret,emptySecret),'copy failure did not select the full key for manual copying');
   await a.page.evaluate(()=>{delete document.execCommand;delete window.fixtureClipboard});
  }
  for(const route of ['playlists','sync']){
   const response=await a.page.evaluate(async({route,secret})=>{const r=await fetch('/api/v2/'+route,{credentials:'omit',headers:{'X-API-Key':secret}});return {status:r.status,body:await r.json()}},{route,secret:emptySecret});
   assert.equal(response.status,200);if(route==='playlists')assert.deepEqual(response.body,[]);else assert.deepEqual(response.body.playlists,[]);
  }
  if(process.env.MUSIC_SERVER_TEST_PLAYER_CLIENT){
   const client=spawnSync(process.env.MUSIC_SERVER_TEST_PLAYER_CLIENT,['--server'],{input:JSON.stringify({url:base,key:emptySecret,cache:path.join(directory,'empty-player-cache')}),encoding:'utf8',timeout:30000,windowsHide:true});
   assert.equal(client.status,0,client.stderr);const result=JSON.parse(client.stdout);assert.equal(result.success,true,result.error);assert.equal(result.offline,false);assert.deepEqual(result.playlists,[]);
  }
  await a.page.click('#doneSecret');await a.page.evaluate(async id=>{await api('auth/keys/'+id,'DELETE');await loadKeys()},emptyKey.id);
  console.log('Empty-library member key passed: zero private playlists, copy fallback/manual selection and 200 empty grants'+(process.env.MUSIC_SERVER_TEST_PLAYER_CLIENT?', native player sync.':'.'));
  await a.page.click('[data-page="upload"]');const upload=Buffer.from(audio);upload[44]=7;
  await a.page.setInputFiles('#uploadInput',{name:'Alice song.wav',mimeType:'audio/wav',buffer:upload});
  await a.page.waitForFunction(()=>state.uploads.length===1);
  await a.page.check('#uploadReview');await a.page.locator('[data-stage-upload]').click();await a.page.waitForFunction(()=>state.uploads[0].stageId);
  await a.page.locator('[data-confirm-upload]').click();await a.page.waitForFunction(()=>state.uploads[0].status==='完成');
  await a.page.waitForFunction(()=>state.tracks.some(t=>t.title==='Alice song'));
  const track=await a.page.evaluate(()=>state.tracks.find(t=>t.title==='Alice song'));
  assert.equal(track.owner_id,a.user.id);assert.equal(track.uploaded_by,a.user.id);assert.equal(track.path,'');
  await a.page.click('[data-page="playlists"]');await a.page.click('#newPlaylist');await a.page.fill('#playlistEditName','Alice private');await a.page.click('#savePlaylistEdit');await a.page.waitForFunction(()=>state.playlists.some(p=>p.name==='Alice private'));
  await a.page.evaluate(async id=>{const p=state.playlists.find(p=>p.name==='Alice private');await api('playlists/'+p.id+'/tracks','POST',{track_ids:[id]},undefined,p.version);await refreshCore()},track.id);
  await a.page.click('[data-page="keys"]');await a.page.click('#newKey');await a.page.fill('#policyName','Alice player');
  const alicePlaylist=await a.page.evaluate(()=>state.playlists.find(p=>p.name==='Alice private'));
  await a.page.locator('[data-playlist-check][value="'+alicePlaylist.id+'"]').check();await a.page.click('#saveModal');await a.page.locator('#secretDialog').waitFor({state:'visible'});const aliceSecret=await a.page.locator('#secretValue').textContent();await a.page.click('#doneSecret');
  assert.equal(await a.page.evaluate(async secret=>(await fetch('/api/v2/tracks',{headers:{'X-API-Key':secret}})).status,aliceSecret),200);
  await a.page.click('[data-page="tracks"]');await a.page.click('[data-filter="mine"]');await a.page.waitForFunction(()=>browseLibraryPage()?.filter==='mine'&&browseLibraryPage().total===1);assert.equal(await a.page.locator('#trackRows tr').count(),1);
  await b.page.evaluate(()=>refreshCore());
  await b.page.click('[data-page="playlists"]');await b.page.click('#newPlaylist');await b.page.fill('#playlistEditName','Bob private');await b.page.click('#savePlaylistEdit');
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
  await b.page.evaluate(()=>refreshCore());
  await b.page.evaluate(id=>location.hash='#/playlists/'+id,playlist.id);
  await b.page.waitForFunction(id=>state.playlistDetailId===id&&document.querySelector('#playlistDetail').textContent.includes('不可用曲目'),playlist.id);
  assert.ok((await b.page.locator('#playlistDetail').textContent()).includes('不可用曲目'));assert.ok((await b.page.locator('#playlistDetail').textContent()).includes('Bob personal cue'));
  await adminPage.evaluate(async id=>{const removed=await api('admin/removed');const t=removed.find(t=>t.id===id);await api('admin/removed/'+id,'POST',undefined,undefined,t.version)},track.id);
  await b.page.evaluate(()=>refreshCore());assert.ok((await b.page.locator('#playlistDetail').textContent()).includes('Alice song'));
  await a.page.locator('#accountMenu summary').click();await a.page.click('#logout');await a.page.waitForFunction(()=>!state.user);assert.equal(await a.page.evaluate(()=>state.tracks.length+state.playlists.length+state.keys.length),0);
  assert.deepEqual(errors,[]);
  console.log('Member UI passed: forced password/reload, global catalogue, owner upload, private notes, own key/stats, reference protection, unavailable placeholder and logout cleanup.');
  return {accounts:accounts.map(({page,...account})=>account),track,playlist,secret,alicePlaylist,aliceSecret};
 }finally {for(const {context} of fixtures)await context.close()}
};
