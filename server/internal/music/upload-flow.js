'use strict';

// Page navigation never owns the queue. Only the account workspace epoch does.
let uploadFlowRun=null,uploadFlowOwner=null;
const uploadFlowControls=document.createElement('div');uploadFlowControls.className='upload-flow-controls toolbar';
uploadFlowControls.innerHTML='<label class="email-toggle"><input id="uploadReview" type="checkbox"> 入库前确认</label><label>同时传输 <select id="uploadConcurrency" class="select" aria-label="同时传输文件数"><option value="1">1 个文件</option><option value="2">2 个文件</option></select></label><button id="retryUploads" class="btn">重试失败项</button><span id="uploadFlowSummary" class="status" role="status"></span><p id="uploadPreflightNotice" class="status"></p>';
$('uploadZone').after(uploadFlowControls);

function uploadFlowCurrent(owner,epoch){return state.user?.id===owner&&resumeWorkspaceEpoch===epoch}
function uploadFlowPending(upload){return !['完成','已去重'].includes(upload.status)&&!upload.resumeCanceled}
async function commitUploadWithReceipt(id,payload,current){
 try{return await api('uploads/stage/'+encodeURIComponent(id)+'/commit','POST',payload)}catch(error){
  if(!current()||[401,403].includes(error.status))throw error;
  try{return await api('uploads/receipts/'+encodeURIComponent(id))}catch(_){throw error}
 }
}
function uploadFlowSummary(){
 const counts={added:0,reused:0,failed:0,review:0};
 for(const u of state.uploads){if(u.status==='完成')counts.added++;if(u.status==='已去重')counts.reused++;if(u.status==='失败')counts.failed++;if(u.stageId&&u.status==='待确认')counts.review++}
 return '新增 '+counts.added+' · 复用 '+counts.reused+' · 待确认 '+counts.review+' · 失败 '+counts.failed;
}
async function startUploadFlow(items=state.uploads.filter(uploadFlowPending)){
 if(uploadFlowRun)return uploadFlowRun;
 const owner=state.user?.id,epoch=resumeWorkspaceEpoch,review=$('uploadReview').checked,concurrency=$('uploadConcurrency').value==='2'?2:1;
 if(!owner||state.user.upload_allowed===false)throw new Error('请先完成邮箱验证，再上传');
 const queue=items.filter(uploadFlowPending);if(!queue.length)return;
 state.uploadBusy=true;renderUploads();
 const run=(async()=>{try{
  const policy=await api('uploads/preflight');if(!uploadFlowCurrent(owner,epoch))return;
  if(!policy.upload_allowed)throw new Error('请先完成邮箱验证，再上传');
  $('uploadPreflightNotice').textContent=policy.limit_bytes&&queue.reduce((n,u)=>n+(u.stageId?0:u.file.size),0)>policy.limit_bytes-policy.used_bytes?'剩余额度可能不足，重复曲目仍可校验复用；新增曲目以入库时额度检查为准。':'';
  let active=0;const waiters=[],jobs=new Set();
  const acquire=async()=>{if(active>=concurrency)await new Promise(resolve=>waiters.push(resolve));active++;let released=false;return()=>{if(released)return;released=true;active--;waiters.shift()?.()}};
  const process=async(upload,release)=>{upload.flowReleaseTransfer=release;try{
   if(!uploadFlowCurrent(owner,epoch)||!state.uploads.includes(upload)||upload.resumeCanceled)return;
   if(policy.max_upload_bytes&&upload.file.size>policy.max_upload_bytes)throw new Error('文件超过服务器大小限制 '+formatBytes(policy.max_upload_bytes));
   if(!upload.stageId)await resumeStageUpload(upload);else release();
   if(!uploadFlowCurrent(owner,epoch)||!state.uploads.includes(upload)||upload.resumePaused||upload.resumeCanceled||!upload.stageId)return;
   if(!review)await confirmUpload(upload);
  }catch(error){if(uploadFlowCurrent(owner,epoch)&&!error.resumePaused){upload.status='失败';upload.error=error.message;renderUploads()}}finally{release();if(upload.flowReleaseTransfer===release)delete upload.flowReleaseTransfer}};
  for(const upload of queue){
   if(!uploadFlowCurrent(owner,epoch))break;
   const release=await acquire();if(!uploadFlowCurrent(owner,epoch)){release();break}
   const job=process(upload,release);jobs.add(job);job.finally(()=>jobs.delete(job));
   if(jobs.size>=concurrency+2)await Promise.race(jobs);
  }
  await Promise.all(jobs);
 }finally{if(uploadFlowCurrent(owner,epoch)){state.uploadBusy=false;await persistResumeQueue();renderUploads()}}})();
 uploadFlowRun=run;
 try{await run}finally{if(uploadFlowRun===run)uploadFlowRun=null}
}

