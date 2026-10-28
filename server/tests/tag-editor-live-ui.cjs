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
    await page.locator('#tagEditorDialog').waitFor({state:'visible'});
    assert.equal(await page.locator('#tagScope').inputValue(),'personal');
    await page.locator('#tagGroup').selectOption(label.group_id);
    await page.locator('[data-tag-operation="'+label.id+'"]').waitFor({state:'visible'});
    await page.locator('[data-tag-operation="'+label.id+'"]').selectOption('add');
    assert.deepEqual(await page.evaluate(id=>api('tracks/'+id+'/tags'),track.id),before.tags,'Selecting tags cannot change stored relationships');
    await page.click('#tagEditorSave');
    await page.locator('#tagEditorStart').waitFor({state:'visible'});
    assert.deepEqual(await page.evaluate(id=>api('tracks/'+id+'/tags'),track.id),before.tags,'Preview/PATCH cannot apply a relationship');
    await page.click('#tagEditorStart');
    await page.waitForFunction(()=>document.querySelector('#tagEditorMessage').textContent.includes('已完成'));
    const after=await page.evaluate(async id=>({tags:await api('tracks/'+id+'/tags'),track:await api('tracks/'+id)}),track.id);
    assert.ok(after.tags.personal.some(tag=>tag.id===label.id),'Confirmed UI mutation must reach the real store');
    assert.equal(after.tags.personal_version,before.tags.personal_version+1);
    assert.equal(after.tags.public_version,before.tags.public_version);
    for(const field of ['id','path','content_hash','genre','version','uploaded_by','owner_id'])assert.deepEqual(after.track[field],before.track[field],'Personal tag editing cannot change track '+field);
    await page.locator('#tagEditorDialog').getByRole('button',{name:'关闭',exact:true}).first().click();

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
    assert.deepEqual(errors,[],'Application page errors');
    console.log('Live tag editor passed: real login, menu/detail entry, frozen preview/PATCH/start, stored personal tag, track identity, upload draft isolation.');
  }finally{await browser.close()}
}
main().catch(error=>{console.error(error);process.exitCode=1});
