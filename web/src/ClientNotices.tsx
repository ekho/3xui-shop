import {useEffect,useRef,useState,type FormEvent} from 'react';
import * as api from './api/client';
import {NoticeBody,noticeNodes,noticeID,noticeInteger,invalidNotice} from './NoticeBody';
import {text,link,loginRedirect,errorText,type Lang} from './i18n';

const messages={
 ru:{title:'Сообщения',email:'Получать сообщения оператора по email',help:'Это отдельное согласие на сообщения оператора. Письма входа, безопасности и предупреждения о подписке имеют свои настройки.',unavailable:'Для писем нужен подтверждённый email и вход по паролю.',identity:'Настроить вход по email',save:'Сохранить настройки сообщений',saved:'Настройки сохранены',empty:'Сообщений нет.',dismissed:'Сообщение закрыто',close:'Закрыть сообщение',page:'Страница'},
 en:{title:'Messages',email:'Receive operator messages by email',help:'This is separate consent for operator messages. Sign-in, security emails and subscription reminders have their own settings.',unavailable:'Email requires a verified email and password sign-in.',identity:'Set up email sign-in',save:'Save message settings',saved:'Settings saved',empty:'No messages.',dismissed:'Message closed',close:'Close message',page:'Page'}
};
function checked(value:api.NoticeResult,page:number){
 if(!value||value.version!=='operator-notices-v1'||typeof value.email_enabled!=='boolean'||typeof value.email_available!=='boolean'||value.page!==page||value.per_page!==20||!Number.isSafeInteger(value.total)||value.total<0||!Array.isArray(value.notices)||value.notices.length>20||value.notices.length>value.total)return invalidNotice();
 const ids=new Set<string>();
 for(const n of value.notices){
  if(!n||!noticeID(n.id)||ids.has(n.id)||!noticeInteger(n.revision,1)||typeof n.text!=='string'||!n.text.trim()||typeof n.created_at!=='string'||!Number.isFinite(Date.parse(n.created_at)))return invalidNotice();
  ids.add(n.id);noticeNodes(n.html);
 }
 return value;
}
type State={scope:string;value?:api.NoticeResult;enabled:boolean;loading:boolean;busy:boolean;error:string;message:''|'saved'|'dismissed'};
export function ClientNotices({lang,accountID,refreshKey}:{lang:Lang;accountID:string;refreshKey?:string|number}){
 const t=text(lang),m=messages[lang],heading=useRef<HTMLHeadingElement>(null),request=useRef<AbortController|null>(null);
 const[page,setPage]=useState(1),[reload,setReload]=useState(0),[state,setState]=useState<State>({scope:'',enabled:false,loading:true,busy:false,error:'',message:''});
 const scope=JSON.stringify([accountID,lang,refreshKey??'',page,reload]);
 useEffect(()=>{
  const c=new AbortController();request.current=c;setState({scope,enabled:false,loading:true,busy:false,error:'',message:''});
  void api.getNotices(page,c.signal).then(result=>{if(c.signal.aborted)return;const value=checked(result,page);setState({scope,value,enabled:value.email_enabled,loading:false,busy:false,error:'',message:''});}).catch(reason=>{if(c.signal.aborted)return;setState({scope,enabled:false,loading:false,busy:false,error:errorText(reason,lang),message:''});if(reason instanceof api.ApiError&&reason.status===401)loginRedirect(lang);});
  return()=>c.abort();
 },[scope,lang,page]);
 const current=state.scope===scope,value=current?state.value:undefined,loading=!current||state.loading;
 function failed(reason:unknown,c:AbortController){
  if(c.signal.aborted)return;
  const lost=reason instanceof api.ApiError&&[401,403,404].includes(reason.status);
  setState(s=>({...s,value:lost?undefined:s.value,enabled:lost?false:s.enabled,busy:false,error:errorText(reason,lang),message:''}));
  if(reason instanceof api.ApiError&&reason.status===401)loginRedirect(lang);
 }
 async function save(event:FormEvent){
  event.preventDefault();const c=request.current;if(!current||!value||state.busy||!c||c.signal.aborted||state.enabled&&!value.email_available)return;
  setState(s=>({...s,busy:true,error:'',message:''}));
  try{const out=checked(await api.setNoticeEmailPreference(state.enabled,c.signal),1);if(!c.signal.aborted)setState({scope,value:page===1?out:{...value,email_enabled:out.email_enabled,email_available:out.email_available},enabled:out.email_enabled,loading:false,busy:false,error:'',message:'saved'});}catch(reason){failed(reason,c);}
 }
 async function dismiss(id:string){
  const c=request.current;if(!current||!value||state.busy||!c||c.signal.aborted)return;
  setState(s=>({...s,busy:true,error:'',message:''}));
  try{await api.dismissNotice(id,c.signal);if(c.signal.aborted)return;
   setState(s=>({...s,value:{...value,total:Math.max(0,value.total-1),notices:value.notices.filter(n=>n.id!==id)}}));
   const out=checked(await api.getNotices(page,c.signal),page);if(c.signal.aborted)return;setState({scope,value:out,enabled:out.email_enabled,loading:false,busy:false,error:'',message:'dismissed'});heading.current?.focus();
  }catch(reason){failed(reason,c);}
 }
 return <section className="admin-action" aria-label={m.title} aria-busy={loading||current&&state.busy}>
  <h2 ref={heading} tabIndex={-1}>{m.title}</h2>
  {loading?<p role="status">{t.loading}</p>:null}
  {current&&state.error?<div className="error" role="alert"><p>{state.error}</p><button disabled={state.busy} onClick={()=>setReload(n=>n+1)}>{t.retry}</button></div>:null}
  {current&&state.message?<p role="status" aria-live="polite">{m[state.message]}</p>:null}
  {value?<><form onSubmit={save} aria-busy={state.busy}>
   <label className="check"><input type="checkbox" checked={state.enabled} disabled={state.busy||!value.email_available&&!state.enabled} onChange={e=>setState(s=>({...s,enabled:e.target.checked,message:''}))} aria-describedby="notice-email-help"/>{m.email}</label>
   <p className="help" id="notice-email-help">{m.help}{!value.email_available?<> {m.unavailable} <a href={link('/cabinet/identity',lang)}>{m.identity}</a></>:null}</p>
   <button disabled={state.busy||state.enabled===value.email_enabled}>{state.busy?t.sending:m.save}</button>
  </form>{!value.notices.length?<p role="status">{m.empty}</p>:<ul className="support-list">{value.notices.map(n=><li className="notice" key={n.id}>
   <p className="help"><time dateTime={n.created_at}>{new Date(n.created_at).toLocaleString(lang==='ru'?'ru-RU':'en-US')}</time></p><NoticeBody html={n.html} lang={lang}/>
   <button type="button" disabled={state.busy} onClick={()=>void dismiss(n.id)}>{m.close}</button>
  </li>)}</ul>}
  <nav className="admin-pagination" aria-label={m.title}><button disabled={state.busy||page<=1} onClick={()=>setPage(p=>p-1)}>{t.previousPage}</button><span>{m.page} {page}</span><button disabled={state.busy||page*20>=value.total} onClick={()=>setPage(p=>p+1)}>{t.nextPage}</button></nav></>:null}
 </section>;
}
