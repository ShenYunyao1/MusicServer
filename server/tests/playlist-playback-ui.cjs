'use strict';
const assert=require('node:assert/strict'),path=require('node:path');
module.exports=async({page,tracks,directory})=>{
 await page.setViewportSize({width:1440,height:1100});
 const playlist=await page.evaluate(async tracks=>{closeOnlinePlayer();const p=await api('playlists','POST',{id:browserUUID(),name:'顺序播放验收',track_ids:[tracks.song.id,tracks.first.id]});await refreshCore();return p},tracks);
 await page.goto(new URL('/#/playlists/'+playlist.id,page.url()).href);await page.locator('[data-play-playlist]').waitFor();
 await page.locator('[data-play-playlist]').click();await page.waitForFunction(id=>onlinePlayback.track?.id===id&&Number.isFinite(memberPlayer.duration),tracks.song.id);await page.evaluate(()=>memberPlayer.pause());
 assert.match(await page.locator('#onlineQueuePosition').textContent(),/1 \/ 2/);assert.equal(await page.locator('#onlinePrevious').isDisabled(),true);
 await page.locator('#onlineQueueToggle').click();await page.locator('#onlineQueuePanel').waitFor({state:'visible'});assert.equal(await page.locator('.online-queue-item').count(),2);
 await page.locator('#onlineLyricsToggle').click();assert.equal(await page.locator('#onlineQueuePanel').isVisible(),false);assert.equal(await page.locator('#onlineLyricsPanel').isVisible(),true);
 await page.evaluate(()=>{renderOnlineLyrics('[00:00]First original\n[00:00]第一句译文\n[00:03]Second original\n[00:03]第二句译文\n[00:07]Third original\n[00:07]第三句译文');memberPlayer.currentTime=4;highlightOnlineLyrics(true)});
 assert.equal(await page.locator('.online-lyric-line').count(),3);assert.match(await page.locator('.online-lyric-line[aria-current=true]').textContent(),/Second original第二句译文/);
 await page.locator('#onlineTranslations').click();assert.equal(await page.locator('.online-lyric-translation').count(),0);assert.equal(await page.evaluate(()=>localStorage.getItem('musicserver.lyrics.translations')),'off');await page.locator('#onlineTranslations').click();assert.equal(await page.locator('.online-lyric-translation').count(),3);assert.equal(await page.evaluate(()=>localStorage.getItem('musicserver.lyrics.translations')),'on');
 const before=await page.locator('#onlineLyricsPanel').boundingBox();assert.ok(before.height<340,'Compact bilingual lyrics do not occupy the whole page');
 await page.locator('#onlineQueueToggle').click();assert.equal(await page.locator('#onlineLyricsPanel').isVisible(),false);await page.mouse.click(700,110);assert.equal(await page.locator('#onlineQueuePanel').isVisible(),false);
 // A route change must preserve the source queue, and natural media end advances it.
 await page.goto(new URL('/#/library',page.url()).href);await page.locator('#trackRows [data-details]').first().waitFor();assert.match(await page.locator('#onlineQueuePosition').textContent(),/1 \/ 2/);
 await page.evaluate(()=>{memberPlayer.currentTime=memberPlayer.duration-.1;memberPlayer.play()});await page.waitForFunction(id=>onlinePlayback.track?.id===id&&memberPlayer.ended,tracks.first.id);
 assert.match(await page.locator('#onlineQueuePosition').textContent(),/2 \/ 2/);assert.equal(await page.locator('#onlineNext').isDisabled(),true);
 await page.locator('#onlinePrevious').click();await page.waitForFunction(id=>onlinePlayback.track?.id===id&&!memberPlayer.paused,tracks.song.id);await page.evaluate(()=>memberPlayer.pause());
 await page.locator('#onlineQueueToggle').click();await page.locator('.online-queue-item').nth(1).click();await page.waitForFunction(id=>onlinePlayback.track?.id===id,tracks.first.id);await page.keyboard.press('Escape');assert.equal(await page.locator('#onlineQueuePanel').isVisible(),false);
 // All library playback entrances return to single-track mode.
 await page.locator('#trackRows .track-cover').first().click();assert.equal(await page.locator('#onlineQueueToggle').isVisible(),false);
 await page.goto(new URL('/#/playlists/'+playlist.id,page.url()).href);await page.locator('[data-play-playlist]').click();await page.waitForFunction(()=>onlineQueue?.items.length===2);
 await page.setViewportSize({width:390,height:844});await page.locator('#onlineLyricsToggle').click();await page.waitForFunction(()=>document.querySelector('.online-lyric-line'));
 await page.evaluate(()=>{memberPlayer.pause();renderOnlineLyrics('[00:00]原文原文原文\n[00:00]Translation with normal wrapping\n[00:03]下一句\n[00:03]Next line')});
 assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);let panel=await page.locator('#onlineLyricsPanel').boundingBox(),bar=await page.locator('#previewBar').boundingBox();assert.ok(panel.y>=45&&panel.y+panel.height<bar.y);
 if(directory)await page.screenshot({path:path.join(directory,'playlist-player-bilingual-mobile.png')});
 await page.locator('#onlineQueueToggle').click();assert.equal(await page.locator('#onlineLyricsPanel').isVisible(),false);panel=await page.locator('#onlineQueuePanel').boundingBox();assert.ok(panel.y>=45&&panel.y+panel.height<bar.y);
 await page.setViewportSize({width:1440,height:1100});
 // Public playback retains its source restriction, even for the owner/admin.
 await page.evaluate(async id=>{const p=(await api('playlists/'+id)).playlist;await api('playlists/'+id+'/visibility','PUT',{visibility:'public'},undefined,p.version)},playlist.id);
 await page.goto(new URL('/#/public/playlists/'+playlist.id,page.url()).href);try{await page.locator('#publicDetail [data-play-playlist]').click({timeout:8000})}catch(error){console.error('Public playback diagnostics',await page.evaluate(()=>({hash:location.hash,page:state.page,detail:state.publicDetailId,status:$('status').textContent,public:$('publicDetail').textContent})));throw error}await page.waitForFunction(()=>onlineQueue?.source==='public');await page.evaluate(()=>memberPlayer.pause());
 await page.evaluate(async id=>{const p=(await api('playlists/'+id)).playlist;await api('playlists/'+id+'/visibility','PUT',{visibility:'private'},undefined,p.version)},playlist.id);
 await page.locator('#onlineNext').click();await page.waitForFunction(()=>onlineQueue===null);assert.equal(await page.evaluate(()=>memberPlayer.paused),true);assert.match(await page.locator('#onlinePlayerStatus').textContent(),/权限/);
 console.log('Playlist playback UI PASS: actual ended advance, previous/next/queue, source isolation, mutual panels, bilingual compact mobile lyrics and revoked public access');
};
