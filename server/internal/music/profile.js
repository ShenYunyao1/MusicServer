'use strict';

let personalRoute='', personalUserID=null, personalOriginalName='', personalVersion=0;
let personalGeneration=0, personalBusy=false, personalReturnRoute='#/library', lastPersonalOrigin='#/library';
let personalOtherSessions=0;

function setupProfileWorkspace() {
  const page=document.createElement('section');page.id='page-account';page.className='page';
  page.innerHTML=`<button id="accountBack" class="btn page-back">← 返回</button>
    <div class="page-intro"><div><h1>个人中心</h1><p>管理你的资料、密码和网页登录。</p></div></div>
    <nav class="page-tabs" aria-label="个人中心页签">
      <a class="btn" href="#/account/profile" data-account-tab="profile">基本资料</a>
      <a class="btn" href="#/account/security" data-account-tab="security">账户安全</a>
      <a class="btn" href="#/account/sessions" data-account-tab="sessions">登录管理</a>
    </nav>
    <section id="account-profile" class="panel account-panel" aria-label="基本资料">
      <div class="profile-summary"><span id="profileAvatar" class="profile-avatar" aria-hidden="true"></span><div><strong id="profileHeading"></strong><p class="status">昵称用于展示，登录名用于登录和指定账户分享。</p></div></div>
      <form id="profileForm" novalidate>
        <div class="field"><label for="profileName">昵称</label><input id="profileName" autocomplete="nickname" maxlength="256" aria-describedby="profileNameHint profileError"><small id="profileNameHint">1–128 个字符，支持中文，允许与其他人重名。</small></div>
        <div class="field"><label for="profileUsername">登录名</label><input id="profileUsername" readonly autocomplete="username"></div>
        <dl class="profile-facts"><div><dt>账户角色</dt><dd id="profileRole"></dd></div><div><dt>注册时间</dt><dd><time id="profileCreatedAt"></time></dd></div></dl>
        <p id="profileError" class="form-error" role="alert" hidden></p>
        <div class="actions"><button id="saveProfile" class="btn primary" type="submit">保存资料</button><button id="reloadProfile" class="btn" type="button">重新读取资料</button><a class="btn" href="#/player/keys">播放器接入</a></div>
      </form>
    </section>
    <section id="account-security" class="panel account-panel" aria-label="账户安全" hidden>
      <p class="notice">修改密码后需要重新登录，播放器连接不受影响。</p>
      <form id="accountPasswordForm" novalidate>
        <div class="field"><label for="accountOldPassword">当前密码</label><input id="accountOldPassword" type="password" autocomplete="current-password" maxlength="1024"></div>
        <div class="field"><label for="accountNewPassword">新密码</label><input id="accountNewPassword" type="password" autocomplete="new-password" maxlength="1024" aria-describedby="accountPasswordHint"><small id="accountPasswordHint">至少 12 个字符，最多 1024 个 UTF-8 字节，须与当前密码不同。</small></div>
        <div class="field"><label for="accountConfirmPassword">确认新密码</label><input id="accountConfirmPassword" type="password" autocomplete="new-password" maxlength="1024"></div>
        <button id="toggleAccountPassword" class="btn small" type="button" aria-pressed="false">显示密码</button>
        <p id="accountPasswordError" class="form-error" role="alert" hidden></p>
        <div class="actions"><button id="saveAccountPassword" class="btn primary" type="submit">修改并重新登录</button></div>
      </form>
    </section>
    <section id="account-sessions" class="account-panel" aria-label="登录管理" hidden>
      <div class="toolbar"><p class="status">这里管理网页登录；播放器 API Key 不受影响。</p><div class="actions"><button id="refreshAccountSessions" class="btn">刷新登录</button><button id="revokeOtherSessions" class="btn danger">退出其他所有登录</button></div></div>
      <p id="accountSessionError" class="form-error" role="alert" hidden></p><p id="accountSessionStatus" class="status" role="status"></p><div id="accountSessionList"></div>
    </section>`;
  document.querySelector('.content').append(page);
  setupEmailProfile();
  if(typeof setupAvatarProfile==='function')setupAvatarProfile();
  const entry=makeButton('个人中心',()=>navigatePage('account'));entry.id='personalCenter';
  entry.addEventListener('click',()=>accountMenu.open=false);accountActions.prepend(entry);
  $('changePassword').onclick=()=>location.hash='#/account/security';
  $('manageSessions').textContent='登录管理';$('manageSessions').onclick=()=>location.hash='#/account/sessions';
  $('accountBack').onclick=()=>location.hash=personalReturnRoute;
  $('profileForm').onsubmit=savePersonalProfile;
  $('reloadProfile').onclick=()=>{if(!personalBusy&&confirmPersonalExit())loadPersonalProfile(true)};
  $('accountPasswordForm').onsubmit=savePersonalPassword;
  $('toggleAccountPassword').onclick=()=>{
    const show=$('accountNewPassword').type==='password';
    for(const id of personalPasswordFields)$(id).type=show?'text':'password';
    $('toggleAccountPassword').textContent=show?'隐藏密码':'显示密码';$('toggleAccountPassword').setAttribute('aria-pressed',String(show));
  };
  $('refreshAccountSessions').onclick=()=>loadPersonalSessions();
  $('revokeOtherSessions').onclick=()=>mutatePersonalSessions(null);
  window.addEventListener('beforeunload',event=>{if(personalDirty()){event.preventDefault();event.returnValue=''}});
}
const personalPasswordFields=['accountOldPassword','accountNewPassword','accountConfirmPassword'];
function personalError(id,message='') {$(id).textContent=message;$(id).hidden=!message;}
function personalDirty() {
  if(!state.user||!personalRoute||state.page!=='account')return false;
  return personalRoute==='#/account/profile' ? $('profileName').value!==personalOriginalName||emailProfileDirty()||(typeof avatarDirty==='function'&&avatarDirty()) :
    personalRoute==='#/account/security' && personalPasswordFields.some(id=>$(id).value!=='');
}
function confirmPersonalExit() {
  if(personalBusy||(typeof avatarWorkspace!=='undefined'&&avatarWorkspace.busy))return false;
  return !personalDirty()||confirm('个人中心有尚未保存的输入，放弃修改并离开？');
}
function resetPersonalPassword() {
  $('accountPasswordForm').reset();personalPasswordFields.forEach(id=>$(id).type='password');
  $('toggleAccountPassword').textContent='显示密码';$('toggleAccountPassword').setAttribute('aria-pressed','false');
}
function clearProfileWorkspace() {
  if(typeof clearAvatarWorkspace==='function')clearAvatarWorkspace();
  ++personalGeneration;personalBusy=false;personalRoute='';personalUserID=null;personalOriginalName='';personalVersion=0;personalOtherSessions=0;
  personalReturnRoute=lastPersonalOrigin='#/library';
  if(!$('page-account'))return;
  $('profileForm').reset();resetPersonalPassword();
  for(const id of ['profileAvatar','profileHeading','profileRole','profileCreatedAt','accountSessionStatus'])$(id).textContent='';
  $('accountSessionList').replaceChildren();
  for(const id of ['profileError','accountPasswordError','accountSessionError'])personalError(id);
  setPersonalBusy(false);
}
function permitPersonalNavigation(hash) {
  if(!personalRoute||hash===personalRoute)return true;
  if(!confirmPersonalExit()){history.replaceState(null,'',personalRoute);return false}
  if(typeof discardAvatarDraft==='function')discardAvatarDraft();
  personalRoute='';personalOriginalName='';resetPersonalPassword();++personalGeneration;return true;
}
function notePersonalNavigation(hash,page) {
  if(page!=='account')lastPersonalOrigin=hash;
}
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
function fillPersonalProfile(user) {
  personalOriginalName=user.display_name||user.username;personalVersion=user.version;
  $('profileName').value=personalOriginalName;$('profileUsername').value=user.username;
  $('profileHeading').textContent=accountLabel(user);
  $('profileAvatar').textContent=Array.from(personalOriginalName.trim())[0]||'♪';
  if(typeof renderOwnAvatar==='function')renderOwnAvatar();
  $('profileRole').textContent=user.role==='admin'?'管理员':'普通用户';
  $('profileCreatedAt').textContent=new Date(user.created_at).toLocaleString();$('profileCreatedAt').dateTime=user.created_at;
}
async function showPersonalCenter(tab) {
  const route='#/account/'+tab;
  if(personalRoute===route&&personalUserID===state.user.id&&personalDirty())return;
  if(!personalRoute)personalReturnRoute=lastPersonalOrigin;
  personalRoute=route;personalUserID=state.user.id;++personalGeneration;
  for(const name of ['profile','security','sessions']){
    $('account-'+name).hidden=name!==tab;
    const link=document.querySelector('[data-account-tab="'+name+'"]');link.classList.toggle('active',name===tab);
    if(name===tab)link.setAttribute('aria-current','page');else link.removeAttribute('aria-current');
  }
  for(const id of ['profileError','accountPasswordError','accountSessionError'])personalError(id);
  if(tab==='profile'){fillPersonalProfile(state.user);await loadPersonalProfile()}
  if(tab==='security')resetPersonalPassword();
  if(tab==='sessions')await loadPersonalSessions();
}
async function loadPersonalProfile(force=false) {
  const generation=++personalGeneration,userID=state.user?.id;
  if(force)setPersonalBusy(true);
  try {
    const identity=await api('auth/me');
    if(generation!==personalGeneration||state.user?.id!==userID||state.page!=='account')return;
    if(identity.user.must_change_password){await enterAccount(identity);return}
    applyPersonalIdentity(identity);
    if(force||!personalDirty())fillPersonalProfile(identity.user);
    await loadEmailProfile();
    if(typeof loadOwnAvatar==='function')await loadOwnAvatar(force);
    personalError('profileError');
  }catch(error){if(generation===personalGeneration&&state.user?.id===userID)personalError('profileError',error.message)}
  finally{if(force&&state.user?.id===userID)setPersonalBusy(false)}
}
function setPersonalBusy(busy) {
  personalBusy=busy;
  for(const id of ['saveProfile','reloadProfile','saveAccountPassword','refreshAccountSessions','revokeOtherSessions'])$(id).disabled=busy;
  $('revokeOtherSessions').disabled=busy||!personalOtherSessions;
  for(const id of ['profileName','toggleAccountPassword',...personalPasswordFields])$(id).disabled=busy;
  document.querySelectorAll('[data-revoke-session]').forEach(button=>button.disabled=busy);
}
async function savePersonalProfile(event) {
  event.preventDefault();if(personalBusy)return;personalError('profileError');
  const name=$('profileName').value.trim(),userID=state.user?.id;
  if(!name||Array.from(name).length>128){personalError('profileError','请输入 1–128 个字符的昵称。');$('profileName').focus();return}
  setPersonalBusy(true);
  try {
    const identity=await api('auth/me','PATCH',{display_name:name},undefined,personalVersion);
    if(state.user?.id!==userID)return;
    applyPersonalIdentity(identity);fillPersonalProfile(identity.user);setStatus('昵称已保存','ok');
  }catch(error){if(state.user?.id===userID)personalError('profileError',error.message)}
  finally{setPersonalBusy(false)}
}
async function savePersonalPassword(event) {
  event.preventDefault();if(personalBusy)return;personalError('accountPasswordError');
  const old=$('accountOldPassword').value,next=$('accountNewPassword').value,confirm=$('accountConfirmPassword').value,userID=state.user?.id;
  const error=!old?'请输入当前密码。':next!==confirm?'两次新密码不一致。':Array.from(next).length<12||new TextEncoder().encode(next).length>1024?'新密码至少 12 个字符，最多 1024 个 UTF-8 字节。':old===next?'新密码须与当前密码不同。':'';
  if(error){personalError('accountPasswordError',error);return}
  setPersonalBusy(true);
  try {
    await api('auth/password','PUT',{old_password:old,new_password:next});
    if(state.user?.id!==userID)return;
    setLoggedOut();$('loginError').textContent='密码已修改，请重新登录。';
  }catch(error){if(state.user?.id===userID)personalError('accountPasswordError',error.message)}
  finally{setPersonalBusy(false)}
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
