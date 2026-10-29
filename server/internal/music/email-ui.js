'use strict';

let emailEpoch=0,emailView=null,emailBusy=false,emailProfileGeneration=0;
let registrationRequest=0,registrationConfig={enabled:false,mail_ready:false};
let registrationSettings=null,registrationDirty=false,registrationSaving=false;
let mailSettings=null,mailDirty=false,mailSaving=false,mailPollTimer;
let emailSettingsRoute='';
function emailCurrent(owner,epoch){return state.user?.id===owner&&emailEpoch===epoch}
function emailProfileDirty(){return Boolean($('accountEmail')&&emailView&&($('accountEmail').value.trim()!==(emailView.pending_email||emailView.email||'')||$('emailCurrentPassword').value||$('emailCode').value))}
function permitEmailNavigation(hash){
  if(!emailSettingsRoute||hash===emailSettingsRoute)return true;
  if(mailSaving||registrationSaving){history.replaceState(null,'',emailSettingsRoute);return false}
  const dirty=emailSettingsRoute.endsWith('/mail')?mailDirty:registrationDirty;
  if(dirty&&!confirm('设置尚未保存，放弃修改并离开？')){history.replaceState(null,'',emailSettingsRoute);return false}
  mailDirty=false;registrationDirty=false;emailSettingsRoute='';clearTimeout(mailPollTimer);return true;
}
function emailSetFormBusy(id,busy){$(id).querySelectorAll('input,select,button').forEach(input=>input.disabled=busy)}
function emailMessage(id,message='',error=false){const node=$(id);if(node){node.textContent=message;node.classList.toggle('form-error',error);node.hidden=!message}}
function emailQuotaBytes(mode,amount,unit){
  if(mode==='unlimited')return 0;
  const bytes=Math.round(Number(amount)*(unit==='GiB'?1073741824:1048576));
  if(!String(amount).trim()||!Number.isSafeInteger(bytes)||bytes<=0||bytes>1125899906842624)throw new Error('请输入有效的正数额度，或明确选择不限额。');
  return bytes;
}
function setupEmailWorkspace(){
  if($('registrationScreen'))return;
  const screen=document.createElement('section');screen.id='registrationScreen';screen.className='login-screen';screen.hidden=true;
  screen.innerHTML='<form id="registrationForm" class="panel login-card"><h1>注册账户</h1><p class="notice">注册后登录并验证邮箱，即可按注册时的默认额度上传音乐。</p><div class="field"><label for="registrationUsername">登录名</label><input id="registrationUsername" autocomplete="username" maxlength="64" required pattern="[A-Za-z0-9][A-Za-z0-9_.\\-]{2,63}"><small>3–64 个字母、数字、点、短横线或下划线；昵称可在注册后修改。</small></div><div class="field"><label for="registrationPassword">密码</label><input id="registrationPassword" type="password" autocomplete="new-password" minlength="12" maxlength="1024" required></div><div class="field"><label for="registrationConfirm">确认密码</label><input id="registrationConfirm" type="password" autocomplete="new-password" minlength="12" maxlength="1024" required></div><p id="registrationError" class="form-error" role="alert" hidden></p><div class="actions"><button id="registerAccount" class="btn primary" type="submit">注册账户</button><button id="backRegistrationLogin" class="btn" type="button">返回登录</button></div></form>';
  document.body.append(screen);
  const link=makeButton('注册账户',()=>{
    if(state.user||!registrationConfig.enabled)return;
    $('loginScreen').hidden=true;screen.hidden=false;$('registrationForm').reset();emailMessage('registrationError');$('registrationUsername').focus();
  });link.id='openRegistration';link.type='button';link.className='btn registration-entry';link.hidden=true;$('loginForm').append(link);
  $('backRegistrationLogin').onclick=()=>{screen.hidden=true;$('registrationForm').reset();emailMessage('registrationError');if(!state.user)$('loginScreen').hidden=false};
  $('registrationForm').onsubmit=registerAccount;
  window.addEventListener('beforeunload',event=>{if(state.user&&state.page==='settings'&&(mailDirty||registrationDirty)){event.preventDefault();event.returnValue=''}});
}
async function loadRegistrationConfiguration(){
  const request=++registrationRequest;
  try{
    const response=await fetch('/api/v2/registration/config',{credentials:'same-origin',cache:'no-store'});
    if(!response.ok)throw new Error('注册状态读取失败');const config=await response.json();if(request!==registrationRequest)return;
    registrationConfig=config;$('openRegistration').hidden=!config.enabled;
  }catch(_){if(request===registrationRequest){registrationConfig={enabled:false,mail_ready:false};if($('openRegistration'))$('openRegistration').hidden=true}}
}
async function registerAccount(event){
  event.preventDefault();if($('registerAccount').disabled)return;emailMessage('registrationError');
  const username=$('registrationUsername').value.trim(),password=$('registrationPassword').value,confirm=$('registrationConfirm').value,epoch=emailEpoch;
  if(password!==confirm){emailMessage('registrationError','两次密码不一致。',true);return}
  if(Array.from(password).length<12||new TextEncoder().encode(password).length>1024){emailMessage('registrationError','密码至少 12 个字符，最多 1024 个 UTF-8 字节。',true);return}
  $('registerAccount').disabled=true;
  try{
    await api('registration','POST',{username,password});if(epoch!==emailEpoch||state.user)return;
    $('registrationScreen').hidden=true;$('registrationForm').reset();$('loginScreen').hidden=false;$('username').value=username;$('password').value='';$('loginError').textContent='注册成功，请登录后验证邮箱。';$('password').focus();
  }catch(error){if(epoch===emailEpoch)emailMessage('registrationError',error.message,true)}
  finally{if(epoch===emailEpoch)$('registerAccount').disabled=false}
}
function setupEmailProfile(){}
function setupEmailEditor(){
  if($('emailChallengeForm'))return;
  const section=document.createElement('section');section.id='emailEditorFields';section.className='email-profile';
  section.innerHTML='<div class="storage-section-head"><h2>邮箱与上传资格</h2><button id="refreshEmail" class="btn small" type="button">刷新状态</button></div><p id="emailEditorBindingStatus" class="notice"></p><form id="emailChallengeForm" novalidate><div class="field"><label for="accountEmail">邮箱地址</label><input id="accountEmail" type="email" autocomplete="email" maxlength="254" required></div><div class="field" id="emailPasswordField" hidden><label for="emailCurrentPassword">当前密码</label><input id="emailCurrentPassword" type="password" autocomplete="current-password" maxlength="1024"><small>更换已绑定邮箱需要验证当前密码；新邮箱验证前保留原绑定。</small></div><div class="actions"><button id="sendEmailCode" class="btn" type="submit">发送验证码</button></div></form><form id="emailVerifyForm" novalidate hidden><div class="field"><label for="emailCode">邮箱验证码</label><input id="emailCode" inputmode="numeric" autocomplete="one-time-code" maxlength="6" pattern="[0-9]{6}" aria-describedby="emailDeliveryStatus"></div><button id="verifyEmailCode" class="btn primary" type="submit">验证邮箱</button></form><p id="emailDeliveryStatus" class="status" role="status"></p><p id="emailProfileError" class="form-error" role="alert" hidden></p>';
  $('personalEditBody').append(section);$('emailChallengeForm').onsubmit=sendEmailChallenge;$('emailVerifyForm').onsubmit=verifyEmailChallenge;$('refreshEmail').onclick=()=>loadEmailProfile(true);
}

