'use strict';

// Touch listeners allow normal scrolling until a stationary long press activates sorting.
function bindPlaylistSorting(list,selector,save,{hold=0,disabled=()=>false}={}){
  list.sortCleanup?.();
  const controller=new AbortController(),options={signal:controller.signal};
  let gesture=null,frame=0,suppressUntil=0;
  const items=()=>[...list.querySelectorAll(selector)];
  function clear(){
    if(!gesture)return;
    clearTimeout(gesture.timer);cancelAnimationFrame(frame);gesture.ghost?.remove();
    gesture.row.classList.remove('sorting');items().forEach(el=>el.classList.remove('drop-before','drop-after'));
    document.body.classList.remove('playlist-sorting');gesture=null;
  }
  list.sortCleanup=()=>{clear();controller.abort()};
  function mark(){
    if(!gesture?.active)return;
    const {x,y,ghost,row}=gesture;ghost.style.left=(x-24)+'px';ghost.style.top=(y-20)+'px';
    const others=items().filter(el=>el!==row);let target=others.find(el=>y<el.getBoundingClientRect().bottom)||others.at(-1);
    items().forEach(el=>el.classList.remove('drop-before','drop-after'));
    if(target){gesture.target=target;gesture.after=y>target.getBoundingClientRect().top+target.offsetHeight/2;target.classList.add(gesture.after?'drop-after':'drop-before')}
  }
  function scroll(){
    if(!gesture?.active)return;
    const edge=64,y=gesture.y,delta=y<edge?-Math.ceil((edge-y)/5):y>innerHeight-edge?Math.ceil((y-innerHeight+edge)/5):0;
    if(delta){window.scrollBy(0,delta);mark()}frame=requestAnimationFrame(scroll);
  }
  function activate(){
    if(!gesture||disabled())return clear();
    gesture.active=true;gesture.row.classList.add('sorting');hideContextMenu();document.body.classList.add('playlist-sorting');
    const ghost=gesture.row.cloneNode(true);ghost.removeAttribute('id');ghost.querySelectorAll('[id]').forEach(el=>el.removeAttribute('id'));
    ghost.classList.add('drag-ghost');ghost.classList.remove('sorting');ghost.style.width=gesture.row.getBoundingClientRect().width+'px';ghost.setAttribute('aria-hidden','true');document.body.append(ghost);gesture.ghost=ghost;
    mark();scroll();
  }
  function begin(event,x,y,touch){
    if(gesture||disabled()||event.target.closest('button,a,input,select,textarea'))return;
    const row=event.target.closest(selector);if(!row||!list.contains(row))return;
    gesture={row,x,y,startX:x,startY:y,touch,active:false,target:null};
    const delay=touch?350:hold;if(delay)gesture.timer=setTimeout(activate,delay);
  }
  function move(event,x,y){
    if(!gesture)return;
    const distance=Math.hypot(x-gesture.startX,y-gesture.startY);gesture.x=x;gesture.y=y;
    if(!gesture.active&&distance>8){if(gesture.touch||hold){clear();return}activate()}
    if(gesture?.active){if(event.cancelable)event.preventDefault();mark()}
  }
  function finish(cancelled=false){
    if(!gesture)return;
    const {active,row,target,after}=gesture;
    if(active)suppressUntil=Date.now()+500;
    const before=items().map(el=>el.dataset.sortId),order=items().filter(el=>el!==row);
    if(active&&target){const index=order.indexOf(target)+(after?1:0);order.splice(index,0,row)}
    const ids=order.map(el=>el.dataset.sortId);clear();
    if(active&&target&&!cancelled&&ids.join('|')!==before.join('|'))save(ids);
  }
  list.addEventListener('pointerdown',e=>{if(e.pointerType!=='touch'&&e.button===0)begin(e,e.clientX,e.clientY,false)},options);
  document.addEventListener('pointermove',e=>{if(gesture&&!gesture.touch)move(e,e.clientX,e.clientY)},options);
  document.addEventListener('pointerup',()=>{if(gesture&&!gesture.touch)finish()},options);
  document.addEventListener('pointercancel',()=>{if(gesture&&!gesture.touch)finish(true)},options);
  list.addEventListener('touchstart',e=>{if(e.touches.length===1)begin(e,e.touches[0].clientX,e.touches[0].clientY,true);else finish(true)},{...options,passive:true});
  document.addEventListener('touchmove',e=>{if(gesture?.touch){if(e.touches.length!==1)return finish(true);move(e,e.touches[0].clientX,e.touches[0].clientY)}},{...options,passive:false});
  document.addEventListener('touchend',()=>{if(gesture?.touch)finish()},options);
  document.addEventListener('touchcancel',()=>{if(gesture?.touch)finish(true)},options);
  document.addEventListener('keydown',e=>{if(e.key==='Escape'&&gesture){e.preventDefault();finish(true)}},options);
  window.addEventListener('blur',()=>finish(true),options);
  list.addEventListener('click',e=>{if(Date.now()<suppressUntil){e.preventDefault();e.stopImmediatePropagation()}},{...options,capture:true});
  list.addEventListener('contextmenu',e=>{if(gesture?.active){e.preventDefault();e.stopImmediatePropagation()}},{...options,capture:true});
}

