'use strict';
let personalRoute='',personalUserID=null,personalOriginalName='',personalVersion=0,personalGeneration=0,personalBusy=false,personalReturnRoute='#/library',lastPersonalOrigin='#/library',personalOtherSessions=0;
let personalEdit=null,personalEditEpoch=0;
const personalPasswordFields=['accountOldPassword','accountNewPassword','accountConfirmPassword'];
function setupProfileWorkspace(){
 const page=document.createElement('section');page.id='page-account';page.className='page';
 page.innerHTML=`<button id="accountBack" class="btn page-back">← 返回</button><div class="page-intro"><h1>个人中心</h1></div>
 <section class="panel account-summary"><div class="profile-summary"><span id="profileAvatar" class="profile-avatar" aria-hidden="true"></span><div><strong id="profileHeading"></strong><p id="profileSummaryMeta" class="status"></p></div></div><a id="accountCapacitySummary" href="#/account/storage">读取容量…</a></section>
 <nav class="page-tabs" aria-label="个人中心页签">${[['profile','基本资料'],['storage','存储与上传'],['security','账户安全'],['sessions','登录管理']].map(([key,name])=>'<a class="btn" href="#/account/'+key+'" data-account-tab="'+key+'">'+name+'</a>').join('')}</nav>
 <section id="account-profile" class="panel account-panel"><div class="storage-section-head"><h2>基本资料</h2><button id="editProfile" class="btn primary">编辑资料</button></div><dl class="profile-facts"><div><dt>昵称</dt><dd id="profileDisplayName"></dd></div><div><dt>登录名</dt><dd id="profileUsername"></dd></div><div><dt>角色</dt><dd id="profileRole"></dd></div><div><dt>注册时间</dt><dd><time id="profileCreatedAt"></time></dd></div></dl><p id="profilePageError" class="form-error" hidden></p></section>
 <section id="account-storage" class="panel account-panel" hidden><div class="storage-section-head"><h2>存储与上传</h2><button id="refreshPersonalStorage" class="btn">刷新容量</button></div><div id="personalStorageMetrics" class="personal-storage-metrics"></div><progress id="personalStorageProgress" max="100" hidden></progress><p id="personalStorageStatus" class="status"></p><p id="personalUploadStatus" class="notice"></p><div class="actions"><a class="btn primary" href="#/uploads">上传曲目</a><a class="btn" href="#/library?filter=mine">查看我的上传</a><a class="btn" href="#/account/security">邮箱与上传资格</a></div><details class="storage-rules"><summary>容量统计说明</summary><p>按原始上传者统计音频占用，接管不转移占用；头像不计入音频额度。额度由管理员调整。</p></details></section>
 <section id="account-security" class="account-panel" hidden><article id="emailProfile" class="panel"><div class="storage-section-head"><h2>邮箱</h2><button id="editEmail" class="btn">绑定／更换邮箱</button></div><p id="emailBindingStatus" class="notice"></p><p id="emailProfilePageError" class="form-error" hidden></p></article><article class="panel"><div class="storage-section-head"><h2>登录密码</h2><button id="editPassword" class="btn">修改密码</button></div><p class="status">修改成功后重新登录，播放器 Key 保留。</p></article></section>
 <section id="account-sessions" class="account-panel" hidden><div class="toolbar"><p class="status">管理网页登录；播放器 API Key 不受影响。</p><div class="actions"><button id="refreshAccountSessions" class="btn">刷新登录</button><button id="revokeOtherSessions" class="btn danger">退出其他所有登录</button></div></div><p id="accountSessionError" class="form-error" hidden></p><p id="accountSessionStatus" class="status"></p><div id="accountSessionList"></div></section>`;
 document.querySelector('.content').append(page);
 const entry=makeButton('个人中心',()=>navigatePage('account'));entry.id='personalCenter';entry.addEventListener('click',()=>accountMenu.open=false);accountActions.prepend(entry);
 $('changePassword').onclick=()=>openPersonalEditor('password');$('manageSessions').onclick=()=>location.hash='#/account/sessions';
 $('accountBack').onclick=()=>location.hash=personalReturnRoute;
 $('editProfile').onclick=()=>openPersonalEditor('profile');$('editPassword').onclick=()=>openPersonalEditor('password');$('editEmail').onclick=()=>openPersonalEditor('email');
 $('refreshPersonalStorage').onclick=()=>loadPersonalStorage();$('refreshAccountSessions').onclick=()=>loadPersonalSessions();$('revokeOtherSessions').onclick=()=>mutatePersonalSessions(null);
 window.addEventListener('beforeunload',event=>{if(personalDirty()){event.preventDefault();event.returnValue=''}});
}
function personalError(id,message=''){const n=$(id);if(n){n.textContent=message;n.hidden=!message}}
function personalEditCurrent(e){return personalEdit===e&&e.owner===state.user?.id&&e.epoch===personalEditEpoch&&e.dialog.isConnected}
function personalDirty(){
 const e=personalEdit;if(!e||!personalEditCurrent(e))return false;
 if(e.type==='profile')return Boolean($('profileName')&&$('profileName').value!==e.originalName||avatarDirty());
 if(e.type==='password')return personalPasswordFields.some(id=>$(id)?.value);
 return Boolean(emailProfileDirty());
}
function confirmPersonalExit(){return !personalEdit}
function permitPersonalNavigation(hash){if(personalEdit){history.replaceState(null,'',personalEdit.openingHash||'#/library');requestPersonalClose();return false}personalRoute='';++personalGeneration;return true}
function notePersonalNavigation(hash,page){if(page!=='account')lastPersonalOrigin=hash}
function resetPersonalPassword(){for(const id of personalPasswordFields)if($(id)){$(id).value='';$(id).type='password'}}
function clearProfileWorkspace(){closePersonalEditor(true);++personalGeneration;personalBusy=false;personalRoute='';personalUserID=null;personalOriginalName='';personalVersion=0;personalOtherSessions=0;personalReturnRoute=lastPersonalOrigin='#/library';if(typeof clearAvatarWorkspace==='function')clearAvatarWorkspace();for(const id of ['profileDisplayName','profileUsername','profileHeading','profileRole','profileSummaryMeta','profileCreatedAt','emailBindingStatus','accountCapacitySummary','personalStorageStatus','personalUploadStatus','accountSessionStatus','accountSessionList','personalStorageMetrics'])if($(id))$(id).replaceChildren();if($('personalStorageProgress'))$('personalStorageProgress').hidden=true}
function fillPersonalProfile(user){
 personalOriginalName=user.display_name||user.username;personalVersion=user.version;
 $('profileHeading').textContent=accountLabel(user);$('profileDisplayName').textContent=personalOriginalName;$('profileUsername').textContent=user.username;$('profileRole').textContent=user.role==='admin'?'管理员':'普通用户';
 $('profileSummaryMeta').textContent='@'+user.username+' · '+$('profileRole').textContent;$('profileCreatedAt').textContent=new Date(user.created_at).toLocaleString();renderOwnAvatar();
}
async function showPersonalCenter(tab){if(!personalRoute)personalReturnRoute=lastPersonalOrigin;personalRoute='#/account/'+tab;personalUserID=state.user.id;
 for(const key of ['profile','storage','security','sessions']){$('account-'+key).hidden=key!==tab;const link=document.querySelector('[data-account-tab="'+key+'"]');link.classList.toggle('active',key===tab);if(key===tab)link.setAttribute('aria-current','page');else link.removeAttribute('aria-current')}
 fillPersonalProfile(state.user);if(tab==='sessions')await loadPersonalSessions();else if(tab==='storage')await loadPersonalStorage();else if(tab==='security')await loadEmailProfile();else await loadPersonalProfile();
}
async function loadPersonalProfile(){const generation=++personalGeneration,owner=state.user?.id;try{const identity=await api('auth/me');if(generation!==personalGeneration||owner!==state.user?.id)return;applyPersonalIdentity(identity);fillPersonalProfile(identity.user);await Promise.all([loadOwnAvatar(),loadPersonalStorage()])}catch(error){if(owner===state.user?.id)personalError('profilePageError',error.message)}}
async function loadPersonalStorage(){const owner=state.user?.id,generation=personalGeneration;try{const [storage,view]=await Promise.all([api('auth/storage'),api('auth/email')]);if(owner!==state.user?.id||generation!==personalGeneration)return;
 $('personalStorageStatus').hidden=false;const limit=storage.limit_bytes,used=storage.used_bytes;$('accountCapacitySummary').textContent='音频占用 '+storageBytes(used)+' / '+(limit?storageBytes(limit):'不限额');
 $('personalStorageMetrics').replaceChildren();for(const [label,value]of [['已使用',storageBytes(used)],['容量额度',limit?storageBytes(limit):'不限额'],['剩余可用',limit?storageBytes(Math.max(0,limit-used)):'不限额']]){const n=document.createElement('div');const title=document.createElement('span'),strong=document.createElement('strong');title.textContent=label;strong.textContent=value;n.append(title,strong);$('personalStorageMetrics').append(n)}
 $('personalStorageProgress').hidden=!limit;$('personalStorageProgress').value=limit?Math.min(100,used/limit*100):0;
 $('personalStorageStatus').textContent=limit&&used>=limit?'已达到容量额度，请联系管理员调整。':'按原始上传者统计音频占用。';
 $('personalUploadStatus').textContent=limit&&used>=limit?'容量不足，暂时无法新增上传。':view.email_verification_required!==false&&!view.email_verified?'验证邮箱后可以上传。':'当前可以按账户配额上传。';
 }catch(error){if(owner===state.user?.id)personalError('personalStorageStatus',error.message)}}
