'use strict';

function setLoggedOut() {
  for (const upload of state.uploads) {
    invalidateLocalParse(upload);
    releaseLocalPreviewURL(upload);
  }
  for (const attachment of new Set([...state.attachments, ...state.uploads.flatMap(upload => [upload.coverFile, upload.lyricsFile]).filter(Boolean)])) releaseAttachmentURL(attachment);
  state.uploads = [];
  state.attachments = [];
  state.user = null;
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
  $('maintenanceResults').replaceChildren();
  $('password').value = '';
  $('secretValue').textContent = '';
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
  if (state.user.role !== 'admin') {
    setLoggedOut();
    $('loginError').textContent = '此管理页面仅供管理员使用。';
    return;
  }
  document.querySelector('.app').hidden = false;
  $('loginScreen').hidden = true;
  setStatus('正在载入…');
  await connect();
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

api('auth/me').then(enterAccount).catch(() => setLoggedOut());

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
