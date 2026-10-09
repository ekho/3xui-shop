import {useEffect,useRef,useState,type FormEvent} from 'react';
import * as api from './api/client';
import {text,link,errorText,type Lang} from './i18n';

type Kind=api.AuditHistoryInput['kind'];
type Cursor=Pick<api.AuditHistoryInput,'before_created_at'|'before_id'|'before_source_id'>;
const uuid=/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

export function AdminAudit({lang,accountId,onForbidden}:{lang:Lang;accountId?:string;onForbidden:()=>void}){
 const t=text(lang),[kind,setKind]=useState<Kind>('native'),[account,setAccount]=useState(accountId??''),[target,setTarget]=useState('');
 const[filter,setFilter]=useState({account:accountId??'',target:''}),[pages,setPages]=useState<Cursor[]>([{}]),[reload,setReload]=useState(0),[busy,setBusy]=useState(true);
 const input:api.AuditHistoryInput={kind,...(kind!=='system'&&filter.account?{account_id:filter.account}:{}),...(kind==='legacy'&&filter.target?{legacy_target_tg_id:filter.target}:{}),...pages.at(-1)};
 const scope=JSON.stringify({lang,input});
 const[result,setResult]=useState<{scope:string;report?:api.AuditHistory;error?:unknown}>();
 const current=result?.scope===scope?result:undefined,report=current?.report,error=current?.error;
 const request=useRef<AbortController|undefined>(undefined),errorRef=useRef<HTMLDivElement>(null);
 useEffect(()=>{
  const controller=new AbortController();request.current=controller;setResult(undefined);setBusy(true);
  void api.getOperatorAuditHistory(input,controller.signal).then(value=>{
   if(controller.signal.aborted)return;
   if(value.version!=='audit-history-v1'||value.kind!==kind||value.account_id!==(input.account_id??null)||value.legacy_target_tg_id!==(input.legacy_target_tg_id??null)||![value.native_events,value.legacy_events,value.system_events].every(Array.isArray)||typeof value.has_more!=='boolean')throw new api.ApiError(503,'SERVICE_UNAVAILABLE','');
   if(kind!=='native'&&value.native_events.length||kind!=='legacy'&&value.legacy_events.length||kind!=='system'&&value.system_events.length||value.native_events.length+value.legacy_events.length+value.system_events.length>50||input.account_id&&(value.native_events.some(row=>row.account_id!==input.account_id)||value.legacy_events.some(row=>row.account_id!==input.account_id))||input.legacy_target_tg_id&&value.legacy_events.some(row=>row.target_tg_id!==input.legacy_target_tg_id))throw new api.ApiError(503,'SERVICE_UNAVAILABLE','');
   setResult({scope,report:value});
  }).catch(failure=>{
   if(controller.signal.aborted)return;
   if(failure instanceof api.ApiError&&(failure.status===401||failure.status===403))onForbidden();else setResult({scope,error:failure});
  }).finally(()=>{if(!controller.signal.aborted)setBusy(false);});
  return()=>controller.abort();
 },[scope,reload]);
 useEffect(()=>{if(error!==undefined)errorRef.current?.focus();},[error]);
 function source(value:Kind){setKind(value);setFilter({account:accountId??'',target:''});setAccount(accountId??'');setTarget('');setPages([{}]);}
 function apply(event:FormEvent){
  event.preventDefault();const chosen=accountId??account;
  if(chosen&&(!uuid.test(chosen)||chosen==='00000000-0000-0000-0000-000000000000')||kind==='legacy'&&target&&(!/^[1-9][0-9]{0,18}$/.test(target)||BigInt(target)>9223372036854775807n||!!chosen)){
   request.current?.abort();setBusy(false);setResult({scope,error:new api.ApiError(400,'INVALID_INPUT','')});return;
  }
  setFilter({account:kind==='system'?'':chosen,target:kind==='legacy'?target:''});setPages([{}]);setReload(v=>v+1);
 }
 function next(){
  if(!report)return;
  let cursor:Cursor;
  if(kind==='legacy'){const row=report.legacy_events.at(-1);if(!row)return;cursor={before_created_at:row.created_at,before_source_id:row.source_id};}
  else{const row=kind==='native'?report.native_events.at(-1)?.event:report.system_events.at(-1);if(!row)return;cursor={before_created_at:row.created_at,before_id:row.id};}
  setPages(v=>[...v,cursor]);
 }
 function supportSource(source:NonNullable<api.AuditHistory['system_events'][number]['support_telegram']>){
  return <><div><dt>{t.auditSupportKind}</dt><dd>{source.kind}</dd></div><div><dt>{t.auditSupportOutcome}</dt><dd>{source.outcome}</dd></div>
   <div><dt>{t.actor}</dt><dd>{source.actor_account_id??source.actor_tg_id??t.auditUnknown}</dd></div>{source.actor_account_id&&source.actor_tg_id?<div><dt>{t.telegramId}</dt><dd>{source.actor_tg_id}</dd></div>:null}
   {source.target_account_id?<div><dt>{t.auditAccount}</dt><dd>{client(source.target_account_id)}</dd></div>:null}
   {[{label:t.auditBot,id:source.bot_id},{label:t.auditGroup,id:source.group_id},{label:t.auditThread,id:source.thread_id},{label:t.auditTelegramMessage,id:source.message_id},{label:t.auditUpdate,id:source.update_id},{label:t.auditLegacyTicket,id:source.source_id},{label:t.auditReceipt,id:source.receipt_id}].map(ref=>ref.id!==null&&ref.id!==undefined?<div key={ref.label}><dt>{ref.label}</dt><dd>{ref.id}</dd></div>:null)}
   {source.reason?<div><dt>{t.reason}</dt><dd className="notice-body">{source.reason}</dd></div>:null}
  </>;
 }
 const date=(value:string)=><time dateTime={value}>{new Date(value).toLocaleString(lang)}</time>;
 const client=(id:string|null)=>id?<a href={link('/admin/clients/'+id+'/show',lang)}>{id}</a>:t.auditUnlinked;
 const Title=accountId?'h3':'h1',Heading=accountId?'h4':'h2';
 const empty=report&&report.native_events.length+report.legacy_events.length+report.system_events.length===0;
 return <section className={accountId?'payment-history':'admin-page payment-history'} aria-label={t.auditJournal} aria-busy={busy}>
  <Title>{t.auditJournal}</Title><p className="help">{t.auditClientHelp}</p>
  <form className="catalogue-selectors" onSubmit={apply}>
   <label>{t.auditSource}<select aria-label={t.auditSource} value={kind} onChange={e=>source(e.target.value as Kind)}><option value="native">{t.auditNative}</option><option value="legacy">{t.auditLegacy}</option>{!accountId?<option value="system">{t.auditSystem}</option>:null}</select></label>
   {!accountId&&kind!=='system'?<label>{t.auditAccount}<input value={account} maxLength={36} autoComplete="off" onChange={e=>setAccount(e.target.value)}/></label>:null}
   {!accountId&&kind==='legacy'?<label>{t.auditTarget}<input value={target} inputMode="numeric" maxLength={19} autoComplete="off" onChange={e=>setTarget(e.target.value)}/></label>:null}
   {kind!=='system'?<button type="submit">{t.auditApply}</button>:null}
  </form>
  {kind==='legacy'?<p className="help">{t.auditLegacyHelp}</p>:null}
  <button type="button" disabled={busy} onClick={()=>{setPages([{}]);setReload(v=>v+1);}}>{t.auditRefresh}</button>
  {error!==undefined?<div ref={errorRef} role="alert" tabIndex={-1} className="error"><p>{errorText(error,lang)}</p><button type="button" onClick={()=>setReload(v=>v+1)}>{t.retry}</button></div>:!report?<p role="status">{t.loading}</p>:<>
   {empty?<p role="status">{t.auditEmpty}</p>:null}
   <ol className="support-list">
    {report.native_events.map(({account_id,event})=><li key={event.id} className="admin-client"><Heading>{event.action}</Heading>{date(event.created_at)}<dl><div><dt>{t.auditId}</dt><dd>{event.id}</dd></div><div><dt>{t.auditAccount}</dt><dd>{client(account_id)}</dd></div><div><dt>{t.actor}</dt><dd>{event.operator_account_id?t.webOperator+': '+event.operator_account_id:event.operator_tg_id?t.telegramOperator+': '+event.operator_tg_id:event.system_actor===true?t.systemActor:t.auditUnknown}</dd></div><div><dt>{t.reason}</dt><dd className="notice-body">{event.reason??t.auditNoReason}</dd></div>{event.system_actor!==null?<div><dt>{t.auditSystemFlag}</dt><dd>{String(event.system_actor)}</dd></div>:null}{[{label:t.auditRequest,id:event.request_id},{label:t.accessOperationId,id:event.operation_id},{label:t.auditSupport,id:event.support_message_id},{label:t.accessOperations,id:event.access_operation_id},{label:t.monthlyPeriod,id:event.monthly_period}].map(ref=>ref.id!==null?<div key={ref.label}><dt>{ref.label}</dt><dd>{ref.id}</dd></div>:null)}</dl></li>)}
    {report.legacy_events.map(row=><li key={row.source_id} className="admin-client"><Heading>{row.action}</Heading>{date(row.created_at)}<dl><div><dt>{t.auditId}</dt><dd>{row.source_id}</dd></div><div><dt>{t.auditTarget}</dt><dd>{row.target_tg_id??t.auditUnlinked}</dd></div><div><dt>{t.auditAccount}</dt><dd>{client(row.account_id)}</dd></div><div><dt>{t.actor}</dt><dd>{row.actor_type??t.auditUnknown}</dd></div><div><dt>{t.auditActorId}</dt><dd>{row.actor_id??t.auditUnknown}</dd></div><div><dt>{t.displayName}</dt><dd className="notice-body">{row.actor_name??t.auditUnknown}</dd></div><div><dt>{t.source}</dt><dd>{row.source??t.auditUnknown}</dd></div></dl></li>)}
    {report.system_events.map(row=><li key={row.id} className="admin-client"><Heading>{row.support_telegram?t.auditSupportEvent:row.action}</Heading>{date(row.created_at)}<dl><div><dt>{t.auditId}</dt><dd>{row.id}</dd></div>{row.support_telegram?supportSource(row.support_telegram):<>{row.period_day!==null?<div><dt>{t.auditDay}</dt><dd>{row.period_day}</dd></div>:null}{row.cutoff!==null?<div><dt>{t.auditCutoff}</dt><dd>{date(row.cutoff)}</dd></div>:null}{row.retention_days!==null?<div><dt>{t.auditRetention}</dt><dd>{row.retention_days}</dd></div>:null}<div><dt>{t.auditNativeCount}</dt><dd>{row.native_count}</dd></div><div><dt>{t.auditLegacyCount}</dt><dd>{row.legacy_count}</dd></div><div><dt>{t.auditSystemCount}</dt><dd>{row.system_count}</dd></div></>}</dl></li>)}
   </ol>
   <div className="admin-pagination"><button type="button" disabled={busy||pages.length===1} onClick={()=>setPages(v=>v.slice(0,-1))}>{t.previousPage}</button><span>{pages.length}</span><button type="button" disabled={busy||!report.has_more} onClick={next}>{t.nextPage}</button></div>
  </>}
 </section>;
}
