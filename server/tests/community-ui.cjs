const assert=require('node:assert/strict');
const path=require('node:path');
module.exports=async function({page,browser,base,directory}){
  assert.equal(await page.locator('.nav [data-page="public"]').count(),1,'Public playlists need a navigation entry');
  const source=await page.evaluate(async()=>{
    let p=await api('playlists','POST',{id:browserUUID(),name:'Community fixture',description:'Public browser regression'});
    p=await api('playlists/'+p.id+'/tracks','POST',{track_ids:[state.tracks[0].id]},undefined,p.version);
    await api('playlists/'+p.id+'/tracks/'+p.track_ids[0],'PATCH',{note:'AUTHOR PRIVATE CUE'},undefined,p.version);
    await refreshCore();return state.playlists.find(v=>v.id===p.id);
  });
  async function visibility(value){
    await page.evaluate(async({id,value})=>{await refreshCore();editPlaylist(state.playlists.find(p=>p.id===id))},{id:source.id,value});
    await page.selectOption('#playlistEditVisibility',value);await page.click('#savePlaylistEdit');
    await page.waitForFunction(({id,value})=>!state.playlistEditing&&state.playlists.some(p=>p.id===id&&p.visibility===value),{id:source.id,value});
  }
  await visibility('public');
  const contexts=[],errors=[];
  try{
    for(const username of ['community-reader','community-observer']){
      const created=await page.evaluate(username=>api('admin/users','POST',{username,display_name:username}),username);
      const context=await browser.newContext({viewport:{width:1440,height:1000}}),reader=await context.newPage();contexts.push({context,page:reader});reader.on('pageerror',e=>errors.push(e.message));
      await reader.goto(base+'/#/public/playlists/'+source.id);
      await reader.fill('#username',username);await reader.fill('#password',created.temporary_password);await reader.click('#loginButton');
      await reader.locator('#firstPasswordScreen').waitFor({state:'visible'});
      const password='community personal password '+username;
      await reader.fill('#firstOldPassword',created.temporary_password);await reader.fill('#firstNewPassword',password);await reader.fill('#firstConfirmPassword',password);await reader.click('#saveFirstPassword');
      await reader.locator('#loginScreen').waitFor({state:'visible'});await reader.fill('#username',username);await reader.fill('#password',password);await reader.click('#loginButton');
      await reader.locator('#publicDetail .playlist-track').waitFor();
      assert.equal(await reader.evaluate(()=>location.hash),'#/public/playlists/'+source.id,'Login must preserve shared target');
      assert.equal(await reader.locator('#publicDetail [data-edit-playlist]').count(),0);
      assert.ok(!(await reader.locator('#publicDetail').textContent()).includes('AUTHOR PRIVATE CUE'));
    }
    const reader=contexts[0].page,observer=contexts[1].page;
    await reader.evaluate(()=>{window.communityCopy=copyText;copyText=async value=>{window.communityCopied=value}});
    await reader.locator('#publicDetail [data-share-playlist]').click();
    assert.equal(await reader.evaluate(()=>window.communityCopied),base+'/#/public/playlists/'+source.id);
    await reader.evaluate(()=>{copyText=window.communityCopy});
    await reader.locator('#publicDetail [data-subscribe-playlist]').click();
    await reader.waitForFunction(id=>mySubscription(id)?.subscription_status==='active',source.id);
    await reader.evaluate(id=>location.hash='#/subscriptions/'+id,source.id);await reader.locator('#playlistDetail .playlist-track').waitFor();
    assert.equal(await reader.locator('#playlistDetail [data-edit-playlist]').count(),0);
    assert.equal(await reader.locator('#playlistDetail [data-add-members]').count(),0);
    await reader.evaluate(id=>editPlaylistTrackNote(mySubscription(id),state.tracks.find(t=>t.id===mySubscription(id).track_ids[0])),source.id);
    await reader.fill('[data-field="comment"]','READER PRIVATE CUE');await reader.click('#saveModal');
    await reader.waitForFunction(id=>Object.values(mySubscription(id)?.track_notes||{}).includes('READER PRIVATE CUE'),source.id);
    await observer.reload();await observer.locator('#publicDetail .playlist-track').waitFor();
    assert.ok(!(await observer.locator('#publicDetail').textContent()).includes('READER PRIVATE CUE'));
    await reader.evaluate(()=>location.hash='#/player/keys');await reader.click('#newKey');await reader.fill('#policyName','Community subscription player');
    assert.equal(await reader.locator('.key-playlist-group').count(),3);
    const grant=reader.locator('[data-playlist-check][value="'+source.id+'"]');assert.equal(await grant.isChecked(),false);await grant.check();
    await reader.click('#saveModal');await reader.locator('#secretDialog').waitFor({state:'visible'});const secret=await reader.locator('#secretValue').textContent();await reader.click('#doneSecret');
    const check=()=>reader.evaluate(async({id,secret})=>{const r=await fetch('/api/v2/playlists/'+id,{credentials:'omit',headers:{'X-API-Key':secret}});return r.status},{id:source.id,secret});
    assert.equal(await check(),200);
    await reader.evaluate(id=>location.hash='#/subscriptions/'+id,source.id);await reader.locator('#playlistDetail .playlist-track').waitFor();
    await reader.screenshot({path:path.join(directory,'subscription-desktop.png'),fullPage:true});
    await reader.setViewportSize({width:390,height:844});
    await reader.waitForFunction(()=>document.querySelector('.main').getBoundingClientRect().left===0&&document.querySelector('.sidebar').getBoundingClientRect().right<=1);
    await reader.screenshot({path:path.join(directory,'subscription-mobile.png'),fullPage:true});
    assert.equal(await reader.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,JSON.stringify(await reader.evaluate(()=>[...document.querySelectorAll('.content *')].filter(e=>e.getBoundingClientRect().right>innerWidth&&e.getBoundingClientRect().width).map(e=>[e.tagName,e.className,e.id,e.getBoundingClientRect().right]))));
    await visibility('private');await reader.evaluate(()=>loadMySubscriptions());await reader.locator('[data-subscription-invalid]').waitFor();
    assert.equal(await reader.locator('#playlistDetail .playlist-track').count(),0);assert.equal(await check(),404);
    await reader.screenshot({path:path.join(directory,'subscription-invalid.png'),fullPage:true});
    await observer.reload();await observer.locator('#publicDetail h1').waitFor();assert.ok((await observer.locator('#publicDetail').textContent()).includes('歌单暂不可用'));
    await visibility('public');await reader.evaluate(()=>loadMySubscriptions());await reader.locator('#playlistDetail .playlist-track').waitFor();assert.equal(await check(),200);
    await reader.locator('#playlistDetail [data-subscribe-playlist]').click();await reader.waitForFunction(()=>location.hash==='#/subscriptions'&&!state.communityMutating);
    assert.equal(await reader.evaluate(id=>Boolean(mySubscription(id)),source.id),false);assert.equal(await check(),404);
    await reader.setViewportSize({width:1440,height:1000});await reader.evaluate(()=>location.hash='#/public/playlists');await reader.locator('[data-public-playlist="'+source.id+'"]').waitFor();
    const responses=[];reader.on('response',r=>{if(r.url().includes('/api/v2/public/playlists?'))responses.push(r.status())});
    await reader.click('#refreshPublic');await reader.waitForFunction(()=>!document.querySelector('#refreshPublic').disabled);assert.ok(responses.includes(304));
    // Realtime remains primary; the minute delta check pauses in hidden tabs.
    await reader.waitForFunction(()=>!communityLive.busy);
    let deltaRequests=0;reader.on('request',r=>{if(r.url().endsWith('/api/v2/community/sync'))deltaRequests++});
    const before=deltaRequests;
    await reader.evaluate(async()=>{
      window.communityRealNow=Date.now;window.communityFakeNow=Date.now()+60001;Date.now=()=>window.communityFakeNow;
      Object.defineProperty(document,'hidden',{configurable:true,value:true});document.dispatchEvent(new Event('visibilitychange'));await communityCheck();
    });assert.equal(deltaRequests,before);
    await reader.evaluate(async()=>{Object.defineProperty(document,'hidden',{configurable:true,value:false});await communityCheck()});assert.equal(deltaRequests,before+1);
    await reader.evaluate(()=>communityCheck());assert.equal(deltaRequests,before+1);
    await reader.evaluate(()=>{Date.now=window.communityRealNow;delete document.hidden;scheduleCommunityRefresh()});
    await reader.screenshot({path:path.join(directory,'public-catalog.png'),fullPage:true});
    await reader.locator('.account-menu summary').click();await reader.click('#logout');await reader.locator('#loginScreen').waitFor({state:'visible'});
    assert.equal(await reader.evaluate(()=>communityCache.size),0);assert.equal(await reader.locator('#publicDetail').textContent(),'');
    assert.deepEqual(errors,[]);console.log('Community UI passed: shared-target login, atomic visibility, private notes, explicit grants, suspend/restore/unsubscribe, ETag and visible-only minute delta fallback.');
  }finally{
    for(const c of contexts)await c.context.close();
    await page.evaluate(async id=>{await refreshCore();const p=state.playlists.find(v=>v.id===id);if(p)await api('playlists/'+id,'DELETE',undefined,undefined,p.version);await refreshCore()},source.id);
  }
};
