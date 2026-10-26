const assert=require('node:assert/strict');
const path=require('node:path');

// Real account/quota endpoints; mail and public registration are deliberately
// intercepted below to exercise the browser contract without external delivery.
module.exports=async function registrationEmailUI({page,browser,base,directory}){
  base=base||page.url().split('#')[0];
  const contexts=[],handlers=[];let createdID;
  const route=async(pattern,handler)=>{await page.route(pattern,handler);handlers.push([pattern,handler])};
  try{
    await page.goto(base+'#/admin/users');await page.waitForFunction(()=>state.page==='users'&&document.querySelector('#createUser'));
    await page.click('#createUser');await page.locator('#editor').waitFor({state:'visible'});
    assert.equal(await page.locator('#modalBody [data-field="display_name"],#editor-field-display_name').count(),0,'Nickname is chosen later in personal settings');
    assert.equal(await page.locator('#createRequireEmail').isChecked(),true,'New members require verification by default');
    await page.fill('#editor-field-username','EmailCaseMember');await page.fill('#createQuotaValue','16');await page.selectOption('#createQuotaUnit','MiB');
    await page.click('#saveModal');await page.locator('#temporaryPasswordDialog').waitFor({state:'visible'});
    assert.match(await page.locator('#temporaryPasswordAccount').textContent(),/EmailCaseMember/);
    createdID=await page.evaluate(()=>state.users.find(user=>user.username==='EmailCaseMember').id);
    const created=await page.evaluate(id=>state.users.find(user=>user.id===id),createdID);
    assert.equal(created.display_name,'EmailCaseMember');assert.equal(created.email_verification_required,true);
    assert.ok((await page.locator('#temporaryPasswordValue').textContent()).length>=12);await page.click('#closeTemporaryPassword');
    assert.equal(await page.locator('#temporaryPasswordValue').textContent(),'');
    await page.goto(base+'#/admin/users/'+createdID);await page.locator('#managedRequireEmail').waitFor({state:'visible'});
    assert.equal(await page.locator('#managedRequireEmail').isChecked(),true);
    await page.uncheck('#managedRequireEmail');await page.waitForFunction(id=>state.users.find(user=>user.id===id)?.email_verification_required===false,createdID);
    await page.locator('[data-account-action="quota"]').click();await page.locator('#quotaEditor').waitFor({state:'visible'});
    assert.equal(await page.inputValue('#quotaLimit'),'16');assert.equal(await page.inputValue('#quotaUnit'),'MiB');await page.click('#cancelQuota');

    let mail={host:'',port:587,tls_mode:'starttls',from_address:'',username:'',version:0,configured:false,tested:false};
    let failMail=true,savedMail;
    await route('**/api/v2/admin/mail',async request=>{
      if(request.request().method()==='PUT'){
        const body=request.request().postDataJSON();savedMail=body;
        if(failMail){failMail=false;return request.fulfill({status:409,json:{error:{code:'VERSION_CONFLICT',message:'Fixture version changed'}}})}
        mail={...body,version:body.version+1,configured:true,tested:false};delete mail.password;
      }
      await request.fulfill({status:200,json:mail});
    });
    await route('**/api/v2/admin/mail/test',async request=>{assert.equal(request.request().postDataJSON().email,'test@example.invalid');mail.tested=true;await request.fulfill({status:202,json:{queued:true}})});
    await page.goto(base+'#/admin/settings/mail');await page.waitForFunction(()=>mailSettings!==null);
    await page.fill('#mailHost','smtp.example.invalid');await page.fill('#mailFrom','sender@example.invalid');await page.fill('#mailUsername','fixture-user');await page.fill('#mailPassword','fixture SMTP authorization');
    await page.click('#saveMailSettings');await page.waitForFunction(()=>document.querySelector('#mailSettingsError').textContent.includes('409'));
    assert.equal(await page.inputValue('#mailHost'),'smtp.example.invalid');assert.equal(await page.inputValue('#mailPassword'),'fixture SMTP authorization','A save failure preserves the draft');
    await page.click('#saveMailSettings');await page.waitForFunction(()=>mailSettings.configured&&!mailSaving);
    assert.equal(savedMail.tls_mode,'starttls');assert.equal(await page.inputValue('#mailPassword'),'','Saved secret is removed from the DOM');
    await page.fill('#mailTestAddress','test@example.invalid');await page.click('#sendMailTest');await page.waitForFunction(()=>mailSettings.tested===true);

    let policy={enabled:false,mail_ready:true,default_quota_bytes:16777216,quota_configured:true,version:1},failPolicy=true;
    await route('**/api/v2/admin/registration',async request=>{
      if(request.request().method()==='PUT'){
        const body=request.request().postDataJSON();
        if(failPolicy){failPolicy=false;return request.fulfill({status:409,json:{error:{code:'VERSION_CONFLICT',message:'Fixture policy changed'}}})}
        policy={...body,mail_ready:true,version:body.version+1};
      }
      await request.fulfill({status:200,json:policy});
    });
    await route('**/api/v2/registration/config',request=>request.fulfill({status:200,json:{enabled:policy.enabled,mail_ready:true}}));
    await page.goto(base+'#/admin/settings/registration');await page.waitForFunction(()=>registrationSettings!==null);
    await page.fill('#registrationQuotaValue','32');await page.selectOption('#registrationQuotaUnit','MiB');await page.check('#registrationEnabled');
    await page.click('#saveRegistrationSettings');await page.waitForFunction(()=>document.querySelector('#registrationSettingsError').textContent.includes('409'));
    assert.equal(await page.inputValue('#registrationQuotaValue'),'32');assert.equal(await page.locator('#registrationEnabled').isChecked(),true);
    await page.click('#saveRegistrationSettings');await page.waitForFunction(()=>registrationSettings.enabled&&!registrationSaving);
    assert.equal(policy.default_quota_bytes,33554432);

    const originalIdentity=await page.evaluate(()=>({user:{...state.user},csrf_token:state.csrf}));
    let view={email:'',email_verified:false,email_verification_required:true,upload_allowed:false,pending_email:'',challenge_id:'',delivery_status:''};
    await route('**/api/v2/auth/email',request=>request.fulfill({status:200,json:view}));
    await route('**/api/v2/auth/email/challenge',async request=>{
      assert.equal(request.request().postDataJSON().email,'person@example.invalid');
      view={...view,pending_email:'person@example.invalid',challenge_id:'fixture-challenge',delivery_status:'sent',retry_after:60,expires_at:new Date(Date.now()+600000).toISOString()};await request.fulfill({status:202,json:view});
    });
    await route('**/api/v2/auth/email/verify',async request=>{
      const body=request.request().postDataJSON();assert.equal(body.challenge_id,'fixture-challenge');
      if(body.code!=='123456')return request.fulfill({status:400,json:{error:{code:'EMAIL_CODE_INVALID',message:'Fixture invalid code'}}});
      view={email:'person@example.invalid',email_verified:true,email_verification_required:true,upload_allowed:true,pending_email:'',challenge_id:'',delivery_status:'sent'};await request.fulfill({status:200,json:view});
    });
    await route('**/api/v2/auth/me',request=>request.fulfill({status:200,json:{...originalIdentity,user:{...originalIdentity.user,email:view.email,email_verified:view.email_verified,email_verification_required:true,upload_allowed:view.upload_allowed}}}));
    await page.goto(base+'#/account/profile');await page.waitForFunction(()=>document.querySelector('#emailBindingStatus').textContent.includes('需要验证'));
    await page.fill('#accountEmail','person@example.invalid');await page.click('#sendEmailCode');await page.locator('#emailVerifyForm').waitFor({state:'visible'});
    await page.fill('#emailCode','123450');await page.click('#verifyEmailCode');await page.waitForFunction(()=>document.querySelector('#emailProfileError').textContent.includes('400'));
    assert.equal(await page.inputValue('#emailCode'),'123450');
    await page.fill('#emailCode','123456');await page.click('#verifyEmailCode');await page.waitForFunction(()=>state.user.email_verified&&state.user.upload_allowed);
    assert.equal(await page.inputValue('#emailCode'),'');assert.ok((await page.locator('#emailBindingStatus').textContent()).includes('邮箱已验证'));assert.equal(await page.locator('#emailBindingStatus').isVisible(),true);assert.equal(await page.locator('#personalStorageStatus').isVisible(),true);
    await page.setViewportSize({width:390,height:844});await page.waitForFunction(()=>document.querySelector('.main').getBoundingClientRect().left<=1&&document.querySelector('#sidebar').getBoundingClientRect().right<=1);await page.evaluate(()=>document.querySelector('#notifications')?.replaceChildren());await page.screenshot({path:path.join(directory,'email-profile-mobile.png'),fullPage:true});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);await page.setViewportSize({width:1440,height:1100});
    await page.evaluate(()=>clearEmailWorkspace());assert.equal(await page.inputValue('#accountEmail'),'');assert.equal(await page.inputValue('#emailCurrentPassword'),'');assert.equal(await page.inputValue('#emailCode'),'');

    const context=await browser.newContext({viewport:{width:390,height:844}});contexts.push(context);const anonymous=await context.newPage();let registrationBody;
    await anonymous.route('**/api/v2/registration/config',request=>request.fulfill({status:200,json:{enabled:true,mail_ready:true}}));
    await anonymous.route('**/api/v2/registration',async request=>{registrationBody=request.request().postDataJSON();await request.fulfill({status:201,json:{registered:true}})});
    await anonymous.goto(base);await anonymous.locator('#openRegistration').waitFor({state:'visible'});await anonymous.click('#openRegistration');
    await anonymous.fill('#registrationUsername','SelfRegistration');await anonymous.fill('#registrationPassword','fixture registration password');await anonymous.fill('#registrationConfirm','fixture registration password');await anonymous.click('#registerAccount');
    await anonymous.locator('#loginScreen').waitFor({state:'visible'});assert.equal(await anonymous.inputValue('#username'),'SelfRegistration');assert.equal(await anonymous.inputValue('#password'),'');assert.deepEqual(Object.keys(registrationBody).sort(),['password','username']);
    assert.equal(await anonymous.evaluate(()=>state.user),null,'Registration does not fabricate an authenticated identity');assert.equal(await anonymous.inputValue('#registrationPassword'),'');assert.equal(await anonymous.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
    console.log('Registration/email UI passed: real member creation with quota/verification selection; isolated browser contracts for SMTP drafts/test, registration policy, binding, code errors, identity refresh, cleanup and mobile registration (mail/API mocks; transport covered separately).');
  }finally{
    for(const context of contexts)await context.close();for(const[pattern,handler]of handlers)await page.unroute(pattern,handler);
    await page.setViewportSize({width:1440,height:1100});await page.goto(base+'#/admin/users');await page.waitForFunction(()=>state.user?.role==='admin'&&state.page==='users'&&document.querySelector('#routeError').hidden);
    if(createdID)await page.evaluate(async id=>{const users=await api('admin/users');let user=users.find(item=>item.id===id);if(!user)return;user=await api('admin/users/'+id,'PATCH',{enabled:false},undefined,user.version);await api('admin/users/'+id,'DELETE',undefined,undefined,user.version);await loadUsers()},createdID);
  }
};

