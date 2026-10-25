const assert=require('node:assert/strict');
const path=require('node:path');

module.exports=async function({page,directory}){
 const logo=()=>page.locator('.brand-mark').boundingBox();
 const logoLeft=(await logo()).x;
 const iconLeft=(await page.locator('.nav-icon').first().boundingBox()).x;
 const starting=await page.evaluate(()=>{document.querySelector('#brandToggle').click();return {logo:document.querySelector('.brand-mark').getBoundingClientRect().left,icon:document.querySelector('.nav-icon').getBoundingClientRect().left}});
 assert.equal(starting.logo,logoLeft,'Starting the collapse animation must not make the logo jump sideways');
 assert.ok(Math.abs(starting.icon-iconLeft)<=4,'Navigation icons must not jump to the middle of the expanding sidebar');
 await page.waitForFunction(()=>document.querySelector('#sidebar').getBoundingClientRect().width<=73);
 const collapsedLogo=await logo();
 assert.equal(Math.round(collapsedLogo.width),32,'Collapsed sidebar must not squeeze the logo');
 assert.equal(Math.round(collapsedLogo.height),32,'Logo must remain square');
 assert.equal(await page.locator('.brand #brandToggle').count(),1,'The logo owns the navigation toggle');
 assert.equal(await page.locator('.topbar #brandToggle, #collapse').count(),0,'There must be no separate collapse control');
 const target=await page.locator('#brandToggle').boundingBox();assert.equal(target.width,44);assert.equal(target.height,44);
 assert.equal(await page.locator('#brandToggle').getAttribute('aria-expanded'),'false');
 assert.equal(await page.locator('.nav').evaluate(el=>el.scrollWidth<=el.clientWidth),true,'Collapsed icons must not create horizontal scrolling');
 assert.equal(await page.locator('.nav .nav-icon svg').count(),await page.locator('.nav .nav-icon').count(),'All navigation icons must use the same SVG system');
 await page.reload();await page.waitForFunction(()=>state.user&&document.querySelector('#status').textContent==='已登录');
 assert.equal(Math.round((await page.locator('#sidebar').boundingBox()).width),72,'Collapsed preference should survive reload');
 for(let index=0;index<6;index++){
   await page.click('#brandToggle');
   const width=index%2===0?232:72;
   await page.waitForFunction(width=>Math.abs(document.querySelector('#sidebar').getBoundingClientRect().width-width)<1,width);
   assert.equal(Math.round((await logo()).width),32);
 }
 await page.locator('.nav [data-page="tracks"]').focus();await page.locator('#navTooltip').waitFor({state:'visible'});
 assert.equal(await page.locator('#navTooltip').textContent(),'全局曲库');await page.keyboard.press('Tab');
 await page.screenshot({path:path.join(directory,'sidebar-collapsed.png'),fullPage:true});
 await page.click('#brandToggle');await page.waitForFunction(()=>document.querySelector('#sidebar').getBoundingClientRect().width>=231);
 for(const scale of [1,1.25,1.5]){
   // Desktop zoom reduces the available CSS viewport by the same ratio.
   await page.setViewportSize({width:Math.round(1440/scale),height:Math.round(900/scale)});
   await page.waitForFunction(()=>document.querySelector('#sidebar').getBoundingClientRect().width>=231);
   assert.equal(Math.round((await logo()).width),32);
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
   assert.equal(await page.locator('.nav').evaluate(el=>el.scrollHeight<=el.clientHeight+1),true,'Normal-height navigation should fit without scrolling');
 }
 await page.setViewportSize({width:1280,height:360});
 assert.equal(await page.locator('.nav').evaluate(el=>el.scrollHeight>el.clientHeight),true,'Short-window fixture must actually exercise navigation overflow');
 await page.locator('.nav').evaluate(el=>el.scrollTop=el.scrollHeight);
 const visible=await page.locator('.nav [data-page="settings"]').evaluate(el=>{const item=el.getBoundingClientRect(),nav=el.closest('.nav').getBoundingClientRect();return item.top>=nav.top&&item.bottom<=nav.bottom+1});
 assert.equal(visible,true,'Last navigation item must remain reachable in a short window');
 assert.equal(await page.locator('.brand').evaluate(el=>el.getBoundingClientRect().top>=0),true);
 assert.equal(await page.locator('.sidebar-foot').evaluate(el=>el.getBoundingClientRect().bottom<=innerHeight+1),true);
 await page.locator('.nav').hover();await page.screenshot({path:path.join(directory,'sidebar-short.png')});
 await page.click('#brandToggle');await page.waitForFunction(()=>document.querySelector('#sidebar').getBoundingClientRect().width<=73);
 await page.locator('.nav').evaluate(el=>el.scrollTop=0);await page.locator('.nav [data-page="settings"]').focus();
 await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
 assert.equal(await page.locator('#navTooltip').isVisible(),true,'Keyboard focus must retain its label after scrolling to the last item');
 assert.equal(await page.locator('#navTooltip').textContent(),'系统设置');
 await page.setViewportSize({width:390,height:844});
 await page.click('#menuToggle');await page.waitForFunction(()=>document.querySelector('#sidebar').getBoundingClientRect().left>=-1);
 assert.equal(Math.round((await page.locator('#sidebar').boundingBox()).width),232,'Mobile drawer must ignore desktop collapsed width');
 assert.equal(await page.locator('.brand-text').isVisible(),true);assert.equal(await page.locator('#brandToggle').isVisible(),true);
 const preference=await page.evaluate(()=>localStorage.getItem('musicserver.sidebarCollapsed'));
 await page.click('#brandToggle');assert.equal(await page.locator('#menuToggle').getAttribute('aria-expanded'),'false');
 assert.equal(await page.evaluate(()=>localStorage.getItem('musicserver.sidebarCollapsed')),preference,'Closing the mobile drawer must not change desktop preference');
 await page.click('#menuToggle');
 assert.equal(Math.round((await logo()).width),32);
  await page.locator('.nav [data-page="tracks"]').click();
  await page.waitForFunction(()=>state.page==='tracks'&&document.querySelector('#menuToggle').getAttribute('aria-expanded')==='false');
  assert.equal(await page.locator('#menuToggle').getAttribute('aria-expanded'),'false');
 await page.setViewportSize({width:1440,height:1100});await page.waitForFunction(()=>document.querySelector('#sidebar').getBoundingClientRect().width<=73);
 await page.locator('#brandToggle').focus();await page.keyboard.press('Enter');await page.waitForFunction(()=>document.querySelector('.main').getBoundingClientRect().left>=231);
 await page.keyboard.press('Space');await page.waitForFunction(()=>document.querySelector('#sidebar').getBoundingClientRect().width<=73);
 await page.keyboard.press('Space');await page.waitForFunction(()=>document.querySelector('#sidebar').getBoundingClientRect().width>=231);
 await page.locator('.nav').evaluate(el=>el.scrollTop=0);
 await page.click('[data-page="overview"]');await page.waitForFunction(()=>state.page==='overview');
 await page.screenshot({path:path.join(directory,'sidebar-expanded.png'),fullPage:true,animations:'disabled'});
 console.log('Sidebar UI passed: fixed logo, repeated toggle, saved preference, keyboard tooltip, zoom-equivalent viewports, short-window scrolling and independent mobile drawer.');
};
