import {useEffect,useRef,useState,type FormEvent} from 'react';
import * as api from './api/client';
import {text,link,loginRedirect,errorText,type Lang} from './i18n';

const messages={
 ru:{title:'Предупреждения о подписке',email:'Получать предупреждения по email',help:'Это отдельное согласие на предупреждения о подписке. Письма входа и безопасности от этой настройки не зависят.',unavailable:'Для писем нужен подтверждённый email и вход по паролю.',identity:'Настроить вход по email',save:'Сохранить настройки уведомлений',saved:'Настройки сохранены',empty:'Актуальных предупреждений нет.',dismissed:'Предупреждение закрыто',close:'Закрыть предупреждение',expiry:'Срок подписки',traffic:'Лимит трафика',stars_lapsed:'Автопродление Stars',expiryHelp:'Подписка приближается к указанному сроку.',trafficHelp:'Достигнут порог трафика',starsHelp:'Оплаченный период закончился. Проверьте состояние автопродления: это предупреждение не подтверждает отсутствие списания.',observed:'По данным на',expires:'Срок подписки',used:'Использовано',bytes:'байт',paid:'Оплачено до',renew:'Продлить подписку',stars:'Проверить Stars'},
 en:{title:'Subscription reminders',email:'Receive reminders by email',help:'This is separate consent for subscription reminders. Sign-in and security emails do not depend on this setting.',unavailable:'Email reminders require a verified email and password sign-in.',identity:'Set up email sign-in',save:'Save reminder settings',saved:'Settings saved',empty:'No current reminders.',dismissed:'Reminder dismissed',close:'Dismiss reminder',expiry:'Subscription expiry',traffic:'Traffic limit',stars_lapsed:'Stars auto-renewal',expiryHelp:'Your subscription is approaching the stated expiry.',trafficHelp:'Traffic threshold reached',starsHelp:'The paid period has ended. Check the auto-renewal status: this reminder does not confirm that a charge failed.',observed:'Observed at',expires:'Subscription expiry',used:'Used',bytes:'bytes',paid:'Paid until',renew:'Renew subscription',stars:'Check Stars'}
};
const validDate=(v:unknown)=>typeof v==='string'&&Number.isFinite(Date.parse(v));
const decimal=(v:unknown)=>typeof v==='string'&&/^(0|[1-9][0-9]{0,18})$/.test(v)&&(v.length<19||v<='9223372036854775807');
function checked(value:api.ReminderResult){
 const fail=()=>{throw new api.ApiError(503,'SERVICE_UNAVAILABLE','');};
 if(!value||value.version!=='reminders-v1'||typeof value.email_enabled!=='boolean'||typeof value.email_available!=='boolean'||!Array.isArray(value.reminders)||value.reminders.length>20)return fail();
 const ids=new Set<string>();
 for(const r of value.reminders){
  if(!r||typeof r.id!=='string'||!/^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(r.id)||ids.has(r.id)||!validDate(r.observed_at))return fail();
  ids.add(r.id);
  if(r.kind==='expiry'){
   if(![1,3].includes(r.threshold)||!validDate(r.expires_at)||r.traffic_used_bytes!==null||r.traffic_limit_bytes!==null||r.paid_until!==null||r.route!=='renew')return fail();
  }else if(r.kind==='traffic'){
   if(![80,100].includes(r.threshold)||!decimal(r.traffic_used_bytes)||!decimal(r.traffic_limit_bytes)||r.traffic_limit_bytes==='0'||r.expires_at!==null||r.paid_until!==null||r.route!=='renew')return fail();
  }else if(r.kind==='stars_lapsed'){
   if(r.threshold!==0||!validDate(r.paid_until)||r.expires_at!==null||r.traffic_used_bytes!==null||r.traffic_limit_bytes!==null||r.route!=='cabinet')return fail();
  }else return fail();
 }
 return value;
}

