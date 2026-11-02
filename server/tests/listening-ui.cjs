'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm'),path=require('node:path');
const source=fs.readFileSync(path.join(__dirname,'../internal/music/listening-ui.js'),'utf8');
function deferred(){let resolve;const promise=new Promise(r=>resolve=r);return {promise,resolve}}
function fixture(hash='#/listening'){
 const ids=new Map(),requests=[],played=[],details=[],menus=[],added=[];
 class Element{
  constructor(){this.children=[];this.attributes={};this.dataset={};this.value='';this.textContent='';this.disabled=false;this.className=''}
  set id(id){this.identifier=id;ids.set(id,this)}get id(){return this.identifier}
  set innerHTML(html){this.children=[];for(const match of html.matchAll(/<[a-z]+[^>]*\bid="([^"]+)"[^>]*>/g)){const el=new Element();el.id=match[1];this.append(el)}}
  append(...items){this.children.push(...items)}replaceChildren(...items){this.children=items}setAttribute(name,value){this.attributes[name]=String(value)}
 }
 const nav=new Element(),content=new Element();
 const c={document:{createElement:()=>new Element(),querySelector:selector=>selector==='.nav'?nav:content},$:id=>ids.get(id),location:{hash},state:{user:{id:'alice'},page:'listening'},resumeWorkspaceEpoch:1,AbortController,URLSearchParams,setTimeout,clearTimeout,console,
 makeButton:(label,action,cls)=>{const button=new Element();button.textContent=label;button.onclick=action;button.className=cls;return button},
 fetch:(url,options)=>{const pending=deferred();requests.push({url,options,pending});return pending.promise},playTrack:t=>played.push(t),openTrackDetails:t=>details.push(t),showContextMenu:(_,items)=>menus.push(items),openAddTracks:ids=>added.push(ids),loadCover(){},setLoggedOut:()=>{throw new Error('stale response logged out the replacement owner')}};
 vm.createContext(c);vm.runInContext(source,c);c.setupListeningPage();return {c,ids,requests,played,details,menus,added,get:id=>ids.get(id),respond(index,page,status=200){requests[index].pending.resolve({ok:status===200,status,json:async()=>page})}};
}
test('page route, both pagers and page size/range changes retain filters and reset offset',async()=>{
 const f=fixture('#/listening?range=7d&q=artist&limit=30&offset=30'),pending=f.c.loadListeningPage();
 assert.match(f.requests[0].url,/range=7d/);assert.match(f.requests[0].url,/offset=30/);assert.match(f.requests[0].url,/limit=30/);
 f.respond(0,{items:[],total:95,offset:30,limit:30});await pending;
 for(const id of ['listeningPagerTop','listeningPagerBottom']){const buttons=f.get(id).children[0].children;assert.equal(buttons.find(el=>el.attributes['aria-current']==='page').textContent,'2');assert.ok(buttons.some(el=>el.textContent==='4'))}
 f.get('listeningPagerBottom').children[0].children.find(el=>el.textContent==='下一页').onclick();assert.match(f.c.location.hash,/offset=60/);
 f.get('listeningPageSize').value='50';f.get('listeningPageSize').onchange();assert.match(f.c.location.hash,/limit=50/);assert.doesNotMatch(f.c.location.hash,/offset=/);assert.match(f.c.location.hash,/q=artist/);
 f.get('listeningRange').value='30d';f.get('listeningRange').onchange();assert.match(f.c.location.hash,/range=30d/);
});
test('late responses from old routes or account cannot render or expire the replacement session',async()=>{
 const f=fixture(),old=f.c.loadListeningPage();f.c.location.hash='#/listening?range=7d';const next=f.c.loadListeningPage();
 assert.equal(f.requests[0].options.signal.aborted,true);f.respond(1,{items:[],total:0,offset:0,limit:20});await next;
 const text=f.get('listeningStatus').textContent;f.respond(0,{items:[{id:'private'}],total:1,offset:0,limit:20});await old;assert.equal(f.get('listeningStatus').textContent,text);
 const previous=f.c.loadListeningPage();f.c.state.user={id:'bob'};f.c.resumeWorkspaceEpoch++;f.c.clearListeningPage();f.respond(2,{},401);await previous;assert.equal(f.get('listeningStatus').textContent,'');assert.equal(f.get('listeningRows').children.length,0);
});
test('errors have a retry state, not an empty history result',async()=>{
 const f=fixture(),pending=f.c.loadListeningPage();f.respond(0,{},503);await pending;assert.match(f.get('listeningStatus').textContent,/读取失败/);assert.equal(f.get('listeningRows').children[0].textContent,'重试读取');assert.equal(f.get('listeningPagerTop').children.length,0);
});
test('row actions are single-track and independent from another page selections',async()=>{
 const f=fixture(),pending=f.c.loadListeningPage(),track={id:'one',title:'<script>ignored</script>',artist:'Artist',play_count:3,last_played_at:'2026-11-02T00:00:00Z'};
 f.c.state.selectedTrackIds=new Set(['foreign-selection']);f.respond(0,{items:[track],total:1,offset:0,limit:20});await pending;
 const row=f.get('listeningRows').children[0];assert.equal(row.children[1].children[0].textContent,track.title);row.children[0].onclick();assert.equal(f.played[0].id,'one');row.children[1].children[0].onclick();assert.equal(f.details[0].detail_loaded,false);
 row.oncontextmenu({preventDefault(){}});f.menus[0][2].action();assert.deepEqual(JSON.parse(JSON.stringify(f.added)),[['one']]);assert.equal(f.c.state.selectedTrackIds.size,1);
});
