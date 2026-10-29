'use strict';

// The compact tagger is nonmodal; the explicit batch/upload editor remains separate.
let compactTagger=null;
function taggerCurrent(e){return compactTagger===e&&e.owner===state.user?.id&&e.generation===tagWorkspace.generation&&e.panel.isConnected}
function closeCompactTagger(){const e=compactTagger;if(!e)return;clearTimeout(e.timer);e.panel.remove();document.body.classList.remove('tagger-open');compactTagger=null}
function taggerMessage(e,text,error=false){if(!taggerCurrent(e))return;e.message.textContent=text;e.message.classList.toggle('error',error)}
function taggerKey(e){return 'musicserver.tagger.recent.'+encodeURIComponent(e.owner)+'.'+e.scope.value}
function taggerRecent(e){try{return JSON.parse(localStorage.getItem(taggerKey(e))||'[]').filter(t=>t&&typeof t.id==='string').slice(0,16)}catch(_){return []}}
function taggerRemember(e,tag){try{localStorage.setItem(taggerKey(e),JSON.stringify([tag,...taggerRecent(e).filter(t=>t.id!==tag.id)].slice(0,16)))}catch(_){}}
function taggerVersion(view,scope){return view[scope+'_version']}
function taggerHas(view,scope,id){return (view[scope]||[]).some(tag=>tag.id===id)}
async function openCompactTagger(ids){
 closeCompactTagger();
 const panel=tagNode('aside',undefined,'compact-tagger');panel.id='compactTagger';panel.setAttribute('aria-label','曲目标签器');
 const e={panel,owner:state.user.id,generation:tagWorkspace.generation,ids:[...new Set(ids)],target:0,request:0,views:new Map(),busy:false,loading:false,failed:false,items:[],active:0,undo:null};compactTagger=e;
 const head=tagNode('header');head.append(tagNode('h3','标签器'));e.pin=tagButton('固定对象',()=>{e.pinned=!e.pinned;e.pin.setAttribute('aria-pressed',String(e.pinned));e.pin.textContent=e.pinned?'已固定':'固定对象';if(!e.pinned)taggerSelectionChanged()});e.pin.id='taggerPin';e.pin.setAttribute('aria-pressed','false');head.append(e.pin,tagButton('关闭',closeCompactTagger));panel.append(head);
 e.summary=tagNode('p','','tagger-summary');e.summary.id='taggerSummary';panel.append(e.summary);
 e.scope=tagNode('select');e.scope.id='taggerScope';e.scope.setAttribute('aria-label','标签作用域');for(const [value,name]of [['public','公共标签'],['personal','个人标签']]){const option=tagNode('option',name);option.value=value;e.scope.append(option)}panel.append(e.scope);
 e.search=tagNode('input');e.search.id='taggerSearch';e.search.placeholder='搜索名称或别名，回车选择／创建';e.search.maxLength=64;e.search.setAttribute('aria-label','搜索或创建标签');panel.append(e.search);
 const columns=tagNode('div',undefined,'tagger-columns');e.groups=tagNode('nav',undefined,'tagger-groups');e.groups.setAttribute('aria-label','标签分组');e.results=tagNode('div',undefined,'tagger-results');e.results.id='taggerResults';columns.append(e.groups,e.results);panel.append(columns);
 e.group=tagWorkspace.groups[0]?.id;for(const group of tagWorkspace.groups){const button=tagButton(group.name,()=>{if(e.busy)return;e.group=group.id;taggerRenderGroups(e);void taggerSearch(e)});button.dataset.group=group.id;e.groups.append(button)}taggerRenderGroups(e);
 e.message=tagNode('p','','tagger-message');e.message.id='taggerMessage';e.message.setAttribute('role','status');panel.append(e.message);
 e.undoButton=tagButton('撤销上次操作',()=>taggerUndo(e));e.undoButton.id='taggerUndo';e.undoButton.hidden=true;panel.append(e.undoButton);
 panel.append(tagNode('small','↑↓ 选择 · Enter 添加／创建 · Esc 关闭','tagger-shortcuts'));
 e.scope.onchange=()=>{e.undo=null;e.undoButton.hidden=true;void taggerLoadTargets(e)};
 e.search.oninput=()=>{clearTimeout(e.timer);e.request++;e.loading=true;e.timer=setTimeout(()=>taggerSearch(e),180)};
 e.search.onkeydown=event=>{
  if(event.isComposing||event.keyCode===229)return;
  if(['ArrowDown','ArrowUp'].includes(event.key)){event.preventDefault();const count=e.options?.length||0;if(count){e.active=(e.active+(event.key==='ArrowDown'?1:-1)+count)%count;taggerHighlight(e)}}
  if(event.key==='Enter'){event.preventDefault();if(!e.busy&&!e.loading&&!e.failed)e.options?.[e.active]?.click()}
 };
 document.body.append(panel);document.body.classList.add('tagger-open');await taggerLoadTargets(e);if(taggerCurrent(e))e.search.focus();return true;
}
function taggerRenderGroups(e){for(const button of e.groups.children)button.setAttribute('aria-current',String(button.dataset.group===e.group))}
async function taggerLoadTargets(e){
 const target=++e.target,scope=e.scope.value,ids=[...e.ids];e.views.clear();e.ready=false;e.loading=true;e.summary.textContent=ids.length+' 首曲目'+(e.pinned?' · 已固定':' · 跟随选择');
 if(ids.length>32){e.results.replaceChildren(tagNode('p','所选超过 32 首，请使用批量任务。'),tagButton('打开批量整理',()=>{const targets=[...e.ids];closeCompactTagger();openTagComposer({ids:targets,range:'selected'})}));e.loading=false;return}
 if(!ids.length){e.results.textContent='请在左侧选择曲目。';e.loading=false;return}
 try{const views=await Promise.all(ids.map(id=>api('tracks/'+encodeURIComponent(id)+'/tags')));if(!taggerCurrent(e)||target!==e.target||scope!==e.scope.value)return;e.views=new Map(ids.map((id,i)=>[id,views[i]]));e.ready=true;await taggerSearch(e)}catch(error){if(taggerCurrent(e)&&target===e.target){e.loading=false;e.failed=true;e.results.replaceChildren(tagNode('p','曲目标签读取失败'),tagButton('重试',()=>taggerLoadTargets(e)));taggerMessage(e,error.message,true)}}
}
function taggerSelectionChanged(){const e=compactTagger;if(!e)return;if(!taggerCurrent(e)){closeCompactTagger();return}if(e.pinned)return;const ids=[...(state.selectedTrackIds||[])];if(ids.join('\0')===e.ids.join('\0'))return;if(e.busy){e.selectionPending=true;e.pendingIDs=ids;return}e.ids=ids;void taggerLoadTargets(e)}
function taggerHighlight(e){e.options.forEach((option,i)=>{option.classList.toggle('tagger-highlight',i===e.active);option.setAttribute('aria-selected',String(i===e.active))});e.options[e.active]?.scrollIntoView({block:'nearest'})}
function taggerNormalize(value){return value.normalize('NFC').trim().replace(/\s+/g,' ').toLocaleLowerCase()}
async function taggerSearch(e){
 if(!taggerCurrent(e)||!e.ready||e.busy)return;
 const request=++e.request,target=e.target,scope=e.scope.value,group=e.group,q=e.search.value.trim();e.loading=true;e.failed=false;e.results.textContent='正在搜索…';
 try{const page=await api('tags?'+new URLSearchParams({scope,group_id:group,q,limit:'50'}));if(!taggerCurrent(e)||request!==e.request||target!==e.target)return;
  e.loading=false;e.items=page.items||[];e.results.replaceChildren();e.options=[];e.active=0;
  const bound=[...e.views.values()].flatMap(v=>v[scope]||[]).filter(t=>t.group_id===group);
  const rows=q?e.items:[...new Map([...taggerRecent(e).filter(t=>t.group_id===group&&e.items.some(x=>x.id===t.id)),...bound,...e.items].map(t=>[t.id,t])).values()];
  for(const tag of rows){const row=tagNode('div',undefined,'tagger-row');const count=[...e.views.values()].filter(v=>taggerHas(v,scope,tag.id)).length;const all=count===e.ids.length;
   const select=tagButton(tag.name,()=>taggerApply(e,tag,false));select.dataset.taggerTag=tag.id;select.disabled=all;select.title=all?'已添加到全部所选曲目':'添加到全部所选曲目';select.append(tagNode('small',all?'全部已有':count?'部分已有':'添加'));
   const alias=(tag.aliases||[]).find(a=>q&&taggerNormalize(a).includes(taggerNormalize(q)));if(alias)select.append(tagNode('small','别名：'+alias));row.append(select);if(!all)e.options.push(select);
   if(count){const remove=tagButton('移除',()=>taggerApply(e,tag,true));remove.dataset.taggerRemove=tag.id;remove.setAttribute('aria-label','从所选曲目移除 '+tag.name);row.append(remove)}
   if(typeof openTagDictionary==='function'&&(scope==='personal'||state.user.role==='admin')){const edit=tagButton('编辑',()=>{if(!taggerCurrent(e)||e.busy||e.loading)return;void openTagDictionary({tag})});edit.dataset.taggerEdit=tag.id;edit.setAttribute('aria-label','编辑词条 '+tag.name+' 与别名');row.append(edit)}
   e.results.append(row);
  }
  const exact=e.items.some(tag=>[tag.name,...tag.aliases||[]].some(n=>taggerNormalize(n)===taggerNormalize(q)));
  if(q&&!exact){const create=tagButton('＋ 创建「'+q+'」到当前分组',()=>taggerCreate(e,q));create.id='taggerCreate';e.results.append(create);e.options.push(create)}
  if(rows.length&&e.options.length===1&&e.options[0].id==='taggerCreate')e.active=-1;
  if(!rows.length&&!q)e.results.append(tagNode('p','此分组暂无标签，输入名称后回车创建。'));
  if(page.total>e.items.length)e.results.append(tagNode('small','仅显示前 50 项，请输入名称缩小范围。'));
  taggerHighlight(e);
 }catch(error){if(taggerCurrent(e)&&request===e.request){e.loading=false;e.failed=true;e.results.replaceChildren(tagNode('p','搜索失败，暂不允许创建'),tagButton('重试',()=>taggerSearch(e)));taggerMessage(e,error.message,true)}}
}
async function taggerCreate(e,name){
 if(!taggerCurrent(e)||e.loading||e.failed||e.busy)return;
 const scope=e.scope.value,group=e.group,target=e.target;e.busy=true;taggerBusy(e);
 let tag;try{try{tag=await api('tags','POST',{scope,group_id:group,name})}catch(error){if(error.code!=='TAG_NAME_EXISTS')throw error;const page=await api('tags?'+new URLSearchParams({scope,group_id:group,q:name,limit:'50'}));tag=page.items.find(t=>[t.name,...t.aliases||[]].some(n=>taggerNormalize(n)===taggerNormalize(name)));if(!tag)throw error}
  if(!taggerCurrent(e)||target!==e.target)return;
 }catch(error){taggerMessage(e,'创建失败：'+error.message,true)}finally{e.busy=false;if(taggerCurrent(e)){taggerBusy(e);if(!tag){if(e.selectionPending){e.selectionPending=false;e.ids=e.pendingIDs;await taggerLoadTargets(e)}else await taggerSearch(e)}}}
 if(tag&&taggerCurrent(e)&&target===e.target)await taggerApply(e,tag,false);
}
function taggerBusy(e){e.scope.disabled=e.busy;e.search.disabled=e.busy;e.pin.disabled=e.busy;for(const b of e.groups.querySelectorAll('button'))b.disabled=e.busy;if(e.busy)for(const b of e.results.querySelectorAll('button'))b.disabled=true;e.undoButton.disabled=e.busy}
async function taggerApply(e,tag,remove){
 if(!taggerCurrent(e)||e.busy||e.loading||e.failed||!e.ready)return;
 const ids=[...e.ids],scope=e.scope.value,target=e.target;
 const pending=ids.filter(id=>taggerHas(e.views.get(id),scope,tag.id)===remove).map(id=>({id,version:taggerVersion(e.views.get(id),scope)}));
 if(!pending.length){if(e.selectionPending){e.selectionPending=false;e.ids=e.pendingIDs;await taggerLoadTargets(e)}else await taggerSearch(e);return}
 e.busy=true;taggerBusy(e);e.undo=null;e.undoButton.hidden=true;const changed=[];let failure;
 try{for(const item of pending){if(!taggerCurrent(e))break;const view=await api('tracks/'+encodeURIComponent(item.id)+'/tags','PATCH',{scope,add_ids:remove?[]:[tag.id],remove_ids:remove?[tag.id]:[]},undefined,item.version);changed.push({id:item.id,version:taggerVersion(view,scope)});if(taggerCurrent(e)&&e.target===target)e.views.set(item.id,view)}}catch(error){failure=error}
 finally{e.busy=false;if(taggerCurrent(e)){e.undo={tag,remove,scope,changed};e.undoButton.hidden=!changed.length;taggerRemember(e,tag);taggerMessage(e,(failure?'部分操作失败：'+failure.message+'；':'')+'已'+(remove?'移除':'添加')+' '+changed.length+' 首'+(changed.length?'，可撤销。':'。'),Boolean(failure));if(e.selectionPending){e.selectionPending=false;e.ids=e.pendingIDs;await taggerLoadTargets(e)}else if(!failure)await taggerSearch(e);else{e.ready=false;e.results.replaceChildren(tagButton('重新读取曲目标签',()=>taggerLoadTargets(e)))}taggerBusy(e);document.dispatchEvent(new CustomEvent('tagger-saved'))}}
}
async function taggerUndo(e){
 const undo=e.undo;if(!taggerCurrent(e)||e.busy||!undo)return;e.busy=true;taggerBusy(e);let done=0;
 try{for(const item of undo.changed){const view=await api('tracks/'+encodeURIComponent(item.id)+'/tags');if(!taggerCurrent(e))return;if(taggerVersion(view,undo.scope)!==item.version)throw new Error('标签已被后续修改，拒绝覆盖，请重新整理');}
  for(const item of undo.changed){if(!taggerCurrent(e))return;await api('tracks/'+encodeURIComponent(item.id)+'/tags','PATCH',{scope:undo.scope,add_ids:undo.remove?[undo.tag.id]:[],remove_ids:undo.remove?[]:[undo.tag.id]},undefined,item.version);done++}
  e.undo=null;e.undoButton.hidden=true;taggerMessage(e,'已撤销 '+done+' 首。');
 }catch(error){undo.changed=undo.changed.slice(done);e.undoButton.hidden=!undo.changed.length;taggerMessage(e,'已撤销 '+done+' 首；'+error.message,true)}finally{e.busy=false;if(taggerCurrent(e)){if(e.selectionPending){e.selectionPending=false;e.ids=e.pendingIDs}await taggerLoadTargets(e);taggerBusy(e);document.dispatchEvent(new CustomEvent('tagger-saved'))}}
}
document.addEventListener('pointerdown',event=>{const e=compactTagger;if(document.querySelector('dialog[open]')||event.button!==0||!e||e.pinned||e.busy||e.panel.contains(event.target))return;if(event.target.closest('#trackRows tr,.track-toolbar,.browse-pager,#playlistDetail [data-track-id],#contextMenu,#contextSubmenu'))return;closeCompactTagger()});
document.addEventListener('keydown',event=>{if(event.key==='Escape'&&!event.isComposing&&compactTagger&&!document.querySelector('dialog[open]')){event.preventDefault();closeCompactTagger()}});
window.addEventListener('tag-dictionary-changed',()=>{const e=compactTagger;if(e&&taggerCurrent(e)&&!e.busy)void taggerLoadTargets(e)});
