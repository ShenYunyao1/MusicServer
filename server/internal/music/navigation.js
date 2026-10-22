'use strict';

const pageRoutes = {tracks:'library', playlists:'playlists', upload:'uploads', keys:'player/keys', stats:'player/stats', overview:'admin/overview', users:'admin/users', maintenance:'admin/library/scan', settings:'admin/settings/general'};
const adminPages = new Set(['overview', 'users', 'maintenance', 'settings']);
Object.assign(titles, {overview:'管理概览', tracks:'全局曲库', playlists:'我的歌单', upload:'上传任务', keys:'播放器接入', stats:'播放器接入', users:'用户管理', settings:'系统设置', maintenance:'曲库维护'});

function makeButton(label, action, className = 'btn') {
  const button = document.createElement('button');
  button.type = 'button'; button.className = className; button.textContent = label; button.onclick = action;
  return button;
}
function routeLink(label, path) {
  const a = document.createElement('a'); a.href = '#/' + path; a.textContent = label; a.className = 'btn'; return a;
}

const nav = document.querySelector('.nav');
const trackIntro=$('page-tracks').querySelector('.page-intro');trackIntro.append(routeLink('上传曲目','uploads'));
const trackCount=document.createElement('p');trackCount.id='trackCount';trackCount.className='status';$('page-tracks').querySelector('.track-toolbar').append(trackCount);
const trackBulk=document.createElement('div');trackBulk.id='trackBulkActions';trackBulk.className='bulk-actions';trackBulk.hidden=true;
const bulkAdd=makeButton('加入歌单',()=>openAddTracks([...state.selectedTrackIds]),'btn primary');bulkAdd.id='bulkAdd';
const bulkCancel=makeButton('取消选择',()=>{state.selectedTrackIds.clear();renderTracks()});
const bulkDelete=makeButton('删除所选曲目',()=>deleteSelectedTracks(),'btn danger');bulkDelete.id='bulkDelete';
trackBulk.append(bulkAdd,bulkCancel,bulkDelete);$('page-tracks').querySelector('.panel').before(trackBulk);
const trackDetails=document.createElement('dialog');trackDetails.id='trackDetails';trackDetails.className='modal drawer';
trackDetails.innerHTML='<div class="modal-head"><h3>曲目详情</h3><button class="btn small" aria-label="关闭曲目详情">×</button></div><div id="trackDetailsBody" class="modal-body"></div>';
trackDetails.querySelector('button').onclick=()=>trackDetails.close();document.body.append(trackDetails);
trackDetails.addEventListener('close',()=>{$('trackDetailsBody').replaceChildren()});
const clearCompleted=makeButton('清理已完成',()=>run(async()=>{for(const upload of [...state.uploads].filter(x=>['完成','已去重'].includes(x.status)))await removeUpload(upload)}));
clearCompleted.id='clearCompleted';$('page-upload').querySelector('.queue-actions').prepend(clearCompleted);
window.addEventListener('beforeunload',event=>{if(state.user&&state.uploads.some(x=>!['完成','已去重'].includes(x.status))){event.preventDefault();event.returnValue=''}});
const groups = [['音乐', ['tracks','playlists','upload']], ['播放器',['keys']], ['管理',['overview','users','maintenance','settings']]];
const maintenanceNav = makeButton('', () => showPage('maintenance'));
maintenanceNav.dataset.page = 'maintenance';
maintenanceNav.className='';
maintenanceNav.innerHTML = '<span class="nav-icon">⌁</span><span class="nav-label">曲库维护</span>';
nav.append(maintenanceNav);
for (const [label,pages] of groups) {
  const group = document.createElement('div'); group.className = 'nav-group';
  if (label === '管理') group.dataset.admin = 'true';
  const heading = document.createElement('p'); heading.className = 'nav-heading'; heading.textContent = label; group.append(heading);
  for (const page of pages) {
    const button = nav.querySelector('[data-page="'+page+'"]');
    button.querySelector('.nav-label').textContent = titles[page]; button.title = titles[page];
    button.onclick = () => showPage(page); group.append(button);
  }
  nav.append(group);
}
// Statistics belongs to player setup; one set of tabs is shared by its two views.
const statsNav = nav.querySelector('[data-page="stats"]'); statsNav.remove();
const playerTabs = document.createElement('nav'); playerTabs.id = 'playerTabs'; playerTabs.className = 'page-tabs'; playerTabs.hidden = true;
const keyTab = routeLink('API Key','player/keys');
statsNav.textContent = '调用统计'; statsNav.className = 'btn'; playerTabs.append(keyTab, statsNav);
document.querySelector('.content').prepend(playerTabs);
const connectionChecks=$('keyTests').closest('.panel');connectionChecks.querySelector('h3').textContent='连接检查（需输入完整 Key）';$('page-keys').append(connectionChecks);
const playerHelp=document.createElement('div');playerHelp.className='notice';playerHelp.innerHTML='<strong>连接 MusicPlayer2</strong><p>服务器地址：<span id="playerServerAddress"></span></p><p>在播放器中填写服务器地址和完整 API Key，保存后测试连接。未授权任何歌单的 Key 可以连接，但曲库为空。</p>';
playerHelp.querySelector('#playerServerAddress').textContent=location.origin;$('page-keys').querySelector('.page-intro').after(playerHelp);
const secretAddress=document.createElement('p');secretAddress.className='notice';secretAddress.textContent='服务器地址：'+location.origin+(['localhost','127.0.0.1','[::1]'].includes(location.hostname)?'。其他设备请改用服务器的局域网 IP。':'');$('secretValue').before(secretAddress);

