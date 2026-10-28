'use strict';

// All drafts and capability caches belong to the current signed-in workspace.
const tagWorkspace={identity:null,generation:0,groups:null,promise:null,dialog:null,editor:null,lastJob:null};
function clearTagWorkspace(){
  tagWorkspace.generation++;
  clearTimeout(tagWorkspace.editor?.timer);
  tagWorkspace.dialog?.remove();
  Object.assign(tagWorkspace,{identity:null,groups:null,promise:null,dialog:null,editor:null,lastJob:null});
  document.querySelectorAll('[data-tag-feature]').forEach(element=>element.remove());
  if(typeof state!=='undefined')delete state.tagUploadDefaults;
}
function tagFeatureEnabled(){return Boolean(state.user&&tagWorkspace.identity===state.user.id&&Array.isArray(tagWorkspace.groups))}
async function loadTagCapability(){
  if(!state.user)return false;
  if(tagWorkspace.identity!==state.user.id){clearTagWorkspace();tagWorkspace.identity=state.user.id}
  if(tagWorkspace.groups!==null)return tagFeatureEnabled();
  if(tagWorkspace.promise)return tagWorkspace.promise;
  const generation=tagWorkspace.generation;
  const pending=(async()=>{
    try{
      const groups=await api('tag-groups');
      if(generation!==tagWorkspace.generation)return false;
      tagWorkspace.groups=Array.isArray(groups)?groups:[];
      try{
        const recent=await api('tag-batches');
        const items=Array.isArray(recent)?recent:recent.items||[];
        if(generation===tagWorkspace.generation&&!tagWorkspace.lastJob&&items.length)tagWorkspace.lastJob=items[0];
      }catch(_){} // A history lookup must not block dictionary editing.
      return generation===tagWorkspace.generation&&tagFeatureEnabled();
    }catch(error){
      if(generation!==tagWorkspace.generation)return false;
      if(error.code==='TAGS_UNAVAILABLE')tagWorkspace.groups=false;
      return false;
    }finally{if(generation===tagWorkspace.generation)tagWorkspace.promise=null}
  })();
  tagWorkspace.promise=pending;
  return pending;
}
function tagNode(tag,text,className){const node=document.createElement(tag);if(text!==undefined)node.textContent=text;if(className)node.className=className;return node}
function tagButton(text,action,className='btn'){const node=tagNode('button',text,className);node.type='button';node.onclick=action;return node}
function tagScopeLabel(scope){return scope==='public'?'公共标签':'个人标签'}
function tagPendingUploads(){return state.uploads.filter(upload=>!['完成','已去重'].includes(upload.status))}
function tagEditorCurrent(editor){return tagWorkspace.editor===editor&&editor.generation===tagWorkspace.generation&&tagFeatureEnabled()}
function tagEditorMessage(editor,text,error=false){if(!tagEditorCurrent(editor))return;editor.message.textContent=text;editor.message.className='tag-editor-message '+(error?'error':'');editor.message.setAttribute('role',error?'alert':'status')}
function tagSetBusy(editor,busy){
  editor.busy=busy;
  const blocked=busy||editor.locked;
  editor.dictionary.querySelectorAll('select').forEach(control=>control.disabled=blocked);
  editor.create.querySelectorAll('button,input').forEach(control=>control.disabled=blocked);
  editor.scopeInput.disabled=busy||editor.job?.state==='running';
  if(editor.rangeInput)editor.rangeInput.disabled=editor.scopeInput.disabled;
  editor.reload.disabled=editor.scopeInput.disabled;
  editor.save.disabled=blocked;
}

