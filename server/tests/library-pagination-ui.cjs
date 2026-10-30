'use strict';

const assert=require('node:assert/strict');
const path=require('node:path');

// Run before browse-ui's original regression: that regression deliberately
// downlists one fixture, whereas this suite keeps all 102 rows unchanged.
module.exports=async function libraryPaginationUI({page,directory}){
 const errors=[],onError=error=>errors.push(error.message);page.on('pageerror',onError);
 const top=page.locator('#libraryPagerTop'),bottom=page.locator('#browseTrackPager');
 const at=async(offset,size,total=102)=>{
  await page.waitForFunction(({offset,size,total})=>{
   const p=browseState.tracks;
   return state.page==='tracks'&&p?.offset===offset&&p.limit===size&&p.total===total&&document.querySelectorAll('[data-library-track]').length===Math.min(size,Math.max(0,total-offset));
  },{offset,size,total});
 };
 const expectSizes=async size=>{assert.equal(await top.locator('[data-page-size]').inputValue(),String(size));assert.equal(await bottom.locator('[data-page-size]').inputValue(),String(size))};
 const current=async number=>{assert.equal(await top.locator('[aria-current="page"]').textContent(),String(number));assert.equal(await bottom.locator('[aria-current="page"]').textContent(),String(number))};
 const go=async hash=>{await page.evaluate(hash=>{location.hash=hash},hash)};
 try{
  await go('#/library?page_size=50');await at(0,50);await top.waitFor();await bottom.waitFor();
  await expectSizes(50);await current(1);
  assert.equal(await top.locator('[data-page-size] option').allTextContents().then(items=>items.map(text=>Number(text.match(/\d+/)?.[0]))).then(items=>JSON.stringify(items)),JSON.stringify([20,30,50]));
  assert.equal(await top.locator('[data-page-action="previous"]').isDisabled(),true);
  assert.equal(await bottom.locator('[data-page-action="next"]').isEnabled(),true);

  await top.locator('[data-page-size]').selectOption('20');await at(0,20);await expectSizes(20);
  await page.locator('#selectAll').check();assert.equal(await page.evaluate(()=>state.selectedTrackIds.size),20);
  await bottom.locator('[data-page-action="next"]').click();await at(20,20);await current(2);
  assert.equal(await page.evaluate(()=>state.selectedTrackIds.size),0,'Turning the page must clear only-page selections');
  assert.equal(await page.evaluate(()=>new URLSearchParams(location.hash.split('?')[1]).get('page')),'2');
  const tableTop=await page.locator('#trackRows').evaluate(node=>node.closest('table').getBoundingClientRect().top);
  assert.ok(tableTop>=-2&&tableTop<500,'Bottom pagination should reveal the table header on the new page');

  await bottom.locator('[data-page-size]').selectOption('30');await at(0,30);await expectSizes(30);await current(1);
  await top.locator('[data-page-action="next"]').click();await at(30,30);await current(2);
  await bottom.locator('[data-page-jump]').fill('4');await bottom.locator('[data-page-jump]').press('Enter');await at(90,30);await current(4);
  assert.equal(await top.locator('[data-page-action="next"]').isDisabled(),true);
  assert.equal(await page.locator('[data-library-track]').count(),12);
  await page.goBack();await at(30,30);await current(2);
  await page.goForward();await at(90,30);await current(4);
  await page.reload();await at(90,30);await expectSizes(30);await current(4);

  // An old offset URL remains usable but is normalized to page-based routing.
  await go('#/library?offset=60&page_size=30');await at(60,30);await current(3);
  assert.equal(await page.evaluate(()=>new URLSearchParams(location.hash.split('?')[1]).has('offset')),false);
  await go('#/library?page=999&page_size=30');await at(90,30);await current(4);
  assert.equal(await page.evaluate(()=>new URLSearchParams(location.hash.split('?')[1]).get('page')),'4');

  // Verify the actual compound-query path, preserving all route inputs.
  const filter={version:1,formats:['wav']};
  const query=new URLSearchParams({q:'Browse',page_size:'20',sort:'title',dir:'desc',tf:JSON.stringify(filter)});
  await go('#/library?'+query);await at(0,20);
  await top.locator('[data-page-action="next"]').click();await at(20,20);
  const route=await page.evaluate(()=>Object.fromEntries(new URLSearchParams(location.hash.split('?')[1])));
  assert.equal(route.q,'Browse');assert.equal(route.dir,'desc');assert.deepEqual(JSON.parse(route.tf),filter);
  assert.equal(route.page_size,'20');assert.equal(route.page,'2');
  await page.fill('#trackSearch','no-pagination-match');await at(0,20,0);
  assert.equal(await top.locator('[data-page-action="previous"]').isDisabled(),true);
  assert.equal(await bottom.locator('[data-page-action="next"]').isDisabled(),true);
  assert.equal(await top.locator('[data-page-size]').isVisible(),true,'Empty results must retain page size choice');
  assert.equal(await bottom.locator('[data-page-jump]').count(),1);
  assert.equal(await top.locator('[data-page-number="0"]').count(),0,'An empty result must not invent page zero');

  await go('#/library?page_size=20');await at(0,20);
  await page.setViewportSize({width:390,height:844});
  await page.waitForFunction(()=>document.querySelector('.sidebar').getBoundingClientRect().right<=1);
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'Pagination must fit a phone viewport');
  await page.screenshot({path:path.join(directory,'library-pagination-mobile.png')});
  await bottom.locator('[data-page-action="next"]').click();await at(20,20);await expectSizes(20);

  // Save the dropdown preference, then exercise a route without an explicit size.
  await top.locator('[data-page-size]').selectOption('30');await at(0,30);
  await go('#/library');await at(0,30);await expectSizes(30);
  await page.setViewportSize({width:1440,height:1100});
  await top.locator('[data-page-size]').selectOption('50');await at(0,50);await expectSizes(50);
  await go('#/library');await at(0,50);
  // A transient page failure exposes retry without resetting the requested page.
  let failOnce=true;
  await page.route('**/api/v2/track-query',async route=>{if(failOnce&&route.request().postDataJSON().offset===50){failOnce=false;await route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:{code:'ISOLATED_FAILURE',message:'isolated page failure'}})})}else await route.continue()});
  await top.locator('[data-page-action="next"]').click();await top.getByRole('button',{name:'重试本页',exact:true}).waitFor();await top.getByRole('button',{name:'重试本页',exact:true}).click();await at(50,50);await current(2);await page.unroute('**/api/v2/track-query');
  // A new search must be able to finish while the superseded page request waits.
  let releasePage,pageStarted,pageReleased;const held=new Promise(resolve=>releasePage=resolve),started=new Promise(resolve=>pageStarted=resolve),settled=new Promise(resolve=>pageReleased=resolve);
  await page.route('**/api/v2/track-query',async route=>{const delayed=route.request().postDataJSON().offset===100;try{if(delayed){pageStarted();await held}await route.continue()}catch(error){if(!delayed||!route.request().failure())throw error}finally{if(delayed)pageReleased()}});
  await top.locator('[data-page-action="next"]').click();await started;
  try{await page.fill('#trackSearch','Browse 100');await at(0,50,1);assert.equal(await top.locator('[data-page-size]').isEnabled(),true,'New search controls must not wait for the obsolete page request')}finally{releasePage()}
  await settled;await page.unroute('**/api/v2/track-query');await go('#/library?page_size=50');await at(0,50);
  // Removing the final page's two tracks must fall back to page 2, not page 1.
  await go('#/library?page=3&page_size=50');await at(100,50);
  const removed=await page.evaluate(async()=>{const tracks=browseState.tracks.items.slice();for(const t of tracks)await api('tracks/'+t.id,'DELETE',undefined,undefined,t.version);await refreshCore();return tracks.map(t=>t.id)});
  await at(50,50,100);await current(2);
  await page.evaluate(async ids=>{const removed=await api('admin/removed');for(const id of ids){const t=removed.find(t=>t.id===id);await api('admin/removed/'+id,'POST',undefined,undefined,t.version)}await refreshCore()},removed);
  await go('#/library?page_size=50');await at(0,50);
  assert.deepEqual(errors,[]);
  console.log('Library pagination UI PASS: synchronized page numbers, 20/30/50, selection boundaries, history/reload, old URLs, overflow clamp, compound query, empty results, phone layout and saved preference.');
 }finally{page.off('pageerror',onError)}
};
