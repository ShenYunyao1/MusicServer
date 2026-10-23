'use strict';

const PUBLIC_REFRESH_MS=5*60*1000;
const communityCache=new Map();let communityTimer=null,communityRequest=0,communityLastCheck=0;
const publicNav=document.createElement('button');publicNav.dataset.page='public';publicNav.innerHTML='<span class="nav-icon"></span><span class="nav-label">公开歌单</span>';document.querySelector('.nav').append(publicNav);
const communityPage=document.createElement('section');communityPage.id='page-public';communityPage.className='page';
communityPage.innerHTML='<div class="page-intro"><div><h1>公开歌单</h1><p>发现本平台用户分享的歌单，订阅后可在播放器中使用。</p></div><button id="refreshPublic" class="btn">刷新</button></div><div id="publicBrowse"><form id="publicSearchForm" class="toolbar"><input id="publicSearch" class="search" placeholder="搜索歌单或作者" aria-label="搜索公开歌单"><button class="btn" type="submit">搜索</button></form><p id="publicCatalogStatus" class="status" role="status"></p><div id="publicCatalogList" class="playlist-list"></div><div class="toolbar public-paging"><button id="publicPrevious" class="btn">上一页</button><button id="publicNext" class="btn">下一页</button><span id="publicCount" class="status"></span></div></div><div id="publicDetail" hidden></div>';
document.querySelector('.content').append(communityPage);
const myPlaylistTabs=document.createElement('nav');myPlaylistTabs.id='myPlaylistTabs';myPlaylistTabs.className='page-tabs';myPlaylistTabs.innerHTML='<a class="btn" href="#/playlists">我创建的</a><a class="btn" href="#/subscriptions">我订阅的</a>';$('page-playlists').querySelector('.page-intro').after(myPlaylistTabs);
const discoverPublic=document.createElement('a');discoverPublic.id='discoverPublic';discoverPublic.href='#/public/playlists';discoverPublic.className='btn primary';discoverPublic.textContent='发现公开歌单';discoverPublic.hidden=true;$('newPlaylist').after(discoverPublic);
function subscriptionReason(p){return {private:'作者已设为私有',owner_disabled:'作者账户已停用',deleted:'歌单已删除',share_revoked:'定向分享尚未接受或已被撤回'}[p.subscription_reason]||'暂不可用'}
function ownPlaylists(){return state.playlists.filter(p=>!p.system&&!p.read_only&&p.owner_id===state.user?.id)}
function playlistVisibilityLabel(p){return {public:'公开歌单',shared:'仅分享歌单',private:'私有歌单'}[p.visibility]||'私有歌单'}
function mySubscription(id){return state.playlists.find(p=>p.id===id&&p.subscription_status)}
function syncMyPlaylistTabs(detail){
  myPlaylistTabs.hidden=Boolean(detail)||state.playlistEditing;myPlaylistTabs.querySelectorAll('a').forEach(a=>a.classList.toggle('active',a.hash===(state.invitationsTab?'#/invitations':state.subscriptionsTab?'#/subscriptions':'#/playlists')));
  discoverPublic.hidden=!state.subscriptionsTab||Boolean(detail)||state.playlistEditing;
}
function publicRoute(offset=0){const query=new URLSearchParams();if($('publicSearch').value.trim())query.set('q',$('publicSearch').value.trim());if(offset)query.set('offset',String(offset));return '#/public/playlists'+(query.size?'?'+query:'')}
$('publicSearchForm').onsubmit=event=>{event.preventDefault();location.hash=publicRoute()};
$('publicPrevious').onclick=()=>location.hash=publicRoute(Math.max(0,(state.publicOffset||0)-30));$('publicNext').onclick=()=>location.hash=publicRoute((state.publicOffset||0)+30);
$('refreshPublic').onclick=()=>loadPublicCatalog(true).catch(error=>setStatus(error.message,'error'));
function communityRelevant(){return state.user&&(state.page==='public'||state.page==='playlists'&&(state.subscriptionsTab||state.invitationsTab))}
function scheduleCommunityRefresh(){clearTimeout(communityTimer);if(!communityRelevant()||document.hidden)return;communityTimer=setTimeout(()=>communityCheck().catch(error=>setStatus(error.message,'error')),Math.max(1000,PUBLIC_REFRESH_MS-(Date.now()-communityLastCheck)))}
async function communityCheck(){
  if(!communityRelevant()||document.hidden)return;
  if(Date.now()-communityLastCheck<PUBLIC_REFRESH_MS){scheduleCommunityRefresh();return}
  try{if(state.page==='public'){if(state.publicDetailId)await loadPublicDetail(state.publicDetailId);else await loadPublicCatalog(true)}else if(state.invitationsTab)await loadReceivedInvitations();else await loadMySubscriptions()}
  finally{communityLastCheck=Date.now();scheduleCommunityRefresh()}
}
document.addEventListener('visibilitychange',()=>{if(document.hidden)clearTimeout(communityTimer);else if(communityRelevant())communityCheck().catch(error=>setStatus(error.message,'error'))});
function clearCommunityWorkspace(){clearTimeout(communityTimer);communityRequest++;communityCache.clear();communityLastCheck=0;state.publicDetailId=null;state.publicDetail=null;state.subscriptionsTab=false;$('publicCatalogList').replaceChildren();$('publicDetail').querySelector('.playlist-tracks')?.sortCleanup?.();$('publicDetail').replaceChildren();$('publicCatalogStatus').textContent='';$('publicSearch').value=''}
function renderPublicCatalog(data){
  const list=$('publicCatalogList');list.replaceChildren();
  for(const p of data.items){
    const card=document.createElement('article');card.className='playlist-card';card.tabIndex=0;card.setAttribute('role','link');card.dataset.publicPlaylist=p.id;card.setAttribute('aria-label','打开公开歌单：'+p.name);
    card.innerHTML='<span class="cover">'+txt((p.name||'♪').slice(0,1))+'</span><div class="playlist-info"><div class="playlist-title">'+txt(p.name)+'</div><div class="playlist-desc">'+txt(p.description||'暂无简介')+'</div><div class="playlist-meta">'+txt(p.source_owner_name||'用户')+' · '+p.track_count+' 首'+(mySubscription(p.id)?' · 已订阅':'')+'</div></div>';
    card.onclick=()=>{location.hash='#/public/playlists/'+p.id;scrollTo(0,0)};card.onkeydown=e=>{if(e.key==='Enter'){e.preventDefault();card.click()}};list.append(card);if(p.cover_id)loadCover(card.querySelector('.cover'),'covers/'+encodeURIComponent(p.cover_id));
  }
  if(!data.items.length)list.innerHTML='<div class="panel empty">暂无匹配的公开歌单。</div>';
  $('publicPrevious').disabled=data.offset===0;$('publicNext').disabled=data.offset+data.items.length>=data.total;$('publicCount').textContent=data.total?'第 '+(data.offset+1)+'–'+(data.offset+data.items.length)+' 个 / 共 '+data.total+' 个':'共 0 个';
}
async function loadPublicCatalog(force=false){
  const query=state.publicQuery||'',offset=state.publicOffset||0,key=JSON.stringify([query,offset]),cached=communityCache.get(key);
  $('publicBrowse').hidden=false;$('publicDetail').hidden=true;communityPage.querySelector('.page-intro').hidden=false;$('publicSearch').value=query;
  if(!force&&cached&&Date.now()-cached.checked<PUBLIC_REFRESH_MS){renderPublicCatalog(cached.data);communityLastCheck=cached.checked;scheduleCommunityRefresh();return}
  const generation=++communityRequest,userID=state.user?.id;communityLastCheck=Date.now();$('publicCatalogStatus').textContent='正在检查公开目录…';$('refreshPublic').disabled=true;
  try{
    const headers={};if(cached)headers['If-None-Match']=cached.etag;
    const response=await scheduleRequest(()=>fetch('/api/v2/public/playlists?'+new URLSearchParams({q:query,offset:String(offset),limit:'30'}),{credentials:'same-origin',headers}));
    if(response.status===401){setLoggedOut();throw new Error('登录已失效')}
    let data;if(response.status===304&&cached)data=cached.data;else{if(!response.ok)throw new Error('公开目录读取失败：HTTP '+response.status);data=await response.json()}
    if(generation!==communityRequest||state.user?.id!==userID)return;
    if(communityCache.size>=20)communityCache.clear();communityCache.set(key,{data,etag:response.headers.get('ETag')||cached?.etag,checked:Date.now()});
    if(state.page==='public'&&!state.publicDetailId){renderPublicCatalog(data);$('publicCatalogStatus').textContent='每 5 分钟自动检查 · '+(response.status===304?'目录未变化':'已更新')+' · '+new Date().toLocaleTimeString()}
  }catch(error){if(generation===communityRequest&&state.page==='public')$('publicCatalogStatus').textContent=error.message;throw error}
  finally{$('refreshPublic').disabled=false;communityLastCheck=Date.now();scheduleCommunityRefresh()}
}
async function loadPublicDetail(id){
  const generation=++communityRequest,userID=state.user?.id;communityLastCheck=Date.now();$('publicBrowse').hidden=true;communityPage.querySelector('.page-intro').hidden=true;$('publicDetail').hidden=false;
  try{const detail=await api('public/playlists/'+encodeURIComponent(id));if(generation!==communityRequest||state.user?.id!==userID||state.page!=='public'||state.publicDetailId!==id)return;
    state.publicDetail=detail;renderPlaylistDetail(detail.playlist,{target:$('publicDetail'),publicView:true,tracks:detail.tracks});
  }catch(error){if(generation===communityRequest){state.publicDetail=null;$('publicDetail').replaceChildren();if(error.status===404){const back=routeLink('← 公开歌单','public/playlists'),title=document.createElement('h1'),notice=document.createElement('p');back.classList.add('page-back');title.textContent='歌单暂不可用';notice.className='notice';notice.textContent='歌单可能已转为私有、被删除或作者账户已停用。';$('publicDetail').append(back,title,notice);document.title='歌单暂不可用 · 音乐服务器'}else throw error}}
  finally{communityLastCheck=Date.now();scheduleCommunityRefresh()}
}
async function loadMySubscriptions(){
  const userID=state.user?.id,views=await api('subscriptions');if(state.user?.id!==userID)return;state.playlists=state.playlists.filter(p=>!p.subscription_status).concat(views);communityLastCheck=Date.now();if(state.page==='playlists')renderPlaylists();scheduleCommunityRefresh();
}
async function changeSubscription(p,subscribing){
  if(state.communityMutating)return;state.communityMutating=true;
  try{await api('subscriptions/'+p.id,subscribing?'PUT':'DELETE');if(!subscribing&&state.page==='playlists'&&state.playlistDetailId===p.id)location.hash='#/subscriptions';await refreshCore();if(state.page==='public'&&state.publicDetailId===p.id)await loadPublicDetail(p.id);setStatus(subscribing?'已订阅歌单，可在 Key 授权中勾选':'已取消订阅并移除相关 Key 授权','ok')}
  catch(error){setStatus(error.message,'error');await loadMySubscriptions().catch(()=>{})}
  finally{state.communityMutating=false}
}
function copyPublicPlaylist(p){copyText(location.origin+'/#/public/playlists/'+encodeURIComponent(p.id)).then(()=>setStatus('公开歌单链接已复制到剪贴板','ok')).catch(error=>setStatus(error.message,'error'))}
function subscriptionActions(p){const own=p.owner_id===state.user?.id,sub=mySubscription(p.id);return {own,sub}}
function appendCommunityActions(actions,p,publicView){
  const {own,sub}=subscriptionActions(p);
  if(publicView&&own)actions.append(makeButton('管理此歌单',()=>location.hash='#/playlists/'+p.id));
  if(!publicView)appendSharingAction(actions,p);
  if(!own&&(p.read_only||publicView)){const button=makeButton(sub?'取消订阅':'订阅歌单',()=>changeSubscription(p,!sub),sub?'btn':'btn primary');button.dataset.subscribePlaylist=p.id;if(!sub&&p.subscription_status==='invalid')button.disabled=true;actions.append(button)}
  if(p.visibility==='public'&&p.subscription_status!=='invalid'){const share=makeButton('复制公开链接',()=>copyPublicPlaylist(p));share.dataset.sharePlaylist=p.id;actions.append(share)}
}
function renderSubscriptionMenu(event,p){showContextMenu(event,[{label:'取消订阅',action:()=>changeSubscription(p,false)},...(p.subscription_status==='active'?[{label:'复制公开链接',action:()=>copyPublicPlaylist(p)}]:[])])}
function groupKeyPlaylistOptions(container,allowed){
  container.replaceChildren();for(const [label,predicate] of [['我创建的歌单',p=>!p.system&&!p.read_only],['我订阅的歌单',p=>Boolean(p.subscription_status)],['全局曲库',p=>p.system]]){
    const group=document.createElement('fieldset');group.className='key-playlist-group';const legend=document.createElement('legend');legend.textContent=label;group.append(legend);
    for(const p of state.playlists.filter(predicate)){const row=document.createElement('label'),input=document.createElement('input');input.type='checkbox';input.value=p.id;input.dataset.playlistCheck='1';input.checked=allowed.has(p.id);input.disabled=p.subscription_status==='invalid';row.append(input,document.createTextNode(p.system?'所有可用曲目':p.name+(p.subscription_status==='invalid'?'（已暂停：'+subscriptionReason(p)+'）':'')));group.append(row)}
    if(group.children.length===1){const empty=document.createElement('p');empty.className='status';empty.textContent='暂无歌单';group.append(empty)}container.append(group);
  }
}