const accountMenu = document.createElement('details'); accountMenu.id = 'accountMenu'; accountMenu.className = 'account-menu';
const accountSummary = document.createElement('summary'); accountSummary.append($('accountName'));
const roleLabel = document.createElement('span'); roleLabel.id = 'accountRole'; roleLabel.className = 'badge'; accountSummary.append(roleLabel);
const accountActions = document.createElement('div'); accountActions.className = 'account-actions';
for (const id of ['changePassword','manageSessions','logout']) {
  const button = $(id); accountActions.append(button); button.addEventListener('click',()=>accountMenu.open=false);
}
accountMenu.append(accountSummary,accountActions); document.querySelector('.top-actions').prepend(accountMenu);
const menuToggle = makeButton('☰',()=>{const open=document.body.classList.toggle('mobile-nav-open');menuToggle.setAttribute('aria-expanded',String(open))});
menuToggle.id='menuToggle';menuToggle.setAttribute('aria-label','打开导航');menuToggle.setAttribute('aria-controls','sidebar');menuToggle.setAttribute('aria-expanded','false');
document.querySelector('.topbar').prepend(menuToggle);
const navBackdrop = makeButton('',()=>closeMobileNav());navBackdrop.id='navBackdrop';navBackdrop.setAttribute('aria-label','关闭导航');document.querySelector('.app').append(navBackdrop);
function closeMobileNav(){document.body.classList.remove('mobile-nav-open');menuToggle.setAttribute('aria-expanded','false')}
document.addEventListener('keydown',e=>{if(e.key==='Escape'){closeMobileNav();accountMenu.open=false}});

const previewBar=document.createElement('div');previewBar.id='previewBar';previewBar.className='preview-bar';previewBar.hidden=true;
const previewTitle=document.createElement('strong');previewTitle.id='previewTitle';
const closePreview=makeButton('×',()=>{memberPlayer.pause();memberPlayer.removeAttribute('src');memberPlayer.load();previewBar.hidden=true});closePreview.setAttribute('aria-label','关闭播放预览');
previewBar.append(previewTitle,memberPlayer,closePreview);document.querySelector('.app').append(previewBar);

const maintenancePage=document.createElement('section');maintenancePage.id='page-maintenance';maintenancePage.className='page';
maintenancePage.innerHTML='<div class="page-intro"><h2>曲库维护</h2></div>';
maintenancePage.append(maintenancePanel);document.querySelector('.content').append(maintenancePage);
maintenancePanel.querySelector('.actions').prepend($('scan'));
const adminStatsLink=routeLink('全站调用统计','admin/stats');adminStatsLink.id='adminStatsLink';$('page-overview').querySelector('.actions').append(adminStatsLink);
const statsScopeLabel=document.createElement('p');statsScopeLabel.id='statsScopeLabel';statsScopeLabel.className='notice';$('page-stats').querySelector('.page-intro').after(statsScopeLabel);

const routeError = document.createElement('section');routeError.id='routeError';routeError.className='panel';routeError.hidden=true;
routeError.innerHTML='<h2>无法打开此页面</h2><p id="routeErrorMessage"></p>';routeError.append(routeLink('返回全局曲库','library'));
document.querySelector('.content').append(routeError);

