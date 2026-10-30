'use strict';
const assert=require('node:assert/strict');
module.exports=async({page,directory})=>{
 const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(new URL('/#/uploads',page.url()).href);await page.locator('#uploadReview').waitFor();
 assert.equal(await page.locator('#uploadReview').isChecked(),false,'Default uploads must automatically commit');
 await page.evaluate(()=>{const bytes=new Uint8Array(844),v=new DataView(bytes.buffer);bytes.set(new TextEncoder().encode('RIFF'));v.setUint32(4,836,true);bytes.set(new TextEncoder().encode('WAVEfmt '),8);v.setUint32(16,16,true);v.setUint16(20,1,true);v.setUint16(22,1,true);v.setUint32(24,8000,true);v.setUint32(28,16000,true);v.setUint16(32,2,true);v.setUint16(34,16,true);bytes.set(new TextEncoder().encode('data'),36);v.setUint32(40,800,true);bytes[843]=23;addFiles([new File([bytes],'automatic.wav',{type:'audio/wav'})])});
 await page.click('#uploadAll');await page.waitForFunction(()=>state.uploads[0]?.status==='完成'&&!state.uploadBusy);
 assert.match(await page.locator('#uploadFlowSummary').textContent(),/新增 1/);
 assert.equal(await page.evaluate(()=>state.uploads[0].stageId),null);
 await page.waitForFunction(()=>uploadHashWorker!==null&&!uploadHashWorkerFailed);
 // Reusing a committed file must preserve its identity and not create a new song.
 const first=await page.evaluate(async()=>{const p=await api('browse/tracks?limit=100');return p.items.find(t=>t.title==='automatic')});
 await page.click('#clearUploads');await page.check('#uploadReview');
 await page.evaluate(async()=>{const response=await fetch('/api/v2/media/'+(await api('browse/tracks?limit=100')).items.find(t=>t.title==='automatic').id);addFiles([new File([await response.blob()],'duplicate.wav',{type:'audio/wav'})])});
 await page.click('#uploadAll');await page.waitForFunction(()=>state.uploads[0]?.status==='待确认');
 assert.equal(await page.evaluate(()=>state.uploads[0].preview.duplicate_of),first.id);
 await page.click('#confirmUploads');await page.waitForFunction(()=>state.uploads[0]?.status==='已去重');
 await page.click('#clearUploads');await page.uncheck('#uploadReview');
 await page.setInputFiles('#uploadInput',{name:'broken.wav',mimeType:'audio/wav',buffer:Buffer.from('not an audio file')});
 await page.click('#uploadAll');await page.waitForFunction(()=>state.uploads[0]?.status==='失败'&&!state.uploadBusy);
 assert.equal(await page.locator('#retryUploads').isEnabled(),true);
 assert.match(await page.locator('#uploadFlowSummary').textContent(),/失败 1/);
 await page.click('#clearUploads');await page.waitForFunction(()=>state.uploads.length===0&&!state.uploadBusy);
 const target=await page.evaluate(async()=>{const p=await api('playlists','POST',{id:browserUUID(),name:'Upload destination',track_ids:[]});await refreshCore();renderUploads();return p});
 await page.selectOption('#uploadTargetPlaylist',target.id);
 await page.fill('#uploadBatchArtist','Batch performer');await page.fill('#uploadBatchAlbum','Batch album');
 // One sender must be able to start the next file while finish is delayed.
 let releaseFinish,finishSeen;const gate=new Promise(r=>releaseFinish=r),seen=new Promise(r=>finishSeen=r);let intercepted=false;
 await page.route('**/uploads/resumable/*/finish',async route=>{if(!intercepted){intercepted=true;finishSeen();await gate}await route.continue()});
 await page.evaluate(()=>{for(let i=0;i<2;i++){const b=new Uint8Array(844),v=new DataView(b.buffer);b.set(new TextEncoder().encode('RIFF'));v.setUint32(4,836,true);b.set(new TextEncoder().encode('WAVEfmt '),8);v.setUint32(16,16,true);v.setUint16(20,1,true);v.setUint16(22,1,true);v.setUint32(24,8000,true);v.setUint32(28,16000,true);v.setUint16(32,2,true);v.setUint16(34,16,true);b.set(new TextEncoder().encode('data'),36);v.setUint32(40,800,true);b[843]=40+i;addFiles([new File([b],'pipeline-'+i+'.wav')])}});
 await page.click('#applyUploadMetadata');
 await page.click('#uploadAll');await seen;
 try{await page.waitForFunction(()=>state.uploads[1]?.progress?.loaded===844)}finally{releaseFinish()}
 await page.waitForFunction(()=>state.uploads.every(u=>u.status==='完成')&&!state.uploadBusy);
 await page.waitForFunction(()=>state.uploads.every(u=>u.playlistAssigned));
 const saved=await page.evaluate(async id=>api('playlists/'+id),target.id);assert.equal(saved.playlist.track_ids.length,2);
 assert.ok(saved.tracks.every(t=>t.artist==='Batch performer'&&t.album==='Batch album'));
 await page.unroute('**/uploads/resumable/*/finish');
 // Drop the commit response after the server has committed. Recover by receipt.
 await page.click('#clearUploads');await page.waitForFunction(()=>state.uploads.length===0&&!state.uploadBusy);
 await page.route('**/uploads/stage/*/commit',async route=>{await route.fetch();await route.abort('failed')});
 await page.route('**/api/v2/playlists/*/tracks',route=>route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:{message:'isolated playlist failure'}})}));
 await page.evaluate(async()=>{const t=(await api('browse/tracks?limit=1')).items[0],b=new Uint8Array(await(await fetch('/api/v2/media/'+t.id)).arrayBuffer());b[b.length-1]=79;addFiles([new File([b],'receipt-recovery.wav')])});
 await page.click('#uploadAll');await page.waitForFunction(()=>state.uploads[0]?.status==='完成'&&state.uploads[0]?.assignmentError);
 const committedID=await page.evaluate(()=>state.uploads[0].commitResult.track.id);
 await page.unroute('**/uploads/stage/*/commit');await page.unroute('**/api/v2/playlists/*/tracks');
 await page.evaluate(()=>persistResumeQueue());await page.reload();await page.waitForFunction(()=>state.uploads[0]?.commitResult?.track?.id);
 assert.equal(await page.evaluate(()=>state.uploads[0].commitResult.track.id),committedID);
 assert.equal(await page.evaluate(()=>state.uploads[0].needsSourceFile),false,'Retrying playlist membership must not require the original audio');
 await page.click('#retryUploads');await page.waitForFunction(()=>state.uploads[0]?.playlistAssigned);
 const afterRecovery=await page.evaluate(async id=>api('playlists/'+id),target.id);assert.ok(afterRecovery.playlist.track_ids.includes(committedID));
 // Pagination must bound actual DOM rows and local metadata concurrency.
 await page.click('#clearUploads');await page.waitForFunction(()=>state.uploads.length===0&&!state.uploadBusy);
 await page.evaluate(()=>addFiles(Array.from({length:120},(_,i)=>new File([new Uint8Array(128)],'queue-'+i+'.wav'))));
 assert.equal(await page.locator('#uploadList .upload-item').count(),50);
 assert.ok(await page.evaluate(()=>uploadParseActive<=2));
 await page.locator('#uploadQueuePager').getByRole('button',{name:'下一页'}).click();assert.match(await page.locator('#uploadQueuePager').textContent(),/51–100/);
 await page.setViewportSize({width:390,height:844});await page.waitForFunction(()=>document.querySelector('.sidebar').getBoundingClientRect().right<=1);await page.evaluate(()=>window.scrollTo(0,0));await page.screenshot({path:require('node:path').join(directory,'upload-flow-mobile.png')});
 assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'Upload controls and queue must fit mobile');
 assert.deepEqual(errors,[]);
 console.log('Upload flow UI passed: default automatic commit, manual review, duplicate identity, failure summary and retry entry.');
};
