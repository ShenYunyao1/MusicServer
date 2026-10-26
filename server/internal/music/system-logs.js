'use strict';

const systemLogs={entries:[],newest:'',oldest:'',more:false,busy:false,paused:false,generation:0};
const logPage=document.createElement('section');logPage.id='page-logs';logPage.className='page';
logPage.innerHTML='<div class="page-intro"><div><h1>系统日志</h1><p>查看服务器活动、上传分段耗时及请求异常。</p></div><div class="actions"><button id="refreshSystemLogs" class="btn">刷新</button><button id="pauseSystemLogs" class="btn">暂停更新</button></div></div><div class="panel"><div class="log-filters"><label>级别<select id="logLevel"><option value="">全部级别</option><option value="info">信息</option><option value="warn">警告</option><option value="error">错误</option></select></label><label>类别<select id="logCategory"><option value="">全部类别</option><option value="upload">上传</option><option value="http">请求</option><option value="server">服务</option><option value="scan">扫描</option><option value="update">更新</option><option value="mail">邮件</option></select></label><label class="log-search">搜索<input id="logSearch" maxlength="200" placeholder="事件、请求编号或账户 ID"></label><button id="searchSystemLogs" class="btn">筛选</button></div><p id="systemLogsStatus" class="status" role="status"></p><div id="systemLogRows" class="system-log-list"></div><button id="olderSystemLogs" class="btn" hidden>加载更早记录</button></div>';
document.querySelector('.content').append(logPage);
function clearSystemLogs(){systemLogs.generation++;systemLogs.entries=[];systemLogs.newest=systemLogs.oldest='';systemLogs.more=false;systemLogs.busy=false;$('systemLogRows').replaceChildren();$('systemLogsStatus').textContent='';$('logSearch').value='';$('logLevel').value='';$('logCategory').value='';systemLogs.paused=false;$('pauseSystemLogs').textContent='暂停更新'}
function renderSystemLogs(){
 const rows=$('systemLogRows');rows.replaceChildren();
 const names={server:'服务',http:'请求',upload:'上传',scan:'扫描',update:'更新',mail:'邮件'},levels={info:'信息',warn:'警告',error:'错误'};
 const times={receive_ms:'接收',wait_ms:'排队',write_ms:'写入',probe_ms:'音频探测',metadata_ms:'标签与哈希',persist_ms:'保存',total_ms:'总计',validation_ms:'校验'};
 if(!systemLogs.entries.length){const empty=document.createElement('p');empty.className='empty';empty.textContent='暂无符合条件的日志';rows.append(empty)}
 for(const entry of systemLogs.entries){
  const row=document.createElement('article');row.className='system-log-entry';
  const head=document.createElement('div');head.className='system-log-head';
  const time=document.createElement('time');time.dateTime=entry.time;time.textContent=new Date(entry.time).toLocaleString();
  const level=document.createElement('span');level.className='badge '+(entry.level==='error'?'error':entry.level==='warn'?'warn':'');level.textContent=levels[entry.level]||entry.level;
  const category=document.createElement('span');category.textContent=names[entry.category]||entry.category;head.append(time,level,category);
  const message=document.createElement('strong');message.textContent=entry.message;row.append(head,message);
  const detail=document.createElement('p');detail.className='system-log-detail';
  detail.textContent=[entry.method,entry.route,entry.status?'HTTP '+entry.status:'',entry.reason?'原因 '+entry.reason:'',entry.duration_ms?entry.duration_ms+' ms':'',entry.bytes?formatBytes(entry.bytes):'',entry.request_id?'请求 '+entry.request_id:'',entry.user_id?'账户 '+entry.user_id:''].filter(Boolean).join(' · ');row.append(detail);
  if(entry.timings&&Object.keys(entry.timings).length){const timing=document.createElement('p');timing.className='system-log-timings';timing.textContent=Object.entries(entry.timings).map(([key,value])=>(times[key]||key)+' '+value+' ms').join(' · ');row.append(timing)}
  rows.append(row);
 }
 $('olderSystemLogs').hidden=!systemLogs.more;
}
async function loadSystemLogs(mode='reset'){
 if(!isAdministrator()||state.page!=='logs'||systemLogs.busy&&mode!=='reset')return;
 if(mode==='reset'){systemLogs.generation++;systemLogs.newest=systemLogs.oldest='';systemLogs.entries=[]}
 const generation=systemLogs.generation;systemLogs.busy=true;
 const query=new URLSearchParams({limit:'100'});for(const [name,id]of [['level','logLevel'],['category','logCategory'],['q','logSearch']])if($(id).value.trim())query.set(name,$(id).value.trim());
 if(mode==='new'&&systemLogs.newest)query.set('after',systemLogs.newest);if(mode==='older'&&systemLogs.oldest)query.set('before',systemLogs.oldest);
 try{
  const result=await api('admin/logs?'+query);
  if(generation!==systemLogs.generation||!state.user||state.page!=='logs')return;
  const entries=mode==='older'?[...systemLogs.entries,...result.entries]:[...result.entries,...systemLogs.entries],seen=new Set();systemLogs.entries=entries.filter(e=>{if(seen.has(e.id))return false;seen.add(e.id);return true}).slice(0,500);
  systemLogs.newest=systemLogs.entries[0]?.id||systemLogs.newest;systemLogs.oldest=systemLogs.entries.at(-1)?.id||'';
  if(mode!=='new')systemLogs.more=result.has_more&&systemLogs.entries.length<500;
  $('systemLogsStatus').textContent=(systemLogs.paused?'已暂停 · ':'每 5 秒更新 · ')+systemLogs.entries.length+' 条 · '+result.retention+(result.truncated?' · 较早记录已轮转':'')+(result.warning?' · '+result.warning:'');renderSystemLogs();
 }catch(error){if(generation===systemLogs.generation)$('systemLogsStatus').textContent=error.message}finally{if(generation===systemLogs.generation)systemLogs.busy=false}
}
$('refreshSystemLogs').onclick=()=>loadSystemLogs();$('searchSystemLogs').onclick=()=>loadSystemLogs();
for(const id of ['logLevel','logCategory'])$(id).onchange=()=>loadSystemLogs();
$('logSearch').onkeydown=e=>{if(e.key==='Enter')loadSystemLogs()};
$('olderSystemLogs').onclick=()=>loadSystemLogs('older');
$('pauseSystemLogs').onclick=()=>{systemLogs.paused=!systemLogs.paused;$('pauseSystemLogs').textContent=systemLogs.paused?'恢复更新':'暂停更新';$('systemLogsStatus').textContent=systemLogs.paused?'已暂停自动更新':$('systemLogsStatus').textContent.replace('已暂停','每 5 秒更新');if(!systemLogs.paused)loadSystemLogs('new')};
setInterval(()=>{if(state.user&&isAdministrator()&&state.page==='logs'&&!document.hidden&&!systemLogs.paused)loadSystemLogs('new')},5000);
