'use strict';
let storageGeneration=0,storageLoading=false,storageDirty=false,storageView=null,storageLoadJob=null,quotaDraft=null;
const accountStorageCache=new Map();let deletedStoragePage=null;
function storageBytes(bytes){const value=Number(bytes)||0;for(const[unit,size]of [['GiB',1073741824],['MiB',1048576],['KiB',1024]])if(value>=size)return Number((value/size).toFixed(2)).toLocaleString()+' '+unit;return value+' B'}
function setupStoragePane(pane){
 pane.classList.remove('panel');pane.classList.add('storage-workspace');
 pane.innerHTML='<section class="panel storage-section"><div class="storage-section-head"><h2>磁盘空间</h2><button id="refreshStorage" class="btn">刷新容量</button></div><p id="storageResult" class="status" role="status"></p><div id="storageSummary" class="storage-volumes"></div><p class="storage-caption">数据卷与媒体卷可能位于同一磁盘，可用空间不相加。</p></section><section class="panel storage-section"><h2>存储分类</h2><div id="storageBreakdown" class="storage-breakdown"></div></section><section class="panel storage-section"><h2>空间保护</h2><p class="storage-caption">为系统运行保留余量，空间不足时暂停新增上传与备份。</p><form id="storagePolicyForm" class="storage-policy"><div class="field"><label for="storageReserve">磁盘预留空间（MiB）</label><input id="storageReserve" type="number" min="0" max="1048576" step="1" required></div><button id="saveStoragePolicy" class="btn primary" type="submit">保存预留空间</button></form><p id="storagePolicyResult" class="status" role="status"></p></section><section class="panel storage-section"><h2>账户存储额度</h2><p class="storage-caption">账户额度、上传资格和历史占用集中在用户管理中设置。</p><a class="btn" href="#/admin/users">前往用户管理</a></section><section class="panel storage-section storage-cleanup"><h2>安全清理</h2><p class="storage-caption">仅清理程序管理的旧更新下载包，保留当前、待更新及回退版本。媒体、已安装程序和未知文件保持原样。备份可在“备份与恢复”逐份删除。</p><button id="cleanUpdateCache" class="btn danger">清理旧更新缓存</button></section>';
 const dialog=document.createElement('dialog');dialog.id='quotaEditor';dialog.className='modal quota-dialog';dialog.setAttribute('aria-labelledby','quotaTitle');
 dialog.innerHTML='<form id="quotaForm"><div class="quota-head"><h2 id="quotaTitle">调整容量额度</h2><button id="closeQuota" type="button" class="btn icon-button" aria-label="关闭额度设置">×</button></div><div class="quota-body"><p id="quotaAccount" class="quota-account"></p><p id="quotaUsed" class="storage-caption"></p><fieldset class="quota-modes"><legend>容量上限</legend><label><input type="radio" name="quotaMode" value="unlimited"> 不限额</label><label><input type="radio" name="quotaMode" value="limited"> 限制额度</label></fieldset><div id="quotaLimited" class="quota-amount"><div class="field"><label for="quotaLimit">容量数值</label><input id="quotaLimit" type="number" min="0" step="any" required></div><div class="field"><label for="quotaUnit">单位</label><select id="quotaUnit"><option value="MiB">MiB</option><option value="GiB">GiB</option></select></div></div><p id="quotaEffect" class="storage-caption"></p><p id="quotaError" class="notice" role="alert" hidden></p></div><div class="quota-footer"><button id="cancelQuota" type="button" class="btn">取消</button><button id="saveQuota" type="submit" class="btn primary">保存修改</button></div></form>';document.body.append(dialog);
 $('refreshStorage').onclick=()=>run(()=>loadStorage());
 $('storagePolicyForm').oninput=()=>storageDirty=true;
 $('storagePolicyForm').onsubmit=event=>{event.preventDefault();run(saveStorageReserve)};
 $('cleanUpdateCache').onclick=()=>{if(confirm('清理不再需要的旧更新下载包？已安装程序和回退版本会保留。'))run(async()=>{const generation=storageGeneration,result=await api('admin/storage/cleanup-updates','POST',{});if(generation!==storageGeneration)return;await loadStorage();$('storageResult').textContent='已清理 '+storageBytes(result.bytes)+' 更新缓存。'})};
 for(const id of ['closeQuota','cancelQuota'])$(id).onclick=closeQuotaEditor;
 dialog.addEventListener('cancel',event=>{event.preventDefault();closeQuotaEditor()});
 dialog.addEventListener('close',()=>{quotaDraft=null;$('quotaError').textContent='';$('quotaError').hidden=true});
 $('quotaForm').onsubmit=event=>{event.preventDefault();saveStorageQuota()};
 for(const input of document.querySelectorAll('[name="quotaMode"]'))input.onchange=()=>{if(quotaDraft){quotaDraft.mode=input.value;updateQuotaEditor()}};
 $('quotaLimit').oninput=()=>{if(quotaDraft){quotaDraft.amount=$('quotaLimit').value;updateQuotaEditor()}};
 $('quotaUnit').onchange=()=>{if(!quotaDraft)return;const value=quotaDraftBytes();quotaDraft.unit=$('quotaUnit').value;quotaDraft.amount=String(value/quotaUnitSize());$('quotaLimit').value=quotaDraft.amount;updateQuotaEditor()};
}
function quotaUnitSize(){return quotaDraft?.unit==='GiB'?1073741824:1048576}
function quotaDraftBytes(){return quotaDraft?.mode==='unlimited'?0:Math.round(Number(quotaDraft?.amount)*quotaUnitSize())}
function quotaChanged(){return quotaDraft&&quotaDraftBytes()!==quotaDraft.limitBytes}
function closeQuotaEditor(){if(!quotaDraft||quotaDraft.saving)return;if(quotaChanged()&&!confirm('额度修改尚未保存，放弃修改？'))return;$('quotaEditor').close()}
function updateQuotaEditor(){
 if(!quotaDraft)return;const bytes=quotaDraftBytes(),limited=quotaDraft.mode==='limited';
 $('quotaLimited').hidden=!limited;$('quotaLimit').disabled=!limited||quotaDraft.saving;$('quotaUnit').disabled=quotaDraft.saving;
 $('saveQuota').disabled=quotaDraft.saving||!quotaChanged()||!Number.isSafeInteger(bytes)||bytes<0||bytes>1125899906842624||limited&&bytes===0;
 for(const id of ['closeQuota','cancelQuota'])$(id).disabled=quotaDraft.saving;
 document.querySelectorAll('[name="quotaMode"]').forEach(input=>input.disabled=quotaDraft.saving);
 $('quotaEffect').textContent=limited&&bytes<quotaDraft.used?'当前占用高于新额度。现有文件不会删除，新的上传入库将受限。':'保存后应用于该账户后续新增上传，不改变已有曲目。';
}
async function openStorageQuota(id){
 if(!state.user||!isAdministrator())return;if(quotaDraft){if(quotaDraft.id===id)return;if(quotaChanged()&&!confirm('放弃当前未保存的额度修改？'))return;$('quotaEditor').close()}
 const generation=storageGeneration;await loadAccountStorage([id]);if(generation!==storageGeneration)return;
 const account=accountStorageCache.get(id);if(!account)throw new Error('该账户不可设置额度');
 const unit=account.limit_bytes&&account.limit_bytes<1073741824?'MiB':'GiB',size=unit==='GiB'?1073741824:1048576;
 quotaDraft={id,username:account.username,version:account.version,used:account.used_bytes,limitBytes:account.limit_bytes,mode:account.limit_bytes?'limited':'unlimited',unit,amount:String(account.limit_bytes?account.limit_bytes/size:Math.max(1,Math.ceil(account.used_bytes/1073741824))),saving:false};
 $('quotaAccount').textContent='@'+account.username;$('quotaUsed').textContent='当前已用 '+storageBytes(account.used_bytes);
 $('quotaUnit').value=unit;$('quotaLimit').value=quotaDraft.amount;document.querySelector('[name="quotaMode"][value="'+quotaDraft.mode+'"]').checked=true;
 $('quotaError').textContent='';$('quotaError').hidden=true;updateQuotaEditor();$('quotaEditor').showModal();
}
async function saveStorageQuota(){
 if(!quotaDraft||quotaDraft.saving||$('saveQuota').disabled)return;
 const draft=quotaDraft,generation=storageGeneration,bytes=quotaDraftBytes();draft.saving=true;updateQuotaEditor();$('quotaError').hidden=true;
 try{await api('admin/storage/quotas/'+draft.id,'PUT',{limit_bytes:bytes,version:draft.version});if(generation!==storageGeneration||quotaDraft!==draft)return;
  const account=accountStorageCache.get(draft.id);if(account){account.limit_bytes=bytes;account.version=draft.version+1}if(typeof renderUsers==='function')renderUsers();if(typeof renderManagedUserPage==='function')renderManagedUserPage();$('quotaEditor').close();setStatus('账户额度已保存','ok');
 }catch(error){if(generation===storageGeneration&&quotaDraft===draft){$('quotaError').hidden=false;$('quotaError').textContent=error.message}}
 finally{if(generation===storageGeneration&&quotaDraft===draft){draft.saving=false;updateQuotaEditor()}}
}
async function saveStorageReserve(){
 if(!storageView||$('saveStoragePolicy').disabled)return;const generation=storageGeneration,value=Number($('storageReserve').value),version=storageView.policy.version;
 if(!Number.isInteger(value)||value<0||value>1048576)throw new Error('请输入 0–1048576 MiB 的整数预留空间');
 $('saveStoragePolicy').disabled=true;
 try{await api('admin/storage/policy','PUT',{reserve_bytes:value*1048576,version});if(generation!==storageGeneration)return;storageView.policy={reserve_bytes:value*1048576,version:version+1};storageDirty=Number($('storageReserve').value)!==value;$('storagePolicyResult').textContent='预留空间已保存';renderStorageVolumes()}
 finally{if(generation===storageGeneration)$('saveStoragePolicy').disabled=false}
}
function renderStorageVolumes(){
 if(!storageView)return;const data=storageView,box=$('storageSummary');box.replaceChildren();
 for(const[label,disk]of [['数据存储卷',data.data_disk],['媒体存储卷',data.media_disk]]){
  const card=document.createElement('div');card.className='storage-volume';const title=document.createElement('h3');title.textContent=label;
  const number=document.createElement('strong');number.textContent=storageBytes(disk.available);const description=document.createElement('p');description.textContent='可用 / 总量 '+storageBytes(disk.total);
  const bar=document.createElement('progress');bar.max=Math.max(1,disk.total);bar.value=Math.min(disk.available,disk.total);bar.setAttribute('aria-label',label+'可用空间');
  if(disk.available<data.policy.reserve_bytes)card.classList.add('storage-low');card.append(title,number,description,bar);box.append(card);
 }
 $('storageResult').textContent=Math.min(data.data_disk.available,data.media_disk.available)<data.policy.reserve_bytes?'可用空间低于预留值，新增上传和备份将被暂停。':'空间检查正常';
 const list=$('storageBreakdown');list.replaceChildren();
 for(const[label,value]of [['数据库（含 WAL）',data.database_bytes],['已入库音频',data.media_bytes],['上传暂存',data.staging_bytes],['备份目录',data.backup_bytes],['更新缓存',data.update_bytes]]){
  const item=document.createElement('div'),name=document.createElement('span'),number=document.createElement('strong');name.textContent=label;number.textContent=storageBytes(value);item.append(name,number);list.append(item);
 }
}
function storageAccountTable(accounts,deleted){
 const table=document.createElement('table');table.className='storage-account-table';const head=document.createElement('thead');head.innerHTML='<tr><th>账户</th><th>已用容量</th><th>容量上限</th><th>使用情况</th><th>操作</th></tr>';const body=document.createElement('tbody');table.append(head,body);
 for(const account of accounts){
  const row=document.createElement('tr');row.dataset.quotaId=account.id;
  const values=['@'+account.username,storageBytes(account.used_bytes),account.limit_bytes?storageBytes(account.limit_bytes):'不限额'];const labels=['账户','已用容量','容量上限','使用情况','操作'];
  for(const[index,value]of values.entries()){const cell=document.createElement('td');cell.dataset.label=labels[index];cell.textContent=value;row.append(cell)}
  const usage=document.createElement('td');usage.dataset.label=labels[3];
  if(deleted){usage.textContent='历史归属记录';row.classList.add('storage-account-deleted')}
  else if(!account.limit_bytes){usage.textContent='未设上限';usage.className='storage-unlimited'}
  else{const percent=account.used_bytes/account.limit_bytes*100,bar=document.createElement('progress'),caption=document.createElement('span');bar.max=100;bar.value=Math.min(100,percent);bar.setAttribute('aria-label',account.username+' 已用额度');caption.textContent=Number(percent.toFixed(1))+'%'+(percent>100?' · 已超额':percent>=90?' · 接近上限':'');usage.className='storage-usage'+(percent>100?' exceeded':percent>=90?' near':'');usage.append(bar,caption)}
  const action=document.createElement('td');action.dataset.label=labels[4];
  if(deleted){action.textContent='只读';if(account.email&&account.account_version){const release=makeButton('释放邮箱占用',()=>run(async()=>{if(!confirm('释放此已删除账户的邮箱占用？历史账户和上传溯源将保留。'))return;await api('admin/users/'+account.id+'/release-email','POST',{},undefined,account.account_version);await loadDeletedAccountStorage(deletedStoragePage?.offset||0);setStatus('邮箱占用已释放','ok')}),'btn small');release.dataset.releaseEmail=account.id;action.append(release)}}else{const button=makeButton('调整额度',()=>run(()=>openStorageQuota(account.id)),'btn small');button.dataset.editQuota=account.id;action.append(button)}
  if(deleted&&account.account_version){const purge=makeButton('完全删除',()=>purgeUserAccount({...account,version:account.account_version}),'btn small danger');purge.dataset.purgeUser=account.id;action.append(purge)}
  row.append(usage,action);body.append(row);
 }
 return table;
}
async function loadAccountStorage(ids){
 if(!state.user||!isAdministrator()||!ids.length)return [];
 const generation=storageGeneration,userID=state.user.id,unique=[...new Set(ids)].slice(0,100);
 const page=await api('admin/storage/accounts?'+new URLSearchParams({ids:unique.join(',')}));
 if(generation!==storageGeneration||state.user?.id!==userID)return [];
 if(accountStorageCache.size>200)accountStorageCache.clear();
 for(const account of page.items||[])accountStorageCache.set(account.id,account);
 return page.items||[];
}
async function loadDeletedAccountStorage(offset=0){
 if(!state.user||!isAdministrator()||!$('storageDeleted'))return;
 const generation=storageGeneration,userID=state.user.id;
 const page=await api('admin/storage/accounts?'+new URLSearchParams({deleted:'true',offset:String(offset),limit:'50'}));
 if(generation!==storageGeneration||state.user?.id!==userID)return;
 deletedStoragePage=page;
 const total=page.used_bytes_total,amount=typeof total==='number'?total:(page.items||[]).reduce((sum,a)=>sum+a.used_bytes,0);
 $('storageDeleted').querySelector('summary').textContent='已删除账户占用（'+page.total+'） · '+(typeof total==='number'?'合计 ':'本页 ')+storageBytes(amount);
 const host=$('storageDeletedAccounts');host.replaceChildren(storageAccountTable(page.items||[],true));
 if(!page.items?.length)host.textContent='暂无已删除账户的历史占用。';
 const nav=document.createElement('div');nav.className='browse-pager';
 const previous=makeButton('上一页',()=>run(()=>loadDeletedAccountStorage(Math.max(0,page.offset-50)))),next=makeButton('下一页',()=>run(()=>loadDeletedAccountStorage(page.offset+50)));
 previous.disabled=page.offset===0;next.disabled=page.offset+(page.items||[]).length>=page.total;
 const status=document.createElement('span');status.textContent=page.total?(page.offset+1)+'–'+(page.offset+page.items.length)+' / '+page.total:'0 / 0';nav.append(previous,status,next);nav.hidden=page.total<=50;host.append(nav);
}
async function loadStorage(){
 if(!state.user||!isAdministrator())return;if(storageLoadJob)return storageLoadJob;
 const generation=storageGeneration,userID=state.user.id;storageLoading=true;
 const job=(async()=>{try{const data=await api('admin/storage');if(generation!==storageGeneration||state.user?.id!==userID)return;
  storageView=data;if(!storageDirty)$('storageReserve').value=data.policy.reserve_bytes/1048576;renderStorageVolumes();
 }finally{if(generation===storageGeneration){storageLoading=false;storageLoadJob=null}}})();
 storageLoadJob=job;return job;
}
function clearStorageWorkspace(){
 ++storageGeneration;storageLoading=false;storageDirty=false;storageView=null;storageLoadJob=null;quotaDraft=null;accountStorageCache.clear();deletedStoragePage=null;
 if($('quotaEditor')?.open)$('quotaEditor').close();
 for(const id of ['storageSummary','storageBreakdown','storageAccounts','storageDeletedAccounts','storageResult','storagePolicyResult'])if($(id))$(id).replaceChildren();
 if($('storageDeleted')){$('storageDeleted').open=false;$('storageDeleted').querySelector('summary').textContent='已删除账户占用';}
}
