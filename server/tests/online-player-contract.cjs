'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const directory=path.join(__dirname,'../internal/music');
const source=fs.readFileSync(path.join(directory,'online-player.js'),'utf8');
const parser=fs.readFileSync(path.join(directory,'lyrics-parser.js'),'utf8');
function deferred(){let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no});return {promise,resolve,reject}}
async function settle(){for(let index=0;index<4;index++)await new Promise(resolve=>setImmediate(resolve))}
function fixture(){
 const ids=new Map(),requests=[],revoked=[],created=[],properties=new Map();
 class Element{
  constructor(tag='div'){this.tagName=tag;this.children=[];this.attributes=new Map();this.listeners=new Map();this.hidden=false;this.disabled=false;this.value='';this.dataset={};this.style={setProperty:(name,value)=>properties.set(name,value)};this.classList={add(){},remove(){},toggle(){}};this.clientHeight=300;this.offsetTop=0;this.offsetHeight=80;this.parentNode=null;this.isConnected=true;this.ownText=''}
  set id(value){this.identifier=value;ids.set(value,this)}get id(){return this.identifier}
  set innerHTML(value){this.replaceChildren();for(const match of value.matchAll(/<([a-z]+)[^>]*\bid="([^"]+)"[^>]*>/g)){const child=new Element(match[1]);child.id=match[2];this.append(child)}}
  get textContent(){return this.ownText+this.children.map(child=>child.textContent||'').join('')}
  set textContent(value){this.ownText=String(value);this.children=[]}
  append(...children){for(const child of children){if(child.tagName==='fragment')this.append(...child.children);else{child.parentNode=this;this.children.push(child)}}}
  replaceChildren(...children){this.ownText='';this.children=[];this.append(...children)}
  setAttribute(name,value){this.attributes.set(name,String(value))}getAttribute(name){return this.attributes.get(name)??null}removeAttribute(name){this.attributes.delete(name)}
  addEventListener(name,listener){if(!this.listeners.has(name))this.listeners.set(name,[]);this.listeners.get(name).push(listener)}
  emit(name,event={}){for(const listener of this.listeners.get(name)||[])listener(event)}
  focus(){}scrollTo(value){this.scroll=value}getBoundingClientRect(){return {height:100,top:600,bottom:700}}
  querySelector(selector){const walk=children=>{for(const child of children){if(selector==='[aria-current="true"]'&&child.getAttribute('aria-current')==='true')return child;const index=selector.match(/\[data-lyric-index="(\d+)"\]/)?.[1];if(index!==undefined&&child.dataset.lyricIndex===index)return child;const nested=walk(child.children);if(nested)return nested}return null};return walk(this.children)}
 }
 class Audio extends Element{
  constructor(){super('audio');this.paused=true;this.ended=false;this.currentTime=0;this.duration=NaN;this.volume=1;this.muted=false;this.playResults=[];this.playCalls=0;this.error=null;this.source=''}
  get src(){return this.source}set src(value){this.source=value;this.setAttribute('src',value)}
  removeAttribute(name){super.removeAttribute(name);if(name==='src')this.source=''}
  pause(){this.paused=true;this.emit('pause')}
  play(){this.playCalls++;this.paused=false;this.emit('play');return this.playResults.shift()||Promise.resolve()}
  load(){this.currentTime=0;this.duration=NaN;this.ended=false}
 }
 const audio=new Audio(),bar=new Element();bar.id='previewBar';bar.hidden=true;
 const doc={body:new Element(),documentElement:new Element(),createElement:tag=>new Element(tag),createDocumentFragment:()=>new Element('fragment'),addEventListener(){}};
 const c={console,document:doc,window:{addEventListener(){}},AbortController,Number,Math,Date,Set,Map,Promise,state:{user:{id:'alice'}},browseState:{epoch:1},memberPlayer:audio,
  $:id=>ids.get(id),makeButton:(text,action)=>{const element=new Element('button');element.textContent=text;element.onclick=action;return element},
  URL:{createObjectURL:blob=>{const url='blob:test-'+created.length;created.push({url,blob});return url},revokeObjectURL:url=>revoked.push(url)},
  fetch:(url,options)=>{const pending=deferred();requests.push({url,options,pending});return pending.promise},
 };
 vm.createContext(c);vm.runInContext(parser+'\n'+source,c);c.initializeOnlinePlayer(bar,audio);
 return {c,audio,bar,ids,requests,created,revoked,properties,get:id=>ids.get(id),snapshot:()=>vm.runInContext('({generation:onlinePlayback.generation,track:onlinePlayback.track,lyrics:onlinePlayback.lyrics,active:onlinePlayback.active,coverURL:onlinePlayback.coverURL,seeking:onlinePlayback.seeking})',c)};
}

test('late metadata, old play resolution and old cover body cannot replace a new song',async()=>{
 const f=fixture(),oldPlay=deferred(),oldJSON=deferred(),oldBlob=deferred();f.audio.playResults.push(oldPlay.promise);
 f.c.playOnlineTrack({id:'old',title:'Old',cover_id:'old-cover',detail_loaded:false});
 const cover=f.requests.find(request=>request.url.includes('/covers/')),metadata=f.requests.find(request=>request.url.includes('/tracks/'));
 cover.pending.resolve({ok:true,blob:()=>oldBlob.promise});metadata.pending.resolve({ok:true,json:()=>oldJSON.promise});await settle();
 f.c.playOnlineTrack({id:'new',title:'New',lyrics:'[00:02]New line',cover_id:'new-cover'});
 f.requests.find(request=>request.url.endsWith('new-cover')).pending.resolve({ok:true,blob:async()=>({id:'new-cover'})});await settle();
 f.get('onlinePlayerStatus').textContent='New status';oldPlay.resolve();oldBlob.resolve({id:'old-cover'});oldJSON.resolve({id:'old',title:'Late old',lyrics:'Old lyrics'});await settle();
 assert.equal(f.get('previewTitle').textContent,'New');assert.equal(f.get('onlineLyricsLines').textContent,'New line');assert.equal(f.get('onlinePlayerStatus').textContent,'New status');assert.equal(f.snapshot().track.id,'new');assert.equal(f.created.length,1);assert.equal(f.created[0].blob.id,'new-cover');assert.ok(cover.options.signal.aborted);
});

test('closing clears media, artwork, lyrics and ignores pending work',async()=>{
 const f=fixture();f.c.playOnlineTrack({id:'one',title:'One',cover_id:'cover',lyrics:'[00:01]First'});f.requests[0].pending.resolve({ok:true,blob:async()=>({})});await settle();
 const coverURL=f.snapshot().coverURL;assert.ok(coverURL);f.c.showOnlineLyrics(true);f.c.closeOnlinePlayer();
 assert.equal(f.bar.hidden,true);assert.equal(f.get('onlineLyricsPanel').hidden,true);assert.equal(f.audio.getAttribute('src'),null);assert.equal(f.audio.paused,true);assert.equal(f.snapshot().track,null);assert.equal(f.get('onlineLyricsLines').textContent,'');assert.ok(f.revoked.includes(coverURL));
 const pending=fixture();pending.c.playOnlineTrack({id:'pending',title:'Pending',detail_loaded:false});pending.c.closeOnlinePlayer();pending.requests[0].pending.resolve({ok:true,json:async()=>({id:'pending',title:'Late',lyrics:'Late lyric'})});await settle();assert.equal(pending.bar.hidden,true);assert.equal(pending.get('previewTitle').textContent,'');assert.equal(pending.get('onlineLyricsLines').textContent,'');
});

test('account replacement rejects metadata and releases existing cover on cleanup',async()=>{
 const f=fixture();f.c.playOnlineTrack({id:'old',title:'Old',detail_loaded:false});const pending=f.requests[0];f.c.state.user={id:'bob'};f.c.browseState.epoch++;f.c.closeOnlinePlayer();pending.pending.resolve({ok:true,json:async()=>({id:'old',title:'Private title',lyrics:'Private lyrics'})});await settle();assert.equal(f.snapshot().track,null);assert.equal(f.get('onlineLyricsLines').textContent,'');assert.equal(f.bar.hidden,true);
});

test('artwork replacement revokes old object URL and never allocates a stale one',async()=>{
 const f=fixture();f.c.playOnlineTrack({id:'a',cover_id:'a',lyrics:''});f.requests[0].pending.resolve({ok:true,blob:async()=>({id:'a'})});await settle();const original=f.snapshot().coverURL;
 f.c.playOnlineTrack({id:'b',cover_id:'b',lyrics:''});assert.ok(f.revoked.includes(original));const pending=f.requests[1];f.c.closeOnlinePlayer();pending.pending.resolve({ok:true,blob:async()=>({id:'b'})});await settle();assert.equal(f.created.length,1);assert.equal(f.snapshot().coverURL,null);
});

test('seek reflects actual duration and uses a preview without claiming audio moved early',()=>{
 const f=fixture(),seek=f.get('onlineSeek');f.c.playOnlineTrack({id:'a',lyrics:'[00:01]a\n[00:08]b'});assert.equal(seek.disabled,true);
 f.audio.duration=10;f.audio.currentTime=2;f.c.renderOnlinePlayer();assert.equal(seek.disabled,false);assert.equal(seek.value,'200');seek.value='800';seek.oninput();assert.equal(f.audio.currentTime,2);assert.equal(f.get('onlineElapsed').textContent,'00:08');seek.onchange();assert.equal(f.audio.currentTime,8);assert.equal(f.snapshot().seeking,false);assert.equal(f.snapshot().active,1);
 f.audio.duration=NaN;seek.value='500';seek.onchange();assert.equal(f.audio.currentTime,8);assert.equal(seek.disabled,true);
});

test('oversized lyric parser failure becomes retry UI and never an uncaught task',async()=>{
 const f=fixture();f.c.playOnlineTrack({id:'huge',lyrics:'a'.repeat(2*1024*1024+1)});await settle();assert.match(f.get('onlineLyricsLines').textContent,/读取失败/);assert.equal(f.snapshot().lyrics.length,0);assert.equal(f.get('onlineLyricsLines').children.at(-1).tagName,'button');
});
