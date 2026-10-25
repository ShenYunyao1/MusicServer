'use strict';

const browseState={userID:null,epoch:0,summary:null,tracks:null,playlists:null,member:null,generations:{tracks:0,playlists:0,member:0},timers:{},polling:false};
const browsePageSize=50;
function clearBrowseWorkspace(){
  browseState.epoch++;browseState.userID=null;browseState.summary=null;browseState.tracks=null;browseState.playlists=null;browseState.member=null;state.browseSummary=null;
  for(const kind of ['tracks','playlists','member']){browseState.generations[kind]++;clearTimeout(browseState.timers[kind])}
}
function browseReset(){
  if(browseState.userID===state.user?.id)return;
  browseState.epoch++;
  browseState.userID=state.user?.id||null;browseState.summary=null;browseState.tracks=null;browseState.playlists=null;browseState.member=null;
  for(const kind of ['tracks','playlists','member']){browseState.generations[kind]++;clearTimeout(browseState.timers[kind])}
}
function browseRouteQuery(){return new URLSearchParams(location.hash.split('?')[1]||'')}
function browseRouteOffset(){return Math.max(0,Number.parseInt(browseRouteQuery().get('offset'),10)||0)}
function browseSetRoute(query,offset,filter){
  const values=new URLSearchParams();if(query)values.set('q',query);if(offset)values.set('offset',offset);if(filter&&filter!=='all')values.set('filter',filter);
  history.replaceState(null,'',location.hash.split('?')[0]+(values.size?'?'+values:''));
}
function browseReuse(items,old=[]){const byID=new Map(old.map(item=>[item.id,item]));return items.map(item=>{const previous=byID.get(item.id);return previous&&JSON.stringify(previous)===JSON.stringify(item)?previous:item})}
function browseCacheTracks(){
  const previous=new Map(state.tracks.map(t=>[t.id,t])),activeMember=state.page==='playlists'&&state.playlistDetailId===browseState.member?.id;
  const items=[...(browseState.tracks?.items||[]),...(activeMember?browseState.member.items:[])],unique=new Map();
  for(const item of items){
    // A cached member page may predate a freshly read catalogue page. Preserve
    // the newest version instead of overwriting it with the older projection.
    const selected=unique.get(item.id);if(selected&&selected.version>=item.version)continue;
    const full=previous.get(item.id);unique.set(item.id,full?.detail_loaded&&full.version===item.version?full:item);
  }
  state.tracks=[...unique.values()];
}
function browseCachePlaylists(){
  const library={id:'library',name:'全局曲库',system:true,track_ids:[],track_count:browseState.summary?.total||0,members_complete:false,version:1};
  const detail=browseState.member?.playlist,own=(browseState.playlists?.items||[]).map(p=>detail?.id===p.id&&detail.version>=p.version?detail:p),subscriptions=state.playlists.filter(p=>p.subscription_status);
  state.playlists=[library,...own,...(detail&&!own.some(p=>p.id===detail.id)?[detail]:[]),...subscriptions];
}
async function browseFetchPage(kind,path,params={}){
  browseReset();const generation=++browseState.generations[kind],userID=state.user?.id,route=location.hash.split('?')[0];
  let values=new URLSearchParams({limit:String(browsePageSize),offset:String(params.offset||0)});
  for(const [key,value]of Object.entries(params))if(value!==undefined&&value!==null&&value!=='')values.set(key,String(value));
  let page;
  try{page=await api(path+'?'+values)}catch(error){
    if(error.code!=='BROWSE_RESTART')throw error;
    values.delete('revision');values.set('offset','0');page=await api(path+'?'+values);
    if(generation===browseState.generations[kind]&&route===location.hash.split('?')[0])browseSetRoute(params.q||'',0,params.filter);
  }
  if(generation!==browseState.generations[kind]||userID!==state.user?.id)return null;
  const previous=browseState[kind];page.items=browseReuse(page.items||[],previous?.items);page.query=params.q||'';page.filter=params.filter||'all';
  browseState[kind]=page;
  if(kind==='member'){
    page.id=path.split('/')[2];
    const existing=state.playlists.find(p=>p.id===page.playlist.id);
    if(existing&&JSON.stringify(existing)===JSON.stringify(page.playlist))page.playlist=existing;
  }
  browseCacheTracks();browseCachePlaylists();return page;
}
async function loadBrowseTracks(offset=browseRouteOffset(),revision){return browseFetchPage('tracks','browse/tracks',{q:$('trackSearch').value.trim(),filter:state.trackFilter||'all',offset,revision})}
async function loadBrowsePlaylists(offset=browseRouteOffset(),revision){return browseFetchPage('playlists','browse/playlists',{q:$('playlistSearch').value.trim(),offset,revision})}
async function loadBrowseMembers(id,offset=browseRouteOffset(),revision){
  state.playlistTrackQuery=browseRouteQuery().get('q')||'';state.playlistTrackSearchID=id;
  return browseFetchPage('member','browse/playlists/'+encodeURIComponent(id)+'/tracks',{q:(state.playlistTrackQuery||'').trim(),offset,revision});
}
async function refreshBrowseCore(){
  browseReset();const userID=state.user?.id,epoch=browseState.epoch;if(!userID)return;
  const summary=await api('browse/summary');if(state.user?.id!==userID||epoch!==browseState.epoch)return;
  browseState.summary=summary;state.browseSummary=summary;state.revision=summary.revision;
  await Promise.all([loadBrowseTracks(state.page==='tracks'?browseRouteOffset():browseState.tracks?.offset||0,summary.revision),loadBrowsePlaylists(state.page==='playlists'&&!state.playlistDetailId?browseRouteOffset():browseState.playlists?.offset||0,summary.revision)]);
  if(epoch!==browseState.epoch)return;
  if(state.page==='playlists'&&state.playlistDetailId&&!state.subscriptionsTab&&!state.invitationsTab&&!state.playlistCreating){
    const detailID=state.playlistDetailId;
    try{await loadBrowseMembers(detailID,browseRouteOffset())}catch(error){
      if(error.status!==404)throw error;
      if(epoch!==browseState.epoch||state.playlistDetailId!==detailID)return;
      browseState.member=null;state.playlistDetailId=null;
      if(location.hash.startsWith('#/playlists/'+detailID))history.replaceState(null,'','#/playlists');
      setStatus('歌单已不可用，已返回我的歌单。','error');
    }
  }
  if(epoch!==browseState.epoch)return;
  browseCachePlaylists();renderOverview();renderTracks();renderPlaylists();
}
async function loadBrowseView(){
  browseReset();
  if(state.page==='tracks'){await loadBrowseTracks();return}
  if(state.page==='playlists'&&!state.subscriptionsTab&&!state.invitationsTab&&!state.playlistCreating){
    if(state.playlistDetailId)await loadBrowseMembers(state.playlistDetailId);
    else{$('playlistSearch').value=browseRouteQuery().get('q')||'';await loadBrowsePlaylists()}
  }
}
async function loadBrowseGrantPlaylists(){
  browseReset();const userID=state.user?.id,epoch=browseState.epoch;let items=[],offset=0,revision;
  for(;;){const values=new URLSearchParams({offset:String(offset),limit:'100',summary_only:'1'});if(revision!==undefined)values.set('revision',revision);
    let page;try{page=await api('browse/playlists?'+values)}catch(error){if(error.code==='BROWSE_RESTART'){items=[];offset=0;revision=undefined;continue}throw error}
    if(state.user?.id!==userID||epoch!==browseState.epoch)return;
    items.push(...page.items);revision=page.revision;offset+=page.items.length;if(offset>=page.total||!page.items.length)break;
  }
  const subscriptions=await api('subscriptions');if(state.user?.id!==userID||epoch!==browseState.epoch)return;
  const existing=new Map(state.playlists.map(p=>[p.id,p]));state.playlists=[state.playlists.find(p=>p.system)||{id:'library',system:true,name:'全局曲库',track_ids:[]},...items.map(p=>existing.get(p.id)?.version===p.version?existing.get(p.id):p),...list(subscriptions,'playlists')];
}
function browseRenderPager(host,id,page,onChange){
  if(!page)return;let nav=$(id);
  if(!nav){nav=document.createElement('nav');nav.id=id;nav.className='browse-pager';nav.setAttribute('aria-label','分页');const previous=makeButton('上一页',()=>nav.change(nav.page.offset-browsePageSize)),next=makeButton('下一页',()=>nav.change(nav.page.offset+browsePageSize)),status=document.createElement('span');status.setAttribute('role','status');nav.append(previous,status,next);host.append(nav)}
  nav.page=page;nav.change=onChange;nav.children[0].disabled=page.offset<=0;nav.children[2].disabled=page.offset+page.items.length>=page.total;
  nav.children[1].textContent=page.total?(page.offset+1)+'–'+(page.offset+page.items.length)+' / '+page.total:'0 / 0';nav.hidden=page.total<=browsePageSize;
}
function browseLibraryPage(){const page=browseState.tracks;return page&&page.query===$('trackSearch').value.trim()&&page.filter===(state.trackFilter||'all')?page:null}
function browseOwnPlaylistPage(){return browseState.playlists}
function browseMemberPage(id){return browseState.member?.id===id?browseState.member:null}
function browseMemberPlaylist(p){
  const page=browseMemberPage(p.id);if(!page)return p;
  return {...p,track_ids:page.items.map(t=>t.id),track_notes:Object.fromEntries(page.items.map(t=>[t.id,t.playlist_note||''])),browse_page:page};
}
function browseSearch(kind,immediate=false){
  clearTimeout(browseState.timers[kind]);browseState.generations[kind]++;
  if(kind==='tracks'){state.selectedTrackIds.clear();$('trackRows').querySelectorAll('input[type="checkbox"]').forEach(input=>input.checked=false);$('selectAll').checked=false;updateTrackSelection()}
  const query=kind==='tracks'?$('trackSearch').value.trim():kind==='playlists'?$('playlistSearch').value.trim():(state.playlistTrackQuery||'').trim();
  browseSetRoute(query,0,kind==='tracks'?state.trackFilter:undefined);
  const action=async()=>{try{if(kind==='tracks'){await loadBrowseTracks(0);renderTracks()}else if(kind==='playlists'){await loadBrowsePlaylists(0);renderPlaylists()}else{await loadBrowseMembers(state.playlistDetailId,0);renderPlaylists()}}catch(error){setStatus(error.message,'error')}};
  if(immediate)return action();browseState.timers[kind]=setTimeout(action,220);
}
function browseMoveDescription(before,after){
  if(before.length!==after.length||new Set(after).size!==before.length)return null;
  // A drag changes one item's relative position; find the moved member while
  // retaining the order of every other page member.
  for(const id of before)if(before.filter(x=>x!==id).join('|')===after.filter(x=>x!==id).join('|')){
    const index=after.indexOf(id);if(index!==before.indexOf(id))return {id,beforeID:after[index+1]||null};
  }
  return null;
}
async function browseSaveMemberOrder(p,ids,focusID){
  const epoch=browseState.epoch;
  const page=browseMemberPage(p.id),previous=page?.items.map(t=>t.id)||[];if(!page)return;
  const move=browseMoveDescription(previous,ids);if(!move)return;
  // Moving to the end of a page means before the first member of the next
  // page, which must be resolved explicitly rather than appending globally.
  let beforeID=move.beforeID;
  if(beforeID===null&&page.offset+previous.length<page.total){const next=await api('browse/playlists/'+encodeURIComponent(p.id)+'/tracks?'+new URLSearchParams({offset:String(page.offset+previous.length),limit:'1',revision:String(page.revision)}));beforeID=next.items[0]?.id||''}
  const saved=await api('browse/playlists/'+encodeURIComponent(p.id)+'/tracks/move','POST',{track_id:move.id,before_id:beforeID||'',expected_track_ids:previous,offset:page.offset,limit:browsePageSize},undefined,p.version);
  if(epoch!==browseState.epoch)return;
  browseState.member={...saved,id:p.id,query:'',filter:'all'};browseCacheTracks();browseCachePlaylists();state.revision=saved.revision;renderPlaylists();if(focusID)$('playlistDetail').querySelector('[data-track-id="'+CSS.escape(focusID)+'"]')?.focus();
}
async function browseSavePlaylistOrder(ids){
  const epoch=browseState.epoch;
  const page=browseState.playlists,previous=page?.items.map(p=>p.id)||[],move=browseMoveDescription(previous,ids);if(!move)return;
  let beforeID=move.beforeID;
  if(beforeID===null&&page.offset+previous.length<page.total){const next=await api('browse/playlists?'+new URLSearchParams({offset:String(page.offset+previous.length),limit:'1',revision:String(page.revision)}));beforeID=next.items[0]?.id||''}
  const saved=await api('browse/playlists/move','POST',{playlist_id:move.id,before_id:beforeID||'',expected_order:previous,offset:page.offset,limit:browsePageSize,revision:page.revision});
  if(epoch!==browseState.epoch)return;
  browseState.playlists={...saved,query:'',filter:'all'};browseCachePlaylists();state.revision=saved.revision;renderPlaylists();
}
async function browseFullTrack(track){
  if(track.detail_loaded!==false)return track;
  const epoch=browseState.epoch;
  const full=await api('tracks/'+encodeURIComponent(track.id));full.detail_loaded=true;
  if(epoch!==browseState.epoch)throw new Error('账户会话已变化，请重新打开曲目。');
  const index=state.tracks.findIndex(t=>t.id===full.id);if(index>=0)state.tracks[index]=full;
  return full;
}
const browseOriginalEditTrack=editTrack;
editTrack=function(track){if(track.detail_loaded!==false)return browseOriginalEditTrack(track);run(async()=>browseOriginalEditTrack(await browseFullTrack(track)))};
openAddTracks=function(ids){
  const body=$('modalBody');body.replaceChildren();$('modalTitle').textContent='加入我的歌单';
  const search=document.createElement('input');search.className='search';search.placeholder='搜索我的歌单';const select=document.createElement('select');select.id='addPlaylistTarget';select.setAttribute('aria-label','选择歌单');select.className='select';const pager=document.createElement('div');body.append(search,select,pager);let generation=0,timer,page;const epoch=browseState.epoch;
  const load=async(offset=0)=>{const current=++generation,result=await api('browse/playlists?'+new URLSearchParams({q:search.value.trim(),offset:String(offset),limit:String(browsePageSize),summary_only:'1'}));if(epoch!==browseState.epoch||current!==generation||!body.contains(select))return;page=result;select.replaceChildren();for(const p of page.items){const option=document.createElement('option');option.value=p.id;option.textContent=p.name;select.append(option)}if(!page.items.length){const option=document.createElement('option');option.textContent='没有匹配的歌单，请先创建歌单';option.value='';select.append(option)}browseRenderPager(pager,'browseTargetPager',page,offset=>load(offset).catch(error=>setStatus(error.message,'error')))};
  search.oninput=()=>{clearTimeout(timer);generation++;timer=setTimeout(()=>load().catch(error=>setStatus(error.message,'error')),220)};
  state.saveAction=async()=>{const p=page?.items.find(p=>p.id===select.value);if(!p)throw new Error('请选择歌单');await api('playlists/'+encodeURIComponent(p.id)+'/tracks','POST',{track_ids:ids},undefined,p.version);state.selectedTrackIds.clear();$('editor').close();await refreshCore();setStatus('曲目已加入歌单','ok')};$('editor').showModal();load().catch(error=>setStatus(error.message,'error'));
};
const browseOriginalShowTrackMenu=showTrackMenu;
showTrackMenu=function(event,track){browseOriginalShowTrackMenu(event,track);if((state.browseSummary?.playlist_count||0)>state.playlists.filter(p=>!p.system&&!p.read_only).length)$('contextMenu').append(makeButton('选择其他歌单…',()=>{hideContextMenu();openAddTracks([...state.selectedTrackIds])}))};
openPlaylistTrackPicker=function(p){
  if(p.system)return;const body=$('modalBody');body.replaceChildren();$('modalTitle').textContent='添加曲目到 '+(p.name||'歌单');
  const search=document.createElement('input');search.className='search';search.placeholder='搜索曲目';search.setAttribute('aria-label','搜索可添加曲目');const list=document.createElement('div');list.className='key-grant-grid';const pager=document.createElement('div'),selected=new Set(),epoch=browseState.epoch;body.append(search,list,pager);let generation=0,timer,page;
  const load=async(offset=0)=>{const current=++generation;const result=await api('browse/tracks?'+new URLSearchParams({q:search.value.trim(),exclude_playlist_id:p.id,offset:String(offset),limit:String(browsePageSize)}));if(current!==generation||epoch!==browseState.epoch||!body.contains(search))return;page=result;selected.clear();list.replaceChildren();for(const t of page.items){const label=document.createElement('label'),input=document.createElement('input');input.type='checkbox';input.value=t.id;input.onchange=()=>{if(input.checked)selected.add(t.id);else selected.delete(t.id)};label.append(input,document.createTextNode((t.title||'未命名')+' · '+(t.artist||'未知艺术家')));list.append(label)}if(!page.items.length)list.textContent='没有可添加的曲目';browseRenderPager(pager,'browsePickerPager',page,offset=>load(offset).catch(error=>setStatus(error.message,'error')))};
  search.oninput=()=>{clearTimeout(timer);generation++;timer=setTimeout(()=>load().catch(error=>setStatus(error.message,'error')),220)};
  state.saveAction=async()=>{if(!selected.size)throw new Error('请选择当前页要添加的曲目');await api('playlists/'+encodeURIComponent(p.id)+'/tracks','POST',{track_ids:[...selected]},undefined,p.version);$('editor').close();await refreshCore()};$('editor').showModal();load().catch(error=>setStatus(error.message,'error'));
};
function browseRefreshBlocked(){return document.hidden||!state.user||state.playlistEditing||state.playlistCreating||state.playlistTrackSortBusy||state.playlistSortBusy||document.body.classList.contains('playlist-sorting')||Boolean(document.querySelector('dialog[open]'))||Boolean(document.activeElement?.matches('input,textarea,select,[contenteditable="true"]'))}
async function pollBrowseChanges(){
  if(browseState.polling||browseRefreshBlocked()||!['tracks','playlists','overview'].includes(state.page)||state.subscriptionsTab||state.invitationsTab)return;
  browseReset();const epoch=browseState.epoch,route=location.hash,page=state.page,detailID=state.playlistDetailId,
    kind=page==='tracks'?'tracks':page==='playlists'?(detailID?'member':'playlists'):null;
  let generation=kind?browseState.generations[kind]:null;
  const sameView=()=>epoch===browseState.epoch&&route===location.hash&&page===state.page&&detailID===state.playlistDetailId&&!state.subscriptionsTab&&!state.invitationsTab&&(!kind||generation===browseState.generations[kind]);
  browseState.polling=true;try{const health=await api('health');if(!sameView()||health.revision===state.revision||browseRefreshBlocked())return;
    const summary=await api('browse/summary');if(!sameView()||browseRefreshBlocked())return;
    if(kind){
      const pending=kind==='tracks'?loadBrowseTracks(browseState.tracks?.offset||0):kind==='member'?loadBrowseMembers(detailID,browseState.member?.offset||0):loadBrowsePlaylists(browseState.playlists?.offset||0);
      // The loader increments its generation synchronously. A newer navigation
      // or search can supersede it before this page (or its error) returns.
      generation=browseState.generations[kind];const loaded=await pending;
      if(!loaded||!sameView()||browseRefreshBlocked())return;
      if(page==='tracks')renderTracks();else renderPlaylists();
    }
    if(sameView()){browseState.summary=summary;state.browseSummary=summary;state.revision=summary.revision;renderOverview()}
  }catch(error){if(sameView()&&kind==='member'&&error.status===404){browseState.member=null;if(browseState.playlists)browseState.playlists.items=browseState.playlists.items.filter(p=>p.id!==detailID);state.playlists=state.playlists.filter(p=>p.id!==detailID);browseCacheTracks();$('playlistDetail').replaceChildren();setStatus('歌单已不可用，请返回歌单列表。','error')}}finally{browseState.polling=false}
}
$('trackSearch').oninput=()=>browseSearch('tracks');$('playlistSearch').oninput=()=>browseSearch('playlists');
document.querySelectorAll('[data-filter]').forEach(button=>button.onclick=()=>{state.trackFilter=button.dataset.filter;state.selectedTrackIds.clear();document.querySelectorAll('[data-filter]').forEach(element=>element.classList.toggle('active',element===button));browseSearch('tracks',true)});
setInterval(()=>pollBrowseChanges(),15000);window.addEventListener('online',()=>pollBrowseChanges());document.addEventListener('visibilitychange',()=>{if(!document.hidden)pollBrowseChanges()});
