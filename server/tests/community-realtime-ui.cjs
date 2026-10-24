const assert=require('node:assert/strict');
const path=require('node:path');

module.exports=async function({page,browser,base,directory}){
  const contexts=[],playlists=[],errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  async function member(username){
    const account=await page.evaluate(username=>api('admin/users','POST',{username}),username);
    const context=await browser.newContext({viewport:{width:1440,height:1000}});contexts.push(context);
    const p=await context.newPage();p.on('pageerror',e=>errors.push(e.message));p.setDefaultTimeout(10000);
    await p.goto(base+'/#/invitations');await p.fill('#username',username);await p.fill('#password',account.temporary_password);await p.click('#loginButton');
    await p.locator('#firstPasswordScreen').waitFor({state:'visible'});
    const password='realtime test password '+username;
    await p.fill('#firstOldPassword',account.temporary_password);await p.fill('#firstNewPassword',password);await p.fill('#firstConfirmPassword',password);await p.click('#saveFirstPassword');
    await p.locator('#loginScreen').waitFor({state:'visible'});await p.fill('#username',username);await p.fill('#password',password);await p.click('#loginButton');
    await p.waitForFunction(()=>document.querySelector('#status').textContent==='已登录');return {page:p,context,username};
  }
  async function create(name,visibility){
    const p=await page.evaluate(async({name,visibility})=>{let p=await api('playlists','POST',{id:browserUUID(),name,track_ids:[state.tracks[0].id]});p=await api('playlists/'+p.id+'/visibility','PUT',{visibility},undefined,p.version);await refreshCore();return p},{name,visibility});
    playlists.push(p.id);return p;
  }
  async function visibility(id,value){return page.evaluate(async({id,value})=>{const p=(await api('playlists/'+id)).playlist;return api('playlists/'+id+'/visibility','PUT',{visibility:value},undefined,p.version)},{id,value})}
  try{
    const reader=await member('realtime-reader'),observer=await member('realtime-observer');
    const shared=await create('Realtime shared','shared');
    await page.evaluate(p=>openSharingSettings(p),shared);
    await page.fill('#inviteUsername',reader.username);await page.click('#sendInvitation');
    await reader.page.locator('[data-received-invitation]').waitFor({state:'visible',timeout:5000});
    assert.equal(await observer.page.locator('[data-received-invitation]').count(),0);
    assert.equal(await reader.page.locator('#invitationBadge').textContent(),'1');
    await page.fill('#inviteUsername','unsaved-invite-input');
    await reader.page.click('[data-invitation-action="accept"]');
    await page.locator('[data-owner-invitation]').filter({hasText:'已接受'}).waitFor({timeout:5000});
    assert.equal(await page.inputValue('#inviteUsername'),'unsaved-invite-input','Live owner list must preserve the invitation input');
    await reader.page.evaluate(id=>api('subscriptions/'+id,'DELETE'),shared.id);
    await reader.page.locator('[data-open-shared]').filter({hasText:'重新订阅'}).waitFor({timeout:5000});
    await reader.page.evaluate(id=>api('subscriptions/'+id,'PUT'),shared.id);
    await reader.page.locator('[data-open-shared]').filter({hasText:'查看订阅'}).waitFor({timeout:5000});
    await reader.page.click('[data-open-shared]');await reader.page.locator('#playlistDetail .playlist-track').waitFor();
    await page.evaluate(async id=>{const t=await api('tracks/'+id);await api('tracks/'+id,'PATCH',{title:'Live subscription track'},undefined,t.version)},shared.track_ids[0]);
    await reader.page.locator('#playlistDetail .track-title').filter({hasText:'Live subscription track'}).waitFor({timeout:5000});
    await page.click('[data-owner-invite-action="revoke"]');
    await reader.page.locator('[data-subscription-invalid]').waitFor({timeout:5000});
    assert.equal(await reader.page.locator('#playlistDetail .playlist-track').count(),0);
    await page.click('#closeSharing');

    await reader.page.goto(base+'/#/public/playlists');
    const first=await create('Live public first','public');
    await reader.page.locator('[data-public-playlist="'+first.id+'"]').waitFor({timeout:5000});
    const second=await create('Live public second','public');
    await reader.page.locator('[data-public-playlist="'+second.id+'"]').waitFor({timeout:5000});
    await reader.page.locator('[data-public-playlist="'+second.id+'"]').evaluate(el=>el.dataset.keepNode='retained');
    await page.evaluate(async id=>{const p=(await api('playlists/'+id)).playlist;await api('playlists/'+id,'PATCH',{name:'Renamed live'},undefined,p.version)},first.id);
    await reader.page.locator('[data-public-playlist="'+first.id+'"] .playlist-title').filter({hasText:'Renamed live'}).waitFor({timeout:5000});
    assert.equal(await reader.page.locator('[data-public-playlist="'+second.id+'"]').getAttribute('data-keep-node'),'retained','Unchanged cards must retain their DOM node');
    await reader.page.locator('[data-public-playlist="'+first.id+'"]').click();await reader.page.locator('#publicDetail .playlist-track').waitFor();
    await visibility(first.id,'private');await reader.page.locator('#publicDetail h1').filter({hasText:'歌单暂不可用'}).waitFor({timeout:5000});
    assert.equal(await reader.page.locator('#publicDetail .playlist-track').count(),0);
    await visibility(first.id,'public');await reader.page.locator('#publicDetail .playlist-track').waitFor({timeout:5000});
    await reader.page.fill('#publicPlaylistTrackSearch','Live subscription');
    await reader.page.locator('#publicPlaylistTrackSearch').evaluate(el=>{el.focus();el.setSelectionRange(2,5)});
    await page.evaluate(async id=>{const p=(await api('playlists/'+id)).playlist;await api('playlists/'+id,'PATCH',{name:'Changed while searching'},undefined,p.version)},first.id);
    await reader.page.locator('#publicDetail h1').filter({hasText:'Changed while searching'}).waitFor({timeout:5000});
    assert.equal(await reader.page.evaluate(()=>document.activeElement.id),'publicPlaylistTrackSearch');
    assert.deepEqual(await reader.page.locator('#publicPlaylistTrackSearch').evaluate(el=>[el.selectionStart,el.selectionEnd]),[2,5]);
    await reader.page.fill('#publicPlaylistTrackSearch','');

    // Disconnect, change the source, and catch up from the existing cursor.
    await reader.context.setOffline(true);
    await page.evaluate(async id=>{const p=(await api('playlists/'+id)).playlist;await api('playlists/'+id,'PATCH',{name:'Changed while offline'},undefined,p.version)},first.id);
    await reader.context.setOffline(false);
    await reader.page.locator('#publicDetail h1').filter({hasText:'Changed while offline'}).waitFor({timeout:12000});
    await reader.page.evaluate(()=>{Object.defineProperty(document,'hidden',{configurable:true,value:true});document.dispatchEvent(new Event('visibilitychange'))});
    await reader.page.waitForFunction(()=>!communityLive.source&&!communityLive.busy);
    let hiddenRequests=0;const count=r=>{if(r.url().includes('/api/v2/community/'))hiddenRequests++};reader.page.on('request',count);
    await visibility(first.id,'private');await page.waitForTimeout(350);assert.equal(hiddenRequests,0,'Hidden pages must not keep polling');reader.page.off('request',count);
    await reader.page.evaluate(()=>{delete document.hidden;document.dispatchEvent(new Event('visibilitychange'))});
    await reader.page.locator('#publicDetail h1').filter({hasText:'歌单暂不可用'}).waitFor({timeout:5000});
    // Explicit fallback uses the same delta endpoint and skips calls until due.
    await reader.page.evaluate(()=>{communityLive.source?.close();communityLive.source=null;communityLive.connected=false;communityLive.lastCheck=Date.now();Object.defineProperty(window,'EventSource',{configurable:true,value:undefined})});
    await visibility(first.id,'public');await reader.page.evaluate(()=>{communityLive.lastCheck-=60001;return communityCheck()});
    await reader.page.locator('#publicDetail .playlist-track').waitFor({timeout:5000});
    await reader.page.screenshot({path:path.join(directory,'community-realtime.png'),fullPage:true});
    await reader.page.locator('#accountMenu summary').click();await reader.page.click('#logout');await reader.page.locator('#loginScreen').waitFor({state:'visible'});
    assert.equal(await reader.page.evaluate(()=>communityLive.source===null&&communityLive.views.size===0),true);
    assert.equal(await reader.page.locator('#invitationBadge').isVisible(),false);
    assert.deepEqual(errors,[]);
    console.log('Realtime community passed: invitations/badge/owner acknowledgement, revocation, public item deltas with retained DOM, privacy, offline/hidden recovery, fallback and logout cleanup.');
  }finally{
    for(const context of contexts)await context.close();
    if(await page.locator('#sharingDialog').evaluate(el=>el.open))await page.click('#closeSharing');
    for(const id of playlists)await page.evaluate(async id=>{const r=await api('playlists/'+id);await api('playlists/'+id,'DELETE',undefined,undefined,r.playlist.version)},id).catch(()=>{});
  }
};
