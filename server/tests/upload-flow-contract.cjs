'use strict';
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const dir=path.join(__dirname,'../internal/music');
const management=fs.readFileSync(path.join(dir,'management.js'),'utf8');
const source=fs.readFileSync(path.join(dir,'upload-flow.js'),'utf8');
function deferred(){let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return {promise,resolve,reject}}
async function until(check,label){for(let n=0;n<150;n++){if(check())return;await new Promise(resolve=>setImmediate(resolve))}throw new Error('Did not reach '+label)}
function method(name){const line=management.split(/\r?\n/).find(line=>line.startsWith('function '+name+'(')||line.startsWith('async function '+name+'('));assert.ok(line,'management method '+name);return line}
function upload(id){return {id,file:{name:id+'.wav',size:10},status:'待暂存',edits:{},error:''}}
function fixture(items){
 const elements=new Map(),rows=new Map(),calls=[],storage=new Map();let renders=0,logout=0;
 function element(){return {checked:false,disabled:false,hidden:false,textContent:'',innerHTML:'',classList:{add(){}},after(){},querySelector(){return null}}}
 const get=id=>{if(!elements.has(id))elements.set(id,element());return elements.get(id)};
 const context={console,FormData,Promise,Array,Map,Set,URLSearchParams,setTimeout,clearTimeout,setInterval,clearInterval,performance,Number,Date,
  state:{user:{id:'alice',upload_allowed:true},csrf:'alice-csrf',uploads:items,uploadBusy:false,tracks:[],playlists:[]},resumeWorkspaceEpoch:0,
  document:{createElement:element,getElementById:get,querySelector(selector){const id=selector.match(/data-upload-id="([^"]+)"/)?.[1];if(!id)return null;if(!rows.has(id)){const controls=new Map();rows.set(id,{querySelector(key){if(!controls.has(key))controls.set(key,element());return controls.get(key)}})}return rows.get(id)}},
  $:get,localStorage:{getItem:key=>storage.get(key)||null,setItem:(key,value)=>storage.set(key,value)},
  renderUploads(){renders++},persistResumeQueue:async()=>{},completeResumeUpload:async()=>{},refreshCore:async()=>{},discardStage:async u=>{u.stageId=null;u.preview=null},
  async resumeStageUpload(u){calls.push(['stage',u.id]);u.stageId='stage-'+u.id;u.status='待确认';return {id:u.stageId}},
  clearResumeWorkspace(){context.resumeWorkspaceEpoch++},setLoggedOut(){logout++;context.state.user=null},scheduleRequest:action=>action(),
  formatBytes:n=>String(n),commitFetch:async(url,options)=>{calls.push(['fetch',url,options]);return {ok:true,status:200,text:async()=>JSON.stringify({track:{id:'track'},duplicate:false})}},
  run:action=>action(),stageUpload:u=>context.resumeStageUpload(u),uploadAll:async()=>{},
 };
 context.fetch=(url,options)=>url==='/api/v2/uploads/preflight'?Promise.resolve({ok:true,status:200,text:async()=>JSON.stringify({upload_allowed:true,max_upload_bytes:512*1048576,used_bytes:0,limit_bytes:0})}):context.commitFetch(url,options);
 vm.createContext(context);vm.runInContext([method('api'),method('confirmUpload'),method('confirmUploads'),source].join('\n'),context);
 return {context,get,rows,calls,logout:()=>logout,renders:()=>renders};
}

async function main(){
 {
  const a=upload('one'),b=upload('two'),f=fixture([a,b]);await f.context.startUploadFlow();
  assert.deepEqual(f.calls.filter(c=>c[0]==='stage').map(c=>c[1]),['one','two']);assert.equal(a.status,'完成');assert.equal(b.status,'完成');assert.equal(f.context.state.uploadBusy,false);
  const before=f.calls.length;await f.context.startUploadFlow();assert.equal(f.calls.length,before,'completed tasks must not repeat');
 }
 {
  const a=upload('review'),f=fixture([a]);f.get('uploadReview').checked=true;await f.context.startUploadFlow();assert.equal(a.status,'待确认');assert.equal(f.calls.filter(c=>c[0]==='fetch').length,0);await f.context.confirmUploads();assert.equal(a.status,'完成');
 }
 {
  const a=upload('failure'),b=upload('continue'),f=fixture([a,b]);let fail=true;
  f.context.resumeStageUpload=async u=>{f.calls.push(['stage',u.id]);if(u===a&&fail)throw new Error('temporary failure');u.stageId='stage-'+u.id;u.status='待确认'};
  await f.context.startUploadFlow();assert.equal(a.status,'失败');assert.equal(b.status,'完成');fail=false;await f.get('retryUploads').onclick();assert.equal(a.status,'完成');assert.equal(f.calls.filter(c=>c[0]==='stage'&&c[1]==='continue').length,1);
 }
 {
  const a=upload('duplicate'),f=fixture([a]);f.context.commitFetch=async()=>({ok:true,status:200,text:async()=>JSON.stringify({track:{id:'existing'},duplicate:true})});await f.context.startUploadFlow();assert.equal(a.status,'已去重');assert.equal(a.commitResult.track.id,'existing');
 }
 {
  const a=upload('resume'),f=fixture([a]);f.context.renderUploads();await f.rows.get(a.id).querySelector('[data-resume-continue]').onclick();assert.equal(a.status,'完成','continue must preserve default automatic commit');
 }
 {
  const a=upload('cancel'),f=fixture([a]),stage=deferred();f.context.resumeStageUpload=async u=>{await stage.promise;u.stageId='stage-cancel';u.status='待确认'};
  const run=f.context.startUploadFlow();await until(()=>a.flowReleaseTransfer,'cancel upload started');a.resumeCanceled=true;a.resumePaused=true;stage.resolve();await run;assert.equal(f.calls.filter(c=>c[0]==='fetch').length,0,'canceled row must not commit after delayed validation');
 }
 {
  const a=upload('old'),f=fixture([a]),stage=deferred();f.context.resumeStageUpload=async u=>{await stage.promise;u.stageId='old-stage';u.status='待确认'};
  const old=f.context.startUploadFlow();f.context.clearResumeWorkspace();f.context.state.user={id:'bob',upload_allowed:true};f.context.state.uploads=[];f.context.state.uploadBusy=true;stage.resolve();await old;
  assert.equal(f.calls.filter(c=>c[0]==='fetch').length,0,'old upload must not commit with new account');assert.equal(f.context.state.uploadBusy,true,'old finally must not clear new account busy state');
 }
 {
  const a=upload('old-401'),f=fixture([a]),response=deferred();f.context.commitFetch=()=>response.promise;const request=f.context.api('uploads/stage/old/commit','POST',{}).catch(e=>e);
  f.context.clearResumeWorkspace();f.context.state.user={id:'bob',upload_allowed:true};f.context.state.csrf='bob-csrf';response.resolve({ok:false,status:401,text:async()=>JSON.stringify({error:{message:'old session'}})});await request;
  assert.equal(f.logout(),0,'late 401 from old upload must not log out new account');assert.equal(f.context.state.user.id,'bob');
 }
 {
  const a=upload('old-batch-a'),b=upload('old-batch-b'),f=fixture([a,b]),response=deferred();for(const u of [a,b]){u.stageId='stage-'+u.id;u.status='待确认'}
  let requests=0;f.context.commitFetch=async()=>{requests++;return response.promise};const old=f.context.confirmUploads();await until(()=>requests===1,'first old commit');
  f.context.clearResumeWorkspace();f.context.state.user={id:'bob',upload_allowed:true};f.context.state.uploads=[];f.context.state.uploadBusy=true;response.resolve({ok:true,status:200,text:async()=>JSON.stringify({track:{id:'old'},duplicate:false})});await old;
  assert.equal(requests,1,'manual batch must stop at account boundary');assert.equal(f.context.state.uploadBusy,true,'old manual finally must not clear new busy state');
 }
 {
  const a=upload('reentry'),f=fixture([a]),stage=deferred();let starts=0;
  f.context.resumeStageUpload=async u=>{starts++;await stage.promise;u.stageId='stage-reentry';u.status='待确认'};
  const first=f.context.startUploadFlow([a]),second=f.context.startUploadFlow([a]);await until(()=>starts===1,'single resumed task');stage.resolve();await Promise.all([first,second]);
  assert.equal(starts,1,'reentrant start must share the task');assert.equal(f.calls.filter(c=>c[0]==='fetch').length,1,'same stage committed once');
 }
 for(const concurrency of [1,2]){
  const items=Array.from({length:7},(_,n)=>upload('pipeline-'+n)),f=fixture(items),started=[],pending=new Map();let transfers=0,maxTransfers=0,autoDrain=false;
  f.get('uploadConcurrency').value=String(concurrency);
  f.context.resumeStageUpload=async u=>{
   const sent=deferred(),validated=deferred();pending.set(u.id,{sent,validated});started.push(u);transfers++;maxTransfers=Math.max(maxTransfers,transfers);
   if(autoDrain){sent.resolve();validated.resolve()}
   await sent.promise;transfers--;u.flowReleaseTransfer?.();await validated.promise;u.stageId='stage-'+u.id;u.status='待确认';
  };
  const run=f.context.startUploadFlow();await until(()=>started.length===concurrency,'initial transfers '+concurrency);
  assert.equal(transfers,concurrency);
  // Completed network transfers release their slot before validation completes.
  const cap=concurrency+2;
  for(let index=0;index<cap;index++){
   await until(()=>started.length>index,'pipeline transfer '+index);pending.get(started[index].id).sent.resolve();
  }
  await new Promise(resolve=>setImmediate(resolve));
  assert.equal(started.length,cap,'validation backlog must stay bounded');assert.equal(f.calls.filter(c=>c[0]==='fetch').length,0,'unfinished validation must not commit');
  assert.ok(maxTransfers<=concurrency,'network concurrency must respect selected limit');
  autoDrain=true;for(const task of pending.values())task.validated.resolve();await run;
  assert.equal(started.length,items.length);assert.ok(items.every(u=>u.status==='完成'));assert.ok(maxTransfers<=concurrency);assert.equal(f.context.state.uploadBusy,false);
 }
 console.log('Upload flow contracts PASS: automatic/review, duplicates, failure retry, resumed automatic commit, cancel/account isolation, late 401, manual batch boundary, single-task reentry and bounded 1/2-transfer validation pipeline');
}
main().catch(error=>{console.error(error);process.exitCode=1});
