'use strict';

let uploadListPage=0,uploadListFilter='all',uploadBatchOwner=null;
const uploadTargetCatalog={owner:null,generation:0,loaded:false,items:[],offset:0,total:0,selected:'',hasSelection:false,names:new Map()};
const uploadPageSize=50,uploadParseQueue=[],uploadParseTasks=new WeakMap(),uploadParseGenerations=new WeakMap();let uploadParseActive=0;
const uploadBatchControls=document.createElement('div');uploadBatchControls.className='upload-batch toolbar';
uploadBatchControls.innerHTML='<input id="uploadBatchArtist" placeholder="批量设置作者" aria-label="批量设置作者"><input id="uploadBatchAlbum" placeholder="批量设置专辑" aria-label="批量设置专辑"><button id="applyUploadMetadata" class="btn">应用到未完成项</button><label>显示 <select id="uploadQueueFilter" class="select"><option value="all">全部任务</option><option value="pending">未完成</option><option value="failed">失败及待重试</option><option value="done">已完成</option></select></label>';
uploadFlowControls.after(uploadBatchControls);
const uploadDestination=document.createElement('section');uploadDestination.className='panel upload-destination';uploadDestination.id='uploadDestination';uploadDestination.setAttribute('aria-label','本次上传位置');uploadDestination.innerHTML='<div><h2>本次上传到</h2><p>选择自己的歌单，曲目入库后自动加入。已开始的任务保留原目标。</p></div><div class="upload-destination-controls"><select id="uploadTargetPlaylist" class="select" aria-label="本次上传到"><option value="">仅加入曲库</option></select><button id="createUploadPlaylist" type="button" class="btn">新建歌单</button><a id="openUploadPlaylist" hidden>打开目标歌单</a></div>';$('uploadZone').before(uploadDestination);
const uploadTargetSearch=document.createElement('input');uploadTargetSearch.id='uploadTargetSearch';uploadTargetSearch.placeholder='搜索自己的歌单';uploadTargetSearch.setAttribute('aria-label','搜索目标歌单');uploadDestination.append(uploadTargetSearch);
const uploadTargetPages=document.createElement('div');uploadTargetPages.className='actions';uploadTargetPages.id='uploadTargetPages';uploadDestination.append(uploadTargetPages);
function uploadTargetMutable(u){return !u.inFlight&&(u.targetNeedsSelection||!u.targetLocked&&!u.resumeId&&!u.stageId&&!u.commitResult&&uploadFlowPending(u))}
function setUploadTarget(target,items=state.uploads.filter(uploadTargetMutable),updateDefault=true){if(target==='__mixed__')throw new Error('请明确选择目标歌单或“仅加入曲库”');if(updateDefault){uploadTargetCatalog.selected=target;uploadTargetCatalog.hasSelection=true}for(const u of items){u.targetPlaylistID=target;u.targetNeedsSelection=false;if(u.commitResult){u.playlistAssigned=!target;u.assignmentError=target?'加入目标歌单尚未完成':'';u.error=u.assignmentError}}void persistResumeQueue();renderUploads()}
function confirmUploadTarget(upload,target){if(target==='__mixed__')throw new Error('请明确选择目标歌单或“仅加入曲库”');setUploadTarget(target,[upload],false)}
async function retryUploadAssignment(upload,target){
 if(!target||target==='__mixed__')throw new Error('请选择要加入的目标歌单；仅加入曲库请使用目标选择确认。');
 if(!state.uploads.includes(upload)||upload.inFlight||!upload.commitResult?.track?.id)return;
 setUploadTarget(target,[upload],false);upload.targetLocked=true;upload.playlistAssigned=false;await assignUploadResults([upload]);
}
async function createUploadTarget(name){
 const owner=state.user?.id,epoch=resumeWorkspaceEpoch;name=name.trim();if(!name||name.length>200)throw new Error('请输入1–200字的歌单名称');
 const p=await api('playlists','POST',{id:browserUUID(),name,track_ids:[]});if(!uploadFlowCurrent(owner,epoch))return;
 uploadTargetCatalog.items.push(p);uploadTargetCatalog.names.set(p.id,p.name);const index=state.playlists.findIndex(item=>item.id===p.id);if(index<0)state.playlists.push(p);else state.playlists[index]=p;setUploadTarget(p.id);return p;
}
let uploadCreateDialog=null;
function openUploadPlaylistCreate(){
 uploadCreateDialog?.remove();const dialog=document.createElement('dialog');uploadCreateDialog=dialog;dialog.className='upload-create-dialog';dialog.setAttribute('aria-label','新建上传目标歌单');dialog.innerHTML='<form id="uploadCreateForm"><h2>新建歌单</h2><label for="uploadCreateName">歌单名称</label><input id="uploadCreateName" maxlength="200" required autocomplete="off"><p id="uploadCreateError" class="status error" role="alert"></p><div class="actions"><button id="uploadCreateCancel" type="button" class="btn">取消</button><button id="uploadCreateSave" type="submit" class="btn primary">创建并选中</button></div></form>';document.body.append(dialog);const owner=state.user?.id,epoch=resumeWorkspaceEpoch;
 const dispose=()=>{dialog.remove();if(uploadCreateDialog===dialog)uploadCreateDialog=null};dialog.addEventListener('close',dispose);dialog.addEventListener('cancel',event=>{if(dialog.dataset.saving==='true'){event.preventDefault();return}dispose()});$('uploadCreateCancel').onclick=()=>dialog.close();
 $('uploadCreateForm').onsubmit=async event=>{event.preventDefault();if(dialog.dataset.saving==='true')return;dialog.dataset.saving='true';$('uploadCreateSave').disabled=true;$('uploadCreateCancel').disabled=true;try{await createUploadTarget($('uploadCreateName').value);dispose()}catch(error){if(uploadFlowCurrent(owner,epoch)&&dialog.isConnected){$('uploadCreateError').textContent=error.message;$('uploadCreateSave').disabled=false;$('uploadCreateCancel').disabled=false}}finally{dialog.dataset.saving='false'}};
 dialog.showModal();$('uploadCreateName').focus();
}
$('createUploadPlaylist').onclick=openUploadPlaylistCreate;
async function loadUploadTargets(offset=0){
 const owner=state.user?.id,generation=++uploadTargetCatalog.generation;if(!owner)return;
 const page=await api('browse/playlists?'+new URLSearchParams({q:uploadTargetSearch.value.trim(),offset:String(offset),limit:'50',summary_only:'1'}));
 if(owner!==state.user?.id||owner!==uploadTargetCatalog.owner||generation!==uploadTargetCatalog.generation)return;
 uploadTargetCatalog.loaded=true;uploadTargetCatalog.items=page.items;uploadTargetCatalog.offset=page.offset;uploadTargetCatalog.total=page.total;
 for(const p of page.items)uploadTargetCatalog.names.set(p.id,p.name);renderUploads();
}
uploadTargetSearch.oninput=()=>{void loadUploadTargets().catch(error=>setStatus('目标歌单读取失败：'+error.message,'error'))};
function renderUploadTargets(select){
 if(uploadTargetCatalog.owner!==state.user?.id){uploadTargetCatalog.owner=state.user?.id;uploadTargetCatalog.generation++;uploadTargetCatalog.loaded=false;uploadTargetCatalog.items=[];uploadTargetCatalog.names.clear();uploadTargetCatalog.selected='';uploadTargetCatalog.hasSelection=false;uploadTargetSearch.value='';uploadCreateDialog?.close()}
 const chosen=uploadTargetCatalog.hasSelection?uploadTargetCatalog.selected:select.value==='__mixed__'?'':select.value||uploadTargetCatalog.selected,targets=[...new Set(state.uploads.filter(uploadFlowPending).map(u=>u.targetPlaylistID||''))];
 select.replaceChildren(new Option('仅加入曲库',''));
 const choices=new Map();for(const p of [...state.playlists,...uploadTargetCatalog.items])if(!p.system&&!p.read_only&&p.owner_id===state.user?.id){choices.set(p.id,p);uploadTargetCatalog.names.set(p.id,p.name)}
 for(const p of choices.values())select.add(new Option(p.name,p.id));
 const selected=uploadTargetCatalog.hasSelection?chosen:chosen||targets.length===1&&targets[0]||'';
 if(selected&&!choices.has(selected))select.add(new Option(uploadTargetCatalog.names.get(selected)||'已选目标歌单（待重新读取）',selected));
 if(targets.length>1&&!chosen&&!uploadTargetCatalog.hasSelection){select.add(new Option('各任务保留原目标（修改此项仅影响未开始项）','__mixed__'));select.value='__mixed__'}else select.value=selected;
 uploadTargetCatalog.selected=chosen;
 uploadTargetPages.replaceChildren();for(const[label,delta]of [['上一组歌单',-50],['下一组歌单',50]]){const b=document.createElement('button');b.type='button';b.className='btn small';b.textContent=label;b.disabled=delta<0?uploadTargetCatalog.offset<=0:uploadTargetCatalog.offset+50>=uploadTargetCatalog.total;b.onclick=()=>run(()=>loadUploadTargets(uploadTargetCatalog.offset+delta));uploadTargetPages.append(b)}
 if(state.page==='upload'&&!uploadTargetCatalog.loaded){uploadTargetCatalog.loaded=true;void loadUploadTargets().catch(error=>{if(uploadTargetCatalog.owner===state.user?.id)setStatus('目标歌单读取失败，可输入搜索重试：'+error.message,'error')})}
}
const uploadPager=document.createElement('div');uploadPager.id='uploadQueuePager';uploadPager.className='browse-pager';$('uploadList').after(uploadPager);

