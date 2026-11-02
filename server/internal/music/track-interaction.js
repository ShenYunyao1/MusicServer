'use strict';

let trackSelectionPress=null,trackSelectionClick=null;
function clearTrackSelectionPress(){trackSelectionPress=null;trackSelectionClick=null}
function trackSelectionHit(target,x,y){
 const row=target.closest?.('[data-library-track],.playlist-track');if(!row)return null;
 const input=row.querySelector('[data-track-id],[data-playlist-select]'),cover=row.querySelector('.track-cover'),rect=row.getBoundingClientRect();
 return input&&!input.disabled&&cover&&x>=rect.left&&x<cover.getBoundingClientRect().left&&y>=rect.top&&y<=rect.bottom?input:null;
}
function bindTrackRowInteraction(row,{title,detail,toggle}){
 const isTitle=target=>title===target||title?.contains(target);
 row.addEventListener('pointerdown',event=>{if(event.button===0&&trackSelectionHit(event.target,event.clientX,event.clientY)){if(event.pointerType==='mouse'&&!event.target.matches('input'))event.preventDefault();event.stopPropagation()}},true);
 row.addEventListener('touchstart',event=>{const touch=event.touches[0];if(touch&&trackSelectionHit(event.target,touch.clientX,touch.clientY))event.stopPropagation()},{capture:true,passive:true});
 row.addEventListener('click',event=>{if(event.button!==0||!trackSelectionHit(event.target,event.clientX,event.clientY))return;if(event.target.matches('input[type="checkbox"]'))return;event.preventDefault();event.stopImmediatePropagation();toggle()},true);
 row.addEventListener('click',event=>{
  if(event.button!==0||!isTitle(event.target)||event.detail>=2)return;
  event.preventDefault();detail();
 });
 row.addEventListener('keydown',event=>{if(event.defaultPrevented||event.target!==row)return;if(event.key==='Enter'){event.preventDefault();detail()}else if(event.key===' '){event.preventDefault();toggle()}});
}
document.addEventListener('pointerdown',event=>{clearTrackSelectionPress();if(event.button!==0)return;const input=trackSelectionHit(event.target,event.clientX,event.clientY);if(input)trackSelectionPress={input,row:input.closest('[data-library-track],.playlist-track'),checked:input.checked,owner:state.user?.id,hash:location.hash,x:event.clientX,y:event.clientY,pointer:event.pointerId}},true);
document.addEventListener('pointermove',event=>{if(trackSelectionPress&&event.pointerId===trackSelectionPress.pointer&&Math.hypot(event.clientX-trackSelectionPress.x,event.clientY-trackSelectionPress.y)>=6)clearTrackSelectionPress()},true);
document.addEventListener('pointerup',event=>{const press=trackSelectionPress;trackSelectionPress=null;if(press&&event.pointerId===press.pointer&&Math.hypot(event.clientX-press.x,event.clientY-press.y)<6)trackSelectionClick={...press,expires:performance.now()+500}},true);
document.addEventListener('click',event=>{const press=trackSelectionClick;trackSelectionClick=null;if(!press||event.button!==0||!event.detail||performance.now()>press.expires||press.owner!==state.user?.id||press.hash!==location.hash||!press.input.isConnected||!press.row.contains(event.target))return;if(event.target===press.input)return;event.preventDefault();event.stopImmediatePropagation();press.input.checked=!press.checked;press.input.dispatchEvent(new Event('change',{bubbles:true}))},true);
document.addEventListener('pointercancel',clearTrackSelectionPress,true);
document.addEventListener('contextmenu',clearTrackSelectionPress,true);
document.addEventListener('scroll',clearTrackSelectionPress,true);
window.addEventListener('hashchange',()=>{clearTrackSelectionPress();state.playlistTrackSelection=null});
window.addEventListener('blur',clearTrackSelectionPress);

function libraryContextIDs(track){return state.selectedTrackIds.has(track.id)?[...state.selectedTrackIds]:[track.id]}
function playlistTrackSelection(list){return [...list.querySelectorAll('[data-playlist-select]:checked')].map(input=>input.dataset.playlistSelect)}
function playlistSelectionFor(p,tracks){const key=[state.user?.id,p.id,p.browse_page?.offset||0,state.playlistTrackQuery||''].join('/');if(state.playlistTrackSelection?.key!==key)state.playlistTrackSelection={key,ids:new Set()};const visible=new Set(tracks.map(t=>t.id));state.playlistTrackSelection.ids=new Set([...state.playlistTrackSelection.ids].filter(id=>visible.has(id)));return state.playlistTrackSelection.ids}
