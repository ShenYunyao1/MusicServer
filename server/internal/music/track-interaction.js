'use strict';

let pendingTrackClick=null;
function cancelTrackClick(){if(pendingTrackClick){clearTimeout(pendingTrackClick.timer);pendingTrackClick=null}}
function bindTrackRowInteraction(row,{title,detail,play,toggle}){
 const isTitle=target=>title===target||title?.contains(target);
 const isBlank=target=>!target.closest('button,a,input,label,select,textarea,summary,[contenteditable="true"],.playlist-sequence,.track-cover');
 row.addEventListener('click',event=>{
  if(event.button!==0||(!isTitle(event.target)&&!isBlank(event.target)))return;
  event.preventDefault();cancelTrackClick();
  if(event.detail>=2)return;
  if(event.detail===0||event.pointerType==='touch'){detail();return}
  const owner=state.user?.id,hash=location.hash;
  pendingTrackClick={row,timer:setTimeout(()=>{pendingTrackClick=null;if(row.isConnected&&state.user?.id===owner&&location.hash===hash)detail()},400)};
 });
 row.addEventListener('dblclick',event=>{
  if(event.button!==0)return;
  if(isTitle(event.target)){event.preventDefault();cancelTrackClick();play()}
  else if(isBlank(event.target)){event.preventDefault();cancelTrackClick();toggle()}
 });
 row.addEventListener('keydown',event=>{if(event.defaultPrevented||event.target!==row)return;if(event.key==='Enter'){event.preventDefault();cancelTrackClick();detail()}else if(event.key===' '){event.preventDefault();cancelTrackClick();toggle()}});
}
document.addEventListener('pointerdown',event=>{if(pendingTrackClick&&(event.button!==0||!pendingTrackClick.row.contains(event.target)))cancelTrackClick()},true);
document.addEventListener('contextmenu',cancelTrackClick,true);
document.addEventListener('scroll',cancelTrackClick,true);
window.addEventListener('hashchange',()=>{cancelTrackClick();state.playlistTrackSelection=null});
window.addEventListener('blur',cancelTrackClick);

function libraryContextIDs(track){return state.selectedTrackIds.has(track.id)?[...state.selectedTrackIds]:[track.id]}
function playlistTrackSelection(list){return [...list.querySelectorAll('[data-playlist-select]:checked')].map(input=>input.dataset.playlistSelect)}
function playlistSelectionFor(p,tracks){const key=[state.user?.id,p.id,p.browse_page?.offset||0,state.playlistTrackQuery||''].join('/');if(state.playlistTrackSelection?.key!==key)state.playlistTrackSelection={key,ids:new Set()};const visible=new Set(tracks.map(t=>t.id));state.playlistTrackSelection.ids=new Set([...state.playlistTrackSelection.ids].filter(id=>visible.has(id)));return state.playlistTrackSelection.ids}
