'use strict';

const COMMUNITY_FALLBACK_MS=60000;
const communityLive={source:null,connected:false,userID:null,key:'',view:null,generation:0,views:new Map(),timer:null,debounce:null,renderTimer:null,busy:false,job:null,pending:false,lastCheck:0,deferred:false};
const invitationBadge=document.createElement('span');invitationBadge.id='invitationBadge';invitationBadge.className='badge';invitationBadge.hidden=true;invitationBadge.setAttribute('aria-label','待处理邀请');invitationTab.append(' ',invitationBadge);

function communityLiveView(){
  const view={};
  if(state.page==='public'){
    if(state.publicDetailId)view.public_detail=state.publicDetailId;
    else {view.catalog=true;view.query=state.publicQuery||'';view.offset=state.publicOffset||0}
  }
  if(state.page==='public'||state.page==='keys'||state.page==='playlists'&&(state.subscriptionsTab||state.invitationsTab))view.subscriptions=true;
  if(state.page==='playlists'&&state.subscriptionsTab&&state.playlistDetailId)view.subscription_detail=state.playlistDetailId;
  if(state.page==='playlists'&&state.invitationsTab){view.invitations=true;view.invitation_id=state.invitationDetailID||'';view.invitation_offset=state.invitationOffset||0}
  if(sharingDialog.open&&state.sharingPlaylist){view.owner_playlist=state.sharingPlaylist.id;view.owner_offset=state.ownerInvitationOffset||0}
  return view;
}
function stopCommunityRealtime(clear=false){
  ++communityLive.generation;communityLive.source?.close();communityLive.source=null;communityLive.connected=false;
  clearTimeout(communityLive.timer);clearTimeout(communityLive.debounce);clearTimeout(communityLive.renderTimer);
  communityLive.timer=communityLive.debounce=communityLive.renderTimer=null;communityLive.job=null;communityLive.busy=false;communityLive.pending=false;
  if(clear){communityLive.views.clear();communityLive.userID=null;communityLive.key='';communityLive.view=null;communityLive.lastCheck=0;communityLive.deferred=false;state.liveSubscriptionTracks=null;state.liveSubscriptionTracksID=null;invitationBadge.textContent='';invitationBadge.hidden=true}
}
function liveCommunityMessage(){return communityLive.connected?'实时更新已连接':'实时连接中断，每分钟补查'}
function scheduleCommunityRealtime(force=false){
  if(!state.user||state.user.must_change_password){stopCommunityRealtime(true);return}
  if(document.hidden){stopCommunityRealtime();return}
  const view=communityLiveView(),key=JSON.stringify(view),changed=communityLive.userID!==state.user.id||communityLive.key!==key;
  if(changed){stopCommunityRealtime();communityLive.userID=state.user.id;communityLive.key=key;communityLive.view=view;force=true}
  const generation=communityLive.generation;
  if(!communityLive.source&&typeof EventSource!=='undefined'){
    const source=new EventSource('/api/v2/community/events?'+new URLSearchParams({view:key}));communityLive.source=source;
    source.onopen=()=>{if(generation===communityLive.generation){communityLive.connected=true;updateCommunityLiveStatus()}};
    const receive=()=>{if(generation!==communityLive.generation)return;clearTimeout(communityLive.debounce);communityLive.debounce=setTimeout(()=>syncCommunityRealtime(true),100)};
    source.addEventListener('ready',receive);source.addEventListener('change',receive);
    source.addEventListener('expired',()=>{if(generation===communityLive.generation)setLoggedOut()});
    source.addEventListener('reconnect',()=>{if(generation!==communityLive.generation)return;source.close();communityLive.source=null;communityLive.connected=false;communityLive.timer=setTimeout(()=>scheduleCommunityRealtime(true),3000)});
    source.onerror=()=>{if(generation!==communityLive.generation)return;communityLive.connected=false;if(source.readyState===2){source.close();communityLive.source=null;syncCommunityRealtime(true)}updateCommunityLiveStatus();scheduleCommunityFallback()};
  }
  if(force)syncCommunityRealtime(true);
  scheduleCommunityFallback();
}
function scheduleCommunityFallback(){
  clearTimeout(communityLive.timer);
  if(!state.user||document.hidden)return;
  communityLive.timer=setTimeout(()=>{scheduleCommunityRealtime();syncCommunityRealtime()},Math.max(1000,COMMUNITY_FALLBACK_MS-(Date.now()-communityLive.lastCheck)));
}
async function syncCommunityRealtime(force=false){
  if(!state.user||state.user.must_change_password||document.hidden)return;
  if(!force&&Date.now()-communityLive.lastCheck<COMMUNITY_FALLBACK_MS){scheduleCommunityFallback();return}
  if(communityLive.busy){communityLive.pending=true;return}
  const generation=communityLive.generation,userID=state.user.id,key=communityLive.key,view=communityLive.view||communityLiveView();
  const job={};communityLive.job=job;communityLive.busy=true;
  let entry=communityLive.views.get(key);
  if(!entry){entry={cursor:'',sections:new Map()};if(communityLive.views.size>=10)communityLive.views.clear();communityLive.views.set(key,entry)}
  try{
    const reply=await api('community/sync','POST',{cursor:entry.cursor,view});
    if(generation!==communityLive.generation||state.user?.id!==userID||key!==communityLive.key)return;
    entry.cursor=reply.cursor;
    if(reply.reset)entry.sections.clear();
    for(const [name,delta] of Object.entries(reply.sections)){
      let section=entry.sections.get(name);
      if(!section||delta.reset)section={rows:new Map(),order:[],meta:{}};
      for(const id of delta.removed)section.rows.delete(id);
      for(const item of delta.changed)section.rows.set(item.id,item);
      section.order=delta.order;section.meta=delta.meta;entry.sections.set(name,section);
    }
    applyCommunityRealtime(entry,new Set(Object.keys(reply.sections)),reply.sections);
    communityLive.lastCheck=Date.now();updateCommunityLiveStatus();
  }catch(error){
    if(generation===communityLive.generation&&state.user?.id===userID){communityLive.connected=false;communityLive.lastCheck=Date.now();updateCommunityLiveStatus()}
  }finally{
    if(communityLive.job===job){communityLive.job=null;communityLive.busy=false;const pending=communityLive.pending;communityLive.pending=false;if(pending)communityLive.debounce=setTimeout(()=>syncCommunityRealtime(true),100);scheduleCommunityFallback()}
  }
}
function liveSectionItems(entry,name){const section=entry.sections.get(name);return section?section.order.map(id=>section.rows.get(id)).filter(Boolean):[]}
function liveSectionPage(entry,name){const section=entry.sections.get(name);return {items:liveSectionItems(entry,name),...(section?.meta||{total:0,offset:0,limit:30})}}
function updateCommunityLiveStatus(){
  const message=liveCommunityMessage()+' · '+new Date().toLocaleTimeString();
  if(state.page==='public'&&!state.publicDetailId)$('publicCatalogStatus').textContent=message;
  if(state.page==='playlists'&&state.invitationsTab){const entry=communityLive.views.get(communityLive.key);$('invitationStatus').textContent=state.invitationDetailID&&entry?.sections.has('invitations')&&!liveSectionItems(entry,'invitations').length?'邀请不存在、已被删除或不属于当前账户。':message}
}
// Reuse unchanged list nodes, preserving keyboard focus and scroll position.
function patchCommunityList(container,attribute,changed,render){
  const old=new Map([...container.querySelectorAll('['+attribute+']')].map(el=>[el.getAttribute(attribute),el]));
  const focus=document.activeElement,focused=[...old.entries()].find(([,el])=>el===focus||el.contains(focus));
  render();
  for(const el of [...container.querySelectorAll('['+attribute+']')]){const id=el.getAttribute(attribute);if(old.has(id)&&!changed.has(id))el.replaceWith(old.get(id))}
  if(focused){const current=[...container.querySelectorAll('['+attribute+']')].find(el=>el.getAttribute(attribute)===focused[0]);if(current&&!current.contains(document.activeElement))current.focus({preventScroll:true})}
}
function renderRealtimePublicUnavailable(){
  state.publicDetail=null;$('publicDetail').querySelector('.playlist-tracks')?.sortCleanup?.();
  const back=routeLink('← 公开歌单','public/playlists'),title=document.createElement('h1'),notice=document.createElement('p');back.classList.add('page-back');title.textContent='歌单暂不可用';notice.className='notice';notice.textContent='歌单可能已转为私有、被删除或作者账户已停用。';$('publicDetail').replaceChildren(back,title,notice);document.title='歌单暂不可用 · 音乐服务器';
}
function applyCommunityRealtime(entry,changed,deltas={}){
  const pending=entry.sections.get('badge')?.meta.pending||0;invitationBadge.textContent=String(pending);invitationBadge.hidden=!pending;
  // Ordinary action responses can update state before the coalesced event is
  // fetched. Reconcile with the retained baseline even if the wire delta is empty.
  if(entry.sections.has('subscriptions')&&JSON.stringify(state.playlists.filter(p=>p.subscription_status))!==JSON.stringify(liveSectionItems(entry,'subscriptions')))changed.add('subscriptions');
  if(entry.sections.has('invitations')&&JSON.stringify(state.receivedInvitations||[])!==JSON.stringify(liveSectionItems(entry,'invitations')))changed.add('invitations');
  if([...changed].every(name=>name==='badge'))return;
  if(state.communityMutating||sharingBusy||state.playlistEditing){
    communityLive.deferred=true;clearTimeout(communityLive.renderTimer);communityLive.renderTimer=setTimeout(()=>{if(state.user&&!document.hidden){communityLive.deferred=false;applyCommunityRealtime(entry,new Set(entry.sections.keys()))}},200);return;
  }
  const y=scrollY,detailId=state.publicDetailId,active=document.activeElement,focusID=active?.id,start=active?.selectionStart,end=active?.selectionEnd,direction=active?.selectionDirection;
  if(changed.has('subscription_tracks')){state.liveSubscriptionTracksID=communityLive.view.subscription_detail;state.liveSubscriptionTracks=liveSectionItems(entry,'subscription_tracks')}
  if(changed.has('subscriptions')){
    state.playlists=state.playlists.filter(p=>!p.subscription_status).concat(liveSectionItems(entry,'subscriptions'));
  }
  if(state.page==='playlists'&&state.subscriptionsTab&&(changed.has('subscriptions')||changed.has('subscription_tracks')))renderPlaylists();
  if(state.page==='public'&&!detailId&&(changed.has('catalog')||changed.has('subscriptions'))&&entry.sections.has('catalog')){
    const data=liveSectionPage(entry,'catalog'),ids=changed.has('subscriptions')?data.items.map(p=>p.id):(deltas.catalog?.changed||data.items).map(p=>p.id);
    patchCommunityList($('publicCatalogList'),'data-public-playlist',new Set(ids),()=>renderPublicCatalog(data));
    const cacheKey=JSON.stringify([state.publicQuery||'',state.publicOffset||0]),cached=communityCache.get(cacheKey);
    if(cached){cached.data={...cached.data,...data};cached.checked=Date.now()}
  }
  if(state.page==='public'&&detailId&&(changed.has('detail_playlist')||changed.has('detail_tracks')||changed.has('subscriptions'))){
    const playlist=liveSectionItems(entry,'detail_playlist')[0];
    if(!playlist)renderRealtimePublicUnavailable();else{const tracks=liveSectionItems(entry,'detail_tracks');state.publicDetail={playlist,tracks};renderPlaylistDetail(playlist,{target:$('publicDetail'),publicView:true,tracks})}
  }
  if(state.page==='playlists'&&state.invitationsTab&&(changed.has('invitations')||changed.has('subscriptions'))){
    const data=liveSectionPage(entry,'invitations');state.receivedInvitations=data.items;
    patchCommunityList($('receivedInvitations'),'data-received-invitation',new Set((changed.has('subscriptions')?data.items:deltas.invitations?.changed||data.items).map(i=>i.id)),()=>renderReceivedInvitations(data));
    if(state.invitationDetailID&&!data.items.length)$('invitationStatus').textContent='邀请不存在、已被删除或不属于当前账户。';
  }
  if(sharingDialog.open&&(changed.has('owner_playlist')||changed.has('owner_invitations'))){
    const playlist=liveSectionItems(entry,'owner_playlist')[0];
    if(!playlist){sharingDialog.close();setStatus('分享设置已不可用，请重新打开歌单。','error')}
    else{const data=liveSectionPage(entry,'owner_invitations');patchCommunityList($('ownerInvitations'),'data-owner-invitation',new Set(changed.has('owner_playlist')?data.items.map(i=>i.id):(deltas.owner_invitations?.changed||data.items).map(i=>i.id)),()=>renderSharingSettings(playlist,data))}
  }
  const replacement=focusID&&$(focusID);
  if(replacement&&replacement!==active&&replacement.getClientRects().length){replacement.focus({preventScroll:true});if(typeof start==='number'&&typeof replacement.setSelectionRange==='function')replacement.setSelectionRange(start,end,direction)}
  if(scrollY!==y)scrollTo(0,y);
}
document.addEventListener('visibilitychange',()=>scheduleCommunityRealtime(true));
window.addEventListener('online',()=>scheduleCommunityRealtime(true));
