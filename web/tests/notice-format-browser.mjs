import {readFileSync} from 'node:fs';
import {build} from 'vite';
import {chromium} from '@playwright/test';

const values=JSON.parse(readFileSync(0,'utf8'));
const result=await build({configFile:false,logLevel:'silent',define:{'process.env.NODE_ENV':'"production"'},build:{write:false,minify:false,lib:{entry:'src/NoticeBody.tsx',name:'NoticeFormat',formats:['iife']}}});
const output=(Array.isArray(result)?result[0]:result).output.find(item=>item.type==='chunk');
if(!output)throw new Error('actual notice renderer build unavailable');
const browser=await chromium.launch();
try{
 const page=await browser.newPage();
 const errors=[];page.on('pageerror',error=>errors.push(error.message));
 await page.addScriptTag({content:output.code});
 if(errors.length)throw new Error(errors.join('\n'));
 await page.evaluate(values=>{for(const html of values)if(!NoticeFormat.noticeNodes(html).length)throw new Error('empty normalized notice');},values);
 console.log('PASS: '+values.length+' actual normalized notices consumed by Chromium');
}finally{await browser.close();}