function visibleUploadItems(){
 const items=state.uploads.filter(u=>uploadListFilter==='all'||uploadListFilter==='done'&&!uploadFlowPending(u)||uploadListFilter==='pending'&&uploadFlowPending(u)||uploadListFilter==='failed'&&(u.status==='失败'||u.assignmentError));
 uploadListPage=Math.min(uploadListPage,Math.max(0,Math.ceil(items.length/uploadPageSize)-1));
 return items.slice(uploadListPage*uploadPageSize,(uploadListPage+1)*uploadPageSize);
}
function renderUploadPager(){
 const count=state.uploads.filter(u=>uploadListFilter==='all'||uploadListFilter==='done'&&!uploadFlowPending(u)||uploadListFilter==='pending'&&uploadFlowPending(u)||uploadListFilter==='failed'&&(u.status==='失败'||u.assignmentError)).length;
 uploadPager.replaceChildren();
 for(const[label,delta]of [['上一页',-1],['下一页',1]]){const b=document.createElement('button');b.className='btn small';b.textContent=label;b.disabled=delta<0?uploadListPage===0:(uploadListPage+1)*uploadPageSize>=count;b.onclick=()=>{uploadListPage+=delta;renderUploads()};uploadPager.append(b)}
 const caption=document.createElement('span');caption.className='status';caption.textContent=count?'第 '+(uploadListPage*uploadPageSize+1)+'–'+Math.min(count,(uploadListPage+1)*uploadPageSize)+' 项 / 共 '+count+' 项':'暂无匹配任务';uploadPager.append(caption);
}

