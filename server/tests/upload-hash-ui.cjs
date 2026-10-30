'use strict';
const assert=require('node:assert/strict'),fs=require('node:fs/promises'),http=require('node:http'),path=require('node:path'),crypto=require('node:crypto');
const {chromium}=require(process.env.MUSIC_SERVER_PLAYWRIGHT||'playwright');
async function main(){
 const directory=path.join(__dirname,'../internal/music');
 const client=await fs.readFile(path.join(directory,'upload-hash.js'),'utf8');
 const worker=await fs.readFile(path.join(directory,'upload-hash-worker.js'),'utf8');
 const resume=await fs.readFile(path.join(directory,'upload-resume.js'),'utf8');
 const fallback=resume.slice(resume.indexOf('function resumeSHA256Fallback('),resume.indexOf('async function resumeHash('));
 let workerMode='normal',requests=0;
 const server=http.createServer((request,response)=>{
  response.setHeader('Content-Type','text/javascript');
  if(request.url==='/assets/upload-hash-worker.js'){requests++;response.end(workerMode==='hang'?'setInterval(()=>{},1000);':workerMode==='error'?'throw new Error("test worker failure");':(workerMode==='insecure'?'Object.defineProperty(self,"crypto",{value:undefined});':'')+worker);return}
  response.setHeader('Content-Type','text/html');response.end('<!doctype html><script>'+fallback+'\n'+client+'</script>');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await chromium.launch({headless:true,...(process.env.MUSIC_SERVER_BROWSER?{executablePath:process.env.MUSIC_SERVER_BROWSER}:{})});
 const expected=crypto.createHash('sha256').update(Buffer.from('abc')).digest('hex');
 const base='http://127.0.0.1:'+server.address().port;
 try{
  for(const mode of ['normal','insecure','disabled','error']){
   workerMode=mode;const page=await browser.newPage();await page.goto(base);
   if(mode==='disabled')await page.evaluate(()=>{window.Worker=undefined;Object.defineProperty(window,'crypto',{value:undefined})});
   const before=requests,result=await page.evaluate(async()=>{const bytes=new TextEncoder().encode('abc').buffer;const hash=await uploadHash(bytes);return {hash,length:bytes.byteLength,bytes:[...new Uint8Array(bytes)]}});
   assert.equal(result.hash,expected,mode);assert.equal(result.length,3);assert.deepEqual(result.bytes,[97,98,99]);
   if(mode!=='disabled')assert.ok(requests>before,mode+' uses actual worker');
   const empty=await page.evaluate(()=>uploadHash(new ArrayBuffer(0)));assert.equal(empty,crypto.createHash('sha256').update('').digest('hex'));
   const large=await page.evaluate(async()=>{const bytes=new Uint8Array(4*1048576);bytes.fill(123);const hash=await uploadHash(bytes.buffer);return {hash,length:bytes.byteLength}});
   assert.equal(large.hash,crypto.createHash('sha256').update(Buffer.alloc(4*1048576,123)).digest('hex'));assert.equal(large.length,4*1048576);
   assert.equal(await page.evaluate(()=>uploadHash(new ArrayBuffer(4*1048576+1)).then(()=>false,e=>e.code==='UPLOAD_HASH_SIZE')),true);
   await page.close();
  }
  workerMode='hang';const page=await browser.newPage();await page.goto(base);
  const bounded=await page.evaluate(async()=>{window.pendingHashes=Array.from({length:4},()=>uploadHash(new ArrayBuffer(1)));return uploadHash(new ArrayBuffer(1)).then(()=>false,e=>e.code==='UPLOAD_HASH_BUSY')});assert.equal(bounded,true,'queue must reject beyond four retained blocks');
  await page.close();console.log('Upload hash browser PASS: real worker, insecure SHA fallback, disabled/error fallback, correct empty/4MiB digests, original buffer retained, bounded queue and block size');
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
}
main().catch(error=>{console.error(error);process.exitCode=1});
