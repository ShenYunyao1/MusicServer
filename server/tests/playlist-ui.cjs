const assert=require('node:assert/strict');

module.exports=async function({page,browser,directory}){
  await page.evaluate(()=>location.hash='#/playlists');await page.locator('.playlist-card').first().waitFor();
  assert.equal(await page.locator('.playlist-card button, .playlist-card select').count(),0,'Playlist items should open as a whole without redundant controls');
  const card=page.locator('.playlist-card').first(),id=await card.getAttribute('data-id');
  await card.click();await page.waitForFunction(id=>state.playlistDetailId===id,id);
  const back=await page.locator('[data-back-playlists]').boundingBox(),title=await page.locator('#playlistDetail h1').boundingBox();
  assert.ok(back.y<title.y,'Back belongs above the detail heading at the left');
  await page.click('[data-back-playlists]');
  await page.locator('.playlist-card').first().waitFor({state:'visible'});
  await page.locator('.playlist-card').first().focus();await page.keyboard.press('Enter');
  await page.waitForFunction(id=>state.playlistDetailId===id,id);await page.click('[data-back-playlists]');
  await page.locator('.playlist-card').first().waitFor({state:'visible'});
  const fixture=await page.evaluate(async()=>{const p=await api('playlists','POST',{id:browserUUID(),name:'Sort fixture',track_ids:[]});await refreshCore();return p});
  const before=await page.evaluate(()=>state.playlists.filter(p=>!p.system).map(p=>p.id));
  const source=await page.locator('.playlist-card').first().boundingBox(),target=await page.locator('.playlist-card').last().boundingBox();
  await page.mouse.move(source.x+100,source.y+25);await page.mouse.down();await page.mouse.move(target.x+100,target.y+target.height-5,{steps:12});await page.mouse.up();
  await page.waitForFunction(first=>state.playlists.filter(p=>!p.system)[0].id!==first,before[0]);
  assert.equal(await page.evaluate(()=>state.playlistDetailId),null,'Dragging must not open the playlist');
  await page.fill('#playlistSearch','Browser');await page.locator('.playlist-card').first().focus();await page.keyboard.press('Shift+F10');await page.locator('#contextMenu').waitFor({state:'visible'});await page.keyboard.press('Escape');
  await page.fill('#playlistSearch','');
  const touch=await browser.newContext({viewport:{width:390,height:844},hasTouch:true,isMobile:true,storageState:await page.context().storageState()});
  try{
    const mobile=await touch.newPage();await mobile.goto(page.url());await mobile.waitForFunction(()=>state.user&&document.querySelectorAll('.playlist-card').length===2);
    const order=await mobile.evaluate(()=>state.playlists.filter(p=>!p.system).map(p=>p.id)),first=await mobile.locator('.playlist-card').first().boundingBox(),last=await mobile.locator('.playlist-card').last().boundingBox();
    const cdp=await touch.newCDPSession(mobile),point={x:Math.round(first.x+80),y:Math.round(first.y+25)};
    await cdp.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[point]});await mobile.waitForTimeout(400);
    await mobile.locator('.drag-ghost').waitFor({state:'visible'});
    await cdp.send('Input.dispatchTouchEvent',{type:'touchMove',touchPoints:[{x:point.x,y:Math.round(last.y+last.height-5)}]});
    await cdp.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});
    await mobile.waitForFunction(first=>state.playlists.filter(p=>!p.system)[0].id!==first,order[0]);
    assert.equal(await mobile.evaluate(()=>state.playlistDetailId),null);
  }finally{await touch.close()}
  await page.evaluate(async id=>{const p=await api('playlists/'+id);await api('playlists/'+id,'DELETE',undefined,undefined,p.playlist.version);await refreshCore()},fixture.id);
  console.log('Playlist items passed: whole-item/keyboard open, mouse/touch sorting, no accidental navigation, context menu and upper-left back.');
  await page.locator('.playlist-card').first().click();await page.click('[data-edit-playlist]');
  assert.ok((await page.evaluate(()=>location.hash)).endsWith('/edit'),'Editing should open its own page');
  await page.locator('#playlistEditName').waitFor();
  await require('./page-headings-ui.cjs').assertHeading(page,'编辑歌单');
  const original=await page.evaluate(()=>({...playlistDraft.original}));
  const images=await page.evaluate(()=>{const c=document.createElement('canvas');c.width=600;c.height=300;const ctx=c.getContext('2d');ctx.fillStyle='red';ctx.fillRect(0,0,300,300);ctx.fillStyle='blue';ctx.fillRect(300,0,300,300);return ['image/png','image/jpeg','image/webp'].map(type=>c.toDataURL(type).split(',')[1])});
  await page.locator('#playlistCoverFile').setInputFiles({name:'fake.png',mimeType:'image/png',buffer:Buffer.from('<svg/>')});
  await page.waitForFunction(()=>document.querySelector('#playlistEditError').textContent.includes('仅支持'));
  await page.locator('#playlistCoverFile').setInputFiles({name:'too-large.png',mimeType:'image/png',buffer:Buffer.alloc(8*1024*1024+1)});
  await page.waitForFunction(()=>document.querySelector('#playlistEditError').textContent.includes('8 MiB'));
  for(const [index,extension] of ['png','jpg','webp'].entries()){
    await page.locator('#playlistCoverFile').setInputFiles({name:'cover.'+extension,mimeType:['image/png','image/jpeg','image/webp'][index],buffer:Buffer.from(images[index],'base64')});
    await page.locator('#playlistCrop').waitFor({state:'visible'});await page.click('#cancelPlaylistCrop');
  }
  await page.fill('#playlistEditDescription','Unsaved draft');
  await page.locator('#playlistCoverFile').setInputFiles({name:'cover.png',mimeType:'image/png',buffer:Buffer.from(images[0],'base64')});
  await page.locator('#playlistCrop').waitFor({state:'visible'});
  await page.locator('#playlistCropZoom').evaluate(el=>{el.value='2';el.dispatchEvent(new Event('input',{bubbles:true}))});
  const crop=await page.locator('#playlistCropCanvas').boundingBox();await page.mouse.move(crop.x+crop.width/2,crop.y+crop.height/2);await page.mouse.down();await page.mouse.move(crop.x+20,crop.y+crop.height/2,{steps:10});await page.mouse.up();
  await page.screenshot({path:require('node:path').join(directory,'playlist-crop.png'),animations:'disabled'});
  await page.click('#applyPlaylistCrop');await page.locator('#playlistCrop').waitFor({state:'hidden'});
  const preview=await page.evaluate(async()=>{const bitmap=await createImageBitmap(playlistDraft.coverBlob),c=document.createElement('canvas');c.width=c.height=1;const ctx=c.getContext('2d');ctx.drawImage(bitmap,0,0,1,1);return {width:bitmap.width,height:bitmap.height,pixel:[...ctx.getImageData(0,0,1,1).data]}});
  assert.equal(preview.width,preview.height);assert.ok(preview.pixel[2]>preview.pixel[0],'Dragging the crop must change the selected source region');
  assert.equal(await page.evaluate(async id=>(await api('playlists/'+id)).playlist.version,original.id),original.version,'Preview must not persist anything');
  page.once('dialog',dialog=>dialog.dismiss());await page.click('#playlistEditBack');assert.ok((await page.evaluate(()=>location.hash)).endsWith('/edit'),'Declining discard must keep the draft');
  await page.click('#savePlaylistEdit');await page.waitForFunction(()=>!playlistDraft&&state.playlists.some(p=>p.cover_id));
  const saved=await page.evaluate(id=>state.playlists.find(p=>p.id===id),original.id);assert.equal(saved.version,original.version+1);assert.equal(saved.description,'Unsaved draft');
  await page.reload();await page.waitForFunction(()=>state.user&&state.playlistDetailId);await page.click('[data-edit-playlist]');await page.locator('#playlistEditName').waitFor();
  await page.reload();await page.waitForFunction(()=>playlistDraft&&state.user);await require('./page-headings-ui.cjs').assertHeading(page,'编辑歌单');
  await page.setViewportSize({width:390,height:844});await page.waitForFunction(()=>document.querySelector('.main').getBoundingClientRect().left<=1&&document.querySelector('#sidebar').getBoundingClientRect().right<=1);assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'The editor must fit mobile width');
  await page.screenshot({path:require('node:path').join(directory,'playlist-editor-mobile.png'),fullPage:true,animations:'disabled'});await page.setViewportSize({width:1440,height:1100});await page.waitForFunction(()=>document.querySelector('.main').getBoundingClientRect().left>=231);
  await page.evaluate(async()=>{await api('playlists/'+playlistDraft.id,'PATCH',{description:'Concurrent description'},undefined,playlistDraft.original.version)});
  await page.fill('#playlistEditDescription','Keep failed draft');await page.click('#savePlaylistEdit');await page.waitForFunction(()=>document.querySelector('#playlistEditError').textContent.includes('409'));
  assert.equal(await page.locator('#playlistEditDescription').inputValue(),'Keep failed draft');
  page.once('dialog',dialog=>dialog.accept());await page.click('#reloadPlaylistDraft');await page.waitForFunction(()=>document.querySelector('#playlistEditDescription').value==='Concurrent description');
  await page.fill('#playlistEditName','Discarded name');page.once('dialog',dialog=>dialog.accept());await page.click('#playlistEditBack');await page.waitForFunction(()=>!playlistDraft);
  assert.equal(await page.evaluate(async id=>(await api('playlists/'+id)).playlist.name,original.id),original.name);
  await page.click('[data-back-playlists]');await page.locator('.playlist-card .cover img').waitFor();
  console.log('Playlist editor passed: independent route, PNG/JPEG/WebP, limits, manual square crop, preview without persistence, atomic save, reload, conflict/draft retention and discard.');
  await page.locator('.playlist-card').first().click();
  assert.equal(await page.locator('.playlist-track .track-cover').count(),1,'Playlist tracks should share the catalogue cover/information layout');
};