const uploadBatchParseOriginal=parseLocalUpload;
function pumpUploadParsing(){
 while(uploadParseActive<2&&uploadParseQueue.length){const job=uploadParseQueue.shift();uploadParseActive++;
  const finish=(callback,value)=>{uploadParseTasks.delete(job.upload);uploadParseActive--;pumpUploadParsing();callback(value)};
  Promise.resolve().then(()=>{if(!state.uploads.includes(job.upload)||job.upload.needsSourceFile)return null;const result=uploadBatchParseOriginal(job.upload);uploadParseGenerations.set(job.upload,job.upload.localGeneration);return result}).then(value=>finish(job.resolve,value),error=>finish(job.reject,error));
 }
}
parseLocalUpload=function(upload){if(uploadParseTasks.has(upload)){const pending=uploadParseTasks.get(upload);if(uploadParseGenerations.get(upload)!==upload.localGeneration)return pending.then(()=>state.uploads.includes(upload)?parseLocalUpload(upload):null);return pending}const promise=new Promise((resolve,reject)=>uploadParseQueue.push({upload,resolve,reject}));uploadParseTasks.set(upload,promise);uploadParseGenerations.set(upload,upload.localGeneration);pumpUploadParsing();return promise};

async function assignUploadResults(items,owner=state.user?.id,epoch=resumeWorkspaceEpoch){
 const groups=new Map();
 for(const u of items){if(u.targetNeedsSelection||!u.commitResult?.track?.id||!u.targetPlaylistID||u.playlistAssigned)continue;if(!groups.has(u.targetPlaylistID))groups.set(u.targetPlaylistID,[]);groups.get(u.targetPlaylistID).push(u)}
 for(const[id,uploads]of groups){
  for(let i=0;i<uploads.length;i+=100){if(!uploadFlowCurrent(owner,epoch))return;const batch=uploads.slice(i,i+100);
   try{for(let retry=0;;retry++){try{const result=await api('playlists/'+encodeURIComponent(id));if(!uploadFlowCurrent(owner,epoch))return;const p=result.playlist||result;if(p.read_only||p.system||p.owner_id!==owner)throw new Error('目标歌单已删除或不可编辑');await api('playlists/'+encodeURIComponent(id)+'/tracks','POST',{track_ids:[...new Set(batch.map(u=>u.commitResult.track.id))]},undefined,p.version);break}catch(error){if(retry===0&&error.status===409)continue;throw error}}
    if(!uploadFlowCurrent(owner,epoch))return;for(const u of batch){u.playlistAssigned=true;u.assignmentError='';u.error=''}
   }catch(error){if(!uploadFlowCurrent(owner,epoch))return;for(const u of batch){u.assignmentError=error.message;u.error='已入库，加入目标歌单失败：'+error.message}}
  }
 }
 if(uploadFlowCurrent(owner,epoch)){await persistResumeQueue();renderUploads()}
}

