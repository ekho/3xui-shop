import {useEffect,useRef,useState,type FormEvent} from 'react';
import * as api from './api/client';
import {config} from './config';
import {text,link,errorText,type Lang} from './i18n';
export function Security({lang}:{lang:Lang}){
 const t=text(lang),input=useRef<HTMLInputElement>(null),poll=useRef<AbortController>(null);
 const[newEmail,setNewEmail]=useState('');const[info,setInfo]=useState<api.AccountSecurity>();const[current,setCurrent]=useState('');const[next,setNext]=useState('');const[repeat,setRepeat]=useState('');const[busy,setBusy]=useState(false);const[error,setError]=useState('');const[saved,setSaved]=useState(false);const[uncertain,setUncertain]=useState(false);const[cooldown,setCooldown]=useState(0);
 useEffect(()=>{const controller=new AbortController();void(async()=>{try{await api.getAccount(controller.signal);const out=await api.getAccountSecurity(controller.signal);if(!controller.signal.aborted){setInfo(out);if(out.pending_email_change)setNewEmail(out.pending_email_change.new_email);}}catch(e){if(!controller.signal.aborted){setError(errorText(e,lang));if(e instanceof api.ApiError&&e.status===401)setUncertain(true);}}})();return()=>controller.abort();},[]);
 useEffect(()=>{if(cooldown){const timer=setTimeout(()=>setCooldown(0),cooldown*1000);return()=>clearTimeout(timer);}},[cooldown]);
 useEffect(()=>{if(!info?.pending_email_change||busy)return;const controller=new AbortController();poll.current=controller;let timer:ReturnType<typeof setTimeout>;
  async function refresh(){try{const out=await api.getAccountSecurity(controller.signal);if(controller.signal.aborted)return;setInfo(out);if(!out.pending_email_change)return;}catch(e){if(controller.signal.aborted)return;setError(errorText(e,lang));if(e instanceof api.ApiError&&(e.status===401||e.status===403)){if(e.status===401){api.clearSession();setInfo(undefined);setNewEmail('');setCurrent('');setNext('');setRepeat('');setUncertain(true);}return;}}timer=setTimeout(refresh,5000);}
  timer=setTimeout(refresh,5000);return()=>{controller.abort();if(poll.current===controller)poll.current=null;clearTimeout(timer);};
 },[!!info?.pending_email_change,busy]);
 async function emailMutation(cancel:boolean){
  if(!info||busy||uncertain||(!cancel&&cooldown>0))return;setError('');setSaved(false);
  if(!cancel&&(!current||!newEmail.trim())){setError(t.required);input.current?.focus();return;}
  poll.current?.abort();setBusy(true);try{if(cancel){await api.cancelEmailChange();setNewEmail('');}else{const out=await api.requestEmailChange({new_email:newEmail,current_password:current});setCooldown(out.resend_after);}setInfo(await api.getAccountSecurity());setSaved(true);
  }catch(e){setError(errorText(e,lang));if(e instanceof api.ApiError&&e.status===429)setCooldown(e.retryAfter||60);if(e instanceof api.ApiError&&e.status===401){api.clearSession();setInfo(undefined);setNewEmail('');setCurrent('');setNext('');setRepeat('');setUncertain(true);}}finally{setCurrent('');setBusy(false);}
 }
 async function mutate(change:boolean){
  if(busy||uncertain||cooldown)return;setError('');setSaved(false);
  if(!current||(change&&!next)){setError(t.required);input.current?.focus();return;}
  if(change&&(Array.from(next).length<15||Array.from(next).length>128)){setError(t.passwordInvalid);return;}
  if(change&&next!==repeat){setError(t.passwordMismatch);return;}
  poll.current?.abort();setBusy(true);try{if(change)await api.changePassword({current_password:current,new_password:next});else await api.revokeOtherSessions({current_password:current});await api.getAccount();setInfo(await api.getAccountSecurity());setSaved(true);
  }catch(e){if(e instanceof api.ApiError&&(e.status===503||e.status===401)){api.clearSession();setInfo(undefined);setNewEmail('');setCurrent('');setNext('');setRepeat('');setUncertain(true);setError(t.signInAgain);}else{setError(errorText(e,lang));if(e instanceof api.ApiError&&e.status===429)setCooldown(e.retryAfter||60);input.current?.focus();}}finally{setCurrent('');setNext('');setRepeat('');setBusy(false);}
 }
 function submit(e:FormEvent){e.preventDefault();void mutate(true);}
 const disabled=!info||busy||uncertain||cooldown>0;
 return <section className="card auth"><p className="eyebrow">{t.cabinet}</p><h1>{t.security}</h1>{info?<p>{info.email}</p>:null}
  {error?<p role="alert" className="error">{error}{uncertain?<><br/><a href={link('/login',lang)}>{t.login}</a></>:null}</p>:null}
  {!info&&!uncertain&&error?<button type="button" onClick={()=>location.reload()}>{t.retry}</button>:null}
  {saved?<p role="status" className="notice">{t.saved}</p>:null}
   <label htmlFor="current-password">{t.currentPassword}<input ref={input} id="current-password" form="password-change" type="password" autoComplete="current-password" value={current} onChange={e=>setCurrent(e.target.value)} required/></label>
  <form id="password-change" onSubmit={submit} noValidate aria-busy={busy}>

   <label htmlFor="new-password">{t.newPassword}<input id="new-password" type="password" autoComplete="new-password" value={next} onChange={e=>setNext(e.target.value)} aria-describedby="security-password-help"/></label>
   <label htmlFor="repeat-password">{t.repeatPassword}<input id="repeat-password" type="password" autoComplete="new-password" value={repeat} onChange={e=>setRepeat(e.target.value)}/></label><p id="security-password-help" className="help">{t.passwordHelp}</p>
   <button className="primary" disabled={disabled}>{t.changePassword}</button><button type="button" disabled={disabled} onClick={()=>void mutate(false)}>{t.endOtherSessions}</button>
   {info?<p className="help">{info.has_other_sessions?t.otherSessions:t.noOtherSessions}</p>:null}{cooldown>0?<p role="status">{t.waiting}: {cooldown} {t.seconds}</p>:null}
  </form>
  {info?.pending_email_change?<div className="key-panel" aria-live="polite"><p>{info.pending_email_change.new_email}</p><p>{t.expires}: {new Date(info.pending_email_change.expires_at).toLocaleString(lang==='ru'?'ru-RU':'en-US')}</p><p>{info.pending_email_change.current_email_confirmed?t.currentEmailConfirmed:t.currentEmailWaiting}</p><p>{info.pending_email_change.new_email_confirmed?t.newEmailConfirmed:t.newEmailWaiting}</p><p className="help">{t.emailReplaceWarning}</p><button type="button" disabled={!info||busy||uncertain} onClick={()=>void emailMutation(true)}>{t.cancelEmail}</button></div>:null}
  <form onSubmit={e=>{e.preventDefault();void emailMutation(false);}} noValidate aria-busy={busy}><label htmlFor="new-email">{t.newEmail}<input id="new-email" type="email" autoComplete="email" value={newEmail} onChange={e=>setNewEmail(e.target.value)} required/></label><button disabled={disabled}>{info?.pending_email_change?t.resendBoth:t.changeEmail}</button><p className="help">{t.oldMailboxHelp}</p><a href={config.supportURL}>{t.support}</a></form>
  <nav className="auth-links"><a href={link('/cabinet',lang)}>{t.cabinet}</a></nav>
 </section>;
}
