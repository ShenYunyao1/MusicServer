const assert=require('node:assert/strict');
module.exports=async function(page,base){
 let release,observe;
 const held=new Promise(resolve=>release=resolve),observed=new Promise(resolve=>observe=resolve);
 const handler=async route=>{const response=await route.fetch();observe(response.status());await held;await route.fulfill({response})};
 await page.route('**/api/v2/auth/me',handler);
 try{
  await page.goto(base+'/#/account/profile');assert.equal(await observed,401);
  for(const id of ['username','password','loginButton'])assert.equal(await page.locator('#'+id).isDisabled(),true,id+' must wait for initial identity lookup');
 }finally{release()}
 await page.waitForFunction(()=>!document.querySelector('#loginButton').disabled);
 assert.equal(await page.locator('#loginScreen').isVisible(),true);
 await page.unroute('**/api/v2/auth/me',handler);
 console.log('Bootstrap login passed: delayed anonymous lookup cannot race editable credentials.');
};
