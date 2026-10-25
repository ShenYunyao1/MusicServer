const assert=require('node:assert/strict');
const fs=require('node:fs/promises');
const os=require('node:os');
const path=require('node:path');
const net=require('node:net');
const {spawn}=require('node:child_process');
const {waitForAsync}=require('./browser-wait.cjs');
const {chromium}=require(process.env.MUSIC_SERVER_PLAYWRIGHT||'playwright');

async function regression({page,directory}){
  const ordinaryFullReads=[];
  const observe=request=>{if(request.method()==='GET'&&/\/api\/v2\/(tracks|playlists)$/.test(request.url()))ordinaryFullReads.push(request.url())};
  page.on('request',observe);
  await page.evaluate(()=>location.hash='#/library');
  await page.waitForFunction(()=>state.page==='tracks'&&browseState.tracks?.total===102&&document.querySelectorAll('[data-library-track]').length===50);
  assert.equal(await page.locator('#trackCount').textContent(),'当前页 50 首 · 匹配 102 首 · 曲库 102 首');
  const first=page.locator('[data-library-track]').first();
  await first.evaluate(node=>window.browseFirstRow=node);
  await page.evaluate(()=>refreshCore());
  assert.equal(await first.evaluate(node=>node===window.browseFirstRow),true,'An unchanged refresh must reuse the row node');
  await page.locator('#selectAll').check();
  assert.equal(await page.evaluate(()=>state.selectedTrackIds.size),50);
  await page.locator('#browseTrackPager button').last().click();
  await page.waitForFunction(()=>browseState.tracks.offset===50&&state.selectedTrackIds.size===0);
  assert.equal(await page.locator('[data-library-track]').count(),50);
  assert.ok((await page.evaluate(()=>location.hash)).includes('offset=50'));
  await page.fill('#trackSearch','Browse 100');
  await page.waitForFunction(()=>browseState.tracks?.total===1&&document.querySelectorAll('[data-library-track]').length===1);
  assert.equal(await page.locator('#trackSearch').inputValue(),'Browse 100');
  assert.ok((await page.evaluate(()=>location.hash)).includes('q=Browse+100'));
  await page.locator('[data-details]').click();
  await page.locator('#trackDetails').waitFor({state:'visible'});
  assert.equal(await page.evaluate(()=>state.tracks.find(t=>t.title==='Browse 100')?.detail_loaded),true,'Details must load the complete single track');
  await page.locator('#trackDetails .modal-head button').click();
  await page.fill('#trackSearch','');await page.waitForFunction(()=>browseState.tracks.total===102);
  assert.deepEqual(ordinaryFullReads,[],'Ordinary views must not fetch complete catalogue or playlist lists');

  const fixture=await page.evaluate(async()=>{
    const first=await api('browse/tracks?limit=100'),last=await api('browse/tracks?offset=100&limit=100'),tracks=[...first.items,...last.items],ids=tracks.slice(0,101).map(t=>t.id);
    const p=await api('playlists','POST',{id:browserUUID(),name:'Paged private collection',track_ids:ids,track_notes:{[ids[50]]:'Personal page cue'}});
    const empty=await api('playlists','POST',{id:browserUUID(),name:'Empty picker',track_ids:[]});
    await refreshCore();return {id:p.id,empty:empty.id,ids};
  });
  await page.evaluate(id=>location.hash='#/playlists/'+id,fixture.id);
  await page.waitForFunction(id=>state.playlistDetailId===id&&browseState.member?.total===101&&document.querySelectorAll('.playlist-track').length===50,fixture.id);
  assert.equal(await page.evaluate(id=>state.playlists.find(p=>p.id===id).members_complete,fixture.id),false);
  assert.equal(await page.evaluate(id=>state.playlists.find(p=>p.id===id).track_ids,fixture.id),undefined);
  await page.locator('#browseMemberPager button').last().click();await page.waitForFunction(()=>browseState.member.offset===50);
  assert.equal(await page.locator('.playlist-sequence').first().textContent(),'51');
  await page.locator('.playlist-track').first().focus();await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));await page.keyboard.press('Shift+F10');
  await page.locator('#contextMenu').getByText('下移曲目',{exact:true}).click();
  await page.waitForFunction(id=>browseState.member.items[0].id===id&&!state.playlistTrackSortBusy,fixture.ids[51]);
  const ordered=await page.evaluate(async id=>(await api('playlists/'+id)).playlist.track_ids,fixture.id);
  const expected=fixture.ids.slice();[expected[50],expected[51]]=[expected[51],expected[50]];assert.deepEqual(ordered,expected,'Sorting a page must preserve all members outside the page');
  await page.locator('.playlist-track').first().evaluate(node=>window.browseMemberRow=node);
  await page.evaluate(async id=>{const other=state.playlists.find(p=>p.id===id);await api('playlists/'+id,'PATCH',{name:'Empty picker renamed'},undefined,other.version);await refreshCore()},fixture.empty);
  assert.equal(await page.locator('.playlist-track').first().evaluate(node=>node===window.browseMemberRow),true,'An unrelated revision must update the baseline without rebuilding the member rows');
  await page.evaluate(async id=>{const track=browseState.member.items.find(t=>t.id===id);await api('tracks/'+id,'DELETE',undefined,undefined,track.version);await refreshCore()},fixture.ids[50]);
  const placeholder=page.locator('[data-track-id="'+fixture.ids[50]+'"]');
  assert.ok((await placeholder.textContent()).includes('不可用曲目'));
  assert.ok((await placeholder.textContent()).includes('Personal page cue'));
  const view=await page.evaluate(id=>browseState.member.items.find(t=>t.id===id),fixture.ids[50]);
  assert.equal(view.owner_id,undefined);assert.equal(view.artist,undefined);assert.equal(view.lyrics,undefined);
  await page.fill('#playlistTrackSearch','Browse 100');await page.waitForFunction(()=>browseState.member.total===1);
  assert.equal(await page.locator('.playlist-track').count(),1);
  await page.fill('#playlistTrackSearch','');await page.waitForFunction(()=>browseState.member.total===101);
  await page.evaluate(async id=>{const track=(await api('tracks/'+id));await api('tracks/'+id,'PATCH',{genre:'GenreOnlyNeedle'},undefined,track.version)},fixture.ids[3]);
  await page.fill('#playlistTrackSearch','GenreOnlyNeedle');await page.waitForFunction(()=>browseState.member.total===1&&document.querySelectorAll('.playlist-track').length===1);
  await page.fill('#playlistTrackSearch','');await page.waitForFunction(()=>browseState.member.total===101);
  await page.evaluate(id=>openPlaylistTrackPicker(state.playlists.find(p=>p.id===id)),fixture.empty);
  await page.waitForFunction(()=>document.querySelectorAll('#modalBody .key-grant-grid label').length===50);
  assert.equal(await page.locator('#browsePickerPager').isVisible(),true);
  await page.locator('#cancelModal').click();

  await page.evaluate(async()=>{for(let i=0;i<53;i++)await api('playlists','POST',{id:browserUUID(),name:'Paged card '+String(i).padStart(2,'0'),track_ids:[]});await refreshCore();location.hash='#/playlists'});
  await page.waitForFunction(()=>browseState.playlists.total===55&&document.querySelectorAll('.playlist-card').length===50);
  const before=await page.evaluate(async()=>[...(await api('browse/playlists?limit=100')).items].map(p=>p.id));
  await page.evaluate(async()=>{const ids=browseState.playlists.items.map(p=>p.id),last=ids.pop();ids.unshift(last);await savePlaylistOrder(ids)});
  const after=await page.evaluate(async()=>[...(await api('browse/playlists?limit=100')).items].map(p=>p.id));
  const wanted=before.slice();wanted.unshift(...wanted.splice(49,1));assert.deepEqual(after,wanted,'Card sorting must preserve the next page');
  await page.locator('#browsePlaylistPager button').last().click();await page.waitForFunction(()=>browseState.playlists.offset===50&&document.querySelectorAll('.playlist-card').length===5);
  await page.evaluate(ids=>openAddTracks(ids),[fixture.ids[0]]);
  await page.waitForFunction(()=>document.querySelectorAll('#addPlaylistTarget option').length===50);
  await page.locator('#browseTargetPager button').last().click();await page.waitForFunction(()=>document.querySelectorAll('#addPlaylistTarget option').length===5);await page.locator('#cancelModal').click();
  await page.setViewportSize({width:390,height:844});await page.waitForFunction(()=>document.querySelector('.main').getBoundingClientRect().left<=1&&document.querySelector('#sidebar').getBoundingClientRect().right<=1);assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
  if(directory)await page.screenshot({path:path.join(directory,'browse-paging-mobile.png'),animations:'disabled'});
  await page.evaluate(()=>location.hash='#/player/keys');await page.waitForFunction(()=>state.page==='keys'&&state.playlists.filter(p=>!p.system&&!p.subscription_status).length===55);
  assert.equal(await page.evaluate(()=>state.playlists.some(p=>p.id==='library'&&p.system)),true,'Global library access must remain an explicit grant');
  const epochGuard=await page.evaluate(async()=>{
    const original=api;let release;
    api=path=>path==='browse/summary'?new Promise(resolve=>release=resolve):original(path);
    try{const stale=refreshBrowseCore();clearBrowseWorkspace();browseReset();release({total:9999,owned:9999,missing:0,playlist_count:9999,revision:9999});await stale;return state.browseSummary===null&&browseState.summary===null}finally{api=original;await refreshCore()}
  });assert.equal(epochGuard,true,'Old responses must be rejected after a session reset even when the account ID is unchanged');
  page.off('request',observe);
  console.log('Browse UI passed: bounded 102-track pages/search, selection boundaries, lazy details, stable nodes, 101-member page order/privacy/notes, bounded picker and 55-card ordering.');
}
module.exports=regression;

