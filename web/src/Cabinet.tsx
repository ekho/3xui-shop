import {useEffect,useRef,useState,type FormEvent} from 'react';
import * as api from './api/client';
import {config} from './config';
import {text,link,errorText,type Lang} from './i18n';
export function Cabinet({lang}:{lang:Lang}){
 const t=text(lang);const[canLogout,setCanLogout]=useState(false);const[account,setAccount]=useState<api.AccountResult>();const[request,setRequest]=useState<api.TrialRequest|null>(null);const[sub,setSub]=useState<api.Subscription>();const[error,setError]=useState('');const[busy,setBusy]=useState(false);const[comment,setComment]=useState('');const[attempt,setAttempt]=useState<{key:string;comment:string}>();const[key,setKey]=useState('');const[copyStatus,setCopyStatus]=useState('');const[revision,setRevision]=useState(0);const controller=useRef<AbortController>(null);const keyField=useRef<HTMLInputElement>(null);
 useEffect(()=>{const c=new AbortController();controller.current=c;let timer:ReturnType<typeof setTimeout>|undefined;
  async function refresh(){if(c.signal.aborted)return;try{const a=await api.getAccount(c.signal);const[r,s]=await Promise.all([api.getCurrentTrialRequest(c.signal),api.getSubscription(c.signal)]);if(c.signal.aborted)return;setAccount(a);setCanLogout(true);setRequest(r.request);setSub(s);setError('');if(!['active','expired'].includes(s.status)&&r.request?.status!=='rejected'&&(r.request||['provisioning','needs_review'].includes(s.status)))timer=setTimeout(refresh,5000);
   }catch(e){if(c.signal.aborted)return;setKey('');if(e instanceof api.ApiError&&e.status===401){location.replace(link('/login',lang));return;}setError(errorText(e,lang));if(e instanceof api.ApiError&&e.code==='ACCOUNT_RESTRICTED'){try{await api.getSessionContext(c.signal);if(!c.signal.aborted)setCanLogout(true);}catch{if(!c.signal.aborted)setCanLogout(false);}}if(!(e instanceof api.ApiError)||e.status===503||e.status===429)timer=setTimeout(refresh,5000);}}
  void refresh();return()=>{c.abort();clearTimeout(timer);};
 },[revision]);
 function handle(e:unknown){if(controller.current?.signal.aborted)return;setError(errorText(e,lang));if(e instanceof api.ApiError&&e.status===401){setKey('');location.replace(link('/login',lang));}}
 async function create(event:FormEvent){event.preventDefault();if(busy)return;if(Array.from(comment).length>1000){setError(t.commentHelp);return;}const current=attempt??{key:crypto.randomUUID(),comment};setAttempt(current);setBusy(true);setError('');try{const out=await api.createTrialRequest({comment:current.comment},current.key,controller.current?.signal);if(controller.current?.signal.aborted)return;setRequest(out);setRevision(r=>r+1);}catch(e){handle(e);}finally{setBusy(false);}}
 async function reveal(){if(busy)return;setBusy(true);try{const out=await api.getSubscriptionKey(controller.current?.signal);if(!controller.current?.signal.aborted){setKey(out.subscription_url);setCopyStatus('');}}catch(e){handle(e);}finally{setBusy(false);}}
 async function copy(){try{await navigator.clipboard.writeText(key);setCopyStatus(t.copied);}catch{setCopyStatus(t.manualCopy);keyField.current?.focus();keyField.current?.select();}}
 async function logout(){setKey('');controller.current?.abort();setBusy(true);try{await api.logoutAccount();location.replace(link('/login',lang));}catch(e){setError(errorText(e,lang));setRevision(r=>r+1);}finally{setBusy(false);}}
 const status=request?.status==='rejected'?'rejected':sub?.status==='none'&&request?.status==='pending'?'pending':sub?.status??'none';
 const descriptions={none:t.none,pending:t.pending,rejected:t.rejected,provisioning:t.provisioning,needs_review:t.needsReview,active:t.active,expired:t.expired};
 const date=(value:string|null)=>value?new Date(value).toLocaleString(lang==='ru'?'ru-RU':'en-US'):t.unknown;
 const bytes=(value:number|null)=>value===null?t.unknown:new Intl.NumberFormat(lang,{maximumFractionDigits:2}).format(value/1024**3)+' GiB';
 return <section className="card"><div className="cabinet-head"><div><p className="eyebrow">{t.cabinet}</p><h1>{account?.account.email??t.loading}</h1></div><button onClick={logout} disabled={!canLogout||busy}>{t.logout}</button></div>
  {error?<div className="error" role="alert"><p>{error}</p><button onClick={()=>setRevision(r=>r+1)} disabled={busy}>{t.retry}</button></div>:null}
  {!sub?<p role="status">{t.loading}</p>:<><p className="notice" role="status">{descriptions[status]}</p>
   {request?.status==='pending'&&status!=='pending'?<p>{t.pending}</p>:null}
   {status==='none'&&!request?(account?.capabilities.trial_available?<form onSubmit={create} aria-busy={busy}><p>{t.trialIntro}</p><label htmlFor="comment">{t.comment}<textarea id="comment" rows={3} value={comment} disabled={!!attempt} onChange={e=>setComment(e.target.value)} aria-describedby="comment-help"/></label><p id="comment-help" className="help">{t.commentHelp}</p><button className="primary" disabled={busy}>{busy?t.sending:t.requestTrial}</button></form>:<p>{t.trialUnavailable}</p>):null}
   {['active','expired','provisioning','needs_review'].includes(status)?<dl><div><dt>{t.devices}</dt><dd>{sub.devices===0?t.unlimited:sub.devices}</dd></div><div><dt>{t.traffic}</dt><dd>{sub.traffic_limit_bytes===0?t.unlimited:bytes(sub.traffic_limit_bytes)}</dd></div><div><dt>{t.used}</dt><dd>{bytes(sub.traffic_used_bytes)}</dd></div><div><dt>{t.expires}</dt><dd>{date(sub.expires_at)}</dd></div></dl>:null}
   {sub.observed_at?<p className="help">{t.observed}: {date(sub.observed_at)}{sub.data_stale?' · '+t.stale:''}</p>:null}
   {status==='active'||status==='expired'?<div className="key-panel">{key?<><label htmlFor="subscription-key">{t.key}<input ref={keyField} id="subscription-key" value={key} readOnly/></label><button onClick={copy}>{t.copy}</button><p role="status">{copyStatus}</p><p>{t.importHelp}</p></>:<button className="primary" onClick={reveal} disabled={busy}>{t.showKey}</button>}</div>:null}
  </>}
  {account?<p><a href={link('/cabinet/security',lang)}>{t.security}</a></p>:null}<a className="support" href={config.supportURL}>{t.support}</a>
 </section>;
}
