import {useEffect,useId,useRef,useState} from 'react';
import * as api from './api/client';
import {text,link,loginRedirect,errorText,type Lang} from './i18n';

function checked(value:api.ReferralsResult){
 const counts=['invited','granted_rewards','pending_rewards','money_records','pending_money_records'] as const;
 const days=/^(0|[1-9][0-9]*)$/;
 if(value?.version!=='2026-10-10-referrals-v1'||typeof value.web_url!=='string'||!value.web_url||!Array.isArray(value.levels)||value.levels.length!==2||!Number.isSafeInteger(value.unclassified_records)||value.unclassified_records<0||value.levels.some((level,index)=>level?.level!==index+1||typeof level.granted_days!=='string'||!days.test(level.granted_days)||typeof level.pending_days!=='string'||!days.test(level.pending_days)||counts.some(key=>!Number.isSafeInteger(level[key])||level[key]<0)))throw new api.ApiError(503,'SERVICE_UNAVAILABLE','');
 return value;
}

export function Referrals({lang}:{lang:Lang}){
 const t=text(lang),id=useId();const[value,setValue]=useState<api.ReferralsResult>();const[loading,setLoading]=useState(true);const[error,setError]=useState('');const[revision,setRevision]=useState(0);const heading=useRef<HTMLHeadingElement>(null),focusAfterRetry=useRef(false);
 useEffect(()=>{
  const controller=new AbortController();setLoading(true);setValue(undefined);setError('');
  void api.getReferrals(controller.signal).then(result=>{if(!controller.signal.aborted)setValue(checked(result));}).catch(reason=>{if(controller.signal.aborted)return;setError(errorText(reason,lang));if(reason instanceof api.ApiError&&reason.status===401)loginRedirect(lang);}).finally(()=>{if(!controller.signal.aborted)setLoading(false);});
  return()=>controller.abort();
 },[lang,revision]);
 useEffect(()=>{if(!loading&&focusAfterRetry.current){heading.current?.focus();focusAfterRetry.current=false;}},[loading]);
 const empty=value&&value.unclassified_records===0&&value.levels.every(level=>level.invited===0&&level.granted_rewards===0&&level.pending_rewards===0&&level.money_records===0&&level.pending_money_records===0);
 return <section className="card" aria-labelledby={id+'-title'} aria-busy={loading}>
  <h1 id={id+'-title'} ref={heading} tabIndex={-1}>{t.referralsTitle}</h1>
  <p><a href={link('/cabinet',lang)}>{t.referralsBack}</a></p>
  {loading?<p role="status">{t.loading}</p>:null}
  {error?<div className="error" role="alert"><p>{error}</p><button disabled={loading} onClick={()=>{focusAfterRetry.current=true;setRevision(value=>value+1);}}>{t.retry}</button></div>:null}
  {value&&!loading?<>
   <label htmlFor={id+'-link'}>{t.referralsLink}<input id={id+'-link'} readOnly value={value.web_url} aria-describedby={id+'-link-help'}/></label>
   <p className="help" id={id+'-link-help'}>{t.referralsLinkHelp}</p>
   <p>{t.referralsFacts}</p>
   {empty?<p role="status">{t.referralsEmpty}</p>:null}
   {value.levels.map(level=><section key={level.level} aria-labelledby={id+'-level-'+level.level}>
    <h2 id={id+'-level-'+level.level}>{level.level===1?t.referralsLevelOne:t.referralsLevelTwo}</h2>
    <dl className="stats-grid">
     <div><dt>{t.referralsInvited}</dt><dd>{level.invited}</dd></div>
     <div><dt>{t.referralsGrantedDays}</dt><dd>{level.granted_days}</dd></div>
     <div><dt>{t.referralsPendingDays}</dt><dd>{level.pending_days}</dd></div>
     <div><dt>{t.referralsGrantedRewards}</dt><dd>{level.granted_rewards}</dd></div>
     <div><dt>{t.referralsPendingRewards}</dt><dd>{level.pending_rewards}</dd></div>
     <div><dt>{t.referralsMoneyRecords}</dt><dd>{level.money_records}</dd></div>
     <div><dt>{t.referralsPendingMoneyRecords}</dt><dd>{level.pending_money_records}</dd></div>
    </dl>
   </section>)}
   <p className="help">{t.referralsMoneyHelp}</p>
   {value.unclassified_records>0?<p className="notice">{t.referralsUnclassified}: {value.unclassified_records}</p>:null}
  </>:null}
 </section>;
}
