'use strict';
const assert=require('node:assert/strict');
const {chromium}=require(process.env.MUSIC_SERVER_PLAYWRIGHT||'playwright');
async function main(){
 const base=new URL(process.env.MUSIC_SERVER_PUBLIC_DETAIL_URL);assert.ok(['127.0.0.1','localhost','[::1]'].includes(base.hostname));
 const browser=await chromium.launch({headless:true,executablePath:process.env.MUSIC_SERVER_BROWSER}),context=await browser.newContext(),page=await context.newPage(),errors=[];
 page.on('pageerror',error=>errors.push(error.message));
 try{
  await page.goto(new URL('/#/public/playlists/public-page',base).href);await page.fill('#username','public-page-reader');await page.fill('#password','personal password for public-page-reader');await page.click('#loginButton');
  await page.waitForFunction(()=>state.publicDetail?.tracks?.length===50&&document.querySelector('#publicMemberPager'));
  assert.equal(await page.locator('#publicDetail .playlist-track').count(),50);assert.match(await page.locator('#publicMemberPager').textContent(),/1–50 \/ 123/);
  assert.equal(await page.locator('#publicDetail .track-title').first().textContent(),'Page 000');
  await page.locator('#publicMemberPager button').last().click();
  await page.waitForFunction(()=>location.hash.includes('offset=50')&&state.publicDetail?.offset===50);
  assert.equal(await page.locator('#publicDetail .track-title').first().textContent(),'Page 050');
  await page.waitForFunction(()=>communityLive.view?.public_offset===50&&!communityLive.busy);
  await page.locator('#publicPlaylistTrackSearch').fill('Page 080');
  await page.waitForFunction(()=>state.publicDetail?.total===1&&state.publicDetail?.tracks?.[0]?.title==='Page 080');
  assert.equal(await page.locator('#publicDetail .playlist-track').count(),1);assert.equal(await page.inputValue('#publicPlaylistTrackSearch'),'Page 080');
  await page.locator('#publicDetail .track-title').click();await page.locator('#trackDetails').waitFor({state:'visible'});await page.waitForFunction(()=>document.querySelector('#trackDetailsBody').textContent.includes('full private lyrics'));await page.locator('#trackDetails .modal-head button').click();
  await page.locator('#publicPlaylistTrackSearch').fill('author-secret-note');await page.waitForFunction(()=>state.publicDetail?.total===0&&location.hash.includes('author-secret-note'));assert.equal(await page.locator('#publicDetail .playlist-track').count(),0);
  await page.locator('#publicPlaylistTrackSearch').fill('');await page.waitForFunction(()=>state.publicDetail?.total===123&&state.publicDetail.offset===0);
  const catalog=await page.evaluate(async()=>api('browse/playback/public-page?source=public&offset=100&limit=50'));assert.equal(catalog.total,123);assert.equal(catalog.items.length,23,'Playback catalog retains the full saved order independent of current public page');
  await page.setViewportSize({width:390,height:844});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
  assert.deepEqual(errors,[]);console.log('Public detail live browser PASS: paging, server search, current-page realtime binding, lazy full details, note privacy, complete playback catalog and mobile width.');
 }finally{await browser.close()}
}
main().catch(error=>{console.error(error);process.exitCode=1});