uploadAll=()=>startUploadFlow();
confirmUploads=async function(){const owner=state.user?.id,epoch=resumeWorkspaceEpoch;state.uploadBusy=true;renderUploads();try{for(const upload of state.uploads.filter(u=>u.stageId&&['待确认','失败'].includes(u.status))){if(!uploadFlowCurrent(owner,epoch))break;try{await confirmUpload(upload)}catch(_){}}}finally{if(uploadFlowCurrent(owner,epoch)){state.uploadBusy=false;renderUploads()}}};
$('retryUploads').onclick=()=>run(()=>startUploadFlow(state.uploads.filter(u=>u.status==='失败')));
$('uploadReview').onchange=()=>{try{if(state.user)localStorage.setItem('upload-review/'+state.user.id,String($('uploadReview').checked))}catch(_){}renderUploads()};
const uploadFlowOriginalRender=renderUploads;
renderUploads=function(){
 if(uploadFlowOwner!==state.user?.id){uploadFlowOwner=state.user?.id;try{$('uploadReview').checked=Boolean(uploadFlowOwner)&&localStorage.getItem('upload-review/'+uploadFlowOwner)==='true'}catch(_){$('uploadReview').checked=false}}
 uploadFlowOriginalRender();
 const review=$('uploadReview').checked,busy=Boolean(state.uploadBusy||state.uploads.some(u=>u.inFlight));
 $('uploadAll').textContent=review?'上传并校验':'开始上传';$('uploadAll').classList.add('primary');
 $('uploadAll').disabled=busy||state.user?.upload_allowed===false||!state.uploads.some(uploadFlowPending);
 $('confirmUploads').hidden=!review&&!state.uploads.some(u=>u.stageId);
 $('retryUploads').disabled=busy||!state.uploads.some(u=>u.status==='失败');$('uploadReview').disabled=busy;$('uploadConcurrency').disabled=busy;
 $('uploadFlowSummary').textContent=uploadFlowSummary();
 for(const upload of state.uploads){
  const row=document.querySelector('[data-upload-id="'+upload.id+'"]');if(!row)continue;
  const start=row.querySelector('[data-stage-upload]');if(!start)continue;
  start.textContent=review?(upload.stageId?'重新校验':'上传校验'):(upload.stageId?'继续入库':'开始上传');
  start.onclick=()=>run(()=>review?stageUpload(upload):startUploadFlow([upload]));
  const resume=row.querySelector('[data-resume-continue]');if(resume&&!review)resume.onclick=()=>run(()=>startUploadFlow([upload]));
  const remove=row.querySelector('[data-remove-upload]');if(remove&&upload.status==='确认中')remove.disabled=true;
 }
};
renderUploads();
const uploadFlowClearWorkspace=clearResumeWorkspace;
clearResumeWorkspace=function(){uploadFlowClearWorkspace();uploadFlowRun=null;state.uploadBusy=false;$('uploadReview').checked=false};