function renderEmailProfile(view){
 const requirement=view.email_verification_required===false?'管理员已免除邮箱验证。':view.email_verified?'邮箱已验证。':'需要验证邮箱后才能上传。';
 for(const id of ['emailBindingStatus','emailEditorBindingStatus'])if($(id)){$(id).textContent=(view.email?'当前绑定：'+view.email+'。':'尚未绑定邮箱。')+requirement;$(id).hidden=false}
 if(!$('accountEmail'))return;
 if(!$('accountEmail').value)$('accountEmail').value=view.pending_email||view.email||'';
 $('emailPasswordField').hidden=!view.email_verified;$('emailVerifyForm').hidden=!view.challenge_id;
 const labels={queued:'邮件已排队。',sending:'正在发送。',submitted:'邮件已提交，请检查收件箱及垃圾邮件。',sent:'邮件已提交，请检查收件箱及垃圾邮件。',failed:'投递失败，请稍后重试。',expired:'验证码已过期，请重新发送。',locked:'错误次数过多，请稍后再试。'};
 $('emailDeliveryStatus').textContent=(view.pending_email?'待验证邮箱：'+view.pending_email+'。':'')+(labels[view.delivery_status]||'')+(view.retry_after>0?' 重发请等待 '+view.retry_after+' 秒。':'');
}
async function loadEmailProfile(force=false){
 if(!state.user)return;const owner=state.user.id,epoch=emailEpoch,generation=++emailProfileGeneration;
 try{const view=await api('auth/email');if(!emailCurrent(owner,epoch)||generation!==emailProfileGeneration)return;emailView=view;renderEmailProfile(view);if(force)emailMessage('emailProfileError')}
 catch(error){if(emailCurrent(owner,epoch)&&generation===emailProfileGeneration)emailMessage($('emailProfileError')?'emailProfileError':'emailProfilePageError',error.message,true)}
}
function setEmailBusy(busy){emailBusy=busy;for(const id of ['sendEmailCode','verifyEmailCode','refreshEmail','accountEmail','emailCurrentPassword','emailCode'])if($(id))$(id).disabled=busy}
async function sendEmailChallenge(event){
  event.preventDefault();if(emailBusy||!state.user)return;
  const email=$('accountEmail').value.trim();if(!$('accountEmail').checkValidity()){emailMessage('emailProfileError','请输入有效的邮箱地址。',true);return}
  const owner=state.user.id,epoch=emailEpoch;setEmailBusy(true);emailMessage('emailProfileError');
  try{
    const view=await api('auth/email/challenge','POST',{email,current_password:$('emailCurrentPassword').value});if(!emailCurrent(owner,epoch))return;
    emailView=view;renderEmailProfile(view);$('emailCurrentPassword').value='';$('emailCode').value='';setStatus('验证码请求已提交，请检查邮件。','ok');
  }catch(error){if(emailCurrent(owner,epoch))emailMessage('emailProfileError',error.message,true)}
  finally{if(emailCurrent(owner,epoch))setEmailBusy(false)}
}
async function verifyEmailChallenge(event){
  event.preventDefault();if(emailBusy||!state.user||!emailView?.challenge_id)return;
  const code=$('emailCode').value.trim();if(!/^[0-9]{6}$/.test(code)){emailMessage('emailProfileError','请输入六位数字验证码。',true);return}
  const owner=state.user.id,epoch=emailEpoch;setEmailBusy(true);emailMessage('emailProfileError');
  try{
    const view=await api('auth/email/verify','POST',{challenge_id:emailView.challenge_id,code});if(!emailCurrent(owner,epoch))return;
    emailView=view;renderEmailProfile(view);$('emailCode').value='';$('emailCurrentPassword').value='';
    const identity=await api('auth/me');if(!emailCurrent(owner,epoch))return;
    applyPersonalIdentity(identity);renderUploads();await loadEmailProfile();if(emailCurrent(owner,epoch)){setEmailBusy(false);closePersonalEditor();setStatus('邮箱验证成功，账户上传资格已更新。','ok');}
  }catch(error){if(emailCurrent(owner,epoch))emailMessage('emailProfileError',error.message,true)}
  finally{if(emailCurrent(owner,epoch))setEmailBusy(false)}
}
function setupEmailSettings(registration,mail){
  registration.innerHTML='<h2>注册与账户</h2><p class="notice">默认额度仅影响保存后注册的新账户。旧账户保留原额度，但仍需按账户要求验证邮箱后上传。</p><form id="registrationSettingsForm" class="email-settings-form" novalidate><label class="email-toggle"><input id="registrationEnabled" type="checkbox"> 允许自行注册</label><fieldset class="field create-quota"><legend>新注册账户默认额度</legend><label><input name="registrationQuotaMode" type="radio" value="limited" checked> 限制额度</label><label><input name="registrationQuotaMode" type="radio" value="unlimited"> 不限额</label><div id="registrationQuotaAmount" class="quota-amount"><input id="registrationQuotaValue" type="number" min="0" step="any" aria-label="默认配额数值"><select id="registrationQuotaUnit" aria-label="默认配额单位"><option value="GiB">GiB</option><option value="MiB">MiB</option></select></div></fieldset><p id="registrationMailReadiness" class="status"></p><p id="registrationSettingsError" class="form-error" role="alert" hidden></p><div class="actions"><button id="saveRegistrationSettings" class="btn primary" type="submit">保存注册设置</button><button id="reloadRegistrationSettings" class="btn" type="button">重新读取</button></div></form>';
  mail.innerHTML='<h2>邮件服务</h2><p class="notice">使用支持 TLS 的 SMTP 服务发送验证码。发信凭据仅保存在服务器，不回显到网页。</p><form id="mailSettingsForm" class="email-settings-form" novalidate><div class="form-grid"><div class="field"><label for="mailHost">SMTP 主机</label><input id="mailHost" autocomplete="off" maxlength="253"></div><div class="field"><label for="mailPort">端口</label><input id="mailPort" type="number" min="1" max="65535"></div><div class="field"><label for="mailTLS">加密方式</label><select id="mailTLS"><option value="starttls">STARTTLS（必须加密）</option><option value="tls">TLS（连接时加密）</option></select></div><div class="field"><label for="mailFrom">发信邮箱</label><input id="mailFrom" type="email" autocomplete="off" maxlength="254"></div><div class="field"><label for="mailUsername">SMTP 用户名</label><input id="mailUsername" autocomplete="off" maxlength="254"></div><div class="field"><label for="mailPassword">SMTP 密码或授权码</label><input id="mailPassword" type="password" autocomplete="new-password" maxlength="2048"><small id="mailCredentialState">读取中…</small></div></div><p id="mailSettingsState" class="status"></p><p id="mailSettingsError" class="form-error" role="alert" hidden></p><div class="actions"><button id="saveMailSettings" class="btn primary" type="submit">保存邮件设置</button><button id="reloadMailSettings" class="btn" type="button">重新读取</button></div></form><form id="mailTestForm" class="email-test-form" novalidate><div class="field"><label for="mailTestAddress">测试收件邮箱</label><input id="mailTestAddress" type="email" autocomplete="email" maxlength="254"></div><button id="sendMailTest" class="btn" type="submit">发送测试邮件</button><p id="mailTestStatus" class="status" role="status"></p></form>';
  $('registrationSettingsForm').oninput=()=>{registrationDirty=true;syncRegistrationQuota()};$('registrationSettingsForm').onsubmit=saveRegistrationSettings;
  $('reloadRegistrationSettings').onclick=()=>{if(!registrationDirty||confirm('放弃未保存的注册设置？'))loadRegistrationSettings(true)};
  $('mailSettingsForm').oninput=()=>{mailDirty=true;if(mailSettings)renderMailState(mailSettings)};$('mailSettingsForm').onsubmit=saveMailSettings;
  $('reloadMailSettings').onclick=()=>{if(!mailDirty||confirm('放弃未保存的邮件设置？'))loadMailSettings(true)};
  $('mailTestForm').onsubmit=sendMailTest;
}
function syncRegistrationQuota(){const limited=document.querySelector('[name="registrationQuotaMode"]:checked').value==='limited';$('registrationQuotaAmount').hidden=!limited;$('registrationQuotaValue').disabled=!limited||registrationSaving;$('registrationQuotaUnit').disabled=registrationSaving}
async function loadRegistrationSettings(force=false){
  if(!isAdministrator())return;const owner=state.user.id,epoch=emailEpoch;
  emailSettingsRoute='#/admin/settings/registration';
  try{
    const settings=await api('admin/registration');if(!emailCurrent(owner,epoch))return;
    registrationSettings=settings;
    if(force||!registrationDirty){registrationDirty=false;$('registrationEnabled').checked=settings.enabled;const bytes=settings.default_quota_bytes,unit=bytes&&bytes<1073741824?'MiB':'GiB';$('registrationQuotaUnit').value=unit;$('registrationQuotaValue').value=settings.quota_configured&&bytes?String(bytes/(unit==='GiB'?1073741824:1048576)):'';document.querySelector('[name="registrationQuotaMode"][value="'+(settings.quota_configured&&bytes===0?'unlimited':'limited')+'"]').checked=true;syncRegistrationQuota()}
    $('registrationMailReadiness').textContent=settings.mail_ready?'邮件服务已通过投递测试，可开启注册。':'开启注册前，先保存默认额度并完成邮件服务测试。';
    $('registrationMailReadiness').hidden=false;
    if(force)emailMessage('registrationSettingsError');
  }catch(error){if(emailCurrent(owner,epoch))emailMessage('registrationSettingsError',error.message,true)}
}
async function saveRegistrationSettings(event){
  event.preventDefault();if(registrationSaving||!registrationSettings)return;const owner=state.user.id,epoch=emailEpoch;
  let quota;try{quota=emailQuotaBytes(document.querySelector('[name="registrationQuotaMode"]:checked').value,$('registrationQuotaValue').value,$('registrationQuotaUnit').value)}catch(error){emailMessage('registrationSettingsError',error.message,true);return}
  const payload={enabled:$('registrationEnabled').checked,default_quota_bytes:quota,quota_configured:true,version:registrationSettings.version};registrationSaving=true;emailSetFormBusy('registrationSettingsForm',true);emailMessage('registrationSettingsError');
  try{await api('admin/registration','PUT',payload);if(!emailCurrent(owner,epoch))return;registrationDirty=false;await loadRegistrationSettings(true);if(!emailCurrent(owner,epoch))return;await loadRegistrationConfiguration();if(emailCurrent(owner,epoch))setStatus('注册设置已保存，仅影响之后注册的账户。','ok')}
  catch(error){if(emailCurrent(owner,epoch))emailMessage('registrationSettingsError',error.message,true)}
  finally{if(emailCurrent(owner,epoch)){registrationSaving=false;emailSetFormBusy('registrationSettingsForm',false);syncRegistrationQuota()}}
}
function renderMailState(settings){
  $('mailSettingsState').textContent=mailDirty?'存在未保存的邮件设置，请先保存再发送测试邮件。':settings.tested?'邮件服务已通过投递测试。':settings.configured?'已配置，完成测试邮件后可启用注册。':'尚未完成邮件服务配置。';
  $('mailSettingsState').hidden=false;
  $('mailCredentialState').textContent=settings.configured?'凭据已在服务器保存；留空保留原值。':'请输入服务商提供的 SMTP 密码或授权码。';
  $('mailCredentialState').hidden=false;
  $('mailPassword').placeholder=settings.configured?'已配置，留空保留':'请输入密码或授权码';$('sendMailTest').disabled=!settings.configured||mailSaving||mailDirty;
}
async function loadMailSettings(force=false){
  if(!isAdministrator())return;const owner=state.user.id,epoch=emailEpoch;
  emailSettingsRoute='#/admin/settings/mail';
  try{
    const settings=await api('admin/mail');if(!emailCurrent(owner,epoch))return;mailSettings=settings;
    if(force||!mailDirty){mailDirty=false;$('mailHost').value=settings.host||'';$('mailPort').value=settings.port||587;$('mailTLS').value=settings.tls_mode||'starttls';$('mailFrom').value=settings.from_address||'';$('mailUsername').value=settings.username||'';$('mailPassword').value=''}
    renderMailState(settings);if(force)emailMessage('mailSettingsError');
  }catch(error){if(emailCurrent(owner,epoch))emailMessage('mailSettingsError',error.message,true)}
}
async function saveMailSettings(event){
  event.preventDefault();if(mailSaving||!mailSettings)return;const owner=state.user.id,epoch=emailEpoch;
  const payload={host:$('mailHost').value.trim(),port:Number($('mailPort').value),tls_mode:$('mailTLS').value,from_address:$('mailFrom').value.trim(),username:$('mailUsername').value,password:$('mailPassword').value,version:mailSettings.version};
  if(!payload.host||!Number.isInteger(payload.port)||payload.port<1||payload.port>65535||!$('mailFrom').checkValidity()||!payload.from_address){emailMessage('mailSettingsError','请填写有效的主机、端口和发信邮箱。',true);return}
  mailSaving=true;emailSetFormBusy('mailSettingsForm',true);emailMessage('mailSettingsError');
  try{await api('admin/mail','PUT',payload);if(!emailCurrent(owner,epoch))return;mailDirty=false;$('mailPassword').value='';await loadMailSettings(true);if(emailCurrent(owner,epoch))setStatus('邮件设置已保存，请发送测试邮件。','ok')}
  catch(error){if(emailCurrent(owner,epoch))emailMessage('mailSettingsError',error.message,true)}
  finally{if(emailCurrent(owner,epoch)){mailSaving=false;emailSetFormBusy('mailSettingsForm',false);if(mailSettings)renderMailState(mailSettings)}}
}
async function sendMailTest(event){
  event.preventDefault();if(!mailSettings?.configured||mailDirty)return;const email=$('mailTestAddress').value.trim();if(!email||!$('mailTestAddress').checkValidity()){emailMessage('mailTestStatus','请输入有效的测试收件邮箱。',true);return}
  const owner=state.user.id,epoch=emailEpoch;$('sendMailTest').disabled=true;emailMessage('mailTestStatus','正在提交测试邮件…');
  try{await api('admin/mail/test','POST',{email});if(!emailCurrent(owner,epoch))return;emailMessage('mailTestStatus','测试邮件已排队，稍后刷新状态并检查收件箱。');let remaining=12;clearTimeout(mailPollTimer);
    const poll=async()=>{if(!emailCurrent(owner,epoch)||mailDirty||state.page!=='settings'||!location.hash.startsWith('#/admin/settings/mail'))return;await loadMailSettings();if(!emailCurrent(owner,epoch))return;if(mailSettings?.tested){emailMessage('mailTestStatus','邮件已提交服务，投递测试通过。');return}if(--remaining>0)mailPollTimer=setTimeout(poll,2500)};mailPollTimer=setTimeout(poll,1000);
  }catch(error){if(emailCurrent(owner,epoch))emailMessage('mailTestStatus',error.message,true)}
  finally{if(emailCurrent(owner,epoch)&&mailSettings)renderMailState(mailSettings)}
}
function clearEmailWorkspace(){
  emailEpoch++;emailProfileGeneration++;registrationRequest++;emailView=null;emailBusy=false;registrationSettings=null;registrationDirty=false;registrationSaving=false;mailSettings=null;mailDirty=false;mailSaving=false;emailSettingsRoute='';clearTimeout(mailPollTimer);
  if(typeof clearUserAccountWorkspace==='function')clearUserAccountWorkspace();
  for(const id of ['registrationForm','emailChallengeForm','emailVerifyForm','registrationSettingsForm','mailSettingsForm','mailTestForm'])$(id)?.reset();
  if($('registrationScreen'))$('registrationScreen').hidden=true;if($('emailVerifyForm'))$('emailVerifyForm').hidden=true;
  for(const id of ['emailBindingStatus','personalStorageStatus','emailDeliveryStatus','emailProfileError','registrationSettingsError','registrationMailReadiness','mailSettingsState','mailSettingsError','mailTestStatus','mailCredentialState','registrationError'])emailMessage(id);
  for(const id of ['registrationSettingsForm','mailSettingsForm'])if($(id))emailSetFormBusy(id,false);
  for(const id of ['registerAccount','saveRegistrationSettings','saveMailSettings'])if($(id))$(id).disabled=false;setEmailBusy(false);
}
