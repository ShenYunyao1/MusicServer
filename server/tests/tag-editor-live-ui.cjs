// Real schema-8 API/browser integration, started by TestTagEditorLiveBrowser.
// No network mocks or replacement of application state/API functions.
const assert=require('node:assert/strict');
const {chromium}=require(process.env.MUSIC_SERVER_PLAYWRIGHT||'playwright');

async function main(){
  const supplied=process.env.MUSIC_SERVER_TAG_LIVE_URL;
  assert.ok(supplied,'Set MUSIC_SERVER_TAG_LIVE_URL to the isolated Go httptest server');
  const base=new URL(supplied);
  assert.ok(['127.0.0.1','localhost','[::1]'].includes(base.hostname),'This test only runs against a local isolated server');
  const browser=await chromium.launch({headless:true,executablePath:process.env.MUSIC_SERVER_BROWSER});
  const page=await browser.newPage({viewport:{width:1200,height:900}}),errors=[];
  const settled=async text=>page.waitForFunction(text=>!compactTagger.busy&&!compactTagger.loading&&document.querySelector('#taggerMessage').textContent.includes(text),text);
  page.on('pageerror',error=>errors.push(error.message));
  try{
    await page.goto(base.href.replace(/\/$/,'')+'/#/library');
    await page.locator('#loginButton').waitFor({state:'visible'});
    await page.waitForFunction(()=>!document.querySelector('#username').disabled);
    await page.fill('#username','admin');
    await page.fill('#password','test administrator password');
    await page.click('#loginButton');
    await page.waitForFunction(()=>state.user?.username==='admin'&&tagFeatureEnabled());
    await page.locator('[data-library-track]').first().waitFor({state:'visible'});
    const track=await page.evaluate(()=>visibleTracks()[0]);
    assert.ok(track?.id,'The isolated fixture must contain a visible track');
    const label=await page.evaluate(async()=>{
      const page=await api('tags?scope=personal&q=My%20music&limit=100');
      return page.items.find(tag=>tag.name==='My music');
    });
    assert.ok(label?.id,'The fixture must contain the personal My music tag');
    const before=await page.evaluate(async id=>({tags:await api('tracks/'+id+'/tags'),track:await api('tracks/'+id)}),track.id);

    // The real context menu exposes the entry without inserting a toolbar.
    const row=page.locator('[data-library-track="'+track.id+'"]'),position=await row.boundingBox();
    await row.click({button:'right'});
    assert.equal(await page.locator('#contextMenu').getByRole('button',{name:'管理标签',exact:true}).isVisible(),true);
    const afterPosition=await row.boundingBox();assert.equal(afterPosition.y,position.y,'Opening the menu must not shift the track list');
    await page.keyboard.press('Escape');
    await page.evaluate(()=>hideContextMenu());

    // Enter through the real track-details panel.
    await row.locator('[data-details]').click();
    await page.locator('#trackDetails [data-tag-feature="details"]').waitFor({state:'visible'});
    await page.locator('#trackDetails').getByRole('button',{name:'管理标签',exact:true}).click();
    await page.locator('#compactTagger').waitFor({state:'visible'});
    assert.equal(await page.locator('#trackDetails').evaluate(dialog=>dialog.open),false,'The details modal must release the nonmodal tagger');
    assert.equal(await page.locator('#taggerScope').inputValue(),'public');
    await row.locator('[data-track-id]').check();
    const otherID=await page.evaluate(id=>visibleTracks().find(track=>track.id!==id).id,track.id);
    await page.locator('[data-track-id="'+otherID+'"]').check();
    await page.waitForFunction(()=>document.querySelector('#taggerSummary').textContent.includes('2 首'));
    await page.locator('#taggerPin').click();
    await page.locator('[data-track-id="'+otherID+'"]').uncheck();
    assert.ok((await page.locator('#taggerSummary').textContent()).includes('2 首'),'Pinned targets survive selection changes');
    await page.locator('#taggerPin').click();
    await page.waitForFunction(()=>document.querySelector('#taggerSummary').textContent.includes('1 首'));
    await page.locator('#taggerScope').selectOption('personal');
    await page.locator('#compactTagger [data-group="'+label.group_id+'"]').click();
    await page.locator('[data-tagger-tag="'+label.id+'"]').click();
    await settled('已添加 1 首');
    assert.ok((await page.evaluate(id=>api('tracks/'+id+'/tags'),track.id)).personal.some(tag=>tag.id===label.id),'A click saves against the real store');
    await page.locator('#taggerUndo').click();
    await settled('已撤销 1 首');
    assert.equal((await page.evaluate(id=>api('tracks/'+id+'/tags'),track.id)).personal.length,0);
    await page.locator('[data-tagger-tag="'+label.id+'"]').click();
    await settled('已添加 1 首');

    // Alias search selects the canonical public term instead of creating a duplicate.
    await page.locator('#taggerScope').selectOption('public');
    await page.locator('#compactTagger [data-group="language"]').click();
    await page.locator('#taggerSearch').fill('日文');
    await page.locator('#taggerResults button[data-tagger-tag]').filter({hasText:'日语'}).waitFor({state:'visible'});
    await page.waitForFunction(()=>!compactTagger.loading);
    assert.equal(await page.locator('#taggerCreate').count(),0,'Exact aliases suppress duplicate creation');
    await page.locator('#taggerSearch').press('Enter');
    await settled('已添加 1 首');
    assert.ok((await page.evaluate(id=>api('tracks/'+id+'/tags'),track.id)).public.some(tag=>tag.name==='日语'));
    await page.locator('#taggerUndo').click();
    await settled('已撤销 1 首');
    const after=await page.evaluate(async id=>({tags:await api('tracks/'+id+'/tags'),track:await api('tracks/'+id)}),track.id);
    assert.ok(after.tags.personal.some(tag=>tag.id===label.id),'Confirmed UI mutation must reach the real store');
    assert.equal(after.tags.personal_version,before.tags.personal_version+3);
    assert.equal(after.tags.public_version,before.tags.public_version+2);
    for(const field of ['id','path','content_hash','genre','uploaded_by','owner_id'])assert.deepEqual(after.track[field],before.track[field],'Tag editing cannot change track '+field);
    await page.locator('#compactTagger').getByRole('button',{name:'关闭',exact:true}).click();

    // A local upload draft uses the same real dictionary but does not create a
    // batch, a track or a relationship before upload confirmation.
    await page.evaluate(()=>location.hash='#/uploads');
    await page.locator('#page-upload').waitFor({state:'visible'});
    const beforeUploads=await page.evaluate(()=>api('tracks'));
    await page.setInputFiles('#uploadInput',{name:'tag-draft.mp3',mimeType:'audio/mpeg',buffer:Buffer.from('local draft; never uploaded')});
    await page.locator('[data-tag-feature="upload"]').first().click();
    await page.locator('#tagGroup').selectOption(label.group_id);
    await page.locator('[data-tag-operation="'+label.id+'"]').waitFor({state:'visible'});
    await page.locator('[data-tag-operation="'+label.id+'"]').selectOption('add');
    await page.click('#tagEditorSave');
    await page.waitForFunction(()=>!document.querySelector('#tagEditorDialog').open);
    assert.deepEqual(await page.evaluate(()=>api('tracks')),beforeUploads,'Saving an upload draft cannot create a track');
    assert.ok(await page.evaluate(id=>state.uploads[0].tagDraft.personal.add_ids.includes(id),label.id));
    assert.deepEqual(await page.evaluate(id=>api('tracks/'+id+'/tags'),track.id),after.tags,'Upload draft cannot modify an existing relationship');

    const member=await browser.newPage({viewport:{width:1200,height:900}});
    member.on('pageerror',error=>errors.push(error.message));
    await member.goto(base.href+'#/library');await member.waitForFunction(()=>!document.querySelector('#username').disabled);
    await member.fill('#username','tagger-member');await member.fill('#password','personal password for tagger-member');await member.click('#loginButton');
    await member.waitForFunction(()=>state.user?.username==='tagger-member'&&tagFeatureEnabled());
    // Keep real HTTP/API behavior but expose the response window deterministically.
    await member.route('**/api/v2/track-query',async route=>{await new Promise(resolve=>setTimeout(resolve,200));await route.continue()});
    await member.locator('#trackUntagged').click();
    await member.waitForFunction(()=>browseState.tracks?.total===2&&getActiveTrackFilter().untagged_public===true&&browseState.tracks.filterKey===activeTrackFilterKey());
    assert.equal(await member.locator('#trackUntagged').getAttribute('aria-pressed'),'true');
    await member.locator('[data-library-track="'+track.id+'"]').click({button:'right'});
    await member.locator('#contextMenu').getByRole('button',{name:'管理标签',exact:true}).click();
    await member.locator('#taggerSearch').fill('Member Created Live');
    await member.locator('#taggerCreate').waitFor({state:'visible'});
    await member.locator('#taggerSearch').press('Enter');
    await member.waitForFunction(()=>!compactTagger.busy&&!compactTagger.loading&&document.querySelector('#taggerMessage').textContent.includes('已添加 1 首'));
    const memberTag=await member.evaluate(async id=>(await api('tracks/'+id+'/tags')).public.find(t=>t.name==='Member Created Live'),track.id);
    assert.ok(memberTag?.id,'Members can create and apply a public term to another owner’s track');
    await member.waitForFunction(id=>browseState.tracks?.total===1&&!document.querySelector('[data-library-track="'+id+'"]'),track.id);
    assert.equal(await member.locator('[data-library-track="'+otherID+'"]').count(),1,'Immediate revision refresh removes the newly classified track from untagged results');
    await member.locator('#trackUntagged').click();
    await member.waitForFunction(()=>browseState.tracks?.total===2&&getActiveTrackFilter().untagged_public===undefined);
    const denied=await member.evaluate(async tag=>{try{await api('tags/'+tag.id,'PATCH',{name:'Forbidden rename'},undefined,tag.version);return 0}catch(error){return error.status}},memberTag);
    assert.equal(denied,403,'Public dictionary governance remains administrator-only');
    await page.evaluate(async id=>{
      const rock=(await api('tags?scope=public&group_id=genre&q=Rock')).items.find(t=>t.name==='Rock');
      const current=await api('tracks/'+id+'/tags');
      await api('tracks/'+id+'/tags','PATCH',{scope:'public',add_ids:[rock.id],remove_ids:[]},undefined,current.public_version);
    },track.id);
    await member.locator('#taggerUndo').click();
    await member.waitForFunction(()=>!compactTagger.busy&&document.querySelector('#taggerMessage').textContent.includes('拒绝覆盖'));
    const preserved=await member.evaluate(id=>api('tracks/'+id+'/tags'),track.id);
    assert.ok(preserved.public.some(t=>t.id===memberTag.id)&&preserved.public.some(t=>t.name==='Rock'),'Undo must preserve intervening real backend changes');
    await member.setViewportSize({width:390,height:844});
    if(process.env.MUSIC_SERVER_TAG_SCREENSHOTS){
      const directory=process.env.MUSIC_SERVER_TAG_SCREENSHOTS;require('node:fs').mkdirSync(directory,{recursive:true});
      await member.screenshot({path:require('node:path').join(directory,'tagger-mobile.png'),fullPage:true,animations:'disabled'});
      await member.setViewportSize({width:1400,height:900});
      await member.screenshot({path:require('node:path').join(directory,'tagger-desktop.png'),fullPage:true,animations:'disabled'});
      await member.setViewportSize({width:390,height:844});
    }
    assert.ok(await member.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Tagger does not cause narrow-screen overflow');
    await member.close();
    assert.deepEqual(errors,[],'Application page errors');
    console.log('Live tagger passed: nonmodal selection/pin, immediate save/undo, alias Enter reuse, member public creation and governance boundary, track identity, narrow layout and upload draft isolation.');
  }finally{await browser.close()}
}
main().catch(error=>{console.error(error);process.exitCode=1});
