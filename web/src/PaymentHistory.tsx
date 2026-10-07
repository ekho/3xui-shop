import {useEffect,useId,useRef,useState,type ReactNode} from 'react';
import {useLocation,useNavigate} from 'react-router-dom';
import {PaymentCase} from './PaymentCase';
import * as api from './api/client';
import {displayPrice,type Currency} from './catalogueMoney';
import {text,link,loginRedirect,errorText,type Lang} from './i18n';

type Kind=api.PaymentHistoryInput['kind'];
type View={key:string;page?:api.PaymentHistoryPage;busy:boolean;error?:string;denied?:boolean;append?:boolean};
type Quote=api.PaymentHistoryPage['orders'][number]['quote']|NonNullable<api.PaymentHistoryPage['legacy_transactions'][number]['quote']>;

export function PaymentHistory({lang,clientId,onDenied,onChanged}:{lang:Lang;clientId?:string;onDenied?:()=>void;onChanged?:()=>void}){
 const t=text(lang),id=useId(),route=useLocation(),navigate=useNavigate(),params=new URLSearchParams(route.search);const[kind,setKind]=useState<Kind>(!clientId&&params.get('kind')==='legacy'?'legacy':'orders');
 const candidate=params.get('legacy_source_id');const source=!clientId&&kind==='legacy'&&params.get('kind')==='legacy'&&params.getAll('legacy_source_id').length===1&&candidate&&/^[1-9][0-9]{0,18}$/.test(candidate)&&BigInt(candidate)<=9223372036854775807n?candidate:undefined;
 const key=(clientId??'self')+'/'+kind+'/'+lang+'/'+(source??'all');
 function clearFilter(next:Kind=kind){params.delete('legacy_source_id');params.set('kind',next);navigate({pathname:route.pathname,search:'?'+params.toString(),hash:route.hash},{replace:true});}
 const[view,setView]=useState<View>({key,busy:true});const request=useRef<AbortController|undefined>(undefined);
 const[selection,setSelection]=useState<{key:string;orderId:string;receiptId?:string}>();const opener=useRef<HTMLButtonElement|null>(null),heading=useRef<HTMLHeadingElement>(null);
 function openCase(button:HTMLButtonElement,orderId:string,receiptId?:string){opener.current=button;setSelection({key,orderId,receiptId});}
 function closeCase(){setSelection(undefined);(opener.current?.isConnected?opener.current:heading.current)?.focus();}
 const current:View=view.key===key?view:{key,busy:true};const page=current.page;
 async function load(append=false){
  if(append&&source)return;
  if(append&&(!page?.has_more||current.busy))return;
  const input:api.PaymentHistoryInput={kind};
  if(source)input.legacy_source_id=source;
  if(append&&page){const row=kind==='orders'?page.orders.at(-1):kind==='receipts'?page.receipts.at(-1):kind==='refunds'?page.refunds?.at(-1):page.legacy_transactions.at(-1);if(!row)return;input.before_created_at=row.created_at;input.before_id='operation_id' in row?row.operation_id:'source_id' in row?row.source_id:'refund_id' in row?row.refund_id:row.order_id;}
  request.current?.abort();const c=new AbortController();request.current=c;const previous=page;
  setView({key,busy:true,page:previous});
  try{
   if(!clientId&&!append)await api.getSessionContext(c.signal);
   const out=await(clientId?api.getOperatorPaymentHistory(clientId,input,c.signal):api.getPaymentHistory(input,c.signal));
   if(c.signal.aborted||request.current!==c)return;
   if(out.kind!==kind||typeof out.has_more!=='boolean'||!Array.isArray(out.orders)||!Array.isArray(out.receipts)||!Array.isArray(out.legacy_transactions)||kind==='refunds'&&!Array.isArray(out.refunds))throw new api.ApiError(503,'SERVICE_UNAVAILABLE','');
   setView({key,busy:false,page:append&&previous?{...out,refunds:[...(previous.refunds??[]),...(out.refunds??[])],orders:[...previous.orders,...out.orders],receipts:[...previous.receipts,...out.receipts],legacy_transactions:[...previous.legacy_transactions,...out.legacy_transactions]}:out});
  }catch(error){
   if(c.signal.aborted||request.current!==c)return;
   const denied=error instanceof api.ApiError&&(error.status===401||error.status===403);
   setView({key,busy:false,page:denied?undefined:previous,error:errorText(error,lang),denied,append});
   if(denied){if(clientId)onDenied?.();else if(error instanceof api.ApiError&&error.status===401)loginRedirect(lang);}
  }
 }
 useEffect(()=>{if(!clientId)setKind(params.get('kind')==='legacy'?'legacy':'orders');},[route.search,clientId]);
 useEffect(()=>{setSelection(undefined);void load();return()=>request.current?.abort();},[clientId,kind,lang,source]);
 const field=(name:string,value:ReactNode)=><div key={name}><dt>{name}</dt><dd>{value}</dd></div>;
 const date=(value:string)=><time dateTime={value}>{new Date(value).toLocaleString(lang==='ru'?'ru-RU':'en-US')}</time>;
 const money=(value:string,currency:Currency|null)=>currency?displayPrice(value,currency,lang):value+' '+t.historyMinorUnits;
 const yes=(value:boolean)=>value?t.historyYes:t.historyNo;
 const methods={yoomoney:'YooMoney',manual:t.manualPayment,yookassa:t.kassaPayment,cryptomus:t.cryptoPayment,heleket:t.heleketPayment,telegram_stars:'Telegram Stars'};
 const actions={purchase:t.purchasePurpose,renew:t.renewalPurpose,change_plan:t.changePlanPurpose};
 const paid={pending:t.historyPending,paid:t.historyPaid,canceled:t.historyCanceled};
 const fulfilled={not_started:t.historyNotStarted,queued:t.historyQueued,running:t.historyRunning,applied:t.historyApplied,needs_review:t.historyReview};
 const oldStatus={pending:t.historyPending,completed:t.historyCompleted,canceled:t.historyCanceled,refunded:t.historyRefunded};
 const terms=(quote:Quote)=><>{field(t.orderPrice,money(quote.amount_minor,quote.currency))}{field(t.devices,quote.devices)}{field(t.period,quote.period_days)}{field(t.trafficGb,String(quote.traffic_gb)==='0'?t.unlimited:quote.traffic_gb)}{'plan_id' in quote?<>{field(t.historyPlanID,<code>{quote.plan_id}</code>)}{field(t.historyRevision,quote.revision)}{field(t.accessProfile,quote.profile==='euru'?'EURU':t.profileRegular)}{quote.source_access_operation_id?field(t.accessOperationId,<code>{quote.source_access_operation_id}</code>):null}</>:null}</>;
 const count=page?(kind==='orders'?page.orders.length:kind==='receipts'?page.receipts.length:kind==='refunds'?(page.refunds?.length??0):page.legacy_transactions.length):0;
 return <section className={clientId?'payment-history':'card payment-history'} aria-labelledby={id+'-title'} aria-busy={current.busy}>
  {clientId?<h2 id={id+'-title'} ref={heading} tabIndex={-1}>{t.paymentHistory}</h2>:<><h1 id={id+'-title'} ref={heading} tabIndex={-1}>{t.paymentHistory}</h1><p><a href={link('/cabinet',lang)}>{t.cabinet}</a></p></>}
  <div className="catalogue-selectors"><label htmlFor={id+'-kind'}>{t.historyKind}<select id={id+'-kind'} value={kind} disabled={current.denied} onChange={event=>{const next=event.target.value as Kind;setKind(next);if(params.has('legacy_source_id'))clearFilter(next);}}><option value="orders">{t.historyOrders}</option><option value="receipts">{t.historyReceipts}</option><option value="refunds">{t.historyRefunds}</option><option value="legacy">{t.historyLegacy}</option></select></label><button disabled={current.busy||current.denied} onClick={()=>void load()}>{t.historyRefresh}</button></div>
  {source?<p>{t.historyOpenedRecord}: <code>{source}</code> <button disabled={current.denied} onClick={()=>clearFilter()}>{t.historyFullArchive}</button></p>:null}
  <p className="help">{t.historyHelp}</p>
  {current.busy?<p role="status">{t.loading}</p>:null}
  {current.error?<div className="error" role="alert"><p>{current.error}</p>{!current.denied?<button disabled={current.busy} onClick={()=>void load(current.append)}>{t.retry}</button>:null}</div>:null}
  {page&&!count&&!current.busy&&!current.error?<p role="status">{t.historyEmpty}</p>:null}
  <ul className="support-list">
   {page&&kind==='orders'?page.orders.map(row=><li key={row.order_id}><article className="admin-client"><h3>{t.orderId}: <code>{row.order_id}</code></h3><dl className="stats-grid">{field(t.orderPurpose,actions[row.action])}{field(t.paymentChoice,methods[row.payment_method]+(row.payment_method==='yoomoney'?' · '+(row.payment_type==='PC'?t.walletPayment:t.cardPayment):''))}{terms(row.quote)}{field(t.historyPaymentStatus,paid[row.payment_status])}{field(t.historyFulfillmentStatus,fulfilled[row.fulfillment_status])}{field(t.historyReviewRequired,yes(row.review_required))}{row.review_reason?field(t.historyReviewReason,row.review_reason):null}{row.access_operation_id?field(t.accessOperationId,<code>{row.access_operation_id}</code>):null}{field(t.created,date(row.created_at))}{field(t.historyDeadline,date(row.expires_at))}</dl>{clientId?<button onClick={event=>openCase(event.currentTarget,row.order_id)}>{t.caseOpen}</button>:<a href={link('/orders/'+row.order_id,lang)}>{t.historyOpenOrder}</a>}</article></li>):null}
   {page&&kind==='receipts'?page.receipts.map(row=><li key={row.operation_id}><article className="admin-client"><h3>{t.historyReceiptID}: <code>{row.operation_id}</code></h3><dl className="stats-grid">{field(t.orderId,<code>{row.order_id}</code>)}{field(t.paymentChoice,methods[row.payment_method])}{field(t.historyGross,money(row.gross_minor,row.currency))}{field(t.historyNet,row.net_minor===null?t.unknown:money(row.net_minor,row.currency))}{field(t.currency,row.currency??t.unknown+' ('+row.raw_currency+')')}{field(t.source,row.source==='operator'?t.historyOperator:t.historyProvider)}{field(t.historyFundsOrder,yes(row.funds_order))}{field(t.historyReviewRequired,yes(row.review_required))}{row.review_reason?field(t.historyReviewReason,row.review_reason):null}{field(t.created,date(row.created_at))}{field(t.historyOccurred,date(row.occurred_at))}{row.crypto_amounts?<>{field(t.historyCryptoPayment,row.crypto_amounts.payment_amount)}{field(t.historyCryptoPayer,row.crypto_amounts.payer_amount)}{field(t.historyCryptoMerchant,row.crypto_amounts.merchant_amount)}{field(t.historyCryptoCurrency,row.crypto_amounts.payer_currency)}</>:null}</dl>{row.codepro?<p className="warning">{t.historyProtected}</p>:null}{row.unaccepted?<p className="warning">{t.historyUnaccepted}</p>:null}{clientId?<button onClick={event=>openCase(event.currentTarget,row.order_id,row.operation_id)}>{t.caseOpen}</button>:null}</article></li>):null}
   {page&&kind==='refunds'?page.refunds?.map(row=><li key={row.refund_id}><article className="admin-client"><h3>{t.historyRefunds}: <code>{row.refund_id}</code></h3><dl className="stats-grid">{field(t.orderId,<code>{row.order_id}</code>)}{field(t.historyReceiptID,<code>{row.receipt_operation_id}</code>)}{field(t.paymentChoice,methods[row.payment_method])}{field(t.historyGross,money(row.receipt_gross_minor,row.receipt_currency))}{field(t.refundAmount,row.returned_amount+' '+row.returned_currency)}{field(t.source,t.historyOperator)}{field(t.refundReference,row.reference)}{field(t.reason,row.reason)}{field(t.created,date(row.created_at))}</dl>{clientId?<button onClick={event=>openCase(event.currentTarget,row.order_id,row.receipt_operation_id)}>{t.caseOpen}</button>:null}</article></li>):null}
   {page&&kind==='legacy'?page.legacy_transactions.map(row=><li key={row.source_id}><article className="admin-client"><h3>{t.historyLegacyID}: <code>{row.source_id}</code></h3><dl className="stats-grid">{field(t.historyPaymentStatus,oldStatus[row.payment_status])}{field(t.historyFulfillmentStatus,t.historyUnknownAccess)}{field(t.paymentChoice,row.payment_method?methods[row.payment_method]:t.unknown)}{row.quote?<>{field(t.orderPurpose,actions[row.quote.action])}{terms(row.quote)}</>:field(t.historyTerms,t.unknown)}{field(t.created,date(row.created_at))}{field(t.historyUpdated,date(row.updated_at))}</dl></article></li>):null}
  </ul>
  {page?.has_more&&!source?<button disabled={current.busy} onClick={()=>void load(true)}>{t.historyMore}</button>:null}
  {clientId&&selection?.key===key&&!current.denied?<PaymentCase key={JSON.stringify([key,selection.orderId,selection.receiptId??null])} clientId={clientId} orderId={selection.orderId} receiptId={selection.receiptId} lang={lang} onDenied={()=>onDenied?.()} onChanged={()=>{void load();onChanged?.();}} onClose={closeCase}/>:null}
 </section>;
}
