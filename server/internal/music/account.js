'use strict';

const firstPasswordScreen=document.createElement('section');
firstPasswordScreen.id='firstPasswordScreen';firstPasswordScreen.className='login-screen';firstPasswordScreen.hidden=true;
firstPasswordScreen.innerHTML='<form id="firstPasswordForm" class="panel login-card"><h1>设置个人密码</h1><p>首次登录或密码重置后，需要先修改临时密码。</p><div class="field"><label for="firstOldPassword">临时密码</label><input id="firstOldPassword" type="password" autocomplete="current-password" required></div><div class="field"><label for="firstNewPassword">新密码（至少 12 个字符）</label><input id="firstNewPassword" type="password" autocomplete="new-password" minlength="12" required></div><div class="field"><label for="firstConfirmPassword">确认新密码</label><input id="firstConfirmPassword" type="password" autocomplete="new-password" minlength="12" required></div><button id="saveFirstPassword" class="btn primary" type="submit">修改并重新登录</button> <button id="logoutFirstPassword" class="btn" type="button">退出</button><p id="firstPasswordError" class="status" role="alert"></p></form>';
document.body.append(firstPasswordScreen);
function isAdministrator(){return state.user?.role==='admin'}
function canManageTrack(track){return isAdministrator() || track.owner_id===state.user?.id}
for(const [filter,label] of [['mine','我的上传'],['managed','我管理的曲目']]){
 const button=document.createElement('button');button.dataset.filter=filter;button.textContent=label;
 button.onclick=()=>{state.trackFilter=filter;document.querySelectorAll('[data-filter]').forEach(el=>el.classList.toggle('active',el===button));renderTracks()};document.querySelector('#page-tracks .filter-tabs').append(button);
}
function applyAccountRole(){
	 document.querySelectorAll('.nav-group[data-admin]').forEach(group=>group.hidden=!isAdministrator());
	 if($('accountRole'))$('accountRole').textContent=isAdministrator()?'管理员':'普通用户';
	if(typeof renderStatsUsers==='function')renderStatsUsers();
 document.querySelector('[data-page="settings"]').hidden=!isAdministrator();
 if(document.querySelector('[data-page="users"]'))document.querySelector('[data-page="users"]').hidden=!isAdministrator();
 maintenancePanel.hidden=!isAdministrator();$('scan').hidden=!isAdministrator();
 document.querySelector('[data-page="tracks"] .nav-label').textContent='全局曲库';
 document.querySelector('[data-page="playlists"] .nav-label').textContent='我的歌单';
}
$('firstPasswordForm').onsubmit=async event=>{
 event.preventDefault();$('firstPasswordError').textContent='';$('saveFirstPassword').disabled=true;
 try {
  if($('firstNewPassword').value!==$('firstConfirmPassword').value)throw new Error('两次新密码不一致');
  await api('auth/password','PUT',{old_password:$('firstOldPassword').value,new_password:$('firstNewPassword').value});
  setLoggedOut();$('loginError').textContent='密码已修改，请使用新密码登录。';
 }catch(error){$('firstPasswordError').textContent=error.message}finally{$('saveFirstPassword').disabled=false}
};
$('logoutFirstPassword').onclick=()=>run(async()=>{await api('auth/logout','POST');setLoggedOut()});
const memberPlayer=document.createElement('audio');memberPlayer.id='memberPlayer';memberPlayer.controls=true;memberPlayer.hidden=true;memberPlayer.style.maxWidth='100%';document.querySelector('.content').prepend(memberPlayer);
function playTrack(track){if(track.missing || track.removed)return;memberPlayer.src='/api/v2/media/'+encodeURIComponent(track.id);memberPlayer.hidden=false;$('previewBar').hidden=false;$('previewTitle').textContent=track.title||'未命名曲目';memberPlayer.play().catch(error=>setStatus(error.message,'error'))}

