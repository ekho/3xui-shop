import {useEffect,useRef,useState,type FormEvent} from 'react';
import * as api from './api/client';
import {displayPrice,majorToMinor,minorToMajor,type Currency} from './catalogueMoney';
import {text,errorText,type Lang} from './i18n';

type Row={days:string;RUB:string;USD:string;XTR:string};
type Draft={devices:string;traffic:string;profile:api.CatalogueTerms['profile'];hidden:boolean;rows:Row[];reason:string};
type Mode='create'|'edit'|'';
const currencies=['RUB','USD','XTR'] as const;
const emptyRow=():Row=>({days:'',RUB:'',USD:'',XTR:''});
const emptyDraft=():Draft=>({devices:'',traffic:'',profile:'regular',hidden:false,rows:[emptyRow()],reason:''});

function fromPlan(plan:api.OperatorCataloguePlan):Draft{
 return {devices:String(plan.devices),traffic:String(plan.traffic_gb),profile:plan.profile,hidden:plan.hidden,reason:'',rows:plan.periods.map(days=>({days:String(days),RUB:price(days,'RUB'),USD:price(days,'USD'),XTR:price(days,'XTR')}))};
 function price(days:number,currency:Currency){const item=plan.prices.find(value=>value.period_days===days&&value.currency===currency);return item?minorToMajor(item.amount_minor,currency):'';}
}

function integer(value:string,min:number,max:number){if(!/^(0|[1-9][0-9]*)$/.test(value))throw new Error('invalid_integer');const parsed=Number(value);if(!Number.isSafeInteger(parsed)||parsed<min||parsed>max)throw new Error('invalid_integer');return parsed;}
function terms(draft:Draft):api.CatalogueTerms{
 const devices=integer(draft.devices,1,10000),traffic_gb=integer(draft.traffic,0,100000);
 if(draft.profile==='unlimited'&&!draft.hidden)throw new Error('invalid_profile');
 const rows=draft.rows.filter(row=>row.days.trim()||currencies.some(currency=>row[currency].trim()));
 if(!draft.hidden&&!rows.length)throw new Error('missing_period');
 if(rows.length>100)throw new Error('too_many_periods');
 const periods:number[]=[],prices:api.CataloguePrice[]=[];
 for(const row of rows){const days=integer(row.days,1,106751);if(periods.includes(days))throw new Error('duplicate_period');periods.push(days);
  const filled=currencies.filter(currency=>row[currency].trim());if(!filled.length&&draft.hidden)continue;
  if(filled.length!==3)throw new Error('incomplete_prices');
  for(const currency of currencies)prices.push({period_days:days,currency,amount_minor:majorToMinor(row[currency],currency)});
 }
 return {devices,traffic_gb,profile:draft.profile,hidden:draft.hidden,periods,prices};
}

