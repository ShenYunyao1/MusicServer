'use strict';

let backupTimer=null,backupLoading=false,backupGeneration=0,backupPolicyLoaded=false,backupPolicyDirty=false,backupCurrentJob=null;
function setupBackupPane(pane){
 pane.innerHTML='<h3>备份与恢复</h3><p class="notice">完整备份包含数据库、已入库且存在的音频、相关歌词封面与应用设置。程序、暂存、日志、Nginx、systemd 和证书不在归档中。请另存一份到服务器之外。</p><div class="actions"><button id="createBackup" class="btn primary">创建完整备份</button><button id="createDatabaseBackup" class="btn">仅备份数据库</button><button id="refreshBackups" class="btn">刷新列表</button><button id="cancelBackup" class="btn danger" hidden>取消当前任务</button></div><p id="backupResult" class="task-result" role="status"></p><progress id="backupProgress" max="100" hidden aria-label="备份进度"></progress><div id="backupArchives"></div><h3>自动备份</h3><p class="status">默认关闭。启用后每分钟检查到期任务；首次到期会开始备份。只自动清理定时备份，手动备份自行删除。</p><form id="backupPolicyForm"><fieldset id="backupPolicyFields" class="form-grid" style="border:0;padding:0"><label><input id="backupEnabled" type="checkbox"> 启用自动备份</label><label><input id="backupIncludeMedia" type="checkbox" checked> 自动备份包含音频及相关资源</label><div class="field"><label for="backupInterval">间隔（小时）</label><input id="backupInterval" type="number" min="1" max="168" value="24" required></div><div class="field"><label for="backupKeep">保留最近几份自动备份</label><input id="backupKeep" type="number" min="1" max="30" value="3" required></div><button id="saveBackupPolicy" class="btn primary" type="submit">保存备份策略</button></fieldset></form><h3>恢复备份</h3><p class="notice">恢复工具会校验文件并写入新的空目录，不覆盖运行库。恢复后旧网页登录失效，自动备份默认关闭；请核对部署配置、文件权限与原有凭据，再启动恢复后的服务。</p><pre id="backupRestoreCommand" class="secret-box"></pre>';
 $('backupRestoreCommand').textContent='musicserver backup restore --file "备份文件.tar.gz" --destination "新的空目录"';
 $('backupRestoreCommand').classList.add('lyrics-text');
 $('createBackup').onclick=()=>run(()=>startManagedBackup(true));
 $('createDatabaseBackup').onclick=()=>run(()=>startManagedBackup(false));
 $('refreshBackups').onclick=()=>run(()=>loadBackupManagement(true));
 $('cancelBackup').onclick=()=>run(async()=>{if(backupCurrentJob?.id){await api('admin/backups/jobs/'+backupCurrentJob.id,'DELETE');await loadBackupManagement()}});
 $('backupPolicyForm').onchange=()=>backupPolicyDirty=true;
 $('backupPolicyForm').onsubmit=event=>{event.preventDefault();run(saveBackupPolicy)};
}
function backupPageVisible(){return state.user&&isAdministrator()&&state.page==='settings'&&location.hash.split('?')[0]==='#/admin/settings/backup'&&!document.hidden}
function scheduleBackupRefresh(){
 clearTimeout(backupTimer);backupTimer=null;
 if(backupPageVisible())backupTimer=setTimeout(()=>{if(backupPageVisible())loadBackupManagement().catch(error=>{$('backupResult').textContent='读取备份失败：'+error.message;scheduleBackupRefresh()})},backupCurrentJob&&['running','cancelling'].includes(backupCurrentJob.state)?1000:30000);
}
async function loadBackupManagement(forcePolicy=false){
 if(!state.user||!isAdministrator()||backupLoading)return;
 const generation=backupGeneration,userID=state.user.id;backupLoading=true;
 try{
  const data=await api('admin/backups');
  if(generation!==backupGeneration||state.user?.id!==userID)return;
  backupCurrentJob=data.job;
  if(!backupPolicyLoaded||forcePolicy&&!backupPolicyDirty){$('backupEnabled').checked=data.policy.enabled;$('backupIncludeMedia').checked=data.policy.include_media;$('backupInterval').value=data.policy.interval_hours;$('backupKeep').value=data.policy.keep_last;backupPolicyLoaded=true}
  renderBackupJob(data.job);renderBackupArchives(data.items);
 }finally{if(generation===backupGeneration){backupLoading=false;scheduleBackupRefresh()}}
}
function renderBackupJob(job){
 const active=['running','cancelling'].includes(job.state),progress=$('backupProgress');
 $('createBackup').disabled=$('createDatabaseBackup').disabled=active;$('cancelBackup').hidden=!active;$('cancelBackup').disabled=job.state==='cancelling';
 progress.hidden=!active;
 const phases={snapshot:'创建数据库快照',checking:'检查备份清单',packing:'归档与校验',completed:'完成'};
 if(active){if(job.total>0)progress.value=Math.min(100,Math.floor(job.bytes*100/job.total));else progress.removeAttribute('value');$('backupResult').textContent=(job.state==='cancelling'?'正在取消任务…':phases[job.phase]||'正在备份')+(job.total>0?' · '+formatBytes(job.bytes)+' / '+formatBytes(job.total):'')}
 else if(job.state==='completed')$('backupResult').textContent='备份已创建并验证：'+job.archive.name+(job.archive.missing_count?' · 原库有 '+job.archive.missing_count+' 个缺失音频，请查看清单。':' · 可从列表下载。')+(job.error?' · '+job.error:'');
 else if(job.state==='failed'||job.state==='cancelled')$('backupResult').textContent=(job.state==='cancelled'?'备份已取消：':'备份失败：')+(job.error||'请查看系统日志');
 else $('backupResult').textContent='尚无本次运行的备份任务。已有备份保留在下方列表。';
}
function renderBackupArchives(items){
 const list=$('backupArchives');list.replaceChildren();
 if(!items.length){list.textContent='暂无受管理的备份。';return}
 for(const archive of items){
  const row=document.createElement('div');row.className='resource-item';row.dataset.backupId=archive.id;
  const label=document.createElement('div');label.className='resource-title';const title=document.createElement('strong');title.textContent=new Date(archive.created_at).toLocaleString()+' · '+(archive.include_media?'完整备份':'仅数据库');const meta=document.createElement('p');meta.className='status';meta.textContent=formatBytes(archive.size)+' · '+archive.file_count+' 个文件 · '+(archive.automatic?'自动':'手动')+(archive.missing_count?' · '+archive.missing_count+' 个原始缺失文件':'');label.append(title,meta);
  const actions=document.createElement('div');actions.className='actions';const download=document.createElement('a');download.className='btn small';download.href='/api/v2/admin/backups/'+archive.id+'/download';download.download=archive.name;download.textContent='下载';download.dataset.backupDownload=archive.id;
  const remove=makeButton('删除',()=>{if(confirm('删除这份备份？不会删除曲库，已下载的副本不受影响。'))run(async()=>{await api('admin/backups/'+archive.id,'DELETE');await loadBackupManagement()})},'btn small danger');remove.dataset.backupDelete=archive.id;if(archive.error){title.textContent='不可用备份';meta.textContent=archive.name+' · '+archive.error;actions.append(remove)}else actions.append(download,remove);row.append(label,actions);list.append(row);
 }
}
async function startManagedBackup(includeMedia){
 const generation=backupGeneration,userID=state.user?.id;
 $('createBackup').disabled=$('createDatabaseBackup').disabled=true;
 try{const job=await api('admin/backups','POST',{include_media:includeMedia});if(generation!==backupGeneration||state.user?.id!==userID)return;backupCurrentJob=job;renderBackupJob(job);await loadBackupManagement()}
 finally{if(generation===backupGeneration){scheduleBackupRefresh();if(!backupCurrentJob||!['running','cancelling'].includes(backupCurrentJob.state))$('createBackup').disabled=$('createDatabaseBackup').disabled=false}}
}
async function saveBackupPolicy(){
 const generation=backupGeneration,userID=state.user?.id;
 const fields=$('backupPolicyFields');if(fields.disabled)return;fields.disabled=true;
 try{await api('admin/backups/policy','PUT',{enabled:$('backupEnabled').checked,interval_hours:Number($('backupInterval').value),keep_last:Number($('backupKeep').value),include_media:$('backupIncludeMedia').checked});if(generation!==backupGeneration||state.user?.id!==userID)return;backupPolicyDirty=false;setStatus('备份策略已保存','ok');await loadBackupManagement(true)}finally{if(generation===backupGeneration)fields.disabled=false}
}
function clearBackupWorkspace(){
 ++backupGeneration;clearTimeout(backupTimer);backupTimer=null;backupLoading=false;backupPolicyLoaded=false;backupPolicyDirty=false;backupCurrentJob=null;
 if($('backupArchives')){$('backupArchives').replaceChildren();$('backupResult').textContent='';$('backupProgress').hidden=true;$('backupPolicyForm').reset();$('backupPolicyFields').disabled=false}
}
document.addEventListener('visibilitychange',()=>{clearTimeout(backupTimer);if(backupPageVisible())loadBackupManagement().catch(()=>scheduleBackupRefresh())});
