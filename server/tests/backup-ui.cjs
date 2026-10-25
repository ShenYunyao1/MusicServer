const assert=require('node:assert/strict');
const fs=require('node:fs/promises');
const path=require('node:path');
const {spawnSync}=require('node:child_process');

module.exports=async function({page,directory}){
 const errors=[],onError=e=>errors.push(e.message);page.on('pageerror',onError);
 try{
  await page.goto(page.url().split('#')[0]+'#/admin/settings/backup');await page.locator('#createBackup').waitFor();
  await page.waitForFunction(()=>backupPolicyLoaded&&!backupLoading);
  assert.equal(await page.isChecked('#backupEnabled'),false);
  await page.fill('#backupInterval','12');await page.fill('#backupKeep','2');await page.click('#saveBackupPolicy');await page.waitForFunction(()=>!document.querySelector('#backupPolicyFields').disabled&&!backupPolicyDirty);
  await page.reload();await page.waitForFunction(()=>backupPolicyLoaded&&!backupLoading);assert.equal(await page.inputValue('#backupInterval'),'12');
  const expected=await page.evaluate(()=>({id:state.tracks[0].id,hash:state.tracks[0].content_hash,path:state.tracks[0].path}));
  await page.click('#createBackup');await page.waitForFunction(()=>backupCurrentJob?.state==='completed',null,{timeout:20000});
  const archive=await page.evaluate(()=>backupCurrentJob.archive);assert.equal(archive.include_media,true);
  const row=page.locator('[data-backup-id="'+archive.id+'"]');await row.waitFor();
  const downloadEvent=page.waitForEvent('download');await row.locator('[data-backup-download]').click();const download=await downloadEvent;
  const archivePath=path.join(directory,download.suggestedFilename());await download.saveAs(archivePath);assert.equal((await fs.stat(archivePath)).size,archive.size);
  const restored=path.join(directory,'backup-restored');
  const result=spawnSync(process.env.MUSIC_SERVER_TEST_BINARY,['backup','restore','--file',archivePath,'--destination',restored],{encoding:'utf8',timeout:20000,windowsHide:true});
  assert.equal(result.status,0,result.stdout+'\n'+result.stderr);assert.ok(result.stdout.includes('RESTORE SUCCEEDED'));
  const media=await fs.readFile(path.join(restored,expected.path));assert.equal(require('node:crypto').createHash('sha256').update(media).digest('hex'),expected.hash);
  assert.ok((await fs.stat(path.join(restored,'data/library.sqlite'))).size>0);
  const denied=spawnSync(process.env.MUSIC_SERVER_TEST_BINARY,['backup','restore','--file',archivePath,'--destination',restored],{encoding:'utf8',timeout:20000,windowsHide:true});assert.notEqual(denied.status,0);
  await page.fill('#backupKeep','4');await page.click('#refreshBackups');await page.waitForFunction(()=>!backupLoading);assert.equal(await page.inputValue('#backupKeep'),'4','List refresh must preserve a policy draft');
  await page.setViewportSize({width:390,height:844});await page.waitForTimeout(250);
  await page.screenshot({path:path.join(directory,'backup-mobile.png'),fullPage:true});
  const mobile=await page.evaluate(()=>({fits:document.documentElement.scrollWidth<=innerWidth,overflow:[...document.querySelectorAll('[data-settings-pane="backup"] *')].filter(el=>el.getBoundingClientRect().right>innerWidth).slice(0,8).map(el=>({tag:el.tagName,id:el.id,class:el.className}))}));
  assert.equal(mobile.fits,true,JSON.stringify(mobile.overflow));await page.setViewportSize({width:1440,height:1100});
  page.once('dialog',d=>d.dismiss());await row.locator('[data-backup-delete]').click();assert.equal(await row.count(),1);
  page.once('dialog',d=>d.accept());await row.locator('[data-backup-delete]').click();await row.waitFor({state:'detached'});
  await page.reload();await page.waitForFunction(()=>backupPolicyLoaded&&!backupLoading);assert.equal(await page.locator('[data-backup-id="'+archive.id+'"]').count(),0);
  assert.deepEqual(errors,[]);
  console.log('Backup UI passed: policy persistence/draft, full archive task, download, real CLI restore/media hash, occupied destination rejection, deletion and mobile layout.');
 }finally{page.off('pageerror',onError)}
};