type State={scope:string;value?:api.ReminderResult;enabled:boolean;loading:boolean;busy:boolean;error:string;message:''|'saved'|'dismissed'};
export function ClientReminders({lang,accountID,refreshKey}:{lang:Lang;accountID:string;refreshKey?:string|number}){
 const t=text(lang),m=messages[lang],heading=useRef<HTMLHeadingElement>(null),request=useRef<AbortController|null>(null);
 const[revision,setRevision]=useState(0),[state,setState]=useState<State>({scope:'',enabled:false,loading:true,busy:false,error:'',message:''});
 const scope=JSON.stringify([accountID,lang,refreshKey??'',revision]);
 useEffect(()=>{
  const c=new AbortController();request.current=c;setState({scope,enabled:false,loading:true,busy:false,error:'',message:''});
  void api.getReminders(c.signal).then(result=>{if(c.signal.aborted)return;const value=checked(result);setState({scope,value,enabled:value.email_enabled,loading:false,busy:false,error:'',message:''});}).catch(reason=>{if(c.signal.aborted)return;setState({scope,enabled:false,loading:false,busy:false,error:errorText(reason,lang),message:''});if(reason instanceof api.ApiError&&reason.status===401)loginRedirect(lang);});
  return()=>c.abort();
 },[scope,lang]);
 const current=state.scope===scope,loading=!current||state.loading,value=current?state.value:undefined;
 function failed(reason:unknown,c:AbortController){
  if(c.signal.aborted)return;
  const authLost=reason instanceof api.ApiError&&[401,403,404].includes(reason.status);
  setState(s=>({...s,value:authLost?undefined:s.value,enabled:authLost?false:s.enabled,busy:false,error:errorText(reason,lang),message:''}));
  if(reason instanceof api.ApiError&&reason.status===401)loginRedirect(lang);
 }
 async function save(event:FormEvent){
  event.preventDefault();const c=request.current;if(!current||!value||state.busy||!c||c.signal.aborted||state.enabled&&!value.email_available)return;
  setState(s=>({...s,busy:true,error:'',message:''}));
  try{const out=checked(await api.setReminderEmailPreference(state.enabled,c.signal));if(!c.signal.aborted)setState({scope,value:out,enabled:out.email_enabled,loading:false,busy:false,error:'',message:'saved'});}catch(reason){failed(reason,c);}
 }
 async function dismiss(id:string){
  const c=request.current;if(!current||!value||state.busy||!c||c.signal.aborted)return;
  setState(s=>({...s,busy:true,error:'',message:''}));
  try{await api.dismissReminder(id,c.signal);if(c.signal.aborted)return;setState(s=>({...s,value:s.value?{...s.value,reminders:s.value.reminders.filter(r=>r.id!==id)}:undefined,busy:false,message:'dismissed'}));heading.current?.focus();}catch(reason){failed(reason,c);}
 }
 const date=(v:string)=><time dateTime={v}>{new Date(v).toLocaleString(lang==='ru'?'ru-RU':'en-US')}</time>;
 return <section className="admin-action" aria-label={m.title} aria-busy={loading||current&&state.busy}>
  <h2 ref={heading} tabIndex={-1}>{m.title}</h2>
  {loading?<p role="status">{t.loading}</p>:null}
  {current&&state.error?<div className="error" role="alert"><p>{state.error}</p><button disabled={state.busy} onClick={()=>setRevision(n=>n+1)}>{t.retry}</button></div>:null}
  {current&&state.message?<p role="status" aria-live="polite">{m[state.message]}</p>:null}
  {value?<><form onSubmit={save} aria-busy={state.busy}>
   <label className="check"><input type="checkbox" checked={state.enabled} disabled={state.busy||!value.email_available&&!state.enabled} onChange={e=>setState(s=>({...s,enabled:e.target.checked,message:''}))} aria-describedby="reminder-email-help"/>{m.email}</label>
   <p className="help" id="reminder-email-help">{m.help}{!value.email_available?<> {m.unavailable} <a href={link('/cabinet/identity',lang)}>{m.identity}</a></>:null}</p>
   <button disabled={state.busy||state.enabled===value.email_enabled}>{state.busy?t.sending:m.save}</button>
  </form>{!value.reminders.length?<p role="status">{m.empty}</p>:<ul className="support-list">{value.reminders.map(r=><li className="notice" key={r.id}>
   <h3>{m[r.kind]}</h3>
   {r.kind==='expiry'?<p>{m.expiryHelp} {m.expires}: {date(r.expires_at!)}</p>:r.kind==='traffic'?<p>{m.trafficHelp}: {r.threshold}%. {m.used}: {r.traffic_used_bytes} / {r.traffic_limit_bytes} {m.bytes}.</p>:<p>{m.starsHelp} {m.paid}: {date(r.paid_until!)}</p>}
   <p className="help">{m.observed}: {date(r.observed_at)}</p>
   <div className="purchase-actions"><a href={link(r.route==='renew'?'/cabinet/renew':'/cabinet',lang)}>{r.route==='renew'?m.renew:m.stars}</a><button type="button" disabled={state.busy} aria-label={m.close+': '+m[r.kind]} onClick={()=>void dismiss(r.id)}>{m.close}</button></div>
  </li>)}</ul>}</>:null}
 </section>;
}