async function savePlaylistOrder(ids){
  if(state.playlistSortBusy)return;
  state.playlistSortBusy=true;
  const expected=ownPlaylists().map(p=>p.id);
  try{if(typeof browseSavePlaylistOrder==='function'&&browseOwnPlaylistPage()?.total>browseOwnPlaylistPage()?.items.length)await browseSavePlaylistOrder(ids);else{await api('playlists/order','PUT',{order:ids,expected_order:expected});await refreshCore()}setStatus('歌单顺序已保存','ok')}
  catch(error){await refreshCore().catch(()=>{});setStatus('顺序保存失败：'+error.message,'error')}
  finally{state.playlistSortBusy=false}
}
function playlistItemMenu(event,playlist,card){
  event.preventDefault();const rect=card.getBoundingClientRect();
  showPlaylistMenu({clientX:event.clientX||rect.left+32,clientY:event.clientY||rect.top+24},playlist);
}
function playlistDuration(ms){if(!ms)return '—';const seconds=Math.floor(ms/1000);return Math.floor(seconds/60)+':'+String(seconds%60).padStart(2,'0')}
async function savePlaylistTrackOrder(p,ids,focusID){
  if(state.playlistTrackSortBusy)return;state.playlistTrackSortBusy=true;
  try{if(p.members_complete===false||p.browse_page&&p.browse_page.total>p.browse_page.items.length)await browseSaveMemberOrder(p,ids,focusID);else{await api('playlists/'+p.id+'/tracks/order','PUT',{track_ids:ids,expected_track_ids:[...members(p)]},undefined,p.version);await refreshCore()}setStatus('歌单曲目顺序已保存','ok')}
  catch(error){await refreshCore().catch(()=>{});setStatus('顺序保存失败：'+error.message,'error')}
  finally{state.playlistTrackSortBusy=false;if(focusID)$('playlistDetail').querySelector('[data-track-id="'+CSS.escape(focusID)+'"]')?.focus()}
}
function playlistTrackActions(p,track,unavailable){
  const ids=[...members(p)],index=ids.indexOf(track.id);
  const shift=offset=>{const order=ids.slice();[order[index],order[index+offset]]=[order[index+offset],order[index]];savePlaylistTrackOrder(p,order,track.id)};
  const base=[{label:'播放曲目',disabled:unavailable,action:()=>playTrack(track)},{label:'曲目详情',action:()=>openPlaylistTrackDetails(p,track,unavailable)}];
  if(p.read_only){if(p.subscription_status==='active')base.push({label:'个人备注',action:()=>editPlaylistTrackNote(p,track)});return base}
  return [{label:'播放曲目',disabled:unavailable,action:()=>playTrack(track)},
    {label:'曲目详情',action:()=>openPlaylistTrackDetails(p,track,unavailable)},
    {label:'个人备注',action:()=>editPlaylistTrackNote(p,track)},
    {label:'上移曲目',disabled:index<=0||state.playlistTrackSortBusy,action:()=>shift(-1)},
    {label:'下移曲目',disabled:index===ids.length-1||state.playlistTrackSortBusy,action:()=>shift(1)},
    {label:'移出歌单',action:()=>removePlaylistMember(p,track)}];
}
function showPlaylistTrackMenu(event,p,track,unavailable){
 const selection=state.playlistTrackSelection?.ids,ids=selection?.has(track.id)?[...selection]:[track.id];
 if(ids.length<2){showContextMenu(event,playlistTrackActions(p,track,unavailable));return}
 const unavailableIDs=new Set(p.unavailable_track_ids||[]),items=[{label:'曲目详情',action:()=>openPlaylistTrackDetails(p,track,unavailable)},{label:'添加所选 '+ids.length+' 首到我的歌单…',disabled:ids.some(id=>unavailableIDs.has(id)||state.tracks.find(t=>t.id===id)?.removed||state.tracks.find(t=>t.id===id)?.missing),action:()=>openAddTracks(ids)}];
 if(!p.read_only)items.push({label:'移出所选 '+ids.length+' 首曲目',action:()=>removePlaylistMembers(p,ids)});
 showContextMenu(event,items);
}
function removePlaylistMembers(p,ids){
 if(!confirm('将所选 '+ids.length+' 首曲目移出此歌单？'))return;
 run(async()=>{let version=p.version,completed=0;try{for(const id of ids){await api('playlists/'+p.id+'/tracks/'+id,'DELETE',undefined,undefined,version);completed++;if(completed<ids.length){const result=await api('playlists/'+p.id);version=(result.playlist||result).version}}}catch(error){throw new Error('已移出 '+completed+' / '+ids.length+' 首；'+error.message)}finally{state.playlistTrackSelection=null;await refreshCore()}});
}
function removePlaylistMember(p,track){
  if(!confirm('将“'+(track.title||'不可用曲目')+'”移出此歌单？'))return;
  run(async()=>{await api('playlists/'+p.id+'/tracks/'+track.id,'DELETE',undefined,undefined,p.version);await refreshCore();setStatus('曲目已移出歌单','ok')});
}
async function openPlaylistTrackDetails(p,track,unavailable){
  if(!unavailable){if(await openTrackDetails(track)===false)return}
  else{$('trackDetails').querySelector('h3').textContent='不可用曲目';$('trackDetailsBody').replaceChildren();$('trackDetails').showModal()}
  const section=document.createElement('section');section.className='playlist-track-personal';
  const title=document.createElement('h3');title.textContent='此歌单的个人备注';const note=document.createElement('p');note.textContent=p.track_notes?.[track.id]||'暂无个人备注';note.className='notice';
  const controls=document.createElement('div');controls.className='actions';
  for(const action of playlistTrackActions(p,track,unavailable).slice(2)){const button=makeButton(action.label,()=>{$('trackDetails').close();action.action()},action.label==='移出歌单'?'btn danger':'btn');button.disabled=Boolean(action.disabled);controls.append(button)}
  section.append(title,note,controls);$('trackDetailsBody').prepend(section);
}

