'use strict';
const assert=require('node:assert/strict'),path=require('node:path');
const {chromium}=require(process.env.MUSIC_SERVER_PLAYWRIGHT||'playwright');
async function main(){const browser=await chromium.launch({headless:true,executablePath:process.env.MUSIC_SERVER_BROWSER});try{
 const page=await browser.newPage({viewport:{width:390,height:850}});await page.setContent('<body><div id="host"></div><dialog id="draft"><input value="unsaved"></dialog></body>');
 await page.evaluate(()=>{
  window.state={user:{id:'alice'},page:'tracks',tracks:[]};window.$=id=>document.getElementById(id);window.tagFeatureEnabled=()=>Boolean(state.user);window.trackFiltersEnabled=tagFeatureEnabled;
  window.tagWorkspace={groups:[],generation:1};window.trackFilterWorkspace={labels:new Map([['old','Old']]),baseline:{},editor:null};
  window.browseState={generations:{tracks:0},tracks:{offset:50}};window.revision={account_id:'alice',library:1,public:1,personal:1,saved:'one'};window.calls=[];window.renders=0;window.applied=null;
  window.api=async name=>{calls.push(name);if(window.delay)await new Promise(resolve=>window.release=resolve);return structuredClone(name==='tag-groups'?[]:revision)};
  window.loadBrowseTracks=async offset=>{calls.push('tracks:'+offset);return {items:[]}};window.renderTracks=()=>renders++;window.applyTrackTagFilter=async tag=>applied=tag;window.setStatus=()=>{};
 });
 await page.addScriptTag({path:path.resolve(__dirname,'../internal/music/tag-ui.js')});
 await page.addStyleTag({path:path.resolve(__dirname,'../internal/music/tag-ui.css')});
 await page.evaluate(async()=>{await pollTagChanges();revision.personal++;await pollTagChanges()});
 assert.equal(await page.evaluate(()=>renders),1);assert.ok(await page.evaluate(()=>calls.includes('tracks:0')));
 await page.evaluate(async()=>{$('draft').showModal();revision.saved='two';await pollTagChanges()});assert.equal(await page.locator('#draft input').inputValue(),'unsaved');assert.equal(await page.evaluate(()=>renders),1);
 await page.evaluate(async()=>{$('draft').close();await pollTagChanges()});assert.equal(await page.evaluate(()=>renders),1);assert.equal(await page.evaluate(()=>tagSyncWorkspace.baseline.saved),'two');
 await page.evaluate(async()=>{revision.personal++;await pollTagChanges()});assert.equal(await page.evaluate(()=>renders),2);
 await page.evaluate(async()=>{clearTagSyncWorkspace();browseState.tracks.baseline={...revision,personal:revision.personal-1};$('draft').showModal();await pollTagChanges()});
 assert.equal(await page.evaluate(()=>renders),2);assert.equal(await page.evaluate(()=>tagSyncWorkspace.baseline),null);
 await page.evaluate(async()=>{$('draft').close();await pollTagChanges()});assert.equal(await page.evaluate(()=>renders),3);
 await page.evaluate(()=>{const summary={items:[{id:'rock',name:'Rock',scope:'public',group_id:'genre'},{id:'a',name:'<img src=x onerror=alert(1)>',scope:'personal',group_id:'mood'}],total:8};$('host').append(renderTrackTagSummary({tag_summary:summary}))});
 assert.equal(await page.locator('#host img').count(),0);assert.match(await page.locator('#host').textContent(),/\+6/);await page.locator('#host button').first().click();assert.equal(await page.evaluate(()=>applied.id),'rock');
 assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
 await page.evaluate(async()=>{delay=true;window.pending=pollTagChanges()});await page.waitForFunction(()=>typeof release==='function');await page.evaluate(async()=>{clearTagSyncWorkspace();state.user={id:'bob'};release();await pending});assert.equal(await page.evaluate(()=>tagSyncWorkspace.baseline),null);
 console.log('tag UI synchronization and summary contracts passed');
}finally{await browser.close()}}
main().catch(error=>{console.error(error);process.exitCode=1});
