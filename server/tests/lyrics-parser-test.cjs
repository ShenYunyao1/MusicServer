'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const context=vm.createContext({});
vm.runInContext(fs.readFileSync(path.join(__dirname,'../internal/music/lyrics-parser.js'),'utf8'),context);
const parse=text=>JSON.parse(JSON.stringify(context.parsePlayerLyrics(text)));

test('plain text retains separate lines and normalizes BOM and CRLF',()=>{
 assert.deepEqual(parse('\ufeff第一行\r\n第二行\r\n'),{timed:false,lines:[{time:null,text:'第一行'},{time:null,text:'第二行'}]});
 assert.deepEqual(parse(''),{timed:false,lines:[]});
 assert.deepEqual(parse(null),{timed:false,lines:[]});
});

test('LRC parses milliseconds and expands multi timestamps in stable time order',()=>{
 assert.deepEqual(parse('[00:30.123]第三\n[00:20.1][00:10.01]重复\n[00:20.100]同时间'),{timed:true,lines:[{time:10.01,text:'重复'},{time:20.1,text:'重复'},{time:20.1,text:'同时间'},{time:30.123,text:'第三'}]});
});

test('metadata is hidden while untimed mixed notes never enter timed lookup',()=>{
 const parsed=parse('[ar:歌手]\n[ti:歌曲]\n[al:专辑]\n[by:制作]\n说明\n[00:02]开始\n[00:03]');
 assert.deepEqual(parsed,{timed:true,lines:[{time:2,text:'开始'},{time:3,text:''},{time:null,text:'说明'}]});
 assert.equal(context.playerLyricIndex(parsed.lines,2.5),0);
 assert.equal(context.playerLyricIndex(parsed.lines,999),1);
});

test('positive offset advances lyrics and negative offset delays every timestamp',()=>{
 assert.deepEqual(parse('[00:01]一\n[offset:+500]\n[00:02]二').lines,[{time:0.5,text:'一'},{time:1.5,text:'二'}]);
 assert.equal(parse('[offset:-1000]\n[00:02.345]延后').lines[0].time,3.345);
 assert.equal(parse('[offset:5000]\n[00:01]提前').lines[0].time,0);
 assert.equal(parse('[offset:100]\n[offset:200]\n[00:01]最后偏移').lines[0].time,0.8);
});

test('invalid timestamps and invalid offset remain readable without a false time',()=>{
 for(const text of ['[00:60]无效','[xx:12]无效','[01:-2]无效','[1:2.0000]无效','[offset:NaN]']){
  assert.deepEqual(parse(text),{timed:false,lines:[{time:null,text}]});
 }
});

test('lookup handles boundaries, duplicate timestamps and nonfinite playback times',()=>{
 const lines=parse('[00:01]a\n[00:02]b\n[00:02]c\n说明').lines;
 assert.equal(context.playerLyricIndex(lines,0),-1);
 assert.equal(context.playerLyricIndex(lines,1),0);
 assert.equal(context.playerLyricIndex(lines,2),2);
 assert.equal(context.playerLyricIndex(lines,100),2);
 assert.equal(context.playerLyricIndex(lines,NaN),-1);
 assert.equal(context.playerLyricIndex(lines,Infinity),-1);
 assert.equal(context.playerLyricIndex([{time:null,text:'plain'}],5),-1);
 assert.equal(context.playerLyricIndex([],5),-1);
});

test('lyric HTML and hostile strings remain inert text',()=>{
 const text='<img src=x onerror=alert(1)>';
 assert.equal(parse('[00:01]'+text).lines[0].text,text);
 assert.equal(context.executed,undefined);
});

test('blank timed lines survive and metadata-only input is empty',()=>{
 assert.deepEqual(parse('[00:01]\n[00:03]   '),{timed:true,lines:[{time:1,text:''},{time:3,text:''}]});
 assert.deepEqual(parse('[ar:Artist]\n[ti:Title]\n[offset:0]'),{timed:false,lines:[]});
});

test('oversized input rejects without silently displaying a truncated lyric',()=>{
 assert.throws(()=>context.parsePlayerLyrics('字'.repeat(2*1024*1024+1)),/歌词/);
 assert.throws(()=>context.parsePlayerLyrics('字'.repeat(800000)),/歌词/);
 assert.throws(()=>context.parsePlayerLyrics('\n'.repeat(20001)),/歌词行数/);
 assert.throws(()=>context.parsePlayerLyrics('[00:01]'.repeat(20001)),/歌词行数/);
 assert.equal(parse('a'.repeat(2*1024*1024)).lines[0].text.length,2*1024*1024);
});
