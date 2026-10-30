'use strict';

const onlinePlayback={generation:0,track:null,controller:null,coverURL:null,coverSerial:0,lyrics:[],active:-1,seeking:false,follow:true,loaded:false};
let onlinePlayerBar=null,onlineLyricsPanel=null,onlineCover=null;
function playerTime(seconds){if(!Number.isFinite(seconds)||seconds<0)return '00:00';const whole=Math.floor(seconds);return String(Math.floor(whole/60)).padStart(2,'0')+':'+String(whole%60).padStart(2,'0')}
function playerIcon(name){const paths={play:'<path d="m9 5 11 7-11 7Z"/>',pause:'<path d="M8 5v14M16 5v14"/>',volume:'<path d="m11 4-6 5H2v6h3l6 5Z"/><path d="M15 8a6 6 0 0 1 0 8m3-11a10 10 0 0 1 0 14"/>',muted:'<path d="m11 4-6 5H2v6h3l6 5Z"/><path d="m16 9 6 6m0-6-6 6"/>',close:'<path d="m6 6 12 12M18 6 6 18"/>'};return '<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">'+paths[name]+'</svg>'}
function initializeOnlinePlayer(bar,audio){
 onlinePlayerBar=bar;audio.controls=false;audio.hidden=true;audio.preload='metadata';bar.classList.add('online-player');bar.setAttribute('aria-label','在线播放器');bar.innerHTML='<div id="onlineCover" class="online-cover" aria-hidden="true">♪</div><div class="online-player-body"><div class="online-player-heading"><div class="online-track-information"><strong id="previewTitle"></strong><span id="onlineArtist"></span></div><div class="online-player-actions"><button id="onlinePlay" type="button" class="online-icon online-play" aria-label="播放"></button><button id="onlineMute" type="button" class="online-icon" aria-label="静音"></button><input id="onlineVolume" class="online-volume" type="range" min="0" max="1" step="0.01" value="1" aria-label="音量"><button id="onlineLyricsToggle" type="button" class="online-lyrics-toggle" aria-expanded="false" aria-controls="onlineLyricsPanel">歌词</button><button id="onlineClose" type="button" class="online-icon" aria-label="关闭播放预览"></button></div></div><div class="online-progress-row"><time id="onlineElapsed">00:00</time><input id="onlineSeek" type="range" min="0" max="1000" value="0" step="1" aria-label="播放进度" disabled><time id="onlineDuration">00:00</time></div><p id="onlinePlayerStatus" class="online-player-status" role="status"></p></div>';
 bar.append(audio);onlineCover=$('onlineCover');
 const panel=document.createElement('section');panel.id='onlineLyricsPanel';panel.className='online-lyrics-panel';panel.hidden=true;panel.setAttribute('aria-label','当前曲目歌词');panel.innerHTML='<div class="online-lyrics-heading"><div><span class="online-eyebrow">正在播放</span><h2 id="onlineLyricsTitle">歌词</h2></div><div class="online-lyrics-actions"><button id="onlineLyricsFollow" type="button" class="btn small" hidden>回到当前歌词</button><button id="onlineLyricsClose" type="button" class="online-icon" aria-label="收起歌词"></button></div></div><div id="onlineLyricsLines" class="online-lyrics-lines" tabindex="0" aria-label="歌词内容"></div>';
 onlineLyricsPanel=panel;document.body.append(panel);
 $('onlineClose').innerHTML=playerIcon('close');$('onlineLyricsClose').innerHTML=playerIcon('close');
 $('onlineClose').onclick=closeOnlinePlayer;$('onlinePlay').onclick=toggleOnlinePlayback;
 $('onlineMute').onclick=()=>{audio.muted=!audio.muted};$('onlineVolume').oninput=()=>{audio.volume=Number($('onlineVolume').value);audio.muted=audio.volume===0};
 $('onlineLyricsToggle').onclick=()=>showOnlineLyrics(panel.hidden);$('onlineLyricsClose').onclick=()=>{showOnlineLyrics(false);$('onlineLyricsToggle').focus()};
 $('onlineLyricsFollow').onclick=()=>{onlinePlayback.follow=true;$('onlineLyricsFollow').hidden=true;highlightOnlineLyrics(true)};
 for(const type of ['wheel','touchstart','pointerdown'])$('onlineLyricsLines').addEventListener(type,()=>{onlinePlayback.follow=false;$('onlineLyricsFollow').hidden=!onlinePlayback.lyrics.some(line=>line.time!==null)},{passive:true});
 $('onlineSeek').oninput=()=>{onlinePlayback.seeking=true;$('onlineElapsed').textContent=playerTime(audio.duration*Number($('onlineSeek').value)/1000);$('onlineSeek').style.setProperty('--progress',Number($('onlineSeek').value)/10+'%')};
 $('onlineSeek').onchange=()=>{if(Number.isFinite(audio.duration)&&audio.duration>0)audio.currentTime=audio.duration*Number($('onlineSeek').value)/1000;onlinePlayback.seeking=false;renderOnlinePlayer();highlightOnlineLyrics(true)};
 $('onlineSeek').onblur=()=>{onlinePlayback.seeking=false;renderOnlinePlayer()};
 for(const event of ['loadedmetadata','durationchange','timeupdate','play','pause','ended','volumechange','canplay'])audio.addEventListener(event,()=>{renderOnlinePlayer();highlightOnlineLyrics()});
 audio.addEventListener('waiting',()=>{if(onlinePlayback.track)$('onlinePlayerStatus').textContent='正在缓冲…'});
 audio.addEventListener('playing',()=>{$('onlinePlayerStatus').textContent=''});
 audio.addEventListener('error',()=>{if(onlinePlayback.track)$('onlinePlayerStatus').textContent='音频加载失败，请点击播放重试。'});
 document.addEventListener('keydown',event=>{if(event.key==='Escape'&&!panel.hidden){event.preventDefault();showOnlineLyrics(false)}});
 if(typeof ResizeObserver==='function'){const observer=new ResizeObserver(updateOnlinePlayerLayout);observer.observe(bar)}
 window.addEventListener('resize',updateOnlinePlayerLayout);renderOnlinePlayer();
}
function updateOnlinePlayerLayout(){
 if(!onlinePlayerBar)return;const height=onlinePlayerBar.hidden?0:onlinePlayerBar.getBoundingClientRect().height;document.documentElement.style.setProperty('--online-player-height',height+'px');onlineLyricsPanel?.style.setProperty('--online-player-height',height+'px');
 if(typeof positionSelectionFloat==='function')positionSelectionFloat();
}
function renderOnlinePlayer(){
 if(!onlinePlayerBar)return;const audio=memberPlayer,playing=!audio.paused&&!audio.ended,play=$('onlinePlay'),playIcon=playing?'pause':'play';if(play.dataset.icon!==playIcon){play.dataset.icon=playIcon;play.innerHTML=playerIcon(playIcon)}play.setAttribute('aria-label',playing?'暂停':'播放');play.setAttribute('aria-pressed',String(playing));
 const muted=audio.muted||audio.volume===0,muteIcon=muted?'muted':'volume';if($('onlineMute').dataset.icon!==muteIcon){$('onlineMute').dataset.icon=muteIcon;$('onlineMute').innerHTML=playerIcon(muteIcon)}$('onlineMute').setAttribute('aria-label',muted?'取消静音':'静音');$('onlineVolume').value=String(audio.volume);
 const duration=Number.isFinite(audio.duration)&&audio.duration>0?audio.duration:0;$('onlineDuration').textContent=playerTime(duration);$('onlineSeek').disabled=!duration;
 if(!onlinePlayback.seeking){const ratio=duration?Math.max(0,Math.min(1,audio.currentTime/duration)):0;$('onlineSeek').value=String(Math.round(ratio*1000));$('onlineSeek').style.setProperty('--progress',ratio*100+'%');$('onlineElapsed').textContent=playerTime(audio.currentTime)}
 $('onlineSeek').setAttribute('aria-valuetext',playerTime(audio.currentTime)+' / '+playerTime(duration));
}
async function toggleOnlinePlayback(){
 if(!onlinePlayback.track)return;if(!memberPlayer.paused&&!memberPlayer.ended){memberPlayer.pause();return}
 const generation=onlinePlayback.generation;
 try{if(memberPlayer.error){memberPlayer.src='/api/v2/media/'+encodeURIComponent(onlinePlayback.track.id);memberPlayer.load()}await memberPlayer.play();if(generation===onlinePlayback.generation)$('onlinePlayerStatus').textContent=''}catch(error){if(generation===onlinePlayback.generation)$('onlinePlayerStatus').textContent='播放未开始，请再次点击播放。'}
}
function releaseOnlineCover(){onlinePlayback.coverSerial++;if(onlinePlayback.coverURL)URL.revokeObjectURL(onlinePlayback.coverURL);onlinePlayback.coverURL=null;if(onlineCover)onlineCover.textContent='♪'}
async function loadOnlineCover(track,current,signal){
 releaseOnlineCover();const serial=onlinePlayback.coverSerial;if(!track.cover_id)return;
 try{const response=await fetch('/api/v2/covers/'+encodeURIComponent(track.cover_id),{credentials:'same-origin',cache:'no-store',signal});if(!response.ok)throw new Error('cover');const blob=await response.blob();if(!current()||serial!==onlinePlayback.coverSerial)return;const url=URL.createObjectURL(blob),img=document.createElement('img');onlinePlayback.coverURL=url;img.alt='';img.src=url;onlineCover.replaceChildren(img)}catch(_){}
}
function setOnlineTrackText(track){$('previewTitle').textContent=track.title||'未命名曲目';$('previewTitle').title=track.title||'未命名曲目';$('onlineArtist').textContent=[track.artist||'未知歌手',track.album].filter(Boolean).join(' · ');$('onlineArtist').title=$('onlineArtist').textContent;$('onlineLyricsTitle').textContent=track.title||'歌词'}
function playOnlineTrack(track){
 if(!state.user||track.missing||track.removed||!onlinePlayerBar)return;
 const generation=++onlinePlayback.generation,owner=state.user.id,epoch=typeof browseState==='undefined'?0:browseState.epoch;
 onlinePlayback.controller?.abort();const controller=new AbortController();onlinePlayback.controller=controller;
 const current=()=>generation===onlinePlayback.generation&&owner===state.user?.id&&(typeof browseState==='undefined'||epoch===browseState.epoch)&&!controller.signal.aborted;
 memberPlayer.pause();onlinePlayback.track={...track};onlinePlayback.lyrics=[];onlinePlayback.active=-1;onlinePlayback.follow=true;onlinePlayback.seeking=false;onlinePlayback.loaded=false;onlinePlayerBar.hidden=false;memberPlayer.hidden=true;setOnlineTrackText(track);$('onlinePlayerStatus').textContent='正在加载音频…';$('onlineLyricsFollow').hidden=true;showOnlineLyricMessage('正在读取歌词…');releaseOnlineCover();updateOnlinePlayerLayout();
 memberPlayer.src='/api/v2/media/'+encodeURIComponent(track.id);memberPlayer.load();toggleOnlinePlayback();
 loadOnlineCover(track,current,controller.signal);
 (async()=>{try{let full=track;if(track.detail_loaded===false||track.lyrics===undefined){const response=await fetch('/api/v2/tracks/'+encodeURIComponent(track.id),{credentials:'same-origin',cache:'no-store',signal:controller.signal});if(!response.ok)throw new Error('metadata');full=await response.json()}
  if(!current())return;if(full.missing||full.removed){memberPlayer.pause();$('onlinePlayerStatus').textContent='曲目已不可用';showOnlineLyricMessage('曲目已不可用');return}
  onlinePlayback.track=full;onlinePlayback.loaded=true;setOnlineTrackText(full);if(full.cover_id!==track.cover_id)loadOnlineCover(full,current,controller.signal);renderOnlineLyrics(full.lyrics||'');
 }catch(error){if(current())showOnlineLyricMessage('歌词读取失败，请点击重试。',true)}})();
}
function closeOnlinePlayer(){
 onlinePlayback.generation++;onlinePlayback.controller?.abort();onlinePlayback.controller=null;onlinePlayback.track=null;onlinePlayback.lyrics=[];onlinePlayback.active=-1;onlinePlayback.seeking=false;onlinePlayback.loaded=false;releaseOnlineCover();memberPlayer.pause();memberPlayer.removeAttribute('src');memberPlayer.load();memberPlayer.hidden=true;
 if(onlinePlayerBar){onlinePlayerBar.hidden=true;$('previewTitle').textContent='';$('onlineArtist').textContent='';$('onlineLyricsTitle').textContent='歌词';$('onlinePlayerStatus').textContent='';$('onlineLyricsLines').replaceChildren();showOnlineLyrics(false);renderOnlinePlayer();updateOnlinePlayerLayout()}
}
function showOnlineLyrics(open){if(!onlineLyricsPanel||!onlinePlayback.track&&open)return;onlineLyricsPanel.hidden=!open;$('onlineLyricsToggle').setAttribute('aria-expanded',String(open));if(open){onlinePlayback.follow=true;$('onlineLyricsFollow').hidden=true;highlightOnlineLyrics(true);$('onlineLyricsLines').focus({preventScroll:true})}updateOnlinePlayerLayout()}
function showOnlineLyricMessage(text,retry=false){
 const body=$('onlineLyricsLines');body.replaceChildren();const message=document.createElement('p');message.className='online-lyrics-message';message.textContent=text;body.append(message);if(retry){const b=makeButton('重试读取歌词',()=>{const track=onlinePlayback.track;if(track)reloadOnlineLyrics(track)},'btn small');body.append(b)}
}
async function reloadOnlineLyrics(track){
 const generation=onlinePlayback.generation,owner=state.user?.id;showOnlineLyricMessage('正在读取歌词…');try{const response=await fetch('/api/v2/tracks/'+encodeURIComponent(track.id),{credentials:'same-origin',cache:'no-store',signal:onlinePlayback.controller?.signal});if(!response.ok)throw new Error('lyrics');const full=await response.json();if(generation!==onlinePlayback.generation||owner!==state.user?.id)return;onlinePlayback.track=full;renderOnlineLyrics(full.lyrics||'')}catch(_){if(generation===onlinePlayback.generation&&owner===state.user?.id)showOnlineLyricMessage('歌词读取失败，请点击重试。',true)}
}
function renderOnlineLyrics(text){
 const parsed=parsePlayerLyrics(text);onlinePlayback.lyrics=parsed.lines;onlinePlayback.active=-1;const body=$('onlineLyricsLines');body.replaceChildren();if(!parsed.lines.length){showOnlineLyricMessage('这首曲目暂无歌词');return}
 const fragment=document.createDocumentFragment();parsed.lines.forEach((line,index)=>{const element=document.createElement(line.time===null?'p':'button');element.className='online-lyric-line';element.dataset.lyricIndex=String(index);element.textContent=line.text||'♪';if(line.time!==null){element.type='button';element.setAttribute('aria-label',playerTime(line.time)+' '+(line.text||'间奏'));element.onclick=()=>{if(!Number.isFinite(memberPlayer.duration))return;memberPlayer.currentTime=Math.min(memberPlayer.duration,Math.max(0,line.time));onlinePlayback.follow=true;$('onlineLyricsFollow').hidden=true;renderOnlinePlayer();highlightOnlineLyrics(true)}}fragment.append(element)});body.append(fragment);highlightOnlineLyrics(true);
}
function highlightOnlineLyrics(force=false){
 if(!onlineLyricsPanel||!onlinePlayback.lyrics.length)return;const index=playerLyricIndex(onlinePlayback.lyrics,memberPlayer.currentTime);if(index===onlinePlayback.active&&!force)return;
 const body=$('onlineLyricsLines');body.querySelector('[aria-current="true"]')?.removeAttribute('aria-current');onlinePlayback.active=index;const active=index<0?null:body.querySelector('[data-lyric-index="'+index+'"]');if(active){active.setAttribute('aria-current','true');if(onlinePlayback.follow&&!onlineLyricsPanel.hidden)body.scrollTo({top:Math.max(0,body.scrollTop+active.getBoundingClientRect().top-body.getBoundingClientRect().top-body.clientHeight*.4),behavior:force?'instant':'smooth'})}
}
