import {useEffect,useRef,useState,type FormEvent} from 'react';
import * as api from './api/client';
import {NoticeBody,noticeNodes,noticeID,noticeInteger,invalidNotice} from './NoticeBody';
import {text,errorText,type Lang} from './i18n';

const messages={
 ru:{title:'Уведомления',personal:'Клиент',all:'Все зарегистрированные',audience:'Получатели',search:'Поиск клиентов',find:'Найти',select:'Выбрать ',selected:'Выбранный клиент',body:'Текст сообщения',reason:'Комментарий операции',limit:'До 4096 символов вместе с разметкой. Комментарий: до 512 символов.',prepare:'Подготовить предпросмотр',preview:'Предпросмотр',send:'Подтвердить отправку',edit:'Подтвердить изменение',delete:'Подтвердить удаление',cancel:'Отменить предпросмотр',last:'Последняя отправка',lastEmpty:'Подтверждённых отправок пока нет.',editLast:'Изменить последнюю отправку',deleteLast:'Удалить последнюю отправку',new:'Новое сообщение',refresh:'Обновить результат',result:'Результат операции',confirmed:'Операция подтверждена.',recipients:'Адресатов',cabinet:'Кабинет',telegram:'Telegram',email:'Email',pending:'Ожидает',succeeded:'Успешно',failed:'Ошибка',skipped:'Пропущено',unknown:'Результат неизвестен',unchanged:'Без изменений',expires:'Действует до',expired:'Предпросмотр истёк. Подготовьте новый.',conflict:'Предпросмотр устарел или заменён. Обновите последнюю отправку и подготовьте новый.',invalid:'Выберите клиента, заполните сообщение и комментарий в указанных пределах.',snapshot:'Адресаты зафиксированы при предпросмотре. Новые регистрации не добавятся.',limits:'Отправленное письмо нельзя изменить или отозвать. Начатая доставка может завершиться после отмены. Telegram можно удалить только при известном ID сообщения и возрасте менее 48 часов.',uncertain:'Неизвестный результат означает, что подтверждение не получено. Он не доказывает отсутствие доставки.',revision:'Версия',withdrawn:'Сообщение отозвано.',change:'Изменение последней отправки',remove:'Удаление последней отправки',emptySearch:'Клиенты не найдены.'},
 en:{title:'Notices',personal:'Client',all:'All registered accounts',audience:'Recipients',search:'Search clients',find:'Find',select:'Select ',selected:'Selected client',body:'Message text',reason:'Operation reason',limit:'Up to 4096 characters including formatting. Reason: up to 512 characters.',prepare:'Prepare preview',preview:'Preview',send:'Confirm send',edit:'Confirm edit',delete:'Confirm deletion',cancel:'Cancel preview',last:'Last notice',lastEmpty:'No confirmed notices yet.',editLast:'Edit last notice',deleteLast:'Delete last notice',new:'New message',refresh:'Refresh result',result:'Operation result',confirmed:'Operation confirmed.',recipients:'Recipients',cabinet:'Cabinet',telegram:'Telegram',email:'Email',pending:'Pending',succeeded:'Succeeded',failed:'Failed',skipped:'Skipped',unknown:'Unknown result',unchanged:'Unchanged',expires:'Valid until',expired:'The preview expired. Prepare a new one.',conflict:'The preview is stale or superseded. Refresh the last notice and prepare a new preview.',invalid:'Select a client and enter a message and reason within the stated limits.',snapshot:'Recipients are fixed at preview time. New registrations will not be added.',limits:'A sent email cannot be edited or recalled. A delivery already started may finish after cancellation. Telegram deletion requires a known message ID and a message younger than 48 hours.',uncertain:'An unknown result means no reliable acknowledgement was received. It does not prove that delivery failed.',revision:'Revision',withdrawn:'The notice was withdrawn.',change:'Editing the last notice',remove:'Deleting the last notice',emptySearch:'No clients found.'}
};
function checkedPreview(p:api.NoticePreview){
 if(!p||!noticeID(p.id)||!noticeID(p.notice_id)||!['send','edit','delete'].includes(p.mode)||!noticeInteger(p.revision,1)||typeof p.text!=='string'||!p.text.trim()||typeof p.reason!=='string'||!p.reason.trim()||Array.from(p.reason).length>512||typeof p.expires_at!=='string'||!Number.isFinite(Date.parse(p.expires_at))||!noticeInteger(p.recipient_count,1)||![p.cabinet_count,p.telegram_count,p.email_count].every(n=>noticeInteger(n)&&n<=p.recipient_count))return invalidNotice();
 noticeNodes(p.html);return p;
}
function checkedBatch(value:api.NoticeBatchResult){
 if(!value||value.version!=='operator-notices-v1'||!noticeInteger(value.revision))return invalidNotice();
 const count=value.notice?checkedPreview(value.notice).recipient_count:0;
 if(value.notice===null?value.revision!==0:value.revision!==value.notice?.revision)return invalidNotice();
 for(const channel of [value.cabinet,value.telegram,value.email]){
  if(!channel||![channel.pending,channel.succeeded,channel.failed,channel.skipped,channel.unknown,channel.unchanged].every(n=>noticeInteger(n))||Object.values(channel).reduce((sum,n)=>sum+n,0)!==count)return invalidNotice();
 }
 return value;
}
function Result({value,lang,title}:{value:api.NoticeBatchResult;lang:Lang;title:string}){
 const m=messages[lang];return <section className="admin-action" aria-label={title}><h2>{title}</h2>{value.notice?<><p>{m.revision} {value.revision} · {m.recipients}: {value.notice.recipient_count}</p><NoticeBody html={value.notice.html} lang={lang}/>{value.notice.mode==='delete'?<p>{m.withdrawn}</p>:null}</>:<p>{m.lastEmpty}</p>}
  <dl className="stats-grid">{(['cabinet','telegram','email'] as const).map(channel=><div key={channel}><dt>{m[channel]}</dt><dd>{(['pending','succeeded','failed','skipped','unknown','unchanged'] as const).map(state=><p key={state}>{m[state]}: {value[channel][state]}</p>)}</dd></div>)}</dl><p className="help">{m.uncertain}</p>
 </section>;
}
export function AdminNotices({lang,onForbidden}:{lang:Lang;onForbidden:()=>void}){
 const t=text(lang),m=messages[lang],initial=new URLSearchParams(location.search).get('account_id')??'';
 const[mode,setMode]=useState<api.NoticePreviewInput['mode']>('send'),[audience,setAudience]=useState<'personal'|'all'>('personal'),[selected,setSelected]=useState(noticeID(initial)?initial:''),[q,setQ]=useState(noticeID(initial)?initial:''),[clients,setClients]=useState<api.OperatorClient[]>([]),[searchPage,setSearchPage]=useState(1),[searchTotal,setSearchTotal]=useState(0),[searched,setSearched]=useState(false),[searchBusy,setSearchBusy]=useState(false);
 const[body,setBody]=useState(''),[reason,setReason]=useState(''),[expectedRevision,setExpectedRevision]=useState(0),[preview,setPreview]=useState<api.NoticePreview>(),[result,setResult]=useState<api.NoticeBatchResult>(),[last,setLast]=useState<api.NoticeBatchResult>(),[lastBusy,setLastBusy]=useState(false),[error,setError]=useState(''),[message,setMessage]=useState(''),[busy,setBusy]=useState(false),[,render]=useState(0);
 const request=useRef<AbortController|null>(null),lastRequest=useRef<AbortController|null>(null),searchRequest=useRef<AbortController|null>(null),epoch=useRef(0),errorRef=useRef<HTMLParagraphElement>(null),previewRef=useRef<HTMLHeadingElement>(null);
 function changed(){epoch.current++;request.current?.abort();lastRequest.current?.abort();searchRequest.current?.abort();setSearchBusy(false);setLastBusy(false);setPreview(undefined);setResult(undefined);setError('');setMessage('');setBusy(false);}
 function failed(failure:unknown,c:AbortController){
  if(c.signal.aborted)return;
  if(failure instanceof api.ApiError&&[401,403].includes(failure.status)){
   request.current?.abort();lastRequest.current?.abort();searchRequest.current?.abort();setPreview(undefined);setResult(undefined);setLast(undefined);setClients([]);setBody('');setReason('');setSelected('');onForbidden();return;
  }
  if(failure instanceof api.ApiError&&failure.status===409){setPreview(undefined);setError(m.conflict);}else setError(errorText(failure,lang));
 }
 async function refreshLast(){
  lastRequest.current?.abort();const c=new AbortController();lastRequest.current=c;setLastBusy(true);
  try{const value=checkedBatch(await api.getLastNotice(c.signal));if(!c.signal.aborted)setLast(value);}catch(failure){failed(failure,c);}finally{if(!c.signal.aborted)setLastBusy(false);}
 }
 async function find(page=1){
  changed();searchRequest.current?.abort();if(Array.from(q).length>256){setError(m.invalid);return;}
  const c=new AbortController();searchRequest.current=c;setSearchBusy(true);setClients([]);setSearched(false);
  try{const out=await api.searchOperatorClients(q,page,c.signal);if(c.signal.aborted)return;
   if(!out||out.page!==page||out.per_page!==50||!Number.isSafeInteger(out.total)||out.total<0||!Array.isArray(out.clients)||out.clients.length>50||out.clients.some(client=>!noticeID(client.account_id)))return invalidNotice();
   setClients(out.clients);setSearchPage(page);setSearchTotal(out.total);setSearched(true);
  }catch(failure){failed(failure,c);}finally{if(!c.signal.aborted)setSearchBusy(false);}
 }
 useEffect(()=>{if(noticeID(initial))void find();void refreshLast();return()=>{request.current?.abort();lastRequest.current?.abort();searchRequest.current?.abort();};},[]);
 useEffect(()=>{if(error)errorRef.current?.focus();},[error]);
 useEffect(()=>{if(!preview)return;previewRef.current?.focus();const timer=setTimeout(()=>render(n=>n+1),Math.min(2147483647,Math.max(0,Date.parse(preview.expires_at)-Date.now())));return()=>clearTimeout(timer);},[preview]);
 async function prepare(event:FormEvent){
  event.preventDefault();if(busy)return;
  if(!reason.trim()||Array.from(reason).length>512||mode!=='delete'&&(!body.trim()||Array.from(body).length>4096)||mode==='send'&&audience==='personal'&&!noticeID(selected)){setError(m.invalid);return;}
  request.current?.abort();const c=new AbortController(),current=epoch.current;request.current=c;setBusy(true);setError('');setMessage('');setPreview(undefined);setResult(undefined);
  const input:api.NoticePreviewInput=mode==='send'?{mode,audience,...(audience==='personal'?{account_id:selected}:{}),body,reason}:{mode,...(mode==='edit'?{body}:{}),expected_revision:expectedRevision,reason};
  try{const p=checkedPreview(await api.previewNotice(input,c.signal));if(c.signal.aborted||current!==epoch.current)return;
   if(p.mode!==mode||p.revision!==(mode==='send'?1:expectedRevision+1)||p.reason!==reason)return invalidNotice();setPreview(p);
  }catch(failure){failed(failure,c);}finally{if(!c.signal.aborted&&current===epoch.current)setBusy(false);}
 }
 async function confirm(){
  if(!preview||busy||Date.parse(preview.expires_at)<=Date.now())return;
  lastRequest.current?.abort();setLastBusy(false);const c=new AbortController(),current=epoch.current,p=preview;request.current=c;setBusy(true);setError('');
  try{const out=checkedBatch(await api.confirmNotice(p.id,c.signal));if(c.signal.aborted||current!==epoch.current)return;
   if(!out.notice||out.notice.id!==p.id||out.notice.notice_id!==p.notice_id||out.notice.html!==p.html||out.notice.mode!==p.mode||out.revision!==p.revision)return invalidNotice();
   setResult(out);setPreview(undefined);setMessage(m.confirmed);void refreshLast();
  }catch(failure){failed(failure,c);}finally{if(!c.signal.aborted&&current===epoch.current)setBusy(false);}
 }
 function changeLast(next:'edit'|'delete'){
  if(!last?.notice||last.notice.mode==='delete')return;changed();setMode(next);setExpectedRevision(last.revision);setBody(next==='edit'?last.notice.html:'');setReason('');
 }
 const expired=!!preview&&Date.parse(preview.expires_at)<=Date.now(),label=(client:api.OperatorClient)=>client.display_name||client.email||client.telegram_id||client.account_id;
 return <section className="admin-page admin-notices"><h1>{m.title}</h1>
  {error?<p ref={errorRef} tabIndex={-1} id="notice-form-error" className="error" role="alert">{error}</p>:null}{message?<p role="status" aria-live="polite">{message}</p>:null}
   {mode==='send'?<><label htmlFor="notice-audience">{m.audience}</label><select id="notice-audience" value={audience} onChange={e=>{changed();setAudience(e.target.value as 'personal'|'all');}}><option value="personal">{m.personal}</option><option value="all">{m.all}</option></select>
   {audience==='personal'?<><form className="admin-search" onSubmit={e=>{e.preventDefault();void find();}} aria-busy={searchBusy}><label>{m.search}<input value={q} onChange={e=>{changed();setQ(e.target.value);setSelected('');setClients([]);setSearched(false);}}/></label><button disabled={searchBusy}>{m.find}</button></form>
    {searchBusy?<p role="status">{t.loading}</p>:null}{searched&&!clients.length?<p>{m.emptySearch}</p>:null}<div className="admin-client-list">{clients.map(client=><article className="admin-client" key={client.account_id}><p>{label(client)}</p><button aria-pressed={selected===client.account_id} onClick={()=>{changed();setSelected(client.account_id);}}>{m.select}{label(client)}</button></article>)}</div>
    {searched?<div className="admin-pagination"><button disabled={searchBusy||searchPage<=1} onClick={()=>void find(searchPage-1)}>{t.previousPage}</button><span>{searchPage}</span><button disabled={searchBusy||searchPage*50>=searchTotal} onClick={()=>void find(searchPage+1)}>{t.nextPage}</button></div>:null}
    {selected?<p>{m.selected}: {clients.find(client=>client.account_id===selected)?label(clients.find(client=>client.account_id===selected)!):selected}</p>:null}</>:null}</>:<><h2>{mode==='edit'?m.change:m.remove}</h2><button onClick={()=>{changed();setMode('send');setBody('');setReason('');}}>{m.new}</button></>}
  <form className="admin-action" onSubmit={prepare} noValidate aria-busy={busy} aria-describedby={error?'notice-form-error':'notice-format-help'}>
   {mode!=='delete'?<label>{m.body}<textarea rows={6} value={body} onChange={e=>{changed();setBody(e.target.value);}} aria-describedby="notice-format-help"/></label>:null}
   <label>{m.reason}<textarea rows={2} value={reason} onChange={e=>{changed();setReason(e.target.value);}} aria-describedby="notice-format-help"/></label>
   <p className="help" id="notice-format-help">{m.limit} {Array.from(body).length} / 4096 · {Array.from(reason).length} / 512</p><button className="primary" disabled={busy}>{busy?t.sending:m.prepare}</button>
  </form>
  {preview?<section className="admin-action" aria-label={m.preview}><h2 ref={previewRef} tabIndex={-1}>{m.preview}</h2><NoticeBody html={preview.html} lang={lang}/><p>{m.reason}: {preview.reason}</p>
   <dl className="stats-grid">{([['recipients',preview.recipient_count],['cabinet',preview.cabinet_count],['telegram',preview.telegram_count],['email',preview.email_count]] as const).map(([name,count])=><div key={name}><dt>{m[name]}</dt><dd>{count}</dd></div>)}</dl>
   <p className="help">{m.snapshot}</p><p className="warning">{m.limits}</p><p>{m.expires}: <time dateTime={preview.expires_at}>{new Date(preview.expires_at).toLocaleString(lang)}</time></p>{expired?<p role="status">{m.expired}</p>:null}
   <div className="purchase-actions"><button className="primary" disabled={busy||expired} onClick={()=>void confirm()}>{m[preview.mode]}</button><button disabled={busy} onClick={changed}>{m.cancel}</button></div></section>:null}
  {result?<Result value={result} lang={lang} title={m.result}/>:null}
  <button disabled={lastBusy} onClick={()=>{setError('');void refreshLast();}}>{m.refresh}</button>{lastBusy?<p role="status">{t.loading}</p>:null}{last?<><Result value={last} lang={lang} title={m.last}/>{last.notice&&last.notice.mode!=='delete'?<div className="purchase-actions"><button onClick={()=>changeLast('edit')}>{m.editLast}</button><button onClick={()=>changeLast('delete')}>{m.deleteLast}</button></div>:null}</>:null}
 </section>;
}
