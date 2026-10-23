const assert = require('node:assert/strict');
module.exports = async ({page}) => {
  const previousHash=await page.evaluate(()=>location.hash);
  let applies = 0, checks = 0;
  let status = {state:'available', current_version:'0.14.0', latest_version:'9.0.0', compatible:true, can_install:true, schema:7, commit:'a'.repeat(40), notes:'<script>untrusted release text</script>'};
  const route = async r => {
    if (r.request().url().endsWith('/check')) { checks++; }
    if (r.request().url().endsWith('/apply')) { applies++; assert.deepEqual(r.request().postDataJSON(),{version:'9.0.0'}); status={...status,state:'queued'}; }
    await r.fulfill({status:r.request().method()==='POST'?202:200,contentType:'application/json',body:JSON.stringify(status)});
  };
  await page.route('**/api/v2/admin/update{,/**}', route);
  try {
    await page.evaluate(()=>location.hash='#/admin/settings/update');
    await page.locator('#installUpdate').waitFor({state:'visible'});
    assert.equal(await page.locator('#updateNotes script').count(),0);
    assert.match(await page.locator('#updateNotes').textContent(), /untrusted release text/);
    await page.click('#checkUpdate');
    assert.equal(checks,1);
    assert.equal(applies,0,'Checking must never install');
    page.once('dialog',dialog=>dialog.dismiss());
    await page.click('#installUpdate');
    assert.equal(applies,0,'Cancel must leave the server running');
    page.once('dialog',dialog=>dialog.accept());
    await page.click('#installUpdate');
    await page.waitForFunction(()=>document.querySelector('#updateStatus').textContent.includes('排队'));
    assert.equal(applies,1);
    assert.equal(await page.locator('#installUpdate').isVisible(),false);
    status={...status,state:'rolled_back',message:'health check failed',backup_path:'/private/backup.sqlite'};
    await page.evaluate(()=>loadUpdate());
    assert.match(await page.locator('#updateStatus').textContent(),/已恢复原版本/);
    status={...status,state:'incompatible',compatible:false};
    await page.evaluate(()=>loadUpdate());
    assert.equal(await page.locator('#installUpdate').isVisible(),false);
    console.log('Updater UI passed: explicit confirmation, version binding, safe notes, durable result and schema guard.');
  } finally {
    await page.evaluate(hash=>location.hash=hash,previousHash);
    await page.waitForFunction(hash=>location.hash===hash,previousHash);
    await page.unroute('**/api/v2/admin/update{,/**}',route);
  }
};
