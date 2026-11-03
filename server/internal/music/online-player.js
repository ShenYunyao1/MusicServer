'use strict';

const onlinePlayback={generation:0,track:null,controller:null,coverURL:null,coverSerial:0,lyrics:[],active:-1,seeking:false,seekTarget:null,follow:true,loaded:false};
let onlinePlayerBar=null,onlineLyricsPanel=null,onlineCover=null;
let onlineQueue=null,onlineQueueSerial=0,onlineQueueController=null,onlineQueuePanel=null;
let onlineTranslations=true;
let onlineLyricsExpanded=false,onlineLyricsDrag=null,onlineLyricClickAfter=0;
try{onlineTranslations=localStorage.getItem('musicserver.lyrics.translations')!=='off'}catch(_){}
try{onlineLyricsExpanded=localStorage.getItem('musicserver.lyrics.expanded')==='on'}catch(_){}
function playerTime(seconds){if(!Number.isFinite(seconds)||seconds<0)return '00:00';const whole=Math.floor(seconds);return String(Math.floor(whole/60)).padStart(2,'0')+':'+String(whole%60).padStart(2,'0')}
function playerIcon(name){const paths={play:'<path d="m9 5 11 7-11 7Z"/>',pause:'<path d="M8 5v14M16 5v14"/>',volume:'<path d="m11 4-6 5H2v6h3l6 5Z"/><path d="M15 8a6 6 0 0 1 0 8m3-11a10 10 0 0 1 0 14"/>',muted:'<path d="m11 4-6 5H2v6h3l6 5Z"/><path d="m16 9 6 6m0-6-6 6"/>',close:'<path d="m6 6 12 12M18 6 6 18"/>'};return '<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">'+paths[name]+'</svg>'}
function initializeOnlinePlayer(bar,audio){
 onlinePlayerBar=bar;audio.controls=false;audio.hidden=true;audio.preload='metadata';bar.classList.add('online-player');bar.setAttribute('aria-label','在线播放器');bar.innerHTML='<div id="onlineCover" class="online-cover" aria-hidden="true">♪</div><div class="online-player-body"><div class="online-player-heading"><div class="online-track-information"><strong id="previewTitle"></strong><span id="onlineArtist"></span></div><div class="online-player-actions"><button id="onlinePlay" type="button" class="online-icon online-play" aria-label="播放"></button><button id="onlineMute" type="button" class="online-icon" aria-label="静音"></button><input id="onlineVolume" class="online-volume" type="range" min="0" max="1" step="0.01" value="1" aria-label="音量"><button id="onlineLyricsToggle" type="button" class="online-lyrics-toggle" aria-expanded="false" aria-controls="onlineLyricsPanel">歌词</button><button id="onlineClose" type="button" class="online-icon" aria-label="关闭播放预览"></button></div></div><div class="online-progress-row"><time id="onlineElapsed">00:00</time><input id="onlineSeek" type="range" min="0" max="1000" value="0" step="1" aria-label="播放进度" disabled><time id="onlineDuration">00:00</time></div><p id="onlinePlayerStatus" class="online-player-status" role="status"></p></div>';
 bar.append(audio);onlineCover=$('onlineCover');
 if(typeof initializeListeningReporter==='function')initializeListeningReporter(audio);
 const previous=makeButton('‹',()=>moveOnlineQueue(-1),'online-icon'),next=makeButton('›',()=>moveOnlineQueue(1),'online-icon'),queue=makeButton('播放列表',()=>showOnlineQueue(onlineQueuePanel.hidden),'online-lyrics-toggle'),position=document.createElement('span');
 previous.id='onlinePrevious';next.id='onlineNext';queue.id='onlineQueueToggle';position.id='onlineQueuePosition';previous.setAttribute('aria-label','上一首');next.setAttribute('aria-label','下一首');queue.setAttribute('aria-controls','onlineQueuePanel');queue.setAttribute('aria-expanded','false');
 for(const [button,paths] of [[previous,'M6 5v14M18 5 8 12l10 7Z'],[next,'M18 5v14M6 5l10 7-10 7Z']])button.innerHTML='<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round" aria-hidden="true"><path d="'+paths+'"/></svg>';
 $('onlinePlay').before(previous);$('onlinePlay').after(next);$('onlineLyricsToggle').after(queue);$('onlineArtist').after(position);
 onlineQueuePanel=document.createElement('section');onlineQueuePanel.id='onlineQueuePanel';onlineQueuePanel.className='online-lyrics-panel online-queue-panel';onlineQueuePanel.hidden=true;onlineQueuePanel.setAttribute('aria-label','当前播放列表');onlineQueuePanel.innerHTML='<div class="online-lyrics-heading"><h2 id="onlineQueueTitle">播放列表</h2><button id="onlineQueueClose" type="button" class="online-icon" aria-label="收起播放列表">×</button></div><div id="onlineQueueItems" class="online-queue-items"></div>';document.body.append(onlineQueuePanel);$('onlineQueueClose').onclick=()=>showOnlineQueue(false);
 const panel=document.createElement('section');panel.id='onlineLyricsPanel';panel.className='online-lyrics-panel';panel.hidden=true;panel.setAttribute('aria-label','当前曲目歌词');panel.innerHTML='<div class="online-lyrics-heading"><div><span class="online-eyebrow">正在播放</span><h2 id="onlineLyricsTitle">歌词</h2></div><div class="online-lyrics-actions"><button id="onlineLyricsFollow" type="button" class="btn small" hidden>回到当前歌词</button><button id="onlineLyricsClose" type="button" class="online-icon" aria-label="收起歌词"></button></div></div><div id="onlineLyricsLines" class="online-lyrics-lines" tabindex="0" aria-label="歌词内容"></div>';
 onlineLyricsPanel=panel;document.body.append(panel);
 const translate=makeButton('译文',()=>{onlineTranslations=!onlineTranslations;try{localStorage.setItem('musicserver.lyrics.translations',onlineTranslations?'on':'off')}catch(_){}translate.setAttribute('aria-pressed',String(onlineTranslations));highlightOnlineLyrics(true)},'btn small');translate.id='onlineTranslations';translate.setAttribute('aria-pressed',String(onlineTranslations));$('onlineLyricsClose').before(translate);
 translate.setAttribute('aria-label','显示译文');
 const expand=makeButton(onlineLyricsExpanded?'折叠':'展开',()=>setOnlineLyricsExpanded(!onlineLyricsExpanded),'btn small');expand.id='onlineLyricsExpand';expand.setAttribute('aria-expanded',String(onlineLyricsExpanded));$('onlineLyricsClose').before(expand);panel.classList.toggle('online-lyrics-expanded',onlineLyricsExpanded);bindOnlineLyricsBrowsing($('onlineLyricsLines'));
 $('onlineClose').innerHTML=playerIcon('close');$('onlineLyricsClose').innerHTML=playerIcon('close');
 $('onlineClose').onclick=closeOnlinePlayer;$('onlinePlay').onclick=toggleOnlinePlayback;
 $('onlineMute').onclick=()=>{audio.muted=!audio.muted};initializeOnlineVolume(bar,audio);
 $('onlineLyricsToggle').onclick=()=>showOnlineLyrics(panel.hidden);$('onlineLyricsClose').onclick=()=>{showOnlineLyrics(false);$('onlineLyricsToggle').focus()};
 $('onlineLyricsFollow').onclick=()=>{onlinePlayback.follow=true;$('onlineLyricsFollow').hidden=true;highlightOnlineLyrics(true)};
 $('onlineSeek').oninput=()=>{onlinePlayback.seeking=true;$('onlineElapsed').textContent=playerTime(audio.duration*Number($('onlineSeek').value)/1000);$('onlineSeek').style.setProperty('--progress',Number($('onlineSeek').value)/10+'%')};
 $('onlineSeek').onchange=()=>{if(Number.isFinite(audio.duration)&&audio.duration>0)seekOnlinePlayer(audio.duration*Number($('onlineSeek').value)/1000);onlinePlayback.seeking=false;renderOnlinePlayer();highlightOnlineLyrics(true)};
 $('onlineSeek').onblur=()=>{onlinePlayback.seeking=false;renderOnlinePlayer()};
 for(const event of ['loadedmetadata','durationchange'])audio.addEventListener(event,applyOnlineSeekTarget);
 audio.addEventListener('seeked',()=>{if(onlinePlayback.seekTarget!==null&&Math.abs(audio.currentTime-onlinePlayback.seekTarget)<.25)onlinePlayback.seekTarget=null});
 for(const event of ['loadedmetadata','durationchange','timeupdate','play','pause','ended','volumechange','canplay'])audio.addEventListener(event,()=>{renderOnlinePlayer();highlightOnlineLyrics()});
 audio.addEventListener('ended',()=>{if(onlineQueue&&!onlineQueue.busy)moveOnlineQueue(1)});
 audio.addEventListener('waiting',()=>{if(onlinePlayback.track)$('onlinePlayerStatus').textContent='正在缓冲…'});
 audio.addEventListener('playing',()=>{$('onlinePlayerStatus').textContent=''});
 audio.addEventListener('error',()=>{if(onlinePlayback.track)$('onlinePlayerStatus').textContent='音频加载失败，请点击播放重试。'});
 document.addEventListener('keydown',event=>{if(event.key==='Escape'&&(!panel.hidden||!onlineQueuePanel.hidden)){event.preventDefault();showOnlineLyrics(false);showOnlineQueue(false)}});
 document.addEventListener('pointerdown',event=>{if(!onlinePlayerBar.contains(event.target)&&!panel.contains(event.target)&&!onlineQueuePanel.contains(event.target)){showOnlineLyrics(false);showOnlineQueue(false)}});
 if(typeof ResizeObserver==='function'){const observer=new ResizeObserver(updateOnlinePlayerLayout);observer.observe(bar)}
 window.addEventListener('resize',updateOnlinePlayerLayout);renderOnlinePlayer();
}
function updateOnlinePlayerLayout(){
 if(!onlinePlayerBar)return;const height=onlinePlayerBar.hidden?0:onlinePlayerBar.getBoundingClientRect().height;document.documentElement.style.setProperty('--online-player-height',height+'px');onlineLyricsPanel?.style.setProperty('--online-player-height',height+'px');
 if(typeof positionSelectionFloat==='function')positionSelectionFloat();
}
function renderOnlinePlayer(){
 if(!onlinePlayerBar)return;const audio=memberPlayer,playing=!audio.paused&&!audio.ended,play=$('onlinePlay'),playIcon=playing?'pause':'play';if(play.dataset.icon!==playIcon){play.dataset.icon=playIcon;play.innerHTML=playerIcon(playIcon)}play.setAttribute('aria-label',playing?'暂停':'播放');play.setAttribute('aria-pressed',String(playing));
 const muted=audio.muted||onlineOutput.level===0,muteIcon=muted?'muted':'volume';if($('onlineMute').dataset.icon!==muteIcon){$('onlineMute').dataset.icon=muteIcon;$('onlineMute').innerHTML=playerIcon(muteIcon)}$('onlineMute').setAttribute('aria-label',muted?'取消静音':'静音');renderOnlineVolume();
 const duration=Number.isFinite(audio.duration)&&audio.duration>0?audio.duration:0;$('onlineDuration').textContent=playerTime(duration);$('onlineSeek').disabled=!duration;
 if(!onlinePlayback.seeking){const ratio=duration?Math.max(0,Math.min(1,audio.currentTime/duration)):0;$('onlineSeek').value=String(Math.round(ratio*1000));$('onlineSeek').style.setProperty('--progress',ratio*100+'%');$('onlineElapsed').textContent=playerTime(audio.currentTime)}
 $('onlineSeek').setAttribute('aria-valuetext',playerTime(audio.currentTime)+' / '+playerTime(duration));
 renderOnlineQueueControls();
}
async function toggleOnlinePlayback(){
 if(onlineQueue?.pendingIndex!==undefined&&!onlineQueue.busy)return selectOnlineQueue(onlineQueue.pendingIndex,onlineQueue.pendingDirection);
 if(!onlinePlayback.track)return;if(!memberPlayer.error&&!memberPlayer.paused&&!memberPlayer.ended){memberPlayer.pause();return}
 const generation=onlinePlayback.generation;
 try{await ensureOnlineAudio();if(generation!==onlinePlayback.generation)return;if(memberPlayer.error){onlinePlayback.seekTarget??=Math.max(0,memberPlayer.currentTime||0);memberPlayer.src='/api/v2/media/'+encodeURIComponent(onlinePlayback.track.id);memberPlayer.load()}applyOnlineSeekTarget();await memberPlayer.play();if(generation===onlinePlayback.generation)$('onlinePlayerStatus').textContent=''}catch(error){if(generation===onlinePlayback.generation)$('onlinePlayerStatus').textContent=error.volume?error.message:'播放未开始，请再次点击播放。'}
}
function applyOnlineSeekTarget(){if(onlinePlayback.seekTarget===null||!onlinePlayback.track||!Number.isFinite(memberPlayer.duration)||memberPlayer.duration<=0)return;const target=Math.min(memberPlayer.duration,onlinePlayback.seekTarget);if(Math.abs(memberPlayer.currentTime-target)>.01)memberPlayer.currentTime=target}
function seekOnlinePlayer(time){onlinePlayback.seekTarget=Math.max(0,Math.min(memberPlayer.duration,time));applyOnlineSeekTarget()}
function releaseOnlineCover(){onlinePlayback.coverSerial++;if(onlinePlayback.coverURL)URL.revokeObjectURL(onlinePlayback.coverURL);onlinePlayback.coverURL=null;if(onlineCover)onlineCover.textContent='♪'}
async function loadOnlineCover(track,current,signal){
 releaseOnlineCover();const serial=onlinePlayback.coverSerial;if(!track.cover_id)return;
 try{const response=await fetch('/api/v2/covers/'+encodeURIComponent(track.cover_id),{credentials:'same-origin',cache:'no-store',signal});if(!response.ok)throw new Error('cover');const blob=await response.blob();if(!current()||serial!==onlinePlayback.coverSerial)return;const url=URL.createObjectURL(blob),img=document.createElement('img');onlinePlayback.coverURL=url;img.alt='';img.src=url;onlineCover.replaceChildren(img)}catch(_){}
}
function setOnlineTrackText(track){$('previewTitle').textContent=track.title||'未命名曲目';$('previewTitle').title=track.title||'未命名曲目';$('onlineArtist').textContent=[track.artist||'未知歌手',track.album].filter(Boolean).join(' · ');$('onlineArtist').title=$('onlineArtist').textContent;$('onlineLyricsTitle').textContent=track.title||'歌词'}
function playOnlineTrack(track,fromQueue=false){
 if(!state.user||track.missing||track.removed||!onlinePlayerBar)return;
 if(!fromQueue)clearOnlineQueue();
 if(typeof newListeningSession==='function')newListeningSession(track);
 onlinePlayback.plainLyrics='';onlinePlayback.textMode=false;
 const generation=++onlinePlayback.generation,owner=state.user.id;
 onlinePlayback.controller?.abort();const controller=new AbortController();onlinePlayback.controller=controller;
 const current=()=>generation===onlinePlayback.generation&&owner===state.user?.id&&!controller.signal.aborted;
 memberPlayer.pause();onlinePlayback.track={...track};onlinePlayback.lyrics=[];onlinePlayback.active=-1;onlinePlayback.follow=true;onlinePlayback.seeking=false;onlinePlayback.seekTarget=null;onlinePlayback.loaded=false;onlinePlayerBar.hidden=false;memberPlayer.hidden=true;setOnlineTrackText(track);$('onlinePlayerStatus').textContent='正在加载音频…';$('onlineLyricsFollow').hidden=true;showOnlineLyricMessage('正在读取歌词…');releaseOnlineCover();updateOnlinePlayerLayout();
 memberPlayer.src='/api/v2/media/'+encodeURIComponent(track.id);memberPlayer.load();toggleOnlinePlayback();
 loadOnlineCover(track,current,controller.signal);
 (async()=>{try{let full=track;if(track.detail_loaded===false||track.lyrics===undefined){const response=await fetch('/api/v2/tracks/'+encodeURIComponent(track.id),{credentials:'same-origin',cache:'no-store',signal:controller.signal});if(!response.ok)throw Object.assign(new Error('metadata'),{status:response.status});full=await response.json()}
  if(!current())return;if(full.missing||full.removed){memberPlayer.pause();$('onlinePlayerStatus').textContent='曲目已不可用';showOnlineLyricMessage('曲目已不可用');if(onlineQueue)moveOnlineQueue(1);return}
  onlinePlayback.track=full;onlinePlayback.loaded=true;setOnlineTrackText(full);if(full.cover_id!==track.cover_id)loadOnlineCover(full,current,controller.signal);renderOnlineLyrics(full.lyrics||'');
 }catch(error){if(current()){if(onlineQueue&&[401,403].includes(error.status)){onlineQueueError(error);return}if(onlineQueue&&error.status===404){memberPlayer.pause();moveOnlineQueue(1);return}showOnlineLyricMessage('歌词读取失败，请点击重试。',true)}}})();
}
function closeOnlinePlayer(){
 if(typeof finishListeningSession==='function')finishListeningSession();
 showOnlineVolume(false);
 clearOnlineQueue();
 onlinePlayback.plainLyrics='';onlinePlayback.textMode=false;
 onlinePlayback.generation++;onlinePlayback.controller?.abort();onlinePlayback.controller=null;onlinePlayback.track=null;onlinePlayback.lyrics=[];onlinePlayback.active=-1;onlinePlayback.seeking=false;onlinePlayback.seekTarget=null;onlinePlayback.loaded=false;releaseOnlineCover();memberPlayer.pause();memberPlayer.removeAttribute('src');memberPlayer.load();memberPlayer.hidden=true;
 if(onlinePlayerBar){onlinePlayerBar.hidden=true;$('previewTitle').textContent='';$('onlineArtist').textContent='';$('onlineLyricsTitle').textContent='歌词';$('onlinePlayerStatus').textContent='';$('onlineLyricsLines').replaceChildren();showOnlineLyrics(false);renderOnlinePlayer();updateOnlinePlayerLayout()}
}
function showOnlineLyrics(open){if(!onlineLyricsPanel||!onlinePlayback.track&&open)return;const restore=onlineLyricsPanel.contains(document.activeElement);if(open){showOnlineQueue(false);showOnlineVolume(false)}onlineLyricsPanel.hidden=!open;$('onlineLyricsToggle').setAttribute('aria-expanded',String(open));if(open){onlinePlayback.follow=true;$('onlineLyricsFollow').hidden=true;highlightOnlineLyrics(true);$('onlineLyricsLines').focus({preventScroll:true})}else if(restore&&!onlinePlayerBar.hidden)$('onlineLyricsToggle').focus({preventScroll:true});updateOnlinePlayerLayout()}

