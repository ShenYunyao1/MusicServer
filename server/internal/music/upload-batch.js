'use strict';

let uploadListPage=0,uploadListFilter='all',uploadBatchOwner=null;
const uploadTargetCatalog={owner:null,generation:0,loaded:false,items:[],offset:0,total:0,selected:'',names:new Map()};
const uploadPageSize=50,uploadParseQueue=[],uploadParseTasks=new WeakMap(),uploadParseGenerations=new WeakMap();let uploadParseActive=0;
const uploadBatchControls=document.createElement('div');uploadBatchControls.className='upload-batch toolbar';
uploadBatchControls.innerHTML='<label>目标歌单 <select id="uploadTargetPlaylist" class="select"><option value="">仅加入曲库</option></select></label><input id="uploadBatchArtist" placeholder="批量设置作者" aria-label="批量设置作者"><input id="uploadBatchAlbum" placeholder="批量设置专辑" aria-label="批量设置专辑"><button id="applyUploadMetadata" class="btn">应用到未完成项</button><label>显示 <select id="uploadQueueFilter" class="select"><option value="all">全部任务</option><option value="pending">未完成</option><option value="failed">失败及待重试</option><option value="done">已完成</option></select></label><a id="openUploadPlaylist" hidden>打开目标歌单</a>';
uploadFlowControls.after(uploadBatchControls);
const uploadTargetSearch=document.createElement('input');uploadTargetSearch.id='uploadTargetSearch';uploadTargetSearch.placeholder='搜索目标歌单';uploadTargetSearch.setAttribute('aria-label','搜索目标歌单');uploadBatchControls.append(uploadTargetSearch);
const uploadTargetPages=document.createElement('div');uploadTargetPages.className='actions';uploadTargetPages.id='uploadTargetPages';uploadBatchControls.append(uploadTargetPages);
async function loadUploadTargets(offset=0){
 const owner=state.user?.id,generation=++uploadTargetCatalog.generation;if(!owner)return;
 const page=await api('browse/playlists?'+new URLSearchParams({q:uploadTargetSearch.value.trim(),offset:String(offset),limit:'50',summary_only:'1'}));
 if(owner!==state.user?.id||owner!==uploadTargetCatalog.owner||generation!==uploadTargetCatalog.generation)return;
 uploadTargetCatalog.loaded=true;uploadTargetCatalog.items=page.items;uploadTargetCatalog.offset=page.offset;uploadTargetCatalog.total=page.total;
 for(const p of page.items)uploadTargetCatalog.names.set(p.id,p.name);renderUploads();
}
uploadTargetSearch.oninput=()=>{void loadUploadTargets().catch(error=>setStatus('目标歌单读取失败：'+error.message,'error'))};
function renderUploadTargets(select){
 if(uploadTargetCatalog.owner!==state.user?.id){uploadTargetCatalog.owner=state.user?.id;uploadTargetCatalog.generation++;uploadTargetCatalog.loaded=false;uploadTargetCatalog.items=[];uploadTargetCatalog.names.clear();uploadTargetCatalog.selected='';uploadTargetSearch.value=''}
 const chosen=select.value||uploadTargetCatalog.selected,targets=[...new Set(state.uploads.filter(uploadFlowPending).map(u=>u.targetPlaylistID||''))];
 select.replaceChildren(new Option('仅加入曲库',''));
 const choices=new Map();for(const p of [...state.playlists,...uploadTargetCatalog.items])if(!p.system&&!p.read_only&&p.owner_id===state.user?.id){choices.set(p.id,p);uploadTargetCatalog.names.set(p.id,p.name)}
 for(const p of choices.values())select.add(new Option(p.name,p.id));
 const selected=chosen||targets.length===1&&targets[0]||'';
 if(selected&&!choices.has(selected))select.add(new Option(uploadTargetCatalog.names.get(selected)||'已选目标歌单（待重新读取）',selected));
 if(targets.length>1&&!chosen){select.add(new Option('各任务保留原目标（修改此项可统一）','__mixed__'));select.value='__mixed__'}else select.value=selected;
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
 const owner=state.user?.id,epoch=resumeWorkspaceEpoch,target=$('uploadTargetPlaylist').value==='__mixed__'?'':$('uploadTargetPlaylist').value;
 for(const u of items)if(!u.targetPlaylistID)u.targetPlaylistID=target;
 await uploadBatchStart(items);if(uploadFlowCurrent(owner,epoch))await assignUploadResults(items,owner,epoch);
};
const uploadBatchConfirm=confirmUpload;
confirmUpload=async function(upload){const owner=state.user?.id,epoch=resumeWorkspaceEpoch;if(!upload.targetPlaylistID&&$('uploadTargetPlaylist').value!=='__mixed__')upload.targetPlaylistID=$('uploadTargetPlaylist').value;const result=await uploadBatchConfirm(upload);if(uploadFlowCurrent(owner,epoch)&&!state.uploadBusy)await assignUploadResults([upload],owner,epoch);return result};
const uploadBatchConfirmAll=confirmUploads;
confirmUploads=async function(){const owner=state.user?.id,epoch=resumeWorkspaceEpoch,items=state.uploads.slice();await uploadBatchConfirmAll();if(uploadFlowCurrent(owner,epoch))await assignUploadResults(items,owner,epoch)};
$('retryUploads').onclick=()=>run(async()=>{const owner=state.user?.id,epoch=resumeWorkspaceEpoch;await assignUploadResults(state.uploads,owner,epoch);if(uploadFlowCurrent(owner,epoch))await startUploadFlow(state.uploads.filter(u=>u.status==='失败'))});
$('uploadTargetPlaylist').onchange=()=>{const target=$('uploadTargetPlaylist').value;if(target==='__mixed__')return;uploadTargetCatalog.selected=target;for(const u of state.uploads.filter(uploadFlowPending))u.targetPlaylistID=target;void persistResumeQueue();renderUploads()};
$('applyUploadMetadata').onclick=()=>{if(state.uploadBusy||state.uploads.some(u=>u.inFlight))return;for(const upload of state.uploads.filter(uploadFlowPending)){for(const[key,id]of [['artist','uploadBatchArtist'],['album','uploadBatchAlbum']]){const value=$(id).value.trim();if(value){upload[key]=value;upload.edits[key]=value}}}void persistResumeQueue();renderUploads()};
$('uploadQueueFilter').onchange=()=>{uploadListFilter=$('uploadQueueFilter').value;uploadListPage=0;renderUploads()};