function requestPersonalClose(){const e=personalEdit;if(!e||e.busy||avatarWorkspace.busy||emailBusy)return;e.afterDiscard=null;if(!personalDirty()){closePersonalEditor();return}$('personalDiscard').hidden=false;$('continuePersonalEdit').focus()}
function closePersonalEditor(force=false){const e=personalEdit;if(!e)return;if(!force&&(e.busy||avatarWorkspace.busy||emailBusy))return;personalEdit=null;personalEditEpoch++;if(e.type==='email'){emailProfileGeneration++;emailEpoch++;emailBusy=false}discardAvatarDraft();resetPersonalPassword();e.dialog.close();e.dialog.remove()}
async function openPersonalEditor(type){
 if(!state.user||personalEdit)return;++personalGeneration;const dialog=document.createElement('dialog');dialog.id='personalEditDialog';dialog.className='modal personal-edit-dialog';dialog.dataset.type=type;
 const e={dialog,type,openingHash:location.hash,owner:state.user.id,epoch:++personalEditEpoch,busy:false,loading:true,originalName:''};personalEdit=e;
 dialog.innerHTML='<div class="modal-head"><h3>'+({profile:'编辑资料',email:'绑定／更换邮箱',password:'修改密码'}[type])+'</h3><button id="closePersonalEdit" class="btn" type="button">关闭</button></div><div id="personalEditBody" class="modal-body"></div><div id="personalDiscard" class="notice" hidden><p>有尚未保存的修改，是否放弃？</p><button id="discardPersonalEdit" class="btn danger" type="button">放弃修改</button><button id="continuePersonalEdit" class="btn" type="button">继续编辑</button></div><div class="modal-foot"><button id="cancelPersonalEdit" class="btn" type="button">取消</button></div>';
 document.body.append(dialog);$('closePersonalEdit').onclick=$('cancelPersonalEdit').onclick=requestPersonalClose;$('continuePersonalEdit').onclick=()=>{e.afterDiscard=null;$('personalDiscard').hidden=true};$('discardPersonalEdit').onclick=()=>{const after=e.afterDiscard;closePersonalEditor();if(!personalEdit&&after)void after()};
 dialog.addEventListener('cancel',event=>{event.preventDefault();requestPersonalClose()});dialog.showModal();
 $('personalEditBody').textContent='正在读取…';
 try{
 if(type==='profile'){
 const [identity,avatar]=await Promise.all([api('auth/me'),api('auth/avatar')]);if(!personalEditCurrent(e))return;e.originalName=identity.user.display_name||identity.user.username;e.userVersion=identity.user.version;
 avatarWorkspace.owner=e.owner;avatarWorkspace.epoch++;avatarWorkspace.metadata=avatar;avatarWorkspace.version=avatar.version;avatarWorkspace.loading=false;
 $('personalEditBody').innerHTML='<form id="profileForm" novalidate><div class="field"><label for="profileName">昵称</label><input id="profileName" autocomplete="nickname" maxlength="256"></div><p id="profileError" class="form-error" hidden></p><div class="actions"><button id="saveProfile" class="btn primary" type="submit">保存更改</button><button id="reloadProfile" class="btn" type="button">重新读取资料</button></div></form>';
 $('profileName').value=e.originalName;$('profileName').oninput=updateAvatarControls;setupAvatarProfile();$('profileForm').onsubmit=savePersonalProfile;$('reloadProfile').onclick=()=>{if(personalDirty()){e.afterDiscard=()=>openPersonalEditor('profile');$('personalDiscard').hidden=false;return}closePersonalEditor();void openPersonalEditor('profile')};
 }else if(type==='password'){
 $('personalEditBody').innerHTML='<form id="accountPasswordForm" novalidate><p class="notice">修改后需要重新登录，播放器 Key 保留。</p>'+[['accountOldPassword','当前密码','current-password'],['accountNewPassword','新密码','new-password'],['accountConfirmPassword','确认新密码','new-password']].map(([id,name,autocomplete])=>'<div class="field"><label for="'+id+'">'+name+'</label><input id="'+id+'" type="password" autocomplete="'+autocomplete+'" maxlength="1024"></div>').join('')+'<button id="toggleAccountPassword" class="btn" type="button" aria-pressed="false">显示密码</button><p id="accountPasswordError" class="form-error" hidden></p><button id="saveAccountPassword" class="btn primary">修改并重新登录</button></form>';
 $('accountPasswordForm').onsubmit=savePersonalPassword;$('toggleAccountPassword').onclick=()=>{const show=$('accountNewPassword').type==='password';for(const id of personalPasswordFields)$(id).type=show?'text':'password';$('toggleAccountPassword').textContent=show?'隐藏密码':'显示密码';$('toggleAccountPassword').setAttribute('aria-pressed',String(show))};
 }else{setupEmailEditor();setEmailBusy(true);try{await loadEmailProfile(true)}finally{if(personalEditCurrent(e))setEmailBusy(false)}}
 }catch(error){if(personalEditCurrent(e)){$('personalEditBody').textContent='读取失败：'+error.message}}
 finally{if(personalEditCurrent(e)){e.loading=false;updateProfileSaveState()}}
}
function updateProfileSaveState(){const e=personalEdit;if(e?.type==='profile'&&$('saveProfile'))$('saveProfile').disabled=e.busy||e.loading||avatarWorkspace.decoding||!personalDirty()}
function setPersonalBusy(busy){personalBusy=busy;const e=personalEdit;if(e){e.busy=busy;if(e.type==='profile')avatarWorkspace.busy=busy;e.dialog.querySelectorAll('input,select,button').forEach(n=>n.disabled=busy);updateProfileSaveState()}if($('revokeOtherSessions'))$('revokeOtherSessions').disabled=busy||!personalOtherSessions;document.querySelectorAll('[data-revoke-session]').forEach(b=>b.disabled=busy)}
async function savePersonalProfile(event){event.preventDefault();const e=personalEdit;if(!e||e.busy||e.loading||avatarWorkspace.decoding)return;const name=$('profileName').value.trim();if(!name||Array.from(name).length>128){personalError('profileError','请输入1–128个字符的昵称。');return}
 setPersonalBusy(true);try{const form=new FormData();form.set('display_name',name);await appendAvatarProfileDraft(form);if(!personalEditCurrent(e))return;const identity=await api('auth/profile','PUT',form,undefined,e.userVersion);if(!personalEditCurrent(e))return;++personalGeneration;avatarWorkspace.metadata=identity.avatar;avatarWorkspace.version=identity.avatar.version;applyPersonalIdentity(identity);fillPersonalProfile(identity.user);e.busy=false;personalBusy=false;avatarWorkspace.busy=false;closePersonalEditor();setStatus('资料已保存','ok')}
 catch(error){if(personalEditCurrent(e))personalError('profileError',error.status===409?'资料已变化，草稿保留。请取消后重新读取。':error.message)}finally{if(personalEditCurrent(e))setPersonalBusy(false)}}