async function main(){
  assert.ok(process.env.MUSIC_SERVER_TEST_BINARY,'Set MUSIC_SERVER_TEST_BINARY to a built server');
  const directory=await fs.mkdtemp(path.join(os.tmpdir(),'music-browse-ui-')),media=path.join(directory,'media');await fs.mkdir(media);
  const audio=Buffer.alloc(844);audio.write('RIFF');audio.writeUInt32LE(836,4);audio.write('WAVEfmt ',8);audio.writeUInt32LE(16,16);audio.writeUInt16LE(1,20);audio.writeUInt16LE(1,22);audio.writeUInt32LE(8000,24);audio.writeUInt32LE(16000,28);audio.writeUInt16LE(2,32);audio.writeUInt16LE(16,34);audio.write('data',36);audio.writeUInt32LE(800,40);
  await Promise.all(Array.from({length:102},(_,i)=>fs.writeFile(path.join(media,'Browse '+String(i).padStart(3,'0')+'.wav'),audio)));
  const listener=net.createServer();await new Promise(resolve=>listener.listen(0,'127.0.0.1',resolve));const port=listener.address().port;await new Promise(resolve=>listener.close(resolve));
  const server=spawn(process.env.MUSIC_SERVER_TEST_BINARY,['-addr','127.0.0.1:'+port,'-media-root',media,'-data',path.join(directory,'library.sqlite')],{windowsHide:true});let logs='';server.stderr.on('data',chunk=>logs+=chunk);
  let browser;
  try{
    const base='http://127.0.0.1:'+port;for(let i=0;i<100;i++){try{if((await fetch(base+'/api/v2/health')).ok)break}catch{}await new Promise(resolve=>setTimeout(resolve,100))}
    const token=(await fs.readFile(path.join(directory,'setup-token'),'utf8')).trim();browser=await chromium.launch({headless:true,...(process.env.MUSIC_SERVER_BROWSER?{executablePath:process.env.MUSIC_SERVER_BROWSER}:{})});
    const page=await browser.newPage({viewport:{width:1440,height:1100}}),errors=[];page.on('pageerror',error=>errors.push(error.message));
    await page.goto(base+'/#setup='+encodeURIComponent(token));await page.fill('#setupPassword','isolated browse password');await page.fill('#setupConfirm','isolated browse password');await page.click('#setupSubmit');await page.locator('#loginScreen').waitFor();await page.fill('#username','admin');await page.fill('#password','isolated browse password');await page.click('#loginButton');await page.waitForFunction(()=>state.user?.username==='admin');
    await waitForAsync(page,async()=>{const scan=await api('admin/scan/status');return scan.state==='completed'},null,{timeout:120000});await page.evaluate(()=>refreshCore());
    await regression({page,directory});assert.deepEqual(errors,[]);console.log('Artifacts: '+directory);
  }catch(error){console.error(logs);throw error}finally{await browser?.close();server.kill()}
}
if(require.main===module)main().catch(error=>{console.error(error);process.exitCode=1});
