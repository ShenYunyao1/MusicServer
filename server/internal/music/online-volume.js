'use strict';

// This is an output attenuation setting, independent of the device's volume steps.
const onlineOutput={level:1,audio:null,context:null,source:null,gain:null,failed:false,panel:null};
try{const saved=localStorage.getItem('musicserver.player.volume');if(saved!==null&&Number.isFinite(Number(saved)))onlineOutput.level=Math.max(0,Math.min(1,Number(saved)))}catch(_){}
function volumeFailure(){const error=new Error('播放器音量未能安全应用，请重新点击播放；仍失败时刷新页面。');error.volume=true;return error}
function applyOnlineVolume(){
 const output=onlineOutput;if(!output.audio)return;
 if(output.failed)throw volumeFailure();
 if(output.gain){output.gain.gain.value=output.level;output.audio.volume=1}
 else{output.audio.volume=output.level;if(Math.abs(output.audio.volume-output.level)>.001)throw volumeFailure()}
}
async function ensureOnlineAudio(){
 const output=onlineOutput;if(output.failed)throw volumeFailure();
 const Context=window.AudioContext||window.webkitAudioContext;
 if(!output.source&&output.level<1&&Context){
  try{
   output.context=output.context||new Context();const gain=output.context.createGain();gain.gain.value=output.level;
   output.source=output.context.createMediaElementSource(output.audio);output.source.connect(gain);gain.connect(output.context.destination);output.gain=gain;
  }catch(error){
   // Once a media source is created its audio is rerouted: never reconnect it or
   // silently try an unattenuated path after a partially constructed graph.
   if(output.source){output.failed=true;throw volumeFailure()}
   applyOnlineVolume();return;
  }
 }
 applyOnlineVolume();
 if(output.context&&output.source&&output.context.state!=='running'){
  try{await output.context.resume();if(output.context.state!=='running')throw volumeFailure()}catch(_){throw volumeFailure()}
 }
}
function renderOnlineVolume(){
 const output=onlineOutput;if(!$('onlineVolume'))return;
 $('onlineVolume').value=String(output.level);if($('onlineVolumeFine'))$('onlineVolumeFine').value=String(output.level);
 if($('onlineVolumeValue'))$('onlineVolumeValue').textContent=(Math.round(output.level*1000)/10)+'%';
 if($('onlineVolumeMute'))$('onlineVolumeMute').textContent=output.audio?.muted?'取消静音':'静音';
}
async function setOnlineVolume(value){
 const level=Number(value);if(!Number.isFinite(level))return false;onlineOutput.level=Math.max(0,Math.min(1,level));
 try{localStorage.setItem('musicserver.player.volume',String(onlineOutput.level))}catch(_){}
 renderOnlineVolume();
 try{await ensureOnlineAudio();if(onlineOutput.level>0)onlineOutput.audio.muted=false;renderOnlineVolume();return true}
 catch(error){onlineOutput.audio.pause();if($('onlinePlayerStatus'))$('onlinePlayerStatus').textContent=error.message;return false}
}
function showOnlineVolume(open){if(!onlineOutput.panel)return;onlineOutput.panel.hidden=!open;$('onlineVolumeToggle').setAttribute('aria-expanded',String(open));if(open){if(typeof showOnlineLyrics==='function')showOnlineLyrics(false);if(typeof showOnlineQueue==='function')showOnlineQueue(false);renderOnlineVolume()}}
function initializeOnlineVolume(bar,audio){
 onlineOutput.audio=audio;try{applyOnlineVolume()}catch(_){} // Playback remains gated by ensureOnlineAudio.
 const toggle=makeButton('音量',()=>showOnlineVolume(onlineOutput.panel.hidden),'online-lyrics-toggle online-volume-toggle');toggle.id='onlineVolumeToggle';toggle.setAttribute('aria-expanded','false');toggle.setAttribute('aria-controls','onlineVolumePanel');$('onlineLyricsToggle').before(toggle);
 const panel=document.createElement('section');panel.id='onlineVolumePanel';panel.className='online-volume-panel';panel.hidden=true;panel.setAttribute('aria-label','播放器音量');panel.innerHTML='<div class="online-volume-heading"><strong>播放器音量</strong><output id="onlineVolumeValue"></output><button id="onlineVolumeClose" type="button" class="online-icon" aria-label="关闭音量">×</button></div><input id="onlineVolumeFine" type="range" min="0" max="1" step="0.001" aria-label="播放器音量细调"><div id="onlineVolumePresets" class="actions"></div><p>可在系统最低音量下继续降低声音。</p>';onlineOutput.panel=panel;document.body.append(panel);
 for(const level of [1,.25,.1,.05])$('onlineVolumePresets').append(makeButton((level*100)+'%',()=>setOnlineVolume(level),'btn small'));
 const mute=makeButton('静音',()=>{audio.muted=!audio.muted;renderOnlineVolume()},'btn small');mute.id='onlineVolumeMute';$('onlineVolumePresets').append(mute);
 $('onlineVolumeClose').onclick=()=>showOnlineVolume(false);$('onlineVolumeFine').oninput=()=>setOnlineVolume($('onlineVolumeFine').value);$('onlineVolume').oninput=()=>setOnlineVolume($('onlineVolume').value);
 audio.addEventListener('volumechange',renderOnlineVolume);document.addEventListener('pointerdown',event=>{if(!panel.hidden&&!panel.contains(event.target)&&!toggle.contains(event.target))showOnlineVolume(false)});document.addEventListener('keydown',event=>{if(event.key==='Escape')showOnlineVolume(false)});renderOnlineVolume();
}
