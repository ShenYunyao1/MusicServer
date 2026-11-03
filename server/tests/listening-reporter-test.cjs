'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs');
test('schema-8 bridge never sends listening reports',()=>{
 const c={document:{documentElement:{dataset:{listeningEnabled:'false'}}},state:{user:{id:'a'}},fetch:()=>{throw new Error('bridge must not report')}};
 vm.createContext(c);vm.runInContext(fs.readFileSync('internal/music/listening-reporter.js','utf8'),c);c.newListeningSession({id:'track'});assert.equal(vm.runInContext('currentListening',c),null);
});
test('429 is visible and respects Retry-After without repeated immediate requests',async()=>{
 let now=0,calls=0;const events={},messages=[],audio={currentTime:0,playbackRate:1,paused:false,addEventListener:(name,fn)=>events[name]=fn};
 const c={document:{documentElement:{dataset:{listeningEnabled:'true'}}},state:{user:{id:'a'}},performance:{now:()=>now},setTimeout,clearTimeout,AbortController,browserUUID:()=> 'id',memberPlayer:audio,onlinePlayback:{track:{id:'track'}},fetch:async()=>{calls++;return {ok:false,status:429,headers:{get:()=> '60'}}}};
 vm.createContext(c);vm.runInContext(fs.readFileSync('internal/music/listening-reporter.js','utf8'),c);c.showListeningNotice=message=>messages.push(message);c.initializeListeningReporter(audio);c.newListeningSession({id:'track'});events.playing();await new Promise(setImmediate);
 assert.equal(calls,1);assert.match(messages.at(-1),/上限/);now=59000;events.timeupdate();assert.equal(calls,1);now=60001;events.timeupdate();await new Promise(setImmediate);assert.equal(calls,2);
 c.state.user={id:'b'};now=120002;events.timeupdate();assert.equal(calls,2,'old account never retries');
});
test('only played wall time counts, seek/pause are excluded and repeat reports stop after acknowledgement',async()=>{
 let now=0;const requests=[],events={};const audio={currentTime:0,paused:false,ended:false,seeking:false,playbackRate:1,addEventListener:(name,fn)=>events[name]=fn};
 const c={document:{documentElement:{dataset:{listeningEnabled:'true'}}},setTimeout,clearTimeout,performance:{now:()=>now},state:{user:{id:'a'},csrf:'csrf'},resumeWorkspaceEpoch:1,browserUUID:()=>String(Math.random()),memberPlayer:audio,AbortController,onlinePlayback:{track:{id:'track'}},fetch:async(url,o)=>{const body=JSON.parse(o.body);requests.push(body);return {ok:true,json:async()=>({minimum_ms:3000,counted:body.played_ms>=3000})}}};vm.createContext(c);vm.runInContext(fs.readFileSync('internal/music/listening-reporter.js','utf8'),c);c.initializeListeningReporter(audio);c.newListeningSession({id:'track'});await new Promise(setImmediate);assert.equal(requests.length,0,'selection alone must not allocate a server session');events.timeupdate();assert.equal(requests.length,0);events.playing();await new Promise(setImmediate);
 now=1000;audio.currentTime=1;events.timeupdate();events.pause();audio.paused=true;now=5000;events.timeupdate();audio.currentTime=90;events.seeking();events.seeked();audio.paused=false;events.playing();now=6000;audio.currentTime=91;events.timeupdate();assert.equal(requests.length,1);
 now=7000;audio.currentTime=92;events.timeupdate();await new Promise(setImmediate);assert.equal(requests.length,2);assert.equal(requests[1].played_ms,3000);now=8000;audio.currentTime=93;events.timeupdate();assert.equal(requests.length,2);
 c.state.user={id:'b'};c.resumeWorkspaceEpoch++;c.clearListeningReporter();events.timeupdate();assert.equal(requests.length,2);
});

test('evicted unconfirmed guard restarts from zero instead of disabling reporting',async()=>{
 let now=0;const events={},requests=[],audio={currentTime:0,paused:false,ended:false,seeking:false,playbackRate:1,addEventListener:(n,f)=>events[n]=f};
 const c={document:{documentElement:{dataset:{listeningEnabled:'true'}}},setTimeout,clearTimeout,performance:{now:()=>now},state:{user:{id:'a'},csrf:'csrf'},browserUUID:()=> 'sid',memberPlayer:audio,AbortController,onlinePlayback:{track:{id:'track'}},fetch:async(_,o)=>{const body=JSON.parse(o.body);requests.push(body);if(requests.length===2)return {ok:false,status:409,json:async()=>({error:{code:'LISTENING_START_REQUIRED'}})};return {ok:true,json:async()=>({minimum_ms:1000,counted:false})}}};
 vm.createContext(c);vm.runInContext(fs.readFileSync('internal/music/listening-reporter.js','utf8'),c);c.initializeListeningReporter(audio);c.newListeningSession({id:'track'});events.playing();await new Promise(setImmediate);
 now=1100;audio.currentTime=1.1;events.timeupdate();await new Promise(setImmediate);assert.equal(requests.length,2);
 now=2200;audio.currentTime=2.2;events.timeupdate();await new Promise(setImmediate);assert.equal(requests.length,3);assert.equal(requests[2].played_ms,0);
});
