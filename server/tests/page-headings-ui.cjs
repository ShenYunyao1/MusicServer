const assert=require('node:assert/strict');

async function assertHeading(page,title){
  const headings=page.locator('.content h1:visible');
  assert.equal(await headings.count(),1,'Each view must have one main title');
  assert.equal(await headings.textContent(),title);
  assert.equal(await page.locator('.topbar h1, .topbar h2').count(),0);
  assert.ok((await page.title()).includes(title),'The browser title must identify the current view');
}

module.exports=async function({page}){
  for(const [route,title] of [
    ['library','全局曲库'],['playlists','我的歌单'],['uploads','上传任务'],
    ['player/keys','播放器接入'],['player/stats','调用统计'],
    ['admin/overview','管理概览'],['admin/users','用户管理'],['admin/stats','调用统计'],
    ...['scan','missing','removed'].map(tab=>['admin/library/'+tab,'曲库维护']),
    ...['general','backup','update','audit'].map(tab=>['admin/settings/'+tab,'系统设置'])
  ]){
    await page.evaluate(route=>{location.hash='#/'+route},route);
    await page.waitForFunction(()=>state.page!=='unavailable'&&document.querySelector('.page.active h1'));
    await page.waitForFunction(title=>document.querySelector('.page.active h1')?.textContent===title,title);
    const tab=route.split('/')[2];
    if(route.startsWith('admin/settings/'))await page.locator('[data-settings-pane="'+tab+'"]:visible').waitFor();
    if(route.startsWith('admin/library/'))await page.locator('[data-maintenance-pane="'+tab+'"]:visible').waitFor();
    await assertHeading(page,title);
  }
  await page.evaluate(()=>location.hash='#/admin/overview');await page.waitForFunction(()=>state.page==='overview');
  assert.equal(Math.round((await page.locator('.topbar').boundingBox()).height),56,'The account bar should be compact');
  console.log('Page headings UI passed: one content title on every route and tab, descriptive browser title and compact topbar.');
};
module.exports.assertHeading=assertHeading;
