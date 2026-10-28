'use strict';
const assert=require('node:assert/strict');
const path=require('node:path');
const {chromium}=require(process.env.MUSIC_SERVER_PLAYWRIGHT||'playwright');
async function fixture(browser,enabled=true){
  const page=await browser.newPage({viewport:{width:1000,height:780}});
  await page.goto('about:blank');
  await page.setContent('<!doctype html><html lang="zh-CN"><body><section id="page-tracks"><div class="track-toolbar"><input id="trackSearch"><input id="playlistSearch"><input id="selectAll" type="checkbox"></div><div id="trackRows"></div></section><div id="status"></div></body></html>');
  await page.evaluate(enabled=>{
    window.state={user:{id:'alice',role:'member'},trackFilter:'all',tracks:[],playlists:[],selectedTrackIds:new Set(),page:'tracks'};
    window.$=id=>document.getElementById(id);window.browsePageSize=50;
    window.browseState={epoch:0,generations:{tracks:0},tracks:null};window.browseReset=()=>{};
    window.browseReuse=items=>items;window.browseCacheTracks=()=>state.tracks=browseState.tracks?.items||[];window.browseCachePlaylists=()=>{};
    window.renderTracks=()=>{};window.setStatus=(text)=>$('status').textContent=text;
    window.tagWorkspace={groups:[{id:'genre',name:'风格'},{id:'mood',name:'情绪'}]};window.tagFeatureEnabled=()=>enabled&&Boolean(state.user);window.loadTagCapability=async()=>tagFeatureEnabled();
    window.calls=[];window.presets=[];window.nextPreset=1;window.snapshotFail=false;
    window.baseline={account_id:'alice',library:1,public:1,personal:1};
    window.api=async(path,method='GET',body,key,version)=>{
      const owner=state.user?.id;
      calls.push({path,method,body,version});
      if(path==='track-query'){
        if(body.filter.q==='old'&&window.delayOldQuery)await new Promise(resolve=>window.releaseOldQuery=resolve);
        if(snapshotFail&&body.baseline){snapshotFail=false;throw Object.assign(new Error('changed'),{code:'FILTER_SNAPSHOT_CHANGED',status:409})}
        if(window.queryFailure)throw Object.assign(new Error('invalid reference'),{code:'FILTER_REFERENCE_INVALID',status:409});
        return {items:[{id:'song',title:body.filter.q||'Filtered song',version:1}],total:75,offset:body.offset,limit:body.limit,baseline:structuredClone(baseline),redirects:window.redirects||[]};
      }
      if(path==='track-facets')return {items:[{tag:{id:'rock',scope:body.scope,group_id:body.group_id,name:'Rock'},count:7}],total:1,offset:0,limit:50,baseline:structuredClone(baseline),redirects:window.facetRedirects||[]};
      if(path==='saved-track-filters'&&method==='GET'){if(window.delayPresetListOnce){window.delayPresetListOnce=false;await new Promise(resolve=>window.releasePresetList=resolve)}return structuredClone(presets.filter(p=>!p.owner_id||p.owner_id===owner))}
      if(path==='saved-track-filters'&&method==='POST'){const p={id:'p'+nextPreset++,name:body.name,filter:body.filter,version:1,validation:{valid:true,resolved_filter:body.filter,redirects:[]}};presets.push(p);return structuredClone(p)}
      if(path.startsWith('saved-track-filters/')){const p=presets.find(p=>p.id===path.split('/')[1]);if(method==='GET')return structuredClone(p);if(method==='DELETE'){presets=presets.filter(x=>x!==p);return}if(p.version!==version)throw Object.assign(new Error('conflict'),{code:'VERSION_CONFLICT',status:409});Object.assign(p,body);p.version++;return structuredClone(p)}
      throw new Error('unexpected '+method+' '+path);
    };
  },enabled);
  await page.addScriptTag({path:path.resolve(__dirname,'../internal/music/track-filter-ui.js')});
  await page.addStyleTag({path:path.resolve(__dirname,'../internal/music/management.css')});
  await page.addStyleTag({path:path.resolve(__dirname,'../internal/music/track-filter-ui.css')});
  return page;
}
async function main(){const browser=await chromium.launch({headless:true,executablePath:process.env.MUSIC_SERVER_BROWSER});try{
  let page=await fixture(browser,false);assert.equal(await page.evaluate(()=>initializeTrackFilters()),false);assert.equal(await page.locator('#trackAdvancedFilter').count(),0);await page.close();
  page=await fixture(browser);await page.evaluate(()=>initializeTrackFilters());await page.locator('#trackAdvancedFilter').click();
  await page.locator('#filterArtists').fill('Artist one\nArtist, two');await page.locator('#filterHasCover').selectOption('yes');
  await page.locator('[data-filter-term="rock"]').selectOption('include');await page.locator('#filterApply').click();
  const sent=await page.evaluate(()=>calls.filter(c=>c.path==='track-query').at(-1).body);
  assert.deepEqual(sent.filter.artists,['Artist one','Artist, two']);assert.equal(sent.filter.has_cover,true);assert.deepEqual(sent.filter.tags[0].include_ids,['rock']);
  assert.equal(await page.evaluate(()=>JSON.parse(new URLSearchParams(location.hash.split('?')[1]).get('tf')).version),1);
  assert.ok(await page.locator('#trackFilterChips').textContent());
  await page.evaluate(async()=>{snapshotFail=true;await loadFilteredTracks(50)});assert.equal(await page.evaluate(()=>browseState.tracks.offset),0);assert.match(await page.locator('#status').textContent(),/更新|变化/);
  await page.locator('#trackAdvancedFilter').click();await page.locator('#savedFilterName').fill('My filter');await page.locator('#filterSavePreset').click();
  await page.waitForFunction(()=>presets.length===1);await page.locator('#savedTrackFilter').selectOption('p1');await page.locator('#savedFilterName').fill('Renamed');await page.locator('#filterRenamePreset').click();await page.waitForFunction(()=>presets[0].name==='Renamed');
  await page.evaluate(()=>presets[0].validation={valid:false,code:'FILTER_REFERENCE_INVALID'});await page.locator('#filterApplyPreset').click();assert.match(await page.locator('#filterDialogMessage').textContent(),/失效/);
  await page.locator('#filterDeletePreset').click();await page.waitForFunction(()=>presets.length===0);
  await page.setViewportSize({width:390,height:850});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
  await page.evaluate(()=>{state.user=null;clearTrackFilterWorkspace()});assert.equal(await page.locator('#trackFilterDialog').count(),0);assert.equal(await page.locator('#trackAdvancedFilter').count(),0);
  await page.close();
  page=await fixture(browser);await page.evaluate(async()=>{await initializeTrackFilters();queryFailure=true;try{await loadFilteredTracks(0)}catch(_){}});assert.match(await page.locator('#trackFilterMessage').textContent(),/失效|invalid/);assert.equal(await page.evaluate(()=>state.tracks.length),0);await page.close();
  page=await fixture(browser);await page.evaluate(async()=>{await initializeTrackFilters();history.replaceState(null,'','#/library?tf='+encodeURIComponent('{"version":1,"tags":{}}'));await initializeTrackFilters()});assert.match(await page.locator('#trackFilterMessage').textContent(),/无效/);await page.getByRole('button',{name:'清除无效条件'}).click();await page.waitForFunction(()=>browseState.tracks?.total===75);await page.close();
  page=await fixture(browser);await page.evaluate(async()=>{await initializeTrackFilters();delayOldQuery=true;$('trackSearch').value='old';window.oldQuery=loadFilteredTracks(0)});await page.waitForFunction(()=>typeof releaseOldQuery==='function');await page.evaluate(async()=>{$('trackSearch').value='new';await loadFilteredTracks(0);releaseOldQuery();await oldQuery});assert.equal(await page.evaluate(()=>browseState.tracks.items[0].title),'new');await page.close();
  page=await fixture(browser);await page.evaluate(async()=>{await initializeTrackFilters();presets=[{id:'invalid',name:'Needs repair',version:1,filter:{version:1,range:'all',tags:[{scope:'personal',group_id:'genre',include_ids:['deleted'],exclude_ids:[]}]},validation:{valid:false,code:'FILTER_REFERENCE_INVALID'}}]});await page.locator('#trackAdvancedFilter').click();await page.locator('#savedTrackFilter').selectOption('invalid');await page.locator('#filterEditPreset').click();await page.waitForFunction(()=>document.querySelector('#filterDraftTags').children.length===1);assert.equal(await page.evaluate(()=>calls.filter(c=>c.path==='track-query').length),0);assert.equal(await page.evaluate(()=>trackFilterWorkspace.editor.filter.tags[0].include_ids[0]),'deleted');await page.locator('#filterDraftTags button').click();await page.locator('#filterUpdatePreset').click();await page.waitForFunction(()=>presets[0].filter.tags.length===0);await page.close();
  page=await fixture(browser);await page.evaluate(async()=>{await initializeTrackFilters();facetRedirects=[{from:'old',to:'rock'}];await openTrackFilterEditor({filter:{version:1,tags:[{scope:'public',group_id:'genre',include_ids:['old'],exclude_ids:[]}]}})});assert.equal(await page.locator('[data-filter-term="rock"]').inputValue(),'include');assert.match(await page.locator('#filterDialogMessage').textContent(),/合并/);assert.equal(await page.evaluate(()=>calls.filter(c=>c.path==='track-query').length),0);await page.close();
  page=await fixture(browser);await page.evaluate(async()=>{await initializeTrackFilters();await openTrackFilterEditor({filter:{version:1,tags:[{scope:'personal',group_id:'mood',include_ids:['private']},{scope:'public',group_id:'mood',exclude_ids:['other']},{scope:'personal',group_id:'genre',include_ids:[],exclude_ids:[]}]}})});await page.locator('[data-filter-term="rock"]').selectOption('include');assert.equal(await page.evaluate(()=>trackFilterWorkspace.editor.filter.tags.length),4);assert.equal(await page.locator('#filterDraftTags button').count(),4);await page.close();
  page=await fixture(browser);await page.evaluate(async()=>{await initializeTrackFilters();presets=[{id:'a',owner_id:'alice',name:'Alice secret name',filter:{version:1,range:'all'},version:1,validation:{valid:true}}];await openTrackFilterEditor();$('savedTrackFilter').value='a';delayPresetListOnce=true;window.pendingEdit=editSavedTrackFilter(trackFilterWorkspace.editor)});await page.waitForFunction(()=>typeof releasePresetList==='function');await page.evaluate(async()=>{clearTrackFilterWorkspace();state.user={id:'bob',role:'member'};await initializeTrackFilters();await openTrackFilterEditor();$('savedFilterName').value='Bob draft';releasePresetList();await pendingEdit});assert.equal(await page.locator('#savedFilterName').inputValue(),'Bob draft');await page.close();
  page=await fixture(browser);await page.evaluate(()=>{window.editTrack=()=>{};window.showTrackMenu=()=>{};window.openAddTracks=()=>{};window.openPlaylistTrackPicker=()=>{};window.updateTrackSelection=()=>{};window.renderCount=0;window.renderTracks=()=>renderCount++;window.renderPlaylists=()=>{};window.renderOverview=()=>{};const original=api;window.api=async(path,...args)=>path==='health'?{revision:2}:path==='browse/summary'?{revision:2,total:75,playlist_count:0}:original(path,...args)});await page.addScriptTag({path:path.resolve(__dirname,'../internal/music/browse-ui.js')});await page.evaluate(async()=>{browseState.userID='alice';await initializeTrackFilters();await loadBrowseTracks(0);state.revision=1;baseline.library=2;await pollBrowseChanges()});assert.deepEqual(await page.evaluate(()=>({renders:renderCount,revision:state.revision})),{renders:1,revision:2});await page.close();
  for(const nextID of ['alice','bob']){
    page=await fixture(browser);await page.evaluate(async()=>{await initializeTrackFilters();presets=[{id:'a',owner_id:'alice',name:'Alice private draft',filter:{version:1,range:'all'},version:1,validation:{valid:true}}];await openTrackFilterEditor();$('savedTrackFilter').value='a';delayPresetListOnce=true;window.pendingEdit=editSavedTrackFilter(trackFilterWorkspace.editor)});await page.waitForFunction(()=>typeof releasePresetList==='function');await page.evaluate(async id=>{clearTrackFilterWorkspace();state.user=null;state.user={id,role:'member'};await initializeTrackFilters();await openTrackFilterEditor();$('savedFilterName').value='Fresh session draft';releasePresetList();await pendingEdit},nextID);assert.equal(await page.locator('#savedFilterName').inputValue(),'Fresh session draft');await page.close();
    page=await fixture(browser);await page.evaluate(async()=>{await initializeTrackFilters();window.originalCapability=loadTagCapability;window.loadTagCapability=()=>new Promise(resolve=>window.releaseCapability=resolve);window.pendingOpen=openTrackFilterEditor({filter:{version:1,tags:[{scope:'personal',group_id:'genre',include_ids:['private-a']}]}})});await page.waitForFunction(()=>typeof releaseCapability==='function');await page.evaluate(async id=>{clearTrackFilterWorkspace();state.user=null;state.user={id,role:'member'};loadTagCapability=originalCapability;await initializeTrackFilters();await openTrackFilterEditor();$('savedFilterName').value='Current editor';releaseCapability(true);await pendingOpen},nextID);assert.equal(await page.locator('#savedFilterName').inputValue(),'Current editor');assert.equal(await page.evaluate(()=>Boolean(trackFilterWorkspace.editor.filter.tags?.some(c=>c.include_ids?.includes('private-a')))),false);await page.close();
  }
  console.log('track filter browser contracts passed');
}finally{await browser.close()}}
main().catch(error=>{console.error(error);process.exitCode=1});
