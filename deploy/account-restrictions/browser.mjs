// Actual Account restriction browser/API acceptance on the owned HTTPS Docker stack.
// Private output contains only verdicts, status codes, counts and opaque hashes.
import {createRequire} from 'node:module';
import {readFileSync,mkdirSync,openSync,writeSync,closeSync,chmodSync} from 'node:fs';
import {createHash,randomUUID,X509Certificate} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import https from 'node:https';
import {fileURLToPath} from 'node:url';

const root=fileURLToPath(new URL('../../',import.meta.url));
const ownedProject=JSON.parse(readFileSync(root+'.superpowers/acceptance/local-docker/runtime.json')).project;
if(typeof ownedProject!=='string'||!ownedProject)throw Error('owned project metadata missing');
const require=createRequire(root+'web/package.json');
const {chromium,expect}=require('@playwright/test');
const origin='https://localhost:58443';
const certPem=readFileSync(root+'.superpowers/acceptance/local-docker/cert.pem');
const pin=createHash('sha256').update(new X509Certificate(certPem).publicKey.export({type:'spki',format:'der'})).digest('base64');
const evidence=root+'.superpowers/acceptance/account-restrictions';
mkdirSync(evidence,{recursive:true,mode:0o700});chmodSync(evidence,0o700);
const evidencePath=evidence+'/browser.jsonl',fd=openSync(evidencePath,'wx',0o600);
chmodSync(evidencePath,0o600);
const command='node deploy/account-restrictions/browser.mjs';
const target=`${ownedProject} HTTPS localhost:58443, Mailpit localhost:59446, own PostgreSQL and 3X-UI 3.7.0`;
function check(criterion,expected,actual,pass){
 writeSync(fd,JSON.stringify({criterion,target,command,expected,actual,verdict:pass?'PASS':'FAIL',artifacts:[evidencePath]})+'\n');
 if(!pass)throw Error(criterion);
}
function bridge(action,data={}){
 return JSON.parse(execFileSync('python3',['deploy/account-restrictions/local.py'],{cwd:root,input:JSON.stringify({action,...data}),stdio:['pipe','pipe','pipe'],timeout:90000}).toString());
}
function mailJSON(path){return new Promise((resolve,reject)=>{
 https.get('https://localhost:59446'+path,{ca:certPem},response=>{
  const chunks=[];response.on('data',chunk=>chunks.push(chunk));response.on('end',()=>{
   try{if(response.statusCode!==200)throw Error('mail status');resolve(JSON.parse(Buffer.concat(chunks).toString()));}catch(error){reject(error);}
  });
 }).on('error',reject);
});}
async function mailToken(email,purpose='/verify-email'){
 for(let attempt=0;attempt<150;attempt++){
  const rows=await mailJSON('/api/v1/messages');
  for(const row of rows.messages??[]){
   if(!row.To?.some(recipient=>recipient.Address===email))continue;
   const detail=await mailJSON('/api/v1/message/'+encodeURIComponent(row.ID));
   const token=detail.Text?.includes(purpose)&&detail.Text?.match(/#token=([A-Za-z0-9_-]{43})/);
   if(token)return token[1];
  }
  await new Promise(resolve=>setTimeout(resolve,200));
 }
 throw Error('mail token timeout');
}
async function register(browser,lang='en'){
 const context=await browser.newContext({viewport:{width:375,height:812}}),page=await context.newPage();
 const email='account-restrictions-'+randomUUID()+'@example.test',password='Local Account restriction '+randomUUID();
 step='register page '+lang;
 await page.goto(origin+'/register?lang='+lang);
 step='register form '+lang;
 await page.getByLabel('Email',{exact:true}).fill(email);
 await page.getByRole('checkbox').nth(0).check();await page.getByRole('checkbox').nth(1).check();
 await page.getByRole('button',{name:lang==='en'?'Continue':'Продолжить',exact:true}).click();
 step='registration email '+lang;
 const token=await mailToken(email);
 step='verify page '+lang;
 await page.goto(origin+'/verify-email?lang='+lang+'#token='+token);
 step='verify form '+lang;
 await page.getByLabel(lang==='en'?'Password':'Пароль',{exact:true}).fill(password);
 await page.getByRole('button',{name:lang==='en'?'Verify email':'Подтвердить email',exact:true}).click();
 await expect(page.getByText(lang==='en'?'Email verified. You can now sign in.':'Email подтверждён. Теперь войдите.')).toBeVisible();
 step='login form '+lang;
 await page.goto(origin+'/login?lang=en');await page.getByLabel('Email',{exact:true}).fill(email);
 await page.getByLabel('Password',{exact:true}).fill(password);
 await page.getByRole('button',{name:'Sign in',exact:true}).click();await expect(page).toHaveURL(/cabinet/);
 const me=await page.evaluate(async()=>await(await fetch('/api/v1/me')).json());
 return {context,page,id:me.account.account_id,email,password,csrf:me.csrf_token,registeredAt:Date.now()};
}
async function http(context,path,method='GET',body,csrf,key,headers={}){
 const cookie=(await context.cookies(origin)).find(item=>item.name==='__Host-session');
 const payload=body===undefined?null:Buffer.from(JSON.stringify(body));
 const h={Cookie:cookie?'__Host-session='+cookie.value:'',...headers};
 if(method!=='GET'&&!Object.keys(h).some(name=>name.toLowerCase()==='origin'))h.Origin=origin;
 if(payload){h['Content-Type']='application/json';h['Content-Length']=String(payload.length);}
 if(csrf)h['X-CSRF-Token']=csrf;if(key)h['Idempotency-Key']=key;
 return new Promise((resolve,reject)=>{
  const req=https.request(origin+path,{method,ca:certPem,headers:h,timeout:30000},response=>{
   const chunks=[];response.on('data',chunk=>chunks.push(chunk));response.on('end',()=>{
    try{const bytes=Buffer.concat(chunks),contentType=response.headers['content-type']??'';
     resolve({status:response.statusCode,body:contentType.includes('json')?JSON.parse(bytes.toString()):null});}
    catch(error){reject(error);}
   });
  });req.on('timeout',()=>req.destroy(Error('HTTPS timeout')));req.on('error',reject);req.end(payload??undefined);
 });
}
const route=id=>'/api/v1/operator/clients/'+id+'/restriction';
let browser,step='startup';
try{
 browser=await chromium.launch({args:['--ignore-certificate-errors-spki-list='+pin]});
 const transport=bridge('transport');
 check('Account restriction owned runtime boundary','owned project, empty Telegram operators, native Telegram disabled; Docker VPN baseline observed',
  'project='+transport.project+', no Telegram operators='+transport.no_telegram_operators+', Telegram disabled='+transport.telegram_disabled+', bot/reconcile stopped='+transport.bot_stopped+'/'+transport.reconcile_stopped+', VPN connected='+transport.vpn_connected,
  transport.project===ownedProject&&transport.no_telegram_operators&&transport.telegram_disabled&&transport.bot_stopped&&transport.reconcile_stopped);
 step='fresh web registration';
 const operator=await register(browser),client=await register(browser,'ru'),other=await register(browser);
 const anonymous=await browser.newContext();
 const before=bridge('state',{account:client.id});
 check('AC1 new account','verified restrictions_bridge@example.test web account signs in without approval or restriction',
  'three fresh verified browser sessions; client restricted='+before.restricted+', kind='+before.kind,
  new Set([operator.id,client.id,other.id]).size===3&&before.kind==='web'&&!before.restricted);
 step='grant operator';
 const nonoperator=await http(other.context,route(client.id),'POST',{restricted:true,reason:'Unauthorized'},other.csrf,randomUUID());
 const granted=bridge('grant',{account:operator.id});
 check('AC3 role boundary','nonoperator denied, dedicated verified web operator granted by private CLI',
  'nonoperator HTTP '+nonoperator.status+', role='+granted.role,nonoperator.status===403&&granted.role);
 const otherGranted=bridge('grant',{account:other.id});
 const otherRevoked=bridge('revoke',{account:other.id});
 const revoked=await http(other.context,route(client.id),'POST',{restricted:true,reason:'Revoked role'},other.csrf,randomUUID());
 check('AC3 revoked operator role','revoked verified web role cannot mutate account with its old session',
  'role before/after='+otherGranted.role+'/'+otherRevoked.role+', HTTP '+revoked.status+', code='+revoked.body?.error?.code,
  otherGranted.role===true&&otherRevoked.role===false&&revoked.status===403&&revoked.body?.error?.code==='INVALID_CREDENTIALS');
 const protectedTarget=await http(operator.context,route(operator.id),'POST',{restricted:true,reason:'Self target'},operator.csrf,randomUUID());
 const protectedOther=await http(operator.context,route(operator.id),'POST',{restricted:false,reason:'Still operator'},operator.csrf,randomUUID());
 const forged=await http(operator.context,route(client.id),'POST',{restricted:true,reason:'Forged',operator_account_id:other.id},operator.csrf,randomUUID());
 const badCSRF=await http(operator.context,route(client.id),'POST',{restricted:true,reason:'Wrong token'},'wrong',randomUUID());
 const badOrigin=await http(operator.context,route(client.id),'POST',{restricted:true,reason:'Wrong origin'},operator.csrf,randomUUID(),{Origin:'https://foreign.example.test'});
 const empty=await http(operator.context,route(client.id),'POST',{restricted:true,reason:'   '},operator.csrf,randomUUID());
 const long=await http(operator.context,route(client.id),'POST',{restricted:true,reason:'x'.repeat(1001)},operator.csrf,randomUUID());
 const oversized=await http(operator.context,route(client.id),'POST',{restricted:true,reason:'x'.repeat(17000)},operator.csrf,randomUUID());
 const nul=await http(operator.context,route(client.id),'POST',{restricted:true,reason:'x\u0000y'},operator.csrf,randomUUID());
 const rejected=bridge('state',{account:client.id});
 check('AC3 protected target and request guards','self/protected operator target yields OPERATOR_ACCOUNT_PROTECTED; forged actor, CSRF, Origin and invalid reasons rejected without state change',
  'HTTP '+[protectedTarget,badCSRF,badOrigin,forged,empty,long,oversized,nul].map(x=>x.status).join('/')+', protected code='+protectedTarget.body?.error?.code+', unchanged='+!rejected.restricted,
  protectedTarget.status===403&&protectedTarget.body?.error?.code==='OPERATOR_ACCOUNT_PROTECTED'&&
  protectedOther.status===403&&protectedOther.body?.error?.code==='OPERATOR_ACCOUNT_PROTECTED'&&
  [400,403].includes(badCSRF.status)&&[400,403].includes(badOrigin.status)&&
  forged.status===400&&empty.status===400&&long.status===400&&[400,413].includes(oversized.status)&&nul.status===400&&!rejected.restricted);
 step='trial rejected';
 const trial=await http(client.context,'/api/v1/trial-requests','POST',{comment:'Account restriction isolated trial rejection'},client.csrf,randomUUID());
 let trialDeniedStatus=0;
 if(trial.status===201){
  const denial=await http(operator.context,'/api/v1/operator/trial-requests/'+trial.body.request_id+'/decision','POST',
    {decision:'reject',reason:'Account restriction trial-only decision'},operator.csrf,randomUUID());trialDeniedStatus=denial.status;
 }
 const afterTrial=bridge('state',{account:client.id});
 check('AC1 trial denial independence','real trial rejection does not restrict verified account',
  'trial HTTP '+trial.status+'/'+trialDeniedStatus+', restricted='+afterTrial.restricted,
  trial.status===201&&trialDeniedStatus===200&&!afterTrial.restricted);
 step='fresh native subscriber';
 const subscriber=await register(browser);
 const subTrial=await http(subscriber.context,'/api/v1/trial-requests','POST',
  {comment:'Account restriction own native invariant'},subscriber.csrf,randomUUID());
 const approved=subTrial.status===201?await http(operator.context,
  '/api/v1/operator/trial-requests/'+subTrial.body.request_id+'/decision','POST',
  {decision:'approve',reason:''},operator.csrf,randomUUID()):{status:0};
 let active=false;
 for(let attempt=0;attempt<150;attempt++){
  const current=await http(subscriber.context,'/api/v1/subscription');
  if(current.status===200&&current.body?.status==='active'){active=true;break;}
  await new Promise(resolve=>setTimeout(resolve,200));
 }
 const subCard=await http(operator.context,'/api/v1/operator/clients/'+subscriber.id);
 const operation=subCard.body?.trial_requests?.find(row=>row.request_id===subTrial.body?.request_id)?.operation_id;
 const nativeBefore=operation?bridge('native',{operation}):null;
 const activeKey=await http(subscriber.context,'/api/v1/subscription/key');
 check('AC2 native subscription precondition','fresh approved trial reaches own 3X-UI with one grant/job, native identity, limits and counters observable',
  'trial/decision/key HTTP '+[subTrial,approved,activeKey].map(x=>x.status).join('/')+', active='+active+', native='+nativeBefore?.status+', grants/jobs='+nativeBefore?.grants+'/'+nativeBefore?.jobs+', counter fields='+nativeBefore?.counter_fields,
  subTrial.status===201&&approved.status===200&&active&&activeKey.status===200&&
  nativeBefore?.status==='applied'&&nativeBefore.grants===1&&nativeBefore.jobs===1&&
  nativeBefore.panel_identity&&nativeBefore.counter_fields);
 step='independent ban fixtures';
 const supportMessage=await http(client.context,'/api/v1/support/messages','POST',
  {text:'Account restriction owned invariant fixture'},client.csrf,randomUUID());
 const supportBan=await http(operator.context,'/api/v1/operator/clients/'+client.id+'/support/ban','POST',
  {banned:true,reason:'Account restriction independent support state'},operator.csrf);
 const vpnBan=bridge('fixture_vpn_ban',{account:client.id});
 const baseline=bridge('state',{account:client.id});
 check('AC2 independent-state precondition','own client has real support ban and separate VPN-ban fixture before account restriction',
  'support HTTP '+supportMessage.status+'/'+supportBan.status+', support banned='+baseline.support_banned+', VPN banned='+baseline.vpn_banned,
  supportMessage.status===201&&supportBan.status===204&&vpnBan.banned&&baseline.support_banned&&baseline.vpn_banned&&baseline.support!=='none');
 step='active credential proof';
 while(Date.now()<client.registeredAt+61000)await new Promise(resolve=>setTimeout(resolve,500));
 const resetRequest=await http(anonymous,'/api/v1/auth/password-reset','POST',{email:client.email,locale:'en'});
 const resetToken=await mailToken(client.email,'/reset-password');
 const beforeProof=bridge('state',{account:client.id});
 check('AC2 credential proof precondition','real password-reset proof delivered to Mailpit; ciphertext already cleared on delivery',
  'request HTTP '+resetRequest.status+', active proofs='+beforeProof.proofs+', ciphertext at rest='+beforeProof.proof_mail,
  resetRequest.status===202&&beforeProof.proofs>0&&beforeProof.proof_mail===0&&typeof resetToken==='string');
 step='restrict and replay';
 const sameKey=randomUUID(),payload={restricted:true,reason:'Account restriction owned acceptance'};
 const once=await http(operator.context,route(client.id),'POST',payload,operator.csrf,sameKey);
 const replay=await http(operator.context,route(client.id),'POST',payload,operator.csrf,sameKey);
 const conflict=await http(operator.context,route(client.id),'POST',{restricted:false,reason:payload.reason},operator.csrf,sameKey);
 const noop=await http(operator.context,route(client.id),'POST',{restricted:true,reason:'Still restricted'},operator.csrf,randomUUID());
 const restricted=bridge('state',{account:client.id});
 check('AC2/4 restrict idempotency','one state transition with real web actor; same key replay, changed body conflict, new-key no-op retains transition metadata',
  'HTTP '+[once,replay,conflict,noop].map(x=>x.status).join('/')+', audit count='+restricted.audit_restricted+', sessions='+restricted.sessions,
  once.status===200&&replay.status===200&&conflict.status===409&&noop.status===200&&
  once.body?.operator_account_id===operator.id&&replay.body?.changed_at===once.body?.changed_at&&
  noop.body?.changed_at===once.body?.changed_at&&noop.body?.operator_account_id===operator.id&&
  restricted.restricted&&restricted.audit_restricted===before.audit_restricted+1&&restricted.sessions===0);
 const oldMe=await http(client.context,'/api/v1/me'),oldKey=await http(client.context,'/api/v1/subscription/key');
 const login=await http(anonymous,'/api/v1/auth/login','POST',{email:client.email,password:client.password});
 const oldProof=await http(anonymous,'/api/v1/auth/password-reset/complete','POST',
  {token:resetToken,new_password:'Unused Account restriction '+randomUUID()});
 check('AC2 restricted access','old cookie cannot read account/key, fresh login denied, old proof revoked and ciphertext cleared',
  'HTTP '+[oldMe,oldKey,login,oldProof].map(x=>x.status).join('/')+', proofs='+restricted.proofs+', encrypted mail='+restricted.proof_mail,
  [401,403].includes(oldMe.status)&&[401,403].includes(oldKey.status)&&login.status===403&&oldProof.status===400&&
  restricted.proofs===0&&restricted.proof_mail===0);
 check('AC2 independent account state','restriction preserves true VPN ban, native identity, operations and true support ban/state',
  'identity equal='+ (restricted.identity===baseline.identity)+', operations equal='+(restricted.operations===baseline.operations)+', VPN ban='+restricted.vpn_banned+', support ban='+restricted.support_banned+', support digest equal='+(restricted.support===baseline.support),
  restricted.identity===baseline.identity&&restricted.operations===baseline.operations&&restricted.vpn_banned===baseline.vpn_banned&&
  restricted.support_banned===baseline.support_banned&&restricted.support===baseline.support);
 step='unrestrict and fresh login';
 const release=await http(operator.context,route(client.id),'POST',{restricted:false,reason:'Account restriction review complete'},operator.csrf,randomUUID());
 const open=bridge('state',{account:client.id});
 const stale=await http(client.context,'/api/v1/me');
 const fresh=await http(anonymous,'/api/v1/auth/login','POST',{email:client.email,password:client.password});
 check('AC2 explicit unrestrict','explicit release succeeds, old session stays revoked, new login succeeds with unchanged identity',
  'HTTP '+[release,stale,fresh].map(x=>x.status).join('/')+', audit count='+open.audit_unrestricted,
  release.status===200&&!open.restricted&&[401,403].includes(stale.status)&&fresh.status===200&&
  open.identity===baseline.identity&&open.support===baseline.support&&open.audit_unrestricted===before.audit_unrestricted+1);
 step='native restriction invariants';
 const nativeStateBefore=bridge('state',{account:subscriber.id});
 const nativeRestrict=await http(operator.context,route(subscriber.id),'POST',
  {restricted:true,reason:'Account restriction native access review'},operator.csrf,randomUUID());
 const nativeDuring=bridge('native',{operation});
 const nativeStateDuring=bridge('state',{account:subscriber.id});
 const nativeKeyDenied=await http(subscriber.context,'/api/v1/subscription/key');
 const nativeLoginDenied=await http(anonymous,'/api/v1/auth/login','POST',
  {email:subscriber.email,password:subscriber.password});
 const nativeUnrestrict=await http(operator.context,route(subscriber.id),'POST',
  {restricted:false,reason:'Account restriction native review complete'},operator.csrf,randomUUID());
 const nativeAfter=bridge('native',{operation});
 const nativeStateAfter=bridge('state',{account:subscriber.id});
 const nativeFreshLogin=await http(anonymous,'/api/v1/auth/login','POST',
  {email:subscriber.email,password:subscriber.password});
 check('AC2 native identity and counters','restriction denies old key/login, then fresh login; panel identity, limit, counter and PG operation digests remain equal',
  'HTTP '+[nativeRestrict,nativeKeyDenied,nativeLoginDenied,nativeUnrestrict,nativeFreshLogin].map(x=>x.status).join('/')+
  ', panel/target/counter/operation equal='+[nativeBefore.panel_digest===nativeDuring.panel_digest&&nativeDuring.panel_digest===nativeAfter.panel_digest,
   nativeBefore.target_digest===nativeDuring.target_digest&&nativeDuring.target_digest===nativeAfter.target_digest,
   nativeBefore.counter_digest===nativeDuring.counter_digest&&nativeDuring.counter_digest===nativeAfter.counter_digest,
   nativeStateBefore.operations===nativeStateDuring.operations&&nativeStateDuring.operations===nativeStateAfter.operations].join('/'),
  nativeRestrict.status===200&&[401,403].includes(nativeKeyDenied.status)&&nativeLoginDenied.status===403&&
  nativeUnrestrict.status===200&&nativeFreshLogin.status===200&&nativeDuring.panel_identity&&nativeAfter.panel_identity&&
  nativeBefore.panel_digest===nativeDuring.panel_digest&&nativeDuring.panel_digest===nativeAfter.panel_digest&&
  nativeBefore.target_digest===nativeDuring.target_digest&&nativeDuring.target_digest===nativeAfter.target_digest&&
  nativeBefore.counter_digest===nativeDuring.counter_digest&&nativeDuring.counter_digest===nativeAfter.counter_digest&&
  nativeStateBefore.operations===nativeStateDuring.operations&&nativeStateDuring.operations===nativeStateAfter.operations);
 step='browser operator controls';
 await operator.page.goto(origin+'/admin/clients/'+client.id+'/show?lang=en');
 await expect(operator.page.getByRole('heading',{name:client.email})).toBeVisible();
 const enReason=operator.page.getByLabel('Account restriction reason');
 await enReason.focus();await expect(enReason).toBeFocused();
 const enFits=await operator.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth);
 await enReason.fill('Account restriction browser restriction');
 await operator.page.getByRole('button',{name:'Restrict account'}).click();
 await expect(operator.page.getByText('Restricting ends active sessions and prevents account sign-in and access to the subscription link.')).toBeVisible();
 const uiRestrict=operator.page.waitForResponse(response=>response.url().endsWith(route(client.id))&&response.request().method()==='POST');
 await operator.page.getByRole('button',{name:'Confirm restriction'}).click();
 const uiRestrictStatus=(await uiRestrict).status();
 await expect(operator.page.getByText('Account restricted',{exact:true})).toBeVisible();
 await operator.page.goto(origin+'/admin/clients/'+client.id+'/show?lang=ru');
 await expect(operator.page.getByRole('heading',{name:client.email})).toBeVisible();
 const ruReason=operator.page.getByLabel('Причина ограничения аккаунта');
 await ruReason.focus();await expect(ruReason).toBeFocused();
 const ruFits=await operator.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth);
 await ruReason.fill('Проверка завершена');
 const uiRelease=operator.page.waitForResponse(response=>response.url().endsWith(route(client.id))&&response.request().method()==='POST');
 await operator.page.getByRole('button',{name:'Снять ограничение аккаунта'}).focus();
 await operator.page.keyboard.press('Enter');
 const uiReleaseStatus=(await uiRelease).status();
 await expect(operator.page.getByText('Аккаунт без ограничений',{exact:true})).toBeVisible();
 check('AC6 browser RU/EN mobile keyboard','real operator card at 375px restricts with confirmation and releases by keyboard; named reason controls and no overflow',
  'EN/RU HTTP '+uiRestrictStatus+'/'+uiReleaseStatus+', fit='+enFits+'/'+ruFits+', focus=true',
  enFits&&ruFits&&uiRestrictStatus===200&&uiReleaseStatus===200&&!bridge('state',{account:client.id}).restricted);
 step='legacy history and explicit release';
 const manifest=bridge('manifest');
 if(manifest.ready){
  const legacyBefore=bridge('state',{account:manifest.rejected_account});
  const importedNoop=await http(operator.context,route(manifest.rejected_account),'POST',
   {restricted:true,reason:'Account restriction imported state check'},operator.csrf,randomUUID());
  const legacyCard=await http(operator.context,'/api/v1/operator/clients/'+manifest.rejected_account);
  await operator.page.goto(origin+'/admin/clients/'+manifest.rejected_account+'/show?lang=en');
  const history=operator.page.locator('section[aria-label="Legacy registration history"]');
  await expect(history.getByRole('heading',{name:'Legacy registration history'})).toBeVisible();
  const older=history.getByRole('button',{name:'Load older registration events'});
  await expect(older).toBeVisible();
  const firstCount=await history.locator('article.admin-audit').count();
  const olderResponse=operator.page.waitForResponse(response=>response.url().endsWith('/history')&&response.request().method()==='POST');
  await older.click();const olderStatus=(await olderResponse).status();
  await expect(history.locator('article.admin-audit')).toHaveCount(52);
  const hasApprovalButton=await history.getByRole('button',{name:/approve|reject/i}).count()>0;
  check('AC5/6 legacy browser history','read-only prior rejection, imported no-op has unknown manual transition metadata, first page 50, stable older page to 52, no registration approval action',
   'card/no-op HTTP '+legacyCard.status+'/'+importedNoop.status+', null metadata='+(importedNoop.body?.changed_at===null)+', initial events='+firstCount+', older HTTP '+olderStatus+', total=52, action button='+hasApprovalButton,
   legacyBefore.restricted&&importedNoop.status===200&&importedNoop.body?.changed_at===null&&importedNoop.body?.operator_account_id===null&&
   legacyCard.status===200&&legacyCard.body?.legacy_approval?.status==='rejected'&&
   legacyCard.body?.legacy_has_more===true&&firstCount===50&&olderStatus===200&&!hasApprovalButton);
  const releaseLegacy=await http(operator.context,route(manifest.rejected_account),'POST',{restricted:false,reason:'Account restriction explicit release'},operator.csrf,randomUUID());
  const afterRelease=bridge('state',{account:manifest.rejected_account});
  const reimport=bridge('reimport');
  const afterReimport=bridge('state',{account:manifest.rejected_account});
  check('AC5 manual release survives reimport','manual unrestrict stays effective after identical legacy replay, history retained, SQLite unchanged',
   'release HTTP '+releaseLegacy.status+', reimport='+JSON.stringify(reimport.report)+', restricted='+afterReimport.restricted+', source equal='+reimport.source_unchanged,
   releaseLegacy.status===200&&!afterRelease.restricted&&!afterReimport.restricted&&
   afterReimport.legacy_snapshot===1&&afterReimport.legacy_events===52&&
   reimport.report?.users===0&&reimport.report?.events===0&&reimport.report?.changed===0&&reimport.source_unchanged);
 }else{
  writeSync(fd,JSON.stringify({criterion:'AC5/6 legacy browser and reimport',target,command,
   expected:'prior import fixture exists',actual:'import fixture absent',verdict:'BLOCKED',artifacts:[evidencePath]})+'\n');
 }
 step='concurrent operator transitions';
 const raceBefore=bridge('state',{account:other.id});
 const racing=await Promise.all([
  http(operator.context,route(other.id),'POST',{restricted:true,reason:'Account restriction race restrict'},operator.csrf,randomUUID()),
  http(operator.context,route(other.id),'POST',{restricted:false,reason:'Account restriction race release'},operator.csrf,randomUUID())
 ]);
 const raceAfter=bridge('state',{account:other.id});
 const raceValid=racing.every(x=>x.status===200)&&raceAfter.audit_restricted===raceBefore.audit_restricted+1&&
  raceAfter.audit_unrestricted===raceBefore.audit_unrestricted+(raceAfter.restricted?0:1);
 check('AC4 concurrent opposite transitions','serialized result has one audit per real state transition and no partial state',
  'HTTP '+racing.map(x=>x.status).join('/')+', restricted='+raceAfter.restricted+', audit deltas='+
  (raceAfter.audit_restricted-raceBefore.audit_restricted)+'/'+(raceAfter.audit_unrestricted-raceBefore.audit_unrestricted),raceValid);
 if(raceAfter.restricted){
  const settle=await http(operator.context,route(other.id),'POST',{restricted:false,reason:'Account restriction race cleanup'},operator.csrf,randomUUID());
  check('AC4 concurrent fixture settled','own fresh fixture returned to unrestricted state after race',
   'HTTP '+settle.status+', restricted='+bridge('state',{account:other.id}).restricted,
   settle.status===200&&!bridge('state',{account:other.id}).restricted);
 }
 check('AC8 runtime postflight','empty Telegram operators, native Telegram disabled and Docker VPN state retained',
  'no Telegram operators='+bridge('transport').no_telegram_operators+', Telegram disabled='+bridge('transport').telegram_disabled+', bot stopped='+bridge('transport').bot_stopped+', VPN baseline equal='+(bridge('transport').vpn_connected===transport.vpn_connected)+', VPN config digest equal='+(bridge('transport').vpn_config_digest===transport.vpn_config_digest),
  bridge('transport').no_telegram_operators&&bridge('transport').telegram_disabled&&bridge('transport').bot_stopped&&bridge('transport').reconcile_stopped&&
  bridge('transport').vpn_connected===transport.vpn_connected&&bridge('transport').vpn_config_digest===transport.vpn_config_digest);
}catch(error){
 const category=error?.message?.includes('mail token timeout')?'mail token timeout':
  error?.name==='TimeoutError'?'browser timeout':error?.name==='AssertionError'?'assertion':'driver or runtime error';
 writeSync(fd,JSON.stringify({criterion:'Account restriction driver interruption at '+step,target,command,
  expected:'all reached checks complete',actual:category+'; inspect private bounded run log',verdict:'BLOCKED',artifacts:[evidencePath]})+'\n');
 process.exitCode=1;
}finally{if(browser)await browser.close();closeSync(fd);}