async function savePersonalPassword(event){event.preventDefault();const e=personalEdit;if(!e||e.busy)return;const old=$('accountOldPassword').value,next=$('accountNewPassword').value,confirm=$('accountConfirmPassword').value;
 const error=!old?'请输入当前密码。':next!==confirm?'两次新密码不一致。':Array.from(next).length<12||new TextEncoder().encode(next).length>1024?'新密码至少12字符，最多1024字节。':old===next?'新密码须不同于当前密码。':'';if(error){personalError('accountPasswordError',error);return}
 setPersonalBusy(true);try{await api('auth/password','PUT',{old_password:old,new_password:next});if(!personalEditCurrent(e))return;setLoggedOut();$('loginError').textContent='密码已修改，请重新登录。'}catch(error){if(personalEditCurrent(e))personalError('accountPasswordError',error.message)}finally{if(personalEditCurrent(e))setPersonalBusy(false)}}
function accountLabel(user) {
  return (user.display_name||user.username)+(user.username?' (@'+user.username+')':'');
}
function applyPersonalIdentity(identity) {
  const user=identity.user;
  if(state.user?.id!==user.id)return;
  state.user=user;state.csrf=identity.csrf_token||state.csrf;
  $('accountName').textContent=user.display_name||user.username;
  if(typeof renderOwnAvatar==='function')renderOwnAvatar();
  state.users=(state.users||[]).map(item=>item.id===user.id?user:item);
  const update=playlist=>{if(playlist?.owner_id===user.id)playlist.source_owner_name=user.display_name};
  state.playlists.forEach(update);update(state.publicDetail?.playlist);update(state.sharingPlaylist);
  for(const invite of state.receivedInvitations||[]){if(invite.inviter_id===user.id)invite.inviter_name=user.display_name;if(invite.recipient_id===user.id)invite.recipient_display_name=user.display_name;}
  // Cached catalog payloads may still contain the old author after a self edit.
  for(const cached of communityCache.values())for(const item of cached.data.items||[])update(item);
  communityCache.clear();
  if(isAdministrator()){renderUsers();if(typeof renderManagedUserPage==='function')renderManagedUserPage()}
}

