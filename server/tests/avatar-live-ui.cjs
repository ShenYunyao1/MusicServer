'use strict';
const assert=require('node:assert/strict');
const path=require('node:path');
const fs=require('node:fs');
const {chromium}=require(process.env.MUSIC_SERVER_PLAYWRIGHT||'playwright');
async function main(){
 const base=process.env.MUSIC_SERVER_AVATAR_LIVE_URL;
 assert.ok(base);assert.ok(['127.0.0.1','localhost','[::1]'].includes(new URL(base).hostname));
 const browser=await chromium.launch({headless:true,executablePath:process.env.MUSIC_SERVER_BROWSER});
 const page=await browser.newPage({viewport:{width:1280,height:900}}),errors=[];
 page.on('pageerror',error=>errors.push(error.message));
 const metadata=()=>page.evaluate(()=>api('auth/avatar'));
 const settled=()=>page.waitForFunction(()=>avatarWorkspace.owner===state.user?.id&&!avatarWorkspace.busy&&!avatarWorkspace.loading);
 try{
  await page.goto(base+'/#/account/profile');
  await page.waitForFunction(()=>!document.querySelector('#username').disabled);
  await page.fill('#username','avatar-member');await page.fill('#password','personal password for avatar-member');await page.click('#loginButton');
  await page.locator('#editProfile').click();await page.locator('#avatarInput').waitFor({state:'attached'});await settled();
  assert.equal((await metadata()).has_avatar,false);
  const source=await page.evaluate(()=>{const c=document.createElement('canvas');c.width=480;c.height=320;const x=c.getContext('2d'),g=x.createLinearGradient(0,0,480,320);g.addColorStop(0,'red');g.addColorStop(1,'blue');x.fillStyle=g;x.fillRect(0,0,480,320);return c.toDataURL('image/png').split(',')[1]});
  await page.setInputFiles('#avatarInput',{name:'crop-source.png',mimeType:'image/png',buffer:Buffer.from(source,'base64')});
  await page.waitForFunction(()=>avatarWorkspace.draft==='image');
  assert.equal((await metadata()).has_avatar,false,'Selecting an image remains local until Save');
  const initial=await page.locator('#avatarCanvas').evaluate(c=>c.toDataURL());
  await page.locator('#avatarZoom').focus();await page.locator('#avatarZoom').press('End');
  const box=await page.locator('#avatarCanvas').boundingBox();
  await page.mouse.move(box.x+box.width/2,box.y+box.height/2);await page.mouse.down();await page.mouse.move(box.x+box.width/2+30,box.y+box.height/2);await page.mouse.up();
  assert.notEqual(await page.locator('#avatarCanvas').evaluate(c=>c.toDataURL()),initial,'Zoom and crop positioning affect the local preview');
  await page.click('#saveProfile');await page.waitForFunction(()=>!document.querySelector('#personalEditDialog')&&avatarWorkspace.metadata?.has_avatar);
  const saved=await metadata();assert.equal(saved.has_avatar,true);assert.ok(saved.version>1);
  await page.waitForFunction(()=>[document.querySelector('#accountAvatar img'),document.querySelector('#profileAvatar img')].every(img=>img?.naturalWidth===256));
  const image=await page.request.get(base+saved.url);
  assert.equal(image.status(),200);assert.match(image.headers()['content-type'],/image\/png/);assert.match(image.headers()['cache-control'],/private/);assert.equal(image.headers()['x-content-type-options'],'nosniff');assert.ok(image.headers().etag);
  assert.equal((await page.request.get(base+saved.url,{headers:{'If-None-Match':image.headers().etag}})).status(),304,'Current sessions may revalidate a cached avatar');
  const anonymous=await browser.newContext();
  const denied=await anonymous.request.get(base+saved.url);assert.ok([401,403].includes(denied.status()),'Avatar content requires a session');
  const keyDenied=await anonymous.request.get(base+saved.url,{headers:{'X-API-Key':process.env.MUSIC_SERVER_AVATAR_TEST_KEY}});assert.equal(keyDenied.status(),403,'Player Keys do not expose account avatars');await anonymous.close();
  await page.reload();assert.equal(await page.locator('#avatarInput').count(),0,'Read-only page has no upload draft');await page.locator('#editProfile').click();await page.locator('#avatarInput').waitFor({state:'attached'});await settled();assert.equal((await metadata()).has_avatar,true,'Reload retains the saved avatar');
  await page.click('#resetAvatar');await page.waitForFunction(()=>avatarWorkspace.draft==='remove');
  assert.equal((await metadata()).has_avatar,true,'Restoring default is also a draft until saved');
  await page.setViewportSize({width:390,height:844});
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Avatar controls fit a narrow screen');
  if(process.env.MUSIC_SERVER_AVATAR_SCREENSHOTS){const d=process.env.MUSIC_SERVER_AVATAR_SCREENSHOTS;fs.mkdirSync(d,{recursive:true});await page.screenshot({path:path.join(d,'avatar-mobile.png'),fullPage:true,animations:'disabled'});await page.setViewportSize({width:1280,height:900});await page.screenshot({path:path.join(d,'avatar-desktop.png'),fullPage:true,animations:'disabled'});}
  await page.click('#saveProfile');await page.waitForFunction(()=>!document.querySelector('#personalEditDialog')&&!avatarWorkspace.metadata?.has_avatar);
  const reset=await metadata();assert.equal(reset.has_avatar,false);assert.ok(reset.version>saved.version);
  assert.equal(await page.locator('#profileAvatar img').count(),0);assert.equal(await page.locator('#accountAvatar img').count(),0);
  assert.deepEqual(errors,[]);
  console.log('Live avatar browser passed: real account upload/crop preview/save, joint profile save, modal cleanup, header/profile sync, persistence, private media, Key rejection, explicit default save and narrow layout');
 }finally{await browser.close()}
}
main().catch(error=>{console.error(error);process.exitCode=1});