async function realBrowserFlow({browser,page,base,directory,fs}){
  const {waitForAsync}=require('./browser-wait.cjs');
  const contexts=[];
  const delivery=async target=>{
    const deadline=Date.now()+15000;
    while(Date.now()<deadline){for(const name of await fs.readdir(path.join(directory,'deliveries'))){try{const mail=JSON.parse(await fs.readFile(path.join(directory,'deliveries',name),'utf8'));if(mail.to===target&&/^[0-9]{6}$/.test(mail.code||''))return mail}catch{}}await new Promise(resolve=>setTimeout(resolve,50))}
    throw new Error('Isolated mail worker did not produce the expected delivery');
  };
  const ageChallenge=async id=>{
    const control=path.join(directory,'age-challenge.json');await fs.writeFile(control,JSON.stringify({id}));
    for(let i=0;i<100;i++){try{await fs.stat(control)}catch(error){if(error.code==='ENOENT')return;throw error}await new Promise(resolve=>setTimeout(resolve,30))}
    throw new Error('Fixture did not advance the resend cooldown');
  };
  const register=async username=>{
    const context=await browser.newContext({viewport:{width:1440,height:1000}});contexts.push(context);const member=await context.newPage();
    await member.goto(base);await member.locator('#openRegistration').waitFor({state:'visible'});await member.click('#openRegistration');
    await member.fill('#registrationUsername',username);await member.fill('#registrationPassword','email browser personal password');await member.fill('#registrationConfirm','email browser personal password');await member.click('#registerAccount');
    await member.locator('#loginScreen').waitFor({state:'visible'});assert.equal(await member.inputValue('#username'),username);assert.equal(await member.inputValue('#password'),'');
    await member.fill('#password','email browser personal password');await member.click('#loginButton');await member.waitForFunction(()=>state.user&&document.querySelector('#status').textContent==='已登录');return member;
  };
  const verify=async(member,address)=>{
    await member.evaluate(()=>location.hash='#/account/profile');await member.locator('#accountEmail').waitFor({state:'visible'});
    await member.fill('#accountEmail',address);await member.click('#sendEmailCode');await member.waitForFunction(()=>Boolean(emailView?.challenge_id));
    const challenge=await member.evaluate(()=>emailView.challenge_id),mail=await delivery(address);
    await member.fill('#emailCode',mail.code==='000000'?'999999':'000000');await member.click('#verifyEmailCode');await member.waitForFunction(()=>document.querySelector('#emailProfileError').textContent.includes('400'));
    await member.fill('#emailCode',mail.code);await member.click('#verifyEmailCode');await member.waitForFunction(()=>state.user.email_verified&&state.user.upload_allowed);
    await member.evaluate(async({challenge,code})=>{const repeated=await api('auth/email/verify','POST',{challenge_id:challenge,code});if(!repeated.email_verified)throw new Error('Repeated verification was not idempotent')},{challenge,code:mail.code});
  };
  try{
    assert.equal(await page.evaluate(()=>state.user.email_verification_required),true,'Existing administrator also requires email verification');
    const denied=await page.evaluate(async()=>{try{await api('uploads/resumable','POST',{name:'blocked.wav',size:844});return 0}catch(error){return error.status}});assert.equal(denied,403,'Unverified administrator cannot bypass the upload gate');
    const administratorID=await page.evaluate(()=>state.user.id);
    await page.goto(base+'#/admin/users/'+administratorID);await page.locator('#managedRequireEmail').waitFor({state:'visible'});
    await page.uncheck('#managedRequireEmail');await page.waitForFunction(id=>state.users.find(user=>user.id===id)?.email_verification_required===false,administratorID);
    assert.equal(await page.evaluate(()=>state.user.upload_allowed),true,'Exempting the current administrator immediately updates their own upload state');
    await page.check('#managedRequireEmail');await page.waitForFunction(id=>state.users.find(user=>user.id===id)?.email_verification_required===true,administratorID);
    assert.equal(await page.evaluate(()=>state.user.upload_allowed),false,'Requiring verification again immediately restricts the current administrator');
    await page.evaluate(()=>location.hash='#/admin/settings/registration');await page.waitForFunction(()=>registrationSettings!==null);await page.fill('#registrationQuotaValue','1');await page.selectOption('#registrationQuotaUnit','MiB');await page.check('#registrationEnabled');await page.click('#saveRegistrationSettings');
    await page.waitForFunction(()=>document.querySelector('#registrationSettingsError').textContent.includes('409'));
    page.once('dialog',dialog=>dialog.accept());await page.click('#reloadRegistrationSettings');await page.waitForFunction(()=>!registrationDirty);
    await page.evaluate(()=>location.hash='#/admin/settings/mail');await page.waitForFunction(()=>mailSettings!==null);
    await page.fill('#mailHost','smtp.fixture.invalid');await page.fill('#mailFrom','sender@example.invalid');await page.fill('#mailUsername','fixture');await page.fill('#mailPassword','fixture SMTP secret');await page.click('#saveMailSettings');
    await page.waitForFunction(()=>mailSettings?.configured&&!mailSaving);assert.equal(await page.inputValue('#mailPassword'),'');assert.equal(await page.locator('#mailSettingsState').isVisible(),true);assert.equal(await page.locator('#mailCredentialState').isVisible(),true);
    await page.fill('#mailTestAddress','transport-test@example.invalid');await page.click('#sendMailTest');await page.waitForFunction(()=>mailSettings?.tested===true);
    await page.evaluate(()=>location.hash='#/admin/settings/registration');await page.waitForFunction(()=>registrationSettings?.mail_ready===true);await page.fill('#registrationQuotaValue','1');await page.selectOption('#registrationQuotaUnit','MiB');await page.check('#registrationEnabled');await page.click('#saveRegistrationSettings');await page.waitForFunction(()=>registrationSettings?.enabled&&!registrationSaving);

    const early=await register('early-member');assert.equal(await early.evaluate(()=>state.user.must_change_password),false);assert.equal(await early.evaluate(()=>state.user.upload_allowed),false);
    assert.equal(await early.evaluate(async()=>(await api('auth/storage')).limit_bytes),1048576);
    await page.fill('#registrationQuotaValue','2');await page.click('#saveRegistrationSettings');await page.waitForFunction(()=>registrationSettings?.default_quota_bytes===2097152&&!registrationSaving);
    const late=await register('late-member');assert.equal(await late.evaluate(async()=>(await api('auth/storage')).limit_bytes),2097152);
    await verify(early,'early@example.invalid');assert.equal(await early.evaluate(async()=>(await api('auth/storage')).limit_bytes),1048576,'Delayed verification retains the registration-time quota snapshot');
    await verify(late,'late@example.invalid');assert.equal(await late.evaluate(async()=>(await api('auth/storage')).limit_bytes),2097152);

    await early.evaluate(()=>location.hash='#/uploads');await early.waitForFunction(()=>state.page==='upload');const audio=await fs.readFile(path.join(directory,'media','First.wav'));audio[44]=7;
    await early.setInputFiles('#uploadInput',{name:'email-upload.wav',mimeType:'audio/wav',buffer:audio});await early.locator('[data-stage-upload]').click();await early.waitForFunction(()=>state.uploads[0]?.stageId);await early.locator('[data-confirm-upload]').click();await early.waitForFunction(()=>['完成','已去重'].includes(state.uploads[0]?.status));
    assert.equal(await early.evaluate(async()=>(await api('auth/storage')).used_bytes),audio.length,'Verified member uses the assigned audio quota');
    await ageChallenge(await early.evaluate(()=>state.user.id));
    await early.evaluate(()=>location.hash='#/account/profile');await early.locator('#accountEmail').waitFor({state:'visible'});await early.fill('#accountEmail','changed@example.invalid');await early.click('#sendEmailCode');await early.waitForFunction(()=>document.querySelector('#emailProfileError').textContent.includes('400'));
    assert.equal(await early.evaluate(()=>state.user.email),'early@example.invalid','Changing email without the current password preserves the old binding');
    await early.fill('#emailCurrentPassword','email browser personal password');await early.click('#sendEmailCode');await early.waitForFunction(()=>emailView?.pending_email==='changed@example.invalid');
    assert.equal(await early.evaluate(()=>state.user.email),'early@example.invalid');const changed=await delivery('changed@example.invalid');await early.fill('#emailCode',changed.code);await early.click('#verifyEmailCode');await early.waitForFunction(()=>state.user.email==='changed@example.invalid');
    assert.equal(await early.evaluate(async()=>(await api('auth/storage')).limit_bytes),1048576,'Changing email never grants the new default quota');
    await early.setViewportSize({width:390,height:844});await early.waitForFunction(()=>document.querySelector('.main').getBoundingClientRect().left<=1&&document.querySelector('#sidebar').getBoundingClientRect().right<=1&&document.querySelector('#emailBindingStatus').textContent.includes('邮箱已验证'));await early.evaluate(()=>document.querySelector('#notifications')?.replaceChildren());await early.screenshot({path:path.join(directory,'real-email-profile-mobile.png'),fullPage:true});assert.equal(await early.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
    // Old-account verification preserves the existing unlimited quota and unlocks uploads.
    await verify(page,'administrator@example.invalid');assert.equal(await page.evaluate(async()=>(await api('auth/storage')).limit_bytes),0);
    await page.evaluate(async()=>{const task=await api('uploads/resumable','POST',{name:'allowed.wav',size:844});await api('uploads/resumable/'+task.id,'DELETE')});
    await waitForAsync(page,async()=>(await api('admin/mail')).tested===true);
    console.log('Real email browser flow passed: old-admin upload gate/config access, SMTP queue test, real self-registration, per-registration quota snapshots, durable code delivery, invalid/idempotent validation, verified upload, password-confirmed email change and mobile layout.');
  }finally{for(const context of contexts)await context.close()}
}

async function main(){
  const fs=require('node:fs/promises'),os=require('node:os'),{spawn}=require('node:child_process');
  const {chromium}=require(process.env.MUSIC_SERVER_PLAYWRIGHT||'playwright');
  assert.ok(process.env.MUSIC_SERVER_TEST_EMAIL_BINARY,'Set MUSIC_SERVER_TEST_EMAIL_BINARY to a compiled Go test binary');
  const directory=await fs.mkdtemp(path.join(os.tmpdir(),'music-email-ui-'));await fs.mkdir(path.join(directory,'media'));
  const audio=Buffer.alloc(844);audio.write('RIFF');audio.writeUInt32LE(836,4);audio.write('WAVEfmt ',8);audio.writeUInt32LE(16,16);audio.writeUInt16LE(1,20);audio.writeUInt16LE(1,22);audio.writeUInt32LE(8000,24);audio.writeUInt32LE(16000,28);audio.writeUInt16LE(2,32);audio.writeUInt16LE(16,34);audio.write('data',36);audio.writeUInt32LE(800,40);await fs.writeFile(path.join(directory,'media','First.wav'),audio);
  const server=spawn(process.env.MUSIC_SERVER_TEST_EMAIL_BINARY,['-test.run','^TestEmailBrowserFixture$','-test.timeout','180s'],{windowsHide:true,env:{...process.env,MUSIC_SERVER_EMAIL_BROWSER_FIXTURE_DIR:directory}});let logs='',browser;
  server.stdout.on('data',data=>logs+=data);server.stderr.on('data',data=>logs+=data);
  try{
    let metadata;for(let i=0;i<150;i++){try{metadata=JSON.parse(await fs.readFile(path.join(directory,'server.json'),'utf8'));break}catch{}if(server.exitCode!==null)throw new Error('Email fixture exited: '+logs);await new Promise(resolve=>setTimeout(resolve,100))}assert.ok(metadata?.url,'Email fixture did not start');
    browser=await chromium.launch({headless:true,...(process.env.MUSIC_SERVER_BROWSER?{executablePath:process.env.MUSIC_SERVER_BROWSER}:{})});const page=await browser.newPage({viewport:{width:1440,height:1100}}),errors=[];page.on('pageerror',error=>errors.push(error.message));
    await page.goto(metadata.url);await page.fill('#username','admin');await page.fill('#password','email fixture administrator password');await page.click('#loginButton');await page.waitForFunction(()=>state.user?.username==='admin'&&document.querySelector('#status').textContent==='已登录');
    await realBrowserFlow({browser,page,base:metadata.url,directory,fs});await module.exports({page,browser,base:metadata.url,directory});assert.deepEqual(errors,[]);console.log('Artifacts: '+directory);
  }catch(error){console.error('Email browser artifacts: '+directory);console.error(logs);throw error}
  finally{await browser?.close();await fs.writeFile(path.join(directory,'stop'),'stop');for(let i=0;i<30&&server.exitCode===null;i++)await new Promise(resolve=>setTimeout(resolve,100));if(server.exitCode===null)server.kill();if(server.exitCode&&server.exitCode!==0)console.error(logs)}
}
if(require.main===module)main().catch(error=>{console.error(error);process.exitCode=1});