const playlistEditor=document.createElement('section');playlistEditor.id='playlistEditor';playlistEditor.hidden=true;$('page-playlists').append(playlistEditor);
let playlistDraft=null,playlistReturnScroll=null,cropState=null,imageSelection=0;
function beginPlaylistCreate(){
  state.playlistEditReturn={creating:true,route:location.hash,y:scrollY};
  location.hash='#/playlists/new';
}
function beginPlaylistEdit(p){
  if(p.read_only)return;
  state.playlistEditReturn={id:p.id,route:location.hash,y:scrollY};
  location.hash='#/playlists/'+p.id+'/edit';
}
function returnPlaylistList(){playlistReturnScroll={route:'#/playlists',y:state.playlistListScrollY||0};showPage('playlists')}
function playlistDraftDirty(){return Boolean(playlistDraft&&(playlistDraft.name!==playlistDraft.original.name||playlistDraft.description!==(playlistDraft.original.description||'')||playlistDraft.visibility!==(playlistDraft.original.visibility||'private')||playlistDraft.coverChanged))}
function closePlaylistCrop(){imageSelection++;cropPointer=null;if(cropState){URL.revokeObjectURL(cropState.url);cropState=null}if(playlistCrop.open)playlistCrop.close()}
function disposePlaylistDraft(){closePlaylistCrop();if(playlistDraft?.previewURL)URL.revokeObjectURL(playlistDraft.previewURL);playlistDraft=null;playlistEditor.replaceChildren();playlistEditor.hidden=true;state.playlistEditing=false;state.playlistCreating=false;state.managedPlaylistEdit=null}
function clearPlaylistWorkspace(){disposePlaylistDraft();$('playlistList').sortCleanup?.();$('playlistDetail').querySelector('.playlist-tracks')?.sortCleanup?.();$('playlistDetail').replaceChildren();state.playlistEditReturn=null;state.playlistListScrollY=0;state.playlistTrackQuery='';state.playlistTrackSearchID=null;playlistReturnScroll=null}
function permitPlaylistNavigation(hash){
  if(!playlistDraft||hash===playlistDraft.route)return true;
  if(playlistDraft.saving){history.replaceState(null,'',playlistDraft.route);return false}
  if(playlistDraftDirty()&&!confirm('歌单修改尚未保存，放弃修改并离开？')){history.replaceState(null,'',playlistDraft.route);return false}
  if(hash===playlistDraft.returnRoute)playlistReturnScroll={route:hash,y:playlistDraft.returnY};
  disposePlaylistDraft();return true;
}
function restorePlaylistScroll(){if(playlistReturnScroll&&location.hash===playlistReturnScroll.route){const y=playlistReturnScroll.y;playlistReturnScroll=null;requestAnimationFrame(()=>scrollTo(0,y))}}
window.addEventListener('beforeunload',event=>{if(playlistDraftDirty()){event.preventDefault();event.returnValue=''}});
function renderPlaylistEditor(p,creating=false){
  if(playlistDraft?.id===p.id){playlistEditor.hidden=false;document.title=creating?'新建歌单 · 音乐服务器':p.name+' · 编辑歌单 · 音乐服务器';return}
  disposePlaylistDraft();state.playlistEditing=true;state.playlistCreating=creating;
  if(p.owner_id!==state.user.id)state.managedPlaylistEdit=p;
  const back=(creating?state.playlistEditReturn?.creating:state.playlistEditReturn?.id===p.id)?state.playlistEditReturn:null;
  playlistDraft={id:p.id,creating,original:{...p},name:p.name,description:p.description||'',visibility:p.visibility||'private',coverChanged:false,coverBlob:null,removeCover:false,previewURL:null,saving:false,route:creating?'#/playlists/new':'#/playlists/'+p.id+'/edit',returnRoute:back?.route||(creating?'#/playlists':'#/playlists/'+p.id),returnY:back?.y||0};
  const backButton=makeButton(creating?'← 返回我的歌单':back?.route.startsWith('#/admin/users')?'← 返回用户详情':'← 返回歌单',()=>location.hash=playlistDraft.returnRoute);backButton.id='playlistEditBack';backButton.classList.add('page-back');
  const intro=document.createElement('div');intro.className='page-intro';intro.innerHTML='<div><h1>编辑歌单</h1><p>调整名称、简介和封面，保存后应用。</p></div>';
  const form=document.createElement('form');form.id='playlistEditForm';form.className='panel playlist-editor-form';
  form.innerHTML='<div class="playlist-editor-grid"><div class="playlist-artwork"><span id="playlistCoverPreview" class="playlist-cover-preview">♪</span><div class="actions"><label class="btn" for="playlistCoverFile">更换图片</label><button id="removePlaylistCover" type="button" class="btn">移除</button></div><input id="playlistCoverFile" type="file" accept=".jpg,.jpeg,.png,.webp,image/jpeg,image/png,image/webp" hidden><p class="status">JPEG、PNG、静态 WebP<br>最多 8 MiB／2000 万像素</p></div><div class="playlist-editor-fields"><div class="field"><label for="playlistEditName">歌单名称</label><input id="playlistEditName" required maxlength="256"></div><div class="field"><label for="playlistEditDescription">歌单简介</label><textarea id="playlistEditDescription" rows="7" maxlength="8000" placeholder="写下这个歌单的故事…"></textarea></div></div></div><p id="playlistEditError" role="alert" class="notice" hidden></p><div class="playlist-editor-footer"><button id="reloadPlaylistDraft" type="button" class="btn" hidden>重新载入</button><button id="cancelPlaylistEdit" type="button" class="btn">取消</button><button id="savePlaylistEdit" type="submit" class="btn primary">保存修改</button></div>';
  if(creating){intro.querySelector('h1').textContent='新建歌单';intro.querySelector('p').textContent='设置名称、简介、可见性和封面，保存后创建。';form.querySelector('#savePlaylistEdit').textContent='创建歌单'}
  const danger=document.createElement('div');danger.className='playlist-danger-zone';const deleteButton=makeButton('删除歌单',()=>deletePlaylist(p),'btn danger');danger.append(deleteButton);
  playlistEditor.replaceChildren(backButton,intro,form,...(creating?[]:[danger]));playlistEditor.hidden=false;document.title=creating?'新建歌单 · 音乐服务器':p.name+' · 编辑歌单 · 音乐服务器';
  const visibility=document.createElement('div');visibility.className='field';visibility.innerHTML='<label for="playlistEditVisibility">可见性</label><select id="playlistEditVisibility"><option value="private">私有 · 仅本人</option><option value="public">公开 · 平台登录用户可见</option><option value="shared">仅分享 · 指定账户接受后可见</option></select><p class="status" style="white-space:normal">个人备注始终按账户隔离。仅分享需在歌单详情的分享设置中邀请账户；转私有会暂停外部授权。</p>';form.querySelector('.playlist-editor-fields').append(visibility);$('playlistEditVisibility').value=playlistDraft.visibility;$('playlistEditVisibility').onchange=e=>playlistDraft.visibility=e.target.value;
  if(p.owner_id!==state.user.id){const inspection=document.createElement('section');inspection.className='panel';const title=document.createElement('h3');title.textContent='歌单曲目（管理查看）';inspection.append(title);for(const id of p.track_ids||[]){const track=state.tracks.find(t=>t.id===id),row=document.createElement('p');row.textContent=(track?.title||'不可用曲目')+' · '+(p.track_notes?.[id]||'无个人备注');inspection.append(row)}playlistEditor.append(inspection)}
  scrollTo(0,0);
  $('playlistEditName').value=p.name;$('playlistEditDescription').value=p.description||'';
  $('playlistEditName').oninput=e=>playlistDraft.name=e.target.value;$('playlistEditDescription').oninput=e=>playlistDraft.description=e.target.value;
  if(p.cover_id){const image=document.createElement('span');image.style.display='contents';$('playlistCoverPreview').replaceChildren(image);loadCover(image,'covers/'+encodeURIComponent(p.cover_id))}
  $('playlistCoverFile').onchange=event=>{const file=event.target.files[0];event.target.value='';if(file)selectPlaylistCover(file).catch(error=>showPlaylistEditError(error))};
  $('removePlaylistCover').onclick=()=>{if(playlistDraft.previewURL)URL.revokeObjectURL(playlistDraft.previewURL);playlistDraft.previewURL=null;playlistDraft.coverBlob=null;playlistDraft.removeCover=true;playlistDraft.coverChanged=Boolean(p.cover_id);$('playlistCoverPreview').replaceChildren(document.createTextNode('♪'))};
  $('cancelPlaylistEdit').onclick=()=>location.hash=playlistDraft.returnRoute;
  $('reloadPlaylistDraft').onclick=async()=>{if(!confirm('重新载入会放弃当前草稿，继续？'))return;try{const fresh=await api('playlists/'+p.id);disposePlaylistDraft();renderPlaylistEditor(fresh.playlist)}catch(error){showPlaylistEditError(error)}};
  form.onsubmit=event=>{event.preventDefault();savePlaylistDraft()};
}
function showPlaylistEditError(error){if(!$('playlistEditError'))return;$('playlistEditError').textContent=error.message;$('playlistEditError').hidden=false;$('reloadPlaylistDraft').hidden=Boolean(playlistDraft?.creating)||error.status!==409}
async function savePlaylistDraft(){
  const draft=playlistDraft;if(!draft||draft.saving)return;
  if(!draft.name.trim())return showPlaylistEditError(new Error('请输入歌单名称'));
  draft.saving=true;$('savePlaylistEdit').disabled=true;$('playlistEditError').hidden=true;
  playlistEditor.querySelectorAll('input,textarea,select,button').forEach(el=>el.disabled=true);
  try{
    const data=new FormData();data.append('name',draft.name);data.append('description',draft.description);data.append('visibility',draft.visibility);
    if(draft.coverBlob)data.append('cover',draft.coverBlob,'cover.png');if(draft.removeCover)data.append('remove_cover','true');
    if(draft.creating)data.append('id',draft.id);
    const saved=await api(draft.creating?'playlists':'playlists/'+draft.id+'/editor',draft.creating?'POST':'PUT',data,undefined,draft.creating?undefined:draft.original.version);
    const index=state.playlists.findIndex(p=>p.id===saved.id);if(index>=0)state.playlists[index]=saved;else if(draft.creating)state.playlists.push(saved);
    draft.original={...saved};draft.coverChanged=false;draft.saving=false;clearCoverCache();
    communityCache.clear();
    location.hash=draft.creating?'#/playlists/'+saved.id:draft.returnRoute;setStatus(draft.creating?'歌单已创建':'歌单修改已保存','ok');
  }catch(error){showPlaylistEditError(error)}finally{if(playlistDraft===draft){draft.saving=false;playlistEditor.querySelectorAll('input,textarea,select,button').forEach(el=>el.disabled=false)}}
}

