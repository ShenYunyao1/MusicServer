// Browser contract tests for the tag editor. Uses isolated API fixtures only.
const assert=require('node:assert/strict');
const path=require('node:path');
const {chromium}=require(process.env.MUSIC_SERVER_PLAYWRIGHT||'playwright');

async function fixture(browser, schema=8) {
  const page=await browser.newPage({viewport:{width:1000,height:760}});
  await page.setContent('<!doctype html><html lang="zh-CN"><body><dialog id="trackDetails"><div id="trackDetailsBody"></div></dialog><div id="uploadList"></div><div class="queue-actions"></div><input id="trackSearch"><div id="status"></div></body></html>');
  await page.evaluate(schema=>{
    window.state={user:{id:'alice',role:'member'},selectedTrackIds:new Set(['song']),tracks:[{id:'song',title:'Song',owner_id:'alice'}],uploads:[],trackFilter:'all'};
    window.$=id=>document.getElementById(id);
    window.isAdministrator=()=>state.user?.role==='admin';window.canManageTrack=t=>t.owner_id===state.user?.id||isAdministrator();
    window.visibleTracks=()=>state.tracks;window.setStatus=()=>{};window.renderUploads=()=>{};window.refreshCore=async()=>{};
    window.calls=[];window.schema=schema;window.conflict=false;
    window.job={id:'job',state:'preview',version:1,total:1,completed:0,conflicts:0,failed:0,cancelled:0,items:[{track_id:'song',version:1,state:'pending'}],common_tag_ids:[],partial_tag_ids:['partial']};
    window.api=async(path,method='GET',body,key,version)=>{
      calls.push({path,method,body,version});
      if(path==='tag-groups'){if(schema===7)throw Object.assign(new Error('not enabled'),{status:503,code:'TAGS_UNAVAILABLE'});return [{id:'genre',name:'风格'}]}
      if(path.startsWith('tags?'))return {items:[{id:'rock',name:'Rock',group_id:'genre'},{id:'partial',name:'Some',group_id:'genre'}],total:2,offset:0,limit:50};
      if(path==='tags'&&method==='POST')return {id:'new-personal',name:body.name,group_id:body.group_id};
      if(path==='tag-batches'&&method==='GET')return window.recentJobs||[];
      if(path==='tracks/song/tag-import-source'){if(window.sourceFailure)throw new Error('source retry fixture');return window.importSource||{values:[],unmapped:[],truncated:false}}
      if(path==='tracks/song/tags')return {public:[],personal:[],public_version:1,personal_version:1};
      if(path==='tag-batches'&&method==='POST')return structuredClone(job);
      if(path==='tag-batches/job'&&method==='PATCH'){if(window.delayPatch)await new Promise(resolve=>window.finishPatch=resolve);job.version++;job.add_ids=body.add_ids;job.remove_ids=body.remove_ids;return structuredClone(job)}
      if(path==='tag-batches/job/start'){if(conflict){job.state='failed';job.conflicts=1;job.version++;throw Object.assign(new Error('version conflict'),{status:409,code:'VERSION_CONFLICT'})}job.state=window.runningJob?'running':'completed';job.completed=window.runningJob?0:job.total;job.version++;return structuredClone(job)}
      if(path==='tag-batches/job/retry'){job.state='preview';job.conflicts=0;job.version++;return structuredClone(job)}
      if(path==='tag-batches/job/cancel'){job.state='cancelled';job.cancelled=job.total-job.completed;job.version++;return structuredClone(job)}
      if(path==='tag-batches/job')return structuredClone(job);
      throw new Error('Unexpected fixture request '+method+' '+path);
    };
  },schema);
  await page.addScriptTag({path:path.resolve(__dirname,'../internal/music/tag-editor.js')});
  await page.addStyleTag({path:path.resolve(__dirname,'../internal/music/management.css')});
  await page.addStyleTag({path:path.resolve(__dirname,'../internal/music/tag-editor.css')});
  return page;
}