async function decorateTrackTagDetails(track){
  const container=$('trackDetailsBody'),dialog=$('trackDetails');
  if(!container)return;
  const capability=loadTagCapability(),workspaceGeneration=tagWorkspace.generation,request=String((Number(container.dataset.tagRequest)||0)+1);container.dataset.tagRequest=request;
  const marker=track.id;container.dataset.tagTrack=marker;
  if(!await capability||workspaceGeneration!==tagWorkspace.generation||container.dataset.tagTrack!==marker||container.dataset.tagRequest!==request)return;
  if(!container||!dialog?.open)return;
  container.querySelector('[data-tag-feature]')?.remove();
  const section=tagNode('section',undefined,'tag-detail-panel');section.dataset.tagFeature='details';
  const heading=tagNode('div',undefined,'actions');heading.append(tagNode('strong','曲目标签'),tagButton('管理标签',()=>{dialog.close();openTrackTagEditor([track.id])}));
  section.append(heading);container.append(section);
  const generation=tagWorkspace.generation;
  const importSource=tagNode('div');importSource.dataset.tagImportSource='1';section.append(importSource);
  loadTrackTagImportSource(track.id,importSource,generation);
  try{
    const view=await api('tracks/'+encodeURIComponent(track.id)+'/tags');
    if(generation!==tagWorkspace.generation||!section.isConnected||container.dataset.tagTrack!==marker||container.dataset.tagRequest!==request)return;
    for(const scope of ['public','personal']){
      const line=tagNode('p');line.append(tagNode('span',tagScopeLabel(scope)+'：','status'));
      const tags=view[scope]||[];
      if(!tags.length)line.append(document.createTextNode('暂无'));
      for(const tag of tags){
        if(typeof applyTrackTagFilter==='function')line.append(tagButton(tag.name,()=>{dialog.close();applyTrackTagFilter(tag)},'tag-chip'));
        else line.append(tagNode('span',tag.name,'tag-chip'));
      }
      section.append(line);
    }
  }catch(error){if(section.isConnected)section.append(tagNode('p','标签读取失败：'+error.message,'status error'))}
}
async function loadTrackTagImportSource(id,container,generation){
  container.replaceChildren(tagNode('p','正在读取文件风格来源…','status'));
  try{
    const source=await api('tracks/'+encodeURIComponent(id)+'/tag-import-source');
    if(generation!==tagWorkspace.generation||!container.isConnected)return;
    container.replaceChildren();
    if(source.unmapped?.length){
      container.append(tagNode('strong','待归一的文件风格','status'));
      const values=tagNode('p');for(const value of source.unmapped)values.append(tagNode('span',value,'tag-chip'));container.append(values);
    }
    if(source.truncated)container.append(tagNode('p','文件风格超出导入边界，部分原值未参与整理，请检查原文件并由管理员确认。','notice'));
    if(!source.unmapped?.length&&!source.truncated&&source.values?.length)container.append(tagNode('p','文件风格原值均已识别。','status'));
  }catch(error){
    if(generation!==tagWorkspace.generation||!container.isConnected)return;
    container.replaceChildren(tagNode('p','文件风格来源读取失败：'+error.message,'status error'),tagButton('重试读取风格来源',()=>loadTrackTagImportSource(id,container,generation)));
  }
}
function trackTagMenuItem(ids){return tagFeatureEnabled()?{label:'管理标签',action:()=>openTrackTagEditor(ids)}:null}
function tagTaskMenuItem(){return tagFeatureEnabled()&&tagWorkspace.lastJob?{label:'最近标签任务',action:()=>openTagComposer({ids:[],range:'selected',resumeJob:tagWorkspace.lastJob})}:null}