const uploadBatchStart=startUploadFlow;
startUploadFlow=async function(items=state.uploads.filter(uploadFlowPending)){
 if(uploadFlowRun)return uploadFlowRun;
 const owner=state.user?.id,epoch=resumeWorkspaceEpoch,target=$('uploadTargetPlaylist').value==='__mixed__'?'':$('uploadTargetPlaylist').value;
 for(const u of items)if(u.targetPlaylistID===undefined)u.targetPlaylistID=target;
 await uploadBatchStart(items);if(uploadFlowCurrent(owner,epoch))await assignUploadResults(items,owner,epoch);
};
const uploadBatchConfirm=confirmUpload;
confirmUpload=async function(upload){const owner=state.user?.id,epoch=resumeWorkspaceEpoch;lockUploadTarget(upload);if(upload.resumeId&&upload.resumeTask?.target_playlist_id==null){await resumeJSON('/'+upload.resumeId+'/finish','POST',{target_playlist_id:upload.targetPlaylistID||''},owner,epoch);if(!uploadFlowCurrent(owner,epoch))return}const result=await uploadBatchConfirm(upload);if(uploadFlowCurrent(owner,epoch)&&!state.uploadBusy)await assignUploadResults([upload],owner,epoch);return result};
function lockUploadTarget(upload){if(upload.targetNeedsSelection)throw new Error('恢复任务缺少原目标，请先确认上传到哪个歌单，或明确选择“仅加入曲库”。');if(upload.targetLocked)return;if(upload.targetPlaylistID===undefined)upload.targetPlaylistID=$('uploadTargetPlaylist').value==='__mixed__'?'':$('uploadTargetPlaylist').value;upload.targetLocked=true;void persistResumeQueue()}
const uploadBatchResumeStage=resumeStageUpload;
resumeStageUpload=async function(upload){lockUploadTarget(upload);return uploadBatchResumeStage(upload)};
const uploadBatchConfirmAll=confirmUploads;
confirmUploads=async function(){const owner=state.user?.id,epoch=resumeWorkspaceEpoch,items=state.uploads.slice();await uploadBatchConfirmAll();if(uploadFlowCurrent(owner,epoch))await assignUploadResults(items,owner,epoch)};
$('retryUploads').onclick=()=>run(async()=>{const owner=state.user?.id,epoch=resumeWorkspaceEpoch;await assignUploadResults(state.uploads,owner,epoch);if(uploadFlowCurrent(owner,epoch))await startUploadFlow(state.uploads.filter(u=>u.status==='失败'))});
$('uploadTargetPlaylist').onchange=()=>{const target=$('uploadTargetPlaylist').value;if(target!=='__mixed__')setUploadTarget(target)};
$('applyUploadMetadata').onclick=()=>{if(state.uploadBusy||state.uploads.some(u=>u.inFlight))return;for(const upload of state.uploads.filter(uploadFlowPending)){for(const[key,id]of [['artist','uploadBatchArtist'],['album','uploadBatchAlbum']]){const value=$(id).value.trim();if(value){upload[key]=value;upload.edits[key]=value}}}void persistResumeQueue();renderUploads()};
$('uploadQueueFilter').onchange=()=>{uploadListFilter=$('uploadQueueFilter').value;uploadListPage=0;renderUploads()};

