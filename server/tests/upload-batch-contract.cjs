'use strict';
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const dir=path.join(__dirname,'../internal/music'),batch=fs.readFileSync(path.join(dir,'upload-batch.js'),'utf8'),resume=fs.readFileSync(path.join(dir,'upload-resume.js'),'utf8');
function deferred(){let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return {promise,resolve,reject}}
async function until(check,label){for(let n=0;n<100;n++){if(check())return;await new Promise(resolve=>setImmediate(resolve))}throw new Error('Did not reach '+label)}
function upload(id){return {id,file:{name:id+'.wav',size:10},edits:{},status:'待暂存'}}
function fixture(items){
 const elements=new Map(),calls=[],state={user:{id:'alice'},uploads:items,playlists:[],batch:0};
 function element(){return {value:'',checked:false,options:[],classList:{add(){}},after(){},append(){},replaceChildren(...items){this.options=items},add(item){this.options.push(item)},querySelector(){return null}}}
 const get=id=>{if(!elements.has(id))elements.set(id,element());return elements.get(id)};
 const c={console,Map,WeakMap,Promise,Array,Math,JSON,Date,Option:function(text,value){this.text=text;this.value=value},document:{createElement:element,querySelector(){return null}},$:get,state,resumeWorkspaceEpoch:0,uploadFlowRun:null,uploadFlowControls:element(),
  uploadFlowCurrent:(owner,epoch)=>state.user?.id===owner&&c.resumeWorkspaceEpoch===epoch,uploadFlowPending:u=>!['完成','已去重'].includes(u.status),
  renderUploads(){},parseLocalUpload:async()=>{},invalidateLocalParse:u=>{u.localGeneration=(u.localGeneration||0)+1;u.localPromise=null;return u.localGeneration},
  startUploadFlow:async items=>{calls.push(['start',items.map(u=>u.id)])},confirmUpload:async()=>{},confirmUploads:async()=>{},persistResumeQueue:async()=>{},run:action=>action(),
  api:async(url,method)=>{calls.push(['api',url,method]);return {id:'target',version:1,owner_id:'alice'}},
 };
 vm.createContext(c);return {c,state,get,calls,load:()=>vm.runInContext(batch,c)};
}
async function restoreCommitted(){
 const f=fixture([]),saved={id:'saved',resumeId:'removed-remote-task',name:'saved.wav',size:10,targetPlaylistID:'target',playlistAssigned:false,commitResult:{track:{id:'existing-track'},duplicate:false}};
 Object.assign(f.c,{resumeQueueStorage:{read:async()=>[saved]},resumeJSON:async()=>({items:[]}),resumeCurrent:(owner,epoch)=>f.state.user?.id===owner&&f.c.resumeWorkspaceEpoch===epoch,
  describeFile:file=>({id:'new',file}),browserUUID:()=> 'new',completeResumeUpload:async u=>{u.resumeId=null},setStatus(){},renderUploads(){}});
 vm.runInContext(resume.slice(resume.indexOf('async function restoreResumeQueue('),resume.indexOf('function decorateResumableUploads(')),f.c);
 await f.c.restoreResumeQueue();assert.equal(f.state.uploads.length,1,'committed assignment must survive missing remote resume task');
 const u=f.state.uploads[0];assert.equal(u.commitResult.track.id,'existing-track');assert.equal(u.needsSourceFile,false);assert.equal(u.status,'完成');assert.equal(u.targetPlaylistID,'target');assert.ok(u.assignmentError);
}
async function retryAccountBoundary(){
 const u={...upload('done'),status:'完成',commitResult:{track:{id:'old-track'}},targetPlaylistID:'target',assignmentError:'retry'};
 const f=fixture([u]),response=deferred();let getStarted=false;
 f.c.api=async()=>{getStarted=true;return response.promise};f.load();const request=f.get('retryUploads').onclick();await until(()=>getStarted,'old assignment fetch');
 f.state.user={id:'bob'};f.c.resumeWorkspaceEpoch++;f.state.uploads=[{...upload('new-failure'),status:'失败'}];response.resolve({id:'target',owner_id:'alice',version:1});await request;
 assert.equal(f.calls.filter(c=>c[0]==='start').length,0,'old retry must not start new account failures');
}
async function parsingInvalidation(){
 const u=upload('parse'),f=fixture([u]),reads=[];
 f.c.parseLocalUpload=async value=>{const generation=f.c.invalidateLocalParse(value),read=deferred();reads.push(read);await read.promise;if(value.localGeneration===generation)value.localPreview={generation}};
 f.load();const old=f.c.parseLocalUpload(u);await until(()=>reads.length===1,'first local parser');f.c.invalidateLocalParse(u);const replacement=f.c.parseLocalUpload(u);reads[0].resolve();await old;
 await until(()=>reads.length===2,'fresh local parser after attachment replacement');reads[1].resolve();await replacement;assert.ok(u.localPreview,'replacement parse must produce preview');
}
async function assignmentDoesNotRetransmit(){
 const u={...upload('done'),status:'完成',commitResult:{track:{id:'track'}},targetPlaylistID:'target',assignmentError:'retry'},f=fixture([u]);f.load();await f.get('retryUploads').onclick();
 assert.equal(u.playlistAssigned,true);assert.equal(u.assignmentError,'');const posts=f.calls.filter(c=>c[0]==='api'&&c[2]==='POST');assert.equal(posts.length,1);assert.equal(posts[0][1],'playlists/target/tracks');
 const started=f.calls.find(c=>c[0]==='start');assert.ok(!started||started[1].length===0,'playlist retry must not retransmit completed audio');
}
async function pagingAndParserLimit(){
 const items=Array.from({length:120},(_,i)=>upload(String(i))),f=fixture(items),reads=[];let active=0,max=0;
 f.c.parseLocalUpload=async()=>{active++;max=Math.max(active,max);const read=deferred();reads.push(read);await read.promise;active--};f.load();
 assert.equal(f.c.visibleUploadItems().length,50);const jobs=items.slice(0,5).map(u=>f.c.parseLocalUpload(u));await until(()=>reads.length===2,'two bounded parsers');assert.equal(max,2);
 while(reads.length<5){for(const read of reads)read.resolve();await new Promise(resolve=>setImmediate(resolve))}for(const read of reads)read.resolve();await Promise.all(jobs);assert.equal(max,2);
}
async function main(){const failures=[];for(const test of [restoreCommitted,retryAccountBoundary,parsingInvalidation,assignmentDoesNotRetransmit,pagingAndParserLimit]){try{await test();console.log('PASS '+test.name)}catch(error){failures.push(test.name+': '+error.message);console.error('FAIL '+test.name+': '+error.message)}}assert.deepEqual(failures,[],'batch contracts failed')}
main().catch(error=>{console.error(error);process.exitCode=1});
