'use strict';

function visibleTracks() {
  const query=$('trackSearch').value.trim().toLowerCase(), membership=playlistMembershipIndex();
  return state.tracks.filter(t=>[t.title,t.artist,t.album,t.genre].join(' ').toLowerCase().includes(query)&&
    (state.trackFilter==='all'||state.trackFilter==='mine'&&t.uploaded_by===state.user?.id||state.trackFilter==='managed'&&t.owner_id===state.user?.id||state.trackFilter==='orphan'&&!membership.has(t.id)));
}
function updateTrackSelection() {
  $('trackSelectionStatus').textContent='';
}
function renderTracks() {
  if(state.page==='tracks'&&location.hash.startsWith('#/library')){
    const query=new URLSearchParams();if($('trackSearch').value.trim())query.set('q',$('trackSearch').value.trim());if(state.trackFilter!=='all')query.set('filter',state.trackFilter);
    history.replaceState(null,'','#/library'+(query.size?'?'+query:''));
  }
  const rows=visibleTracks(), visible=new Set(rows.map(t=>t.id));
  state.selectedTrackIds=new Set([...state.selectedTrackIds].filter(id=>visible.has(id)));
  $('trackRows').replaceChildren();
  $('trackRows').closest('table').querySelector('thead').innerHTML='<tr><th><input id="selectAll" type="checkbox" aria-label="选择当前筛选的全部曲目"></th><th>曲目</th><th>歌手</th><th>专辑</th><th>状态</th></tr>';
  $('selectAll').onchange=e=>{for(const t of rows){if(e.target.checked)state.selectedTrackIds.add(t.id);else state.selectedTrackIds.delete(t.id)}renderTracks()};
  if(!rows.length)$('trackRows').innerHTML='<tr><td colspan="5" class="empty">'+(state.tracks.length?'没有匹配的曲目，请调整搜索或筛选。':'曲库暂无曲目，可以上传音乐。')+'</td></tr>';
  for(const t of rows){
    const row=document.createElement('tr');row.dataset.libraryTrack=t.id;
    row.innerHTML='<td><input type="checkbox" data-track-id="'+txt(t.id)+'" aria-label="选择 '+txt(t.title)+'" '+(state.selectedTrackIds.has(t.id)?'checked':'')+'></td><td><div class="track-title-cell"><span class="track-cover">♪</span><button class="text-button track-title" data-details>'+txt(t.title||'未命名')+'</button></div></td><td class="track-artist">'+txt(t.artist||'—')+'</td><td class="track-album">'+txt(t.album||'—')+'</td><td><span class="badge '+(t.missing?'warn':'ok')+'">'+(t.missing?'文件缺失':'可播放')+'</span></td><td class="row-actions"></td>';
    row.querySelector('[data-details]').onclick=()=>openTrackDetails(t);
    row.querySelector('[data-track-id]').onchange=e=>{if(e.target.checked)state.selectedTrackIds.add(t.id);else state.selectedTrackIds.delete(t.id);updateTrackSelection();$('selectAll').checked=rows.every(t=>state.selectedTrackIds.has(t.id))};
    row.querySelector('.row-actions').remove();
    row.tabIndex=0;
    const context=e=>{e.preventDefault();prepareTrackContext(t);const r=row.getBoundingClientRect();showTrackMenu({clientX:e.clientX||r.left+48,clientY:e.clientY||r.top+20},t)};
    row.oncontextmenu=context;
    row.onkeydown=e=>{if(e.key==='ContextMenu'||e.shiftKey&&e.key==='F10')context(e)};
    let pressTimer,pressPoint,longPressed=false;
    const cancelPress=()=>{clearTimeout(pressTimer);pressTimer=null};
    row.onpointerdown=e=>{if(e.pointerType!=='touch')return;longPressed=false;pressPoint={x:e.clientX,y:e.clientY};pressTimer=setTimeout(()=>{longPressed=true;context(e)},450)};
    row.onpointermove=e=>{if(pressPoint&&Math.hypot(e.clientX-pressPoint.x,e.clientY-pressPoint.y)>10)cancelPress()};
    row.onpointerup=row.onpointercancel=cancelPress;
    row.addEventListener('click',e=>{if(longPressed){longPressed=false;e.preventDefault();e.stopImmediatePropagation()}},true);
    $('trackRows').append(row);if(t.cover_id&&state.user)loadCover(row.querySelector('.track-cover'),'covers/'+encodeURIComponent(t.cover_id));
  }
  $('selectAll').checked=rows.length>0&&rows.every(t=>state.selectedTrackIds.has(t.id));
  if($('trackCount'))$('trackCount').textContent='当前 '+rows.length+' 首 · 曲库 '+state.tracks.length+' 首';
  updateTrackSelection();
}
function openTrackDetails(track) {
  const dialog=$('trackDetails');dialog.querySelector('h3').textContent=track.title||'未命名曲目';
  const body=$('trackDetailsBody');body.replaceChildren();
  const actions=document.createElement('div');actions.className='actions';
  const play=makeButton('播放预览',()=>playTrack(track),'btn primary');play.disabled=Boolean(track.missing||track.removed);actions.append(play);
  actions.append(makeButton('加入我的歌单',()=>{dialog.close();openAddTracks([track.id])}));
  if(canManageTrack(track))actions.append(makeButton('编辑曲目',()=>{dialog.close();editTrack(track)}));
  body.append(actions);
  const metadata=document.createElement('dl');metadata.className='detail-grid';
  for(const [label,value] of [['歌手',track.artist],['专辑',track.album],['风格',track.genre],['音频参数',technicalText(track)],['歌词来源',sourceName(track.lyrics_source,track.embedded_lyrics)],['封面来源',sourceName(track.cover_source)],['管理者',track.owner_id===state.user.id?'我':state.users?.find(u=>u.id===track.owner_id)?.username||'其他账户'],['全局备注',track.comment]]){
    const dt=document.createElement('dt'),dd=document.createElement('dd');dt.textContent=label;dd.textContent=value||'—';metadata.append(dt,dd);
  }
  body.append(metadata);
  const lyrics=document.createElement('details');lyrics.innerHTML='<summary>歌词</summary>';const text=document.createElement('pre');text.className='lyrics-text';text.textContent=track.lyrics||'暂无歌词';lyrics.append(text);body.append(lyrics);
  const technical=document.createElement('details');technical.innerHTML='<summary>来源与技术信息</summary>';
  const info=document.createElement('pre');info.className='lyrics-text';info.textContent='曲目 UUID：'+track.id+'\n原始上传者：'+track.uploaded_by+'\n当前管理者：'+track.owner_id+(isAdministrator()?'\n文件路径：'+(track.path||'—'):'');technical.append(info);body.append(technical);
  dialog.showModal();
}
function openAddTracks(ids) {
  const playlists=state.playlists.filter(p=>!p.system&&p.owner_id===state.user.id);
  if(!playlists.length){setStatus('请先在“我的歌单”创建私人歌单，再添加曲目。','error');return}
  $('modalTitle').textContent='加入我的歌单';$('modalBody').replaceChildren();
  const field=document.createElement('div');field.className='field';const label=document.createElement('label');label.htmlFor='addPlaylistTarget';label.textContent='选择歌单';
  const select=document.createElement('select');select.id='addPlaylistTarget';
  for(const p of playlists){const option=document.createElement('option');option.value=p.id;option.textContent=p.name;select.append(option)}field.append(label,select);$('modalBody').append(field);
  state.saveAction=async()=>{const p=state.playlists.find(p=>p.id===select.value);await api('playlists/'+p.id+'/tracks','POST',{track_ids:ids},undefined,p.version);state.selectedTrackIds.clear();await refreshCore();$('editor').close();setStatus('曲目已加入歌单','ok')};$('editor').showModal();
}
async function deleteSelectedTracks(){
  const tracks=[...state.selectedTrackIds].map(id=>state.tracks.find(t=>t.id===id)).filter(Boolean);
  if(!tracks.length||!tracks.every(canManageTrack))return;
  if(!confirm((isAdministrator()?'下架':'删除')+'所选 '+tracks.length+' 首曲目？原文件和歌单占位会保留。'))return;
  await run(async()=>{let completed=0;try{for(const t of tracks){await api('tracks/'+t.id,'DELETE',undefined,undefined,t.version);completed++}}catch(error){throw new Error('已处理 '+completed+' / '+tracks.length+' 首；'+error.message)}finally{state.selectedTrackIds.clear();await refreshCore()}});
}
function renderPlaylists(){
  $('playlistDetail').querySelector('.playlist-tracks')?.sortCleanup?.();
  const detail=state.playlists.find(p=>p.id===state.playlistDetailId&&!p.system)||(state.playlistEditing?state.managedPlaylistEdit:null);
  syncMyPlaylistTabs(detail);
  invitationBrowse.hidden=!state.invitationsTab;
  if(state.invitationsTab){playlistEditor.hidden=true;$('playlistBrowse').hidden=true;$('playlistDetail').hidden=true;$('newPlaylist').hidden=true;$('page-playlists').querySelector(':scope > .page-intro').hidden=false;return}
  if(detail)$('playlistList').sortCleanup?.();
  if(state.playlistCreating){$('playlistList').sortCleanup?.();$('playlistBrowse').hidden=true;$('playlistDetail').hidden=true;$('page-playlists').querySelector(':scope > .page-intro').hidden=true;const p=playlistDraft?.creating?playlistDraft.original:{id:browserUUID(),name:'',description:'',visibility:'private',owner_id:state.user.id,track_ids:[]};renderPlaylistEditor(p,true);return}
  if(state.playlistEditing&&detail){$('playlistBrowse').hidden=true;$('playlistDetail').hidden=true;$('page-playlists').querySelector(':scope > .page-intro').hidden=true;renderPlaylistEditor(detail);return}
  playlistEditor.hidden=true;
  $('playlistBrowse').hidden=Boolean(detail);$('playlistDetail').hidden=!detail;
  $('page-playlists').querySelector(':scope > .page-intro').hidden=Boolean(detail);
  $('newPlaylist').hidden=Boolean(detail)||state.subscriptionsTab;
  if(detail){renderPlaylistDetail(detail,state.subscriptionsTab&&state.liveSubscriptionTracksID===detail.id?{tracks:state.liveSubscriptionTracks}:{});return}
  state.playlistDetailId=null;
  if(state.page==='playlists')document.title='我的歌单 · 音乐服务器';
  const query=$('playlistSearch').value.trim().toLowerCase(),playlists=state.playlists.filter(p=>!p.system&&(state.subscriptionsTab?Boolean(p.subscription_status):!p.read_only)&&(p.name+' '+(p.description||'')).toLowerCase().includes(query));
  $('playlistCount').textContent=playlists.length+' 个'+(state.subscriptionsTab?'订阅':'歌单 · '+(query?'清空搜索后可拖拽排序':'拖拽调整顺序'));$('playlistList').replaceChildren();
  if(!playlists.length)$('playlistList').innerHTML='<div class="panel empty">'+(query?'没有匹配的歌单。':state.subscriptionsTab?'还没有订阅，可以前往“公开歌单”发现歌单。':'还没有歌单，点击“新建歌单”开始。')+'</div>';
  for(const p of playlists){
    const card=document.createElement('article');card.className='playlist-card';card.tabIndex=0;card.setAttribute('role','link');card.setAttribute('aria-label','打开歌单：'+p.name);card.dataset.id=p.id;card.dataset.sortId=p.id;
    card.innerHTML='<span class="cover">'+txt((p.name||'♪').slice(0,1))+'</span><div class="playlist-info"><div class="playlist-title">'+txt(p.name)+'</div><div class="playlist-desc">'+txt(p.description||'暂无描述')+'</div><div class="playlist-meta">'+members(p).size+' 首曲目 · 私人歌单</div></div><div class="playlist-actions"></div>';
    card.querySelector('.playlist-actions').remove();
    card.querySelector('.playlist-meta').textContent=p.subscription_status?(p.subscription_status==='invalid'?'已失效：'+subscriptionReason(p):p.track_ids.length+' 首 · '+(p.source_owner_name||'用户')+' · 只读订阅'):members(p).size+' 首曲目 · '+playlistVisibilityLabel(p);
    card.onclick=()=>{state.playlistListScrollY=scrollY;location.hash=(state.subscriptionsTab?'#/subscriptions/':'#/playlists/')+p.id;scrollTo(0,0)};
    const context=e=>{if(p.read_only){e.preventDefault();const r=card.getBoundingClientRect();renderSubscriptionMenu({clientX:e.clientX||r.left+32,clientY:e.clientY||r.top+20},p)}else playlistItemMenu(e,p,card)};
    card.oncontextmenu=context;
    card.onkeydown=e=>{if(e.key==='Enter'){e.preventDefault();card.click()}if(e.key==='ContextMenu'||e.shiftKey&&e.key==='F10')context(e)};
    $('playlistList').append(card);
    if(p.cover_id)loadCover(card.querySelector('.cover'),'covers/'+encodeURIComponent(p.cover_id));
  }
  bindPlaylistSorting($('playlistList'),'.playlist-card',savePlaylistOrder,{disabled:()=>state.subscriptionsTab||Boolean($('playlistSearch').value.trim())||state.playlistSortBusy});
}
function renderPlaylistDetail(p,config={}){
  const detail=config.target||$('playlistDetail'),readonly=Boolean(p.read_only||config.publicView);detail.querySelector('.playlist-tracks')?.sortCleanup?.();detail.replaceChildren();
  if(state.page==='playlists'||state.page==='public'&&config.publicView)document.title=p.name+' · '+(config.publicView?'公开歌单':'我的歌单')+' · 音乐服务器';
  if(state.playlistTrackSearchID!==p.id){state.playlistTrackSearchID=p.id;state.playlistTrackQuery=''}
  const intro=document.createElement('div');intro.className='page-intro';intro.innerHTML='<div class="playlist-detail-summary"><span class="cover playlist-detail-cover">♪</span><div><p>我的歌单 / 私人歌单</p><h1>'+txt(p.name)+'</h1><p>'+txt(p.description||'')+'</p></div></div><div class="actions"></div>';
  intro.querySelector('p').textContent=config.publicView?'公开歌单 / 只读查看':readonly?'我的订阅 / 只读歌单':'我的歌单 / '+playlistVisibilityLabel(p);
  const actions=intro.querySelector('.actions');const back=makeButton(config.publicView?'← 公开歌单':readonly?'← 我的订阅':'← 我的歌单',()=>{if(config.publicView)location.hash='#/public/playlists';else if(readonly)location.hash='#/subscriptions';else returnPlaylistList()});back.dataset.backPlaylists='';back.classList.add('page-back');
  if(!readonly){const edit=makeButton('编辑歌单',()=>editPlaylist(p));edit.dataset.editPlaylist='';const add=makeButton('添加曲目',()=>openPlaylistTrackPicker(p),'btn primary');add.dataset.addMembers='';actions.append(edit,add)}appendCommunityActions(actions,p,config.publicView);detail.append(back,intro);
  if(p.subscription_status==='invalid'){const notice=document.createElement('p');notice.className='notice';notice.dataset.subscriptionInvalid='';notice.textContent='订阅已失效：'+subscriptionReason(p)+'。恢复可见性、分享授权和账户状态后会恢复；也可以取消此订阅。';detail.append(notice);return}
  if(p.cover_id)loadCover(intro.querySelector('.cover'),'covers/'+encodeURIComponent(p.cover_id));
  const all=[...members(p)].map(id=>(config.tracks||state.tracks).find(t=>t.id===id)||{id,title:'不可用曲目',removed:true}),query=(state.playlistTrackQuery||'').trim().toLowerCase(),tracks=all.filter(t=>[t.title,t.artist,t.album,p.track_notes?.[t.id]].join(' ').toLowerCase().includes(query));
  const toolbar=document.createElement('div');toolbar.className='toolbar';const search=document.createElement('input');search.id=config.publicView?'publicPlaylistTrackSearch':'playlistTrackSearch';search.className='search';search.placeholder='搜索歌单曲目';search.setAttribute('aria-label','搜索歌单曲目');search.value=state.playlistTrackQuery||'';
  search.oninput=()=>{const position=search.selectionStart;state.playlistTrackQuery=search.value;renderPlaylistDetail(p,config);const current=detail.querySelector('input[aria-label="搜索歌单曲目"]');current.focus();current.setSelectionRange(position,position)};
  const count=document.createElement('span');count.className='status';count.textContent=tracks.length+' / '+all.length+' 首 · '+(readonly?'只读歌单':query?'清空搜索后可拖拽排序':'长按序号或行空白处拖拽排序');toolbar.append(search,count);detail.append(toolbar);
  const list=document.createElement('div');list.className='playlist-tracks panel';list.setAttribute('role','list');list.setAttribute('aria-label','歌单曲目');
  if(!tracks.length)list.innerHTML='<p class="empty">'+(query?'没有匹配的曲目。':'歌单中暂无曲目，点击“添加曲目”从全局曲库选择。')+'</p>';
  if(tracks.length){const head=document.createElement('div');head.className='playlist-track-columns';head.setAttribute('aria-hidden','true');head.innerHTML='<span>#</span><span>曲目</span><span>歌手</span><span>专辑</span><span>时长</span><span>状态</span>';list.append(head)}
  for(const track of tracks){
    const index=all.findIndex(t=>t.id===track.id),unavailable=Boolean(track.missing||track.removed||(p.unavailable_track_ids||[]).includes(track.id)),name=unavailable?'不可用曲目':track.title||'未命名';
    const row=document.createElement('article');row.className='playlist-track';row.dataset.trackId=track.id;row.dataset.sortId=track.id;row.tabIndex=0;row.setAttribute('role','listitem');row.setAttribute('aria-label',(index+1)+'. '+name);
    row.innerHTML='<span class="playlist-sequence" title="长按拖拽排序">'+(index+1)+'</span><div class="track-title-cell"><button type="button" class="track-cover" aria-label="播放：'+txt(name)+'" '+(unavailable?'disabled':'')+'>♪</button><div class="playlist-track-info"><button type="button" class="text-button track-title" data-playlist-track-details>'+txt(name)+'</button><span class="playlist-artist-mobile">'+txt(unavailable?'不可用':track.artist||'未知歌手')+'</span>'+(p.track_notes?.[track.id]?'<p class="playlist-note" title="'+txt(p.track_notes[track.id])+'">'+txt(p.track_notes[track.id])+'</p>':'')+'</div></div><span class="playlist-track-artist">'+txt(unavailable?'—':track.artist||'—')+'</span><span class="playlist-track-album">'+txt(unavailable?'—':track.album||'—')+'</span><span class="playlist-duration">'+playlistDuration(track.duration_ms)+'</span><span class="playlist-track-state"><span class="badge '+(unavailable?'warn':'ok')+'">'+(unavailable?'不可用':'可播放')+'</span></span>';
    row.querySelector('.track-cover').onclick=()=>playTrack(track);row.querySelector('[data-playlist-track-details]').onclick=()=>openPlaylistTrackDetails(p,track,unavailable);
    row.oncontextmenu=e=>{e.preventDefault();showPlaylistTrackMenu(e,p,track,unavailable)};
    row.onkeydown=e=>{if(e.target!==row)return;if(e.key==='ContextMenu'||e.shiftKey&&e.key==='F10'){e.preventDefault();const r=row.getBoundingClientRect();showPlaylistTrackMenu({clientX:r.left+45,clientY:r.top+20},p,track,unavailable)}if(e.key==='Enter'){e.preventDefault();openPlaylistTrackDetails(p,track,unavailable)}};
    list.append(row);if(track.cover_id&&!unavailable)loadCover(row.querySelector('.track-cover'),'covers/'+encodeURIComponent(track.cover_id));
  }
  detail.append(list);bindPlaylistSorting(list,'.playlist-track',ids=>savePlaylistTrackOrder(p,ids),{hold:350,disabled:()=>readonly||Boolean(query)||state.playlistTrackSortBusy});
}