function navigatePage(page) {
  if (!pageRoutes[page]) return;
  const hash='#/'+pageRoutes[page];
  if(location.hash===hash) restoreWorkspaceRoute(); else location.hash=hash;
}
function displayWorkspacePage(page) {
  hideContextMenu(); closeMobileNav(); routeError.hidden=true;
  state.page=page;
  if(page!=='tracks')state.selectedTrackIds.clear();
  document.querySelectorAll('.page').forEach(el=>el.classList.toggle('active',el.id==='page-'+page));
  document.querySelectorAll('.nav [data-page]').forEach(el=>el.classList.toggle('active',el.dataset.page===page || page==='stats'&&el.dataset.page==='keys'));
  document.querySelectorAll('.nav-group[data-admin]').forEach(el=>el.hidden=!isAdministrator());
  $('accountRole').textContent=isAdministrator()?'管理员':'普通用户';
  $('pageTitle').textContent=titles[page];document.title=titles[page]+' · 音乐服务器';
  playerTabs.hidden=!['keys','stats'].includes(page)||page==='stats'&&state.statsScope==='admin'; keyTab.classList.toggle('active',page==='keys');statsNav.classList.toggle('active',page==='stats');
  if(page==='stats'){
    const adminScope=state.statsScope==='admin';$('pageTitle').textContent=adminScope?'管理 / 调用统计':'播放器接入 / 调用统计';
    statsScopeLabel.textContent=adminScope?'统计范围：'+(state.statsUserID?(state.users||[]).find(u=>u.id===state.statsUserID)?.username||'指定账户':'全部账户'):'统计范围：仅当前账户的播放器 Key';
  }
  document.querySelectorAll('.page-intro h2').forEach(el=>{const section=el.closest('.page');if(section&&titles[section.id.slice(5)])el.textContent=titles[section.id.slice(5)]});
  $('page-stats').querySelector('h2').textContent='调用统计';
}
let routeGeneration=0;
async function restoreWorkspaceRoute() {
  if(!state.user || state.user.must_change_password)return;
  const generation=++routeGeneration;
  if(!location.hash || location.hash==='#/') {history.replaceState(null,'','#/'+pageRoutes[isAdministrator()?'overview':'tracks']);}
  const path=location.hash.slice(2).split('?')[0].split('/');
  let page=Object.keys(pageRoutes).find(key=>pageRoutes[key]===path.join('/'));
  if(path[0]==='playlists'&&path.length===2)page='playlists';
  if(path[0]==='admin'&&path[1]==='users'&&path.length===3)page='users';
  if(path[0]==='admin'&&path[1]==='library')page='maintenance';
  if(path[0]==='admin'&&path[1]==='settings')page='settings';
  if(path[0]==='admin'&&path[1]==='stats'&&path.length===2)page='stats';
  try {
    if(!page || (adminPages.has(page)||path[0]==='admin')&&!isAdministrator())throw new Error('页面不存在或当前账户无权访问。');
    state.statsScope=path[0]==='admin'?'admin':'self';
    state.statsUserID=page==='stats'&&state.statsScope==='admin'?new URLSearchParams(location.hash.split('?')[1]||'').get('user')||'':'';
    renderStatsUsers();
    displayWorkspacePage(page);
    if(page==='playlists') {
      state.playlistDetailId=path[1]||null;
      if(state.playlistDetailId&&!state.playlists.some(p=>p.id===state.playlistDetailId&&!p.system))throw new Error('歌单不存在或当前账户无权访问。');
      renderPlaylists();
    }
    if(page==='tracks'){
      const query=new URLSearchParams(location.hash.split('?')[1]||'');$('trackSearch').value=query.get('q')||'';
      state.trackFilter=['all','mine','managed','orphan'].includes(query.get('filter'))?query.get('filter'):'all';
      document.querySelectorAll('[data-filter]').forEach(el=>el.classList.toggle('active',el.dataset.filter===state.trackFilter));renderTracks();
    }
    if(page==='keys')await loadKeys();
    if(page==='stats')await loadStats();
    if(page==='overview'){renderOverview();await loadStats(true)}
    if(page==='settings'){await loadSettings();await loadUpdate()}
    if(page==='maintenance')await pollScan();
    if(page==='users') {
      state.managedUserID=path[2]||null;await loadUsers();
      if(path[2]&&!managedUser())throw new Error('用户不存在或无法读取。');
    }
  } catch(error) {
    if(generation!==routeGeneration||!state.user)return;
    document.querySelectorAll('.page').forEach(el=>el.classList.remove('active'));
    playerTabs.hidden=true;routeError.hidden=false;$('routeErrorMessage').textContent=error.message;
  }
}
window.addEventListener('hashchange',()=>restoreWorkspaceRoute());
// The layout must be ready before restoring a browser login.
api('auth/me').then(enterAccount).catch(()=>setLoggedOut());