function setLoggedOut() {
  if(typeof disposePlaylistDraft==='function')disposePlaylistDraft();
  if(typeof hideNavTooltip==='function')hideNavTooltip();
  for (const upload of state.uploads) {
    invalidateLocalParse(upload);
    releaseLocalPreviewURL(upload);
  }
  for (const attachment of new Set([...state.attachments, ...state.uploads.flatMap(upload => [upload.coverFile, upload.lyricsFile]).filter(Boolean)])) releaseAttachmentURL(attachment);
  state.uploads = [];
  state.attachments = [];
  state.user = null;
  state.stats=null;state.settings={};state.playlistDetailId=null;state.trackFilter="all";state.revision=null;
  state.page='tracks';$('trackSearch').value='';$('playlistSearch').value='';
  state.users=[];state.statsUserID="";state.managedUserID=null;state.userResources=[];state.resourceOffset=0;
  $("firstPasswordScreen").hidden=true;$("firstPasswordForm").reset();
  const player=$("memberPlayer");if(player){player.pause();player.removeAttribute("src");player.load();player.hidden=true;}
  if($('previewBar'))$('previewBar').hidden=true;
  if($('previewTitle'))$('previewTitle').textContent='';
  if($('notifications'))$('notifications').replaceChildren();
  state.csrf = '';
  state.tracks = [];
  state.playlists = [];
  state.keys = [];
  state.selectedTrackIds.clear();
  clearCoverCache();
  clearTimeout(scanPollTimer);
  document.querySelectorAll('dialog[open]').forEach(dialog => dialog.close());
  document.querySelector('.app').hidden = true;
  $('loginScreen').hidden = false;
  $('accountName').textContent = '';
  for(const id of ['removedResults','missingResults','auditResults','backupResult'])if($(id))$(id).replaceChildren();
  state.statsScope='self';state.auditEntries=[];state.auditOffset=0;
  if($('userDetailHeader'))$('userDetailHeader').hidden=true;
  for(const id of ['managedUserName','managedUserStatus','userResourcesTitle','userDetailActions','accountRole','copyFeedback','editorError'])if($(id))$(id).replaceChildren();
  $('password').value = '';
  $('secretValue').textContent = '';
  $('statsTable').replaceChildren();$('keyRows').replaceChildren();$('settingsForm').replaceChildren();
  if($('userList'))$('userList').replaceChildren();if($('userResources'))$('userResources').replaceChildren();
  if($('userResourcesPanel'))$('userResourcesPanel').hidden=true;if($('statsUser'))$('statsUser').replaceChildren();
  $('modalBody').replaceChildren();
  renderUploads();
  renderTracks();
  renderPlaylists();
}

async function enterAccount(identity) {
  state.user = identity.user;
  state.csrf = identity.csrf_token;
  $('accountName').textContent = state.user.display_name || state.user.username;
  $('password').value = '';
  if(state.user.must_change_password) {
    document.querySelector('.app').hidden=true;$('loginScreen').hidden=true;$('firstPasswordScreen').hidden=false;
    $('firstPasswordForm').reset();$('firstPasswordError').textContent='';return;
  }
  $('firstPasswordScreen').hidden=true;
  applyAccountRole();
  if(isAdministrator()&&typeof loadUsers==='function')await loadUsers();
  document.querySelector('.app').hidden = false;
  $('loginScreen').hidden = true;
  setStatus('正在载入…');
  await connect();
  await restoreWorkspaceRoute();
  if (state.user) setStatus('已登录', 'ok');
}

$('loginForm').onsubmit = async event => {
  event.preventDefault();
  $('loginError').textContent = '';
  $('loginButton').disabled = true;
  try {
    const identity = await api('auth/login', 'POST', {
      username: $('username').value,
      password: $('password').value,
    });
    await enterAccount(identity);
  } catch (error) {
    $('loginError').textContent = error.message;
    $('password').value = '';
  } finally {
    $('loginButton').disabled = false;
  }
};

$('logout').onclick = () => run(async () => {
  await api('auth/logout', 'POST');
  setLoggedOut();
});

$('changePassword').onclick = () => openEditor('修改密码', {
  old_password: '', new_password: '', confirm_password: '',
}, async fields => {
  if (fields.new_password !== fields.confirm_password) throw new Error('两次新密码不一致');
  await api('auth/password', 'PUT', {
    old_password: fields.old_password, new_password: fields.new_password,
  });
  setLoggedOut();
  $('modalBody').replaceChildren();
  $('loginError').textContent = '密码已修改，请重新登录。';
});

$('manageSessions').onclick = () => run(async () => {
  const sessions = await api('auth/sessions');
  $('modalTitle').textContent = '登录会话';
  $('modalBody').replaceChildren();
  for (const session of sessions) {
    const row = document.createElement('p');
    const button = document.createElement('button');
    button.className = 'btn small danger';
    button.textContent = session.current ? '退出当前会话' : '撤销';
    button.onclick = () => run(async () => {
      await api('auth/sessions/' + encodeURIComponent(session.id), 'DELETE');
      if (session.current) setLoggedOut();
      else row.remove();
    });
    row.append(document.createTextNode(
      (session.current ? '当前会话 · ' : '') + new Date(session.created_at).toLocaleString()
      + ' · 到期 ' + new Date(session.expires_at).toLocaleString() + ' ',
    ), button);
    $('modalBody').append(row);
  }
  state.saveAction = async () => { $('editor').close(); };
  $('editor').showModal();
});

let userActive = false;
let lastActivity = 0;
for (const name of ['pointerdown', 'keydown']) {
  document.addEventListener(name, event => {
    if (event.isTrusted && state.user) userActive = true;
  }, { passive: true });
}
setInterval(() => {
  if (state.user && userActive && !document.hidden && Date.now() - lastActivity > 60000) {
    userActive = false;
    lastActivity = Date.now();
    api('auth/activity', 'POST').catch(() => {});
  }
}, 15000);


const revokeAllKeys = document.createElement('button');
revokeAllKeys.className = 'btn danger';
revokeAllKeys.id = 'revokeAllKeys';
revokeAllKeys.textContent = '撤销我的全部 Key';
revokeAllKeys.onclick = () => {
  if (confirm('撤销当前账号的全部播放器 Key？需要重新配置播放器。')) {
    run(async () => { await api('auth/keys', 'DELETE'); await loadKeys(); });
  }
};
$('page-keys').querySelector('.actions').append(revokeAllKeys);
