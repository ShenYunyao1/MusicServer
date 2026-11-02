'use strict';

const listeningPageState={generation:0,owner:null,controller:null,timer:null,page:null};
function listeningAvailable(){return document.documentElement.dataset.listeningEnabled==='true'}
function listeningRouteValues(){
 const values=new URLSearchParams(location.hash.split('?')[1]||''),range=['all','7d','30d'].includes(values.get('range'))?values.get('range'):'all',limit=[20,30,50].includes(Number(values.get('limit')))?Number(values.get('limit')):20;
 const offset=Number(values.get('offset'));return {range,q:(values.get('q')||'').trim().slice(0,200),offset:Number.isSafeInteger(offset)&&offset>=0?Math.floor(offset/limit)*limit:0,limit};
}
function listeningRoute(changes={}){const values={...listeningRouteValues(),...changes},query=new URLSearchParams();if(values.range!=='all')query.set('range',values.range);if(values.q)query.set('q',values.q);if(values.offset)query.set('offset',String(values.offset));if(values.limit!==20)query.set('limit',String(values.limit));return '#/listening'+(query.size?'?'+query:'')}
function setupListeningPage(){
 if(!listeningAvailable())return;
 if($('page-listening'))return;
 const button=document.createElement('button');button.dataset.page='listening';button.innerHTML='<span class="nav-icon"></span><span class="nav-label">我常听的</span>';document.querySelector('.nav').append(button);
 const section=document.createElement('section');section.id='page-listening';section.className='page';section.innerHTML='<div class="page-intro"><div><h1>我常听的</h1><p>按有效播放次数排序，仅统计功能启用后的播放。</p></div><button id="refreshListening" type="button" class="btn">刷新</button></div><div class="toolbar listening-toolbar"><label>时间范围 <select id="listeningRange" class="select"><option value="all">全部</option><option value="30d">近30天</option><option value="7d">近7天</option></select></label><input id="listeningSearch" class="search" type="search" placeholder="搜索曲名、歌手或专辑" aria-label="搜索常听曲目"><label>每页 <select id="listeningPageSize" class="select"><option value="20">20首</option><option value="30">30首</option><option value="50">50首</option></select></label></div><p id="listeningStatus" class="status" role="status"></p><div id="listeningPagerTop"></div><div id="listeningRows" class="panel listening-rows" role="list" aria-label="常听曲目"></div><div id="listeningPagerBottom"></div>';
 document.querySelector('.content').append(section);
 $('refreshListening').onclick=()=>loadListeningPage();
 $('listeningRange').onchange=()=>navigateListening({range:$('listeningRange').value,offset:0});
 $('listeningPageSize').onchange=()=>navigateListening({limit:Number($('listeningPageSize').value),offset:0});
 $('listeningSearch').oninput=()=>{clearTimeout(listeningPageState.timer);const owner=state.user?.id;listeningPageState.timer=setTimeout(()=>{if(owner===state.user?.id&&state.page==='listening')navigateListening({q:$('listeningSearch').value.trim().slice(0,200),offset:0})},300)};
}
function navigateListening(changes){clearTimeout(listeningPageState.timer);const route=listeningRoute({...changes,q:changes.q??$('listeningSearch').value.trim().slice(0,200)});if(location.hash===route)return loadListeningPage();location.hash=route}
function clearListeningPage(){
 clearTimeout(listeningPageState.timer);listeningPageState.controller?.abort();listeningPageState.controller=null;listeningPageState.generation++;listeningPageState.owner=null;listeningPageState.page=null;
 for(const id of ['listeningRows','listeningPagerTop','listeningPagerBottom'])$(id)?.replaceChildren();if($('listeningStatus'))$('listeningStatus').textContent='';if($('listeningSearch'))$('listeningSearch').value='';if($('listeningRange'))$('listeningRange').value='all';if($('listeningPageSize'))$('listeningPageSize').value='20';
}
async function loadListeningPage(){
 if(!listeningAvailable())throw new Error('当前版本尚未启用听歌统计，请完成数据库升级。');
 setupListeningPage();if(!state.user)return clearListeningPage();
 if(listeningPageState.owner!==state.user.id)clearListeningPage();listeningPageState.owner=state.user.id;
 clearTimeout(listeningPageState.timer);listeningPageState.controller?.abort();const controller=new AbortController();listeningPageState.controller=controller;
 const generation=++listeningPageState.generation,owner=state.user.id,epoch=typeof resumeWorkspaceEpoch==='number'?resumeWorkspaceEpoch:0,route=location.hash,values=listeningRouteValues();
 const current=()=>generation===listeningPageState.generation&&owner===state.user?.id&&(typeof resumeWorkspaceEpoch!=='number'||epoch===resumeWorkspaceEpoch)&&route===location.hash&&state.page==='listening'&&!controller.signal.aborted;
 $('listeningRange').value=values.range;$('listeningSearch').value=values.q;$('listeningPageSize').value=String(values.limit);$('listeningRows').replaceChildren();$('listeningPagerTop').replaceChildren();$('listeningPagerBottom').replaceChildren();$('listeningStatus').textContent='正在读取听歌统计…';$('refreshListening').disabled=true;
 try{
  const response=await fetch('/api/v2/listening/tracks?'+new URLSearchParams({...values,offset:String(values.offset),limit:String(values.limit)}),{credentials:'same-origin',cache:'no-store',signal:controller.signal});
  if(!current())return;
  if(!response.ok){if(response.status===401&&typeof setLoggedOut==='function')setLoggedOut();throw new Error('HTTP '+response.status)}
  const page=await response.json();if(!current())return;
  if(!Array.isArray(page.items)||!Number.isSafeInteger(page.total)||page.total<0||page.offset!==values.offset||page.limit!==values.limit)throw new Error('统计分页响应无效');
  if(page.total>0&&page.offset>=page.total){navigateListening({offset:Math.floor((page.total-1)/values.limit)*values.limit});return}
  listeningPageState.page=page;renderListeningPage(page);$('listeningStatus').textContent='共 '+page.total+' 首 · 按有效播放次数排序';
 }catch(error){if(!current()||error.name==='AbortError')return;listeningPageState.page=null;$('listeningStatus').textContent='读取失败：'+error.message;const retry=makeButton('重试读取',()=>loadListeningPage(),'btn');$('listeningRows').replaceChildren(retry)}
 finally{if(generation===listeningPageState.generation)$('refreshListening').disabled=false}
}
function listeningDate(value){const date=new Date(value);return Number.isFinite(date.getTime())?date.toLocaleString():'—'}
function renderListeningPage(page){
 const rows=$('listeningRows');rows.replaceChildren();
 if(!page.items.length){const empty=document.createElement('p');empty.className='empty';empty.textContent=listeningRouteValues().q?'没有匹配的常听曲目。':'还没有有效播放记录。听歌后可在这里查看。';rows.append(empty)}
 for(const track of page.items){
  const row=document.createElement('article');row.className='listening-track';row.dataset.listeningTrack=track.id;row.setAttribute('role','listitem');
  const cover=makeButton('♪',()=>playTrack({...track,detail_loaded:false}),'track-cover'),info=document.createElement('div'),name=makeButton(track.title||'未命名曲目',()=>openTrackDetails({...track,detail_loaded:false}),'text-button track-title'),artist=document.createElement('span'),count=document.createElement('span'),last=document.createElement('span');
  cover.setAttribute('aria-label','播放：'+(track.title||'未命名曲目'));cover.disabled=Boolean(track.missing||track.removed);info.className='listening-track-info';artist.className='listening-track-artist';artist.textContent=[track.artist||'未知歌手',track.album].filter(Boolean).join(' · ');count.className='listening-track-count';count.textContent=String(track.play_count||0)+' 次';last.className='listening-track-last';last.textContent='最近播放 '+listeningDate(track.last_played_at);info.append(name,artist);row.append(cover,info,count,last);
  row.oncontextmenu=event=>{event.preventDefault();showContextMenu(event,[{label:'播放曲目',disabled:cover.disabled,action:()=>playTrack({...track,detail_loaded:false})},{label:'曲目详情',action:()=>openTrackDetails({...track,detail_loaded:false})},{label:'加入我的歌单',disabled:cover.disabled,action:()=>openAddTracks([track.id])}])};
  row.tabIndex=0;row.onkeydown=event=>{if(event.target===row&&event.key==='Enter'){event.preventDefault();openTrackDetails({...track,detail_loaded:false})}else if(event.key==='ContextMenu'||event.shiftKey&&event.key==='F10'){event.preventDefault();const r=row.getBoundingClientRect();row.oncontextmenu({preventDefault(){},clientX:r.left+32,clientY:r.top+20})}};
  rows.append(row);if(track.cover_id&&!cover.disabled)loadCover(cover,'covers/'+encodeURIComponent(track.cover_id));
 }
 for(const id of ['listeningPagerTop','listeningPagerBottom'])renderListeningPager($(id),page);
}
function renderListeningPager(host,page){
 host.replaceChildren();if(!page.total)return;const nav=document.createElement('nav');nav.className='listening-pager';nav.setAttribute('aria-label',host.id==='listeningPagerTop'?'常听曲目顶部页码':'常听曲目底部页码');
 const pages=Math.ceil(page.total/page.limit),current=Math.floor(page.offset/page.limit)+1;
 const add=(label,number,disabled=false)=>{const button=makeButton(label,()=>navigateListening({offset:(number-1)*page.limit}),'btn small');button.disabled=disabled;if(number===current&&/^\d+$/.test(label))button.setAttribute('aria-current','page');nav.append(button)};
 add('上一页',current-1,current===1);const visible=new Set([1,pages,current-1,current,current+1].filter(n=>n>=1&&n<=pages));let previous=0;for(const number of [...visible].sort((a,b)=>a-b)){if(number-previous>1){const dots=document.createElement('span');dots.textContent='…';nav.append(dots)}add(String(number),number,number===current);previous=number}add('下一页',current+1,current===pages);const total=document.createElement('span');total.className='status';total.textContent='第 '+current+' / '+pages+' 页 · '+page.total+' 首';nav.append(total);host.append(nav);
}
