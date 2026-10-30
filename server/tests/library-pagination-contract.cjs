'use strict';
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const text=fs.readFileSync(path.join(__dirname,'../internal/music/library-pagination.js'),'utf8');
const source=text.slice(0,text.indexOf('function renderLibraryPagination('));
function deferred(){let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no});return {promise,resolve,reject}}
function fixture(){
 const saved=new Map(),renders=[],statuses=[];
 const context={console,URLSearchParams,Number,Math,Set,Promise,AbortController,
  state:{user:{id:'alice'},page:'tracks',selectedTrackIds:new Set(['selected'])},
  browseState:{epoch:1,generations:{tracks:1},tracks:{offset:0,limit:50,total:102,items:[{id:'previous'}],query:''}},
  location:{hash:'#/library'},localStorage:{getItem:key=>saved.get(key)||null},
  document:{querySelectorAll:()=>[]},$:()=>({checked:true,scrollIntoView(){}}),updateTrackSelection(){},
  renderLibraryPagination:page=>renders.push(page),renderTracks(){renders.push('tracks')},
  browseCacheTracks(){},setStatus:(...args)=>statuses.push(args),
 };
 context.history={replaceState(_,__,hash){context.location.hash=hash},pushState(_,__,hash){context.location.hash=hash}};
 context.browseRouteQuery=()=>new URLSearchParams(context.location.hash.split('?')[1]||'');
 context.browseLibraryPage=()=>context.browseState.tracks;
 vm.createContext(context);vm.runInContext(source,context);
 return {c:context,saved,renders,statuses};
}
async function main(){
 const unsafeRoutes=[];
 {
  const{c,saved}=fixture();assert.equal(c.libraryPageSize(),50);
  saved.set('library-page-size/alice','30');assert.equal(c.libraryPageSize(),30);
  c.state.user={id:'bob'};assert.equal(c.libraryPageSize(),50,'A new account must not inherit another account preference');
  saved.set('library-page-size/bob','20');assert.equal(c.libraryPageSize(),20);
  c.state.user={id:'alice'};assert.equal(c.libraryPageSize(),30);
  c.location.hash='#/library?page_size=20';assert.equal(c.libraryPageSize(),20,'A URL size takes precedence over the saved preference');
  c.location.hash='#/playlists?page_size=20';assert.equal(c.libraryPageSize(),30,'Other page URLs must not change the library size');
  saved.set('library-page-size/alice','999');c.location.hash='#/library?page_size=NaN';assert.equal(c.libraryPageSize(),50);
  c.localStorage.getItem=()=>{throw new Error('storage denied')};assert.equal(c.libraryPageSize(),50);
 }
 {
  const{c}=fixture();
  for(const[query,offset]of [['page=3&page_size=30',60],['offset=65&page_size=30',60],['offset=-5&page_size=20',0],['page=0&page_size=20',0],['page=1.2&page_size=20',0],['page=NaN&page_size=30',0],['page=1000001&page_size=20',19999980]]){c.location.hash='#/library?'+query;assert.equal(c.libraryReadRouteOffset(),offset,query)}
  for(const query of ['page=Infinity','page=bad','page=-999','offset=99999999999999999999999999999999999999']){c.location.hash='#/library?'+query;const offset=c.libraryReadRouteOffset();if(!Number.isSafeInteger(offset)||offset<0)unsafeRoutes.push(query)}
  const values=new URLSearchParams({q:'Singer',tf:'saved-condition',sort:'artist',dir:'desc',offset:'99'});c.libraryWriteRouteValues(values,60,30);
  assert.equal(values.get('page'),'3');assert.equal(values.get('page_size'),'30');assert.equal(values.has('offset'),false);assert.equal(values.get('q'),'Singer');assert.equal(values.get('tf'),'saved-condition');assert.equal(values.get('sort'),'artist');assert.equal(values.get('dir'),'desc');
 }
 {
  const{c}=fixture();
  assert.deepEqual(Array.from(c.libraryPageNumbers(1,1)),[1]);assert.deepEqual(Array.from(c.libraryPageNumbers(1,0)),[]);
  assert.deepEqual(Array.from(c.libraryPageNumbers(1,10)),[1,2,3,4,5,10]);assert.deepEqual(Array.from(c.libraryPageNumbers(5,10)),[1,4,5,6,10]);assert.deepEqual(Array.from(c.libraryPageNumbers(10,10)),[1,6,7,8,9,10]);
  for(const current of [1,2,500,999,1000]){const numbers=Array.from(c.libraryPageNumbers(current,1000));assert.ok(numbers.length<=7);assert.ok(numbers.includes(current));assert.equal(numbers[0],1);assert.equal(numbers.at(-1),1000);assert.ok(numbers.every(n=>Number.isInteger(n)&&n>=1&&n<=1000))}
 }
 for(const fail of [false,true]){
  const{c,renders,statuses}=fixture(),old=deferred();
  c.loadBrowseTracks=()=>{c.browseState.generations.tracks++;return old.promise};
  const navigating=c.libraryNavigate(2,30);assert.equal(c.state.selectedTrackIds.size,0);
  const fresh={offset:0,limit:30,total:1,items:[{id:'new-search'}],query:'new'};
  c.location.hash='#/library?q=new&page_size=30';c.browseState.generations.tracks++;c.browseState.tracks=fresh;
  if(fail)old.reject(new Error('old page disconnected'));else old.resolve({offset:30,limit:30,total:102,items:[{id:'stale'}]});
  await navigating;
  assert.equal(c.browseState.tracks,fresh,'An older page request must not replace the current search');
  assert.equal(renders.includes('tracks'),false,'An older page request must not re-render the current view');assert.deepEqual(statuses,[],'Old errors must not label a new search failed');
  assert.equal(renders.at(-1),fresh,'Busy cleanup must use the latest page, never a previous snapshot');
 }
 for(const replaceAccount of [false,true]){
  const{c}=fixture(),body=deferred(),reading=deferred();let loggedOut=0;
  c.setLoggedOut=()=>loggedOut++;
  c.fetch=async url=>url.includes('old')?{ok:false,status:401,text:()=>{reading.resolve();return body.promise}}:{ok:true,status:200,text:async()=>JSON.stringify({marker:'new'})};
  const old=c.libraryPageRequest('browse/tracks?old').then(value=>({value}),error=>({error}));await reading.promise;
  if(replaceAccount){c.state.user={id:'bob'};c.browseState.epoch++}
  const fresh=await c.libraryPageRequest('browse/tracks?new');assert.equal(fresh.marker,'new','New page reading must not wait for the old body');
  body.resolve(JSON.stringify({error:{message:'old expired session'}}));const result=await old;
  assert.ok(result.error,'A canceled old response must not return page data');
  assert.equal(loggedOut,0,replaceAccount?'An old account 401 must not log out the new account':'An explicitly canceled old 401 must not log out the active request owner');
 }
 assert.deepEqual(unsafeRoutes,[],'Invalid routes must not send unsafe SQL offsets');
 console.log('Library pagination contracts PASS: account preference isolation, URL priority, invalid values, page conversion, bounded page numbers and old success/error isolation.');
}
main().catch(error=>{console.error(error);process.exitCode=1});
