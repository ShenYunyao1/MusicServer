'use strict';

function setupAdministration(){
  const overview=$('page-overview');
  overview.querySelector('.page-intro p').textContent='管理账户和曲库，查看需要处理的状态。';
  const labels=['全局曲库可见曲目','用户账户','文件缺失','我的私人歌单'];
  overview.querySelectorAll('.metric-label').forEach((label,index)=>label.textContent=labels[index]);
  const links=document.createElement('div');links.className='overview-links';
  for(const [label,path,description] of [
    ['用户管理','admin/users','创建账户、启停和资源接管'],
    ['目录扫描','admin/library/scan','发现新增文件，查看扫描进度'],
    ['已下架曲目','admin/library/removed','恢复曲目及原有歌单关联'],
    ['系统设置','admin/settings/general','参数、备份、更新与操作记录']]){
    const link=routeLink(label,path),small=document.createElement('span');small.textContent=description;link.className='overview-link';link.append(small);links.append(link);
  }
  overview.querySelector('.metric-grid').after(links);
  const summary=document.createElement('div');summary.className='panel';summary.innerHTML='<h3>扫描状态</h3><p id="overviewScanStatus" class="status">尚未读取</p>';overview.append(summary);
  overview.querySelector('.panel h3').textContent='管理范围';

  // Each task owns its output, so audit reads cannot replace a recovery or backup result.
  const scanPane=document.createElement('section');scanPane.dataset.maintenancePane='scan';
  scanPane.innerHTML='<h3>目录扫描</h3><p class="status">快速扫描检查文件变化；完整校验会重新计算内容并解析音频信息。</p><div class="actions"></div>';
  for(const id of ['scan','fullScan','cancelScan'])scanPane.querySelector('.actions').append($(id));scanPane.append($('scanProgress'));
  const missingPane=document.createElement('section');missingPane.dataset.maintenancePane='missing';missingPane.hidden=true;missingPane.innerHTML='<h3>文件缺失</h3><p class="notice">缺失表示文件不可读取，与主动下架不同。将文件放回媒体目录后重新扫描。</p><div id="missingResults"></div>';
  const removedPane=document.createElement('section');removedPane.dataset.maintenancePane='removed';removedPane.hidden=true;removedPane.innerHTML='<h3>已下架曲目</h3><p class="notice">下架保留原文件和歌单占位；恢复继续使用原 UUID 和归属。</p><div id="removedResults"></div>';
  const refreshRemoved=$('viewRemoved');refreshRemoved.textContent='刷新已下架曲目';removedPane.querySelector('h3').after(refreshRemoved);
  maintenancePanel.replaceChildren(scanPane,missingPane,removedPane);
  const tabs=document.createElement('nav');tabs.className='page-tabs';tabs.id='maintenanceTabs';
  for(const [tab,label] of [['scan','目录扫描'],['missing','文件缺失'],['removed','已下架曲目']])tabs.append(routeLink(label,'admin/library/'+tab));
  $('page-maintenance').querySelector('.page-intro').after(tabs);

  const settings=$('page-settings'),general=settings.querySelector('.panel'),update=$('updateStatus').closest('.panel');general.dataset.settingsPane='general';update.dataset.settingsPane='update';update.hidden=true;
  const backup=document.createElement('section');backup.className='panel';backup.dataset.settingsPane='backup';backup.hidden=true;backup.innerHTML='<h3>数据库备份</h3><p class="notice">备份包含数据库中的账户、曲目记录和歌单。音频文件需单独备份；此页不提供在线恢复。</p><div id="backupResult" class="task-result"></div>';
  // These buttons were removed with the old shared maintenance container.
  const backupButton=makeButton('创建数据库备份',()=>run(createDatabaseBackup),'btn primary');backupButton.id='createBackup';backup.querySelector('#backupResult').before(backupButton);
  const audit=document.createElement('section');audit.className='panel';audit.dataset.settingsPane='audit';audit.hidden=true;audit.innerHTML='<h3>操作记录</h3><p class="status">最近 100 条记录；展开查看变更详情。</p><div id="auditResults"></div>';
  const auditButton=makeButton('刷新操作记录',()=>run(loadAuditEntries));auditButton.id='viewAudit';audit.querySelector('#auditResults').before(auditButton);
  settings.append(backup,audit);
  const settingsTabs=document.createElement('nav');settingsTabs.className='page-tabs';settingsTabs.id='settingsTabs';
  for(const [tab,label] of [['general','常规'],['backup','数据库备份'],['update','程序更新'],['audit','操作记录']])settingsTabs.append(routeLink(label,'admin/settings/'+tab));settings.querySelector('.page-intro').after(settingsTabs);
  const updateSource=document.createElement('div');updateSource.id='updateSource';updateSource.className='field';update.querySelector('#updateStatus').before(updateSource);
  const saveUpdate=makeButton('保存更新来源',()=>run(()=>saveSettings('update')),'btn primary');updateSource.after(saveUpdate);
  $('saveSettings').onclick=()=>run(()=>saveSettings('general'));
  $('viewRemoved').onclick=()=>run(loadRemovedTracks);

  const users=$('page-users'),detail=$('userResourcesPanel');detail.id='userResourcesPanel';
  const heading=document.createElement('div');heading.id='userDetailHeader';heading.hidden=true;heading.innerHTML='<p class="status">用户管理 / 账户详情</p><h2 id="managedUserName"></h2><p id="managedUserStatus" class="notice"></p><div id="userDetailActions" class="actions"></div>';
  const back=routeLink('返回用户列表','admin/users');back.id='backUsers';heading.prepend(back);detail.before(heading);
  const resourceTabs=document.createElement('nav');resourceTabs.id='resourceTabs';resourceTabs.className='page-tabs';
  for(const [kind,label] of [['owned','当前管理曲目'],['uploaded','原始上传曲目'],['playlists','私人歌单'],['keys','API Key']]){
    const button=makeButton(label,()=>{state.resourceOffset=0;location.hash='#/admin/users/'+state.managedUserID+'?kind='+kind});button.dataset.resourceKind=kind;resourceTabs.append(button);
  }
  detail.querySelector('.toolbar').before(resourceTabs);$('userResourceKind').hidden=true;
}
function renderOverview(){
  $('metricTracks').textContent=state.tracks.length;
  $('metricPlaylists').textContent=(state.users||[]).length;
  $('metricKeys').textContent=state.tracks.filter(t=>t.missing).length;
  $('metricRevision').textContent=state.playlists.filter(p=>!p.system).length;
  $('overviewNotice').textContent='曲库统计包含当前可见曲目；私人歌单计数仅属于本人。账户 '+(state.users||[]).filter(u=>u.enabled).length+' 个已启用。服务修订版本 '+(state.revision??'—')+'。';
}
async function loadOverviewSummary(){
  try{const scan=await api('admin/scan/status');$('overviewScanStatus').textContent=scanSummary(scan)}catch(error){$('overviewScanStatus').textContent='扫描状态读取失败：'+error.message}
}
function scanSummary(scan){const names={running:'扫描中',completed:'已完成',cancelled:'已取消',failed:'失败'};return '状态：'+(names[scan.state]||'未开始')+' · 已处理 '+(scan.processed||0)+' · 变更 '+(scan.changed||0)+(scan.errors?.length?' · '+scan.errors.join('；'):'')}
function layoutSettingsFields(){
  const repository=document.querySelector('[data-setting="update_repository"]');if(repository)$('updateSource').replaceChildren(repository.closest('.field'));
}
async function showMaintenanceTab(tab){
  document.querySelectorAll('[data-maintenance-pane]').forEach(el=>el.hidden=el.dataset.maintenancePane!==tab);
  $('maintenanceTabs').querySelectorAll('a').forEach(el=>el.classList.toggle('active',el.hash.endsWith('/'+tab)));
  if(tab==='scan')await pollScan();
  if(tab==='removed')await loadRemovedTracks();
  if(tab==='missing'){
    await refreshCore();const container=$('missingResults');container.replaceChildren();const missing=state.tracks.filter(t=>t.missing);
    if(!missing.length)container.textContent='暂无缺失文件。';
    for(const track of missing){const item=document.createElement('p');item.className='resource-item';item.textContent=(track.title||track.id)+' · '+track.path;container.append(item)}
  }
}
async function showSettingsTab(tab){
  document.querySelectorAll('[data-settings-pane]').forEach(el=>el.hidden=el.dataset.settingsPane!==tab);
  $('settingsTabs').querySelectorAll('a').forEach(el=>el.classList.toggle('active',el.hash.endsWith('/'+tab)));
  $('refreshSettings').hidden=!['general','update'].includes(tab);
  if(['general','update'].includes(tab))await loadSettings();
  if(tab==='update')await loadUpdate();
  if(tab==='audit')await loadAuditEntries();
}
async function loadRemovedTracks(){
  const container=$('removedResults');container.textContent='正在读取已下架曲目…';
  try{
    const tracks=await api('admin/removed');container.replaceChildren();if(!tracks.length)container.textContent='暂无已下架曲目。';
    for(const track of tracks){const item=document.createElement('div');item.className='resource-item';const label=document.createElement('span');label.textContent=(track.title||track.id)+' · '+track.path;
      const button=makeButton('恢复',()=>run(async()=>{button.disabled=true;try{await api('admin/removed/'+track.id,'POST',undefined,undefined,track.version);await refreshCore();await loadRemovedTracks();setStatus('曲目已恢复','ok')}finally{button.disabled=false}}),'btn small');item.append(label,button);container.append(item)}
  }catch(error){container.textContent='读取失败：'+error.message;throw error}
}
async function createDatabaseBackup(){
  const button=$('createBackup');if(button.disabled)return;button.disabled=true;$('backupResult').textContent='正在创建并验证备份…';
  try{const result=await api('admin/backups','POST');$('backupResult').textContent='备份已创建并验证：'+result.path+'。音频文件需单独备份。'}catch(error){$('backupResult').textContent='备份失败：'+error.message;throw error}finally{button.disabled=false}
}
async function loadAuditEntries(){
  const container=$('auditResults');container.textContent='正在读取操作记录…';
  try{state.auditEntries=await api('admin/audit?limit=100');state.auditOffset=0;renderAuditPage();
  }catch(error){container.textContent='记录读取失败：'+error.message;throw error}
}
function renderAuditPage(){
  const entries=state.auditEntries||[],offset=state.auditOffset||0,container=$('auditResults');container.replaceChildren();
  if(!entries.length){container.textContent='暂无操作记录。';return}
  const paging=document.createElement('div');paging.className='toolbar';
  const previous=makeButton('上一页',()=>{state.auditOffset=Math.max(0,offset-15);renderAuditPage()});previous.disabled=offset===0;
  const next=makeButton('下一页',()=>{state.auditOffset=offset+15;renderAuditPage()});next.disabled=offset+15>=entries.length;
  const count=document.createElement('span');count.className='status';count.textContent='最近 '+entries.length+' 条中的第 '+(offset+1)+'–'+Math.min(offset+15,entries.length)+' 条';paging.append(previous,next,count);container.append(paging);
  for(const entry of entries.slice(offset,offset+15)){const details=document.createElement('details'),summary=document.createElement('summary'),changes=document.createElement('pre');details.className='audit-entry';summary.textContent=new Date(entry.created_at).toLocaleString()+' · '+entry.actor+' · '+entry.action;changes.textContent='对象：'+entry.entity_id+'\n'+JSON.stringify(entry.changes,null,2);changes.className='lyrics-text';details.append(summary,changes);container.append(details)}
}
function renderManagedUserPage(){
  const user=managedUser(),detail=Boolean(user);$('userDetailHeader').hidden=!detail;$('userResourcesPanel').hidden=!detail;
  $('userList').closest('.panel').hidden=detail;$('createUser').hidden=detail;
  if(!user)return;
  if(state.page==='users')$('pageTitle').textContent='用户管理 / '+user.username;$('managedUserName').textContent=user.display_name||user.username;
  $('managedUserStatus').textContent='登录名：'+user.username+' · '+(user.enabled?'已启用':'已停用')+(user.must_change_password?' · 首次登录需改密':'');
  const actions=$('userDetailActions');actions.replaceChildren();
  const row=[...$('userList').children].find(el=>el.dataset.userId===user.id);
  for(const action of ['display','reset','enabled']){
    const source=row?.querySelector('[data-user-action="'+action+'"]');if(!source)continue;
    const button=makeButton(source.textContent,()=>source.click(),source.className);button.dataset.accountAction=action;button.disabled=source.disabled;button.title=source.title;actions.append(button);
  }
  const stats=routeLink('查看此账户调用统计','admin/stats?user='+encodeURIComponent(user.id));stats.id='userStatsLink';actions.append(stats);
  document.querySelectorAll('[data-resource-kind]').forEach(button=>button.classList.toggle('active',button.dataset.resourceKind===$('userResourceKind').value));
}
