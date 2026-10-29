'use strict';
const assert=require('node:assert/strict'),path=require('node:path');
const {chromium}=require(process.env.MUSIC_SERVER_PLAYWRIGHT||'playwright');
async function main(){const browser=await chromium.launch({headless:true,executablePath:process.env.MUSIC_SERVER_BROWSER});try{
 const page=await browser.newPage({viewport:{width:1300,height:850}});
 await page.setContent('<main class="main"><section id="page-tracks"><div class="track-toolbar"><input id="leftSearch"></div><table id="trackRows"><tr><td><button id="selectB">选择第二首</button></td></tr></table><div id="blank" style="height:100px">空白</div></section></main>');
 await page.evaluate(()=>{
  window.state={user:{id:'alice'},selectedTrackIds:new Set(['a'])};window.tagWorkspace={generation:1,groups:[{id:'language',name:'演唱语言'},{id:'genre',name:'风格'}]};
  window.tagNode=(tag,text,className)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(className)n.className=className;return n};
  window.tagButton=(text,action)=>{const n=tagNode('button',text,'btn');n.type='button';n.onclick=action;return n};
  window.data={a:{public:[],personal:[],public_version:1,personal_version:1},b:{public:[],personal:[],public_version:1,personal_version:1}};
  window.tags=[{id:'jp',name:'日语',aliases:['日文','Japanese'],group_id:'language'}];window.calls=[];
  window.api=async(url,method='GET',body,key,version)=>{
   calls.push({url,method,body,version});
   if(url.startsWith('tags?')){if(window.searchFailure)throw new Error('search failure');if(window.delaySearch)await new Promise(r=>window.releaseSearch=r);const p=new URLSearchParams(url.split('?')[1]);const items=tags.filter(t=>t.group_id===p.get('group_id')&&[t.name,...t.aliases||[]].some(n=>n.includes(p.get('q'))));return {items:structuredClone(items),total:items.length}}
   if(url==='tags'){if(window.delayCreate)await new Promise(r=>window.releaseCreate=r);if(window.createConflict)throw Object.assign(new Error('duplicate'),{code:'TAG_NAME_EXISTS'});const t={id:'new'+tags.length,name:body.name,group_id:body.group_id};tags.push(t);return structuredClone(t)}
   if(url.startsWith('tracks/')){const id=url.split('/')[1],v=data[id];if(method==='GET')return structuredClone(v);if(window.failPatchID===id)throw new Error('fixture write failure');if(window.delayPatch)await new Promise(r=>window.releasePatch=r);if(v[body.scope+'_version']!==version)throw new Error('changed');for(const id of body.add_ids)v[body.scope].push(tags.find(t=>t.id===id));v[body.scope]=v[body.scope].filter(t=>!body.remove_ids.includes(t.id));v[body.scope+'_version']++;return structuredClone(v)}
   throw new Error('unexpected '+url);
  };
 });
 await page.addStyleTag({path:path.resolve(__dirname,'../internal/music/tagger-ui.css')});
 await page.addScriptTag({path:path.resolve(__dirname,'../internal/music/tagger-ui.js')});
 await page.evaluate(()=>document.querySelector('#selectB').onclick=()=>{state.selectedTrackIds=new Set(['b']);taggerSelectionChanged()});
 await page.evaluate(()=>openCompactTagger(['a']));
 await page.evaluate(()=>{const old=document.querySelector('[data-tagger-tag=jp]');const search=document.querySelector('#taggerSearch');search.value='pending query';search.dispatchEvent(new Event('input'));old.click()});
 assert.equal(await page.evaluate(()=>data.a.public.length),0,'Old search results cannot write during debounce');
 assert.equal(await page.locator('dialog').count(),0);
 await page.locator('#taggerSearch').fill('日文');await page.waitForFunction(()=>!compactTagger.loading);await page.waitForTimeout(220);
 assert.ok((await page.locator('[data-tagger-tag=jp]').textContent()).includes('别名：日文'));
 assert.equal(await page.locator('#taggerCreate').count(),0);
 await page.locator('#taggerSearch').press('Enter');await page.waitForFunction(()=>data.a.public.length===1&&!compactTagger.busy);
 await page.locator('#taggerUndo').click();await page.waitForFunction(()=>data.a.public.length===0&&!compactTagger.busy);
 await page.locator('#selectB').click();await page.waitForFunction(()=>compactTagger.ids[0]==='b'&&compactTagger.ready);
 assert.equal(await page.locator('#compactTagger').count(),1,'Selecting a track keeps the nonmodal tagger open');
 await page.locator('#taggerPin').click();await page.evaluate(()=>{state.selectedTrackIds=new Set(['a']);taggerSelectionChanged()});assert.equal(await page.evaluate(()=>compactTagger.ids[0]),'b');
 await page.locator('#taggerPin').click();await page.waitForFunction(()=>compactTagger.ids[0]==='a'&&compactTagger.ready);
 await page.locator('#taggerSearch').fill('新语言');await page.waitForTimeout(250);await page.waitForFunction(()=>!compactTagger.loading);
 await page.evaluate(()=>{document.querySelector('#taggerSearch').dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',isComposing:true,bubbles:true}))});assert.equal(await page.evaluate(()=>tags.length),1,'IME confirmation does not create');
 await page.evaluate(()=>delayCreate=true);await page.locator('#taggerSearch').press('Enter');await page.waitForFunction(()=>typeof releaseCreate==='function');
 await page.locator('#selectB').click();await page.evaluate(()=>{delayCreate=false;releaseCreate()});await page.waitForFunction(()=>!compactTagger.busy&&compactTagger.ids[0]==='b'&&compactTagger.ready);
 assert.equal(await page.evaluate(()=>data.a.public[0]?.name),'新语言','Create keeps its original target despite selection changes');assert.equal(await page.evaluate(()=>data.b.public.length),0);
 await page.locator('#taggerUndo').click();await page.waitForFunction(()=>!compactTagger.busy);assert.equal(await page.evaluate(()=>data.a.public.length),0);
 await page.locator('#taggerSearch').fill('日语');await page.waitForTimeout(250);await page.locator('[data-tagger-tag=jp]').click();await page.waitForFunction(()=>!compactTagger.busy&&data.b.public.length===1);
 await page.evaluate(()=>data.b.public_version++);await page.locator('#taggerUndo').click();await page.waitForFunction(()=>!compactTagger.busy);assert.equal(await page.evaluate(()=>data.b.public.length),1,'Undo refuses to overwrite later edits');assert.ok((await page.locator('#taggerMessage').textContent()).includes('后续修改'));
 await page.evaluate(()=>searchFailure=true);await page.locator('#taggerSearch').fill('不存在');await page.waitForTimeout(250);await page.locator('#taggerSearch').press('Enter');assert.equal(await page.locator('#taggerCreate').count(),0,'Search errors never offer creation');
 await page.locator('#blank').click();assert.equal(await page.locator('#compactTagger').count(),0,'Outside empty page closes unpinned tagger');
 await page.evaluate(()=>{searchFailure=false;data.a.public=[];data.b.public=[];failPatchID='b';return openCompactTagger(['a','b'])});
 await page.locator('[data-tagger-tag=jp]').click();await page.waitForFunction(()=>!compactTagger.busy);
 assert.equal(await page.evaluate(()=>data.a.public.length),1);assert.equal(await page.evaluate(()=>data.b.public.length),0);
 assert.ok((await page.locator('#taggerMessage').textContent()).includes('部分操作失败'),'Partial failures must not claim full success');
 await page.evaluate(()=>failPatchID=null);await page.locator('#taggerUndo').click();await page.waitForFunction(()=>!compactTagger.busy);assert.equal(await page.evaluate(()=>data.a.public.length),0,'Successful subset is independently undoable');
 await page.locator('#taggerScope').selectOption('personal');await page.waitForFunction(()=>compactTagger.ready&&!compactTagger.loading);
 await page.locator('[data-tagger-tag=jp]').click();await page.waitForFunction(()=>!compactTagger.busy);assert.equal(await page.evaluate(()=>data.a.personal.length),1);assert.equal(await page.evaluate(()=>data.a.public.length),0,'Personal writes do not enter public bindings');
 await page.evaluate(()=>{searchFailure=false;return openCompactTagger(['a'])});await page.locator('#taggerPin').click();await page.locator('#blank').click();assert.equal(await page.locator('#compactTagger').count(),1);await page.locator('#leftSearch').focus();await page.keyboard.press('Escape');assert.equal(await page.locator('#compactTagger').count(),0);
 await page.setViewportSize({width:390,height:844});await page.evaluate(()=>openCompactTagger(['a']));assert.ok(await page.locator('#compactTagger').isVisible());assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'Narrow tagger must not create horizontal overflow');
 await page.evaluate(()=>{state.user={id:'bob'};taggerSelectionChanged()});assert.equal(await page.locator('#compactTagger').count(),0,'Account switch invalidates workspace');
 console.log('Compact tagger contracts passed: nonmodal selection, alias, create/IME, frozen targets, pin, safe undo, errors, outside dismissal and narrow layout');
 }finally{await browser.close()}}
main().catch(error=>{console.error(error);process.exitCode=1});