function clearOnlineQueue(){onlineQueueSerial++;onlineQueueController?.abort();onlineQueueController=null;onlineQueue=null;showOnlineQueue(false);renderOnlineQueueControls();if($('onlineQueueItems'))$('onlineQueueItems').replaceChildren();if($('onlineQueuePosition'))$('onlineQueuePosition').textContent='';if($('onlineQueueTitle'))$('onlineQueueTitle').textContent='播放列表'}
function renderOnlineQueueControls(){
 if(!$('onlinePrevious'))return;
 for(const id of ['onlinePrevious','onlineNext','onlineQueueToggle','onlineQueuePosition'])$(id).hidden=!onlineQueue;
 if(!onlineQueue)return;
 $('onlinePrevious').disabled=onlineQueue.busy||onlineQueue.index<=0;$('onlineNext').disabled=onlineQueue.busy||onlineQueue.index>=onlineQueue.items.length-1;
 $('onlineQueuePosition').textContent=onlineQueue.name+' · '+(onlineQueue.index+1)+' / '+onlineQueue.items.length;
}
function showOnlineQueue(open){
 if(!onlineQueuePanel)return;open=Boolean(open&&onlineQueue);const focused=onlineQueuePanel.contains(document.activeElement),focusIndex=[...$('onlineQueueItems').children].indexOf(document.activeElement);onlineQueuePanel.hidden=!open;$('onlineQueueToggle').setAttribute('aria-expanded',String(open));if(!open&&focused&&!onlinePlayerBar.hidden)$('onlineQueueToggle').focus({preventScroll:true});
 if(!open)return;showOnlineLyrics(false);showOnlineVolume(false);$('onlineQueueTitle').textContent=onlineQueue.name+' · 播放列表';const body=$('onlineQueueItems');body.replaceChildren();
 onlineQueue.items.forEach((track,index)=>{const button=makeButton((index+1)+'. '+(track.title||'未命名曲目')+(track.missing||track.removed?' · 不可用':''),()=>selectOnlineQueue(index),'online-queue-item');button.disabled=Boolean(track.missing||track.removed);button.setAttribute('aria-current',String(index===onlineQueue.index));body.append(button)});
 body.querySelector('[aria-current="true"]')?.scrollIntoView?.({block:'nearest'});
 if(focused&&focusIndex>=0)(body.children[focusIndex]||body.querySelector('[aria-current="true"]'))?.focus({preventScroll:true});
}
async function onlineJSON(path,signal){
 const response=await fetch('/api/v2/'+path,{credentials:'same-origin',cache:'no-store',signal});
 if(!response.ok){const error=new Error('HTTP '+response.status);error.status=response.status;try{error.code=(await response.json()).error?.code}catch(_){}throw error}return response.json();
}
function onlineQueuePath(p,offset=0,limit=100,revision){const params=new URLSearchParams({source:p.source,offset:String(offset),limit:String(limit)});if(revision!==undefined)params.set('revision',String(revision));return 'browse/playback/'+encodeURIComponent(p.id)+'?'+params}
function onlineQueueError(error){
 if([401,403,404].includes(error.status)){memberPlayer.pause();clearOnlineQueue();$('onlinePlayerStatus').textContent='登录或歌单访问权限已失效，已停止顺序播放。'}
 else $('onlinePlayerStatus').textContent='播放列表读取失败，请重试：'+error.message;
}
async function playPlaylistTrack(playlist,track,source){
 if(!state.user||playlist.system||playlist.subscription_status==='invalid')return;
 clearOnlineQueue();memberPlayer.pause();onlinePlayback.generation++;onlinePlayback.controller?.abort();
 const serial=onlineQueueSerial,owner=state.user.id,controller=new AbortController();onlineQueueController=controller;
 const current=()=>serial===onlineQueueSerial&&owner===state.user?.id&&!controller.signal.aborted;
 const p={id:playlist.id,name:playlist.name,source:source|| (playlist.subscription_status?'subscription':'own')};
 try{
  let items=[],revision,name=p.name;
  for(let attempt=0;attempt<3;attempt++){
   items=[];revision=undefined;
   try{for(let offset=0;;){const page=await onlineJSON(onlineQueuePath(p,offset,100,revision),controller.signal);if(!current())return;
    if(!Array.isArray(page.items)||!Number.isInteger(page.total)||page.total<offset+page.items.length||!Number.isFinite(page.revision))throw new Error('播放目录格式无效');
    if(revision!==undefined&&revision!==page.revision)throw Object.assign(new Error('目录变化'),{code:'BROWSE_RESTART'});
    revision=page.revision;name=page.playlist?.name||name;items.push(...page.items);offset+=page.items.length;if(offset>=page.total)break;if(!page.items.length)throw new Error('播放目录不完整');
   }break}catch(error){if(error.code==='BROWSE_RESTART'&&attempt<2)continue;throw error}
  }
  if(!current())return;let index=track?items.findIndex(item=>item.id===track.id):items.findIndex(item=>!item.missing&&!item.removed);
  if(index<0||items[index].missing||items[index].removed)throw new Error('歌单中没有目标可播放曲目，请刷新后重试');
  onlineQueue={...p,name,items,index,busy:false};playOnlineTrack(items[index],true);renderOnlineQueueControls();
 }catch(error){if(current()&&error.name!=='AbortError'){onlineQueueError(error);if(typeof setStatus==='function')setStatus('无法开始歌单播放：'+error.message,'error')}}
}
function moveOnlineQueue(direction){if(!onlineQueue||onlineQueue.busy)return;const index=onlineQueue.index+direction;if(index<0||index>=onlineQueue.items.length)return;return selectOnlineQueue(index,direction)}
async function selectOnlineQueue(index,direction=1){
 const queue=onlineQueue;if(!queue||queue.busy)return;queue.busy=true;memberPlayer.pause();onlinePlayback.generation++;onlinePlayback.controller?.abort();renderOnlineQueueControls();
 const serial=onlineQueueSerial,owner=state.user?.id,current=()=>queue===onlineQueue&&serial===onlineQueueSerial&&owner===state.user?.id;
 try{
  await onlineJSON(onlineQueuePath(queue,0,1),onlineQueueController?.signal);if(!current())return;
  let skipped=0;
  for(;index>=0&&index<queue.items.length;index+=direction){
   const item=queue.items[index];if(item.missing||item.removed){skipped++;continue}
   let full;try{full=await onlineJSON('tracks/'+encodeURIComponent(item.id),onlineQueueController?.signal)}catch(error){if(error.status===404){skipped++;continue}throw error}if(!current())return;
   if(full.missing||full.removed){skipped++;continue}
   delete queue.pendingIndex;delete queue.pendingDirection;queue.index=index;const {lyrics,...summary}=full;queue.items[index]={...summary,detail_loaded:false};queue.busy=false;playOnlineTrack({...full,detail_loaded:true},true);if(skipped)$('onlinePlayerStatus').textContent='已跳过 '+skipped+' 首不可用曲目';return;
  }
  $('onlinePlayerStatus').textContent=skipped?'剩余曲目不可用，播放已停止。':'歌单播放完毕。';
 }catch(error){if(current()&&error.name!=='AbortError'){queue.pendingIndex=index;queue.pendingDirection=direction;onlineQueueError(error)}}
 finally{if(current()){queue.busy=false;renderOnlineQueueControls();if(!onlineQueuePanel.hidden)showOnlineQueue(true)}}
}
function showOnlineLyricMessage(text,retry=false){
 const body=$('onlineLyricsLines');body.classList.remove('online-lyrics-text-mode');body.replaceChildren();const message=document.createElement('p');message.className='online-lyrics-message';message.textContent=text;body.append(message);if(retry){const b=makeButton('重试读取歌词',()=>{const track=onlinePlayback.track;if(track)reloadOnlineLyrics(track)},'btn small');body.append(b)}
}
async function reloadOnlineLyrics(track){
 const generation=onlinePlayback.generation,owner=state.user?.id;showOnlineLyricMessage('正在读取歌词…');try{const response=await fetch('/api/v2/tracks/'+encodeURIComponent(track.id),{credentials:'same-origin',cache:'no-store',signal:onlinePlayback.controller?.signal});if(!response.ok)throw new Error('lyrics');const full=await response.json();if(generation!==onlinePlayback.generation||owner!==state.user?.id)return;onlinePlayback.track=full;renderOnlineLyrics(full.lyrics||'')}catch(_){if(generation===onlinePlayback.generation&&owner===state.user?.id)showOnlineLyricMessage('歌词读取失败，请点击重试。',true)}
}
function renderOnlineLyrics(text){
 const parsed=parsePlayerLyrics(text);onlinePlayback.lyrics=groupPlayerLyrics(parsed.lines);onlinePlayback.plainLyrics=parsed.lines.map(line=>line.text).join('\n');onlinePlayback.textMode=false;onlinePlayback.active=-1;onlinePlayback.follow=true;const body=$('onlineLyricsLines');body.dataset.lyricMode='';body.classList.remove('online-lyrics-text-mode');body.replaceChildren();$('onlineTranslations').hidden=!onlinePlayback.lyrics.some(line=>line.translations.length);$('onlineLyricsExpand').hidden=!parsed.timed;$('onlineLyricsFollow').hidden=true;
 if(!parsed.lines.length){showOnlineLyricMessage('这首曲目暂无歌词');return}
 if(!parsed.timed){showOnlineLyricMessage('暂无同步歌词');body.append(makeButton('查看文本歌词',showOnlineTextLyrics,'btn small'));return}
 highlightOnlineLyrics(true);
}
function highlightOnlineLyrics(force=false){
 if(!onlineLyricsPanel||!onlinePlayback.lyrics.length||onlinePlayback.textMode)return;const index=playerLyricIndex(onlinePlayback.lyrics,memberPlayer.currentTime);if(index===onlinePlayback.active&&!force)return;
 onlinePlayback.active=index;const body=$('onlineLyricsLines'),savedScroll=body.scrollTop||0,focus=document.activeElement,focusedIndex=focus!==body&&body.contains(focus)&&/^\d+$/.test(focus?.dataset?.lyricIndex||'')?focus.dataset.lyricIndex:null;
 if(onlineLyricsExpanded&&body.dataset.lyricMode==='expanded'&&!force){body.querySelector('[aria-current="true"]')?.removeAttribute('aria-current');body.querySelector('[data-lyric-index="'+index+'"]')?.setAttribute('aria-current','true');followOnlineLyrics();return}
 body.replaceChildren();body.dataset.lyricMode=onlineLyricsExpanded?'expanded':'collapsed';
 const indices=onlineLyricsExpanded?onlinePlayback.lyrics.map((_,i)=>i):[index-1,index,index+1];
 for(const lyricIndex of indices){
  const line=onlinePlayback.lyrics[lyricIndex],element=document.createElement(line?'button':'div');element.className='online-lyric-line';
  if(!line){element.setAttribute('aria-hidden','true');body.append(element);continue}
  element.type='button';element.dataset.lyricIndex=String(lyricIndex);element.setAttribute('aria-label',playerTime(line.time)+' '+[line.text||'间奏',...(onlineTranslations?line.translations:[])].join(' · '));if(lyricIndex===index)element.setAttribute('aria-current','true');
  const original=document.createElement('span');original.className='online-lyric-original';original.textContent=line.text||'♪';element.append(original);
  if(onlineTranslations)for(const text of line.translations){const translation=document.createElement('span');translation.className='online-lyric-translation';translation.textContent=text;element.append(translation)}
  element.onclick=()=>{if(Date.now()<onlineLyricClickAfter||!Number.isFinite(memberPlayer.duration))return;seekOnlinePlayer(line.time);onlinePlayback.follow=true;$('onlineLyricsFollow').hidden=true;renderOnlinePlayer();highlightOnlineLyrics(true);if(memberPlayer.paused||memberPlayer.ended||memberPlayer.error)toggleOnlinePlayback()};body.append(element);
 }
 if(focusedIndex!==null)(body.querySelector('[data-lyric-index="'+focusedIndex+'"]')||body.querySelector('[aria-current="true"]')||body).focus({preventScroll:true});
 if(onlineLyricsExpanded&&!onlinePlayback.follow)body.scrollTop=savedScroll;else followOnlineLyrics();
}
function setOnlineLyricsExpanded(expanded){onlineLyricsExpanded=Boolean(expanded);try{localStorage.setItem('musicserver.lyrics.expanded',onlineLyricsExpanded?'on':'off')}catch(_){}onlineLyricsPanel.classList.toggle('online-lyrics-expanded',onlineLyricsExpanded);$('onlineLyricsExpand').textContent=onlineLyricsExpanded?'折叠':'展开';$('onlineLyricsExpand').setAttribute('aria-expanded',String(onlineLyricsExpanded));onlinePlayback.follow=true;$('onlineLyricsFollow').hidden=true;highlightOnlineLyrics(true)}
function followOnlineLyrics(){if(!onlineLyricsExpanded||!onlinePlayback.follow||onlineLyricsPanel.hidden)return;const body=$('onlineLyricsLines'),active=body.querySelector('[aria-current="true"]');if(active)body.scrollTo({top:Math.max(0,(body.scrollTop||0)+active.getBoundingClientRect().top-body.getBoundingClientRect().top-body.clientHeight*.4),behavior:'instant'})}
function pauseOnlineLyricsFollow(){if(!onlineLyricsExpanded||onlinePlayback.textMode||!onlinePlayback.lyrics.length)return;onlinePlayback.follow=false;$('onlineLyricsFollow').hidden=false}
function bindOnlineLyricsBrowsing(body){
 body.addEventListener('wheel',pauseOnlineLyricsFollow,{passive:true});body.addEventListener('touchstart',pauseOnlineLyricsFollow,{passive:true});
 body.addEventListener('keydown',event=>{if(['ArrowUp','ArrowDown','PageUp','PageDown','Home','End'].includes(event.key))pauseOnlineLyricsFollow()});
 body.addEventListener('pointerdown',event=>{if(!onlineLyricsExpanded||event.pointerType!=='mouse'||event.button!==0)return;onlineLyricsDrag={id:event.pointerId,y:event.clientY,scroll:body.scrollTop||0,moved:false};event.preventDefault()});
 body.addEventListener('pointermove',event=>{const drag=onlineLyricsDrag;if(!drag||drag.id!==event.pointerId)return;const distance=event.clientY-drag.y;if(Math.abs(distance)>6&&!drag.moved){drag.moved=true;body.setPointerCapture?.(event.pointerId)}if(drag.moved){pauseOnlineLyricsFollow();body.scrollTop=drag.scroll-distance;event.preventDefault()}});
 const finish=event=>{if(onlineLyricsDrag?.id!==event.pointerId)return;if(onlineLyricsDrag.moved)onlineLyricClickAfter=Date.now()+400;onlineLyricsDrag=null;if(body.hasPointerCapture?.(event.pointerId))body.releasePointerCapture(event.pointerId)};
 body.addEventListener('pointerup',finish);body.addEventListener('pointercancel',finish);body.addEventListener('lostpointercapture',()=>{onlineLyricsDrag=null});
}
function showOnlineTextLyrics(){const body=$('onlineLyricsLines');onlinePlayback.textMode=true;body.classList.add('online-lyrics-text-mode');body.replaceChildren();const text=document.createElement('pre');text.className='online-lyrics-plain';text.textContent=onlinePlayback.plainLyrics||'';body.append(text);if(onlinePlayback.lyrics.length)body.append(makeButton('返回同步歌词',()=>{onlinePlayback.textMode=false;body.classList.remove('online-lyrics-text-mode');highlightOnlineLyrics(true)},'btn small'))}
