const assert=require('node:assert/strict');
const {test}=require('node:test');
const {waitForAsync}=require('./browser-wait.cjs');
test('async false results must not satisfy a browser wait',async()=>{
 let polls=0;
 const page={evaluate:async predicate=>predicate()};
 const value=await waitForAsync(page,async()=>++polls>=3,undefined,{interval:1});
 assert.equal(value,true);assert.equal(polls,3);
});
test('async wait times out instead of accepting an unresolved Promise',async()=>{
 const page={evaluate:async predicate=>predicate()};
 await assert.rejects(waitForAsync(page,async()=>false,undefined,{interval:1,timeout:5}),/timed out/);
});
