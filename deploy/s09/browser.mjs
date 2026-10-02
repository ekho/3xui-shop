// S09 API/browser acceptance on the owned HTTPS Docker stack.
// Only statuses, counts and opaque digests are recorded.
import {createRequire} from 'node:module';
import {readFileSync,mkdirSync,openSync,writeSync,closeSync,chmodSync} from 'node:fs';
import {createHash,randomUUID,X509Certificate} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import https from 'node:https';
import {fileURLToPath} from 'node:url';

const root=fileURLToPath(new URL('../../',import.meta.url));
const require=createRequire(root+'web/package.json');
const {chromium,expect}=require('@playwright/test');
const origin='https://localhost:58443';
const certPem=readFileSync(root+'.superpowers/sdd/2026-10-01-s01-web-trial/local-docker/cert.pem');
const pin=createHash('sha256').update(new X509Certificate(certPem).publicKey.export({type:'spki',format:'der'})).digest('base64');
const evidence=root+'.superpowers/sdd/2026-10-02-s09-catalogue/e2e';
mkdirSync(evidence,{recursive:true,mode:0o700});chmodSync(evidence,0o700);
const evidencePath=evidence+'/browser.jsonl',fd=openSync(evidencePath,'a',0o600);
chmodSync(evidencePath,0o600);
const command='node deploy/s09/browser.mjs';
const target='cabinet-s01-local HTTPS localhost:58443, own PostgreSQL and Mailpit localhost:59446';
function check(criterion,expected,actual,pass){
 writeSync(fd,JSON.stringify({criterion,target,command,expected,actual,verdict:pass?'PASS':'FAIL',artifacts:[evidencePath]})+'\n');
 if(!pass)throw Error(criterion);
}
function bridge(action,data={}){
 return JSON.parse(execFileSync('python3',['deploy/s09/local.py'],{cwd:root,input:JSON.stringify({action,...data}),stdio:['pipe','pipe','pipe'],timeout:90000}).toString());
}
function mailJSON(path){return new Promise((resolve,reject)=>{
 https.get('https://localhost:59446'+path,{ca:certPem},response=>{
  const chunks=[];response.on('data',chunk=>chunks.push(chunk));response.on('end',()=>{
   try{if(response.statusCode!==200)throw Error('mail status');resolve(JSON.parse(Buffer.concat(chunks).toString()));}catch(error){reject(error);}
  });
 }).on('error',reject);
});}
async function mailToken(email){
 for(let attempt=0;attempt<150;attempt++){
  const rows=await mailJSON('/api/v1/messages');
  for(const row of rows.messages??[]){
   if(!row.To?.some(recipient=>recipient.Address===email))continue;
   const detail=await mailJSON('/api/v1/message/'+encodeURIComponent(row.ID));
   const token=detail.Text?.includes('/verify-email')&&detail.Text?.match(/#token=([A-Za-z0-9_-]{43})/);
   if(token)return token[1];
  }
  await new Promise(resolve=>setTimeout(resolve,200));
 }
 throw Error('mail token timeout');
}
async function register(browser,lang='en'){
 const context=await browser.newContext({viewport:{width:375,height:812}}),page=await context.newPage();
 const email='s09-'+randomUUID()+'@example.test',password='Local S09 '+randomUUID();
 step='register '+lang;
 await page.goto(origin+'/register?lang='+lang);
 await page.getByLabel('Email',{exact:true}).fill(email);
 await page.getByRole('checkbox').nth(0).check();await page.getByRole('checkbox').nth(1).check();
 await page.getByRole('button',{name:lang==='en'?'Continue':'Продолжить',exact:true}).click();
 const token=await mailToken(email);
 await page.goto(origin+'/verify-email?lang='+lang+'#token='+token);
 await page.getByLabel(lang==='en'?'Password':'Пароль',{exact:true}).fill(password);
 await page.getByRole('button',{name:lang==='en'?'Verify email':'Подтвердить email',exact:true}).click();
 await expect(page.getByText(lang==='en'?'Email verified. You can now sign in.':'Email подтверждён. Теперь войдите.')).toBeVisible();
 await page.goto(origin+'/login?lang=en');
 await page.getByLabel('Email',{exact:true}).fill(email);
 await page.getByLabel('Password',{exact:true}).fill(password);
 await page.getByRole('button',{name:'Sign in',exact:true}).click();
 await expect(page).toHaveURL(/cabinet/);
 const me=await page.evaluate(async()=>await(await fetch('/api/v1/me')).json());
 return {context,page,id:me.account.account_id,csrf:me.csrf_token};
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
const base='/api/v1/operator/catalogue';
const price=(period,currency,amount)=>({period_days:period,currency,amount_minor:amount});
const termsA={devices:2,traffic_gb:0,profile:'regular',hidden:false,periods:[30,90],prices:[
 price(30,'RUB','9007199254740993'),price(30,'USD','12345'),price(30,'XTR','0'),
 price(90,'RUB','0'),price(90,'USD','98765'),price(90,'XTR','10')]};
const termsB={devices:3,traffic_gb:100,profile:'euru',hidden:false,periods:[30],prices:[
 price(30,'RUB','10000'),price(30,'USD','100'),price(30,'XTR','100')]};
const plan=id=>base+'/plans/'+id;
const sameTerms=(a,b)=>a.devices===b.devices&&a.traffic_gb===b.traffic_gb&&
 a.profile===b.profile&&a.hidden===b.hidden&&
 JSON.stringify([...a.periods].sort((x,y)=>x-y))===JSON.stringify([...b.periods].sort((x,y)=>x-y))&&
 JSON.stringify(a.prices.map(p=>[p.period_days,p.currency,p.amount_minor].join(':')).sort())===
 JSON.stringify(b.prices.map(p=>[p.period_days,p.currency,p.amount_minor].join(':')).sort());
let browser,operator,outsider,step='startup';
try{
 browser=await chromium.launch({args:['--ignore-certificate-errors-spki-list='+pin]});
 const transport=bridge('transport'),initial=bridge('current');
 check('S09 owned runtime and empty catalogue','own project, bot/reconcile stopped, Docker VPN connected, empty catalogue',
  'owned='+String(transport.project==='cabinet-s01-local')+', stopped='+transport.bot_stopped+'/'+transport.reconcile_stopped+
  ', VPN='+transport.vpn_connected+', plans/revisions='+initial.plans+'/'+initial.revisions,
  transport.project==='cabinet-s01-local'&&transport.no_telegram_operators&&transport.bot_stopped&&
  transport.reconcile_stopped&&transport.vpn_connected&&initial.plans===0&&initial.revisions===0);
 operator=await register(browser,'en');
 const customer=await register(browser,'ru');
 outsider=await register(browser,'en');
 const denied=await http(outsider.context,base+'?page=1&per_page=50');
 const empty=await http(customer.context,'/api/v1/catalogue');
 check('AC1/5 empty catalogue and role boundary','client returns {plans:[]}; nonoperator denied operator list',
  'client/operator HTTP '+empty.status+'/'+denied.status+', client count='+empty.body?.plans?.length,
  empty.status===200&&Array.isArray(empty.body?.plans)&&empty.body.plans.length===0&&denied.status===403);
 const granted=bridge('grant',{account:operator.id});
 if(!granted.role)throw Error('operator grant absent');
 step='create plans';
 const createKey=randomUUID();
 const first=await http(operator.context,base+'/plans','POST',{terms:termsA,reason:'S09 exact price'},operator.csrf,createKey);
 const firstReplay=await http(operator.context,base+'/plans','POST',{terms:termsA,reason:'S09 exact price'},operator.csrf,createKey);
 const firstConflict=await http(operator.context,base+'/plans','POST',{terms:termsB,reason:'Different body'},operator.csrf,createKey);
 const second=await http(operator.context,base+'/plans','POST',{terms:termsB,reason:'S09 second visible'},operator.csrf,randomUUID());
 const afterCreate=bridge('current');
 const clientList=await http(customer.context,'/api/v1/catalogue');
 const operatorList=await http(operator.context,base+'?page=1&per_page=1');
 const clientA=clientList.body?.plans?.find(row=>row.plan_id===first.body?.plan_id);
 check('AC1/2 exact visible offers and pagination','two visible plans, complete periods/currencies, exact minor-unit strings, create201/replay201/body mismatch409, operator page limited',
  'create/replay/conflict/second HTTP '+[first,firstReplay,firstConflict,second].map(x=>x.status).join('/')+
  ', client/operator HTTP '+clientList.status+'/'+operatorList.status+', client plans='+clientList.body?.plans?.length+
  ', operator page/total='+operatorList.body?.plans?.length+'/'+operatorList.body?.total+
  ', revisions='+afterCreate.revisions,
  first.status===201&&firstReplay.status===201&&firstConflict.status===409&&second.status===201&&
  first.body?.plan_id===firstReplay.body?.plan_id&&first.body?.revision===1&&second.body?.revision===1&&
  first.body?.actor_account_id===operator.id&&first.body?.source==='operator'&&
  clientList.status===200&&clientList.body?.plans?.length===2&&clientA?.traffic_gb===0&&
  sameTerms({devices:clientA.devices,traffic_gb:clientA.traffic_gb,profile:clientA.profile,hidden:clientA.hidden,
    periods:clientA.periods,prices:clientA.prices},termsA)&&
  !('actor_account_id' in clientA)&&!('reason' in clientA)&&
  operatorList.status===200&&operatorList.body?.plans?.length===1&&operatorList.body?.total===2&&
  operatorList.body?.page===1&&operatorList.body?.per_page===1&&afterCreate.plans===2&&afterCreate.revisions===2);
 step='customer browser exact price';
 await customer.page.goto(origin+'/catalogue?lang=en');
 await expect(customer.page.getByRole('heading',{name:'Plans'})).toBeVisible();
 const enSelect=customer.page.getByRole('button',{name:'Select plan'}).first();
 await enSelect.focus();await expect(enSelect).toBeFocused();await customer.page.keyboard.press('Enter');
 await customer.page.getByLabel('Period').selectOption('30');
 await customer.page.getByLabel('Currency').selectOption('RUB');
 await expect(customer.page.getByText('90,071,992,547,409.93 RUB')).toBeVisible();
 await customer.page.getByLabel('Currency').selectOption('XTR');
 await expect(customer.page.getByText('0 XTR')).toBeVisible();
 const enFits=await customer.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth);
 const hasPaymentAction=await customer.page.getByRole('button',{name:/pay|checkout|buy/i}).count()>0;
 await customer.page.goto(origin+'/catalogue?lang=ru');
 await expect(customer.page.getByRole('heading',{name:'Тарифы'})).toBeVisible();
 const ruSelect=customer.page.getByRole('button',{name:'Выбрать тариф'}).first();
 await ruSelect.focus();await expect(ruSelect).toBeFocused();await customer.page.keyboard.press('Enter');
 await expect(customer.page.getByLabel('Период')).toBeVisible();
 const ruFits=await customer.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth);
 check('AC1/2/7 customer RU/EN browser selection','real 375px catalogue, keyboard selection, exact large/zero price, no payment action',
  'large and zero rendered=true, EN/RU fit='+enFits+'/'+ruFits+', payment action='+hasPaymentAction,
  enFits&&ruFits&&!hasPaymentAction);
 step='strict boundaries';
 const beforeInvalid=bridge('current');
 const invalid=[
  {...termsA,prices:termsA.prices.slice(1)},
  {...termsA,profile:''},
  {...termsA,prices:[...termsA.prices,termsA.prices[0]]},
  {...termsA,prices:termsA.prices.map((p,i)=>i===0?{...p,amount_minor:'9223372036854775808'}:p)},
  {...termsA,prices:termsA.prices.map((p,i)=>i===2?{...p,amount_minor:'1.5'}:p)},
  {...termsA,prices:termsA.prices.map((p,i)=>i===0?{...p,amount_minor:'NaN'}:p)},
  {...termsA,prices:termsA.prices.map((p,i)=>i===0?{...p,amount_minor:'-1'}:p)},
  {...termsA,devices:0},
  {...termsA,profile:'unknown'},
  {...termsA,periods:[30,30]},
 ];
 const invalidStatuses=[];
 for(const terms of invalid){
  const response=await http(operator.context,base+'/plans','POST',{terms,reason:'S09 invalid boundary'},operator.csrf,randomUUID());
  invalidStatuses.push(response.status);
 }
 const afterInvalid=bridge('current');
 check('AC2 invalid matrix/profile/duplicate/overflow/XTR/period','incomplete/duplicate/overflow/fractional/nonfinite/negative/bounds/unknown profile writes rejected with 4xx and zero catalogue changes',
  'statuses='+invalidStatuses.join('/')+', state equal='+String(beforeInvalid.digest===afterInvalid.digest)+
  ', revisions='+afterInvalid.revisions,
  invalidStatuses.every(status=>status===400||status===409)&&
  beforeInvalid.digest===afterInvalid.digest&&beforeInvalid.revisions===afterInvalid.revisions);
 step='authorization guards';
 const noRole=await http(outsider.context,base+'/plans','POST',{terms:termsB,reason:'No role'},outsider.csrf,randomUUID());
 const forged=await http(operator.context,base+'/plans','POST',{terms:termsB,reason:'Forged actor',actor_account_id:outsider.id},operator.csrf,randomUUID());
 const badCsrf=await http(operator.context,base+'/plans','POST',{terms:termsB,reason:'Bad CSRF'},'wrong',randomUUID());
 const badOrigin=await http(operator.context,base+'/plans','POST',{terms:termsB,reason:'Bad Origin'},operator.csrf,randomUUID(),{Origin:'https://foreign.example.test'});
 const beforeRevoke=bridge('grant',{account:outsider.id});
 const afterRevoke=bridge('revoke',{account:outsider.id});
 const revoked=await http(outsider.context,base+'/plans','POST',{terms:termsB,reason:'Revoked role'},outsider.csrf,randomUUID());
 const afterGuards=bridge('current');
 check('AC5 role, forged actor and request guards','nonoperator/revoked role, actor spoof, CSRF and Origin rejected without write',
  'HTTP '+[noRole,forged,badCsrf,badOrigin,revoked].map(x=>x.status).join('/')+
  ', role '+beforeRevoke.role+'/'+afterRevoke.role+', state equal='+String(afterGuards.digest===afterInvalid.digest),
  noRole.status===403&&forged.status===400&&[400,403].includes(badCsrf.status)&&
  [400,403].includes(badOrigin.status)&&beforeRevoke.role&&!afterRevoke.role&&
  revoked.status===403&&afterGuards.digest===afterInvalid.digest);
 step='revision';
 const oldRevision=bridge('revisions',{plan:first.body.plan_id});
 const revisedTerms={...termsA,devices:4,prices:termsA.prices.map((p,i)=>i===0?{...p,amount_minor:'9007199254740994'}:p)};
 const reviseKey=randomUUID();
 const revised=await http(operator.context,plan(first.body.plan_id)+'/revision','POST',
  {terms:revisedTerms,expected_revision:1,reason:'S09 revised exact price'},operator.csrf,reviseKey);
 const replay=await http(operator.context,plan(first.body.plan_id)+'/revision','POST',
  {terms:revisedTerms,expected_revision:1,reason:'S09 revised exact price'},operator.csrf,reviseKey);
 const stale=await http(operator.context,plan(first.body.plan_id)+'/revision','POST',
  {terms:termsA,expected_revision:1,reason:'Stale revision'},operator.csrf,randomUUID());
 const mismatch=await http(operator.context,plan(first.body.plan_id)+'/revision','POST',
  {terms:termsA,expected_revision:1,reason:'Different replay body'},operator.csrf,reviseKey);
 const newRevision=bridge('revisions',{plan:first.body.plan_id});
 check('AC3/4 immutable revision and optimistic conflicts','revise200/replay200/stale409/body mismatch409; old revision digest unchanged and one new revision',
  'HTTP '+[revised,replay,stale,mismatch].map(x=>x.status).join('/')+
  ', old/current revision rows='+oldRevision.length+'/'+newRevision.length+
  ', old digest equal='+String(oldRevision[0]?.terms_digest===newRevision[0]?.terms_digest),
  revised.status===200&&replay.status===200&&stale.status===409&&mismatch.status===409&&
  revised.body?.revision===2&&replay.body?.revision===2&&oldRevision.length===1&&newRevision.length===2&&
  oldRevision[0]?.terms_digest===newRevision[0]?.terms_digest&&
  newRevision[1]?.has_actor&&newRevision[1]?.has_reason);
 step='concurrent archives';
 const beforeRaceA=bridge('revisions',{plan:first.body.plan_id});
 const beforeRaceB=bridge('revisions',{plan:second.body.plan_id});
 const [archiveA,archiveB]=await Promise.all([
  http(operator.context,plan(first.body.plan_id)+'/archive','POST',{expected_revision:2,reason:'S09 concurrent archive A'},operator.csrf,randomUUID()),
  http(operator.context,plan(second.body.plan_id)+'/archive','POST',{expected_revision:1,reason:'S09 concurrent archive B'},operator.csrf,randomUUID())]);
 const archiveCodes=[archiveA.status,archiveB.status].sort((a,b)=>a-b);
 const afterRace=bridge('current');
 const afterRaceA=bridge('revisions',{plan:first.body.plan_id});
 const afterRaceB=bridge('revisions',{plan:second.body.plan_id});
 const prefixEqual=(before,after)=>before.every((row,index)=>row.terms_digest===after[index]?.terms_digest);
 const survivor=archiveA.status===200?second:first;
 const archived=archiveA.status===200?first:second;
 const survivorRevision=archiveA.status===200?1:2;
 const last=await http(operator.context,plan(survivor.body.plan_id)+'/archive','POST',
  {expected_revision:survivorRevision,reason:'S09 last visible guard'},operator.csrf,randomUUID());
 const afterLast=bridge('current');
 check('AC4 concurrent archive and last visible guard','one archive200, one409, then last-visible archive409 with one visible plan',
  'archive HTTP '+archiveCodes.join('/')+', last HTTP '+last.status+
  ', active/visible='+afterRace.active+'/'+afterRace.visible+', state equal='+String(afterRace.digest===afterLast.digest)+
  ', old revisions equal='+String(prefixEqual(beforeRaceA,afterRaceA)&&prefixEqual(beforeRaceB,afterRaceB)),
  archiveCodes[0]===200&&archiveCodes[1]===409&&last.status===409&&
  afterRace.visible===1&&afterLast.visible===1&&afterRace.digest===afterLast.digest&&
  prefixEqual(beforeRaceA,afterRaceA)&&prefixEqual(beforeRaceB,afterRaceB));
 const archivedRevisions=bridge('revisions',{plan:archived.body.plan_id});
 const activeTerms=survivor.body.plan_id===first.body.plan_id?revisedTerms:termsB;
 const occupied=await http(operator.context,base+'/plans','POST',
  {terms:{...activeTerms,prices:activeTerms.prices},reason:'S09 occupied devices'},operator.csrf,randomUUID());
 const archivedTerms=archived.body.plan_id===first.body.plan_id?revisedTerms:termsB;
 const reuse=await http(operator.context,base+'/plans','POST',
  {terms:archivedTerms,reason:'S09 archived slot reuse'},operator.csrf,randomUUID());
 const afterReuse=bridge('current');
 check('AC3/4 device conflict and archived slot','occupied devices409, archived slot reusable201, archived revision history retained',
  'occupied/reuse HTTP '+occupied.status+'/'+reuse.status+', archived revision rows='+archivedRevisions.length+
  ', visible='+afterReuse.visible,
  occupied.status===409&&reuse.status===201&&archivedRevisions.length>=2&&
  archivedRevisions.at(-1)?.archived===true&&afterReuse.visible===2);
 step='explicit hidden unlimited seed';
 const beforeSeed=bridge('current');
 const seeded=bridge('seed'),seedReplay=bridge('seed');
 const afterSeed=bridge('current');
 const clientAfterSeed=await http(customer.context,'/api/v1/catalogue');
 const operatorAfterSeed=await http(operator.context,base+'?page=1&per_page=50');
 check('AC2/6 explicit hidden unlimited seed','created once, replay no-op, metadata NULL, 7 devices/100 GiB hidden from client and present to operator',
  'created/replay='+seeded.created+'/'+seedReplay.created+', state delta='+
  (afterSeed.plans-beforeSeed.plans)+'/'+(afterSeed.revisions-beforeSeed.revisions)+
  ', client/operator counts='+clientAfterSeed.body?.plans?.length+'/'+operatorAfterSeed.body?.total+
  ', metadata null='+seeded.metadata?.actor_null+'/'+seeded.metadata?.reason_null,
  seeded.created===true&&seedReplay.created===false&&seeded.plan_id===seedReplay.plan_id&&
  seeded.revision===1&&seeded.metadata?.hidden===true&&seeded.metadata?.profile==='unlimited'&&
  seeded.metadata?.devices===7&&seeded.metadata?.traffic_gb==='100'&&
  seeded.metadata?.source==='unlimited_seed'&&seeded.metadata?.actor_null&&seeded.metadata?.reason_null&&
  afterSeed.plans===beforeSeed.plans+1&&afterSeed.revisions===beforeSeed.revisions+1&&
  clientAfterSeed.status===200&&clientAfterSeed.body?.plans?.length===afterSeed.visible&&
  !clientAfterSeed.body.plans.some(row=>row.plan_id===seeded.plan_id)&&
  operatorAfterSeed.status===200&&operatorAfterSeed.body?.total===afterSeed.plans&&
  operatorAfterSeed.body.plans.some(row=>row.plan_id===seeded.plan_id&&row.actor_account_id===null&&row.reason===null));
 step='operator browser create revise archive';
 await operator.page.goto(origin+'/admin/catalogue?lang=en');
 await expect(operator.page.getByRole('heading',{name:'Plans'})).toBeVisible();
 await operator.page.getByRole('button',{name:'New plan'}).click();
 await operator.page.getByLabel('Devices').fill('12');
 await operator.page.getByLabel('Traffic (GB)').fill('0');
 await operator.page.getByLabel('Period days').fill('30');
 await operator.page.getByLabel('RUB').fill('90071992547409.93');
 await operator.page.getByLabel('USD').fill('123.45');
 await operator.page.getByLabel('XTR').fill('0');
 await operator.page.getByLabel('Reason').fill('S09 UI create');
 const uiCreateResponse=operator.page.waitForResponse(response=>response.url().endsWith(base+'/plans')&&response.request().method()==='POST');
 await operator.page.getByRole('button',{name:'Create plan'}).click();
 const uiCreate=await uiCreateResponse,uiCreated=await uiCreate.json();
 const uiInitial=uiCreate.status()===201?bridge('revisions',{plan:uiCreated.plan_id}):[];
 const createdRow=operator.page.locator('article.admin-client').filter({hasText:'12 devices'});
 await expect(createdRow).toBeVisible();
 await createdRow.getByRole('button',{name:'Edit plan'}).click();
 await operator.page.getByLabel('Devices').fill('13');
 await operator.page.getByLabel('Reason').fill('S09 UI revision');
 const uiReviseResponse=operator.page.waitForResponse(response=>response.url().endsWith('/revision')&&response.request().method()==='POST');
 await operator.page.getByRole('button',{name:'Save revision'}).click();
 const uiRevise=await uiReviseResponse;
 const revisedRow=operator.page.locator('article.admin-client').filter({hasText:'13 devices'});
 await expect(revisedRow).toBeVisible();
 await revisedRow.getByRole('button',{name:'Edit plan'}).click();
 await operator.page.getByLabel('Reason').fill('S09 UI archive');
 await operator.page.getByRole('button',{name:'Archive plan'}).click();
 await expect(operator.page.getByRole('button',{name:'Confirm archive'})).toBeVisible();
 const uiArchiveResponse=operator.page.waitForResponse(response=>response.url().endsWith('/archive')&&response.request().method()==='POST');
 await operator.page.getByRole('button',{name:'Confirm archive'}).click();
 const uiArchive=await uiArchiveResponse;
 await expect(operator.page.locator('article.admin-client').filter({hasText:'13 devices'})).toContainText('Archived plan');
 const uiHistory=bridge('revisions',{plan:uiCreated.plan_id});
 const afterUI=bridge('current');
 check('AC1/3/7 operator real browser editor','375px admin creates/revises/archives with typed exact amount, reason and confirmation; immutable three revisions',
  'UI HTTP '+[uiCreate,uiRevise,uiArchive].map(x=>x.status()).join('/')+
  ', revisions='+uiHistory.length+', archived='+uiHistory.at(-1)?.archived+
  ', visible='+afterUI.visible,
  uiCreate.status()===201&&uiRevise.status()===200&&uiArchive.status()===200&&
  uiHistory.length===3&&uiHistory.at(-1)?.archived===true&&
  uiInitial.length===1&&uiHistory[0]?.terms_digest===uiInitial[0]?.terms_digest&&
  afterUI.visible===afterSeed.visible);
 await operator.page.goto(origin+'/admin/catalogue?lang=ru');
 await expect(operator.page.getByRole('heading',{name:'Тарифы'})).toBeVisible();
 const ruNew=operator.page.getByRole('button',{name:'Новый тариф'});
 await ruNew.focus();await expect(ruNew).toBeFocused();await operator.page.keyboard.press('Enter');
 await expect(operator.page.getByLabel('Причина')).toBeVisible();
 const operatorRuFits=await operator.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth);
 check('AC7 operator RU mobile keyboard','RU 375px admin keyboard opens labelled form without horizontal overflow',
  'RU focus=true, form=true, fit='+operatorRuFits,operatorRuFits);
 const finalTransport=bridge('transport');
 check('AC7 local runtime postflight','own Docker VPN/config and bot/reconcile boundary retained',
  'connected='+finalTransport.vpn_connected+', stopped='+finalTransport.bot_stopped+'/'+finalTransport.reconcile_stopped+
  ', config equal='+String(finalTransport.vpn_config_digest===transport.vpn_config_digest),
  finalTransport.vpn_connected&&finalTransport.bot_stopped&&finalTransport.reconcile_stopped&&
  finalTransport.vpn_config_digest===transport.vpn_config_digest);
}catch(error){
 writeSync(fd,JSON.stringify({criterion:'S09 browser driver interruption at '+step,target,command,
  expected:'all reached checks complete',actual:'driver or runtime error; inspect private bounded log',
  verdict:'BLOCKED',artifacts:[evidencePath]})+'\n');
 process.exitCode=1;
}finally{
 if(operator){try{bridge('revoke',{account:operator.id});}catch{}}
 if(outsider){try{bridge('revoke',{account:outsider.id});}catch{}}
 if(browser)await browser.close();
 closeSync(fd);
}
