'use strict';
// Uses the real preview executable and the existing web backup/CLI restore flow.
const assert=require('node:assert/strict');
const path=require('node:path');
const {spawnSync}=require('node:child_process');

function tagRows(database){
 const script=`import sqlite3,json,sys
c=sqlite3.connect(sys.argv[1])
tables=['tag_groups','tag_scopes','tags','tag_aliases','track_tags','track_tag_overrides','track_tag_states','saved_track_filters','track_tag_imports','tag_batches']
result={t:sorted(c.execute('SELECT * FROM '+t).fetchall(),key=repr) for t in tables}
result['schema']=c.execute('PRAGMA user_version').fetchone()[0]
assert c.execute('PRAGMA integrity_check').fetchone()[0]=='ok'
assert not c.execute('PRAGMA foreign_key_check').fetchall()
print(json.dumps(result))`;
 const result=spawnSync(process.env.MUSIC_SERVER_TEST_PYTHON||'python',['-c',script,database],{encoding:'utf8',windowsHide:true,timeout:20000});
 assert.equal(result.status,0,result.stderr);return JSON.parse(result.stdout);
}

module.exports=async function({page,browser,base,directory}){
 const health=await(await fetch(base+'/api/v2/health')).json();assert.equal(health.schema,8,'This acceptance requires the isolated schema-8 preview');
 await page.evaluate(async()=>{
  const track=state.tracks[0];
  const publicTag=await api('tags','POST',{scope:'public',group_id:'genre',name:'Backup public',aliases:['Backup alias']});
  const privateTag=await api('tags','POST',{scope:'personal',group_id:'scene',name:'Backup private'});
  const view=await api('tracks/'+track.id+'/tags');
  await api('tracks/'+track.id+'/tags','PATCH',{scope:'public',add_ids:[publicTag.id],remove_ids:[]},undefined,view.public_version);
  await api('tracks/'+track.id+'/tags','PATCH',{scope:'personal',add_ids:[privateTag.id],remove_ids:[]},undefined,view.personal_version);
  await api('saved-track-filters','POST',{name:'Backup rule',filter:{version:1,tags:[{scope:'personal',group_id:'scene',include_ids:[privateTag.id]}]}});
  await api('tag-batches','POST',{scope:'personal',track_ids:[track.id],add_ids:[privateTag.id],remove_ids:[]});
 });
 const before=tagRows(path.join(directory,'library.sqlite'));assert.equal(before.schema,8);assert.ok(before.saved_track_filters.length&&before.track_tags.length&&before.tag_batches.length);
 await require('./backup-ui.cjs')({page,browser,base,directory});
 const after=tagRows(path.join(directory,'backup-restored','data','library.sqlite'));
 assert.deepEqual(after,before,'Full web backup and real CLI restore must retain every tag/filter/import/task row');
 console.log('Schema-8 backup acceptance passed: real executable, public/private bindings, aliases, saved filters, all ten tables, media digest, integrity and foreign keys.');
};