async function openTrackTagEditor(ids,options={}){
  if(!await loadTagCapability())return false;
  return openTagComposer({ids:[...new Set(ids)],range:options.range||'selected'});
}
async function openUploadTagEditor(upload){
  if(!await loadTagCapability())return false;
  const uploads=upload?[upload]:tagPendingUploads();
  if(!uploads.length)return false;
  return openTagComposer({uploads,allUploads:!upload});
}
async function openTagComposer(config){
  clearTimeout(tagWorkspace.editor?.timer);tagWorkspace.dialog?.remove();
  const editor={...config,generation:tagWorkspace.generation,scope:'personal',add:new Set(),remove:new Set(),common:new Set(),partial:new Set(),query:'',offset:0,request:0,busy:false,job:null,confirmed:false};
  const dialog=tagNode('dialog',undefined,'modal tag-editor');dialog.id='tagEditorDialog';tagWorkspace.dialog=dialog;tagWorkspace.editor=editor;
  const head=tagNode('div',undefined,'modal-head');head.append(tagNode('h3',config.uploads?(config.allUploads?'本批上传默认标签':'上传标签草稿'):'管理曲目标签'),tagButton('关闭',()=>dialog.close()));dialog.append(head);
  const body=tagNode('div',undefined,'modal-body');dialog.append(body);
  const intro=tagNode('p',config.uploads?'先保存为上传草稿，确认入库后才应用。公共词条须由管理员维护。':'先选择范围并预览，再明确确认执行。个人标签只对自己可见。','notice');body.append(intro);
  const controls=tagNode('div',undefined,'tag-editor-controls');body.append(controls);
  const scope=tagNode('select');scope.id='tagScope';scope.setAttribute('aria-label','标签作用域');
  for(const [value,label] of [['personal','个人标签'],['public','公共标签']]){const option=tagNode('option',label);option.value=value;scope.append(option)}
  if(config.resumeJob?.scope)scope.value=config.resumeJob.scope;
  const wrapControl=(label,control)=>{const field=tagNode('label',undefined,'field');field.append(tagNode('span',label),control);controls.append(field)};
  wrapControl('作用域',scope);editor.scopeInput=scope;
  if(!config.uploads){
    const range=tagNode('select');range.id='tagRange';range.setAttribute('aria-label','选择曲目范围');
    for(const [value,label] of [['selected','已选曲目（'+config.ids.length+' 首）'],['page','当前页'],['all','全部匹配结果']]){const option=tagNode('option',label);option.value=value;range.append(option)}
    range.value=config.range;wrapControl('选择范围',range);editor.rangeInput=range;
  }
  const refresh=tagButton(config.uploads?'切换作用域':'重新加载范围',()=>tagLoadSelection(editor,true));refresh.id='tagEditorReload';controls.append(refresh);editor.reload=refresh;
  const summary=tagNode('p','','status');summary.id='tagEditorSummary';body.append(summary);editor.summary=summary;
  const filters=tagNode('div',undefined,'tag-editor-controls'),group=tagNode('select');group.id='tagGroup';group.setAttribute('aria-label','标签分组');
  for(const item of tagWorkspace.groups){const option=tagNode('option',item.name);option.value=item.id;group.append(option)}
  const search=tagNode('input');search.id='tagSearch';search.placeholder='搜索标签名称或别名';search.maxLength=64;search.setAttribute('aria-label','搜索标签');filters.append(group,search);body.append(filters);editor.group=group;editor.search=search;
  const dictionary=tagNode('div',undefined,'tag-dictionary');dictionary.id='tagDictionary';body.append(dictionary);editor.dictionary=dictionary;
  const pager=tagNode('div',undefined,'tag-editor-pager');editor.previous=tagButton('上一页',()=>{editor.offset=Math.max(0,editor.offset-50);tagLoadDictionary(editor)});editor.next=tagButton('下一页',()=>{editor.offset+=50;tagLoadDictionary(editor)});editor.pageText=tagNode('span','','status');pager.append(editor.previous,editor.pageText,editor.next);body.append(pager);
  const create=tagNode('div',undefined,'tag-create');editor.create=create;const name=tagNode('input');name.maxLength=64;name.placeholder='新个人标签名称';name.setAttribute('aria-label','新个人标签名称');const createButton=tagButton('创建个人标签',async()=>{
    if(editor.scope!=='personal'||!name.value.trim())return;
    tagSetBusy(editor,true);
    try{const tag=await api('tags','POST',{scope:'personal',group_id:group.value,name:name.value.trim()});if(!tagEditorCurrent(editor))return;editor.add.add(tag.id);editor.remove.delete(tag.id);editor.confirmed=false;editor.start.hidden=true;name.value='';editor.offset=0;editor.search.value='';await tagLoadDictionary(editor);tagUpdateSummary(editor)}catch(error){tagEditorMessage(editor,error.message,true)}finally{tagSetBusy(editor,false)}
  });create.append(name,createButton);body.append(create);
  editor.progress=tagNode('div',undefined,'tag-task-progress');body.append(editor.progress);
  editor.message=tagNode('p','','tag-editor-message');editor.message.id='tagEditorMessage';editor.message.setAttribute('aria-live','polite');body.append(editor.message);
  const footer=tagNode('div',undefined,'modal-foot');editor.save=tagButton(config.uploads?'保存上传草稿':'预览修改',()=>tagPreviewChanges(editor),'btn primary');editor.save.id='tagEditorSave';
  editor.start=tagButton('确认执行',()=>tagStartJob(editor),'btn primary');editor.start.id='tagEditorStart';editor.start.hidden=true;
  editor.cancel=tagButton('取消任务',()=>tagJobAction(editor,'cancel'));editor.cancel.hidden=true;
  editor.retry=tagButton('重新读取失败项',()=>tagJobAction(editor,'retry'));editor.retry.id='tagEditorRetry';editor.retry.hidden=true;
  footer.append(tagButton('关闭',()=>dialog.close()),editor.cancel,editor.retry,editor.save,editor.start);dialog.append(footer);
  dialog.addEventListener('close',()=>{clearTimeout(editor.timer);editor.request++});
  scope.onchange=()=>{editor.save.disabled=true;editor.start.hidden=true;tagEditorMessage(editor,'点击“'+(config.uploads?'切换作用域':'重新加载范围')+'”加载；当前草稿尚未执行。')};
  editor.rangeInput&&(editor.rangeInput.onchange=()=>{editor.save.disabled=true;editor.start.hidden=true;tagEditorMessage(editor,'点击“重新加载范围”明确更新范围，当前草稿尚未执行。')});
  group.onchange=()=>{editor.offset=0;tagLoadDictionary(editor)};
  let timer;search.oninput=()=>{clearTimeout(timer);timer=setTimeout(()=>{editor.offset=0;tagLoadDictionary(editor)},250)};
  document.body.append(dialog);dialog.showModal();
  await tagLoadSelection(editor,false);
  return true;
}

