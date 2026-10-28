'use strict';

const trackFilterWorkspace={owner:null,epoch:0,serial:0,baseline:null,dialog:null,editor:null,labels:new Map()};
function filterNode(tag,text,className){const node=document.createElement(tag);if(text!==undefined)node.textContent=text;if(className)node.className=className;return node}
function filterButton(label,action,className='btn'){const button=filterNode('button',label,className);button.type='button';button.onclick=action;return button}
function clearTrackFilterWorkspace(){trackFilterWorkspace.epoch++;trackFilterWorkspace.owner=null;trackFilterWorkspace.baseline=null;trackFilterWorkspace.dialog?.remove();trackFilterWorkspace.dialog=null;trackFilterWorkspace.editor=null;trackFilterWorkspace.labels.clear();document.querySelectorAll('[data-track-filter-ui]').forEach(node=>node.remove())}
function trackFiltersEnabled(){return Boolean(state.user&&typeof tagFeatureEnabled==='function'&&tagFeatureEnabled())}
function filterOwner(){if(trackFilterWorkspace.owner!==state.user?.id){clearTrackFilterWorkspace();trackFilterWorkspace.owner=state.user?.id||null}}
function filterRouteValues(){return new URLSearchParams(location.hash.split('?')[1]||'')}
function readTrackFilterRoute(){
  const values=filterRouteValues();let filter={version:1};
  if(values.has('tf')){try{filter=JSON.parse(values.get('tf'));if(!filter||typeof filter!=='object'||Array.isArray(filter)||filter.version!==1)throw new Error();
    for(const key of ['artists','albums','formats'])if(filter[key]!==undefined&&(!Array.isArray(filter[key])||filter[key].length>20||filter[key].some(value=>typeof value!=='string')))throw new Error();
    if(filter.tags!==undefined&&(!Array.isArray(filter.tags)||filter.tags.length>12||filter.tags.some(clause=>!clause||typeof clause!=='object'||typeof clause.scope!=='string'||typeof clause.group_id!=='string'||['include_ids','exclude_ids'].some(key=>clause[key]!==undefined&&(!Array.isArray(clause[key])||clause[key].length>64||clause[key].some(value=>typeof value!=='string'))))))throw new Error();
  }catch(_){throw Object.assign(new Error('筛选地址无效，请清除条件后重新选择。'),{code:'FILTER_INVALID'})}}
  filter={...filter,version:1};
  const query=$('trackSearch')?.value.trim()||'';if(query)filter.q=query;else delete filter.q;
  filter.range=state.trackFilter||values.get('filter')||'all';
  return {filter,sort:values.get('sort')||'title',direction:values.get('dir')||'asc'};
}
function getActiveTrackFilter(){if(!trackFiltersEnabled())return null;return structuredClone(readTrackFilterRoute().filter)}
function getActiveTrackFilterBaseline(){if(!trackFiltersEnabled())return undefined;const page=browseState.tracks;return page?.filterKey===activeTrackFilterKey()?page.baseline:undefined}
function activeTrackFilterKey(){if(!trackFiltersEnabled())return '';try{return JSON.stringify(readTrackFilterRoute())}catch(_){return 'invalid:'+location.hash}}
function writeTrackFilterRoute(filter,sort='title',direction='asc',offset=0){
  const values=new URLSearchParams(),copy=structuredClone(filter);delete copy.q;delete copy.range;
  if(filter.q)values.set('q',filter.q);if(filter.range&&filter.range!=='all')values.set('filter',filter.range);
  if(Object.keys(copy).some(key=>key!=='version'))values.set('tf',JSON.stringify(copy));
  if(sort!=='title')values.set('sort',sort);if(direction!=='asc')values.set('dir',direction);if(offset)values.set('offset',String(offset));
  history.replaceState(null,'',(location.hash.split('?')[0]||'#/library')+(values.size?'?'+values:''));
}
function filterNotice(text,error=false){const host=$('trackFilterMessage');if(host){host.textContent=text;host.classList.toggle('error',error);host.setAttribute('role',error?'alert':'status');if(error)host.append(document.createTextNode(' '),filterButton('重试读取',()=>loadFilteredTracks(0).then(page=>page&&typeof renderTracks==='function'&&renderTracks()).catch(()=>{}),'btn small'))}if(text)setStatus(text,error?'error':'ok')}
async function initializeTrackFilters(){
  if(!state.user||typeof loadTagCapability!=='function')return false;
  filterOwner();const owner=state.user.id,epoch=trackFilterWorkspace.epoch;
  if(!await loadTagCapability()||owner!==state.user?.id||epoch!==trackFilterWorkspace.epoch)return false;
  const toolbar=document.querySelector('#page-tracks .track-toolbar');if(!toolbar)return false;
  if(!$('trackAdvancedFilter')){const button=filterButton('筛选与分类',()=>openTrackFilterEditor());button.id='trackAdvancedFilter';button.dataset.trackFilterUi='button';toolbar.append(button)}
  if(!$('trackFilterChips')){const chips=filterNode('div',undefined,'track-filter-chips');chips.id='trackFilterChips';chips.dataset.trackFilterUi='chips';toolbar.append(chips)}
  if(!$('trackFilterMessage')){const message=filterNode('p','','track-filter-message');message.id='trackFilterMessage';message.dataset.trackFilterUi='message';message.setAttribute('role','status');toolbar.append(message)}
  renderTrackFilterChips();return true;
}
function renderTrackFilterChips(){
  const host=$('trackFilterChips');if(!host)return;host.replaceChildren();
  let config;try{config=readTrackFilterRoute()}catch(error){filterNotice(error.message,true);host.append(filterButton('清除无效条件',()=>applyTrackFilter({version:1,range:'all'},'title','asc')));return}
  const f=config.filter,groups=typeof tagWorkspace!=='undefined'?tagWorkspace.groups||[]:[];
  const chip=(label,remove)=>{const button=filterButton(label+' ×',remove,'btn small filter-chip');host.append(button)};
  const update=next=>applyTrackFilter(next,config.sort,config.direction);
  (f.tags||[]).forEach((clause,index)=>{const group=groups.find(group=>group.id===clause.group_id)?.name||clause.group_id;
    chip((clause.scope==='personal'?'我的':'公共')+group+'：'+(clause.match==='all'?'全部':'任一')+' '+(clause.include_ids?.length||0)+'，排除 '+(clause.exclude_ids?.length||0),()=>{const next=structuredClone(f);next.tags.splice(index,1);if(!next.tags.length)delete next.tags;update(next)})});
  for(const [key,label]of [['artists','歌手'],['albums','专辑'],['formats','格式']])if(f[key]?.length)chip(label+'：'+f[key].join('、'),()=>{const next={...f};delete next[key];update(next)});
  for(const [key,label]of [['duration_min_ms','最短时长'],['duration_max_ms','最长时长'],['has_lyrics','歌词'],['has_cover','封面'],['untagged_public','未标公共标签'],['untagged_personal','未标个人标签'],['uploaded_by','上传者']])if(Object.hasOwn(f,key))chip(label+(typeof f[key]==='boolean'?'：'+(f[key]?'有／是':'无'):': '+(key.startsWith('duration')?f[key]/1000+' 秒':f[key])),()=>{const next={...f};delete next[key];update(next)});
  if(host.children.length)host.append(filterButton('清除筛选',()=>update({version:1,q:f.q,range:f.range}),'btn small'));
}
function applyFilterRedirects(filter,redirects){
  if(!redirects?.length)return filter;const targets=new Map(redirects.map(item=>[item.from,item.to])),next=structuredClone(filter);
  for(const clause of next.tags||[])for(const key of ['include_ids','exclude_ids'])if(clause[key])clause[key]=[...new Set(clause[key].map(id=>targets.get(id)||id))];
  return next;
}
async function loadFilteredTracks(offset=0){
  browseReset();filterOwner();const generation=++browseState.generations.tracks,epoch=trackFilterWorkspace.epoch,owner=state.user?.id,route=location.hash.split('?')[0];
  const current=()=>generation===browseState.generations.tracks&&owner===state.user?.id&&epoch===trackFilterWorkspace.epoch&&route===location.hash.split('?')[0];
  let config,key;
  try{
    config=readTrackFilterRoute();key=JSON.stringify(config);const previous=browseState.tracks;
    const input={...config,offset,limit:typeof browsePageSize==='number'?browsePageSize:50};if(offset&&previous?.filterKey===key)input.baseline=previous.baseline;
    let page;
    try{page=await api('track-query','POST',input)}catch(error){
      if(error.code!=='FILTER_SNAPSHOT_CHANGED'||!current())throw error;
      delete input.baseline;input.offset=0;page=await api('track-query','POST',input);
      if(current()){writeTrackFilterRoute(config.filter,config.sort,config.direction,0);filterNotice('曲库或标签数据已更新，已重新读取第一页。')}
    }
    if(!current())return null;
    if($('trackFilterMessage')?.classList.contains('error')){filterNotice('');setStatus('筛选已更新','ok')}
    if(page.redirects?.length){config.filter=applyFilterRedirects(config.filter,page.redirects);writeTrackFilterRoute(config.filter,config.sort,config.direction,page.offset);key=JSON.stringify(config);filterNotice('部分标签已合并，筛选已明确替换为目标标签。')}
    page.items=browseReuse(page.items||[],previous?.items);page.query=config.filter.q||'';page.filter=config.filter.range||'all';page.filterKey=key;page.revision=page.baseline.library;
    browseState.tracks=page;trackFilterWorkspace.baseline=page.baseline;browseCacheTracks();browseCachePlaylists();renderTrackFilterChips();return page;
  }catch(error){
    if(current()){browseState.tracks={items:[],total:0,offset:0,limit:50,query:config?.filter.q||'',filter:config?.filter.range||'all',filterKey:key,error:error.message};browseCacheTracks();filterNotice(error.code==='FILTER_REFERENCE_INVALID'?'筛选引用已失效，请编辑条件；没有扩大结果范围。':error.message,true);if(typeof renderTracks==='function')renderTracks()}
    throw error;
  }
}
async function applyTrackFilter(filter,sort='title',direction='asc'){
  if(!trackFiltersEnabled())return;state.trackFilter=filter.range||'all';if($('trackSearch'))$('trackSearch').value=filter.q||'';
  document.querySelectorAll('[data-filter]').forEach(button=>button.classList.toggle('active',button.dataset.filter===state.trackFilter));
  state.selectedTrackIds?.clear();trackFilterWorkspace.baseline=null;writeTrackFilterRoute(filter,sort,direction,0);
  try{const page=await loadFilteredTracks(0);if(page&&typeof renderTracks==='function')renderTracks()}catch(_){}
}
async function applyTrackTagFilter(tag){
  if(!await initializeTrackFilters())return;
  let f;try{f=getActiveTrackFilter()}catch(_){f={version:1,range:'all'}}
  f.tags=(f.tags||[]).filter(clause=>clause.scope!==tag.scope||clause.group_id!==tag.group_id);
  f.tags.push({scope:tag.scope,group_id:tag.group_id,match:'any',include_ids:[tag.id],exclude_ids:[]});
  await applyTrackFilter(f);
}
function filterEditorCurrent(editor){return trackFilterWorkspace.editor===editor&&editor.epoch===trackFilterWorkspace.epoch&&state.user?.id===trackFilterWorkspace.owner}
function filterEditorMessage(editor,text,error=false){if(!filterEditorCurrent(editor))return;editor.message.textContent=text;editor.message.classList.toggle('error',error)}
function filterField(host,label,control){const field=filterNode('label',undefined,'field');field.append(filterNode('span',label),control);host.append(field);return control}
function filterSelect(id,options){const select=filterNode('select');select.id=id;for(const [value,label]of options){const option=filterNode('option',label);option.value=value;select.append(option)}return select}
function filterInput(id,type='text'){const input=filterNode('input');input.id=id;input.type=type;return input}
function readFilterDraft(editor){
  const f=structuredClone(editor.filter);f.version=1;f.range=editor.range.value;
  for(const key of ['artists','albums']){const values=editor[key].value.split('\n').map(x=>x.trim()).filter(Boolean);if(values.length)f[key]=values;else delete f[key]}
  const formats=[...editor.formats.selectedOptions].map(option=>option.value);if(formats.length)f.formats=formats;else delete f.formats;
  for(const [key,input]of [['duration_min_ms',editor.min],['duration_max_ms',editor.max]]){if(input.value===''){delete f[key];continue}const seconds=Number(input.value);if(!Number.isFinite(seconds)||seconds<0)throw new Error('时长必须是非负秒数');f[key]=Math.round(seconds*1000)}
  for(const [key,input]of [['has_lyrics',editor.lyrics],['has_cover',editor.cover]]){if(input.value==='any')delete f[key];else f[key]=input.value==='yes'}
  for(const [key,input]of [['untagged_public',editor.noPublic],['untagged_personal',editor.noPersonal]]){if(input.checked)f[key]=true;else delete f[key]}
  if(editor.uploader){if(editor.uploader.value.trim())f.uploaded_by=editor.uploader.value.trim();else delete f.uploaded_by}
  return f;
}
async function openTrackFilterEditor(prefill){
  if(!state.user)return false;filterOwner();const owner=state.user.id,epoch=trackFilterWorkspace.epoch;
  if(!await initializeTrackFilters()||owner!==state.user?.id||epoch!==trackFilterWorkspace.epoch)return false;
  trackFilterWorkspace.dialog?.remove();let config;try{config=readTrackFilterRoute()}catch(_){config={filter:{version:1,range:'all'},sort:'title',direction:'asc'}}
  if(prefill?.filter)config.filter=structuredClone(prefill.filter);
  const editor={epoch:trackFilterWorkspace.epoch,serial:++trackFilterWorkspace.serial,filter:structuredClone(config.filter),facetRequest:0,facetOffset:0,savedRequest:0,presets:[]};
  const dialog=filterNode('dialog',undefined,'modal drawer track-filter-dialog');dialog.id='trackFilterDialog';trackFilterWorkspace.dialog=dialog;trackFilterWorkspace.editor=editor;
  const head=filterNode('div',undefined,'modal-head');head.append(filterNode('h3','筛选与分类'),filterButton('关闭',()=>dialog.close()));dialog.append(head);
  const body=filterNode('div',undefined,'modal-body');dialog.append(body);const form=filterNode('div',undefined,'filter-field-grid');body.append(form);
  const ranges=[['all','全部曲目'],['mine','本人上传'],['managed','本人管理'],['orphan','未加入本人歌单']];if(state.user.role==='admin'||editor.filter.range==='missing')ranges.push(['missing','缺失文件（仅管理员）']);editor.range=filterField(form,'曲库范围',filterSelect('filterRange',ranges));editor.range.value=editor.filter.range||'all';
  editor.sort=filterField(form,'排序',filterSelect('filterSort',[['title','标题'],['artist','歌手'],['album','专辑'],['duration_ms','时长'],['modified_at','文件修改时间']]));editor.sort.value=config.sort;
  editor.direction=filterField(form,'顺序',filterSelect('filterDirection',[['asc','升序'],['desc','降序']]));editor.direction.value=config.direction;
  for(const [key,label]of [['artists','歌手（每行一个，精确匹配）'],['albums','专辑（每行一个，精确匹配）']]){const input=filterNode('textarea');input.id=key==='artists'?'filterArtists':'filterAlbums';input.rows=2;input.value=(editor.filter[key]||[]).join('\n');editor[key]=filterField(form,label,input)}
  editor.formats=filterField(form,'格式（可多选）',filterSelect('filterFormats',['aac','flac','m4a','mp3','oga','ogg','opus','wav','wma'].map(value=>[value,value.toUpperCase()])));editor.formats.multiple=true;editor.formats.size=3;for(const option of editor.formats.options)option.selected=(editor.filter.formats||[]).includes(option.value);
  editor.min=filterField(form,'最短时长（秒）',filterInput('filterDurationMin','number'));editor.max=filterField(form,'最长时长（秒）',filterInput('filterDurationMax','number'));for(const input of [editor.min,editor.max]){input.min='0';input.step='1'}editor.min.value=editor.filter.duration_min_ms===undefined?'':editor.filter.duration_min_ms/1000;editor.max.value=editor.filter.duration_max_ms===undefined?'':editor.filter.duration_max_ms/1000;
  for(const [key,id,label]of [['lyrics','filterHasLyrics','歌词'],['cover','filterHasCover','封面']]){editor[key]=filterField(form,label,filterSelect(id,[['any','不限'],['yes','有'],['no','无']]));const value=editor.filter[key==='lyrics'?'has_lyrics':'has_cover'];editor[key].value=value===undefined?'any':value?'yes':'no'}
  editor.noPublic=filterField(form,'未标公共标签',filterInput('filterNoPublic','checkbox'));editor.noPublic.checked=Boolean(editor.filter.untagged_public);editor.noPersonal=filterField(form,'未标个人标签',filterInput('filterNoPersonal','checkbox'));editor.noPersonal.checked=Boolean(editor.filter.untagged_personal);
  if(state.user.role==='admin'||editor.filter.uploaded_by){
    const accounts=state.user.role==='admin'?(state.users||[]):[],options=[['','不限上传者'],...accounts.map(user=>[user.id,(user.display_name&&user.display_name!==user.username?user.display_name+'（'+user.username+'）':user.username)+(user.deleted_at?' · 已删除':user.enabled===false?' · 已停用':'')])];
    if(editor.filter.uploaded_by&&!options.some(([id])=>id===editor.filter.uploaded_by))options.push([editor.filter.uploaded_by,'原条件中的账户']);
    editor.uploader=filterField(form,'上传者（仅管理员）',filterSelect('filterUploader',options));editor.uploader.value=editor.filter.uploaded_by||'';
  }
  const tagSection=filterNode('section',undefined,'filter-tag-section');body.append(tagSection);tagSection.append(filterNode('h4','标签条件'),filterNode('p','同组任一／全部，跨组同时满足。计数应用其他条件后计算当前分组。','status'));
  editor.draftTags=filterNode('div',undefined,'track-filter-chips');editor.draftTags.id='filterDraftTags';tagSection.append(editor.draftTags);renderFilterDraftTags(editor);
  const controls=filterNode('div',undefined,'filter-field-grid');tagSection.append(controls);
  editor.scope=filterField(controls,'作用域',filterSelect('filterScope',[['public','公共标签'],['personal','个人标签']]));
  editor.group=filterField(controls,'分组',filterSelect('filterGroup',(tagWorkspace.groups||[]).map(group=>[group.id,group.name])));
  editor.match=filterField(controls,'包含方式',filterSelect('filterMatch',[['any','任一标签'],['all','全部标签']]));
  editor.search=filterField(controls,'搜索本组标签',filterInput('filterTagSearch'));editor.search.maxLength=64;
  editor.facetRows=filterNode('div',undefined,'filter-facet-rows');editor.facetRows.id='filterFacetRows';tagSection.append(editor.facetRows);
  editor.facetPager=filterNode('div',undefined,'actions');tagSection.append(editor.facetPager);editor.facetPager.append(filterButton('上一页',()=>loadFilterFacets(editor,Math.max(0,editor.facetOffset-50))),filterNode('span','','status'),filterButton('下一页',()=>loadFilterFacets(editor,editor.facetOffset+50)));
  const reload=()=>{editor.facetOffset=0;loadFilterFacets(editor,0)};editor.scope.onchange=reload;editor.group.onchange=reload;editor.match.onchange=()=>{const clause=filterClause(editor);if(clause)clause.match=editor.match.value;renderFilterDraftTags(editor)};let searchTimer;editor.search.oninput=()=>{clearTimeout(searchTimer);editor.facetRequest++;searchTimer=setTimeout(reload,220)};
  for(const control of form.querySelectorAll('input,select,textarea'))control.addEventListener('change',reload);
  const saved=filterNode('section',undefined,'filter-saved-section');saved.append(filterNode('h4','保存的个人筛选条件'));body.append(saved);
  editor.preset=filterField(saved,'选择条件',filterSelect('savedTrackFilter',[['','请选择']]));editor.name=filterField(saved,'条件名称',filterInput('savedFilterName'));editor.name.maxLength=64;
  const savedActions=filterNode('div',undefined,'actions');saved.append(savedActions);
  const save=filterButton('保存草稿为条件',()=>filterPresetAction(editor,()=>saveTrackFilterPreset(editor)));save.id='filterSavePreset';const apply=filterButton('应用条件',()=>filterPresetAction(editor,()=>applySavedTrackFilter(editor)));apply.id='filterApplyPreset';const rename=filterButton('重命名',()=>filterPresetAction(editor,()=>renameTrackFilterPreset(editor)));rename.id='filterRenamePreset';const remove=filterButton('删除保存条件',()=>filterPresetAction(editor,()=>deleteTrackFilterPreset(editor)),'btn danger');remove.id='filterDeletePreset';const edit=filterButton('载入条件草稿',()=>filterPresetAction(editor,()=>editSavedTrackFilter(editor)));edit.id='filterEditPreset';const update=filterButton('更新选中条件',()=>filterPresetAction(editor,()=>updateSavedTrackFilter(editor)));update.id='filterUpdatePreset';savedActions.append(save,apply,edit,update,rename,remove,filterButton('重新读取列表',()=>filterPresetAction(editor,()=>loadSavedTrackFilters(editor,editor.preset.value))));
  editor.preset.onchange=()=>{const p=editor.presets.find(p=>p.id===editor.preset.value);editor.name.value=p?.name||'';if(p?.validation?.valid===false)filterEditorMessage(editor,'该保存条件引用已失效，请编辑后重新保存；不会自动扩大结果。',true)};
  editor.message=filterNode('p','','filter-dialog-message');editor.message.id='filterDialogMessage';editor.message.setAttribute('aria-live','polite');body.append(editor.message);
  const foot=filterNode('div',undefined,'modal-foot');const applyDraft=filterButton('应用筛选',async()=>{try{const f=readFilterDraft(editor);dialog.close();await applyTrackFilter(f,editor.sort.value,editor.direction.value)}catch(error){filterEditorMessage(editor,error.message,true)}},'btn primary');applyDraft.id='filterApply';foot.append(filterButton('清除条件',async()=>{dialog.close();await applyTrackFilter({version:1,range:'all'})}),filterButton('关闭',()=>dialog.close()),applyDraft);dialog.append(foot);
  dialog.addEventListener('close',()=>{clearTimeout(searchTimer);editor.facetRequest++});document.body.append(dialog);dialog.showModal();await Promise.all([loadFilterFacets(editor,0),loadSavedTrackFilters(editor)]);return editor.serial;
}
function renderFilterDraftTags(editor){
  editor.draftTags.replaceChildren();
  for(const [index,clause]of (editor.filter.tags||[]).entries()){
    const group=tagWorkspace.groups.find(group=>group.id===clause.group_id)?.name||clause.group_id;
    editor.draftTags.append(filterButton((clause.scope==='personal'?'我的':'公共')+group+'：包含 '+(clause.include_ids?.length||0)+'、排除 '+(clause.exclude_ids?.length||0)+' ×',()=>{editor.filter.tags.splice(index,1);renderFilterDraftTags(editor);loadFilterFacets(editor,0)},'btn small filter-chip'));
  }
}
function filterClause(editor,create=false){
  let clause=(editor.filter.tags||[]).find(clause=>clause.scope===editor.scope.value&&clause.group_id===editor.group.value);
  if(!clause&&create){clause={scope:editor.scope.value,group_id:editor.group.value,match:editor.match.value,include_ids:[],exclude_ids:[]};(editor.filter.tags||=[]).push(clause)}return clause;
}
async function loadFilterFacets(editor,offset=0){
  const request=++editor.facetRequest;editor.facetRows.textContent='正在读取分类计数…';
  try{const f=readFilterDraft(editor);let clause=filterClause(editor);editor.match.value=clause?.match||'any';
    const page=await api('track-facets','POST',{filter:f,scope:editor.scope.value,group_id:editor.group.value,q:editor.search.value.trim(),offset,limit:50});
    if(!filterEditorCurrent(editor)||request!==editor.facetRequest)return;editor.facetOffset=page.offset;editor.facetRows.replaceChildren();
    if(page.redirects?.length){editor.filter=applyFilterRedirects(editor.filter,page.redirects);clause=filterClause(editor);renderFilterDraftTags(editor);filterEditorMessage(editor,'部分标签已合并，草稿中的 UUID 已明确替换为目标标签；尚未应用到曲库。')}
    for(const item of page.items){const tag=item.tag;trackFilterWorkspace.labels.set(tag.id,tag.name);const row=filterNode('label',undefined,'filter-facet-row');row.append(filterNode('span',tag.name+' · '+item.count));const select=filterSelect('',[['any','不限'],['include','包含'],['exclude','排除']]);select.dataset.filterTerm=tag.id;select.setAttribute('aria-label',tag.name+' 条件');select.value=clause?.exclude_ids?.includes(tag.id)?'exclude':clause?.include_ids?.includes(tag.id)?'include':'any';select.onchange=()=>{const target=filterClause(editor,true);target.include_ids=(target.include_ids||[]).filter(id=>id!==tag.id);target.exclude_ids=(target.exclude_ids||[]).filter(id=>id!==tag.id);if(select.value==='include')target.include_ids.push(tag.id);if(select.value==='exclude')target.exclude_ids.push(tag.id);if(!target.include_ids.length&&!target.exclude_ids.length)editor.filter.tags=editor.filter.tags.filter(c=>c!==target);renderFilterDraftTags(editor)};row.append(select);editor.facetRows.append(row)}
    if(!page.items.length)editor.facetRows.append(filterNode('p','没有匹配词条；可修改搜索或分组。','status'));const [previous,status,next]=editor.facetPager.children;previous.disabled=page.offset<=0;next.disabled=page.offset+page.items.length>=page.total;status.textContent='共 '+page.total+' 项 · 第 '+(Math.floor(page.offset/50)+1)+' 页';
  }catch(error){if(filterEditorCurrent(editor)&&request===editor.facetRequest){editor.facetRows.replaceChildren(filterNode('p','分类读取失败：'+error.message,'error'),filterButton('重试分类',()=>loadFilterFacets(editor,offset)));filterEditorMessage(editor,error.message,true)}}
}
async function loadSavedTrackFilters(editor,selected=''){
  const request=++editor.savedRequest;
  try{const response=await api('saved-track-filters');if(!filterEditorCurrent(editor)||request!==editor.savedRequest)return;editor.presets=Array.isArray(response)?response:response.items||[];editor.preset.replaceChildren();const none=filterNode('option','请选择保存条件');none.value='';editor.preset.append(none);for(const p of editor.presets){const option=filterNode('option',p.name+(p.validation?.valid===false?'（失效）':''));option.value=p.id;editor.preset.append(option)}editor.preset.value=selected}
  catch(error){if(request===editor.savedRequest)filterEditorMessage(editor,'保存条件读取失败：'+error.message,true)}
}
async function filterPresetAction(editor,action){
  if(editor.busy||!filterEditorCurrent(editor))return;editor.busy=true;
  editor.facetRequest++;
  const controls=new Map([...trackFilterWorkspace.dialog.querySelectorAll('input,select,textarea,button')].map(control=>[control,control.disabled]));
  controls.forEach((_,control)=>control.disabled=true);
  try{await action()}finally{editor.busy=false;if(filterEditorCurrent(editor)){controls.forEach((disabled,control)=>control.disabled=disabled);void loadFilterFacets(editor,editor.facetOffset)}}
}
async function saveTrackFilterPreset(editor){try{const name=editor.name.value.trim();if(!name)throw new Error('请输入条件名称');const p=await api('saved-track-filters','POST',{name,filter:readFilterDraft(editor)});if(!filterEditorCurrent(editor))return;await loadSavedTrackFilters(editor,p.id);filterEditorMessage(editor,'已保存个人筛选条件。')}catch(error){filterEditorMessage(editor,error.message+'；草稿保留。',true)}}
async function applySavedTrackFilter(editor){try{if(!editor.preset.value)throw new Error('请选择保存条件');const p=await api('saved-track-filters/'+encodeURIComponent(editor.preset.value));if(!filterEditorCurrent(editor))return;if(p.validation?.valid===false)throw new Error('保存条件引用已失效，请编辑后重新保存。');const f=p.validation?.resolved_filter||p.filter;if(p.validation?.redirects?.length)filterNotice('保存条件中的合并标签已解析到目标标签。');trackFilterWorkspace.dialog.close();await applyTrackFilter(f)}catch(error){filterEditorMessage(editor,error.message,true)}}
async function renameTrackFilterPreset(editor){try{const p=editor.presets.find(p=>p.id===editor.preset.value);if(!p)throw new Error('请选择保存条件');const name=editor.name.value.trim();if(!name)throw new Error('请输入条件名称');await api('saved-track-filters/'+encodeURIComponent(p.id),'PATCH',{name},undefined,p.version);await loadSavedTrackFilters(editor,p.id);filterEditorMessage(editor,'名称已保存。')}catch(error){filterEditorMessage(editor,error.message+'；名称草稿保留，请重新读取条件。',true)}}
async function deleteTrackFilterPreset(editor){try{const p=editor.presets.find(p=>p.id===editor.preset.value);if(!p)throw new Error('请选择保存条件');await api('saved-track-filters/'+encodeURIComponent(p.id),'DELETE',undefined,undefined,p.version);await loadSavedTrackFilters(editor);filterEditorMessage(editor,'保存条件已删除，当前筛选不变。')}catch(error){filterEditorMessage(editor,error.message,true)}}
async function editSavedTrackFilter(editor){const owner=state.user?.id,epoch=trackFilterWorkspace.epoch;try{if(!editor.preset.value)throw new Error('请选择保存条件');const p=await api('saved-track-filters/'+encodeURIComponent(editor.preset.value));if(!filterEditorCurrent(editor))return;const serial=await openTrackFilterEditor({filter:p.filter});if(!serial||owner!==state.user?.id||epoch!==trackFilterWorkspace.epoch||trackFilterWorkspace.editor?.serial!==serial)return;const next=trackFilterWorkspace.editor;next.preset.value=p.id;next.name.value=p.name;filterEditorMessage(next,p.validation?.valid===false?'已载入失效条件原草稿，请移除失效标签后更新；尚未应用到曲库。':'已载入保存条件草稿，尚未应用到曲库。',p.validation?.valid===false)}catch(error){filterEditorMessage(editor,error.message,true)}}
async function updateSavedTrackFilter(editor){try{const p=editor.presets.find(p=>p.id===editor.preset.value);if(!p)throw new Error('请选择要更新的保存条件');const saved=await api('saved-track-filters/'+encodeURIComponent(p.id),'PATCH',{name:editor.name.value.trim(),filter:readFilterDraft(editor)},undefined,p.version);await loadSavedTrackFilters(editor,saved.id);filterEditorMessage(editor,'保存条件已更新，当前曲库尚未重新应用。')}catch(error){filterEditorMessage(editor,error.message+'；草稿保留。',true)}}
