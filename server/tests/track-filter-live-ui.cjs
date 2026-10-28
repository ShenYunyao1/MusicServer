'use strict';
// Real API and browser integration. No replacement of application state/API.
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const {chromium}=require(process.env.MUSIC_SERVER_PLAYWRIGHT||'playwright');
async function main(){
  const base=new URL(process.env.MUSIC_SERVER_FILTER_LIVE_URL||'http://invalid');
  assert.ok(['127.0.0.1','localhost','[::1]'].includes(base.hostname),'Only an isolated loopback fixture is allowed');
  const browser=await chromium.launch({headless:true,executablePath:process.env.MUSIC_SERVER_BROWSER});
  const page=await browser.newPage({viewport:{width:1200,height:900}}),errors=[];page.on('pageerror',error=>errors.push(error.message));
  try{
    await page.goto(base.href.replace(/\/$/,'')+'/#/library');await page.waitForFunction(()=>!document.querySelector('#username').disabled);
    await page.fill('#username','admin');await page.fill('#password','test administrator password');await page.click('#loginButton');
    await page.locator('#trackAdvancedFilter').waitFor({state:'visible'});
    await page.waitForFunction(()=>browseState.tracks?.filterKey&&browseState.tracks.total===3);
    const labels=await page.evaluate(async()=>({public:(await api('tags?scope=public&limit=100')).items,personal:(await api('tags?scope=personal&limit=100')).items}));
    const rock=labels.public.find(tag=>tag.name==='Rock'),personal=labels.personal.find(tag=>tag.name==='My music');assert.ok(rock&&personal);
    await page.locator('#trackAdvancedFilter').click();
    const rockChoice=page.locator('[data-filter-term="'+rock.id+'"]');await rockChoice.waitFor({state:'visible'});
    assert.match(await rockChoice.locator('..').textContent(),/2/);await rockChoice.selectOption('include');
    await page.locator('#filterScope').selectOption('personal');const personalChoice=page.locator('[data-filter-term="'+personal.id+'"]');await personalChoice.waitFor({state:'visible'});
    assert.match(await personalChoice.locator('..').textContent(),/1/);await personalChoice.selectOption('include');
    await page.locator('#savedFilterName').fill('Rock and my music');await page.locator('#filterSavePreset').click();
    await page.waitForFunction(()=>document.querySelector('#savedTrackFilter').value!=='');
    const presetID=await page.locator('#savedTrackFilter').inputValue();
    await page.locator('#filterApply').click();await page.waitForFunction(()=>browseState.tracks?.total===1&&visibleTracks()[0]?.title==='Gamma');
    const applied=await page.evaluate(()=>getActiveTrackFilter());assert.equal(applied.tags.length,2);assert.ok((await page.locator('#trackFilterChips').textContent()).includes('我的'));
    const url=page.url();assert.ok(new URLSearchParams(url.split('?')[1]).get('tf'));
    await page.reload();await page.locator('#trackAdvancedFilter').waitFor({state:'visible'});await page.waitForFunction(()=>browseState.tracks?.total===1&&visibleTracks()[0]?.title==='Gamma');
    assert.deepEqual(await page.evaluate(()=>getActiveTrackFilter()),applied,'Reload must restore the structured rule');
    await page.evaluate(async()=>{await openTrackTagEditor([visibleTracks()[0].id],{range:'all'})});
    await page.waitForFunction(()=>tagWorkspace.editor?.job?.total===1);assert.equal(await page.evaluate(()=>tagWorkspace.editor.job.items[0].track_id),await page.evaluate(()=>visibleTracks()[0].id));await page.locator('#tagEditorDialog').getByRole('button',{name:'关闭',exact:true}).first().click();
    await page.locator('#trackAdvancedFilter').click();await page.locator('#savedTrackFilter').selectOption(presetID);await page.locator('#savedFilterName').fill('Renamed filter');await page.locator('#filterRenamePreset').click();await page.waitForFunction(()=>document.querySelector('#savedTrackFilter').selectedOptions[0]?.textContent==='Renamed filter');
    await page.locator('#filterApplyPreset').click();await page.waitForFunction(()=>!document.querySelector('#trackFilterDialog').open&&browseState.tracks.total===1);
    await page.evaluate(async()=>{await api('tags','POST',{scope:'public',group_id:'genre',name:'Snapshot change'});await loadFilteredTracks(50)});
    assert.equal(await page.evaluate(()=>browseState.tracks.offset),0);assert.match(await page.locator('#status').textContent(),/更新|变化/);
    await page.locator('#trackAdvancedFilter').click();await page.locator('#savedTrackFilter').selectOption(presetID);await page.locator('#filterDeletePreset').click();await page.waitForFunction(()=>document.querySelector('#savedTrackFilter').options.length===1);
    await page.setViewportSize({width:390,height:850});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
    const screenshot=path.resolve(__dirname,'../build/tag-bridge/ui/track-filter-narrow.png');fs.mkdirSync(path.dirname(screenshot),{recursive:true});await page.screenshot({path:screenshot,fullPage:true});
    await page.locator('#trackFilterDialog').getByRole('button',{name:'关闭',exact:true}).first().click();
    await page.goto(base.href.replace(/\/$/,'')+'/#/library?filter=missing');await page.reload();await page.locator('#trackAdvancedFilter').waitFor({state:'visible'});await page.waitForFunction(()=>browseState.tracks?.filterKey);
    assert.equal(await page.evaluate(()=>state.trackFilter),'missing','Reload must not broaden the admin missing-file range');assert.equal(await page.evaluate(()=>browseState.tracks.total),0);assert.equal(new URLSearchParams(page.url().split('?')[1]).get('filter'),'missing');
    assert.deepEqual(errors,[],'No application page errors');
    console.log('Live track filters passed: public/personal facets, structured query, saved CRUD, URL reload, frozen filtered batch, snapshot restart, narrow layout.');
  }finally{await browser.close()}
}
main().catch(error=>{console.error(error);process.exitCode=1});