// Parse dimensions/animation before decoding the bounded source image.
function playlistImageInfo(buffer){
  const bytes=new Uint8Array(buffer),view=new DataView(buffer),text=(start,n)=>String.fromCharCode(...bytes.slice(start,start+n));let width=0,height=0;
  if(bytes.length>=24&&text(1,3)==='PNG'&&bytes[0]===137){
    width=view.getUint32(16);height=view.getUint32(20);
    for(let p=8;p+12<=bytes.length;){const size=view.getUint32(p);if(p+12+size>bytes.length)throw new Error('图片文件已损坏');if(text(p+4,4)==='acTL')throw new Error('不支持动画图片，请选择静态封面');p+=12+size}
  }else if(bytes.length>=12&&text(0,4)==='RIFF'&&text(8,4)==='WEBP'){
    for(let p=12;p+8<=bytes.length;){const size=view.getUint32(p+4,true),kind=text(p,4),start=p+8;if(start+size>bytes.length)throw new Error('图片文件已损坏');
      if(kind==='ANIM'||kind==='ANMF'||kind==='VP8X'&&(bytes[start]&2))throw new Error('不支持动画图片，请选择静态封面');
      if(kind==='VP8X'&&size>=10){width=1+bytes[start+4]+(bytes[start+5]<<8)+(bytes[start+6]<<16);height=1+bytes[start+7]+(bytes[start+8]<<8)+(bytes[start+9]<<16)}
      if(kind==='VP8 '&&size>=10){width=view.getUint16(start+6,true)&16383;height=view.getUint16(start+8,true)&16383}
      if(kind==='VP8L'&&size>=5){const bits=view.getUint32(start+1,true);width=(bits&16383)+1;height=((bits>>>14)&16383)+1}p=start+size+(size%2)
    }
  }else if(bytes.length>=4&&bytes[0]===255&&bytes[1]===216){
    for(let p=2;p+4<=bytes.length;){if(bytes[p]!==255)break;const marker=bytes[p+1];if(marker===255){p++;continue}if(marker===217||marker===218)break;const size=view.getUint16(p+2);if(size<2||p+2+size>bytes.length)throw new Error('图片文件已损坏');if([192,193,194,195,197,198,199,201,202,203,205,206,207].includes(marker)&&size>=7){height=view.getUint16(p+5);width=view.getUint16(p+7);break}p+=size+2}
  }else throw new Error('仅支持 JPEG、PNG 和静态 WebP 图片');
  if(!width||!height)throw new Error('无法读取图片尺寸');if(width*height>20000000)throw new Error('图片不得超过 2000 万像素');return {width,height};
}
const playlistCrop=document.createElement('dialog');playlistCrop.id='playlistCrop';playlistCrop.className='modal playlist-crop';
playlistCrop.innerHTML='<div class="modal-head"><h3>裁剪歌单封面</h3><button type="button" id="cancelPlaylistCrop" class="btn small" aria-label="取消裁剪">×</button></div><div class="modal-body"><p class="notice">拖动图片调整位置，使用滑块缩放。方框内为最终封面。</p><canvas id="playlistCropCanvas" width="600" height="600" tabindex="0" aria-label="封面裁剪区域，可用方向键移动图片"></canvas><div class="crop-controls"><label for="playlistCropZoom">缩放</label><input id="playlistCropZoom" type="range" min="1" max="4" step="0.01" value="1"><button id="resetPlaylistCrop" type="button" class="btn">重置</button></div></div><div class="modal-foot"><button id="applyPlaylistCrop" type="button" class="btn primary">使用此封面</button></div>';
document.body.append(playlistCrop);
$('cancelPlaylistCrop').onclick=closePlaylistCrop;playlistCrop.addEventListener('cancel',closePlaylistCrop);playlistCrop.addEventListener('close',()=>{if(cropState){URL.revokeObjectURL(cropState.url);cropState=null}});
async function selectPlaylistCover(file){
  if(!playlistDraft||playlistDraft.saving)return;if(file.size>8*1024*1024)throw new Error('图片大小不得超过 8 MiB');
  const generation=++imageSelection,draft=playlistDraft,buffer=await file.arrayBuffer();playlistImageInfo(buffer);
  const url=URL.createObjectURL(file),image=new Image();image.src=url;
  try{await image.decode();if(generation!==imageSelection||playlistDraft!==draft){URL.revokeObjectURL(url);return}if(cropState)URL.revokeObjectURL(cropState.url);
    cropState={url,image,zoom:1,cx:image.naturalWidth/2,cy:image.naturalHeight/2};$('playlistEditError').hidden=true;$('playlistCropZoom').value='1';$('applyPlaylistCrop').disabled=false;playlistCrop.showModal();drawPlaylistCrop();
  }catch(error){URL.revokeObjectURL(url);throw new Error('无法解码图片，请选择有效的静态图片')}
}
function drawPlaylistCrop(){
  if(!cropState)return;const crop=cropState,canvas=$('playlistCropCanvas'),ctx=canvas.getContext('2d'),size=canvas.width,frame=size*.8;
  crop.side=Math.min(crop.image.naturalWidth,crop.image.naturalHeight)/crop.zoom;const half=crop.side/2;crop.cx=Math.max(half,Math.min(crop.image.naturalWidth-half,crop.cx));crop.cy=Math.max(half,Math.min(crop.image.naturalHeight-half,crop.cy));crop.scale=frame/crop.side;
  ctx.clearRect(0,0,size,size);ctx.fillStyle='#e8edf5';ctx.fillRect(0,0,size,size);ctx.drawImage(crop.image,size/2-crop.cx*crop.scale,size/2-crop.cy*crop.scale,crop.image.naturalWidth*crop.scale,crop.image.naturalHeight*crop.scale);
  const inset=size*.1;ctx.fillStyle='#101a2c99';ctx.fillRect(0,0,size,inset);ctx.fillRect(0,size-inset,size,inset);ctx.fillRect(0,inset,inset,frame);ctx.fillRect(size-inset,inset,inset,frame);ctx.strokeStyle='#fff';ctx.lineWidth=3;ctx.strokeRect(inset,inset,frame,frame);
}
$('playlistCropZoom').oninput=e=>{if(cropState){cropState.zoom=Number(e.target.value);drawPlaylistCrop()}};
$('resetPlaylistCrop').onclick=()=>{if(cropState){cropState.zoom=1;cropState.cx=cropState.image.naturalWidth/2;cropState.cy=cropState.image.naturalHeight/2;$('playlistCropZoom').value='1';drawPlaylistCrop()}};
let cropPointer=null;
$('playlistCropCanvas').onpointerdown=e=>{cropPointer={x:e.clientX,y:e.clientY};e.currentTarget.setPointerCapture(e.pointerId)};
$('playlistCropCanvas').onpointermove=e=>{if(!cropPointer||!cropState)return;const ratio=e.currentTarget.width/e.currentTarget.getBoundingClientRect().width/cropState.scale;cropState.cx-=(e.clientX-cropPointer.x)*ratio;cropState.cy-=(e.clientY-cropPointer.y)*ratio;cropPointer={x:e.clientX,y:e.clientY};drawPlaylistCrop()};
$('playlistCropCanvas').onpointerup=$('playlistCropCanvas').onpointercancel=()=>cropPointer=null;
$('playlistCropCanvas').onkeydown=e=>{const offsets={ArrowLeft:[1,0],ArrowRight:[-1,0],ArrowUp:[0,1],ArrowDown:[0,-1]},offset=offsets[e.key];if(cropState&&offset){e.preventDefault();cropState.cx+=offset[0]*cropState.side/100*(e.shiftKey?10:1);cropState.cy+=offset[1]*cropState.side/100*(e.shiftKey?10:1);drawPlaylistCrop()}};
$('applyPlaylistCrop').onclick=async()=>{
  const crop=cropState,draft=playlistDraft;if(!crop||!draft)return;$('applyPlaylistCrop').disabled=true;
  const canvas=document.createElement('canvas');canvas.width=canvas.height=Math.max(1,Math.min(1024,Math.floor(crop.side)));
  canvas.getContext('2d').drawImage(crop.image,crop.cx-crop.side/2,crop.cy-crop.side/2,crop.side,crop.side,0,0,canvas.width,canvas.height);
  const blob=await new Promise(resolve=>canvas.toBlob(resolve,'image/png'));
  if(playlistDraft!==draft||cropState!==crop)return;if(!blob){$('applyPlaylistCrop').disabled=false;return showPlaylistEditError(new Error('裁剪失败，请重试'))}
  if(draft.previewURL)URL.revokeObjectURL(draft.previewURL);draft.previewURL=URL.createObjectURL(blob);draft.coverBlob=blob;draft.coverChanged=true;draft.removeCover=false;
  const img=document.createElement('img');img.src=draft.previewURL;img.alt='待保存的歌单封面';$('playlistCoverPreview').replaceChildren(img);closePlaylistCrop();
};