const uploadBatchRender=renderUploads;
renderUploads=function(){
 if(uploadBatchOwner!==state.user?.id){uploadBatchOwner=state.user?.id;uploadListPage=0;uploadListFilter='all';$('uploadQueueFilter').value='all';$('uploadBatchArtist').value='';$('uploadBatchAlbum').value='';$('uploadTargetPlaylist').value=''}
 uploadBatchRender();
 const select=$('uploadTargetPlaylist');renderUploadTargets(select);
 const busy=Boolean(state.uploadBusy||state.uploads.some(u=>u.inFlight));for(const id of ['uploadBatchArtist','uploadBatchAlbum','applyUploadMetadata'])$(id).disabled=busy;
 $('retryUploads').disabled=busy||!state.uploads.some(u=>u.status==='失败'||u.assignmentError);
 const assignmentFailures=state.uploads.filter(u=>u.assignmentError).length;if(assignmentFailures)$('uploadFlowSummary').textContent+=' · 歌单待重试 '+assignmentFailures;
 const target=(select.value==='__mixed__'?'':select.value)||state.uploads.find(u=>u.targetPlaylistID&&u.playlistAssigned)?.targetPlaylistID,link=$('openUploadPlaylist');link.hidden=!target;if(target)link.href='#/playlists/'+encodeURIComponent(target);
 for(const u of visibleUploadItems()){if(!u.assignmentError)continue;const row=document.querySelector('[data-upload-id="'+u.id+'"] .upload-meta');if(row){const note=document.createElement('p');note.className='status error';note.textContent='音频已入库，仅歌单添加待重试。';row.append(note)}}
 for(const u of visibleUploadItems()){if(u.targetPlaylistID===undefined&&uploadTargetMutable(u))u.targetPlaylistID=select.value==='__mixed__'?'':select.value;const row=document.querySelector('[data-upload-id="'+u.id+'"] .upload-meta');if(!row)continue;const note=document.createElement('p');note.className='status upload-task-target';note.textContent='上传到：'+(u.targetPlaylistID?uploadTargetCatalog.names.get(u.targetPlaylistID)||'已选歌单':'仅加入曲库')+(uploadTargetMutable(u)?'':' · 目标已固定');row.append(note);if(u.assignmentError){const change=document.createElement('button');change.type='button';change.className='btn small';change.textContent='改为当前目标并重试';change.disabled=busy;change.onclick=()=>run(()=>retryUploadAssignment(u,select.value));row.append(change)}}
 for(const u of visibleUploadItems().filter(u=>u.targetNeedsSelection)){const row=document.querySelector('[data-upload-id="'+u.id+'"] .upload-meta');if(!row)continue;const note=row.querySelector('.upload-task-target');if(note)note.textContent='旧任务未保存原目标，请选择并确认上传位置。';const confirm=makeButton('确认使用当前目标',()=>confirmUploadTarget(u,select.value),'btn small');confirm.disabled=busy||select.value==='__mixed__';row.append(confirm)}
 renderUploadPager();
};
renderUploads();
