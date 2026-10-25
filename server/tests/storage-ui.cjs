const assert=require('node:assert/strict');
const path=require('node:path');
module.exports=async function({page,directory}){
 await page.goto(page.url().split('#')[0]+'#/admin/settings/storage');
 await page.waitForFunction(()=>storageView&&!storageLoading);
 assert.equal(await page.inputValue('#storageReserve'),'512');
 assert.ok(await page.evaluate(()=>storageView.media_bytes>0));
 const admin=await page.evaluate(()=>storageView.accounts.find(a=>a.id===state.user.id));
 assert.equal(admin.limit_bytes,0);
 const row=page.locator('[data-quota-id="'+admin.id+'"]');await row.locator('input').fill('1');
 await row.locator('button').click();await page.waitForFunction(()=>storageView.accounts.find(a=>a.id===state.user.id).version===1);
 await page.fill('#storageReserve','256');await page.click('#saveStoragePolicy');await page.waitForFunction(()=>storageView.policy.reserve_bytes===268435456&&!storageLoading);
 await page.reload();await page.waitForFunction(()=>storageView&&!storageLoading);assert.equal(await row.locator('input').inputValue(),'1');assert.equal(await page.inputValue('#storageReserve'),'256');
 // Reset the isolated admin quota so later upload regressions can run normally.
 await row.locator('input').fill('0');await row.locator('button').click();await page.waitForFunction(()=>storageView.accounts.find(a=>a.id===state.user.id).limit_bytes===0);
 await page.fill('#storageReserve','512');await page.click('#saveStoragePolicy');await page.waitForFunction(()=>storageView.policy.reserve_bytes===536870912&&!storageLoading);
 await row.locator('input').fill('3');page.once('dialog',d=>d.dismiss());await page.click('#refreshStorage');assert.equal(await row.locator('input').inputValue(),'3');
 page.once('dialog',d=>d.accept());await page.click('#refreshStorage');await page.waitForFunction(()=>!storageLoading&&!storageDirty);
 await page.setViewportSize({width:390,height:844});await page.screenshot({path:path.join(directory,'storage-mobile.png'),fullPage:true});
 assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'Storage page must fit mobile viewport');await page.setViewportSize({width:1440,height:1100});
 page.once('dialog',d=>d.accept());await page.click('#cleanUpdateCache');await page.waitForFunction(()=>document.querySelector('#storageResult').textContent.includes('已清理'));
 console.log('Storage UI passed: physical usage, unlimited default, quota/policy persistence, draft protection, safe cleanup and mobile layout.');
};
