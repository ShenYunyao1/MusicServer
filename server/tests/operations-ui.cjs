const assert=require('node:assert/strict');
module.exports=async({page,audio})=>{
 await page.click('[data-page="upload"]');await page.waitForFunction(()=>state.page==='upload');
 const data=Buffer.alloc(256*1024);audio.copy(data);data.writeUInt32LE(data.length-8,4);data.writeUInt32LE(data.length-44,40);data[data.length-1]=9;
 await page.setInputFiles('#uploadInput',{name:'Progress.wav',mimeType:'audio/wav',buffer:data});
 await page.waitForFunction(()=>state.uploads.length===1);
 const session=await page.context().newCDPSession(page);await session.send('Network.enable');
 await session.send('Network.emulateNetworkConditions',{offline:false,latency:200,downloadThroughput:-1,uploadThroughput:64*1024});
 await page.evaluate(()=>{window.metadataBarrier=scheduleRequest(()=>new Promise(resolve=>{window.releaseMetadataBarrier=resolve}))});
 try{
  await page.locator('[data-stage-upload]').click();
  await page.waitForFunction(()=>{const p=state.uploads[0]?.progress;return p?.phase==='sending'&&p.loaded>0&&p.loaded<p.total});
  assert.match(await page.locator('[data-upload-progress]').textContent(),/%.*\/s/);
  await page.waitForFunction(()=>state.uploads[0]?.progress?.phase==='waiting');
  assert.match(await page.locator('[data-upload-progress]').textContent(),/文件已发送/);
  assert.equal(await page.locator('[data-upload-progress] progress[value]').count(),0,'Validation must not invent a percentage');
  await page.waitForFunction(()=>state.uploads[0]?.stageId&&state.uploads[0].status==='待确认');
  assert.equal(await page.evaluate(()=>state.uploads[0].progress.phase),'complete');
  assert.equal(await page.evaluate(()=>typeof state.uploads[0].preview.timings.probe_ms),'number');
 }finally{
  await page.evaluate(()=>{window.releaseMetadataBarrier?.();return window.metadataBarrier});
  await session.send('Network.emulateNetworkConditions',{offline:false,latency:0,downloadThroughput:-1,uploadThroughput:-1});await session.detach();
 }
 await page.evaluate(()=>clearUploads());
 const png=Buffer.from(await page.evaluate(()=>{const canvas=document.createElement('canvas');canvas.width=canvas.height=2;canvas.getContext('2d').fillRect(0,0,2,2);return canvas.toDataURL().split(',')[1]}),'base64');
 let audioPosts=0,coverPuts=0;const count=r=>{if(r.url().endsWith('/api/v2/uploads/stage')&&r.method()==='POST')audioPosts++;if(r.url().includes('/api/v2/uploads/stage/')&&r.url().endsWith('/cover')&&r.method()==='PUT')coverPuts++};page.on('request',count);
 await page.setInputFiles('#uploadInput',[{name:'Tagged.wav',mimeType:'audio/wav',buffer:audio},{name:'AlbumCover - Remote Artist - Remote Album.png',mimeType:'image/png',buffer:png}]);
 await page.evaluate(async()=>{await Promise.all(state.uploads.map(x=>x.localPromise));const upload=state.uploads[0];upload.edits.artist='Remote Artist';upload.edits.album='Remote Album'});
 await page.locator('[data-stage-upload]').click();await page.waitForFunction(()=>state.uploads[0]?.status==='待确认');
 assert.equal(audioPosts,1,'Late metadata cover matching must not upload the audio again');assert.equal(coverPuts,1);assert.equal(await page.evaluate(()=>state.uploads[0].preview.track.cover_source),'external');page.off('request',count);
 await page.evaluate(()=>clearUploads());
 await page.click('[data-page="logs"]');await page.waitForFunction(()=>state.page==='logs');
 await page.selectOption('#logCategory','upload');
 await page.waitForFunction(()=>document.querySelector('#systemLogRows').textContent.includes('校验完成'));
 assert.match(await page.locator('#systemLogRows').textContent(),/接收.*ms.*音频探测.*ms|音频探测.*ms.*接收.*ms/s);
 await page.click('#pauseSystemLogs');assert.equal(await page.evaluate(()=>systemLogs.paused),true);
 await page.setViewportSize({width:390,height:844});await page.waitForFunction(()=>document.querySelector('.main').getBoundingClientRect().left<=1&&document.querySelector('#sidebar').getBoundingClientRect().right<=1);assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
 await page.setViewportSize({width:1440,height:1100});
 await page.click('[data-page="overview"]');await page.waitForFunction(()=>state.page==='overview');
 console.log('Operations UI passed: actual throttled upload bytes/rate, separate validation wait, independent request queue, server timings, admin journal/filter/pause and mobile layout.');
};