async function main(){
  const browser=await chromium.launch({headless:true,executablePath:process.env.MUSIC_SERVER_BROWSER});
  try{
    let page=await fixture(browser,7);
    assert.equal(await page.evaluate(()=>loadTagCapability()),false);
    assert.equal(await page.evaluate(()=>openTrackTagEditor(['song'])),false);
    assert.equal(await page.locator('#tagEditorDialog').count(),0);
    assert.equal(await page.evaluate(()=>calls.some(c=>c.method!=='GET')),false);
    await page.close();

    page=await fixture(browser);
    await page.evaluate(()=>openTrackTagEditor(['song']));
    await page.locator('[data-tag-operation="rock"]').selectOption('add');
    assert.equal(await page.evaluate(()=>calls.some(c=>c.path.endsWith('/start'))),false,'editing must not apply');
    await page.evaluate(()=>window.delayPatch=true);
    await page.locator('#tagEditorSave').click();
    await page.waitForFunction(()=>Boolean(window.finishPatch));
    assert.equal(await page.locator('[data-tag-operation="rock"]').isDisabled(),true,'preview request must freeze mutable draft controls');
    await page.evaluate(()=>{window.delayPatch=false;window.finishPatch()});
    await page.locator('#tagEditorStart').waitFor({state:'visible'});
    await page.evaluate(()=>{window.conflict=true});
    await page.locator('#tagEditorStart').click();
    await page.waitForFunction(()=>document.querySelector('#tagEditorMessage').textContent.includes('version conflict'));
    assert.equal(await page.locator('[data-tag-operation="rock"]').inputValue(),'add','conflict must preserve draft');
    await page.evaluate(()=>{window.conflict=false});
    await page.locator('#tagEditorRetry').click();
    await page.locator('#tagEditorStart').click();
    await page.waitForFunction(()=>document.querySelector('#tagEditorMessage').textContent.includes('已完成'));
    assert.equal(await page.evaluate(()=>calls.find(c=>c.method==='PATCH').body.add_ids[0]),'rock');
    await page.evaluate(()=>{tagWorkspace.dialog.close();return tagTaskMenuItem().action()});
    await page.waitForFunction(()=>document.querySelector('#tagEditorMessage').textContent.includes('已完成'));
    assert.equal(await page.evaluate(()=>calls.filter(c=>c.path==='tag-batches'&&c.method==='POST').length),1,'reopening a task cannot create a fresh snapshot');
    await page.evaluate(()=>clearTagWorkspace());
    assert.equal(await page.locator('#tagEditorDialog').count(),0);
    await page.close();

    page=await fixture(browser);
    await page.evaluate(async()=>{
      state.uploads=[{id:'upload',status:'待确认',stageId:'stage',edits:{},preview:{tag_versions:{public_version:1,personal_version:1}},file:{name:'sample.mp3'}}];
      await openUploadTagEditor(state.uploads[0]);
    });
    await page.locator('[data-tag-operation="rock"]').selectOption('add');
    await page.locator('#tagEditorSave').click();
    const result=await page.evaluate(()=>({draft:state.uploads[0].tagDraft,edits:uploadTagEdits(state.uploads[0]),writes:calls.filter(c=>c.method!=='GET')}));
    assert.deepEqual(result.edits,{personal:{version:1,add_ids:['rock'],remove_ids:[]}});
    assert.equal(result.writes.length,0,'upload draft cannot mutate tracks');
    await page.setViewportSize({width:390,height:740});
    await page.evaluate(()=>openUploadTagEditor(state.uploads[0]));
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'narrow screen cannot overflow horizontally');
    if(process.env.MUSIC_SERVER_TAG_UI_ARTIFACTS){const fs=require('node:fs/promises');await fs.mkdir(process.env.MUSIC_SERVER_TAG_UI_ARTIFACTS,{recursive:true});await page.screenshot({path:path.join(process.env.MUSIC_SERVER_TAG_UI_ARTIFACTS,'tag-editor-narrow.png')})}
    await page.locator('#tagEditorDialog button').first().click();
    await page.close();

    page=await fixture(browser);
    await page.evaluate(()=>openTrackTagEditor(['song']));
    await page.locator('#tagRange').selectOption('all');
    await page.locator('#tagEditorReload').click();
    await page.locator('[data-tag-operation="rock"]').selectOption('add');
    await page.evaluate(()=>state.tracks.push({id:'later',owner_id:'alice'}));
    await page.locator('#tagEditorSave').click();
    await page.locator('#tagEditorStart').waitFor({state:'visible'});
    const requests=await page.evaluate(()=>calls.filter(call=>call.path==='tag-batches'&&call.method==='POST'));
    assert.deepEqual(requests[1].body.query,{q:'',filter:'all'});
    assert.equal(await page.evaluate(()=>calls.filter(call=>call.method==='PATCH').some(call=>JSON.stringify(call.body).includes('later'))),false);
    await page.close();

    page=await fixture(browser);
    await page.evaluate(async()=>{window.sourceFailure=true;$('trackDetails').showModal();await decorateTrackTagDetails(state.tracks[0])});
    await page.getByRole('button',{name:'重试读取风格来源'}).waitFor({state:'visible'});
    assert.equal(await page.locator('[data-tag-feature="details"]').getByRole('button',{name:'管理标签'}).isVisible(),true);
    await page.evaluate(()=>{window.sourceFailure=false;window.importSource={values:['Rock','<img src=x onerror=alert(1)>'],unmapped:['<img src=x onerror=alert(1)>'],truncated:true}});
    await page.getByRole('button',{name:'重试读取风格来源'}).click();
    await page.waitForFunction(()=>document.querySelector('[data-tag-import-source]').textContent.includes('部分原值未参与整理'));
    assert.equal(await page.locator('[data-tag-import-source] img').count(),0,'raw genre values must only render as text');
    assert.equal(await page.locator('[data-tag-import-source] .tag-chip').textContent(),'<img src=x onerror=alert(1)>');
    await page.close();

    page=await fixture(browser);
    await page.evaluate(()=>{job.state='interrupted';window.recentJobs=[{id:'job',scope:'personal',state:'interrupted',version:job.version,total:1,completed:0,conflicts:0,failed:0,cancelled:0}]});
    await page.evaluate(()=>loadTagCapability());
    assert.ok(await page.evaluate(()=>Boolean(tagTaskMenuItem())),'persisted task must be discoverable after login/refresh');
    await page.evaluate(()=>tagTaskMenuItem().action());
    await page.locator('#tagEditorRetry').waitFor({state:'visible'});
    assert.equal(await page.evaluate(()=>calls.some(call=>call.method==='POST')),false,'recovering a task cannot create a new snapshot');
    await page.close();

    page=await fixture(browser);
    await page.evaluate(()=>{window.runningJob=true;return openTrackTagEditor(['song'])});
    await page.locator('[data-tag-operation="rock"]').selectOption('add');
    await page.locator('#tagEditorSave').click();
    await page.locator('#tagEditorStart').click();
    await page.getByRole('button',{name:'取消任务',exact:true}).click();
    await page.waitForFunction(()=>document.querySelector('#tagEditorMessage').textContent.includes('已取消'));
    assert.equal(await page.locator('#tagEditorRetry').isVisible(),true);
    const cancellation=await page.evaluate(()=>calls.find(call=>call.path.endsWith('/cancel')));
    assert.equal(cancellation.method,'POST');assert.ok(cancellation.version>0);
    await page.close();
    console.log('tag editor browser contracts passed: schema gate, preview/confirm, conflict draft, logout, upload drafts');
  }finally{await browser.close()}
}
main().catch(error=>{console.error(error);process.exitCode=1});
