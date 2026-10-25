'use strict';

const setupScreen=document.createElement('section');
setupScreen.id='setupScreen';setupScreen.className='login-screen';setupScreen.hidden=true;
setupScreen.innerHTML='<form id="setupForm" class="panel login-card"><h1>初始化音乐服务器</h1><p>设置第一个管理员。完成后初始化入口将自动关闭。</p><div class="field"><label for="setupToken">初始化授权码</label><input id="setupToken" type="password" autocomplete="off" required maxlength="64"><small id="setupHint">从服务器数据库目录中的 setup-token 文件获取授权码，或使用部署时打开的一次性链接。</small></div><div class="field"><label for="setupUsername">管理员账号</label><input id="setupUsername" value="admin" autocomplete="username" required pattern="[a-z0-9][a-z0-9_.-]{2,63}" maxlength="64"></div><div class="field"><label for="setupPassword">密码（至少 12 个字符）</label><input id="setupPassword" type="password" autocomplete="new-password" required minlength="12" maxlength="1024"></div><div class="field"><label for="setupConfirm">确认密码</label><input id="setupConfirm" type="password" autocomplete="new-password" required minlength="12" maxlength="1024"></div><button id="setupSubmit" class="btn primary" type="submit">创建管理员</button><p id="setupError" role="alert" class="status"></p></form>';
document.body.append(setupScreen);

// Fragments never reach HTTP access logs. Remove the capability from the
// address bar before subsequent navigation and retain it only in this form.
{
 const fragment=new URLSearchParams(location.hash.slice(1));
 if(fragment.has('setup')) {
  $('setupToken').value=fragment.get('setup');
  history.replaceState(null,'',location.pathname+location.search);
 }
}

async function initializeAccountScreen(){
 const loginFields=[...$('loginForm').querySelectorAll('input,button')];
 loginFields.forEach(field=>field.disabled=true);$('loginForm').setAttribute('aria-busy','true');$('loginButton').textContent='正在检查登录…';
 try {
  const status=await api('setup');
  if(status.required){
   $('loginScreen').hidden=true;setupScreen.hidden=false;
   if($('setupToken').value){$('setupToken').closest('.field').hidden=true;$('setupPassword').focus()}
   else $('setupToken').focus();
   return;
  }
  $('setupForm').reset();
  await enterAccount(await api('auth/me'));
 }catch(error){setLoggedOut();if(error.status!==401)$('loginError').textContent=error.message}
 finally{loginFields.forEach(field=>field.disabled=false);$('loginForm').removeAttribute('aria-busy');$('loginButton').textContent='登录'}
}
$('setupForm').onsubmit=async event=>{
 event.preventDefault();$('setupError').textContent='';$('setupSubmit').disabled=true;
 try {
  if($('setupPassword').value!==$('setupConfirm').value)throw new Error('两次密码不一致');
  await api('setup','POST',{token:$('setupToken').value,username:$('setupUsername').value,password:$('setupPassword').value,confirm_password:$('setupConfirm').value});
  $('username').value=$('setupUsername').value;
  $('setupForm').reset();setupScreen.hidden=true;$('loginScreen').hidden=false;
  $('loginError').textContent='管理员已创建，请使用刚设置的密码登录。';$('password').focus();
 }catch(error){
  $('setupError').textContent=error.message;
  if(error.status===403){$('setupToken').closest('.field').hidden=false;$('setupToken').focus()}
 }finally{$('setupSubmit').disabled=false}
};