async function loadPersonalSessions() {
  const generation=++personalGeneration,userID=state.user?.id;
  $('accountSessionStatus').textContent='正在读取登录…';personalError('accountSessionError');
  try {
    const sessions=await api('auth/sessions');
    if(generation!==personalGeneration||state.user?.id!==userID||personalRoute!=='#/account/sessions')return;
    $('accountSessionList').replaceChildren();
    for(const session of sessions){
      const row=document.createElement('article');row.className='panel session-card';row.dataset.current=String(session.current);
      const info=document.createElement('div'),title=document.createElement('strong'),details=document.createElement('p');
      title.textContent=session.current?'当前登录':'其他网页登录';details.className='status';
      details.textContent='创建：'+new Date(session.created_at).toLocaleString()+' · 最近活动：'+new Date(session.last_active_at).toLocaleString()+' · 到期：'+new Date(session.expires_at).toLocaleString();
      info.append(title,details);const button=makeButton(session.current?'退出当前登录':'退出该登录',()=>mutatePersonalSessions(session),'btn danger');button.dataset.revokeSession=session.id;
      row.append(info,button);$('accountSessionList').append(row);
    }
    $('accountSessionStatus').textContent=sessions.length?'共 '+sessions.length+' 个有效登录':'暂无有效登录。';
    personalOtherSessions=sessions.filter(s=>!s.current).length;
    $('revokeOtherSessions').disabled=personalBusy||!personalOtherSessions;
  }catch(error){if(generation===personalGeneration&&state.user?.id===userID){$('accountSessionStatus').textContent='';personalError('accountSessionError',error.message)}}
}
async function mutatePersonalSessions(session) {
  if(personalBusy)return;
  if(!confirm(session?'退出'+(session.current?'当前':'该')+'网页登录？':'退出本账号的其他所有网页登录？当前登录和播放器 Key 将保留。'))return;
  const userID=state.user?.id;setPersonalBusy(true);personalError('accountSessionError');
  try {
    const result=session?await api('auth/sessions/'+encodeURIComponent(session.id),'DELETE'):await api('auth/sessions/revoke-others','POST');
    if(state.user?.id!==userID)return;
    if(session?.current){setLoggedOut();return}
    await loadPersonalSessions();setStatus(session?'该登录已退出':'已退出 '+result.revoked+' 个其他登录','ok');
  }catch(error){if(state.user?.id===userID)personalError('accountSessionError',error.message)}
  finally{setPersonalBusy(false)}
}
