'use strict';
const assert=require('node:assert/strict');
module.exports=async({page,base})=>{
 const errors=[];page.on('pageerror',e=>errors.push(e.message));
 const playlists=await page.evaluate(async()=>{const list=[];for(let i=0;i<53;i++)list.push(await api('playlists','POST',{id:browserUUID(),name:'Audit target '+String(i).padStart(2,'0'),track_ids:[]}));return list});
 await page.goto(base+'/#/uploads');await page.locator('#uploadTargetPlaylist').waitFor();await page.waitForFunction(()=>uploadTargetCatalog.total===53);
 await page.selectOption('#uploadTargetPlaylist',playlists[0].id);await page.evaluate(()=>addFiles([new File([new Uint8Array(100)],'pending-target.wav')]));
 await page.goto(base+'/#/playlists?q=not-an-audit-target');await page.waitForFunction(()=>browseState.playlists?.items.length===0);
 await page.goto(base+'/#/uploads');await page.waitForFunction(id=>document.querySelector('#uploadTargetPlaylist').value===id,playlists[0].id);assert.equal(await page.evaluate(()=>state.uploads[0].targetPlaylistID),undefined,'Target is attached at start, without silently rewriting a queue');
 await page.fill('#uploadTargetSearch','Audit target 52');await page.waitForFunction(()=>uploadTargetCatalog.total===1);await page.selectOption('#uploadTargetPlaylist',playlists[52].id);assert.equal(await page.evaluate(()=>state.uploads[0].targetPlaylistID),playlists[52].id);
 await page.goto(base+'/#/playlists/'+playlists[0].id);await page.waitForFunction(()=>state.playlistDetailId!==null);await page.evaluate(()=>applyTrackFilter({version:1,q:'First',range:'all'}));await page.waitForFunction(()=>state.page==='tracks'&&location.hash.startsWith('#/library'));assert.equal(new URLSearchParams((await page.evaluate(()=>location.hash)).split('?')[1]).get('q'),'First');
 const member=await page.evaluate(()=>api('admin/users','POST',{username:'audit-resource-tabs',quota_bytes:0,email_verification_required:false}));
 await page.goto(base+'/#/admin/users/'+member.user.id+'?kind=keys');await page.waitForFunction(()=>state.userResourceKind==='keys');await page.locator('[data-resource-kind="owned"]').waitFor();
 let release,started;const gate=new Promise(r=>release=r),seen=new Promise(r=>started=r);
 await page.route('**/api/v2/admin/users/'+member.user.id+'/tracks?**',async route=>{started();await gate;await route.fulfill({status:200,contentType:'application/json',body:JSON.stringify({items:[{id:'stale-track',title:'Stale track',version:1}],total:1})})});
 await page.locator('[data-resource-kind="owned"]').click();await seen;await page.locator('[data-resource-kind="keys"]').click();await page.waitForFunction(()=>document.querySelector('#userResourceKind').value==='keys');assert.equal(await page.locator('[data-resource-id="stale-track"]').count(),0);release();await page.waitForFunction(()=>state.userResourceKind==='keys');assert.equal(await page.locator('[data-resource-id="stale-track"]').count(),0);await page.unroute('**/api/v2/admin/users/'+member.user.id+'/tracks?**');
 assert.deepEqual(errors,[]);console.log('Audit frontend UI PASS: target catalogue outside current view page, retained selection, cross-page tag filtering and stale resource-kind isolation');
};
