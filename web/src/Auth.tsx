import {useEffect,useRef,useState,type FormEvent} from 'react';
import * as api from './api/client';
import {config} from './config';
import {text,link,errorText,type Lang} from './i18n';
declare global{interface Window{__emailToken?:string}}
export function Auth({mode,lang}:{mode:'register'|'verify-email'|'login';lang:Lang}){
 const t=text(lang);const[email,setEmail]=useState('');const[password,setPassword]=useState('');const[challenge,setChallenge]=useState('');const[code,setCode]=useState('');const[terms,setTerms]=useState(false);const[privacy,setPrivacy]=useState(false);const[token,setToken]=useState(()=>{const value=window.__emailToken;delete window.__emailToken;return value;});
 const[busy,setBusy]=useState(false);const[error,setError]=useState('');const[success,setSuccess]=useState(false);const[cooldown,setCooldown]=useState(0);const pw=useRef<HTMLInputElement>(null);const emailInput=useRef<HTMLInputElement>(null);
 useEffect(()=>{if(cooldown>0){const timer=setTimeout(()=>setCooldown(0),cooldown*1000);return()=>clearTimeout(timer);}},[cooldown]);
 async function submit(event:FormEvent){event.preventDefault();if(busy||cooldown>0)return;setError('');
  if(mode==='register'&&(!email.trim()||!terms||!privacy)||mode==='login'&&(!email.trim()||!password)||mode==='verify-email'&&!token&&(!challenge||!code)){setError(t.required);emailInput.current?.focus();return;}
  if(mode==='verify-email'&&(Array.from(password).length<15||Array.from(password).length>128)){setError(t.passwordInvalid);pw.current?.focus();return;}
  setBusy(true);try{if(mode==='register'){const out=await api.registerAccount({email,locale:lang,accepted_terms_version:config.termsVersion,accepted_privacy_version:config.privacyVersion});setChallenge(out.challenge_id);setCooldown(out.resend_after);setSuccess(true);}
   else if(mode==='verify-email'){await api.verifyEmail(token?{token,new_password:password}:{challenge_id:challenge,code,new_password:password});setToken(undefined);setPassword('');setSuccess(true);}
   else{await api.loginAccount({email,password});setPassword('');location.assign(link('/cabinet',lang));}
  }catch(e){setError(errorText(e,lang));if(e instanceof api.ApiError&&e.status===429)setCooldown(e.retryAfter||60);pw.current?.focus();}finally{setBusy(false);}
 }
 async function resend(){if(busy||cooldown>0||!email.trim())return;setBusy(true);setError('');try{const out=await api.resendVerification({email});setCooldown(out.resend_after);if(mode==='register')setSuccess(true);}catch(e){setError(errorText(e,lang));if(e instanceof api.ApiError&&e.status===429)setCooldown(e.retryAfter||60);}finally{setBusy(false);}}
 return <section className="card auth"><p className="eyebrow">{t.cabinet}</p><h1>{mode==='register'?t.register:mode==='verify-email'?t.verifyPage:t.login}</h1>
  {success?<div role="status"><p className="notice">{mode==='verify-email'?t.verified:t.mailSent}</p>{mode==='register'?<><p>{t.checkMail}</p><p>{t.challenge}: <code>{challenge}</code></p><a href={link('/verify-email',lang)}>{t.verifyLink}</a></>:<a href={link('/login',lang)}>{t.login}</a>}</div>:null}
  {mode==='verify-email'&&success?null:<form onSubmit={submit} noValidate aria-busy={busy}>
   {mode!=='verify-email'?<label htmlFor="email">{t.email}<input ref={emailInput} id="email" type="email" autoComplete="email" value={email} onChange={e=>setEmail(e.target.value)} required aria-describedby={error?'form-error':undefined}/></label>:null}
   {mode==='verify-email'&&!token?<><p>{t.checkMail}</p><label htmlFor="challenge">{t.challenge}<input id="challenge" value={challenge} onChange={e=>setChallenge(e.target.value)} required aria-describedby={error?'form-error':undefined}/></label><label htmlFor="code">{t.code}<input id="code" inputMode="numeric" autoComplete="one-time-code" value={code} onChange={e=>setCode(e.target.value)} required aria-describedby={error?'form-error':undefined}/></label></>:null}
   {mode!=='register'?<><label htmlFor="password">{t.password}<input ref={pw} id="password" type="password" autoComplete={mode==='login'?'current-password':'new-password'} value={password} onChange={e=>setPassword(e.target.value)} required aria-invalid={!!error} aria-describedby={'password-help'+(error?' form-error':'')}/></label><p id="password-help" className="help">{t.passwordHelp}</p></>:<><label className="check"><input type="checkbox" checked={terms} onChange={e=>setTerms(e.target.checked)}/>{t.terms} <a href={config.termsURL}>{t.termsLink}</a></label><label className="check"><input type="checkbox" checked={privacy} onChange={e=>setPrivacy(e.target.checked)}/>{t.privacy} <a href={config.privacyURL}>{t.privacyLink}</a></label></>}
   {error?<p id="form-error" role="alert" className="error">{error}</p>:null}
   <button className="primary" disabled={busy||cooldown>0}>{busy?t.sending:mode==='register'?t.continue:mode==='verify-email'?t.verify:t.login}</button>
   {cooldown>0?<p role="status" className="help">{t.waiting}: {cooldown} {t.seconds}</p>:null}
  </form>}
  {mode==='register'&&success?<button disabled={busy||cooldown>0} onClick={resend}>{t.resend}</button>:null}
  <nav className="auth-links"><a href={link(mode==='login'?'/register':'/login',lang)}>{mode==='login'?t.register:t.login}</a><a href={config.supportURL}>{t.support}</a></nav>
 </section>;
}
