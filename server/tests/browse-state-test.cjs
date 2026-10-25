const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const source=fs.readFileSync(path.join(__dirname,'../internal/music/browse-ui.js'),'utf8');
function functionSource(start,end){return source.slice(source.indexOf(start),source.indexOf(end,source.indexOf(start)))}
test('failed current-page read retries without another server revision',async()=>{
  let loads=0,updated=false;
  const context={location:{hash:'#/library?offset=50'},state:{user:{id:'a'},page:'tracks',revision:1},browseState:{epoch:1,generations:{tracks:1},tracks:{offset:50}},browseRefreshBlocked:()=>false,browseReset:()=>{},
    api:async endpoint=>endpoint==='health'?{revision:2}:{revision:2,total:102},
    loadBrowseTracks:async offset=>{assert.equal(offset,50);if(++loads===1)throw new Error('transient disconnect');updated=true;return {items:[],revision:2}},renderTracks:()=>{},renderPlaylists:()=>{},renderOverview:()=>{}};
  vm.createContext(context);vm.runInContext(functionSource('async function pollBrowseChanges()',"$('trackSearch').oninput"),context);
  await context.pollBrowseChanges();assert.equal(context.state.revision,1);assert.equal(updated,false);
  await context.pollBrowseChanges();assert.equal(loads,2);assert.equal(updated,true);assert.equal(context.state.revision,2);
});
test('older cached member projection cannot replace a newer catalogue version',()=>{
  const context={state:{tracks:[],page:'playlists',playlistDetailId:'p'},browseState:{tracks:{items:[{id:'a',version:4,title:'new'}]},member:{id:'p',items:[{id:'a',version:3,title:'old'},{id:'b',version:1,title:'member'}]}}};
  vm.createContext(context);vm.runInContext(functionSource('function browseCacheTracks()','function browseCachePlaylists()'),context);context.browseCacheTracks();
  assert.equal(context.state.tracks.find(t=>t.id==='a').version,4);assert.equal(context.state.tracks.length,2);
  context.state.page='tracks';context.state.playlistDetailId=null;context.browseCacheTracks();
  assert.equal(context.state.tracks.length,1);assert.equal(context.state.tracks[0].id,'a');
});
test('a deleted previous playlist response cannot clear the newly opened playlist',async()=>{
  let rejectPage,started,clears=0;
  const pending=new Promise((_,reject)=>{rejectPage=reject}),loading=new Promise(resolve=>{started=resolve});
  const context={location:{hash:'#/playlists/p'},state:{user:{id:'a'},page:'playlists',revision:1,playlistDetailId:'p',playlists:[{id:'p'},{id:'q'}]},
    browseState:{epoch:1,generations:{member:1},member:{id:'p',offset:0},playlists:{items:[{id:'p'},{id:'q'}]}},browseRefreshBlocked:()=>false,browseReset:()=>{},
    api:async()=>({revision:2}),loadBrowseMembers:()=>{started();return pending},renderPlaylists:()=>{},renderOverview:()=>{},browseCacheTracks:()=>{},setStatus:()=>{},$:()=>({replaceChildren(){clears++}})};
  vm.createContext(context);vm.runInContext(functionSource('async function pollBrowseChanges()',"$('trackSearch').oninput"),context);
  const poll=context.pollBrowseChanges();await loading;
  context.location.hash='#/playlists/q';context.state.playlistDetailId='q';context.browseState.generations.member++;context.browseState.member={id:'q'};
  rejectPage(Object.assign(new Error('previous playlist unavailable'),{status:404}));await poll;
  assert.equal(clears,0);assert.equal(context.browseState.member.id,'q');assert.equal(context.state.playlists.some(p=>p.id==='q'),true);assert.equal(context.state.revision,1);
});
for(const stale of ['null','generation'])test('a '+stale+' current-page result cannot advance the refresh baseline',async()=>{
  let resolvePage,started,renders=0;
  const pending=new Promise(resolve=>{resolvePage=resolve}),loading=new Promise(resolve=>{started=resolve});
  const context={location:{hash:'#/library'},state:{user:{id:'a'},page:'tracks',revision:1},browseState:{epoch:1,generations:{tracks:1},tracks:{offset:0}},
    browseRefreshBlocked:()=>false,browseReset:()=>{},api:async()=>({revision:2}),loadBrowseTracks:()=>{started();return pending},renderTracks:()=>{renders++},renderOverview:()=>{}};
  vm.createContext(context);vm.runInContext(functionSource('async function pollBrowseChanges()',"$('trackSearch').oninput"),context);
  const poll=context.pollBrowseChanges();await loading;
  if(stale==='generation')context.browseState.generations.tracks++;
  resolvePage(stale==='null'?null:{revision:2,items:[]});await poll;
  assert.equal(context.state.revision,1);assert.equal(renders,0);
});
test('a successful member poll accounts for the loader generation and commits the baseline',async()=>{
  let renders=0;
  const context={location:{hash:'#/playlists/p'},state:{user:{id:'a'},page:'playlists',revision:1,playlistDetailId:'p'},browseState:{epoch:1,generations:{member:1},member:{id:'p',offset:50}},
    browseRefreshBlocked:()=>false,browseReset:()=>{},api:async()=>({revision:2}),loadBrowseMembers:async(id,offset)=>{assert.equal(id,'p');assert.equal(offset,50);context.browseState.generations.member++;return {revision:2,items:[]}},renderPlaylists:()=>{renders++},renderOverview:()=>{}};
  vm.createContext(context);vm.runInContext(functionSource('async function pollBrowseChanges()',"$('trackSearch').oninput"),context);await context.pollBrowseChanges();
  assert.equal(renders,1);assert.equal(context.state.revision,2);
});
