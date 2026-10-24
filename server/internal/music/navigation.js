'use strict';

const pageRoutes = {account:'account/profile',tracks:'library', public:'public/playlists', playlists:'playlists', upload:'uploads', keys:'player/keys', stats:'player/stats', overview:'admin/overview', users:'admin/users', maintenance:'admin/library/scan', logs:'admin/logs',settings:'admin/settings/general'};
titles.account='个人中心';
const adminPages = new Set(['overview', 'users', 'maintenance', 'settings','logs']);
Object.assign(titles, {overview:'管理概览', tracks:'全局曲库', playlists:'我的歌单', upload:'上传任务', keys:'播放器接入', stats:'播放器接入', users:'用户管理', settings:'系统设置', maintenance:'曲库维护'});
titles.public='公开歌单';
titles.logs='系统日志';

function makeButton(label, action, className = 'btn') {
  const button = document.createElement('button');
  button.type = 'button'; button.className = className; button.textContent = label; button.onclick = action;
  return button;
}
function routeLink(label, path) {
  const a = document.createElement('a'); a.href = '#/' + path; a.textContent = label; a.className = 'btn'; return a;
}

const nav = document.querySelector('.nav');
nav.setAttribute('aria-label','主导航');
const navIcons={
  logs:'<path d="M5 3h14v18H5zM8 7h8M8 12h8M8 17h5"/>',
  public:'<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c5 5 5 13 0 18-5-5-5-13 0-18"/>',
  tracks:'<path d="M9 18V5l11-2v13M9 8l11-2"/><ellipse cx="6" cy="18" rx="3" ry="2"/><ellipse cx="17" cy="16" rx="3" ry="2"/>',
  playlists:'<path d="M4 5h16M4 10h16M4 15h8M17 14v6"/><ellipse cx="14.5" cy="20" rx="2.5" ry="1.8"/>',
  upload:'<path d="M12 16V3m-5 5 5-5 5 5M4 15v5h16v-5"/>',
  keys:'<circle cx="8" cy="9" r="5"/><path d="m12 12 8 8m-3-3 3-3m-6 0 3-3"/>',
  overview:'<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>',
  users:'<circle cx="9" cy="7" r="3"/><path d="M3 21v-3a6 6 0 0 1 12 0v3m2-17a3 3 0 0 1 0 6m2 4a5 5 0 0 1 2 4v3"/>',
  maintenance:'<path d="M3 6h7m4 0h7M3 12h12m4 0h2M3 18h3m4 0h11"/><circle cx="12" cy="6" r="2"/><circle cx="17" cy="12" r="2"/><circle cx="8" cy="18" r="2"/>',
  settings:'<circle cx="12" cy="12" r="3"/><path d="m9 3-1 3-3 1 1 3-3 2 3 2-1 3 3 1 1 3 3-1 3 1 1-3 3-1-1-3 3-2-3-2 1-3-3-1-1-3-3 1Z"/>'
};
function sidebarSVG(shapes){return '<svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">'+shapes+'</svg>'}
$('trackFilterButton').hidden=true;
const trackFilters=$('page-tracks').querySelector('.filter-tabs');for(const name of ['all','mine','managed','orphan'])trackFilters.append(document.querySelector('[data-filter="'+name+'"]'));
const trackIntro=$('page-tracks').querySelector('.page-intro');trackIntro.append(routeLink('上传曲目','uploads'));
const trackCount=document.createElement('p');trackCount.id='trackCount';trackCount.className='status';$('page-tracks').querySelector('.track-toolbar').append(trackCount);
const trackDetails=document.createElement('dialog');trackDetails.id='trackDetails';trackDetails.className='modal drawer';
trackDetails.innerHTML='<div class="modal-head"><h3>曲目详情</h3><button class="btn small" aria-label="关闭曲目详情">×</button></div><div id="trackDetailsBody" class="modal-body"></div>';
trackDetails.querySelector('button').onclick=()=>trackDetails.close();document.body.append(trackDetails);
trackDetails.addEventListener('close',()=>{$('trackDetailsBody').replaceChildren()});
const clearCompleted=makeButton('清理已完成',()=>run(async()=>{for(const upload of [...state.uploads].filter(x=>['完成','已去重'].includes(x.status)))await removeUpload(upload)}));
clearCompleted.id='clearCompleted';$('page-upload').querySelector('.queue-actions').prepend(clearCompleted);
window.addEventListener('beforeunload',event=>{if(state.user&&state.uploads.some(x=>!['完成','已去重'].includes(x.status))){event.preventDefault();event.returnValue=''}});
const groups = [['音乐', ['tracks','public','playlists','upload']], ['播放器',['keys']], ['管理',['overview','users','maintenance','logs','settings']]];
const logsNav=makeButton('',()=>showPage('logs'));logsNav.dataset.page='logs';logsNav.innerHTML='<span class="nav-icon">▤</span><span class="nav-label">系统日志</span>';nav.append(logsNav);
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
    button.querySelector('.nav-label').textContent = titles[page];button.removeAttribute('title');button.setAttribute('aria-label',titles[page]);
    button.querySelector('.nav-icon').innerHTML=sidebarSVG(navIcons[page]);
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
$('keyTests').closest('.panel').remove();
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
menuToggle.innerHTML=sidebarSVG('<path d="M4 6h16M4 12h16M4 18h16"/>');
menuToggle.id='menuToggle';menuToggle.setAttribute('aria-label','打开导航');menuToggle.setAttribute('aria-controls','sidebar');menuToggle.setAttribute('aria-expanded','false');
document.querySelector('.topbar').prepend(menuToggle);
const brandToggle=$('brandToggle');brandToggle.setAttribute('aria-controls','sidebar');
const sidebarBreakpoint=matchMedia('(max-width: 900px)');
let sidebarCollapsed=false;
try{sidebarCollapsed=localStorage.getItem('musicserver.sidebarCollapsed')==='true'}catch(_){}
function syncSidebarControls(){
  const collapsed=sidebarCollapsed&&!sidebarBreakpoint.matches;
  document.body.classList.toggle('nav-collapsed',sidebarCollapsed);
  brandToggle.title=sidebarBreakpoint.matches?'关闭导航':collapsed?'展开导航':'收起导航';brandToggle.setAttribute('aria-label',brandToggle.title);
  if(sidebarBreakpoint.matches)brandToggle.removeAttribute('aria-expanded');else brandToggle.setAttribute('aria-expanded',String(!collapsed));
  hideNavTooltip();
}
brandToggle.onclick=()=>{if(sidebarBreakpoint.matches){closeMobileNav();menuToggle.focus();return}sidebarCollapsed=!sidebarCollapsed;try{localStorage.setItem('musicserver.sidebarCollapsed',String(sidebarCollapsed))}catch(_){}syncSidebarControls()};
sidebarBreakpoint.addEventListener('change',()=>{closeMobileNav();syncSidebarControls()});
const navTooltip=document.createElement('div');navTooltip.id='navTooltip';navTooltip.className='nav-tooltip';navTooltip.setAttribute('role','tooltip');navTooltip.hidden=true;document.body.append(navTooltip);
let tooltipOwner=null;
function hideNavTooltip(){navTooltip.hidden=true;if(tooltipOwner)tooltipOwner.removeAttribute('aria-describedby');tooltipOwner=null}
function showNavTooltip(button){
  if(!sidebarCollapsed||sidebarBreakpoint.matches)return;
  const rect=button.getBoundingClientRect(),bounds=nav.getBoundingClientRect();
  if(rect.bottom<=bounds.top||rect.top>=bounds.bottom){hideNavTooltip();return}
  hideNavTooltip();tooltipOwner=button;navTooltip.textContent=button.getAttribute('aria-label');button.setAttribute('aria-describedby','navTooltip');navTooltip.hidden=false;
  navTooltip.style.left=Math.min(rect.right+12,innerWidth-navTooltip.offsetWidth-8)+'px';navTooltip.style.top=Math.max(8,Math.min(rect.top+(rect.height-navTooltip.offsetHeight)/2,innerHeight-navTooltip.offsetHeight-8))+'px';
}
for(const button of nav.querySelectorAll('[data-page]')){
  button.addEventListener('pointerenter',()=>showNavTooltip(button));button.addEventListener('pointerleave',()=>{if(document.activeElement!==button)hideNavTooltip()});
  button.addEventListener('focus',()=>showNavTooltip(button));button.addEventListener('blur',hideNavTooltip);
}
nav.addEventListener('scroll',()=>{const focused=document.activeElement;if(nav.contains(focused)&&focused.matches('button[data-page]'))showNavTooltip(focused);else hideNavTooltip()});syncSidebarControls();
const navBackdrop = makeButton('',()=>closeMobileNav());navBackdrop.id='navBackdrop';navBackdrop.setAttribute('aria-label','关闭导航');document.querySelector('.app').append(navBackdrop);
function closeMobileNav(){document.body.classList.remove('mobile-nav-open');menuToggle.setAttribute('aria-expanded','false');hideNavTooltip()}
document.addEventListener('keydown',e=>{if(e.key==='Escape'){closeMobileNav();accountMenu.open=false}});

