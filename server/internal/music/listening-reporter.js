'use strict';
let currentListening=null;
const listeningRequests=new Set();
function listeningNow(){return performance.now()}
function newListeningSession(track){finishListeningSession();if(document.documentElement.dataset.listeningEnabled!=='true'||!state.user||!track?.id)return;currentListening={id:browserUUID(),track:track.id,owner:state.user.id,epoch:typeof resumeWorkspaceEpoch==='number'?resumeWorkspaceEpoch:0,played:0,minimum:30000,started:false,counted:false,active:false,wall:0,position:0,busy:false,attempts:0,retryAt:0,ended:false};submitListening(currentListening)}
function listeningOwner(s){return s.owner===state.user?.id&&s.epoch===(typeof resumeWorkspaceEpoch==='number'?resumeWorkspaceEpoch:0)}
async function submitListening(s){
 if(!listeningOwner(s)||s.busy||s.counted||s.attempts>=3||listeningRequests.size>=4||listeningNow()<s.retryAt||s.started&&s.played<s.minimum)return;
 const controller=new AbortController(),timeout=setTimeout(()=>controller.abort(),5000);listeningRequests.add(controller);s.busy=true;
 try{const response=await fetch('/api/v2/listening/sessions',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json','X-CSRF-Token':state.csrf},body:JSON.stringify({session_id:s.id,track_id:s.track,played_ms:s.started?Math.floor(s.played):0}),signal:controller.signal});if(!listeningOwner(s))return;if(!response.ok){if([400,401,403,404,409].includes(response.status)){s.attempts=3;return}throw new Error('report')}
 const result=await response.json();if(!listeningOwner(s))return;if(!s.started){s.started=true;s.played=0;s.wall=listeningNow();s.position=memberPlayer.currentTime}s.minimum=result.minimum_ms;s.counted=Boolean(result.counted);s.attempts=0;
 }catch(_){s.attempts++;s.retryAt=listeningNow()+3000}finally{clearTimeout(timeout);s.busy=false;listeningRequests.delete(controller)}
}
function sampleListening(active){const s=currentListening;if(!s||!listeningOwner(s))return;const now=listeningNow(),position=memberPlayer.currentTime,elapsed=now-s.wall,advance=(position-s.position)*1000,rate=Math.max(.1,memberPlayer.playbackRate||1);if(s.started&&s.active&&Number.isFinite(advance)&&advance>0&&elapsed>0&&advance<=elapsed*rate+750)s.played+=Math.min(elapsed,advance/rate);s.wall=now;s.position=position;s.active=Boolean(active);submitListening(s)}
function finishListeningSession(){if(currentListening){sampleListening(false);currentListening=null}}
function clearListeningReporter(){currentListening=null;for(const controller of listeningRequests)controller.abort();listeningRequests.clear()}
function initializeListeningReporter(audio){
 audio.addEventListener('playing',()=>{if(currentListening?.ended&&onlinePlayback.track)newListeningSession(onlinePlayback.track);sampleListening(true)});audio.addEventListener('timeupdate',()=>sampleListening(!audio.paused&&!audio.ended&&!audio.seeking));
 for(const event of ['pause','waiting','stalled'])audio.addEventListener(event,()=>sampleListening(false));
 audio.addEventListener('seeking',()=>{if(currentListening){currentListening.active=false;currentListening.position=audio.currentTime;currentListening.wall=listeningNow()}});audio.addEventListener('seeked',()=>sampleListening(!audio.paused&&!audio.ended));audio.addEventListener('ended',()=>{sampleListening(false);if(currentListening)currentListening.ended=true});
}
