'use strict';

// Only collections of item checkboxes participate; account/security toggles do
// not become selection groups merely because they also use checkbox inputs.
const selectionGroupSelector='#trackRows,.playlist-tracks,.key-grant-grid,.scope-grid,#userResources,[data-selection-group]';
const selectionSummaries=new Map();let selectionGesture=null,selectionSuppressedClick=null,selectionRefreshFrame=0;
function selectionIdentity(){return typeof state==='undefined'?null:(state.user?.id||'')+'/'+(typeof resumeWorkspaceEpoch==='undefined'?0:resumeWorkspaceEpoch)}
function selectionGroup(input){return input?.closest?.(selectionGroupSelector)}
function selectionInputs(group){return [...group.querySelectorAll('input[type="checkbox"]')].filter(input=>selectionGroup(input)===group&&input.id!=='selectAll'&&!input.hasAttribute('data-selection-all')&&!input.disabled&&!input.closest('[hidden]'))}
function selectionSummary(group){
 const selector=group.dataset.selectionSummary;
 if(selector){try{return document.querySelector(selector)||document.getElementById(selector)}catch(_){return document.getElementById(selector)}}
 if(group.id==='trackRows')return document.getElementById('trackSelectionStatus');
 if(group.classList.contains('playlist-tracks')){const output=group.closest('section')?.querySelector('[data-playlist-selection-status]');if(output)return output}
 let output=selectionSummaries.get(group);
 if(!output?.isConnected){output=document.createElement('p');output.className='status selection-summary';output.dataset.selectionGenerated='';output.setAttribute('role','status');group.after(output);selectionSummaries.set(group,output)}
 return output;
}
function refreshSelectionUI(){
 selectionRefreshFrame=0;
 if(selectionGesture&&(selectionGesture.owner!==selectionIdentity()||!selectionGesture.group.isConnected||!selectionGesture.input.isConnected))endSelectionGesture(false);
 for(const[group,output]of selectionSummaries){if(!group.isConnected){output.remove();selectionSummaries.delete(group)}}
 for(const group of document.querySelectorAll(selectionGroupSelector)){
  const inputs=selectionInputs(group);if(!inputs.length&&!group.matches('#trackRows,.playlist-tracks,[data-selection-group]')){const previous=selectionSummaries.get(group);if(previous){previous.remove();selectionSummaries.delete(group)}continue}
  const output=selectionSummary(group);if(!output)continue;
  const text='已选 '+inputs.filter(input=>input.checked).length+' / 当前页 '+inputs.length+' 项';
  if(output.textContent!==text)output.textContent=text;
  group.dataset.selectionCount=String(inputs.filter(input=>input.checked).length);
 }
}
function scheduleSelectionRefresh(){if(!selectionRefreshFrame)selectionRefreshFrame=requestAnimationFrame(refreshSelectionUI)}
function selectionStartInput(target){
 if(!(target instanceof Element))return null;
 const control=target.closest('input,button,a,textarea,select');
 if(control&&!control.matches('input[type="checkbox"]'))return null;
 const input=control||target.closest('label,td,[data-selection-hit]')?.querySelector('input[type="checkbox"]');
 return input?.matches('input[type="checkbox"]')&&!input.disabled&&input.id!=='selectAll'&&!input.hasAttribute('data-selection-all')&&selectionGroup(input)?input:null;
}
function selectionHit(input){return input.closest('tr,.playlist-track,.resource-item,label,[data-selection-item]')||input}
function selectionSegmentHits(a,b,r){
 if(!r.width||!r.height)return false;
 let low=0,high=1;const dx=b.x-a.x,dy=b.y-a.y;
 for(const[p,q]of [[-dx,a.x-r.left],[dx,r.right-a.x],[-dy,a.y-r.top],[dy,r.bottom-a.y]]){
  if(p===0){if(q<0)return false;continue}const t=q/p;if(p<0)low=Math.max(low,t);else high=Math.min(high,t);if(low>high)return false;
 }
 return true;
}
function selectionApply(input,gesture){
 if(gesture.visited.has(input)||!input.isConnected||input.disabled||selectionGroup(input)!==gesture.group)return;
 gesture.visited.add(input);
 if(input.checked!==gesture.checked){input.checked=gesture.checked;input.dispatchEvent(new Event('change',{bubbles:true}))}
}
function selectionMove(point){
 const gesture=selectionGesture;if(!gesture)return;
 if(gesture.owner!==selectionIdentity()||!gesture.group.isConnected||!gesture.input.isConnected){endSelectionGesture(false);return}
 if(!gesture.dragging&&Math.hypot(point.x-gesture.start.x,point.y-gesture.start.y)<6)return;
 if(!gesture.dragging){gesture.dragging=true;document.body.classList.add('selection-painting');selectionApply(gesture.input,gesture)}
 // Test the entire segment, rather than just the element under the last
 // pointer event: fast diagonal movements must not skip intervening rows.
 for(const input of selectionInputs(gesture.group)){if(selectionSegmentHits(gesture.last,point,selectionHit(input).getBoundingClientRect()))selectionApply(input,gesture)}
 gesture.last=point;scheduleSelectionRefresh();
}
function endSelectionGesture(suppress){
 const gesture=selectionGesture;selectionGesture=null;document.body.classList.remove('selection-painting');
 if(gesture?.dragging&&suppress)selectionSuppressedClick={group:gesture.group,until:performance.now()+400};
 scheduleSelectionRefresh();
}
document.addEventListener('pointerdown',event=>{
 selectionSuppressedClick=null;
 if(event.button!==0||event.pointerType!=='mouse'){if(selectionGesture)endSelectionGesture(false);return}
 const input=selectionStartInput(event.target);if(!input)return;
 const point={x:event.clientX,y:event.clientY};selectionGesture={owner:selectionIdentity(),input,group:selectionGroup(input),checked:!input.checked,start:point,last:point,point,pointerId:event.pointerId,dragging:false,visited:new Set()};
},true);
document.addEventListener('pointermove',event=>{
 if(!selectionGesture||event.pointerId!==selectionGesture.pointerId)return;
 if(!(event.buttons&1)){endSelectionGesture(false);return}
 selectionGesture.point={x:event.clientX,y:event.clientY};selectionMove(selectionGesture.point);
 if(selectionGesture?.dragging){event.preventDefault();event.stopPropagation()}
},true);
document.addEventListener('pointerup',event=>{if(selectionGesture&&event.pointerId===selectionGesture.pointerId)endSelectionGesture(true)},true);
document.addEventListener('pointercancel',()=>endSelectionGesture(false),true);
document.addEventListener('contextmenu',()=>endSelectionGesture(false),true);
document.addEventListener('keydown',event=>{if(event.key==='Escape')endSelectionGesture(true)},true);
window.addEventListener('blur',()=>endSelectionGesture(false));
window.addEventListener('hashchange',()=>endSelectionGesture(false));
document.addEventListener('scroll',()=>{if(selectionGesture?.dragging)selectionMove(selectionGesture.point)},true);
document.addEventListener('click',event=>{
 if(event.detail&&selectionSuppressedClick&&performance.now()<selectionSuppressedClick.until&&selectionSuppressedClick.group.contains(event.target)){event.preventDefault();event.stopImmediatePropagation();selectionSuppressedClick=null}
},true);
document.addEventListener('change',scheduleSelectionRefresh,true);
new MutationObserver(scheduleSelectionRefresh).observe(document.body,{subtree:true,childList:true,attributes:true,attributeFilter:['disabled','checked','hidden']});
scheduleSelectionRefresh();
