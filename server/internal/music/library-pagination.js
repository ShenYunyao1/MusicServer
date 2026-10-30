'use strict';

const libraryPagination={serial:0,busy:false,target:null,owner:null};
function libraryPaginationOwner(){if(libraryPagination.owner!==state.user?.id){libraryPagination.activeRequest?.abort();libraryPagination.owner=state.user?.id;libraryPagination.serial++;libraryPagination.busy=false;libraryPagination.target=null}}
function libraryPaginationReadStarted(){libraryPagination.activeRequest?.abort();if(libraryPagination.busy&&!libraryPagination.requesting){libraryPagination.serial++;libraryPagination.busy=false;libraryPagination.target=null}}
async function libraryPageRequest(path,method='GET',body){
 const owner=state.user?.id,epoch=browseState.epoch,controller=new AbortController();libraryPagination.activeRequest?.abort();libraryPagination.activeRequest=controller;
 const current=()=>owner===state.user?.id&&epoch===browseState.epoch&&!controller.signal.aborted&&libraryPagination.activeRequest===controller;
 try{const response=await fetch('/api/v2/'+path,{method,credentials:'same-origin',cache:'no-store',signal:controller.signal,headers:{'X-CSRF-Token':state.csrf,...(body===undefined?{}:{'Content-Type':'application/json'})},body:body===undefined?undefined:JSON.stringify(body)});const raw=await response.text();if(!current())throw new Error('账户已变化');let payload;try{payload=raw?JSON.parse(raw):null}catch(_){throw new Error('曲库分页响应无效')}if(!response.ok){if(response.status===401&&current())setLoggedOut();throw Object.assign(new Error(payload?.error?.message||payload?.error||'曲库读取失败'),{status:response.status,code:payload?.error?.code||''})}return payload}
 finally{if(libraryPagination.activeRequest===controller)libraryPagination.activeRequest=null}
}
function libraryPageSize(){
 const explicit=Number(browseRouteQuery().get('page_size'));if(location.hash.split('?')[0]==='#/library'&&[20,30,50].includes(explicit))return explicit;
 try{const saved=Number(localStorage.getItem('library-page-size/'+state.user?.id));if([20,30,50].includes(saved))return saved}catch(_){}
 return 50;
}
function libraryReadRouteOffset(){
 const values=browseRouteQuery(),page=Number(values.get('page'));
 if(Number.isSafeInteger(page)&&page>=1)return (Math.min(page,1000000)-1)*libraryPageSize();
 const legacy=Number(values.get('offset')||0),offset=Number.isSafeInteger(legacy)&&legacy>=0?legacy:0;return Math.min(999999,Math.floor(offset/libraryPageSize()))*libraryPageSize();
}
function libraryWriteRouteValues(values,offset,size=libraryPageSize()){
 values.delete('offset');values.delete('page');values.set('page_size',String(size));const page=Math.floor(offset/size)+1;if(page>1)values.set('page',String(page));
}
function libraryCanonicalPage(offset,size){
 if(location.hash.split('?')[0]!=='#/library'||state.page!=='tracks')return;
 const values=browseRouteQuery();libraryWriteRouteValues(values,offset,size);history.replaceState(null,'','#/library?'+values);
}
function libraryPageNumbers(current,total){const values=new Set([1,total,current-1,current,current+1]);if(current<=3)for(let i=1;i<=Math.min(5,total);i++)values.add(i);if(current>=total-2)for(let i=Math.max(1,total-4);i<=total;i++)values.add(i);return [...values].filter(n=>n>=1&&n<=total).sort((a,b)=>a-b)}

