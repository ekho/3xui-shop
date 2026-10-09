import {createElement,type ReactNode} from 'react';
import {ApiError} from './api/client';
import type {Lang} from './i18n';

export const noticeID=(v:unknown):v is string=>typeof v==='string'&&/^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(v)&&v!=='00000000-0000-0000-0000-000000000000';
export const noticeInteger=(v:unknown,min=0):v is number=>Number.isSafeInteger(v)&&Number(v)>=min&&Number(v)<=2147483647;
export const invalidNotice=():never=>{throw new ApiError(503,'SERVICE_UNAVAILABLE','');};
function safeLink(value:string){
 if(/[\s\u0000-\u001f\u007f-\u009f]/u.test(value))return false;
 try{if(/[\u0000-\u001f\u007f-\u009f\ufffe\uffff]/u.test(decodeURIComponent(value)))return false;const u=new URL(value);if(u.username||u.password)return false;
  if(u.protocol==='https:')return !!u.hostname;
  return /^tg:\/\/user\?id=[1-9][0-9]*$/.test(value)&&BigInt(value.split('=')[1])<=4503599627370495n;
 }catch{return false;}
}
// The normalized fragment is strict XML apart from Telegram's boolean attribute.
// Parse into a detached document; only validated React elements reach the page.
export function noticeNodes(html:string,lang:Lang='en'):ReactNode[]{
 if(typeof html!=='string'||!html||html.length>32768||/[\u0000-\u0008\u000b-\u001f\u007f-\u009f\ud800-\udfff]/u.test(html)||/<[!?]/.test(html))return invalidNotice();
 const doc=new DOMParser().parseFromString('<notice>'+html.replaceAll('<blockquote expandable>','<blockquote expandable="">')+'</notice>','application/xml');
 if(doc.querySelector('parsererror')||doc.documentElement.tagName!=='notice'||!doc.documentElement.textContent?.trim())return invalidNotice();
 const render=(node:Node,parents:string[],key:number):ReactNode=>{
  if(node.nodeType===Node.TEXT_NODE)return node.textContent;
  if(node.nodeType!==Node.ELEMENT_NODE)return invalidNotice();
  const e=node as Element,tag=e.tagName;
  if(e.namespaceURI||!['b','i','u','s','tg-spoiler','code','pre','blockquote','a'].includes(tag))return invalidNotice();
  if(parents.some(p=>p==='code'||p==='pre'&&tag!=='code'||tag==='a'&&p==='a'||tag==='blockquote'&&p==='blockquote'||(tag==='pre'||tag==='code')&&p!=='pre'&&p!=='blockquote'))return invalidNotice();
  const props:Record<string,unknown>={key};
  for(const a of Array.from(e.attributes)){
   if(a.namespaceURI)return invalidNotice();
   if(tag==='a'&&a.name==='href'&&safeLink(a.value)){props.href=a.value;props.target='_blank';props.rel='noopener noreferrer';}
   else if(tag==='code'&&a.name==='class'&&/^language-[A-Za-z0-9_+.-]{1,32}$/.test(a.value)&&parents.at(-1)==='pre')props.className=a.value;
   else if(!(tag==='blockquote'&&a.name==='expandable'&&a.value===''))return invalidNotice();
  }
  if(tag==='a'&&!props.href)return invalidNotice();
  const children=Array.from(e.childNodes).map((child,i)=>render(child,[...parents,tag],i));
  if(tag==='tg-spoiler')return createElement('details',{key,className:'notice-spoiler'},createElement('summary',null,lang==='ru'?'Показать скрытый текст':'Show hidden text'),createElement('div',null,children));
  return createElement(tag,props,children);
 };
 return Array.from(doc.documentElement.childNodes).map((node,i)=>render(node,[],i));
}
export function NoticeBody({html,lang='en'}:{html:string;lang?:Lang}){return <div className="notice-body">{noticeNodes(html,lang)}</div>;}
