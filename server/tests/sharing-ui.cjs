const assert=require('node:assert/strict');
const path=require('node:path');
module.exports=async function({page,browser,base,directory}){
  assert.equal(await page.locator('#myPlaylistTabs a[href="#/invitations"]').count(),1,'Invitation tab must be available in My playlists');
  const source=await page.evaluate(async()=>{const p=await api('playlists','POST',{id:browserUUID(),name:'Account-only mix',track_ids:[state.tracks[0].id],track_notes:{[state.tracks[0].id]:'AUTHOR ONLY SHARED CUE'}});await refreshCore();return p});
  const contexts=[],errors=[];
  try{
    const accounts=[];
    for(const username of ['share-reader','share-stranger'])accounts.push(await page.evaluate(username=>api('admin/users','POST',{username,display_name:username,quota_bytes:0,email_verification_required:false}),username));
    await page.evaluate(id=>location.hash='#/playlists/'+id,source.id);await page.locator('#playlistDetail [data-edit-playlist]').click();await page.selectOption('#playlistEditVisibility','shared');await page.click('#savePlaylistEdit');
    await page.waitForFunction(id=>!state.playlistEditing&&state.playlists.find(p=>p.id===id)?.visibility==='shared',source.id);
    await page.locator('[data-manage-sharing="'+source.id+'"]').click();await page.locator('#sharingDialog').waitFor({state:'visible'});
    await page.fill('#inviteUsername','share-reader');await page.click('#sendInvitation');await page.locator('[data-owner-invitation]').waitFor();
    const inviteID=await page.locator('[data-owner-invitation]').getAttribute('data-owner-invitation');
    await page.evaluate(()=>{window.shareCopy=copyText;copyText=async value=>{window.shareCopied=value}});await page.locator('[data-copy-invitation]').click();assert.equal(await page.evaluate(()=>window.shareCopied),base+'/#/invitations/'+inviteID);await page.evaluate(()=>{copyText=window.shareCopy});
    for(let index=0;index<accounts.length;index++){
      const account=accounts[index],username=account.user.username,context=await browser.newContext({viewport:{width:1440,height:1000}}),reader=await context.newPage();contexts.push({context,page:reader});reader.on('pageerror',error=>errors.push(error.message));
      await reader.goto(base+'/#/invitations/'+inviteID);await reader.fill('#username',username);await reader.fill('#password',account.temporary_password);await reader.click('#loginButton');await reader.locator('#firstPasswordScreen').waitFor({state:'visible'});
      const password='share personal password '+username;await reader.fill('#firstOldPassword',account.temporary_password);await reader.fill('#firstNewPassword',password);await reader.fill('#firstConfirmPassword',password);await reader.click('#saveFirstPassword');
      await reader.locator('#loginScreen').waitFor({state:'visible'});await reader.fill('#username',username);await reader.fill('#password',password);await reader.click('#loginButton');
      await reader.waitForFunction(()=>document.querySelector('#status').textContent==='已登录');assert.equal(await reader.evaluate(()=>location.hash),'#/invitations/'+inviteID);
      if(index===0)await reader.locator('[data-received-invitation]').waitFor();else{await reader.waitForFunction(()=>document.querySelector('#invitationStatus').textContent.includes('不属于当前账户'));assert.equal(await reader.locator('[data-received-invitation]').count(),0)}
    }
    const reader=contexts[0].page;
    await reader.locator('[data-invitation-action="accept"]').click();await reader.waitForFunction(id=>mySubscription(id)?.subscription_status==='active',source.id);
    await reader.locator('[data-open-shared]').click();await reader.locator('#playlistDetail .playlist-track').waitFor();
    assert.equal(await reader.locator('#playlistDetail [data-edit-playlist]').count(),0);assert.equal(await reader.locator('#playlistDetail [data-manage-sharing]').count(),0);
    assert.ok(!(await reader.locator('#playlistDetail').textContent()).includes('AUTHOR ONLY SHARED CUE'));
    await reader.evaluate(()=>location.hash='#/player/keys');await reader.click('#newKey');await reader.fill('#policyName','Shared account player');const check=reader.locator('[data-playlist-check][value="'+source.id+'"]');assert.equal(await check.isChecked(),false);await check.check();await reader.click('#saveModal');await reader.locator('#secretDialog').waitFor({state:'visible'});const secret=await reader.locator('#secretValue').textContent();await reader.click('#doneSecret');
    const access=()=>reader.evaluate(async({id,secret})=>(await fetch('/api/v2/playlists/'+id,{credentials:'omit',headers:{'X-API-Key':secret}})).status,{id:source.id,secret});assert.equal(await access(),200);
    await page.evaluate(()=>loadSharingSettings());assert.ok((await page.locator('[data-owner-invitation]').textContent()).includes('已接受'));
    await page.screenshot({path:path.join(directory,'sharing-owner-desktop.png'),fullPage:true});
    await page.setViewportSize({width:390,height:844});await page.waitForFunction(()=>document.querySelector('.main').getBoundingClientRect().left===0&&document.querySelector('.sidebar').getBoundingClientRect().right<=1);assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);await page.screenshot({path:path.join(directory,'sharing-owner-mobile.png'),fullPage:true});
    await page.locator('[data-owner-invite-action="revoke"]').click();await page.waitForFunction(()=>document.querySelector('[data-owner-invitation]').textContent.includes('已撤回'));
    await reader.evaluate(id=>location.hash='#/subscriptions/'+id,source.id);await reader.locator('[data-subscription-invalid]').waitFor();assert.equal(await access(),404);
    await page.locator('[data-owner-invite-action="resend"]').click();await page.waitForFunction(()=>document.querySelector('[data-owner-invitation]').textContent.includes('等待接受'));
    await reader.evaluate(id=>location.hash='#/invitations/'+id,inviteID);await reader.locator('[data-invitation-action="decline"]').click();await reader.waitForFunction(()=>document.querySelector('[data-received-invitation]').textContent.includes('已拒绝'));
    await page.evaluate(()=>loadSharingSettings());await page.locator('[data-owner-invite-action="resend"]').click();await page.waitForFunction(()=>document.querySelector('[data-owner-invitation]').textContent.includes('等待接受'));
    await reader.reload();await reader.locator('[data-invitation-action="accept"]').click();await reader.waitForFunction(()=>document.querySelector('[data-received-invitation]').textContent.includes('已接受'));assert.equal(await access(),200);
    await reader.setViewportSize({width:390,height:844});await reader.waitForFunction(()=>document.querySelector('.main').getBoundingClientRect().left===0);assert.equal(await reader.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);await reader.screenshot({path:path.join(directory,'sharing-received-mobile.png'),fullPage:true});
    await reader.locator('[data-open-shared]').click();await reader.locator('#playlistDetail [data-subscribe-playlist]').click();await reader.waitForFunction(()=>!state.communityMutating&&location.hash==='#/subscriptions');
    await reader.evaluate(id=>location.hash='#/invitations/'+id,inviteID);await reader.locator('[data-open-shared]').click();await reader.locator('#playlistDetail .playlist-track').waitFor();assert.equal(await access(),404,'Resubscribe must not re-grant key');
    await reader.evaluate(()=>location.hash='#/invitations');await reader.locator('[data-received-invitation]').waitFor();
    await reader.waitForFunction(()=>!communityLive.busy);
    let requests=0;reader.on('request',r=>{if(r.url().endsWith('/api/v2/community/sync'))requests++});
    await reader.evaluate(async()=>{window.sharingNow=Date.now;window.sharingTime=Date.now()+60001;Date.now=()=>window.sharingTime;Object.defineProperty(document,'hidden',{configurable:true,value:true});document.dispatchEvent(new Event('visibilitychange'));await communityCheck()});assert.equal(requests,0);
    const refreshed=reader.waitForRequest(r=>r.url().endsWith('/api/v2/community/sync'));
    await reader.evaluate(async()=>{Object.defineProperty(document,'hidden',{configurable:true,value:false});await communityCheck()});await refreshed;assert.equal(requests,1);
    await reader.evaluate(()=>{Date.now=window.sharingNow;delete document.hidden;scheduleCommunityRefresh()});
    await reader.locator('.account-menu summary').click();await reader.click('#logout');await reader.locator('#loginScreen').waitFor({state:'visible'});assert.equal(await reader.locator('#receivedInvitations').textContent(),'');assert.equal(await reader.evaluate(()=>state.receivedInvitations.length),0);
    assert.deepEqual(errors,[]);console.log('Sharing UI passed: shared editor, owner invitations and copy, account-bound login target, reject/reinvite/accept, explicit key grants, revoke/recover, resubscribe isolation, visible-only refresh, mobile and logout cleanup.');
  }finally{
    for(const c of contexts)await c.context.close();
    if(await page.locator('#sharingDialog').evaluate(d=>d.open))await page.click('#closeSharing');await page.setViewportSize({width:1440,height:1100});
    await page.evaluate(async id=>{await refreshCore();const p=state.playlists.find(v=>v.id===id);if(p)await api('playlists/'+id,'DELETE',undefined,undefined,p.version);await refreshCore()},source.id);
  }
};
