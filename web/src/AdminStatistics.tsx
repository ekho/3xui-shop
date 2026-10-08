import {useEffect,useRef,useState} from 'react';
import * as api from './api/client';
import {displayPrice} from './catalogueMoney';
import {text,errorText,type Lang} from './i18n';

export function AdminStatistics({lang,campaignId,onForbidden}:{lang:Lang;campaignId?:string;onForbidden:()=>void}){
 const t=text(lang),scope=campaignId??null;
 const[result,setResult]=useState<{scope:string|null;lang:Lang;report?:api.StatisticsReport;error?:unknown}>(),[reload,setReload]=useState(0),[busy,setBusy]=useState(true);
 const errorRef=useRef<HTMLDivElement>(null);
 const current=result?.scope===scope&&result.lang===lang?result:undefined,report=current?.report,error=current?.error;
 useEffect(()=>{
  const controller=new AbortController();setResult(undefined);setBusy(true);
  void api.getOperatorStatistics(campaignId,controller.signal).then(value=>{
   if(controller.signal.aborted)return;
   if(value.version!=='2026-10-08-s26-statistics-v1'||value.campaign_id!==scope)throw new api.ApiError(503,'SERVICE_UNAVAILABLE','');
   setResult({scope,lang,report:value});
  }).catch(failure=>{
   if(controller.signal.aborted)return;
   if(failure instanceof api.ApiError&&(failure.status===401||failure.status===403))onForbidden();else setResult({scope,lang,error:failure});
  }).finally(()=>{if(!controller.signal.aborted)setBusy(false);});
  return()=>controller.abort();
 },[scope,lang,reload]);
 useEffect(()=>{if(error!==undefined)errorRef.current?.focus();},[error]);
 const count=(value:number|null)=>value===null?t.unknown:new Intl.NumberFormat(lang).format(value);
 const percentage=(value:string|null)=>value===null?t.unknown:(lang==='ru'?value.replace('.',','):value)+'%';
 const date=(value:string)=><time dateTime={value}>{new Date(value).toLocaleString(lang)}</time>;
 const Title=campaignId?'h3':'h1',Heading=campaignId?'h4':'h2',Subheading=campaignId?'h5':'h3';
 const names={banned:t.profileBanned,regular:t.profileRegular,unlimited:t.unlimited,euru:'EURU'};
 return <section className={campaignId?'payment-history':'admin-page payment-history'} aria-label={t.statistics} aria-busy={busy}>
  <Title>{t.statistics}</Title><p className="help">{campaignId?t.statisticsCampaignScope:t.statisticsGlobalScope}</p>
  <button type="button" disabled={busy} onClick={()=>setReload(value=>value+1)}>{t.statisticsRefresh}</button>
  {error!==undefined?<div ref={errorRef} role="alert" tabIndex={-1} className="error"><p>{errorText(error,lang)}</p><button type="button" onClick={()=>setReload(value=>value+1)}>{t.retry}</button></div>:!report?<p role="status">{t.loading}</p>:<>
   {report.users===0?<p>{t.statisticsEmpty}</p>:null}
   <p className="help">{t.statisticsAccountsObserved}: {date(report.database_observed_at)}</p>
   <dl className="stats-grid"><div><dt>{t.statisticsAccounts}</dt><dd>{count(report.users)}</dd></div><div><dt>{t.campaignTrials}</dt><dd>{count(report.trials.trial_users)}</dd></div><div><dt>{t.campaignPaidOrders}</dt><dd>{count(report.payments.paid_orders)}</dd></div><div><dt>{t.campaignPaidUsers}</dt><dd>{count(report.payments.paid_users)}</dd></div><div><dt>{t.campaignRepeatUsers}</dt><dd>{count(report.payments.repeat_users)}</dd></div></dl>
   <Heading>{t.statisticsConversions}</Heading><p className="help">{t.statisticsConversionHelp}</p>
   <dl className="stats-grid"><div><dt>{t.statisticsTrialPercent}</dt><dd>{percentage(report.conversions.trial_percent)}</dd></div><div><dt>{t.statisticsPaidPercent}</dt><dd>{percentage(report.conversions.paid_percent)}</dd></div><div><dt>{t.statisticsRepeatPercent}</dt><dd>{percentage(report.conversions.repeat_percent)}</dd></div></dl>
   <Heading>{t.statisticsActivity}</Heading><p className="help">{t.statisticsPanelObserved}: {date(report.activity.observed_at)}</p>
   {report.activity.unknown_users>0?<p className="warning" role="status">{t.statisticsPartial}</p>:null}
   <dl className="stats-grid"><div><dt>{t.statisticsActive}</dt><dd>{count(report.activity.active_users)}</dd></div><div><dt>{t.statisticsKnownActive}</dt><dd>{count(report.activity.known_active_users)}</dd></div><div><dt>{t.statisticsKnownInactive}</dt><dd>{count(report.activity.known_inactive_users)}</dd></div><div><dt>{t.statisticsUnknownActivity}</dt><dd>{count(report.activity.unknown_users)}</dd></div></dl>
   <Heading>{t.campaignNativeMoney}</Heading>{!report.payments.money.length?<p>{t.campaignNoMoney}</p>:report.payments.money.map(money=><dl className="stats-grid" key={money.currency}><div><dt>{t.campaignGross}</dt><dd>{displayPrice(money.gross_minor,money.currency,lang)}</dd></div><div><dt>{t.campaignKnownNet}</dt><dd>{displayPrice(money.known_net_minor,money.currency,lang)}</dd></div><div><dt>{t.campaignUnknownNet}</dt><dd>{count(money.unknown_net_receipts)}</dd></div></dl>)}
   <Heading>{t.campaignRefunds}</Heading>{!report.payments.refunds.length?<p>{t.campaignNoMoney}</p>:report.payments.refunds.map(item=><p key={item.currency}>{item.returned_amount} {item.currency}</p>)}
   <Heading>{t.campaignArchive}</Heading><p className="help">{t.campaignArchiveHelp}</p>
   <dl className="stats-grid"><div><dt>{t.campaignPaidOrders}</dt><dd>{count(report.payments.legacy.completed_transactions)}</dd></div><div><dt>{t.campaignPaidUsers}</dt><dd>{count(report.payments.legacy.paid_users)}</dd></div><div><dt>{t.campaignRepeatUsers}</dt><dd>{count(report.payments.legacy.repeat_users)}</dd></div><div><dt>{t.campaignUnknownQuotes}</dt><dd>{count(report.payments.legacy.unknown_quote_count)}</dd></div></dl>
   {report.payments.legacy.money.map(item=><p key={item.currency}>{t.campaignQuoted}: {displayPrice(item.quoted_minor,item.currency,lang)}</p>)}
   <Heading>{t.statisticsGroups}</Heading><p className="help">{t.statisticsGroupScope}</p><p className="help">{t.statisticsArchivedPlans}</p>
   {report.groups.map(group=><article key={group.name} className="admin-client"><Subheading>{names[group.name]}</Subheading><dl className="stats-grid"><div><dt>{t.statisticsUserReferences}</dt><dd>{count(group.user_references)}</dd></div><div><dt>{t.statisticsPlanReferences}</dt><dd>{count(group.plan_references)}</dd></div><div><dt>{t.statisticsInboundReferences}</dt><dd>{count(group.inbound_references)}</dd></div><div><dt>{t.statisticsEnabledInbounds}</dt><dd>{count(group.enabled_inbound_references)}</dd></div></dl></article>)}
   <p>{t.statisticsUnknownProfiles}: {count(report.unknown_user_profiles)}</p>
   <Heading>{t.statisticsServers}</Heading><p className="help">{t.statisticsInfrastructureScope}</p>
   {report.servers.map(server=><article key={server.panel_id} className="admin-client"><Subheading>{server.panel_id}</Subheading><p className="help">{t.statisticsPanelObserved}: {date(server.observed_at)}</p>{server.availability==='unavailable'?<p className="warning" role="status">{t.statisticsPanelUnavailable}</p>:null}<dl className="stats-grid"><div><dt>{t.statisticsPanelClients}</dt><dd>{count(server.clients)}</dd></div><div><dt>{t.statisticsInboundReferences}</dt><dd>{count(server.inbounds)}</dd></div><div><dt>{t.statisticsEnabledInbounds}</dt><dd>{count(server.enabled_inbounds)}</dd></div></dl></article>)}
  </>}
 </section>;
}
