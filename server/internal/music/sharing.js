'use strict';

const invitationTab=routeInvitationLink();
function routeInvitationLink(){const a=document.createElement('a');a.href='#/invitations';a.className='btn';a.textContent='分享邀请';myPlaylistTabs.append(a);return a}
const invitationBrowse=document.createElement('section');invitationBrowse.id='invitationBrowse';invitationBrowse.hidden=true;
invitationBrowse.innerHTML='<div class="toolbar"><button id="backInvitations" class="btn page-back" hidden>← 分享邀请</button><button id="refreshInvitations" class="btn">刷新邀请</button></div><p class="notice">仅目标账户可接受邀请。接受后加入“我订阅的”，播放器 Key 仍需手动勾选授权。</p><p id="invitationStatus" role="status" class="status"></p><div id="receivedInvitations"></div><div id="invitationPaging" class="toolbar"><button id="previousInvitations" class="btn">上一页</button><button id="nextInvitations" class="btn">下一页</button><span id="invitationCount" class="status"></span></div>';
$('page-playlists').append(invitationBrowse);
$('backInvitations').onclick=()=>location.hash='#/invitations';$('refreshInvitations').onclick=()=>loadReceivedInvitations().catch(error=>setStatus(error.message,'error'));
function invitationRoute(offset){return '#/invitations'+(offset?'?offset='+offset:'')}
$('previousInvitations').onclick=()=>location.hash=invitationRoute(Math.max(0,(state.invitationOffset||0)-30));$('nextInvitations').onclick=()=>location.hash=invitationRoute((state.invitationOffset||0)+30);
let receivedGeneration=0,sharingGeneration=0,sharingBusy=false;
function invitationState(i){return {pending:'等待接受',accepted:'已接受',declined:'已拒绝',revoked:'已撤回'}[i.state]||'不可用'}
async function loadReceivedInvitations(){
  const generation=++receivedGeneration,userID=state.user?.id,id=state.invitationDetailID,offset=state.invitationOffset||0;communityLastCheck=Date.now();$('invitationStatus').textContent='正在读取邀请…';
  try{
    const data=id?{items:[await api('invitations/'+encodeURIComponent(id))],total:1,offset:0,limit:30}:await api('invitations?'+new URLSearchParams({offset:String(offset),limit:'30'}));
    if(generation!==receivedGeneration||state.user?.id!==userID||state.page!=='playlists'||!state.invitationsTab)return;
    state.receivedInvitations=data.items;renderReceivedInvitations(data);$('invitationStatus').textContent='每 5 分钟检查 · '+new Date().toLocaleTimeString();
  }catch(error){if(generation===receivedGeneration){state.receivedInvitations=[];$('receivedInvitations').replaceChildren();$('invitationStatus').textContent=error.status===404?'邀请不存在、已被删除或不属于当前账户。':error.message;$('invitationPaging').hidden=true}if(error.status!==404)throw error}
  finally{communityLastCheck=Date.now();scheduleCommunityRefresh()}
}
function renderReceivedInvitations(data){
  const container=$('receivedInvitations');container.replaceChildren();$('backInvitations').hidden=!state.invitationDetailID;$('invitationPaging').hidden=Boolean(state.invitationDetailID);
  for(const i of data.items){
    const card=document.createElement('article');card.className='panel invitation-item';card.dataset.receivedInvitation=i.id;
    const info=document.createElement('div'),name=document.createElement('h2'),meta=document.createElement('p'),actions=document.createElement('div');name.textContent=i.playlist_name;meta.className='status';meta.textContent=accountLabel({display_name:i.inviter_name,username:i.inviter_username})+' · '+invitationState(i)+(!i.available?' · 分享暂不可用':'');info.append(name,meta);actions.className='actions';
    if(i.state==='pending')for(const [action,label] of [['accept','接受分享'],['decline','拒绝']]){const b=makeButton(label,()=>respondInvitation(i,action),action==='accept'?'btn primary':'btn');b.dataset.invitationAction=action;b.disabled=!i.available;actions.append(b)}
    if(i.state==='accepted'){
      const subscribed=mySubscription(i.playlist_id),b=makeButton(subscribed?'查看订阅':'重新订阅',()=>subscribed?location.hash='#/subscriptions/'+i.playlist_id:restoreSharedSubscription(i));b.dataset.openShared=i.playlist_id;b.disabled=!subscribed&&!i.available;actions.append(b);
    }
    card.append(info,actions);container.append(card);
  }
  if(!data.items.length)container.innerHTML='<div class="panel empty">暂无收到的分享邀请。</div>';
  $('previousInvitations').disabled=data.offset===0;$('nextInvitations').disabled=data.offset+data.items.length>=data.total;$('invitationCount').textContent='共 '+data.total+' 条';
  document.title='分享邀请 · 我的歌单 · 音乐服务器';
}
async function respondInvitation(i,action){
  if(sharingBusy)return;sharingBusy=true;
  try{await api('invitations/'+i.id+'/'+action,'POST',undefined,undefined,i.version);await refreshCore();await loadReceivedInvitations();setStatus(action==='accept'?'已接受分享；可前往 Key 授权中勾选该订阅':'已拒绝邀请','ok')}
  catch(error){setStatus(error.message,'error');await loadReceivedInvitations().catch(()=>{})}
  finally{sharingBusy=false}
}
async function restoreSharedSubscription(i){await changeSubscription({id:i.playlist_id},true);if(mySubscription(i.playlist_id))location.hash='#/subscriptions/'+i.playlist_id}