module.exports.tracks=async function({page,browser,directory}){
  const fixture=await page.evaluate(async()=>{
    const p=state.playlists.find(p=>p.name==='Browser playlist'),extra=state.tracks.find(t=>!p.track_ids.includes(t.id));
    await api('playlists/'+p.id+'/tracks','POST',{track_ids:[extra.id]},undefined,p.version);
    const c=document.createElement('canvas');c.width=c.height=16;c.getContext('2d').fillRect(0,0,16,16);const blob=await new Promise(resolve=>c.toBlob(resolve));const f=new FormData();f.append('cover',blob,'track.png');const track=state.tracks.find(t=>t.id===p.track_ids[0]);await api('tracks/'+track.id+'/cover','PUT',f,undefined,track.version);await refreshCore();location.hash='#/playlists/'+p.id;return {id:p.id,extra:extra.id,first:track.id};
  });
  await page.waitForFunction(()=>document.querySelectorAll('.playlist-track').length===2);await page.locator('.playlist-track .track-cover img').waitFor();
  assert.deepEqual(await page.locator('.playlist-sequence').allTextContents(),['1','2']);
  await page.locator('[data-playlist-track-details]').first().click();await page.locator('#trackDetails').waitFor({state:'visible'});assert.ok((await page.locator('.playlist-track-personal').textContent()).includes('Browser cue'));
  await page.locator('.playlist-track-personal button').filter({hasText:'个人备注'}).click();assert.equal(await page.locator('[data-field="comment"]').inputValue(),'Browser cue');await page.click('#saveModal');await page.locator('#editor').waitFor({state:'hidden'});
  for(const width of [901,960,1440]){await page.setViewportSize({width,height:1100});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'Track columns must fit desktop width '+width)}
  async function mouseSort(){const first=await page.locator('.playlist-sequence').first().boundingBox(),last=await page.locator('.playlist-track').last().boundingBox();await page.mouse.move(first.x+first.width/2,first.y+first.height/2);await page.mouse.down();await page.waitForTimeout(400);await page.locator('.drag-ghost').waitFor({state:'visible'});await page.mouse.move(last.x+20,last.y+last.height-3,{steps:12});await page.mouse.up()}
  await mouseSort();await page.waitForFunction(({id,extra})=>state.playlists.find(p=>p.id===id).track_ids[0]===extra&&!state.playlistTrackSortBusy,fixture);
  assert.equal(await page.evaluate(id=>state.playlists.find(p=>p.id===id).track_ids[0],fixture.id),fixture.extra);
  await page.locator('.playlist-track').first().focus();await page.keyboard.press('Shift+F10');await page.locator('#contextMenu').getByText('下移曲目',{exact:true}).click();
  await page.waitForFunction(({id,first})=>state.playlists.find(p=>p.id===id).track_ids[0]===first&&!state.playlistTrackSortBusy,fixture);
  await page.evaluate(async id=>{const p=state.playlists.find(p=>p.id===id);await api('playlists/'+id,'PATCH',{description:'Concurrent order fixture'},undefined,p.version)},fixture.id);
  const conflict=page.waitForResponse(r=>r.url().endsWith('/tracks/order')&&r.status()===409);await mouseSort();await conflict;await page.waitForFunction(()=>!state.playlistTrackSortBusy);assert.equal(await page.evaluate(id=>state.playlists.find(p=>p.id===id).track_ids[0],fixture.id),fixture.first);
  await page.screenshot({path:require('node:path').join(directory,'playlist-tracks-desktop.png'),fullPage:true,animations:'disabled'});
  const context=await browser.newContext({viewport:{width:390,height:500},isMobile:true,hasTouch:true,storageState:await page.context().storageState()});
  try{
    const mobile=await context.newPage();await mobile.goto(page.url());await mobile.waitForFunction(()=>state.user&&document.querySelectorAll('.playlist-track').length===2);const cdp=await context.newCDPSession(mobile);
    assert.equal(await mobile.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'Playlist rows must fit mobile width');
    const start=await mobile.locator('.playlist-sequence').first().boundingBox(),point={x:Math.round(start.x+10),y:Math.round(start.y+5)};
    await cdp.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[point]});for(let i=1;i<=6;i++){await cdp.send('Input.dispatchTouchEvent',{type:'touchMove',touchPoints:[{x:point.x,y:point.y-i*18}]});await mobile.waitForTimeout(20)}await cdp.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});
    assert.equal(await mobile.locator('.drag-ghost').count(),0,'Ordinary swiping must not start sorting');assert.ok(await mobile.evaluate(()=>scrollY>0),'Ordinary touch motion should scroll the page');
    await mobile.evaluate(()=>scrollTo(0,0));const first=await mobile.locator('.playlist-sequence').first().boundingBox(),last=await mobile.locator('.playlist-track').last().boundingBox();
    await cdp.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[{x:Math.round(first.x+10),y:Math.round(first.y+5)}]});await mobile.waitForTimeout(400);await mobile.locator('.drag-ghost').waitFor({state:'visible'});
    await cdp.send('Input.dispatchTouchEvent',{type:'touchMove',touchPoints:[{x:Math.round(last.x+20),y:490}]});await mobile.waitForTimeout(150);await cdp.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});
    await mobile.waitForFunction(({id,extra})=>state.playlists.find(p=>p.id===id).track_ids[0]===extra&&!state.playlistTrackSortBusy,fixture);
    assert.ok(await mobile.evaluate(()=>scrollY>0),'Dragging near the viewport edge should scroll');
    await mobile.evaluate(()=>scrollTo(0,0));await mobile.screenshot({path:require('node:path').join(directory,'playlist-tracks-mobile.png'),fullPage:true,animations:'disabled'});
  }finally{await context.close()}
  await page.evaluate(()=>refreshCore());await page.locator('.playlist-track[data-track-id="'+fixture.extra+'"] [data-playlist-track-details]').click();page.once('dialog',dialog=>dialog.accept());await page.locator('.playlist-track-personal button').filter({hasText:'移出歌单'}).click();await page.waitForFunction(()=>document.querySelectorAll('.playlist-track').length===1);
  assert.ok((await page.locator('#playlistDetail').textContent()).includes('Browser cue'));
  console.log('Playlist tracks passed: artwork/info/notes, mouse long press, keyboard sorting, stale-order rejection, real touch scrolling/sorting, mobile layout and remove via details.');
};