async function tagLoadSelection(editor,explicit){
  if(!tagEditorCurrent(editor))return;
  const nextScope=editor.scopeInput.value;
  if(explicit&&(editor.add.size||editor.remove.size)){
    tagEditorMessage(editor,'重新加载已清除未提交的标签选择；请重新选择后预览。');
  }
  editor.scope=nextScope;editor.add.clear();editor.remove.clear();editor.common.clear();editor.partial.clear();editor.confirmed=false;editor.start.hidden=true;editor.job=null;editor.locked=false;
  tagSetBusy(editor,true);
  editor.create.hidden=nextScope!=='personal';editor.save.disabled=true;
  try{
    if(editor.resumeJob){
      const job=await api('tag-batches/'+editor.resumeJob.id);
      if(!tagEditorCurrent(editor))return;
      editor.job=job;editor.scope=job.scope||editor.resumeJob.scope||editor.scope;editor.scopeInput.value=editor.scope;
      editor.common=new Set(job.common_tag_ids||[]);editor.partial=new Set(job.partial_tag_ids||[]);
      editor.add=new Set(job.add_ids||[]);editor.remove=new Set(job.remove_ids||[]);editor.confirmed=job.state==='preview';
      editor.resumeJob=null;tagUpdateSummary(editor);await tagLoadDictionary(editor);tagRenderJob(editor);return;
    }else if(editor.uploads){
      const saved=editor.allUploads?state.tagUploadDefaults:editor.uploads[0].tagDraft;
      for(const id of saved?.[editor.scope]?.add_ids||[])editor.add.add(id);
      for(const id of saved?.[editor.scope]?.remove_ids||[])editor.remove.add(id);
    }else{
      const range=editor.rangeInput.value,input={scope:editor.scope,add_ids:[],remove_ids:[]};
      if(range==='all'){
        const conditions=typeof getActiveTrackFilter==='function'?getActiveTrackFilter():null;
        if(conditions){input.query={conditions};const baseline=typeof getActiveTrackFilterBaseline==='function'?getActiveTrackFilterBaseline():null;if(baseline)input.query.baseline=baseline}
        else input.query={q:$('trackSearch')?.value.trim()||'',filter:state.trackFilter||'all'};
      }
      else input.track_ids=range==='page'?visibleTracks().map(track=>track.id):editor.ids;
      if(!input.query&&!input.track_ids.length)throw new Error('所选范围没有曲目');
      const job=await api('tag-batches','POST',input);if(!tagEditorCurrent(editor))return;
      editor.job=job;editor.common=new Set(job.common_tag_ids||[]);editor.partial=new Set(job.partial_tag_ids||[]);
    }
    editor.save.disabled=false;tagUpdateSummary(editor);await tagLoadDictionary(editor);
  }catch(error){tagEditorMessage(editor,error.message,true)}finally{tagSetBusy(editor,false);if(!editor.job&&!editor.uploads)editor.save.disabled=true}
}
function tagUpdateSummary(editor){
  if(!tagEditorCurrent(editor))return;
  editor.summary.textContent=(editor.uploads?editor.uploads.length+' 首上传草稿':(editor.job?.total||0)+' 首已冻结 · '+editor.common.size+' 个共同标签 · '+editor.partial.size+' 个部分拥有')+' · 添加 '+editor.add.size+' · 移除 '+editor.remove.size;
}
async function tagLoadDictionary(editor){
  const request=++editor.request,query=new URLSearchParams({scope:editor.scope,group_id:editor.group.value,q:editor.search.value.trim(),offset:String(editor.offset),limit:'50'});
  editor.dictionary.textContent='正在读取标签…';
  try{
    const page=await api('tags?'+query);
    if(!tagEditorCurrent(editor)||request!==editor.request)return;
    editor.dictionary.replaceChildren();
    for(const tag of page.items||[]){
      const row=tagNode('div',undefined,'tag-option'),label=tagNode('span',tag.name,'tag-option-name');
      const status=tagNode('small',editor.common.has(tag.id)?'全部拥有':editor.partial.has(tag.id)?'部分拥有':'尚未添加','status');
      const operation=tagNode('select');operation.dataset.tagOperation=tag.id;operation.setAttribute('aria-label','操作 '+tag.name);
      operation.disabled=Boolean(editor.locked||editor.busy);
      for(const [value,text] of [['keep','保持'],['add','添加'],['remove','移除']]){const option=tagNode('option',text);option.value=value;operation.append(option)}
      operation.value=editor.add.has(tag.id)?'add':editor.remove.has(tag.id)?'remove':'keep';
      operation.onchange=()=>{editor.add.delete(tag.id);editor.remove.delete(tag.id);if(operation.value==='add')editor.add.add(tag.id);if(operation.value==='remove')editor.remove.add(tag.id);editor.confirmed=false;editor.start.hidden=true;tagUpdateSummary(editor)};
      row.append(label,status,operation);editor.dictionary.append(row);
    }
    if(!page.items?.length)editor.dictionary.append(tagNode('p','没有匹配标签。','empty'));
    editor.previous.disabled=editor.offset===0;editor.next.disabled=editor.offset+(page.items?.length||0)>=page.total;
    editor.pageText.textContent='共 '+page.total+' 个 · 第 '+(Math.floor(editor.offset/50)+1)+' 页';
  }catch(error){if(tagEditorCurrent(editor)&&request===editor.request){editor.dictionary.textContent='标签读取失败：'+error.message;editor.dictionary.append(tagButton('重试',()=>tagLoadDictionary(editor)))}}
}
async function tagPreviewChanges(editor){
  if(editor.busy||!tagEditorCurrent(editor))return;
  if(editor.uploads){
    const edits={add_ids:[...editor.add],remove_ids:[...editor.remove]};
    for(const upload of editor.uploads){upload.tagDraft={...(upload.tagDraft||{}),[editor.scope]:structuredClone(edits)}}
    if(editor.allUploads)state.tagUploadDefaults={...(state.tagUploadDefaults||{}),[editor.scope]:structuredClone(edits)};
    if(typeof scheduleResumeQueueSave==='function')scheduleResumeQueueSave();
    tagWorkspace.dialog.close();renderUploads();setStatus('标签已保存为上传草稿，确认入库后应用。','ok');return;
  }
  if(!editor.job)return;
  if(!editor.add.size&&!editor.remove.size){tagEditorMessage(editor,'请先选择要添加或移除的标签。');return}
  tagSetBusy(editor,true);
  try{
    const job=await api('tag-batches/'+editor.job.id,'PATCH',{add_ids:[...editor.add],remove_ids:[...editor.remove]},undefined,editor.job.version);
    if(!tagEditorCurrent(editor))return;editor.job=job;editor.confirmed=true;editor.start.hidden=false;
    tagEditorMessage(editor,'将修改 '+job.total+' 首冻结曲目：添加 '+editor.add.size+' 个、移除 '+editor.remove.size+' 个'+tagScopeLabel(editor.scope)+'。请确认执行；不会加入随后新增的曲目。');
  }catch(error){tagEditorMessage(editor,error.message+'；标签草稿已保留。',true)}finally{tagSetBusy(editor,false)}
}
async function tagStartJob(editor){
  if(editor.busy||!editor.confirmed||!editor.job)return;
  await tagJobAction(editor,'start');
}
async function tagJobAction(editor,action){
  if(editor.busy||!tagEditorCurrent(editor))return;
  tagSetBusy(editor,true);editor.start.disabled=true;editor.cancel.disabled=true;editor.retry.disabled=true;
  try{
    const job=await api('tag-batches/'+editor.job.id+'/'+action,'POST',undefined,undefined,editor.job.version);
    if(!tagEditorCurrent(editor))return;editor.job=job;tagWorkspace.lastJob={...job,scope:editor.scope};
    if(action==='retry'){editor.confirmed=true;tagRenderJob(editor);editor.start.hidden=false;tagEditorMessage(editor,'未成功项已重新读取版本。请检查后再次确认执行；成功项不会重复处理。')}
    else tagRenderJob(editor);
  }catch(error){
    try{const job=await api('tag-batches/'+editor.job.id);if(tagEditorCurrent(editor)){editor.job=job;tagWorkspace.lastJob={...job,scope:editor.scope};tagRenderJob(editor)}}catch(_){}
    tagEditorMessage(editor,error.message+'；草稿保留，请重新读取失败项或重新加载范围。',true);
  }finally{tagSetBusy(editor,false);editor.start.disabled=false;editor.cancel.disabled=false;editor.retry.disabled=false}
}
function tagRenderJob(editor){
  const job=editor.job,running=job.state==='running';
  editor.start.hidden=job.state!=='preview';editor.save.disabled=job.state!=='preview';editor.cancel.hidden=!running;editor.retry.hidden=running||!((job.conflicts||0)+(job.failed||0)+(job.cancelled||0)||['interrupted','blocked','failed','partial'].includes(job.state));
  editor.locked=job.state!=='preview';
  editor.scopeInput.disabled=running;if(editor.rangeInput)editor.rangeInput.disabled=running;editor.reload.disabled=running;
  editor.dictionary.querySelectorAll('select').forEach(control=>control.disabled=editor.locked);
  editor.create.querySelectorAll('button,input').forEach(control=>control.disabled=editor.locked);
  const label={preview:'待确认',running:'处理中',completed:'已完成',partial:'部分完成',blocked:'已停止（权限或会话变化）',cancelled:'已取消',interrupted:'已中断',failed:'处理失败'}[job.state]||job.state;
  tagEditorMessage(editor,label+' · '+job.completed+' / '+job.total+' 首 · 冲突 '+job.conflicts+' · 失败 '+job.failed+' · 取消 '+job.cancelled);
  editor.progress.replaceChildren();
  for(const item of (job.items||[]).filter(item=>item.error).slice(0,10))editor.progress.append(tagNode('p',item.track_id+'：'+item.error,'status error'));
  if(running&&tagWorkspace.dialog.open){clearTimeout(editor.timer);editor.timer=setTimeout(async()=>{
    try{const next=await api('tag-batches/'+job.id);if(!tagEditorCurrent(editor))return;editor.job=next;tagWorkspace.lastJob={...next,scope:editor.scope};tagRenderJob(editor)}catch(error){tagEditorMessage(editor,'任务状态读取失败：'+error.message+'；可点击重新读取状态。',true);editor.progress.append(tagButton('重新读取状态',async()=>{try{editor.job=await api('tag-batches/'+job.id);tagRenderJob(editor)}catch(nextError){tagEditorMessage(editor,nextError.message,true)}}))}
  },1000)}
  if(!running&&job.completed>0&&typeof refreshCore==='function')refreshCore().catch(()=>{});
}

