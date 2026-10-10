import {useEffect,useRef,useState,type FormEvent} from 'react';
import * as api from './api/client';
import {text,errorText,type Lang} from './i18n';

export function AdminPromocodes({lang,onForbidden}:{lang:Lang;onForbidden:()=>void}){
 const t=text(lang);const[page,setPage]=useState(1),[reload,setReload]=useState(0),[list,setList]=useState<api.PromocodeList>(),[listError,setListError]=useState<unknown>();
 const[selected,setSelected]=useState(''),[detailReload,setDetailReload]=useState(0),[detail,setDetail]=useState<api.PromocodeDetail>(),[detailError,setDetailError]=useState<unknown>();
 const[creating,setCreating]=useState(false),[days,setDays]=useState(''),[reason,setReason]=useState(''),[error,setError]=useState<unknown>(),[notice,setNotice]=useState(''),[busy,setBusy]=useState(false),[conflict,setConflict]=useState(false),[confirmDelete,setConfirmDelete]=useState(false);
 const attempt=useRef<{signature:string;key:string}|undefined>(undefined),writeRequest=useRef<AbortController|undefined>(undefined),errorRef=useRef<HTMLParagraphElement>(null),listErrorRef=useRef<HTMLDivElement>(null),detailErrorRef=useRef<HTMLDivElement>(null);
 const forbidden=(failure:unknown)=>failure instanceof api.ApiError&&(failure.status===401||failure.status===403);
 function message(failure:unknown){if(failure==='invalid')return t.promocodeInvalid;if(failure instanceof api.ApiError){if(failure.code==='PROMOCODE_REVISION_CONFLICT')return t.promocodeConflict;if(failure.code==='PROMOCODE_USED')return t.promocodeUsed;if(failure.code==='PROMOCODE_DELETED')return t.promocodeDeletedError;if(failure.code==='IDEMPOTENCY_CONFLICT')return t.catalogueIdempotencyConflict;}return errorText(failure,lang);}
 useEffect(()=>{const controller=new AbortController();setList(undefined);setListError(undefined);void api.getOperatorPromocodes(page,controller.signal).then(result=>{if(!controller.signal.aborted)setList(result);}).catch(failure=>{if(controller.signal.aborted)return;if(forbidden(failure))onForbidden();else setListError(failure);});return()=>controller.abort();},[page,reload]);
 useEffect(()=>{const controller=new AbortController();setDetail(undefined);setDetailError(undefined);if(selected)void api.getOperatorPromocode(selected,controller.signal).then(result=>{if(controller.signal.aborted)return;if(result.promocode.promocode_id!==selected)throw new api.ApiError(503,'SERVICE_UNAVAILABLE','');setDays(value=>value||String(result.promocode.duration_days));setDetail(result);}).catch(failure=>{if(controller.signal.aborted)return;if(forbidden(failure))onForbidden();else setDetailError(failure);});return()=>controller.abort();},[selected,detailReload]);
 useEffect(()=>()=>writeRequest.current?.abort(),[]);
 useEffect(()=>{if(error)errorRef.current?.focus();},[error]);useEffect(()=>{if(listError)listErrorRef.current?.focus();},[listError]);useEffect(()=>{if(detailError)detailErrorRef.current?.focus();},[detailError]);
 function open(id:string){if(busy)return;if(id===selected&&id)setDetailReload(value=>value+1);setSelected(id);setDetail(undefined);setCreating(false);setDays('');setReason('');setError(undefined);setNotice('');setConflict(false);setConfirmDelete(false);attempt.current=undefined;}
 function create(){open('');setCreating(true);}
 function refresh(){if(busy)return;setDetail(undefined);setConflict(false);setError(undefined);setConfirmDelete(false);attempt.current=undefined;setDetailReload(value=>value+1);}
 function validReason(){const value=reason.trim();if(!value||Array.from(value).length>1000||value.includes('\0'))throw new Error('invalid');return value;}
 function validDays(){const value=days.trim();if(!/^[1-9]\d{0,2}$/.test(value)||Number(value)>365)throw new Error('invalid');return Number(value);}
 async function write(action:'create'|'edit'|'delete'){
  if(busy||conflict||(action!=='create'&&detail?.promocode.state!=='available'))return;
  let input:api.CreatePromocodeInput|api.EditPromocodeInput|api.DeletePromocodeInput;
  try{const why=validReason();input=action==='create'?{duration_days:validDays(),reason:why}:action==='edit'?{duration_days:validDays(),expected_revision:detail!.promocode.revision,reason:why}:{expected_revision:detail!.promocode.revision,reason:why};}catch{setError('invalid');return;}
  const signature=action+selected+JSON.stringify(input),current=attempt.current?.signature===signature?attempt.current:{signature,key:crypto.randomUUID()},controller=new AbortController();attempt.current=current;writeRequest.current=controller;setBusy(true);setError(undefined);setNotice('');
  try{const result=action==='create'?await api.createPromocode(input as api.CreatePromocodeInput,current.key,controller.signal):action==='edit'?await api.editPromocode(selected,input as api.EditPromocodeInput,current.key,controller.signal):await api.deletePromocode(selected,input as api.DeletePromocodeInput,current.key,controller.signal);
   if(controller.signal.aborted)return;
   if(!result||!result.promocode_id||(action!=='create'&&result.promocode_id!==selected))throw new api.ApiError(503,'SERVICE_UNAVAILABLE','');
   attempt.current=undefined;setCreating(false);setSelected(result.promocode_id);setDays('');setConfirmDelete(false);setConflict(false);setNotice(t.promocodeSaved);setReload(value=>value+1);setDetailReload(value=>value+1);
  }catch(failure){if(controller.signal.aborted)return;if(forbidden(failure))onForbidden();else{setError(failure);setConflict(failure instanceof api.ApiError&&['PROMOCODE_REVISION_CONFLICT','PROMOCODE_USED','PROMOCODE_DELETED'].includes(failure.code));setConfirmDelete(false);}}
  finally{if(writeRequest.current===controller){writeRequest.current=undefined;if(!controller.signal.aborted)setBusy(false);}}
 }
 function submit(event:FormEvent){event.preventDefault();void write(creating?'create':'edit');}
 const item=detail?.promocode,states={available:t.promocodeAvailable,activated:t.promocodeActivated,deleted:t.promocodeDeleted};
 const actions:Record<string,string>={create:t.promocodeCreated,edit:t.promocodeEdited,delete:t.promocodeEventDeleted,legacy_import:t.promocodeImported};
 const date=(value:string|null)=>value?new Date(value).toLocaleString(lang==='ru'?'ru-RU':'en-US'):t.unknown;
 return <section className="admin-page admin-catalogue"><h1>{t.promocodes}</h1>{notice?<p role="status" className="notice">{notice}</p>:null}
  {listError?<div ref={listErrorRef} tabIndex={-1} role="alert" className="error"><p>{message(listError)}</p><button disabled={busy} onClick={()=>setReload(value=>value+1)}>{t.retry}</button></div>:!list?<p role="status">{t.loading}</p>:<>
   <button disabled={busy} onClick={create}>{t.newPromocode}</button>{!list.promocodes.length?<p>{t.promocodesEmpty}</p>:<div className="admin-client-list">{list.promocodes.map(code=><article key={code.promocode_id} className="admin-client"><h2>{code.code}</h2><p>{states[code.state]}</p><button disabled={busy} onClick={()=>open(code.promocode_id)}>{t.openPromocode}</button></article>)}</div>}
   <div className="admin-pagination"><button disabled={busy||page<=1} onClick={()=>{open('');setPage(value=>value-1);}}>{t.previousPage}</button><span>{page}</span><button disabled={busy||page*50>=list.total} onClick={()=>{open('');setPage(value=>value+1);}}>{t.nextPage}</button></div>
  </>}
  {selected&&!detail?(detailError?<div ref={detailErrorRef} tabIndex={-1} role="alert" className="error"><p>{message(detailError)}</p><button onClick={refresh}>{t.retry}</button></div>:<p role="status">{t.loading}</p>):null}
  {creating||item?.state==='available'?<form className="admin-action" onSubmit={submit} noValidate aria-busy={busy} aria-describedby={error?'promocode-form-error':undefined}>
   {creating?<h2>{t.newPromocode}</h2>:null}{error?<p ref={errorRef} id="promocode-form-error" tabIndex={-1} role="alert" className="error">{message(error)}</p>:null}{conflict?<button type="button" disabled={busy} onClick={refresh}>{t.promocodeRefresh}</button>:null}
   <fieldset disabled={busy}><label>{t.promocodeDuration}<input inputMode="numeric" value={days} onChange={event=>{setDays(event.target.value);setError(undefined);setConfirmDelete(false);}}/></label><label>{t.reason}<textarea value={reason} onChange={event=>{setReason(event.target.value);setError(undefined);setConfirmDelete(false);}}/></label></fieldset>
   {busy?<p role="status">{t.sending}</p>:null}{creating?<button className="primary" disabled={busy}>{t.promocodeCreate}</button>:<><button className="primary" disabled={busy||conflict}>{t.promocodeSave}</button><button type="button" disabled={busy||conflict} onClick={()=>{try{validReason();setError(undefined);setConfirmDelete(true);}catch{setError('invalid');}}}>{t.promocodeDelete}</button>{confirmDelete?<div className="warning"><p>{t.promocodeDeleteWarning}</p><button type="button" disabled={busy} onClick={()=>void write('delete')}>{t.promocodeConfirmDelete}</button><button type="button" disabled={busy} onClick={()=>setConfirmDelete(false)}>{t.cancelRestriction}</button></div>:null}</>}
  </form>:null}
  {detail&&item?<section className="admin-card payment-history" aria-label={t.promocodeDetails}><h2>{item.code}</h2><p>{states[item.state]}</p><p>{t.revision} {item.revision}</p>{!conflict?<button type="button" disabled={busy} onClick={refresh}>{t.promocodeRefresh}</button>:null}
   <dl className="stats-grid"><div><dt>{t.promocodeDuration}</dt><dd>{item.duration_days}</dd></div><div><dt>{t.created}</dt><dd>{date(item.created_at)}</dd></div><div><dt>{t.promocodeActivatedAt}</dt><dd>{date(item.activated_at)}</dd></div><div><dt>{t.promocodeActivatedAccount}</dt><dd>{item.activated_account_id??t.unknown}</dd></div><div><dt>{t.promocodeActivatedTelegram}</dt><dd>{item.activated_by_tg_id??t.unknown}</dd></div><div><dt>{t.promocodeLegacySource}</dt><dd>{item.legacy_source??t.unknown}</dd></div><div><dt>{t.promocodeLegacyId}</dt><dd>{item.legacy_promocode_id??t.unknown}</dd></div></dl>
   <h3>{t.promocodeEvents}</h3>{detail.events.map(event=><article key={event.event_id} className="admin-client"><p>{date(event.created_at)} · {actions[event.action]??event.action} · {event.actor_account_id??t.unknown}</p><p>{event.reason??t.unknown}</p><p>{event.before?states[event.before.state]+' → ':''}{event.after?states[event.after.state]:t.unknown} · {t.revision} {event.after?.revision??t.unknown}</p></article>)}{detail.events_has_more?<p className="help">{t.promocodeEventsLimited}</p>:null}
  </section>:null}
 </section>;
}
