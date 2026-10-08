import {useEffect,useRef,useState,type FormEvent} from 'react';
import * as api from './api/client';
import {AdminStatistics} from './AdminStatistics';
import {text,errorText,type Lang} from './i18n';

export function AdminCampaigns({lang,onForbidden}:{lang:Lang;onForbidden:()=>void}){
 const t=text(lang);const[page,setPage]=useState(1),[reload,setReload]=useState(0),[list,setList]=useState<api.CampaignListResult>(),[listError,setListError]=useState<unknown>();
 const[selected,setSelected]=useState(''),[detailReload,setDetailReload]=useState(0),[detail,setDetail]=useState<api.CampaignDetail>(),[detailError,setDetailError]=useState<unknown>();
 const[creating,setCreating]=useState(false),[name,setName]=useState(''),[reason,setReason]=useState(''),[error,setError]=useState<unknown>(),[notice,setNotice]=useState(''),[busy,setBusy]=useState(false),[conflict,setConflict]=useState(false),[confirmDelete,setConfirmDelete]=useState(false);
 const attempt=useRef<{signature:string;key:string}|undefined>(undefined),writeRequest=useRef<AbortController|undefined>(undefined),errorRef=useRef<HTMLParagraphElement>(null),listErrorRef=useRef<HTMLDivElement>(null),detailErrorRef=useRef<HTMLDivElement>(null);
 const forbidden=(failure:unknown)=>failure instanceof api.ApiError&&(failure.status===401||failure.status===403);
 function message(failure:unknown){if(failure==='invalid')return t.campaignInvalid;if(failure instanceof api.ApiError){if(failure.code==='CAMPAIGN_REVISION_CONFLICT')return t.campaignRevisionConflict;if(failure.code==='CAMPAIGN_NAME_CONFLICT')return t.campaignNameConflict;if(failure.code==='IDEMPOTENCY_CONFLICT')return t.catalogueIdempotencyConflict;}return errorText(failure,lang);}
 useEffect(()=>{const controller=new AbortController();setList(undefined);setListError(undefined);void api.getOperatorCampaigns(page,controller.signal).then(result=>{if(!controller.signal.aborted)setList(result);}).catch(failure=>{if(controller.signal.aborted)return;if(forbidden(failure))onForbidden();else setListError(failure);});return()=>controller.abort();},[page,reload]);
 useEffect(()=>{const controller=new AbortController();setDetail(undefined);setDetailError(undefined);if(selected)void api.getOperatorCampaign(selected,controller.signal).then(result=>{if(controller.signal.aborted)return;if(result.campaign.campaign_id!==selected)throw new api.ApiError(503,'SERVICE_UNAVAILABLE','');setDetail(result);}).catch(failure=>{if(controller.signal.aborted)return;if(forbidden(failure))onForbidden();else setDetailError(failure);});return()=>controller.abort();},[selected,detailReload]);
 useEffect(()=>()=>writeRequest.current?.abort(),[]);
 useEffect(()=>{if(error)errorRef.current?.focus();},[error]);useEffect(()=>{if(listError)listErrorRef.current?.focus();},[listError]);useEffect(()=>{if(detailError)detailErrorRef.current?.focus();},[detailError]);
 function open(id:string){if(busy)return;setSelected(id);setDetail(undefined);setCreating(false);setReason('');setError(undefined);setNotice('');setConflict(false);setConfirmDelete(false);attempt.current=undefined;}
 function create(){open('');setCreating(true);setName('');}
 function refresh(){if(busy)return;setConflict(false);setError(undefined);setConfirmDelete(false);attempt.current=undefined;setDetailReload(value=>value+1);}
 function validReason(){const value=reason.trim();if(!value||Array.from(value).length>1000||value.includes('\0'))throw new Error('invalid');return value;}
 async function write(state?:api.Campaign['state']){
  if(busy||conflict||(!creating&&!detail))return;
  let input:api.CampaignCreateInput|api.CampaignStateInput;
  try{const why=validReason();if(creating){const value=name.trim();if(!value||Array.from(value).length>100||value.includes('\0'))throw new Error('invalid');input={name:value,reason:why};}else input={state:state!,expected_revision:detail!.campaign.revision,reason:why};}catch{setError('invalid');return;}
  const target=creating?'':selected,signature=target+JSON.stringify(input),current=attempt.current?.signature===signature?attempt.current:{signature,key:crypto.randomUUID()},controller=new AbortController();attempt.current=current;writeRequest.current=controller;setBusy(true);setError(undefined);setNotice('');
  try{const result=creating?await api.createCampaign(input as api.CampaignCreateInput,current.key,controller.signal):await api.setCampaignState(target,input as api.CampaignStateInput,current.key,controller.signal);
   if(controller.signal.aborted)return;
   if(!result||!Number.isSafeInteger(result.revision)||result.revision<1||!['active','paused','deleted'].includes(result.state)||(!target&&(!/^[0-9a-f-]{36}$/i.test(result.campaign_id)||result.name!==(input as api.CampaignCreateInput).name||result.state!=='active'))||(target&&(result.campaign_id!==target||result.state!==(input as api.CampaignStateInput).state||result.revision<=(input as api.CampaignStateInput).expected_revision)))throw new api.ApiError(503,'SERVICE_UNAVAILABLE','');
   attempt.current=undefined;setCreating(false);setSelected(result.campaign_id);setConfirmDelete(false);setConflict(false);setNotice(t.campaignSaved);setReload(value=>value+1);setDetailReload(value=>value+1);
  }catch(failure){if(controller.signal.aborted)return;if(forbidden(failure))onForbidden();else{setError(failure);setConflict(failure instanceof api.ApiError&&failure.code==='CAMPAIGN_REVISION_CONFLICT');setConfirmDelete(false);}}
  finally{if(writeRequest.current===controller){writeRequest.current=undefined;if(!controller.signal.aborted)setBusy(false);}}
 }
 function submit(event:FormEvent){event.preventDefault();void write();}
 async function copyLink(){if(!detail?.campaign.code)return;const url=new URL('/register',location.origin);url.searchParams.set('invite',detail.campaign.code);if(lang==='en')url.searchParams.set('lang','en');try{await navigator.clipboard.writeText(url.toString());setNotice(t.campaignLinkCopied);}catch(failure){setError(failure);}}
 const campaign=detail?.campaign,stats=detail?.statistics,states={active:t.campaignActive,paused:t.campaignPaused,deleted:t.campaignDeleted};
 const actions:Record<string,string>={create:t.campaignEventCreated,state:t.campaignEventState,legacy_import:t.campaignImportActor};
 const date=(value:string|null)=>value?new Date(value).toLocaleString(lang):t.unknown;
 const count=(value:number)=>new Intl.NumberFormat(lang).format(value);
 return <section className="admin-page admin-catalogue"><h1>{t.campaigns}</h1>{notice?<p role="status" className="notice">{notice}</p>:null}
  {listError?<div ref={listErrorRef} tabIndex={-1} role="alert" className="error"><p>{message(listError)}</p><button disabled={busy} onClick={()=>setReload(value=>value+1)}>{t.retry}</button></div>:!list?<p role="status">{t.loading}</p>:<>
   <button disabled={busy} onClick={create}>{t.newCampaign}</button>{!list.campaigns.length?<p>{t.campaignsEmpty}</p>:<div className="admin-client-list">{list.campaigns.map(item=><article key={item.campaign_id} className="admin-client"><h2>{item.name}</h2><p>{states[item.state]}</p><button disabled={busy} onClick={()=>open(item.campaign_id)}>{t.openCampaign}</button></article>)}</div>}
   <div className="admin-pagination"><button disabled={busy||page<=1} onClick={()=>{open('');setPage(value=>value-1);}}>{t.previousPage}</button><span>{page}</span><button disabled={busy||page*50>=list.total} onClick={()=>{open('');setPage(value=>value+1);}}>{t.nextPage}</button></div>
  </>}
  {selected&&!detail?(detailError?<div ref={detailErrorRef} tabIndex={-1} role="alert" className="error"><p>{message(detailError)}</p><button onClick={refresh}>{t.retry}</button></div>:<p role="status">{t.loading}</p>):null}
  {creating||campaign?<form className="admin-action" onSubmit={submit} noValidate aria-busy={busy} aria-describedby={error?'campaign-form-error':undefined}>
   {creating?<h2>{t.newCampaign}</h2>:null}{error?<p ref={errorRef} id="campaign-form-error" tabIndex={-1} role="alert" className="error">{message(error)}</p>:null}{conflict?<button type="button" disabled={busy} onClick={refresh}>{t.refreshCampaign}</button>:null}
   {creating||campaign?.state!=='deleted'?<fieldset disabled={busy}>{creating?<label>{t.campaignName}<input value={name} onChange={event=>{setName(event.target.value);setError(undefined);}}/></label>:null}<label htmlFor="campaign-reason">{t.reason}</label><textarea id="campaign-reason" value={reason} onChange={event=>{setReason(event.target.value);setError(undefined);setConfirmDelete(false);}}/></fieldset>:null}
   {busy?<p role="status">{t.sending}</p>:null}{creating?<button className="primary" disabled={busy}>{t.createCampaign}</button>:campaign?.state!=='deleted'?<>
    <button type="button" disabled={busy||conflict} onClick={()=>void write(campaign?.state==='active'?'paused':'active')}>{campaign?.state==='active'?t.pauseCampaign:t.enableCampaign}</button>
    <button type="button" disabled={busy||conflict} onClick={()=>{try{validReason();setError(undefined);setConfirmDelete(true);}catch{setError('invalid');}}}>{t.deleteCampaign}</button>
    {confirmDelete?<div className="warning"><p>{t.campaignDeleteWarning}</p><button type="button" disabled={busy} onClick={()=>void write('deleted')}>{t.confirmCampaignDelete}</button><button type="button" disabled={busy} onClick={()=>setConfirmDelete(false)}>{t.cancelRestriction}</button></div>:null}
   </>:null}
  </form>:null}
  {detail&&campaign&&stats?<section className="admin-card payment-history" aria-label={t.campaignDetails}><h2>{campaign.name}</h2><p>{states[campaign.state]}</p><p>{t.revision} {campaign.revision}</p>
   <dl className="stats-grid"><div><dt>{t.created}</dt><dd>{date(campaign.created_at)}</dd></div><div><dt>{t.campaignVisits}</dt><dd>{count(campaign.web_visits)}</dd></div><div><dt>{t.campaignLegacyClicks}</dt><dd>{campaign.legacy_clicks===null?t.unknown:count(campaign.legacy_clicks)}</dd></div><div><dt>{t.campaignWebUsers}</dt><dd>{count(stats.web_registrations)}</dd></div><div><dt>{t.campaignTelegramUsers}</dt><dd>{count(stats.telegram_registrations)}</dd></div></dl>
   <p className="help">{t.campaignVisitsHelp}</p>{campaign.code?<button type="button" onClick={()=>void copyLink()}>{t.copyCampaignLink}</button>:<p>{t.campaignNoLink}</p>}
   <p className="help">{t.campaignLegacyUncertainty}</p><dl className="stats-grid"><div><dt>{t.campaignLegacyNames}</dt><dd>{count(stats.legacy_name_users)}</dd></div><div><dt>{t.campaignLegacyTrials}</dt><dd>{count(stats.legacy_trial_used)}</dd></div></dl>
   <AdminStatistics lang={lang} campaignId={campaign.campaign_id} onForbidden={onForbidden}/>
   <h3>{t.campaignEvents}</h3>{detail.events.map(event=><article key={event.event_id} className="admin-client"><p>{date(event.created_at)} · {actions[event.action]??t.unknown} · {event.actor_account_id??t.campaignImportActor}</p><p>{event.reason??t.unknown}</p><p>{event.before?states[event.before.state]+' → ':''}{states[event.after.state]} · {t.revision} {event.after.revision}</p></article>)}{detail.events_has_more?<p className="help">{t.campaignEventsLimited}</p>:null}
  </section>:null}
 </section>;
}
