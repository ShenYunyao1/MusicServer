'use strict';

// Page navigation never owns the queue. Only the account workspace epoch does.
let uploadFlowRun=null,uploadFlowOwner=null;
const uploadFlowControls=document.createElement('div');uploadFlowControls.className='upload-flow-controls toolbar';
uploadFlowControls.innerHTML='<label class="email-toggle"><input id="uploadReview" type="checkbox"> 入库前确认</label><button id="retryUploads" class="btn">重试失败项</button><span id="uploadFlowSummary" class="status" role="status"></span>';
$('uploadZone').after(uploadFlowControls);

function uploadFlowCurrent(owner,epoch){return state.user?.id===owner&&resumeWorkspaceEpoch===epoch}
function uploadFlowPending(upload){return !['完成','已去重'].includes(upload.status)&&!upload.resumeCanceled}
function uploadFlowSummary(){
 const counts={added:0,reused:0,failed:0,review:0};
 for(const u of state.uploads){if(u.status==='完成')counts.added++;if(u.status==='已去重')counts.reused++;if(u.status==='失败')counts.failed++;if(u.stageId&&u.status==='待确认')counts.review++}
 return '新增 '+counts.added+' · 复用 '+counts.reused+' · 待确认 '+counts.review+' · 失败 '+counts.failed;
}
async function startUploadFlow(items=state.uploads.filter(uploadFlowPending)){
 if(uploadFlowRun)return uploadFlowRun;
 const owner=state.user?.id,epoch=resumeWorkspaceEpoch,review=$('uploadReview').checked;
 if(!owner||state.user.upload_allowed===false)throw new Error('请先完成邮箱验证，再上传');
 const queue=items.filter(uploadFlowPending);if(!queue.length)return;
 state.uploadBusy=true;renderUploads();
 const run=(async()=>{try{
  for(const upload of queue){
   if(!uploadFlowCurrent(owner,epoch))break;
   if(!state.uploads.includes(upload)||upload.resumeCanceled)continue;
   try{
    if(!upload.stageId)await resumeStageUpload(upload);
    if(!uploadFlowCurrent(owner,epoch))break;
    if(!state.uploads.includes(upload)||upload.resumePaused||upload.resumeCanceled||!upload.stageId)continue;
    if(!review)await confirmUpload(upload);
   }catch(error){if(!uploadFlowCurrent(owner,epoch))break;if(!error.resumePaused){upload.status='失败';upload.error=error.message;renderUploads()}}
  }
 }finally{if(uploadFlowCurrent(owner,epoch)){state.uploadBusy=false;await persistResumeQueue();renderUploads()}}})();
 uploadFlowRun=run;
 try{await run}finally{if(uploadFlowRun===run)uploadFlowRun=null}
}

uploadAll=()=>startUploadFlow();
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
 $('retryUploads').disabled=busy||!state.uploads.some(u=>u.status==='失败');$('uploadReview').disabled=busy;
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
