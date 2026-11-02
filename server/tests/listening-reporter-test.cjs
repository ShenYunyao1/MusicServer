'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs');
test('schema-8 bridge never sends listening reports',()=>{
 const c={document:{documentElement:{dataset:{listeningEnabled:'false'}}},state:{user:{id:'a'}},fetch:()=>{throw new Error('bridge must not report')}};
 vm.createContext(c);vm.runInContext(fs.readFileSync('internal/music/listening-reporter.js','utf8'),c);c.newListeningSession({id:'track'});assert.equal(vm.runInContext('currentListening',c),null);
});
test('only played wall time counts, seek/pause are excluded and repeat reports stop after acknowledgement',async()=>{
 let now=0;const requests=[],events={};const audio={currentTime:0,paused:false,ended:false,seeking:false,playbackRate:1,addEventListener:(name,fn)=>events[name]=fn};
 const c={document:{documentElement:{dataset:{listeningEnabled:'true'}}},setTimeout,clearTimeout,performance:{now:()=>now},state:{user:{id:'a'},csrf:'csrf'},resumeWorkspaceEpoch:1,browserUUID:()=>String(Math.random()),memberPlayer:audio,AbortController,onlinePlayback:{track:{id:'track'}},fetch:async(url,o)=>{const body=JSON.parse(o.body);requests.push(body);return {ok:true,json:async()=>({minimum_ms:3000,counted:body.played_ms>=3000})}}};vm.createContext(c);vm.runInContext(fs.readFileSync('internal/music/listening-reporter.js','utf8'),c);c.initializeListeningReporter(audio);c.newListeningSession({id:'track'});await new Promise(setImmediate);events.playing();
 now=1000;audio.currentTime=1;events.timeupdate();events.pause();audio.paused=true;now=5000;events.timeupdate();audio.currentTime=90;events.seeking();events.seeked();audio.paused=false;events.playing();now=6000;audio.currentTime=91;events.timeupdate();assert.equal(requests.length,1);
 now=7000;audio.currentTime=92;events.timeupdate();await new Promise(setImmediate);assert.equal(requests.length,2);assert.equal(requests[1].played_ms,3000);now=8000;audio.currentTime=93;events.timeupdate();assert.equal(requests.length,2);
 c.state.user={id:'b'};c.resumeWorkspaceEpoch++;c.clearListeningReporter();events.timeupdate();assert.equal(requests.length,2);
});
