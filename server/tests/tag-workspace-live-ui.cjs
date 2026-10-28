'use strict';
// Real independent browser sessions, no API replacement or application-state injection.
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path');
const {chromium}=require(process.env.MUSIC_SERVER_PLAYWRIGHT||'playwright');
async function main(){
 const base=new URL(process.env.MUSIC_SERVER_TAG_WORKSPACE_URL||'http://invalid');
 assert.ok(['127.0.0.1','localhost','[::1]'].includes(base.hostname));
 const browser=await chromium.launch({headless:true,executablePath:process.env.MUSIC_SERVER_BROWSER});
 const contexts=[],errors=[];
 async function login(user,password){
  const context=await browser.newContext({viewport:{width:1200,height:900}});contexts.push(context);
  const page=await context.newPage();page.on('pageerror',error=>errors.push(error.message));
  await page.goto(base.href+'#/library');await page.waitForFunction(()=>!document.querySelector('#username').disabled);
  await page.fill('#username',user);await page.fill('#password',password);await page.click('#loginButton');
  await page.waitForFunction(()=>browseState.tracks?.filterKey&&browseState.tracks.total===3&&tagSyncWorkspace.baseline);
  return page;
 }
 try{
  const first=await login('admin','test administrator password');
  const second=await login('admin','test administrator password');
  const member=await login('workspace-member','personal password for workspace-member');
  let firstQueries=0,memberQueries=0;
  first.on('request',r=>{if(new URL(r.url()).pathname==='/api/v2/track-query')firstQueries++});
  member.on('request',r=>{if(new URL(r.url()).pathname==='/api/v2/track-query')memberQueries++});
  const alpha=await first.evaluate(()=>browseState.tracks.items.find(t=>t.title==='Alpha'));
  const originalVersion=alpha.version;
  const label=await second.evaluate(async id=>{
   const tag=await api('tags','POST',{scope:'personal',group_id:'scene',name:'Cross-tab private'});
   const view=await api('tracks/'+id+'/tags');
   await api('tracks/'+id+'/tags','PATCH',{scope:'personal',add_ids:[tag.id],remove_ids:[]},undefined,view.personal_version);
   return tag;
  },alpha.id);
  await first.evaluate(()=>pollTagChanges());
  await first.waitForFunction(()=>[...document.querySelectorAll('.track-summary-tag')].some(n=>n.textContent==='Cross-tab private'));
  assert.equal(await first.evaluate(id=>browseState.tracks.items.find(t=>t.id===id).version,alpha.id),originalVersion,'Personal changes preserve player track versions');
  assert.ok(firstQueries>0);
  await member.evaluate(()=>pollTagChanges());
  assert.equal(memberQueries,0,'Foreign private changes must not cause catalogue reloads');
  assert.ok(!(await member.locator('#trackRows').textContent()).includes(label.name));
  const beforeSaved=firstQueries;
  await second.evaluate(()=>api('saved-track-filters','POST',{name:'From another tab',filter:{version:1}}));
  await first.evaluate(()=>pollTagChanges());assert.equal(firstQueries,beforeSaved,'Saving a rule does not change the current catalogue');
  await first.locator('#trackAdvancedFilter').click();
  await first.locator('#savedFilterName').fill('My unsaved draft');
  await second.evaluate(async tag=>{await api('tags/'+tag.id,'PATCH',{name:'Updated private'},undefined,tag.version)},label);
  await first.evaluate(()=>pollTagChanges());assert.equal(await first.locator('#savedFilterName').inputValue(),'My unsaved draft');
  await first.locator('#trackFilterDialog').getByRole('button',{name:'关闭',exact:true}).first().click();
  await first.evaluate(()=>pollTagChanges());
  await first.waitForFunction(()=>[...document.querySelectorAll('.track-summary-tag')].some(n=>n.textContent==='Updated private'));
  await first.locator('#trackTagDictionary').click();
  await first.locator('#dictionaryName').fill('Live dictionary source');
  await first.locator('#dictionaryAliases').fill('Live alias one\nLive alias two');
  await first.locator('#dictionarySave').click();await first.waitForFunction(()=>document.querySelector('#dictionaryMessage').textContent==='词条已保存。');
  const sourceID=await first.evaluate(()=>tagDictionaryWorkspace.editor.selected.id);
  await first.locator('#dictionaryNew').click();await first.locator('#dictionaryName').fill('Live dictionary target');
  await first.locator('#dictionarySave').click();await first.waitForFunction(()=>tagDictionaryWorkspace.editor.selected?.name==='Live dictionary target'&&!tagDictionaryWorkspace.editor.busy);
  const targetID=await first.evaluate(()=>tagDictionaryWorkspace.editor.selected.id);
  await first.locator('[data-dictionary-edit="'+sourceID+'"]').click();
  await first.locator('#dictionaryMergeSearch').fill('Live dictionary target');await first.locator('#dictionaryMergeFind').click();
  await first.locator('[data-dictionary-target="'+targetID+'"]').click();await first.locator('#dictionaryMerge').click();await first.locator('#dictionaryConfirmMerge').click();
  await first.waitForFunction(()=>document.querySelector('#dictionaryMessage').textContent.startsWith('词条已合并'));
  const merged=await second.evaluate(id=>api('tags/'+id),targetID);assert.ok(merged.aliases.includes('Live alias one')&&merged.aliases.includes('Live dictionary source'));
  await first.locator('#dictionaryGroupManage summary').click();await first.locator('#dictionaryGroupName').fill('Live group');
  await first.locator('#dictionaryGroupSave').click();await first.waitForFunction(()=>document.querySelector('#dictionaryMessage').textContent==='分组已保存。');
  const groupID=await first.evaluate(()=>tagWorkspace.groups.find(g=>g.name==='Live group').id);
  await first.locator('#dictionaryGroupEdit').selectOption(groupID);await first.locator('#dictionaryGroupDelete').click();await first.locator('#dictionaryGroupConfirmDelete').click();
  await first.waitForFunction(()=>document.querySelector('#dictionaryMessage').textContent==='分组已删除。');
  await first.setViewportSize({width:390,height:850});
  assert.ok(await first.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
  const shot=path.resolve(__dirname,'../build/tag-bridge/ui/tag-dictionary-narrow.png');fs.mkdirSync(path.dirname(shot),{recursive:true});await first.screenshot({path:shot,fullPage:true});
  await first.setViewportSize({width:1200,height:900});
  await first.locator('#dictionaryClose').click();
  const row=first.locator('#trackRows tr').filter({hasText:'Alpha'}),before=await row.boundingBox();
  await row.click({button:'right'});await first.locator('#contextMenu').waitFor({state:'visible'});
  const after=await row.boundingBox();assert.equal(after.y,before.y,'Context menu must not move the list');
  await first.keyboard.press('Escape');await row.focus();await row.press('Shift+F10');await first.locator('#contextMenu').waitFor({state:'visible'});
  assert.ok((await first.locator('#contextMenu').textContent()).includes('管理标签'));
  await first.keyboard.press('Escape');await row.dispatchEvent('pointerdown',{pointerType:'touch',clientX:before.x+40,clientY:before.y+20});
  await first.locator('#contextMenu').waitFor({state:'visible'});await row.dispatchEvent('pointerup',{pointerType:'touch'});
  assert.ok((await first.locator('#contextMenu').textContent()).includes('管理标签'));
  await first.keyboard.press('Escape');await first.setViewportSize({width:390,height:850});
  assert.ok(await first.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Narrow layout does not overflow');
  // A fresh account entry with a filtered URL must use that rule from its first request.
  const publicTags=await second.evaluate(()=>api('tags?scope=public&group_id=genre&limit=100'));
  const rock=publicTags.items.find(t=>t.name==='Rock');
  const rule={version:1,tags:[{scope:'public',group_id:'genre',include_ids:[rock.id]}]};
  const requests=[];first.on('request',r=>{if(new URL(r.url()).pathname==='/api/v2/track-query')requests.push(r.postDataJSON())});
  await first.goto(base.href+'#/library?'+new URLSearchParams({tf:JSON.stringify(rule),q:'Gamma'}));
  await first.waitForFunction(()=>browseState.tracks?.filterKey&&browseState.tracks.total===1);
  assert.ok(requests.length>0);for(const request of requests){assert.equal(request.filter.q,'Gamma');assert.deepEqual(request.filter.tags,rule.tags)}
  assert.deepEqual(errors,[]);
  console.log('Live tag workspace passed: private cross-tab updates, foreign isolation, saved-only no reload, draft preservation, dictionary create/alias/merge/group CRUD, stable context menu, 390px and filtered first load.');
 }finally{await browser.close()}
}
main().catch(error=>{console.error(error);process.exitCode=1});