const sharingDialog=document.createElement('dialog');sharingDialog.id='sharingDialog';sharingDialog.className='modal sharing-dialog';
sharingDialog.innerHTML='<div class="modal-head"><h3>分享设置</h3><button id="closeSharing" class="btn">关闭</button></div><div class="modal-body"><p id="sharingSource" class="notice"></p><button id="editSharingVisibility" class="btn">编辑可见性</button><form id="inviteForm" class="invite-form"><div class="field"><label for="inviteUsername">完整登录名</label><input id="inviteUsername" autocomplete="off" maxlength="64" required placeholder="输入对方完整登录名"></div><button id="sendInvitation" type="submit" class="btn primary">发送邀请</button></form><p class="status">仅目标账户可接受，邀请不会自动授予播放器 Key 权限。</p><p id="sharingError" class="status error" role="alert"></p><div id="ownerInvitations"></div><div class="toolbar"><button id="previousOwnerInvitations" class="btn">上一页</button><button id="nextOwnerInvitations" class="btn">下一页</button><span id="ownerInvitationCount" class="status"></span></div></div>';
document.body.append(sharingDialog);$('closeSharing').onclick=()=>sharingDialog.close();sharingDialog.addEventListener('close',()=>{sharingGeneration++;state.sharingPlaylist=null;$('inviteUsername').value='';$('ownerInvitations').replaceChildren();$('sharingSource').textContent='';$('sharingError').textContent=''});
$('previousOwnerInvitations').onclick=()=>{state.ownerInvitationOffset=Math.max(0,state.ownerInvitationOffset-30);loadSharingSettings().catch(error=>setStatus(error.message,'error'))};$('nextOwnerInvitations').onclick=()=>{state.ownerInvitationOffset+=30;loadSharingSettings().catch(error=>setStatus(error.message,'error'))};
$('editSharingVisibility').onclick=()=>{const p=state.sharingPlaylist;sharingDialog.close();if(p)editPlaylist(p)};
$('inviteForm').onsubmit=event=>{event.preventDefault();mutateSharing(()=>api('playlists/'+state.sharingPlaylist.id+'/invitations','POST',{username:$('inviteUsername').value.trim()},undefined,state.sharingPlaylist.version),'邀请已保存')};
function appendSharingAction(actions,p){if(p.owner_id!==state.user?.id||p.system)return;const b=makeButton('分享设置',()=>openSharingSettings(p));b.dataset.manageSharing=p.id;actions.append(b)}
async function openSharingSettings(p){
  state.sharingPlaylist=p;state.ownerInvitationOffset=0;$('sharingError').textContent='';$('ownerInvitations').replaceChildren();sharingDialog.showModal();
  try{await loadSharingSettings()}catch(error){$('sharingError').textContent=error.message}
}
async function loadSharingSettings(){
  const p=state.sharingPlaylist;if(!p)return;
  const generation=++sharingGeneration,userID=state.user?.id;
  const [detail,page]=await Promise.all([api('playlists/'+p.id),api('playlists/'+p.id+'/invitations?'+new URLSearchParams({offset:String(state.ownerInvitationOffset||0),limit:'30'}))]);
  if(generation!==sharingGeneration||state.user?.id!==userID||!sharingDialog.open||state.sharingPlaylist?.id!==p.id)return;
  state.sharingPlaylist=detail.playlist;
  $('sharingSource').textContent=detail.playlist.name+' · '+(detail.playlist.visibility==='shared'?'仅分享：只向接受邀请的账户开放。':'当前并非“仅分享”。请在编辑页切换可见性并保存后再邀请。');
  $('sendInvitation').disabled=$('inviteUsername').disabled=detail.playlist.visibility!=='shared'||page.total>=100;
  const rows=$('ownerInvitations');rows.replaceChildren();
  for(const i of page.items){
    const row=document.createElement('article');row.className='invitation-item';row.dataset.ownerInvitation=i.id;
    const info=document.createElement('div'),name=document.createElement('strong'),status=document.createElement('p'),actions=document.createElement('div');name.textContent=accountLabel({display_name:i.recipient_display_name,username:i.recipient_username});status.className='status';status.textContent=invitationState(i);info.append(name,status);actions.className='actions';
    const active=['pending','accepted'].includes(i.state),action=active?'revoke':'resend',button=makeButton(active?'撤回':'再次邀请',()=>mutateSharing(()=>api('playlists/'+p.id+'/invitations/'+i.id+(active?'':'/resend'),active?'DELETE':'POST',undefined,undefined,i.version),active?'邀请已撤回，相关分享授权已暂停':'已再次邀请，等待对方接受'));button.dataset.ownerInviteAction=action;button.disabled=!active&&(detail.playlist.visibility!=='shared'||i.recipient_id===state.user.id);actions.append(button);
    if(active){const copy=makeButton('复制邀请链接',()=>copyInvitationLink(i));copy.dataset.copyInvitation=i.id;actions.append(copy)}row.append(info,actions);rows.append(row);
  }
  if(!page.items.length)rows.innerHTML='<p class="empty">尚未邀请任何账户。</p>';
  $('previousOwnerInvitations').disabled=page.offset===0;$('nextOwnerInvitations').disabled=page.offset+page.items.length>=page.total;$('ownerInvitationCount').textContent='共 '+page.total+' / 100 条';
}
async function mutateSharing(action,message){
  if(sharingBusy)return;sharingBusy=true;$('sharingError').textContent='';$('sendInvitation').disabled=true;
  try{await action();$('inviteUsername').value='';await refreshCore();if(sharingDialog.open)await loadSharingSettings();setStatus(message,'ok')}
  catch(error){$('sharingError').textContent=error.message;setStatus(error.message,'error');if(sharingDialog.open)await loadSharingSettings().catch(()=>{})}
  finally{sharingBusy=false;if(sharingDialog.open)$('sendInvitation').disabled=state.sharingPlaylist?.visibility!=='shared'||$('inviteUsername').disabled}
}
function copyInvitationLink(i){copyText(location.origin+'/#/invitations/'+encodeURIComponent(i.id)).then(()=>setStatus('邀请链接已复制到剪贴板，仅目标账户可接受','ok')).catch(error=>setStatus(error.message,'error'))}
function clearSharingWorkspace(){receivedGeneration++;sharingGeneration++;sharingBusy=false;state.invitationsTab=false;state.invitationDetailID=null;state.receivedInvitations=[];state.sharingPlaylist=null;if(sharingDialog.open)sharingDialog.close();$('receivedInvitations').replaceChildren();$('ownerInvitations').replaceChildren();$('sharingSource').textContent='';$('invitationStatus').textContent='';$('inviteUsername').value='';invitationBrowse.hidden=true}
