'use strict';

function parsePlayerLyrics(value){
 if(value===null||value===undefined||value==='')return {timed:false,lines:[]};
 if(typeof value!=='string')throw new Error('歌词格式无效');
 const limit=2*1024*1024;
 if(value.length>limit)throw new Error('歌词超过2 MiB限制');
 let bytes=0,lineCount=1;
 for(let index=0;index<value.length;index++){
  const code=value.charCodeAt(index);
  if((code===10||code===13&&value.charCodeAt(index+1)!==10)&&++lineCount>20000)throw new Error('歌词行数超过限制');
  if(code<128)bytes++;
  else if(code<2048)bytes+=2;
  else if(code>=0xd800&&code<=0xdbff&&index+1<value.length&&value.charCodeAt(index+1)>=0xdc00&&value.charCodeAt(index+1)<=0xdfff){bytes+=4;index++}
  else bytes+=3;
  if(bytes>limit)throw new Error('歌词超过2 MiB限制');
 }
 const source=value.replace(/^\ufeff/,'').replace(/\r\n?/g,'\n').split('\n');
 const metadata=/^\[(?:ar|ti|al|by|re|ve|length|id):[^\]]*\]\s*$/i;
 const offsetTag=/^\[offset:([+-]?\d+)\]\s*$/i;
 let offset=0;
 for(const raw of source){const match=raw.trim().match(offsetTag);if(match){const amount=Number(match[1]);if(Number.isSafeInteger(amount)&&Math.abs(amount)<=86400000)offset=amount}}
 const timed=[],notes=[],plain=[];
 for(const raw of source){
  let text=raw.trim();
  if(metadata.test(text))continue;
  const offsetMatch=text.match(offsetTag);
  if(offsetMatch&&Number.isSafeInteger(Number(offsetMatch[1]))&&Math.abs(Number(offsetMatch[1]))<=86400000)continue;
  const timestamps=[];
  for(;;){const match=text.match(/^\[(\d{1,5}):([0-5]\d)(?:[.:](\d{1,3}))?\]/);if(!match)break;timestamps.push((Number(match[1])*60+Number(match[2]))*1000+Number((match[3]||'').padEnd(3,'0')));text=text.slice(match[0].length);if(timestamps.length+timed.length>20000)throw new Error('歌词行数超过限制')}
  if(timestamps.length){for(const time of timestamps)timed.push({time:Math.max(0,time-offset)/1000,text:text.trim()})}
  else{plain.push({time:null,text});if(text)notes.push({time:null,text})}
 }
 if(timed.length){timed.sort((first,second)=>first.time-second.time);return {timed:true,lines:timed.concat(notes)}}
 let first=0,last=plain.length;while(first<last&&!plain[first].text)first++;while(last>first&&!plain[last-1].text)last--;
 return {timed:false,lines:plain.slice(first,last)};
}

function playerLyricIndex(lines,seconds){
 if(!Array.isArray(lines)||!Number.isFinite(seconds)||seconds<0)return -1;
 let low=0,high=lines.length-1,result=-1;
 while(low<=high){const middle=Math.floor((low+high)/2),time=lines[middle]?.time;if(Number.isFinite(time)&&time<=seconds){result=middle;low=middle+1}else high=middle-1}
 return result;
}