export function AdminCatalogue({lang,onForbidden}:{lang:Lang;onForbidden:()=>void}){
 const t=text(lang);const[page,setPage]=useState(1);const[reload,setReload]=useState(0);const[list,setList]=useState<api.OperatorCatalogueResult>({plans:[],total:0,page:1,per_page:50});const[loadState,setLoadState]=useState<'loading'|'ready'|'error'>('loading');const[loadError,setLoadError]=useState('');const[mode,setMode]=useState<Mode>('');const[selected,setSelected]=useState<api.OperatorCataloguePlan>();const[draft,setDraft]=useState<Draft>(emptyDraft);const[error,setError]=useState('');const[notice,setNotice]=useState('');const[busy,setBusy]=useState(false);const[confirmArchive,setConfirmArchive]=useState(false);const[conflict,setConflict]=useState(false);const attempt=useRef<{signature:string;key:string}|undefined>(undefined);const writeRequest=useRef<AbortController|undefined>(undefined);const refreshSelection=useRef<string|undefined>(undefined);const errorRef=useRef<HTMLParagraphElement>(null);
 useEffect(()=>{const controller=new AbortController();setLoadState('loading');void api.getOperatorCatalogue(page,controller.signal).then(result=>{if(controller.signal.aborted)return;setList(result);if(refreshSelection.current){const fresh=result.plans.find(plan=>plan.plan_id===refreshSelection.current);refreshSelection.current=undefined;if(fresh){setSelected(fresh);setDraft(current=>({...fromPlan(fresh),reason:current.reason}));}else{setError(t.catalogueRevisionConflict);setConflict(true);}}setLoadState('ready');setLoadError('');}).catch(reason=>{if(controller.signal.aborted)return;if(reason instanceof api.ApiError&&(reason.status===401||reason.status===403)){onForbidden();return;}setLoadError(errorText(reason,lang));setLoadState('error');});return()=>controller.abort();},[page,reload,lang]);
 useEffect(()=>()=>writeRequest.current?.abort(),[]);useEffect(()=>{if(error)errorRef.current?.focus();},[error]);
 function change(next:Partial<Draft>){setDraft(current=>({...current,...next}));attempt.current=undefined;setError('');setNotice('');setConflict(false);setConfirmArchive(false);}
 function rowChange(index:number,key:keyof Row,value:string){const rows=draft.rows.map((row,at)=>at===index?{...row,[key]:value}:row);change({rows});}
 function create(){if(busy)return;setMode('create');setSelected(undefined);setDraft(emptyDraft());attempt.current=undefined;setError('');setNotice('');setConflict(false);setConfirmArchive(false);}
 function edit(plan:api.OperatorCataloguePlan){if(busy)return;setMode('edit');setSelected(plan);setDraft(fromPlan(plan));attempt.current=undefined;setError('');setNotice('');setConflict(false);setConfirmArchive(false);}
 function safeReason(){const reason=draft.reason.trim();if(!reason||Array.from(reason).length>1000||reason.includes('\0'))throw new Error('invalid_reason');return reason;}
 function message(reason:unknown){if(reason instanceof api.ApiError){if(reason.code==='CATALOGUE_REVISION_CONFLICT')return t.catalogueRevisionConflict;if(reason.code==='CATALOGUE_LAST_VISIBLE')return t.catalogueLastVisible;if(reason.code==='CATALOGUE_DEVICES_CONFLICT')return t.catalogueDevicesConflict;if(reason.code==='IDEMPOTENCY_CONFLICT')return t.catalogueIdempotencyConflict;}return errorText(reason,lang);}
 async function write(action:'create'|'revision'|'archive'){
  if(busy)return;let body:api.CatalogueTerms|undefined,reason:string;try{reason=safeReason();if(action!=='archive')body=terms(draft);}catch{setError(t.catalogueInvalid);return;}
  if(action!=='create'&&!selected)return;
  const input=action==='archive'?{expected_revision:selected!.revision,reason}:action==='revision'?{terms:body!,expected_revision:selected!.revision,reason}:{terms:body!,reason};
  const signature=action+selected?.plan_id+JSON.stringify(input),current=attempt.current?.signature===signature?attempt.current:{signature,key:crypto.randomUUID()};const controller=new AbortController();attempt.current=current;writeRequest.current=controller;setBusy(true);setError('');
  try{if(action==='create')await api.createCataloguePlan(input as componentsCreate,current.key,controller.signal);else if(action==='revision')await api.reviseCataloguePlan(selected!.plan_id,input as componentsRevision,current.key,controller.signal);else await api.archiveCataloguePlan(selected!.plan_id,input as componentsArchive,current.key,controller.signal);
   if(controller.signal.aborted)return;attempt.current=undefined;setMode('');setSelected(undefined);setConfirmArchive(false);setConflict(false);setNotice(action==='archive'?t.catalogueArchived:t.catalogueSaved);setReload(value=>value+1);
  }catch(failure){if(controller.signal.aborted)return;if(failure instanceof api.ApiError&&(failure.status===401||failure.status===403)){onForbidden();return;}setError(message(failure));setConflict(failure instanceof api.ApiError&&failure.code==='CATALOGUE_REVISION_CONFLICT');setConfirmArchive(false);}finally{if(writeRequest.current===controller){writeRequest.current=undefined;if(!controller.signal.aborted)setBusy(false);}}
 }
 function submit(event:FormEvent){event.preventDefault();void write(mode==='create'?'create':'revision');}
 function refresh(){if(conflict&&selected)refreshSelection.current=selected.plan_id;attempt.current=undefined;setConflict(false);setError('');setReload(value=>value+1);}
 return <section className="admin-page admin-catalogue"><h1>{t.plans}</h1>{notice?<p role="status" className="notice">{notice}</p>:null}{loadState==='error'?<div className="error" role="alert"><p>{loadError}</p><button onClick={refresh}>{t.retry}</button></div>:null}{loadState==='loading'?<p role="status">{t.loading}</p>:null}
  {loadState==='ready'?<><button onClick={create} disabled={busy}>{t.newPlan}</button>{list.plans.length===0?<p>{t.plansEmpty}</p>:<div className="admin-client-list">{list.plans.map(plan=><article className="admin-client" key={plan.plan_id}><h2>{plan.devices} {t.devicesCount}</h2><p>{t.revision} {plan.revision} · {plan.profile} · {plan.traffic_gb} GB{plan.hidden?' · '+t.hiddenPlan:''}{plan.archived?' · '+t.archivedPlan:''}</p><p>{plan.periods.map(days=>`${days} ${t.days}: ${currencies.map(currency=>{const price=plan.prices.find(value=>value.period_days===days&&value.currency===currency);return price?displayPrice(price.amount_minor,currency,lang):'—';}).join(' · ')}`).join('; ')}</p><p className="help">{plan.reason??t.unknown} · {plan.actor_account_id??t.unknown}</p>{!plan.archived?<button disabled={busy} onClick={()=>edit(plan)}>{t.editPlan}</button>:null}</article>)}</div>}<div className="admin-pagination"><button disabled={busy||page<=1} onClick={()=>{setMode('');setPage(value=>value-1);}}>{t.previousPage}</button><span>{page}</span><button disabled={busy||page*50>=list.total} onClick={()=>{setMode('');setPage(value=>value+1);}}>{t.nextPage}</button></div></>:null}
  {mode?<form className="admin-action catalogue-form" onSubmit={submit} noValidate aria-busy={busy} aria-describedby={error?'catalogue-form-error':undefined}><h2>{mode==='create'?t.newPlan:t.editPlan}</h2>{selected?<p>{t.revision} {selected.revision}</p>:null}{error?<p id="catalogue-form-error" ref={errorRef} tabIndex={-1} role="alert" className="error">{error}</p>:null}{conflict?<button type="button" onClick={refresh}>{t.reloadCatalogue}</button>:null}{busy?<p role="status">{t.sending}</p>:null}<fieldset disabled={busy||loadState==='loading'}><label>{t.devices}<input type="number" inputMode="numeric" value={draft.devices} onChange={event=>change({devices:event.target.value})}/></label><label>{t.trafficGb}<input type="number" inputMode="numeric" value={draft.traffic} onChange={event=>change({traffic:event.target.value})}/></label><label>{t.accessProfile}<select value={draft.profile} onChange={event=>change({profile:event.target.value as Draft['profile'],hidden:event.target.value==='unlimited'?true:draft.hidden})}><option value="regular">{t.profileRegular}</option><option value="euru">EURU</option><option value="unlimited">{t.unlimited}</option></select></label><label className="check"><input type="checkbox" checked={draft.hidden} disabled={draft.profile==='unlimited'} onChange={event=>change({hidden:event.target.checked})}/>{t.hiddenPlan}</label>{draft.rows.map((row,index)=><fieldset key={index} className="catalogue-period"><legend>{t.period} {index+1}</legend><label>{t.periodDays}<input type="number" inputMode="numeric" value={row.days} onChange={event=>rowChange(index,'days',event.target.value)}/></label>{currencies.map(currency=><label key={currency}>{currency}<input type="text" inputMode="decimal" value={row[currency]} onChange={event=>rowChange(index,currency,event.target.value)}/></label>)}<button type="button" onClick={()=>change({rows:draft.rows.filter((_,at)=>at!==index)})}>{t.removePeriod}</button></fieldset>)}<button type="button" onClick={()=>change({rows:[...draft.rows,emptyRow()]})} disabled={draft.rows.length>=100}>{t.addPeriod}</button><label>{t.reason}<textarea value={draft.reason} onChange={event=>change({reason:event.target.value})}/></label></fieldset><button className="primary" disabled={busy||loadState==='loading'}>{mode==='create'?t.createPlan:t.saveRevision}</button>{mode==='edit'?<button type="button" disabled={busy||loadState==='loading'} onClick={()=>{try{safeReason();setError('');setConfirmArchive(true);}catch{setError(t.catalogueInvalid);}}}>{t.archivePlan}</button>:null}{confirmArchive?<div className="warning"><p>{t.archiveWarning}</p><button type="button" disabled={busy||loadState==='loading'} onClick={()=>void write('archive')}>{t.confirmArchive}</button><button type="button" disabled={busy||loadState==='loading'} onClick={()=>setConfirmArchive(false)}>{t.cancelRestriction}</button></div>:null}</form>:null}
 </section>;
}

type componentsCreate={terms:api.CatalogueTerms;reason:string};
type componentsRevision=componentsCreate&{expected_revision:number};
type componentsArchive={expected_revision:number;reason:string};
