'use strict';
const assert=require('node:assert/strict'),fs=require('node:fs/promises'),http=require('node:http'),path=require('node:path');
const {chromium}=require(process.env.MUSIC_SERVER_PLAYWRIGHT||'playwright');
async function main(){
 const script=await fs.readFile(path.join(__dirname,'../internal/music/selection-ui.js'),'utf8');
 const server=http.createServer((req,res)=>{res.setHeader('Content-Type','text/html; charset=utf-8');res.end('<!doctype html><style>body{margin:0}.key-grant-grid{width:320px}label{display:flex;align-items:center;height:36px;width:300px}input{margin:0 12px;width:18px;height:18px}</style><div class="key-grant-grid" id="group">'+Array.from({length:10},(_,i)=>'<label><input type="checkbox" id="i'+i+'" '+(i===3?'disabled':'')+'>Item '+i+'</label>').join('')+'</div><label><input type="checkbox" id="setting">Single configuration</label><script>window.changes=[];document.addEventListener("change",e=>changes.push(e.target.id));</script><script>'+script+'</script>')});
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await chromium.launch({headless:true,...(process.env.MUSIC_SERVER_BROWSER?{executablePath:process.env.MUSIC_SERVER_BROWSER}:{})});
 try{
  const page=await browser.newPage({viewport:{width:600,height:700}}),errors=[];page.on('pageerror',e=>errors.push(e.message));
  const base='http://127.0.0.1:'+server.address().port;
  const selected=()=>page.evaluate(()=>[...document.querySelectorAll('#group input:checked')].map(i=>i.id));
  const point=async id=>{const b=await page.locator('#'+id).boundingBox();return{x:b.x+b.width/2,y:b.y+b.height/2}};
  const down=async id=>{const p=await point(id);await page.mouse.move(p.x,p.y);await page.mouse.down()};
  const move=async id=>{const p=await point(id);await page.mouse.move(p.x,p.y)};
  await page.goto(base);try{await page.waitForFunction(()=>document.querySelector('.selection-summary')?.textContent==='已选 0 / 当前页 9 项')}catch(error){console.error({errors,summary:await page.locator('.selection-summary').allTextContents()});throw error}
  assert.equal(await page.locator('.selection-summary').count(),1,'Single configuration toggles must not get selection counters');
  await page.locator('#i1').click({button:'right'});assert.deepEqual(await selected(),[],'Right click must not change selection');
  await page.locator('#i1').click();assert.deepEqual(await selected(),['i1']);
  await page.waitForFunction(()=>document.querySelector('.selection-summary').textContent==='已选 1 / 当前页 9 项');
  await page.locator('#i1').click();
  await down('i0');await move('i5');await move('i0');await move('i5');await page.mouse.up();
  assert.deepEqual(await selected(),['i0','i1','i2','i4','i5'],'Fast line path must select intervening enabled rows once');
  await down('i5');await move('i0');await page.mouse.up();assert.deepEqual(await selected(),[],'Starting from a selected checkbox paints deselection');
  // Label text is a checkbox hit region too; rows are wide enough for diagonal paths.
  const label=await page.locator('label').first().boundingBox();await page.mouse.move(label.x+160,label.y+18);await page.mouse.down();await move('i4');await page.mouse.up();
  assert.deepEqual(await selected(),['i0','i1','i2','i4']);
  await down('i6');await move('i7');await page.dispatchEvent('#i6','pointercancel',{pointerId:1,pointerType:'mouse'});await move('i9');await page.mouse.up();
  assert.equal((await selected()).includes('i9'),false,'Canceled gesture must not keep painting');
  await page.waitForFunction(()=>!document.body.classList.contains('selection-painting'));
  // DOM replacement must terminate a gesture and dispose the detached summary.
  await down('i0');await move('i1');await page.evaluate(()=>document.querySelector('#group').remove());await page.mouse.up();
  await page.waitForFunction(()=>!document.querySelector('.selection-summary')&&!document.body.classList.contains('selection-painting'));
  await page.goto(base);await page.evaluate(()=>{const g=document.querySelector('#group');g.style.height='120px';g.style.overflow='auto'});
  await down('i0');await move('i2');await page.evaluate(()=>{document.querySelector('#group').scrollTop=180});
  await page.waitForFunction(()=>document.querySelector('#i7').checked||document.querySelector('#i6').checked);await page.mouse.up();
  // Repeated DOM mutations must update one summary without observer loops.
  await page.evaluate(()=>{for(let i=0;i<5;i++){const label=document.createElement('label');label.innerHTML='<input type="checkbox">New';document.querySelector('#group').append(label)}});
  await page.waitForFunction(()=>document.querySelector('.selection-summary').textContent.includes('当前页 14 项'));
  assert.equal(await page.locator('.selection-summary').count(),1);
  await page.goto(base);await page.evaluate(()=>{window.state={user:{id:'before'}}});await down('i0');await page.evaluate(()=>{state.user={id:'after'}});await move('i2');await page.mouse.up();assert.deepEqual(await selected(),[],'Account replacement must cancel an old selection gesture');
  assert.deepEqual(errors,[]);
  console.log('Selection UI PASS: right click isolation, normal checkbox, swept path, paint once/deselect, labels, disabled rows, cancel, DOM replacement, scrolling and stable counters');
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
}
main().catch(error=>{console.error(error);process.exitCode=1});
