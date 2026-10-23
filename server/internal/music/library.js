'use strict';

function visibleTracks() {
  const query=$('trackSearch').value.trim().toLowerCase(), membership=playlistMembershipIndex();
  return state.tracks.filter(t=>[t.title,t.artist,t.album,t.genre].join(' ').toLowerCase().includes(query)&&
    (state.trackFilter==='all'||state.trackFilter==='mine'&&t.uploaded_by===state.user?.id||state.trackFilter==='managed'&&t.owner_id===state.user?.id||state.trackFilter==='orphan'&&!membership.has(t.id)));
}
function updateTrackSelection() {
  $('trackSelectionStatus').textContent=state.selectedTrackIds.size?'已选择 '+state.selectedTrackIds.size+' 首':'';
  if(!$('trackBulkActions'))return;
  $('trackBulkActions').hidden=!state.selectedTrackIds.size;
  $('bulkDelete').hidden=![...state.selectedTrackIds].every(id=>canManageTrack(state.tracks.find(t=>t.id===id)||{}));
  $('bulkDelete').textContent=isAdministrator()?'下架所选曲目':'删除所选曲目';
}
function renderTracks() {
  if(state.page==='tracks'&&location.hash.startsWith('#/library')){
    const query=new URLSearchParams();if($('trackSearch').value.trim())query.set('q',$('trackSearch').value.trim());if(state.trackFilter!=='all')query.set('filter',state.trackFilter);
    history.replaceState(null,'','#/library'+(query.size?'?'+query:''));
  }
  const rows=visibleTracks(), visible=new Set(rows.map(t=>t.id));
  state.selectedTrackIds=new Set([...state.selectedTrackIds].filter(id=>visible.has(id)));
  $('trackRows').replaceChildren();
  $('trackRows').closest('table').querySelector('thead').innerHTML='<tr><th><input id="selectAll" type="checkbox" aria-label="选择当前筛选的全部曲目"></th><th>曲目</th><th>歌手</th><th>专辑</th><th>状态</th><th>操作</th></tr>';
  $('selectAll').onchange=e=>{for(const t of rows){if(e.target.checked)state.selectedTrackIds.add(t.id);else state.selectedTrackIds.delete(t.id)}renderTracks()};
  if(!rows.length)$('trackRows').innerHTML='<tr><td colspan="6" class="empty">'+(state.tracks.length?'没有匹配的曲目，请调整搜索或筛选。':'曲库暂无曲目，可以上传音乐。')+'</td></tr>';
  for(const t of rows){
    const row=document.createElement('tr');row.dataset.libraryTrack=t.id;
    row.innerHTML='<td><input type="checkbox" data-track-id="'+txt(t.id)+'" aria-label="选择 '+txt(t.title)+'" '+(state.selectedTrackIds.has(t.id)?'checked':'')+'></td><td><div class="track-title-cell"><span class="track-cover">♪</span><button class="text-button track-title" data-details>'+txt(t.title||'未命名')+'</button></div></td><td class="track-artist">'+txt(t.artist||'—')+'</td><td class="track-album">'+txt(t.album||'—')+'</td><td><span class="badge '+(t.missing?'warn':'ok')+'">'+(t.missing?'文件缺失':'可播放')+'</span></td><td class="row-actions"></td>';
    row.querySelector('[data-details]').onclick=()=>openTrackDetails(t);
    row.querySelector('[data-track-id]').onchange=e=>{if(e.target.checked)state.selectedTrackIds.add(t.id);else state.selectedTrackIds.delete(t.id);updateTrackSelection();$('selectAll').checked=rows.every(t=>state.selectedTrackIds.has(t.id))};
    const actions=row.querySelector('.row-actions'), play=makeButton('播放',()=>playTrack(t),'btn small');play.disabled=Boolean(t.missing||t.removed);
    const more=makeButton('更多',e=>{e.stopPropagation();prepareTrackContext(t);showTrackMenu(e,t)},'btn small');more.setAttribute('aria-label','更多操作：'+t.title);actions.append(play,more);
    row.oncontextmenu=e=>{e.preventDefault();prepareTrackContext(t);showTrackMenu(e,t)};
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
  const detail=state.playlists.find(p=>p.id===state.playlistDetailId&&!p.system)||(state.playlistEditing?state.managedPlaylistEdit:null);
  if(state.playlistEditing&&detail){$('playlistBrowse').hidden=true;$('playlistDetail').hidden=true;$('page-playlists').querySelector(':scope > .page-intro').hidden=true;renderPlaylistEditor(detail);return}
  playlistEditor.hidden=true;
  $('playlistBrowse').hidden=Boolean(detail);$('playlistDetail').hidden=!detail;
  $('page-playlists').querySelector(':scope > .page-intro').hidden=Boolean(detail);
  $('newPlaylist').hidden=Boolean(detail);
  if(detail){renderPlaylistDetail(detail);return}
  state.playlistDetailId=null;
  if(state.page==='playlists')document.title='我的歌单 · 音乐服务器';
  const query=$('playlistSearch').value.trim().toLowerCase(),playlists=state.playlists.filter(p=>!p.system&&(p.name+' '+(p.description||'')).toLowerCase().includes(query));
  $('playlistCount').textContent=playlists.length+' 个私人歌单';$('playlistList').replaceChildren();
  if(!playlists.length)$('playlistList').innerHTML='<div class="panel empty">'+(query?'没有匹配的歌单。':'还没有私人歌单，点击“新建歌单”开始。')+'</div>';
  for(const p of playlists){
    const card=document.createElement('article');card.className='playlist-card';card.tabIndex=0;card.setAttribute('role','link');card.setAttribute('aria-label','打开歌单：'+p.name);card.dataset.id=p.id;card.dataset.sortId=p.id;
    card.innerHTML='<span class="cover">'+txt((p.name||'♪').slice(0,1))+'</span><div class="playlist-info"><div class="playlist-title">'+txt(p.name)+'</div><div class="playlist-desc">'+txt(p.description||'暂无描述')+'</div><div class="playlist-meta">'+members(p).size+' 首曲目 · 私人歌单</div></div><div class="playlist-actions"></div>';
    card.querySelector('.playlist-actions').remove();
    card.onclick=()=>{state.playlistListScrollY=scrollY;location.hash='#/playlists/'+p.id;scrollTo(0,0)};
    card.oncontextmenu=e=>playlistItemMenu(e,p,card);
    card.onkeydown=e=>{if(e.key==='Enter'){e.preventDefault();card.click()}if(e.key==='ContextMenu'||e.shiftKey&&e.key==='F10')playlistItemMenu(e,p,card)};
    $('playlistList').append(card);
    if(p.cover_id)loadCover(card.querySelector('.cover'),'covers/'+encodeURIComponent(p.cover_id));
  }
  bindPlaylistSorting($('playlistList'),'.playlist-card',savePlaylistOrder,{disabled:()=>Boolean($('playlistSearch').value.trim())||state.playlistSortBusy});
}
function renderPlaylistDetail(p){
  const detail=$('playlistDetail');detail.replaceChildren();
  if(state.page==='playlists')document.title=p.name+' · 我的歌单 · 音乐服务器';
  const intro=document.createElement('div');intro.className='page-intro';intro.innerHTML='<div><p>我的歌单 / 私人歌单</p><h1>'+txt(p.name)+'</h1><p>'+txt(p.description||'')+'</p></div><div class="actions"></div>';
  const actions=intro.querySelector('.actions');const back=makeButton('← 我的歌单',returnPlaylistList);back.dataset.backPlaylists='';back.classList.add('page-back');
  const edit=makeButton('编辑歌单',()=>editPlaylist(p));edit.dataset.editPlaylist='';const add=makeButton('添加曲目',()=>openPlaylistTrackPicker(p),'btn primary');add.dataset.addMembers='';actions.append(edit,add);detail.append(back,intro);
  const count=document.createElement('p');count.className='status';count.textContent=members(p).size+' 首曲目 · 个人备注仅属于此歌单';detail.append(count);
  const list=document.createElement('div');list.className='playlist-tracks';
  const tracks=[...members(p)].map(id=>state.tracks.find(t=>t.id===id)||{id,title:'不可用曲目',removed:true});
  if(!tracks.length)list.innerHTML='<div class="panel empty">歌单中暂无曲目，点击“添加曲目”从全局曲库选择。</div>';
  tracks.forEach((track,index)=>{
    const unavailable=track.missing||track.removed||(p.unavailable_track_ids||[]).includes(track.id);
    const row=document.createElement('article');row.className='playlist-track';row.dataset.trackId=track.id;row.draggable=true;
    const title=document.createElement('div');title.className='playlist-track-info';
    const strong=document.createElement('strong');strong.textContent=(index+1)+'. '+(unavailable?'不可用曲目':track.title||'未命名');
    const note=document.createElement('p');note.textContent=p.track_notes?.[track.id]||'暂无个人备注';note.className='status';title.append(strong,note);
    const controls=document.createElement('div');controls.className='actions';
    const play=makeButton('播放',()=>playTrack(track),'btn small');play.disabled=Boolean(unavailable);controls.append(play);
    const edit=makeButton('个人备注',()=>editPlaylistTrackNote(p,track),'btn small');edit.dataset.editNote='';controls.append(edit);
    const up=makeButton('↑',()=>reorderPlaylistTracks(p,track.id,tracks[index-1].id),'btn small');up.disabled=index===0;up.setAttribute('aria-label','上移曲目');
    const down=makeButton('↓',()=>reorderPlaylistTracks(p,track.id,tracks[index+1].id),'btn small');down.disabled=index===tracks.length-1;down.setAttribute('aria-label','下移曲目');controls.append(up,down);
    const remove=makeButton('移出歌单',()=>run(async()=>{await api('playlists/'+p.id+'/tracks/'+track.id,'DELETE',undefined,undefined,p.version);await refreshCore()}),'btn small danger');remove.dataset.removeMember='';controls.append(remove);
    row.ondragstart=()=>state.playlistTrackDragId=track.id;row.ondragend=()=>state.playlistTrackDragId=null;
    row.ondragover=e=>{if(state.playlistTrackDragId&&state.playlistTrackDragId!==track.id)e.preventDefault()};row.ondrop=e=>{e.preventDefault();if(state.playlistTrackDragId&&state.playlistTrackDragId!==track.id)reorderPlaylistTracks(p,state.playlistTrackDragId,track.id)};
    row.append(title,controls);list.append(row);
  });detail.append(list);
}
