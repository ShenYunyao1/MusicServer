const assert=require('node:assert/strict');

module.exports=async function({page,browser}){
  await page.evaluate(()=>location.hash='#/playlists');await page.locator('.playlist-card').first().waitFor();
  assert.equal(await page.locator('.playlist-card button, .playlist-card select').count(),0,'Playlist items should open as a whole without redundant controls');
  const card=page.locator('.playlist-card').first(),id=await card.getAttribute('data-id');
  await card.click();await page.waitForFunction(id=>state.playlistDetailId===id,id);
  const back=await page.locator('[data-back-playlists]').boundingBox(),title=await page.locator('#playlistDetail h1').boundingBox();
  assert.ok(back.y<title.y,'Back belongs above the detail heading at the left');
  await page.click('[data-back-playlists]');await page.locator('.playlist-card').first().focus();await page.keyboard.press('Enter');
  await page.waitForFunction(id=>state.playlistDetailId===id,id);await page.click('[data-back-playlists]');
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
};
