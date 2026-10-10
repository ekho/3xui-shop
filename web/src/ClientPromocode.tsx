import {useEffect,useRef,useState,type FormEvent} from 'react';
import * as api from './api/client';
import {errorText,loginRedirect,text,type Lang} from './i18n';

export function ClientPromocode({lang}:{lang:Lang}){
 const t=text(lang),[code,setCode]=useState(''),[busy,setBusy]=useState(false),[error,setError]=useState(''),[activation,setActivation]=useState<api.PromocodeActivation>();
 const attempt=useRef<{key:string;code:string}|undefined>(undefined),errorRef=useRef<HTMLParagraphElement>(null),controller=useRef(new AbortController());
 useEffect(()=>()=>controller.current.abort(),[]);
 useEffect(()=>{if(error)errorRef.current?.focus();},[error]);
 function showError(reason:unknown){
  if(controller.current.signal.aborted)return;
  if(reason instanceof api.ApiError&&reason.status===401){loginRedirect(lang);return;}
  const kind=reason instanceof api.ApiError?reason.code:'';
  setError(({PROMOCODE_INVALID:t.clientPromoInvalid,PROMOCODE_USED:t.clientPromoUsed,ACCESS_NOT_ELIGIBLE:t.clientPromoIneligible,ACCESS_OPERATION_CONFLICT:t.clientPromoConflict,IDEMPOTENCY_CONFLICT:t.clientPromoIdempotency,SERVICE_UNAVAILABLE:t.clientPromoUnavailable} as Record<string,string>)[kind]??errorText(reason,lang));
 }
 async function submit(event:FormEvent){
  event.preventDefault();if(busy)return;
  const trimmed=code.trim();if(!trimmed){setError(t.clientPromoRequired);return;}
  const current=attempt.current??{key:crypto.randomUUID(),code:trimmed};attempt.current=current;
  setBusy(true);setError('');
  try{const result=await api.activatePromocode(current.code,current.key,controller.current.signal);if(controller.current.signal.aborted)return;setActivation(result);setCode('');attempt.current=undefined;}
  catch(reason){if(!(reason instanceof api.ApiError&&reason.status===503))attempt.current=undefined;showError(reason);}
  finally{if(!controller.current.signal.aborted)setBusy(false);}
 }
 async function refresh(){
  if(busy||!activation)return;setBusy(true);setError('');
  try{const result=await api.getPromocodeActivation(activation.operation_id,controller.current.signal);if(!controller.current.signal.aborted)setActivation(result);}
  catch(reason){showError(reason);}
  finally{if(!controller.current.signal.aborted)setBusy(false);}
 }
 const statuses={pending:t.clientPromoPending,provisioning:t.clientPromoProvisioning,applied:t.clientPromoApplied,needs_review:t.clientPromoReview};
 return <section className="key-panel" role="region" aria-label={t.clientPromoTitle} aria-busy={busy}>
  <h2>{t.clientPromoTitle}</h2><p className="help">{t.clientPromoHelp}</p>
  <form onSubmit={submit}><label htmlFor="client-promocode">{t.clientPromoLabel}<input id="client-promocode" name="promocode" autoComplete="off" autoCapitalize="none" spellCheck={false} value={code} disabled={busy} onChange={event=>{setCode(event.target.value);attempt.current=undefined;setError('');}}/></label>
   <button className="primary" disabled={busy}>{busy?t.sending:t.clientPromoSubmit}</button>
  </form>
  {error?<p className="error" role="alert" tabIndex={-1} ref={errorRef}>{error}</p>:null}
  {activation?<><p role="status">{statuses[activation.status]} · {t.clientPromoDays.replace('{days}',String(activation.duration_days))}</p><button type="button" disabled={busy} onClick={()=>void refresh()}>{t.clientPromoRefresh}</button></>:<p className="help">{t.clientPromoEmpty}</p>}
 </section>;
}
