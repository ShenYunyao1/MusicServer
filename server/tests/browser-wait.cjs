// Playwright waitForFunction treats an async predicate's Promise as truthy.
// Resolve browser-side async work in Node and test its value on every poll.
async function waitForAsync(page,predicate,arg,options={}){
 const deadline=Date.now()+(options.timeout??30000),interval=options.interval??100;
 do{
  const result=await page.evaluate(predicate,arg);
  if(result)return result;
  if(Date.now()>=deadline)break;
  await new Promise(resolve=>setTimeout(resolve,interval));
 }while(Date.now()<deadline);
 throw new Error('Asynchronous browser condition timed out');
}
module.exports={waitForAsync};
