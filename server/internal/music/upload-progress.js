'use strict';

function uploadDuration(ms){return (Math.max(0,Number(ms)||0)/1000).toFixed(1)+' 秒'}
function uploadProgressMarkup(upload){
 const p=upload.progress;if(!p)return '';
 const percent=p.total>0?Math.min(100,Math.floor(p.loaded*100/p.total)):0;
 if(p.phase==='sending')return '<progress max="100" value="'+percent+'" aria-label="上传进度"></progress><span>'+percent+'% · '+formatBytes(p.loaded)+' / '+(p.total?formatBytes(p.total):'计算中')+' · '+formatBytes(p.rate||0)+'/s</span>';
 if(p.phase==='waiting')return '<progress aria-label="等待服务器校验"></progress><span>文件已发送，等待服务器校验结果 · '+uploadDuration(performance.now()-p.waitStarted)+'</span>';
 if(p.phase==='complete'){
  const timings=upload.preview?.timings||{};
  return '<span>发送耗时 '+uploadDuration(p.sendMS)+' · 服务器接收 '+uploadDuration(timings.receive_ms)+' · 校验 '+uploadDuration(timings.validation_ms??Math.max(0,(timings.total_ms||0)-(timings.receive_ms||0)))+'</span>';
 }
 return '<span>传输未完成 · 已发送 '+formatBytes(p.loaded)+'</span>';
}
function updateUploadProgress(upload){
 const row=document.querySelector('[data-upload-id="'+upload.id+'"]');if(!row)return;
 const element=row.querySelector('[data-upload-progress]');if(element)element.innerHTML=uploadProgressMarkup(upload);
 const badge=row.querySelector('.upload-status .badge');if(badge)badge.textContent=upload.status;
}
// Upload requests have their own connection: waiting for a large cover download
// in the management request queue must never delay sending a selected file.
function uploadRequest(path,method,form,upload){
 return new Promise((resolve,reject)=>{
  const xhr=new XMLHttpRequest(),started=performance.now();let timer;
  upload.transfer=xhr;upload.progress={phase:'sending',loaded:0,total:0,rate:0};upload.status='上传中';updateUploadProgress(upload);
  xhr.open(method,'/api/v2/'+path);xhr.withCredentials=true;if(state.csrf)xhr.setRequestHeader('X-CSRF-Token',state.csrf);
  xhr.upload.onprogress=event=>{const p=upload.progress;p.loaded=event.loaded;p.total=event.lengthComputable?event.total:0;p.rate=event.loaded/Math.max(.001,(performance.now()-started)/1000);updateUploadProgress(upload)};
  xhr.upload.onload=()=>{const p=upload.progress;p.phase='waiting';p.sendMS=performance.now()-started;p.waitStarted=performance.now();if(p.total)p.loaded=p.total;upload.status='等待校验';updateUploadProgress(upload);timer=setInterval(()=>updateUploadProgress(upload),500)};
  xhr.onload=()=>{
   let payload;try{payload=xhr.responseText?JSON.parse(xhr.responseText):null}catch(_){upload.progress.phase='failed';reject(new Error('服务端返回了无效的上传响应'));return}
   if(xhr.status<200||xhr.status>=300){const error=new Error('HTTP '+xhr.status+' · '+(payload?.error?.message||'上传失败'));error.status=xhr.status;upload.progress.phase='failed';upload.transfer=null;if(xhr.status===401)setLoggedOut();reject(error);return}
   upload.progress.phase='complete';upload.progress.sendMS??=performance.now()-started;resolve(payload);
  };
  xhr.onerror=()=>{upload.progress.phase='failed';reject(new Error('上传连接中断，请检查网络后重试'))};
  xhr.onabort=()=>{upload.progress.phase='failed';reject(new Error('上传已取消'))};
  xhr.onloadend=()=>{clearInterval(timer);upload.transfer=null;updateUploadProgress(upload)};
  xhr.send(form);
 });
}
