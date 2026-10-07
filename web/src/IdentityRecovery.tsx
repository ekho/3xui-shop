import {useEffect,useRef,useState,type FormEvent} from 'react';
import * as api from './api/client';
import {config} from './config';
import {text,link,errorText,type Lang} from './i18n';

export function IdentityRecovery({lang}:{lang:Lang}){
 const t=text(lang),request=useRef<AbortController|undefined>(undefined),alive=useRef(true),pw=useRef<HTMLInputElement>(null);
 const[token,setToken]=useState(()=>{const value=window.__emailToken;delete window.__emailToken;return value&&/^[A-Za-z0-9_-]{43}$/.test(value)?value:undefined;});
 const[challenge,setChallenge]=useState(''),[code,setCode]=useState(''),[password,setPassword]=useState(''),[repeat,setRepeat]=useState(''),[terms,setTerms]=useState(false),[privacy,setPrivacy]=useState(false),[busy,setBusy]=useState(false),[error,setError]=useState(''),[done,setDone]=useState(false),[closed,setClosed]=useState(false);
 function clear(){setToken(undefined);setChallenge('');setCode('');setPassword('');setRepeat('');setTerms(false);setPrivacy(false);}
 useEffect(()=>{alive.current=true;const hide=()=>{alive.current=false;request.current?.abort();clear();setBusy(false);setClosed(true);};window.addEventListener('pagehide',hide);return()=>{alive.current=false;request.current?.abort();window.removeEventListener('pagehide',hide);};},[]);
 async function submit(event:FormEvent){
  event.preventDefault();if(busy||closed||done)return;setError('');
  if(!terms||!privacy||!token&&(!/^[\da-f]{8}-[\da-f]{4}-[\da-f]{4}-[\da-f]{4}-[\da-f]{12}$/i.test(challenge)||!/^\d{8}$/.test(code))){setError(t.required);return;}
  if(Array.from(password).length<15||Array.from(password).length>128){setError(t.passwordInvalid);pw.current?.focus();return;}
  if(password!==repeat){setError(t.passwordMismatch);pw.current?.focus();return;}
  const controller=new AbortController();request.current=controller;setBusy(true);
  try{await api.completeIdentityRecovery({...token?{token}:{challenge_id:challenge,code},new_password:password,accepted_terms_version:config.termsVersion,accepted_privacy_version:config.privacyVersion},controller.signal);if(alive.current&&!controller.signal.aborted){clear();setDone(true);}}
  catch(reason){if(alive.current&&!controller.signal.aborted)setError(reason instanceof api.ApiError&&reason.code==='INVALID_VERIFICATION'?t.identityExpired:reason instanceof api.ApiError&&reason.code==='IDENTITY_CONFLICT'?t.identityConflict:errorText(reason,lang));}
  finally{if(alive.current&&!controller.signal.aborted){setPassword('');setRepeat('');setBusy(false);}}
 }
 return <section className="card auth"><h1>{t.recoveryTitle}</h1>{error?<p role="alert">{error}</p>:null}{closed?<p role="alert">{t.recoveryClosed}</p>:done?<><p role="status">{t.recoveryComplete}</p><a href={link('/login',lang)}>{t.login}</a></>:<form aria-busy={busy} onSubmit={event=>void submit(event)}>{!token?<><label>{t.challenge}<input value={challenge} onChange={event=>setChallenge(event.target.value)} autoComplete="off" disabled={busy}/></label><label>{t.code}<input value={code} onChange={event=>setCode(event.target.value)} inputMode="numeric" maxLength={8} autoComplete="off" disabled={busy}/></label></>:<p>{t.recoveryExplicit}</p>}<label>{t.newPassword}<input ref={pw} type="password" autoComplete="new-password" value={password} onChange={event=>setPassword(event.target.value)} disabled={busy}/></label><label>{t.repeatPassword}<input type="password" autoComplete="new-password" value={repeat} onChange={event=>setRepeat(event.target.value)} disabled={busy}/></label><label className="check"><input type="checkbox" checked={terms} onChange={event=>setTerms(event.target.checked)} disabled={busy}/>{t.terms}</label><a href={config.termsURL}>{t.termsLink}</a><label className="check"><input type="checkbox" checked={privacy} onChange={event=>setPrivacy(event.target.checked)} disabled={busy}/>{t.privacy}</label><a href={config.privacyURL}>{t.privacyLink}</a><button className="primary" disabled={busy||!terms||!privacy}>{busy?t.sending:t.recoveryRestore}</button></form>}<a className="support" href={config.supportURL}>{t.support}</a></section>;
}