const uploadBatchRender=renderUploads;
renderUploads=function(){
 if(uploadBatchOwner!==state.user?.id){uploadBatchOwner=state.user?.id;uploadListPage=0;uploadListFilter='all';$('uploadQueueFilter').value='all';$('uploadBatchArtist').value='';$('uploadBatchAlbum').value='';$('uploadTargetPlaylist').value=''}
 uploadBatchRender();
 const select=$('uploadTargetPlaylist');renderUploadTargets(select);
 const busy=Boolean(state.uploadBusy||state.uploads.some(u=>u.inFlight));for(const id of ['uploadTargetPlaylist','uploadBatchArtist','uploadBatchAlbum','applyUploadMetadata'])$(id).disabled=busy;
 $('retryUploads').disabled=busy||!state.uploads.some(u=>u.status==='失败'||u.assignmentError);
 const assignmentFailures=state.uploads.filter(u=>u.assignmentError).length;if(assignmentFailures)$('uploadFlowSummary').textContent+=' · 歌单待重试 '+assignmentFailures;
 const target=(select.value==='__mixed__'?'':select.value)||state.uploads.find(u=>u.targetPlaylistID&&u.playlistAssigned)?.targetPlaylistID,link=$('openUploadPlaylist');link.hidden=!target;if(target)link.href='#/playlists/'+encodeURIComponent(target);
 for(const u of visibleUploadItems()){if(!u.assignmentError)continue;const row=document.querySelector('[data-upload-id="'+u.id+'"] .upload-meta');if(row){const note=document.createElement('p');note.className='status error';note.textContent='音频已入库，仅歌单添加待重试。';row.append(note)}}
 renderUploadPager();
};
renderUploads();
