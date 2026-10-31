'use strict';
const assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm'),path=require('node:path');
function source(file,start,end){const text=fs.readFileSync(path.join(__dirname,'../internal/music',file),'utf8');return text.slice(text.indexOf(start),text.indexOf(end,text.indexOf(start)))}
async function main(){
 let resolve;const pending=new Promise(r=>resolve=r),els=new Map();const get=id=>{if(!els.has(id))els.set(id,{value:'owned',textContent:'',replaceChildren(){},disabled:false,querySelectorAll:()=>[]});return els.get(id)};
 const user={id:'target'},state={user:{id:'admin'},page:'users',managedUserID:'target',resourceOffset:0,userResources:[]};
 const c={console,state,usersEpoch:1,userResourceGeneration:0,managedUser:()=>user,$:get,api:()=>pending,document:{createElement(){throw new Error('stale resource response rendered')}}};vm.createContext(c);vm.runInContext(source('users.js','async function loadUserResources()','$(\'userResourceKind\').onchange'),c);
 const old=c.loadUserResources();get('userResourceKind').value='playlists';resolve({items:[{id:'stale-track'}],total:1});await old;assert.equal(state.userResources.length,0,'Old resource kind must not populate the replacement tab');
 const routes=[],filter={version:1,tags:[]};const ctx={console,URLSearchParams,structuredClone,state:{page:'playlists',trackFilter:'all',selectedTrackIds:new Set()},location:{hash:'#/playlists/example'},trackFilterWorkspace:{baseline:1},trackFiltersEnabled:()=>true,$:()=>({value:''}),document:{querySelectorAll:()=>[]},writeTrackFilterRoute:()=>routes.push(ctx.location.hash),loadFilteredTracks:async()=>{throw new Error('Hidden library must not be queried')},renderTracks(){},history:{replaceState(_,__,hash){ctx.location.hash=hash}},libraryWriteRouteValues(){}};vm.createContext(ctx);vm.runInContext(source('track-filter-ui.js','async function applyTrackFilter(','async function applyTrackTagFilter('),ctx);await ctx.applyTrackFilter(filter);assert.equal(ctx.location.hash.split('?')[0],'#/library','Applying a filter from a playlist opens library');assert.deepEqual(routes,[],'Do not rewrite the playlist route with library filters');
 console.log('Audit frontend contracts PASS: resource-kind isolation and cross-page filter navigation');
}
main().catch(e=>{console.error(e);process.exitCode=1});