async function libraryNavigate(page,size=libraryPageSize(),fromBottom=false){
 libraryPaginationOwner();
 if(libraryPagination.busy||!state.user)return;
 const previous=browseLibraryPage(),totalPages=Math.max(1,Math.ceil((previous?.total||0)/size));page=Math.max(1,Math.min(totalPages,Number(page)||1));
 const owner=state.user.id,epoch=browseState.epoch,serial=++libraryPagination.serial,values=browseRouteQuery();
 libraryWriteRouteValues(values,(page-1)*size,size);const route='#/library?'+values;
 if(location.hash!==route)history.pushState(null,'',route);
 libraryPagination.busy=true;libraryPagination.target={page,size};
 state.selectedTrackIds.clear();document.querySelectorAll('#trackRows [data-track-id]').forEach(input=>input.checked=false);$('selectAll').checked=false;updateTrackSelection();
 renderLibraryPagination(previous);
 let generation;
 const sameAccount=()=>serial===libraryPagination.serial&&owner===state.user?.id&&epoch===browseState.epoch&&location.hash.split('?')[0]==='#/library';
 const current=()=>sameAccount()&&(generation===undefined||generation===browseState.generations.tracks);
 try{let pending;libraryPagination.requesting=true;try{pending=loadBrowseTracks((page-1)*size)}finally{libraryPagination.requesting=false}generation=browseState.generations.tracks;const result=await pending;if(!current()||!result)return;renderTracks();if(fromBottom)$('libraryPagerTop')?.scrollIntoView({block:'start',behavior:'instant'})}
  catch(error){if(error.name!=='AbortError'&&current()){browseState.tracks={...previous,items:[],total:previous?.total||0,offset:(page-1)*size,limit:size,error:error.message};browseCacheTracks();renderTracks();setStatus('曲库分页读取失败：'+error.message+'；可重试当前页。','error')}}
 finally{if(serial===libraryPagination.serial){libraryPagination.busy=false;libraryPagination.target=null;if(sameAccount()){const actual=browseLibraryPage();if(actual)renderLibraryPagination(actual)}}}
}
function renderLibraryPagination(page){
 libraryPaginationOwner();
 if(!page)return;
 const panel=$('trackRows').closest('.panel');
 for(const[id,bottom]of [['libraryPagerTop',false],['browseTrackPager',true]]){
  let nav=$(id);if(!nav){nav=document.createElement('nav');nav.id=id;if(bottom)panel.after(nav);else panel.prepend(nav)}
  nav.className='library-pager';nav.hidden=false;nav.setAttribute('aria-label',bottom?'曲库底部分页':'曲库顶部分页');nav.setAttribute('aria-busy',String(libraryPagination.busy));nav.replaceChildren();
  const size=libraryPagination.target?.size||page.limit||libraryPageSize(),totalPages=Math.max(1,Math.ceil(page.total/size)),number=libraryPagination.target?.page||Math.floor(page.offset/size)+1;
  const information=document.createElement('div');information.className='library-page-information';const total=document.createElement('span');total.textContent='共 '+page.total+' 首';information.append(total);
  const label=document.createElement('label');label.append(document.createTextNode('每页 '));const select=document.createElement('select');select.dataset.pageSize='';select.setAttribute('aria-label','每页曲目数量');for(const value of [20,30,50])select.add(new Option(value+' 首',String(value)));select.value=String(size);select.onchange=()=>{const chosen=Number(select.value);try{localStorage.setItem('library-page-size/'+state.user.id,String(chosen))}catch(_){}run(()=>libraryNavigate(1,chosen,bottom))};label.append(select);information.append(label);nav.append(information);
  const controls=document.createElement('div');controls.className='library-page-controls';
  const button=(text,action)=>{const b=makeButton(text,action,'btn small');b.type='button';return b};
  const previous=button('上一页',()=>run(()=>libraryNavigate(number-1,size,bottom)));previous.dataset.pageAction='previous';previous.disabled=number<=1||!page.total;controls.append(previous);
  const numbers=document.createElement('div');numbers.className='library-page-numbers';let last=0;
  if(page.total)for(const n of libraryPageNumbers(number,totalPages)){if(last&&n-last>1){const ellipsis=document.createElement('span');ellipsis.textContent='…';numbers.append(ellipsis)}const b=button(String(n),()=>run(()=>libraryNavigate(n,size,bottom)));b.dataset.pageNumber=String(n);b.setAttribute('aria-label','第 '+n+' 页');if(n===number)b.setAttribute('aria-current','page');numbers.append(b);last=n}controls.append(numbers);
  const jump=document.createElement('label');jump.className='library-page-jump';jump.append(document.createTextNode(page.total?'第 ':'暂无曲目'));const input=document.createElement('input');input.type='number';input.min='1';input.max=String(totalPages);input.value=String(number);input.dataset.pageJump='';input.setAttribute('aria-label','跳转页码');input.disabled=!page.total;input.onkeydown=e=>{if(e.key==='Enter'){e.preventDefault();if(input.checkValidity())run(()=>libraryNavigate(Number(input.value),size,bottom));else input.reportValidity()}};if(page.total){jump.append(input,document.createTextNode(' / '+totalPages+' 页'))}else{input.hidden=true;jump.append(input)}controls.append(jump);
  const next=button('下一页',()=>run(()=>libraryNavigate(number+1,size,bottom)));next.dataset.pageAction='next';next.disabled=number>=totalPages||!page.total;controls.append(next);nav.append(controls);
  if(page.error)controls.append(button('重试本页',()=>run(()=>libraryNavigate(number,size,bottom))));
  if(libraryPagination.busy)nav.querySelectorAll('button,select,input').forEach(control=>control.disabled=true);
 }
}
window.addEventListener('hashchange',()=>{libraryPagination.activeRequest?.abort();libraryPagination.serial++;libraryPagination.busy=false;libraryPagination.target=null;state.selectedTrackIds.clear()});
