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
  const expected=state.playlists.filter(p=>!p.system).map(p=>p.id);
  try{await api('playlists/order','PUT',{order:ids,expected_order:expected});await refreshCore();setStatus('歌单顺序已保存','ok')}
  catch(error){await refreshCore().catch(()=>{});setStatus('顺序保存失败：'+error.message,'error')}
  finally{state.playlistSortBusy=false}
}
function playlistItemMenu(event,playlist,card){
  event.preventDefault();const rect=card.getBoundingClientRect();
  showPlaylistMenu({clientX:event.clientX||rect.left+32,clientY:event.clientY||rect.top+24},playlist);
}
