'use strict';

const avatarWorkspace={owner:null,epoch:0,decode:0,version:1,metadata:null,draft:null,busy:false,loading:false,decoding:false,image:null,zoom:1,x:0,y:0};
function avatarCurrent(owner,epoch){return state.user?.id===owner&&avatarWorkspace.owner===owner&&avatarWorkspace.epoch===epoch}
function avatarDirty(){return Boolean(avatarWorkspace.draft||avatarWorkspace.busy||avatarWorkspace.decoding)}
function avatarMessage(text='',error=false){const node=$('avatarMessage');if(node){node.textContent=text;node.classList.toggle('error',error)}}
function discardAvatarDraft(){avatarWorkspace.decode++;avatarWorkspace.decoding=false;avatarWorkspace.draft=null;avatarWorkspace.image=null;avatarWorkspace.zoom=1;avatarWorkspace.x=avatarWorkspace.y=0;const input=$('avatarInput');if(input)input.value='';updateAvatarControls()}
function clearAvatarWorkspace(){avatarWorkspace.epoch++;Object.assign(avatarWorkspace,{owner:null,version:1,metadata:null,busy:false,loading:false});discardAvatarDraft();avatarMessage();renderOwnAvatar()}
function renderOwnAvatar(){
 const user=state.user,meta=avatarWorkspace.owner===user?.id?avatarWorkspace.metadata:null;
 let header=$('accountAvatar');if(!header&&$('accountName')){header=document.createElement('span');header.id='accountAvatar';header.className='account-avatar';header.setAttribute('aria-hidden','true');$('accountName').before(header)}
 for(const node of [header,$('profileAvatar')]){if(!node)continue;node.replaceChildren();node.hidden=!user;if(!user)continue;
  const fallback=Array.from((user.display_name||user.username||'♪').trim())[0]||'♪';node.textContent=fallback;
  if(meta?.has_avatar&&typeof meta.url==='string'&&/^\/api\/v2\/auth\/avatar\/content(?:\?|$)/.test(meta.url)){
   const img=document.createElement('img');img.alt='';img.src=meta.url;img.onload=()=>{if(img.isConnected)node.firstChild?.nodeType===Node.TEXT_NODE&&node.firstChild.remove()};img.onerror=()=>img.remove();node.append(img);
  }
 }
}
function updateAvatarControls(){
 const a=avatarWorkspace;if(!$('avatarInput'))return;
 for(const id of ['avatarInput','resetAvatar','avatarZoom'])$(id).disabled=a.busy||a.loading;
 $('chooseAvatar').disabled=a.busy||a.loading||a.decoding;
 if(typeof updateProfileSaveState==='function')updateProfileSaveState();
 $('avatarCrop').hidden=a.draft!=='image';$('avatarZoom').value=String(a.zoom);
 $('avatarDefaultPreview').hidden=a.draft!=='remove';$('avatarDefaultPreview').textContent=Array.from(($('profileName')?.value||state.user?.display_name||state.user?.username||'♪').trim())[0]||'♪';
 $('avatarPreviewNote').textContent=a.draft==='remove'?'预览：保存资料后恢复昵称首字头像。':a.draft==='image'?'预览中，保存资料后统一应用。':'JPEG / PNG，最多 8 MiB、20MP；保存为 256×256 静态头像。';
}
async function loadOwnAvatar(discard=false){
 if(typeof personalEdit!=='undefined'&&personalEdit?.type==='profile'&&!discard)return;
 if(!state.user)return;const a=avatarWorkspace,owner=state.user.id;
 if(a.owner!==owner){clearAvatarWorkspace();a.owner=owner}
 if((a.draft||a.decoding)&&!discard)return;if(discard)discardAvatarDraft();
 const epoch=++a.epoch;a.loading=true;renderOwnAvatar();updateAvatarControls();
 try{const metadata=await api('auth/avatar');if(!avatarCurrent(owner,epoch))return;a.metadata=metadata;a.version=metadata.version;renderOwnAvatar();avatarMessage()}
 catch(error){if(avatarCurrent(owner,epoch))avatarMessage('头像读取失败：'+error.message,true)}
 finally{if(avatarCurrent(owner,epoch)){a.loading=false;updateAvatarControls()}}
}
function drawAvatarCrop(){
 const a=avatarWorkspace;if(!a.image)return;const image=a.image,scale=256/Math.min(image.naturalWidth,image.naturalHeight)*a.zoom;
 const width=image.naturalWidth*scale,height=image.naturalHeight*scale;
 a.x=Math.max(-(width-256)/2,Math.min((width-256)/2,a.x));a.y=Math.max(-(height-256)/2,Math.min((height-256)/2,a.y));
 const context=$('avatarCanvas').getContext('2d');context.clearRect(0,0,256,256);context.drawImage(image,(256-width)/2+a.x,(256-height)/2+a.y,width,height);
}
async function selectAvatarFile(file){
 const a=avatarWorkspace;if(!file||a.busy||a.loading)return;
 const owner=state.user?.id,epoch=a.epoch,decode=++a.decode;
 const valid=()=>avatarCurrent(owner,epoch)&&decode===a.decode;a.decoding=true;updateAvatarControls();
 try{
  if(file.size>8*1024*1024)throw new Error('图片不能超过 8 MiB。');
  const bytes=new Uint8Array(await file.arrayBuffer());
  const png=[137,80,78,71,13,10,26,10].every((b,i)=>bytes[i]===b),jpeg=bytes[0]===255&&bytes[1]===216&&bytes[2]===255;
  if(!png&&!jpeg||!['image/png','image/jpeg',''].includes(file.type))throw new Error('仅支持 JPEG 和 PNG 图片。');
  const dimensions=avatarImageDimensions(bytes,png);
  if(!dimensions||!dimensions[0]||!dimensions[1]||dimensions[0]*dimensions[1]>20000000)throw new Error('图片尺寸无效或超过 20MP。');
  if(!valid())return;
  const url=URL.createObjectURL(file);let image;
  try{image=await new Promise((resolve,reject)=>{const img=new Image();img.onload=()=>resolve(img);img.onerror=()=>reject(new Error('图片无法解码。'));img.src=url})}finally{URL.revokeObjectURL(url)}
  if(!valid())return;
  if(!image.naturalWidth||image.naturalWidth*image.naturalHeight>20000000)throw new Error('图片不能超过 20MP。');
  a.image=image;a.draft='image';a.zoom=1;a.x=a.y=0;drawAvatarCrop();avatarMessage();updateAvatarControls();
 }catch(error){if(valid())avatarMessage(error.message,true)}finally{if(valid()){a.decoding=false;updateAvatarControls()}}
}
// Read bounded source headers before asking the browser to allocate decoded pixels.
function avatarImageDimensions(bytes,png){
 if(png){
  if(bytes.length<33||String.fromCharCode(...bytes.slice(12,16))!=='IHDR')return null;
  const view=new DataView(bytes.buffer,bytes.byteOffset,bytes.byteLength);let offset=8;
  while(offset+12<=bytes.length){const length=view.getUint32(offset),type=String.fromCharCode(...bytes.slice(offset+4,offset+8));if(length>bytes.length-offset-12)return null;if(type==='acTL')throw new Error('不支持动画 PNG，请选择静态图片。');offset+=length+12;if(type==='IEND')break}
  return [view.getUint32(16),view.getUint32(20)];
 }
 let offset=2;
 while(offset+3<bytes.length){if(bytes[offset++]!==255)return null;while(bytes[offset]===255)offset++;const marker=bytes[offset++];if(marker===217||marker===218)return null;if(marker===1||marker>=208&&marker<=215)continue;if(offset+2>bytes.length)return null;const length=bytes[offset]*256+bytes[offset+1];if(length<2||offset+length>bytes.length)return null;
  if(marker>=192&&marker<=207&&![196,200,204].includes(marker)){if(length<8)return null;return [bytes[offset+5]*256+bytes[offset+6],bytes[offset+3]*256+bytes[offset+4]]}offset+=length;
 }
 return null;
}
async function appendAvatarProfileDraft(form){
 const a=avatarWorkspace;if(!a.draft)return;form.set('avatar_version',String(a.version));
 if(a.draft==='remove'){form.set('remove_avatar','true');return}
 const blob=await new Promise(resolve=>$('avatarCanvas').toBlob(resolve,'image/png'));if(!blob||blob.size>512*1024)throw new Error('头像生成失败或过大，请重新选择。');form.set('avatar',blob,'avatar.png');
}
async function saveOwnAvatar(){return savePersonalProfile({preventDefault(){}})}
function setupAvatarProfile(){
 if($('avatarEditor'))return;
 const box=document.createElement('section');box.id='avatarEditor';box.className='avatar-editor';box.setAttribute('aria-label','个人头像');
 box.innerHTML='<div class="actions"><button id="chooseAvatar" class="btn" type="button">选择头像</button><input id="avatarInput" type="file" accept="image/jpeg,image/png" hidden><button id="resetAvatar" class="btn" type="button">恢复默认</button></div><p id="avatarPreviewNote" class="status"></p><span id="avatarDefaultPreview" class="profile-avatar avatar-default-preview" role="img" aria-label="默认头像预览" hidden></span><div id="avatarCrop" hidden><canvas id="avatarCanvas" width="256" height="256" tabindex="0" role="img" aria-label="头像正方形裁剪预览，拖动或方向键调整位置"></canvas><label for="avatarZoom">缩放</label><input id="avatarZoom" type="range" min="1" max="4" step="0.01" value="1"></div><p id="avatarMessage" class="avatar-message" role="status" aria-live="polite"></p>';
 $('profileForm').before(box);$('avatarInput').onchange=()=>selectAvatarFile($('avatarInput').files[0]);
 $('chooseAvatar').onclick=()=>{if(!$('chooseAvatar').disabled)$('avatarInput').click()};
 $('resetAvatar').onclick=()=>{if(avatarWorkspace.busy||avatarWorkspace.loading)return;discardAvatarDraft();avatarWorkspace.draft='remove';avatarMessage();updateAvatarControls()};
 $('avatarZoom').oninput=()=>{avatarWorkspace.zoom=Number($('avatarZoom').value);drawAvatarCrop()};
 const canvas=$('avatarCanvas');let drag=null;
 canvas.onpointerdown=event=>{if(event.button!==0||avatarWorkspace.busy)return;drag={id:event.pointerId,x:event.clientX,y:event.clientY};canvas.setPointerCapture(event.pointerId)};
 canvas.onpointermove=event=>{if(!drag||drag.id!==event.pointerId||avatarWorkspace.busy)return;const scale=256/canvas.getBoundingClientRect().width;avatarWorkspace.x+=(event.clientX-drag.x)*scale;avatarWorkspace.y+=(event.clientY-drag.y)*scale;drag.x=event.clientX;drag.y=event.clientY;drawAvatarCrop()};
 canvas.onpointerup=canvas.onpointercancel=()=>{drag=null};
 canvas.onkeydown=event=>{if(avatarWorkspace.busy)return;const delta={ArrowLeft:[-4,0],ArrowRight:[4,0],ArrowUp:[0,-4],ArrowDown:[0,4]}[event.key];if(delta){event.preventDefault();avatarWorkspace.x+=delta[0];avatarWorkspace.y+=delta[1];drawAvatarCrop()}};
 updateAvatarControls();renderOwnAvatar();
}