const previewBar=document.createElement('div');previewBar.id='previewBar';previewBar.className='preview-bar';previewBar.hidden=true;
const previewTitle=document.createElement('strong');previewTitle.id='previewTitle';
const closePreview=makeButton('×',()=>{memberPlayer.pause();memberPlayer.removeAttribute('src');memberPlayer.load();previewBar.hidden=true});closePreview.setAttribute('aria-label','关闭播放预览');
previewBar.append(previewTitle,memberPlayer,closePreview);document.querySelector('.app').append(previewBar);

const maintenancePage=document.createElement('section');maintenancePage.id='page-maintenance';maintenancePage.className='page';
maintenancePage.innerHTML='<div class="page-intro"><h1>曲库维护</h1></div>';
maintenancePage.append(maintenancePanel);document.querySelector('.content').append(maintenancePage);
maintenancePanel.querySelector('.actions').prepend($('scan'));
const adminStatsLink=routeLink('全站调用统计','admin/stats');adminStatsLink.id='adminStatsLink';$('page-overview').querySelector('.actions').append(adminStatsLink);
const statsScopeLabel=document.createElement('p');statsScopeLabel.id='statsScopeLabel';statsScopeLabel.className='notice';$('page-stats').querySelector('.page-intro').after(statsScopeLabel);