function uploadTagEdits(upload){
  if(!tagFeatureEnabled()||!upload.tagDraft)return undefined;
  const versions=upload.preview?.tag_versions;
  const edits={};
  for(const scope of ['public','personal']){
    const draft=upload.tagDraft[scope];if(!draft||!(draft.add_ids?.length||draft.remove_ids?.length))continue;
    if(!versions)throw new Error('请重新校验上传以读取标签版本；标签草稿已保留。');
    edits[scope]={version:versions[scope+'_version'],add_ids:[...(draft.add_ids||[])],remove_ids:[...(draft.remove_ids||[])]};
  }
  return Object.keys(edits).length?edits:undefined;
}
async function decorateTagUploads(){
  if(!await loadTagCapability())return;
  const actions=document.querySelector('.queue-actions');
  if(actions&&!actions.querySelector('[data-tag-feature="upload-defaults"]')){const button=tagButton('本批默认标签',()=>openUploadTagEditor());button.dataset.tagFeature='upload-defaults';actions.append(button)}
  const busy=Boolean(state.uploadBusy||state.uploads.some(upload=>upload.inFlight||['暂存中','确认中'].includes(upload.status)));
  const defaults=actions?.querySelector('[data-tag-feature="upload-defaults"]');if(defaults)defaults.disabled=busy||!tagPendingUploads().length;
  for(const upload of tagPendingUploads()){
    if(!Object.hasOwn(upload,'tagDraft')&&state.tagUploadDefaults)upload.tagDraft=structuredClone(state.tagUploadDefaults);
    const row=[...document.querySelectorAll('[data-upload-id]')].find(row=>row.dataset.uploadId===upload.id);
    if(!row||row.querySelector('[data-tag-feature]'))continue;
    const button=tagButton('编辑标签草稿',()=>openUploadTagEditor(upload),'btn small');button.dataset.tagFeature='upload';button.disabled=Boolean(state.uploadBusy||upload.inFlight||['暂存中','确认中'].includes(upload.status));
    row.querySelector('.upload-buttons')?.prepend(button);
    const count=Object.values(upload.tagDraft||{}).reduce((sum,draft)=>sum+(draft.add_ids?.length||0)+(draft.remove_ids?.length||0),0);
    if(count){const note=tagNode('span',count+' 项标签操作待入库时应用','status');note.dataset.tagFeature='upload-draft';row.querySelector('.upload-meta')?.append(note)}
    if(upload.preview?.genre_values?.length){const note=tagNode('p','文件风格原值：'+upload.preview.genre_values.join('、')+'。入库时按公共词典匹配，未识别的原值会保留。','status');note.dataset.tagFeature='upload-genre';row.querySelector('.upload-meta')?.append(note)}
  }
}
