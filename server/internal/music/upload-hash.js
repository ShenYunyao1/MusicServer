'use strict';

// One worker, at most four retained 4 MiB blocks. Never transfer the caller's
// ArrayBuffer: the uploader still needs it to send the original bytes.
const uploadHashQueue=[];
let uploadHashWorker=null,uploadHashWorkerFailed=false,uploadHashActive=null,uploadHashSequence=0;

async function uploadHashFallback(bytes){
 if(globalThis.crypto?.subtle){
  try{return Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',bytes)),value=>value.toString(16).padStart(2,'0')).join('')}catch(_){}
 }
 if(typeof resumeSHA256Fallback==='function')return resumeSHA256Fallback(bytes);
 throw new Error('无法计算文件摘要，请重试上传');
}

function uploadHashComplete(task,error,hash){
 if(uploadHashActive!==task)return;
 clearTimeout(task.timer);uploadHashActive=null;
 if(error)task.reject(error);else task.resolve(hash);
 task.bytes=null;uploadHashPump();
}

function uploadHashFailWorker(){
 uploadHashWorkerFailed=true;uploadHashWorker?.terminate();uploadHashWorker=null;
 const task=uploadHashActive;
 if(task){clearTimeout(task.timer);uploadHashFallback(task.bytes).then(hash=>uploadHashComplete(task,null,hash),error=>uploadHashComplete(task,error))}
}

function uploadHashPump(){
 if(uploadHashActive||!uploadHashQueue.length)return;
 const task=uploadHashActive=uploadHashQueue.shift();
 if(!uploadHashWorkerFailed&&!uploadHashWorker){
  try{
   uploadHashWorker=new Worker('/assets/upload-hash-worker.js');
   uploadHashWorker.onmessage=event=>{
    const current=uploadHashActive,message=event.data;
    if(!current||message?.id!==current.id)return;
    if(message.error||typeof message.hash!=='string'||!/^[0-9a-f]{64}$/.test(message.hash)){uploadHashFailWorker();return}
    uploadHashComplete(current,null,message.hash);
   };
   uploadHashWorker.onerror=event=>{event.preventDefault?.();uploadHashFailWorker()};
   uploadHashWorker.onmessageerror=uploadHashFailWorker;
  }catch(_){uploadHashWorkerFailed=true}
 }
 if(!uploadHashWorker){uploadHashFallback(task.bytes).then(hash=>uploadHashComplete(task,null,hash),error=>uploadHashComplete(task,error));return}
 task.timer=setTimeout(uploadHashFailWorker,30000);
 try{uploadHashWorker.postMessage({id:task.id,bytes:task.bytes})}catch(_){uploadHashFailWorker()}
}

function uploadHash(bytes){
 if(!(bytes instanceof ArrayBuffer)||bytes.byteLength>4*1048576)return Promise.reject(Object.assign(new Error('摘要分片不得超过4 MiB'),{code:'UPLOAD_HASH_SIZE'}));
 if(uploadHashQueue.length+(uploadHashActive?1:0)>=4)return Promise.reject(Object.assign(new Error('摘要计算繁忙，请稍后重试'),{code:'UPLOAD_HASH_BUSY'}));
 return new Promise((resolve,reject)=>{uploadHashQueue.push({id:++uploadHashSequence,bytes,resolve,reject});uploadHashPump()});
}
