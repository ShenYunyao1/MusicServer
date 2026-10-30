'use strict';

let uploadListPage=0,uploadListFilter='all',uploadBatchOwner=null;
const uploadPageSize=50,uploadParseQueue=[],uploadParseTasks=new WeakMap(),uploadParseGenerations=new WeakMap();let uploadParseActive=0;
const uploadBatchControls=document.createElement('div');uploadBatchControls.className='upload-batch toolbar';
uploadBatchControls.innerHTML='<label>目标歌单 <select id="uploadTargetPlaylist" class="select"><option value="">仅加入曲库</option></select></label><input id="uploadBatchArtist" placeholder="批量设置作者" aria-label="批量设置作者"><input id="uploadBatchAlbum" placeholder="批量设置专辑" aria-label="批量设置专辑"><button id="applyUploadMetadata" class="btn">应用到未完成项</button><label>显示 <select id="uploadQueueFilter" class="select"><option value="all">全部任务</option><option value="pending">未完成</option><option value="failed">失败及待重试</option><option value="done">已完成</option></select></label><a id="openUploadPlaylist" hidden>打开目标歌单</a>';
uploadFlowControls.after(uploadBatchControls);
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
 for(const u of items){if(!u.commitResult?.track?.id||!u.targetPlaylistID||u.playlistAssigned)continue;if(!groups.has(u.targetPlaylistID))groups.set(u.targetPlaylistID,[]);groups.get(u.targetPlaylistID).push(u)}
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
 const owner=state.user?.id,epoch=resumeWorkspaceEpoch,target=$('uploadTargetPlaylist').value;
 for(const u of items)if(!u.targetPlaylistID)u.targetPlaylistID=target;
 await uploadBatchStart(items);if(uploadFlowCurrent(owner,epoch))await assignUploadResults(items,owner,epoch);
};
const uploadBatchConfirm=confirmUpload;
confirmUpload=async function(upload){const owner=state.user?.id,epoch=resumeWorkspaceEpoch;if(!upload.targetPlaylistID)upload.targetPlaylistID=$('uploadTargetPlaylist').value;const result=await uploadBatchConfirm(upload);if(uploadFlowCurrent(owner,epoch)&&!state.uploadBusy)await assignUploadResults([upload],owner,epoch);return result};
const uploadBatchConfirmAll=confirmUploads;
confirmUploads=async function(){const owner=state.user?.id,epoch=resumeWorkspaceEpoch,items=state.uploads.slice();await uploadBatchConfirmAll();if(uploadFlowCurrent(owner,epoch))await assignUploadResults(items,owner,epoch)};
$('retryUploads').onclick=()=>run(async()=>{const owner=state.user?.id,epoch=resumeWorkspaceEpoch;await assignUploadResults(state.uploads,owner,epoch);if(uploadFlowCurrent(owner,epoch))await startUploadFlow(state.uploads.filter(u=>u.status==='失败'))});
$('uploadTargetPlaylist').onchange=()=>{const target=$('uploadTargetPlaylist').value;for(const u of state.uploads.filter(uploadFlowPending))u.targetPlaylistID=target;void persistResumeQueue();renderUploads()};
$('applyUploadMetadata').onclick=()=>{if(state.uploadBusy||state.uploads.some(u=>u.inFlight))return;for(const upload of state.uploads.filter(uploadFlowPending)){for(const[key,id]of [['artist','uploadBatchArtist'],['album','uploadBatchAlbum']]){const value=$(id).value.trim();if(value){upload[key]=value;upload.edits[key]=value}}}void persistResumeQueue();renderUploads()};
$('uploadQueueFilter').onchange=()=>{uploadListFilter=$('uploadQueueFilter').value;uploadListPage=0;renderUploads()};

const uploadBatchRender=renderUploads;
renderUploads=function(){
 if(uploadBatchOwner!==state.user?.id){uploadBatchOwner=state.user?.id;uploadListPage=0;uploadListFilter='all';$('uploadQueueFilter').value='all';$('uploadBatchArtist').value='';$('uploadBatchAlbum').value='';$('uploadTargetPlaylist').value=''}
 uploadBatchRender();
 const select=$('uploadTargetPlaylist'),chosen=select.value;
 select.replaceChildren(new Option('仅加入曲库',''));
 for(const p of state.playlists.filter(p=>!p.system&&!p.read_only&&p.owner_id===state.user?.id)){select.add(new Option(p.name,p.id))}
 select.value=[...select.options].some(o=>o.value===chosen)?chosen:'';
 const busy=Boolean(state.uploadBusy||state.uploads.some(u=>u.inFlight));for(const id of ['uploadTargetPlaylist','uploadBatchArtist','uploadBatchAlbum','applyUploadMetadata'])$(id).disabled=busy;
 $('retryUploads').disabled=busy||!state.uploads.some(u=>u.status==='失败'||u.assignmentError);
 const assignmentFailures=state.uploads.filter(u=>u.assignmentError).length;if(assignmentFailures)$('uploadFlowSummary').textContent+=' · 歌单待重试 '+assignmentFailures;
 const target=select.value||state.uploads.find(u=>u.targetPlaylistID&&u.playlistAssigned)?.targetPlaylistID,link=$('openUploadPlaylist');link.hidden=!target;if(target)link.href='#/playlists/'+encodeURIComponent(target);
 for(const u of visibleUploadItems()){if(!u.assignmentError)continue;const row=document.querySelector('[data-upload-id="'+u.id+'"] .upload-meta');if(row){const note=document.createElement('p');note.className='status error';note.textContent='音频已入库，仅歌单添加待重试。';row.append(note)}}
 renderUploadPager();
};
renderUploads();
