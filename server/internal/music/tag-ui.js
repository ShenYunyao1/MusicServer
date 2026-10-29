'use strict';

// Only account-scoped revision tokens are polled. Private object IDs never
// enter shared events, and drafts keep their original optimistic versions.
const tagSyncWorkspace={owner:null,epoch:0,baseline:null,pending:false};
function clearTagSyncWorkspace(){tagSyncWorkspace.epoch++;tagSyncWorkspace.owner=null;tagSyncWorkspace.baseline=null;tagSyncWorkspace.pending=false}
function renderTrackTagSummary(track){
  if(!tagFeatureEnabled()||!track.tag_summary?.items?.length)return null;
  const host=document.createElement('div');host.className='track-tag-summary';host.setAttribute('aria-label','曲目标签');
  for(const tag of track.tag_summary.items.slice(0,3)){
    const button=document.createElement('button');button.type='button';button.className='track-summary-tag'+(tag.scope==='personal'?' personal':'');button.textContent=tag.name;
    button.title=(tag.scope==='personal'?'我的标签：':'公共标签：')+tag.name;button.onclick=event=>{event.stopPropagation();void applyTrackTagFilter(tag)};host.append(button);
  }
  const remaining=track.tag_summary.total-host.children.length;
  if(remaining>0){const more=document.createElement('span');more.className='track-tag-count';more.textContent='+'+remaining;more.title='打开曲目详情查看全部标签';host.append(more)}
  return host;
}
function tagSyncDraftOpen(){
  if(typeof compactTagger!=='undefined'&&compactTagger?.busy)return true;
  return Boolean(document.querySelector('dialog[open]:not(#trackDetails)'))||Boolean(state.playlistEditing||state.playlistCreating||state.playlistTrackSortBusy||state.playlistSortBusy);
}
async function pollTagChanges(){
  if(document.hidden||!state.user||!tagFeatureEnabled()||tagSyncWorkspace.pending)return;
  if(tagSyncWorkspace.owner!==state.user.id){clearTagSyncWorkspace();tagSyncWorkspace.owner=state.user.id}
  const owner=state.user.id,epoch=tagSyncWorkspace.epoch;
  const current=()=>owner===state.user?.id&&epoch===tagSyncWorkspace.epoch;
  tagSyncWorkspace.pending=true;
  try{
    const next=await api('tag-sync');if(!current()||next.account_id!==owner)return;
    // A change can land between the first page read and the first poll.
    // Compare with the page's own transaction snapshot before acknowledging
    // a newly observed token; otherwise that change would never refresh it.
    const pageBaseline=typeof browseState!=='undefined'?browseState.tracks?.baseline:null;
    const previous=tagSyncWorkspace.baseline||(pageBaseline?.account_id===owner?{...pageBaseline,saved:next.saved}:null);
    if(!previous){tagSyncWorkspace.baseline=next;return}
    if(['library','public','personal','saved'].every(key=>next[key]===previous[key])){tagSyncWorkspace.baseline=next;return}
    if(tagSyncDraftOpen())return;
    // Saved rules never mutate the currently applied query. The preset list
    // is re-read on every drawer open, preserving any open draft's version.
    if(['library','public','personal'].every(key=>next[key]===previous[key])){tagSyncWorkspace.baseline=next;return}
    if(next.public!==previous.public){
      const groups=await api('tag-groups');if(!current()||tagSyncDraftOpen())return;tagWorkspace.groups=groups;
    }
    if(typeof trackFilterWorkspace!=='undefined'){trackFilterWorkspace.labels.clear();trackFilterWorkspace.baseline=null}
    const keepTaggerPage=typeof compactTagger!=='undefined'&&compactTagger&&typeof loadFilteredTracks==='function';
    let offset=keepTaggerPage?(browseState.tracks?.offset||0):0;
    if(state.page==='tracks'&&typeof readTrackFilterRoute==='function'){
      const config=readTrackFilterRoute();writeTrackFilterRoute(config.filter,config.sort,config.direction,offset);
    }
    let route=location.hash,loads=1;const page=state.page,generation=typeof browseState!=='undefined'?browseState.generations.tracks:0;
    if(page==='tracks'&&typeof loadBrowseTracks==='function'){
      let result=keepTaggerPage?await loadFilteredTracks(offset,{fresh:true}):await loadBrowseTracks(0);
      if(result&&offset>0&&!result.items.length&&current()&&route===location.hash&&state.page===page){
        offset=Math.floor(Math.max(0,result.total-1)/(result.limit||50))*(result.limit||50);
        const config=readTrackFilterRoute();writeTrackFilterRoute(config.filter,config.sort,config.direction,offset);
        route=location.hash;loads++;
        result=await loadFilteredTracks(offset,{fresh:true});
      }
      if(!current()||route!==location.hash||state.page!==page)return;
      // The loader owns request generation and ignores older results.
      if(!result)return;
      if(typeof browseState!=='undefined'&&browseState.generations.tracks>generation+loads)return;
      renderTracks();
    }
    const detail=$('trackDetails');
    if(detail?.open&&typeof decorateTrackTagDetails==='function'){
      const id=$('trackDetailsBody')?.dataset.tagTrack,track=state.tracks.find(track=>track.id===id);
      if(track)await decorateTrackTagDetails(track);
    }
    if(current()&&!tagSyncDraftOpen())tagSyncWorkspace.baseline=next;
  }catch(error){
    // Keep the old token so a transient failure is retried; normal account
    // authentication handling still owns expiry and disabled-account UI.
    if(current()&&error.code!=='TAGS_UNAVAILABLE')tagSyncWorkspace.error=error.message;
  }finally{if(current())tagSyncWorkspace.pending=false}
}
setInterval(()=>void pollTagChanges(),15000);
window.addEventListener('online',()=>void pollTagChanges());
document.addEventListener('visibilitychange',()=>{if(!document.hidden)void pollTagChanges()});
// Reuse revision-based refresh after an immediate edit; do not reload the whole
// workspace or reset the user's selection while the tagger is in use.
let taggerRefreshTimer;
document.addEventListener('tagger-saved',()=>{
  clearTimeout(taggerRefreshTimer);
  const owner=state.user?.id;
  const refresh=()=>{if(state.user?.id!==owner)return;if(tagSyncWorkspace.pending){taggerRefreshTimer=setTimeout(refresh,100);return}void pollTagChanges()};
  taggerRefreshTimer=setTimeout(refresh,0);
});
