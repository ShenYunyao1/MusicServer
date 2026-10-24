'use strict';

const usersNav=document.createElement('button');usersNav.dataset.page='users';usersNav.hidden=true;usersNav.innerHTML='<span class="nav-icon">♙</span><span class="nav-label">账户管理</span>';usersNav.onclick=()=>showPage('users');document.querySelector('.nav').append(usersNav);titles.users='账户管理';
const usersPage=document.createElement('section');usersPage.className='page';usersPage.id='page-users';
usersPage.innerHTML='<div class="page-intro"><div><h1>账户管理</h1></div><div class="actions"><button id="refreshUsers" class="btn">刷新</button><button id="createUser" class="btn primary">创建成员</button></div></div><div class="panel table-wrap"><table class="data-table"><thead><tr><th>昵称 / 登录名</th><th>状态</th><th>操作</th></tr></thead><tbody id="userList"></tbody></table></div><section id="userResourcesPanel" class="panel" hidden><h3 id="userResourcesTitle"></h3><div class="toolbar"><select id="userResourceKind" class="select"><option value="owned">当前管理曲目</option><option value="uploaded">原始上传曲目</option><option value="playlists">私人歌单</option><option value="keys">播放器 Key</option></select><button id="resourcesPrevious" class="btn">上一页</button><button id="resourcesNext" class="btn">下一页</button><span id="resourcesCount" class="status"></span></div><div id="userResources" class="table-wrap"></div><div id="transferControls" class="actions"><select id="transferTarget" class="select" aria-label="接管目标"></select><button id="transferSelected" class="btn">接管所选资源</button><button id="transferAll" class="btn danger">接管全部曲目与歌单</button></div><p class="status" id="resourceNotice"></p></section>';
document.querySelector('.content').append(usersPage);
const temporaryPasswordDialog=document.createElement('dialog');temporaryPasswordDialog.id='temporaryPasswordDialog';temporaryPasswordDialog.className='modal';temporaryPasswordDialog.innerHTML='<div class="modal-head"><h3>临时密码（仅展示一次）</h3></div><div class="modal-body"><p id="temporaryPasswordAccount"></p><p>请安全交给本人，首次登录必须修改。关闭后不能再次读取。</p><pre id="temporaryPasswordValue" class="secret-box"></pre></div><div class="modal-foot"><button id="closeTemporaryPassword" class="btn primary">已保存，关闭</button></div>';document.body.append(temporaryPasswordDialog);
function clearTemporaryPassword(){$('temporaryPasswordValue').textContent='';$('temporaryPasswordAccount').textContent=''}
temporaryPasswordDialog.addEventListener('close',()=>{clearTemporaryPassword();if(isAdministrator())loadUsers().catch(error=>setStatus(error.message,'error'))});temporaryPasswordDialog.addEventListener('cancel',clearTemporaryPassword);
$('closeTemporaryPassword').onclick=()=>{clearTemporaryPassword();temporaryPasswordDialog.close()};
function showTemporaryPassword(result){state.users=(state.users||[]).filter(user=>user.id!==result.user.id).concat(result.user);renderUsers();$('temporaryPasswordAccount').textContent='账号：'+result.user.username;$('temporaryPasswordValue').textContent=result.temporary_password;temporaryPasswordDialog.showModal()}
async function loadUsers(){
 if(!isAdministrator())return;
 state.users=await api('admin/users');renderUsers();
 const selected=state.managedUserID&&state.users.find(user=>user.id===state.managedUserID);
 if(selected)await loadUserResources();else {$('userResourcesPanel').hidden=true;state.managedUserID=null}
 renderStatsUsers();if(typeof renderManagedUserPage==='function')renderManagedUserPage();
}
function renderUsers(){
 $('userList').replaceChildren();
 for(const user of state.users||[]) {
  const row=document.createElement('tr');row.dataset.userId=user.id;
  row.innerHTML='<td><strong>'+txt(user.display_name||user.username)+'</strong><br>@'+txt(user.username)+'<br><span class="mono">'+txt(user.id)+'</span></td><td>'+txt(user.role==='admin'?'管理员':'成员')+' · '+(user.enabled?'已启用':'已停用')+(user.must_change_password?' · 待改密':'')+'</td><td><div class="actions"><button class="btn small" data-user-action="resources">资源 / Key</button><button class="btn small" data-user-action="display">昵称</button><button class="btn small" data-user-action="reset">重置密码</button><button class="btn small '+(user.enabled?'danger':'primary')+'" data-user-action="enabled">'+(user.enabled?'停用':'重新启用')+'</button></div></td>';
  row.querySelector('[data-user-action="resources"]').onclick=()=>{state.resourceOffset=0;location.hash='#/admin/users/'+user.id};
  row.querySelector('[data-user-action="display"]').onclick=()=>openEditor('修改昵称',{display_name:user.display_name},async fields=>{await api('admin/users/'+user.id,'PATCH',fields,undefined,user.version);await loadUsers()});
  row.querySelector('[data-user-action="reset"]').onclick=()=>openResetUser(user);
  if(user.id===state.user?.id){const button=row.querySelector('[data-user-action="reset"]');button.disabled=true;button.title='请使用个人中心的账户安全'}
  row.querySelector('[data-user-action="enabled"]').onclick=()=>{
   if(user.enabled&&!confirm('停用 '+user.username+'？浏览器会话将退出，Key 暂不可用但记录和统计保留。'))return;
   run(async()=>{try{await api('admin/users/'+user.id,'PATCH',{enabled:!user.enabled},undefined,user.version)}finally{await loadUsers()}});
  };
  $('userList').append(row);
 }
}
$('createUser').onclick=()=>openEditor('创建成员',{username:'',display_name:''},async fields=>{const result=await api('admin/users','POST',fields);$('editor').close();showTemporaryPassword(result)});
$('refreshUsers').onclick=()=>run(loadUsers);
function openResetUser(user){
 if(user.id===state.user?.id){setStatus('请使用个人中心的账户安全','error');return}
 $('modalTitle').textContent='重置 '+user.username+' 的密码';$('modalBody').innerHTML='<p>生成新的临时密码，撤销全部登录会话，再次要求首次改密。</p><label><input id="resetRevokeKeys" type="checkbox"> 同时删除全部播放器 Key、授权和调用统计（不可恢复）</label>';
 state.saveAction=async()=>{const result=await api('admin/users/'+user.id+'/reset-password','POST',{revoke_keys:$('resetRevokeKeys').checked},undefined,user.version);$('editor').close();showTemporaryPassword(result)};$('editor').showModal();
}
function managedUser(){return (state.users||[]).find(user=>user.id===state.managedUserID)}
async function loadUserResources(){
 const user=managedUser();if(!user)return;
 const kind=$('userResourceKind').value,endpoint=kind==='owned'||kind==='uploaded'?'tracks?relation='+kind+'&':'playlists'===kind?'playlists?':'keys?';
 const result=await api('admin/users/'+user.id+'/'+endpoint+'offset='+(state.resourceOffset||0)+'&limit=50');
 if(state.page!=='users'||state.managedUserID!==user.id)return;
 state.userResources=result.items;state.resourceTotal=result.total;
 $('userResourcesPanel').hidden=false;$('userResourcesTitle').textContent=user.username+' 的资源'+(user.enabled?'':'（账户已停用）');
 $('resourcesCount').textContent=(result.total?'第 '+((state.resourceOffset||0)+1)+'–'+Math.min((state.resourceOffset||0)+50,result.total)+' 条 / 共 '+result.total+' 条':'暂无资源');$('resourcesPrevious').disabled=!(state.resourceOffset>0);$('resourcesNext').disabled=(state.resourceOffset||0)+50>=result.total;
 $('userResources').replaceChildren();
 if(!result.items.length)$('userResources').textContent='暂无资源';
 for(const resource of result.items){
  const row=document.createElement('div');row.className='resource-item';
  if(['owned','playlists'].includes(kind)){const checkbox=document.createElement('input');checkbox.type='checkbox';checkbox.dataset.resourceId=resource.id;checkbox.setAttribute('aria-label','选择 '+(resource.title||resource.name));row.append(checkbox)}
  const label=document.createElement('div');label.className='resource-title';const name=document.createElement('strong');name.textContent=resource.title||resource.name||'未命名';label.append(name);if(resource.removed||resource.missing){const status=document.createElement('span');status.className='badge warn';status.textContent=resource.removed?'已下架':'文件缺失';label.append(status)}const metadata=document.createElement('details');metadata.innerHTML='<summary>技术信息</summary>';const identifier=document.createElement('span');identifier.className='mono';identifier.textContent=resource.id;metadata.append(identifier);label.append(metadata);row.append(label);
  if(kind==='keys'){const status=document.createElement('span');status.className='badge';status.textContent=resource.revoked_at?'已主动撤销':resource.expires_at&&Date.parse(resource.expires_at)<=Date.now()?'已过期':!user.enabled?'账户已停用':'有效';row.append(status)}
  if(kind==='playlists'){const view=document.createElement('button');view.className='btn small';view.textContent='查看 / 编辑';view.onclick=()=>beginPlaylistEdit(resource);row.append(view)}
  $('userResources').append(row);
 }
 const target=$('transferTarget'),previous=target.value;target.replaceChildren();for(const candidate of state.users.filter(candidate=>candidate.enabled&&candidate.id!==user.id)){const option=document.createElement('option');option.value=candidate.id;option.textContent=accountLabel(candidate);target.append(option)}if([...target.options].some(option=>option.value===previous))target.value=previous;
 $('transferControls').hidden=user.enabled;$('transferSelected').disabled=!['owned','playlists'].includes(kind);$('resourceNotice').textContent=user.enabled?'接管前先停用源账户。':'接管不转移 Key 或会话，原始上传者保持不变；旧私人歌单授权将移除。';if(typeof renderManagedUserPage==='function')renderManagedUserPage();
}
$('userResourceKind').onchange=()=>run(async()=>{state.resourceOffset=0;await loadUserResources()});
$('resourcesPrevious').onclick=()=>run(async()=>{state.resourceOffset=Math.max(0,(state.resourceOffset||0)-50);await loadUserResources()});
$('resourcesNext').onclick=()=>run(async()=>{state.resourceOffset=(state.resourceOffset||0)+50;await loadUserResources()});
async function allUserResources(id,kind){let offset=0;const result=[];for(;;){const page=await api('admin/users/'+id+'/'+kind+'?limit=500&offset='+offset);result.push(...page.items);offset+=page.items.length;if(offset>=page.total)break;if(!page.items.length)throw new Error('资源分页发生变化，请刷新重试')}return result}
async function transferUserResources(all){
 const user=managedUser(),target=(state.users||[]).find(candidate=>candidate.id===$('transferTarget').value);if(!user||!target)throw new Error('请选择接管目标');
 let tracks=[],playlists=[];
 if(all){[tracks,playlists]=await Promise.all([allUserResources(user.id,'tracks'),allUserResources(user.id,'playlists')])}else {const selected=new Set([...$('userResources').querySelectorAll('[data-resource-id]:checked')].map(input=>input.dataset.resourceId));const items=state.userResources.filter(resource=>selected.has(resource.id));if($('userResourceKind').value==='owned')tracks=items;else playlists=items}
 if(!tracks.length&&!playlists.length)throw new Error('请选择要接管的资源');
 if(!confirm('将 '+user.username+' 的 '+tracks.length+' 首曲目、'+playlists.length+' 个歌单接管给 '+target.username+'？'))return;
 const versions=items=>items.map(({id,version})=>({id,version}));
 try {await api('admin/users/'+user.id+'/transfer','POST',{target_id:target.id,target_version:target.version,all,tracks:versions(tracks),playlists:versions(playlists)},undefined,user.version);await refreshCore()}
 finally {await loadUsers()}
}
$('transferSelected').onclick=()=>run(()=>transferUserResources(false));$('transferAll').onclick=()=>run(()=>transferUserResources(true));

const statsUser=document.createElement('select');statsUser.id='statsUser';statsUser.className='select';statsUser.setAttribute('aria-label','按账户筛选统计');statsUser.hidden=true;$('page-stats').querySelector('.actions').prepend(statsUser);
statsUser.onchange=()=>{location.hash='#/admin/stats'+(statsUser.value?'?user='+encodeURIComponent(statsUser.value):'')};
function renderStatsUsers(){statsUser.hidden=!isAdministrator()||state.statsScope!=='admin';if(!isAdministrator())return;statsUser.replaceChildren();const all=document.createElement('option');all.value='';all.textContent='全部账户';statsUser.append(all);for(const user of state.users||[]){const option=document.createElement('option');option.value=user.id;option.textContent=accountLabel(user)+(user.enabled?'':'（账户已停用）');statsUser.append(option)}statsUser.value=state.statsUserID||''}