const routeError = document.createElement('section');routeError.id='routeError';routeError.className='panel';routeError.hidden=true;
routeError.innerHTML='<h1>无法打开此页面</h1><p id="routeErrorMessage"></p>';routeError.append(routeLink('返回全局曲库','library'));
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
  if(page!=='users')state.managedUserID=null;
  document.querySelectorAll('.page').forEach(el=>el.classList.toggle('active',el.id==='page-'+page));
  document.querySelectorAll('.nav [data-page]').forEach(el=>el.classList.toggle('active',el.dataset.page===page || page==='stats'&&el.dataset.page==='keys'));
  document.querySelectorAll('.nav-group[data-admin]').forEach(el=>el.hidden=!isAdministrator());
  $('accountRole').textContent=isAdministrator()?'管理员':'普通用户';
  scheduleCommunityRefresh();
  document.title=(page==='stats'?'调用统计':titles[page])+' · 音乐服务器';
  playerTabs.hidden=!['keys','stats'].includes(page)||page==='stats'&&state.statsScope==='admin'; keyTab.classList.toggle('active',page==='keys');statsNav.classList.toggle('active',page==='stats');
  if(page==='stats'){
    const adminScope=state.statsScope==='admin';document.title=(adminScope?'全站调用统计':'我的调用统计')+' · 音乐服务器';
    statsScopeLabel.textContent=adminScope?'统计范围：'+(state.statsUserID?(state.users||[]).find(u=>u.id===state.statsUserID)?.username||'指定账户':'全部账户'):'统计范围：仅当前账户的播放器 Key';
  }
  document.querySelectorAll('.page > .page-intro h1').forEach(el=>{const section=el.closest('.page');if(section&&titles[section.id.slice(5)])el.textContent=section.id==='page-stats'?'调用统计':titles[section.id.slice(5)]});
}
let routeGeneration=0;
async function restoreWorkspaceRoute() {
  if(!state.user || state.user.must_change_password)return;
  if(!permitPersonalNavigation(location.hash))return;
  if(!permitPlaylistNavigation(location.hash))return;
  const generation=++routeGeneration;
  if(!location.hash || location.hash==='#/') {history.replaceState(null,'','#/'+pageRoutes[isAdministrator()?'overview':'tracks']);}
  const path=location.hash.slice(2).split('?')[0].split('/');
  let page=Object.keys(pageRoutes).find(key=>pageRoutes[key]===path.join('/'));
  if(path[0]==='account'&&path.length===2&&['profile','security','sessions'].includes(path[1]))page='account';
  if(path[0]==='playlists'&&path.length===2)page='playlists';
  if(path[0]==='playlists'&&path.length===3&&path[2]==='edit')page='playlists';
  if(path[0]==='subscriptions'&&(path.length===1||path.length===2))page='playlists';
  if(path[0]==='invitations'&&(path.length===1||path.length===2))page='playlists';
  if(path[0]==='public'&&path[1]==='playlists'&&path.length===3)page='public';
  if(path[0]==='admin'&&path[1]==='users'&&path.length===3)page='users';
  if(path[0]==='admin'&&path[1]==='library'&&path.length===3&&['scan','missing','removed'].includes(path[2]))page='maintenance';
  if(path[0]==='admin'&&path[1]==='settings'&&path.length===3&&['general','backup','update','audit'].includes(path[2]))page='settings';
  if(path[0]==='admin'&&path[1]==='stats'&&path.length===2)page='stats';
  try {
    if(!page || (adminPages.has(page)||path[0]==='admin')&&!isAdministrator())throw new Error('页面不存在或当前账户无权访问。');
    state.statsScope=path[0]==='admin'?'admin':'self';
    state.statsUserID=page==='stats'&&state.statsScope==='admin'?new URLSearchParams(location.hash.split('?')[1]||'').get('user')||'':'';
    renderStatsUsers();
    displayWorkspacePage(page);
    if(page==='account')await showPersonalCenter(path[1]);
    if(page==='public'){
      state.publicDetailId=path[2]||null;const query=new URLSearchParams(location.hash.split('?')[1]||'');state.publicQuery=query.get('q')||'';state.publicOffset=Math.max(0,parseInt(query.get('offset'),10)||0);
      if(state.publicDetailId)await loadPublicDetail(state.publicDetailId);else await loadPublicCatalog();
    }
    if(page==='playlists') {
      state.invitationsTab=path[0]==='invitations';
      state.subscriptionsTab=path[0]==='subscriptions';
      state.playlistCreating=path[0]==='playlists'&&path.length===2&&path[1]==='new';
      state.playlistEditing=state.playlistCreating||path[2]==='edit';
      state.playlistDetailId=state.invitationsTab||state.playlistCreating?null:path[1]||null;
      state.invitationDetailID=state.invitationsTab?path[1]||null:null;
      state.invitationOffset=Math.max(0,parseInt(new URLSearchParams(location.hash.split('?')[1]||'').get('offset'),10)||0);
      if(state.invitationsTab){renderPlaylists();await loadReceivedInvitations()}
      else {
      if(state.subscriptionsTab){await loadMySubscriptions();if(generation!==routeGeneration)return}
      const target=state.playlists.find(p=>p.id===state.playlistDetailId&&!p.system);
      if(state.playlistEditing&&target?.read_only||!state.subscriptionsTab&&target?.read_only)throw new Error('请从“我订阅的”打开此歌单。');
      if(state.playlistDetailId&&!state.playlists.some(p=>p.id===state.playlistDetailId&&!p.system)){
        if(state.playlistEditing&&isAdministrator()){const detail=await api('playlists/'+state.playlistDetailId);if(generation!==routeGeneration)return;state.managedPlaylistEdit=detail.playlist}
        else throw new Error('歌单不存在或当前账户无权访问。');
      }
      renderPlaylists();
      }
    }
    if(page==='tracks'){
      const query=new URLSearchParams(location.hash.split('?')[1]||'');$('trackSearch').value=query.get('q')||'';
      state.trackFilter=['all','mine','managed','orphan'].includes(query.get('filter'))?query.get('filter'):'all';
      document.querySelectorAll('[data-filter]').forEach(el=>el.classList.toggle('active',el.dataset.filter===state.trackFilter));renderTracks();
    }
    if(page==='keys')await loadKeys();
    if(page==='logs')await loadSystemLogs();
    if(page==='stats')await loadStats();
    if(page==='overview'){await loadUsers();renderOverview();await loadOverviewSummary()}
    if(page==='settings')await showSettingsTab(path[2]);
    if(page==='maintenance')await showMaintenanceTab(path[2]);
    if(page==='users') {
      const kind=new URLSearchParams(location.hash.split('?')[1]||'').get('kind')||'owned';
      $('userResourceKind').value=['owned','uploaded','playlists','keys'].includes(kind)?kind:'owned';
      if(state.managedUserID!==(path[2]||null))state.resourceOffset=0;
      state.managedUserID=path[2]||null;await loadUsers();
      if(path[2]&&!managedUser())throw new Error('用户不存在或无法读取。');
    }
    if(generation!==routeGeneration||!state.user)return;
    notePersonalNavigation(location.hash,page);
    restorePlaylistScroll();
    scheduleCommunityRefresh();
  } catch(error) {
    if(generation!==routeGeneration||!state.user)return;
    state.page='unavailable';document.querySelectorAll('.page').forEach(el=>el.classList.remove('active'));
    playerTabs.hidden=true;routeError.hidden=false;$('routeErrorMessage').textContent=error.message;document.title='无法打开此页面 · 音乐服务器';
  }
}
window.addEventListener('hashchange',()=>restoreWorkspaceRoute());
// The layout must be ready before restoring a browser login.
setupAdministration();
// Deferred personal-center script runs after the existing classic scripts.
window.addEventListener('DOMContentLoaded',()=>{setupProfileWorkspace();initializeAccountScreen()});
