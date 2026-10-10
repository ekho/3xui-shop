// Real Subscription operation cabinet and native-panel acceptance. Run only against a root-approved Subscription operation image.
// Fixture credentials stay in private files; evidence contains booleans/counts/digests only.
import {createRequire} from 'node:module';
import {readFileSync,mkdirSync,openSync,writeSync,closeSync,chmodSync,existsSync,renameSync} from 'node:fs';
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
const cert=readFileSync(root+'.superpowers/acceptance/local-docker/cert.pem');
const pin=createHash('sha256').update(new X509Certificate(cert).publicKey.export({type:'spki',format:'der'})).digest('base64');
const dir=root+'.superpowers/acceptance/subscription-operations';
mkdirSync(dir,{recursive:true,mode:0o700});chmodSync(dir,0o700);
const fixturePath=dir+'/fixtures.json';
const faultStatePath=dir+'/fault-state.json';
const continuationPath=dir+'/continuation.json';
const stage=process.argv[2];
if(!['setup','fixtures','compensation','assignment','reset','guards','recovery-expire','expired-recovery','ui-ru','ui-keyboard','fault-unavailable','fault-lost-reply','fault-restart-check',
 'fault-noack','fault-ack','fault-partial-start','fault-partial-reconcile'].includes(stage))throw Error('Subscription operation stage required');
const ready=process.env.ACCEPTANCE_RUNTIME_MANIFEST;
if(!ready||!existsSync(ready))throw Error('root-approved Subscription operation runtime manifest required');
const manifest=JSON.parse(readFileSync(ready));
if(!manifest||typeof manifest!=='object'||manifest.project!==ownedProject)
 throw Error('Subscription operation runtime manifest project mismatch');
const evidencePath=dir+'/'+stage+'.jsonl';
const fd=openSync(evidencePath,'wx',0o600);chmodSync(evidencePath,0o600);
const command='node deploy/subscription-operations/browser.mjs '+stage;
const target=`${ownedProject} HTTPS localhost:58443, own PostgreSQL/Mailpit and pinned 3X-UI 3.7.0`;
function record(criterion,expected,actual,pass){
 writeSync(fd,JSON.stringify({criterion,target,command,expected,actual,verdict:pass?'PASS':'FAIL',artifacts:[evidencePath]})+'\n');
 if(!pass)throw Error(criterion);
}
function blocked(criterion,expected,actual){
 writeSync(fd,JSON.stringify({criterion,target,command,expected,actual,verdict:'BLOCKED',artifacts:[evidencePath]})+'\n');
}
function bridge(action,data={}){
 return JSON.parse(execFileSync('python3',['deploy/subscription-operations/local.py'],{cwd:root,input:JSON.stringify({action,...data}),stdio:['pipe','pipe','pipe'],timeout:90000}).toString());
}
function mailJSON(path){return new Promise((resolve,reject)=>{
 https.get('https://localhost:59446'+path,{ca:cert},response=>{
  const chunks=[];response.on('data',chunk=>chunks.push(chunk));response.on('end',()=>{
   try{if(response.statusCode!==200)throw Error('Mailpit status');resolve(JSON.parse(Buffer.concat(chunks).toString()));}
   catch(error){reject(error);}
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
 throw Error('Subscription operation mail token timeout');
}
async function register(browser){
 const context=await browser.newContext({viewport:{width:375,height:812}}),page=await context.newPage();
 const email='subscription-operations-'+randomUUID()+'@example.test',password='Local Subscription operation '+randomUUID();
 await page.goto(origin+'/register?lang=en');
 await page.getByLabel('Email',{exact:true}).fill(email);
 await page.getByRole('checkbox').nth(0).check();await page.getByRole('checkbox').nth(1).check();
 await page.getByRole('button',{name:'Continue',exact:true}).click();
 const token=await mailToken(email);
 await page.goto(origin+'/verify-email?lang=en#token='+token);
 await page.getByLabel('Password',{exact:true}).fill(password);
 await page.getByRole('button',{name:'Verify email',exact:true}).click();
 await expect(page.getByText('Email verified. You can now sign in.')).toBeVisible();
 const result=await login(browser,{email,password},context,page);
 return {...result,email,password};
}
async function login(browser,credentials,existingContext,existingPage){
 const context=existingContext??await browser.newContext({viewport:{width:375,height:812}});
 const page=existingPage??await context.newPage();
 await page.goto(origin+'/login?lang=en');
 await page.getByLabel('Email',{exact:true}).fill(credentials.email);
 await page.getByLabel('Password',{exact:true}).fill(credentials.password);
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
  const req=https.request(origin+path,{method,ca:cert,headers:h,timeout:30000},response=>{
   const chunks=[];response.on('data',chunk=>chunks.push(chunk));response.on('end',()=>{
    try{const bytes=Buffer.concat(chunks),type=response.headers['content-type']??'';
     resolve({status:response.statusCode,body:type.includes('json')?JSON.parse(bytes.toString()):null});}
    catch(error){reject(error);}
   });
  });req.on('timeout',()=>req.destroy(Error('Subscription operation HTTPS timeout')));req.on('error',reject);req.end(payload??undefined);
 });
}
const base=id=>'/api/v1/operator/clients/'+id+'/access-operations';
const opPath=(id,op)=>base(id)+'/'+op;
const sleep=ms=>new Promise(resolve=>setTimeout(resolve,ms));
async function waitOperation(actor,account,op,expected=['applied']){
 const end=Date.now()+90000;
 do{
  const response=await http(actor.context,opPath(account,op));
  if(response.status!==200)throw Error('Subscription operation operation GET failed');
  if(expected.includes(response.body?.status))return response.body;
  if(response.body?.status==='needs_review')throw Error('Subscription operation unexpected needs_review');
  await sleep(400);
 }while(Date.now()<end);
 throw Error('Subscription operation operation did not settle');
}
async function approveTrial(actor,customer){
 const created=await http(customer.context,'/api/v1/trial-requests','POST',{comment:'Subscription operation owned native fixture'},customer.csrf,randomUUID());
 if(created.status!==201||!created.body?.request_id)throw Error('Subscription operation trial request rejected');
 const decided=await http(actor.context,'/api/v1/operator/trial-requests/'+created.body.request_id+'/decision',
  'POST',{decision:'approve',reason:''},actor.csrf,randomUUID());
 if(decided.status!==200)throw Error('Subscription operation trial approval rejected');
 const until=Date.now()+90000;
 do{
  const sub=await http(customer.context,'/api/v1/subscription');
  if(sub.status===200&&sub.body?.status==='active')return sub.body;
  await sleep(400);
 }while(Date.now()<until);
 throw Error('Subscription operation native trial not active');
}
function sameNative(before,after,fields){return fields.every(key=>before[key]===after[key]);}
function fixture(){
 if(!existsSync(fixturePath))throw Error('Subscription operation private fixtures absent');
 const value=JSON.parse(readFileSync(fixturePath));
 if(value.setup_complete!==true)throw Error('Subscription operation private setup incomplete');
 return value;
}
function privateFixture(value){const out=openSync(fixturePath,'wx',0o600);try{writeSync(out,JSON.stringify(value));}finally{closeSync(out);}chmodSync(fixturePath,0o600);}
function completeFixture(value){
 const temporary=fixturePath+'.complete-'+randomUUID();
 const out=openSync(temporary,'wx',0o600);
 try{writeSync(out,JSON.stringify({...value,setup_complete:true}));}finally{closeSync(out);}
 chmodSync(temporary,0o600);renameSync(temporary,fixturePath);
}
function faultState(){return existsSync(faultStatePath)?JSON.parse(readFileSync(faultStatePath)):{};}
function privateFault(value){const out=openSync(faultStatePath,'w',0o600);try{writeSync(out,JSON.stringify(value));}finally{closeSync(out);}chmodSync(faultStatePath,0o600);}
function continuation(){return existsSync(continuationPath)?JSON.parse(readFileSync(continuationPath)):{};}
function privateContinuation(value){
 const temporary=continuationPath+'.next-'+randomUUID();
 const out=openSync(temporary,'wx',0o600);
 try{writeSync(out,JSON.stringify(value));}finally{closeSync(out);}
 chmodSync(temporary,0o600);renameSync(temporary,continuationPath);
}

let browser,step='preflight';
try{
 const transport=bridge('transport');
 record('AC8 Subscription operation owned runtime guard','root manifest supplied, own project, native Telegram disabled, baseline VPN connected, bot/reconcile stopped',
  {manifest_present:true,own_project:transport.project===manifest.project,vpn:transport.vpn_connected,
   stopped:transport.bot_stopped&&transport.reconcile_stopped,telegram_disabled:transport.telegram_disabled},
  transport.project===manifest.project&&transport.vpn_connected&&transport.bot_stopped&&transport.reconcile_stopped&&transport.no_telegram_operators&&transport.telegram_disabled);
 browser=await chromium.launch({args:['--ignore-certificate-errors-spki-list='+pin]});
 if(stage==='setup'){
  if(existsSync(fixturePath))throw Error('Subscription operation fixtures already exist; do not overwrite');
  step='register owned accounts';
  const actor=await register(browser);
  const accounts={};
  for(const name of ['active','expired','missing','banned','perpetual','fault','unlimited'])accounts[name]=await register(browser);
  if(!bridge('grant',{account:actor.id}).role)throw Error('Subscription operation operator grant failed');
  record('AC7 owned roles and identities','eight fresh operations_bridge-UUID@example.test accounts and one operator role',
   {distinct:new Set([actor.id,...Object.values(accounts).map(x=>x.id)]).size===8,role:true},
   new Set([actor.id,...Object.values(accounts).map(x=>x.id)]).size===8);
  step='native trial fixtures';
  for(const name of ['active','expired','banned','perpetual','fault'])await approveTrial(actor,accounts[name]);
  bridge('fixture-unlimited',{account:accounts.unlimited.id,reference_account:accounts.active.id});
  const native={};for(const name of Object.keys(accounts))native[name]=bridge('snapshot',{account:accounts[name].id});
  record('AC1/2/8 controlled native baseline','five applied native trial clients, one direct owned unlimited negative fixture, one missing client',
   {native_count:Object.values(native).filter(x=>x.panel.exists&&x.panel.identity_matches).length,
    missing:!native.missing.panel.exists,unlimited_without_grant:native.unlimited.panel.exists&&native.unlimited.trial_grants===0,
    grants:Object.values(native).reduce((sum,x)=>sum+x.trial_grants,0)},
   Object.values(native).filter(x=>x.panel.exists&&x.panel.identity_matches).length===6&&
    !native.missing.panel.exists&&Object.values(native).reduce((sum,x)=>sum+x.trial_grants,0)===5);
  step='catalogue fixtures';
  const makeTerms=(devices,profile)=>({devices,traffic_gb:profile==='regular'?5:7,profile,hidden:true,
    periods:[30],prices:['RUB','USD','XTR'].map(currency=>({period_days:30,currency,amount_minor:'0'}))});
  const plans={};
  for(const [name,devices,profile] of [['regular',45,'regular'],['euru',46,'euru']]){
   const response=await http(actor.context,'/api/v1/operator/catalogue/plans','POST',
     {terms:makeTerms(devices,profile),reason:'Subscription operation own hidden plan'},actor.csrf,randomUUID());
   if(response.status!==201||response.body?.revision!==1)throw Error('Subscription operation plan fixture rejected');
   plans[name]={id:response.body.plan_id,revision:1,terms:makeTerms(devices,profile)};
  }
  const checkpoint={actor:{id:actor.id,email:actor.email,password:actor.password},
   accounts:Object.fromEntries(Object.entries(accounts).map(([name,a])=>[name,{id:a.id,email:a.email,password:a.password}])),
   plans,baseline_vpn_digest:transport.vpn_config_digest,setup_complete:false};
  privateFixture(checkpoint);
  const probe={};
  for(const name of ['active','banned','fault'])probe[name]=bridge('probe-config',
    {account:accounts[name].id,email:accounts[name].email,password:accounts[name].password});
  const faultTargets=bridge('fault-targets',{account:accounts.fault.id});
  completeFixture({...checkpoint,probe,faultTargets});
  record('AC3/4/8 Subscription operation private setup','two hidden ordinary plans and three private probe configs; exact fault descriptor saved',
   {plans:Object.keys(plans).length,probe_configs:Object.keys(probe).length,
    private_files:existsSync(fixturePath)&&existsSync(faultTargets.descriptor)},
   Object.keys(plans).length===2&&Object.keys(probe).length===3&&
    existsSync(fixturePath)&&existsSync(faultTargets.descriptor));
  process.stdout.write(JSON.stringify({stage:'setup',rows:4,fixture_file:fixturePath,
    probe_configs:Object.fromEntries(Object.entries(probe).map(([name,p])=>[name,p.config_path])),
    fault_descriptor:faultTargets.descriptor})+'\n');
 }
 if(['fixtures','compensation','assignment','reset','guards','recovery-expire','expired-recovery'].includes(stage)){
  const data=fixture();step='login existing owned fixtures';
  const actor=await login(browser,data.actor);
  if(actor.id!==data.actor.id)throw Error('Subscription operation operator identity changed');
  const customers={};for(const [name,credentials] of Object.entries(data.accounts)){
   customers[name]=await login(browser,credentials);
   if(customers[name].id!==credentials.id)throw Error('Subscription operation customer identity changed');
  }
  const nativeBefore={};for(const [name,c] of Object.entries(customers))nativeBefore[name]=bridge('snapshot',{account:c.id});
  if(stage==='fixtures'){
  step='expired native fixture';
  const expired=bridge('fixture',{account:customers.expired.id,kind:'expire'});
  step='perpetual native fixture';
  const perpetual=bridge('fixture',{account:customers.perpetual.id,kind:'perpetual'});
  step='VPN-ban native fixture';
  const banned=bridge('fixture',{account:customers.banned.id,kind:'ban'});
  record('AC1/2/4 native negative fixtures','expired/perpetual expiry and true VPN ban confirmed, no unrelated native changes',
   {expired:expired.after.expiry_ms<Date.now(),perpetual:perpetual.after.expiry_ms===0,
     banned:banned.after.enabled===false},
    expired.after.expiry_ms<Date.now()&&perpetual.after.expiry_ms===0&&banned.after.enabled===false);
  }
  if(stage==='compensation'){
  if(nativeBefore.active.access_operations||nativeBefore.expired.access_operations||nativeBefore.missing.access_operations)
   throw Error('Subscription operation compensation continuation already advanced');
  const expired={after:nativeBefore.expired.panel};
  const perpetual={after:nativeBefore.perpetual.panel};
  const banned={after:nativeBefore.banned.panel};
  if(!(expired.after.expiry_ms<Date.now()&&perpetual.after.expiry_ms===0&&banned.after.enabled===false&&
    nativeBefore.banned.vpn_banned&&nativeBefore.active.panel.used_traffic>0))
   throw Error('Subscription operation native fixture continuation precondition absent');
  const reject=[];
  for(const days of [0,366])reject.push(await http(actor.context,base(customers.active.id),'POST',
    {kind:'compensate',days,reason:'Subscription operation bound'},actor.csrf,randomUUID()));
  for(const name of ['perpetual','banned','unlimited'])reject.push(await http(actor.context,base(customers[name].id),'POST',
    {kind:'compensate',days:1,reason:'Subscription operation negative'},actor.csrf,randomUUID()));
  const negativeAfter={};for(const name of ['active','perpetual','banned','unlimited'])negativeAfter[name]=bridge('snapshot',{account:customers[name].id});
  record('AC1/2 finite compensation guards','days0/366 400, perpetual/VPN-banned/unlimited 409 and no access operations or panel changes',
   {codes:reject.map(r=>r.status),unchanged:['active','perpetual','banned','unlimited'].every(name=>
     negativeAfter[name].access_operations===nativeBefore[name].access_operations&&
     sameNative(negativeAfter[name].panel,name==='banned'?banned.after:name==='perpetual'?perpetual.after:nativeBefore[name].panel,
       ['identity_digest','expiry_ms','limit_ip','traffic_limit_bytes','membership_digest','used_traffic']))},
   reject.map(r=>r.status).join('/')==='400/400/409/409/409'&&
   ['active','perpetual','banned','unlimited'].every(name=>negativeAfter[name].access_operations===nativeBefore[name].access_operations));
  step='active finite compensation';
  const key=randomUUID(),input={kind:'compensate',days:2,reason:'Subscription operation active compensation'};
  const accepted=await http(actor.context,base(customers.active.id),'POST',input,actor.csrf,key);
  const replay=await http(actor.context,base(customers.active.id),'POST',input,actor.csrf,key);
  const conflict=await http(actor.context,base(customers.active.id),'POST',{...input,days:3},actor.csrf,key);
  if(accepted.status!==202||!accepted.body?.operation_id)throw Error('Subscription operation active compensation not accepted');
  const done=await waitOperation(actor,customers.active.id,accepted.body.operation_id);
  const applied=bridge('operation',{operation:done.operation_id});
  const after=bridge('snapshot',{account:customers.active.id});
  const expectedExpiry=Date.parse(accepted.body.desired.expires_at);
  record('AC1/5 active compensation exact target','202/replay202/body conflict409, one applied operation; +2d absolute expiry, identities/limits/counters/profile unchanged',
   {codes:[accepted,replay,conflict].map(r=>r.status),same_operation:accepted.body.operation_id===replay.body?.operation_id,
    applied:done.status==='applied',native_target:applied.panel_matches_target,
    expiry_equal:after.panel.expiry_ms===expectedExpiry,
    expiry_delta_ms:after.panel.expiry_ms-nativeBefore.active.panel.expiry_ms,
    preserved:sameNative(nativeBefore.active.panel,after.panel,
      ['identity_digest','limit_ip','traffic_limit_bytes','membership_digest','used_traffic','up','down']),
    operations_delta:after.access_operations-nativeBefore.active.access_operations,
    grants_delta:after.trial_grants-nativeBefore.active.trial_grants,
    audit:applied.requested_audit+'/'+applied.applied_audit},
   accepted.status===202&&replay.status===202&&conflict.status===409&&
   accepted.body.operation_id===replay.body?.operation_id&&done.status==='applied'&&applied.panel_matches_target&&
   after.panel.expiry_ms===expectedExpiry&&after.panel.expiry_ms-nativeBefore.active.panel.expiry_ms===2*86400000&&
   sameNative(nativeBefore.active.panel,after.panel,
     ['identity_digest','limit_ip','traffic_limit_bytes','membership_digest','used_traffic','up','down'])&&
   after.access_operations-nativeBefore.active.access_operations===1&&after.trial_grants===nativeBefore.active.trial_grants&&
   applied.requested_audit===1&&applied.applied_audit===1);
  step='expired finite compensation';
  const expiredPostTime=Date.now();
  const expiredOp=await http(actor.context,base(customers.expired.id),'POST',
    {kind:'compensate',days:1,reason:'Subscription operation expired compensation'},actor.csrf,randomUUID());
  if(expiredOp.status!==202||!expiredOp.body?.operation_id)throw Error('Subscription operation expired compensation not accepted');
  const expiredDone=await waitOperation(actor,customers.expired.id,expiredOp.body.operation_id);
  const expiredAfter=bridge('snapshot',{account:customers.expired.id});
  record('AC1 expired compensation basis','expired finite client gets now+1d, native identity/limits/counters unchanged',
    {applied:expiredDone.status==='applied',native_target:bridge('operation',{operation:expiredDone.operation_id}).panel_matches_target,
     base_from_now:expiredAfter.panel.expiry_ms>=expiredPostTime+86400000&&expiredAfter.panel.expiry_ms<=Date.now()+86400000,
     preserved:sameNative(expired.after,expiredAfter.panel,
       ['identity_digest','limit_ip','traffic_limit_bytes','membership_digest','used_traffic','up','down'])},
    expiredDone.status==='applied'&&expiredAfter.panel.expiry_ms>=expiredPostTime+86400000&&
    expiredAfter.panel.expiry_ms<=Date.now()+86400000&&
    sameNative(expired.after,expiredAfter.panel,
      ['identity_digest','limit_ip','traffic_limit_bytes','membership_digest','used_traffic','up','down']));
  step='missing bonus';
  const missingBefore=nativeBefore.missing;
  const bonusKey=randomUUID(),bonusInput={kind:'compensate',days:2,reason:'Subscription operation missing bonus'};
  const bonus=await http(actor.context,base(customers.missing.id),'POST',bonusInput,actor.csrf,bonusKey);
  if(bonus.status!==202||!bonus.body?.operation_id)throw Error('Subscription operation missing bonus not accepted');
  const bonusDone=await waitOperation(actor,customers.missing.id,bonus.body.operation_id);
  const bonusReplay=await http(actor.context,base(customers.missing.id),'POST',bonusInput,actor.csrf,bonusKey);
  const missingAfter=bridge('snapshot',{account:customers.missing.id});
  record('AC2 missing bonus once','one native bonus client, no first-trial grant, zero traffic cap, allocated key/UUID/subId preserved, configured server newly assigned, idempotent replay',
   {codes:[bonus,bonusReplay].map(r=>r.status),applied:bonusDone.status==='applied',exists:missingAfter.panel.exists,
    allocated_identity:missingAfter.panel.allocated_identity_digest===missingBefore.panel.allocated_identity_digest,
    assigned_server:missingBefore.panel.assigned_server_digest===null&&
      missingAfter.panel.assigned_server_digest===nativeBefore.active.panel.assigned_server_digest,
    traffic_limit:missingAfter.panel.traffic_limit_bytes,
    grants:missingAfter.trial_grants,operations:missingAfter.access_operations},
   bonus.status===202&&bonusReplay.status===202&&bonusDone.status==='applied'&&missingAfter.panel.exists&&
   missingAfter.panel.allocated_identity_digest===missingBefore.panel.allocated_identity_digest&&
   missingBefore.panel.assigned_server_digest===null&&
   missingAfter.panel.assigned_server_digest===nativeBefore.active.panel.assigned_server_digest&&
   missingAfter.panel.traffic_limit_bytes===0&&
   missingAfter.trial_grants===0&&missingAfter.access_operations===1);
  }
  if(stage==='assignment'){
  if(nativeBefore.active.access_operations!==1||nativeBefore.missing.access_operations!==1)
   throw Error('Subscription operation assignment continuation precondition absent');
  step='stale and current assignment';
  const regular=data.plans.regular;
  const assignInput={kind:'assign_plan',plan_id:regular.id,revision:regular.revision,
    period_days:30,reason:'Subscription operation hidden regular assignment'};
  const assign=await http(actor.context,base(customers.active.id),'POST',assignInput,actor.csrf,randomUUID());
  if(assign.status!==202||!assign.body?.operation_id)throw Error('Subscription operation assignment not accepted');
  const assignDone=await waitOperation(actor,customers.active.id,assign.body.operation_id);
  const assignNative=bridge('snapshot',{account:customers.active.id});
  const assignStore=bridge('operation',{operation:assignDone.operation_id});
  const revised=await http(actor.context,'/api/v1/operator/catalogue/plans/'+regular.id+'/revision','POST',
    {terms:{...regular.terms,traffic_gb:6},expected_revision:1,reason:'Subscription operation snapshot proof'},actor.csrf,randomUUID());
  const stale=await http(actor.context,base(customers.active.id),'POST',assignInput,actor.csrf,randomUUID());
  const assignAfterRevision=bridge('operation',{operation:assignDone.operation_id});
  record('AC3 hidden assignment and stale revision','hidden regular revision1 accepted; immutable desired survives catalogue revision2; stale revision409; reset/new period/limits applied',
   {codes:[assign,revised,stale].map(r=>r.status),applied:assignDone.status==='applied',target:assignStore.panel_matches_target,
    frozen:JSON.stringify(assignStore.desired)===JSON.stringify(assignAfterRevision.desired),
    plan:assignStore.desired.plan_id===regular.id&&assignStore.desired.revision===1,
    limit:assignNative.panel.traffic_limit_bytes,used:assignNative.panel.used_traffic},
   assign.status===202&&revised.status===200&&stale.status===409&&assignDone.status==='applied'&&
   assignStore.panel_matches_target&&JSON.stringify(assignStore.desired)===JSON.stringify(assignAfterRevision.desired)&&
   assignStore.desired.plan_id===regular.id&&assignStore.desired.revision===1&&
   assignNative.panel.traffic_limit_bytes===5*1024**3&&assignNative.panel.used_traffic===0);
  step='starter trial after grant';
  const starter=await http(actor.context,base(customers.active.id),'POST',
    {kind:'starter_trial',reason:'Subscription operation configured starter'},actor.csrf,randomUUID());
  if(starter.status!==202||!starter.body?.operation_id)throw Error('Subscription operation starter not accepted');
  const starterDone=await waitOperation(actor,customers.active.id,starter.body.operation_id);
  const starterNative=bridge('snapshot',{account:customers.active.id});
  record('AC3 starter without first-grant reset','existing trial grant retained, configured regular terms and native reset applied with same identity',
    {applied:starterDone.status==='applied',grants:starterNative.trial_grants,
     profile:starterDone.desired.profile,used:starterNative.panel.used_traffic,
     identity:starterNative.panel.identity_digest===nativeBefore.active.panel.identity_digest},
    starterDone.status==='applied'&&starterNative.trial_grants===nativeBefore.active.trial_grants&&
    starterDone.desired.profile==='regular'&&starterNative.panel.used_traffic===0&&
    starterNative.panel.identity_digest===nativeBefore.active.panel.identity_digest);
  privateContinuation({...continuation(),starter_operation:starterDone.operation_id});
  }
  if(stage==='reset'){
  const starterOperation=continuation().starter_operation;
  if(!starterOperation||nativeBefore.banned.access_operations)
   throw Error('Subscription operation reset continuation precondition absent');
  step='banned manual reset';
  const bannedBefore=bridge('snapshot',{account:customers.banned.id});
  const reset=await http(actor.context,base(customers.banned.id),'POST',
    {kind:'reset_traffic',reason:'Subscription operation banned reset'},actor.csrf,randomUUID());
  if(reset.status!==202||!reset.body?.operation_id)throw Error('Subscription operation reset not accepted');
  const resetDone=await waitOperation(actor,customers.banned.id,reset.body.operation_id);
  const resetAfter=bridge('snapshot',{account:customers.banned.id});
  record('AC4 native manual reset with true ban','nonzero counters become zero, ban/disabled and expiry/limits/identity/profile retained',
   {applied:resetDone.status==='applied',before_positive:bannedBefore.panel.used_traffic>0,
    after_zero:resetAfter.panel.used_traffic===0&&resetAfter.panel.up===0&&resetAfter.panel.down===0,
    true_ban:resetAfter.vpn_banned&&resetAfter.panel.enabled===false,
    preserved:sameNative(bannedBefore.panel,resetAfter.panel,
      ['identity_digest','expiry_ms','limit_ip','traffic_limit_bytes','membership_digest'])},
   resetDone.status==='applied'&&bannedBefore.panel.used_traffic>0&&
   resetAfter.panel.used_traffic===0&&resetAfter.panel.up===0&&resetAfter.panel.down===0&&
   resetAfter.vpn_banned&&resetAfter.panel.enabled===false&&
   sameNative(bannedBefore.panel,resetAfter.panel,
     ['identity_digest','expiry_ms','limit_ip','traffic_limit_bytes','membership_digest']));
  const sub=await http(customers.active.context,'/api/v1/subscription');
  record('AC7 current subscription reflects last applied access','Subscription subscription exposes latest access operation/status and confirmed current terms',
   {status:sub.status,operation_match:sub.body?.access_operation_id===starterOperation,
    operation_status:sub.body?.access_operation_status,
    expiry_present:!!sub.body?.expires_at},
   sub.status===200&&sub.body?.access_operation_id===starterOperation&&
   sub.body?.access_operation_status==='applied'&&!!sub.body?.expires_at);
  }
  if(stage==='guards'){
  const starterOperation=continuation().starter_operation;
  if(!starterOperation||nativeBefore.active.access_operations!==3)
   throw Error('Subscription operation guards continuation precondition absent');
  step='operator API guards';
  const beforeGuards=bridge('snapshot',{account:customers.active.id});
  const denied=await http(customers.missing.context,base(customers.active.id),'POST',
    {kind:'compensate',days:1,reason:'Subscription operation no role'},customers.missing.csrf,randomUUID());
  const forged=await http(actor.context,base(customers.active.id),'POST',
    {kind:'compensate',days:1,reason:'Subscription operation spoof',operator_account_id:customers.missing.id},actor.csrf,randomUUID());
  const csrf=await http(actor.context,base(customers.active.id),'POST',
    {kind:'compensate',days:1,reason:'Subscription operation CSRF'},'wrong',randomUUID());
  const originGuard=await http(actor.context,base(customers.active.id),'POST',
    {kind:'compensate',days:1,reason:'Subscription operation origin'},actor.csrf,randomUUID(),{Origin:'https://foreign.example.test'});
  const wrongPair=await http(actor.context,opPath(customers.missing.id,starterOperation));
  const badKind=await http(actor.context,base(customers.active.id),'POST',
    {kind:'reset_traffic',days:2,reason:'Subscription operation mismatched body'},actor.csrf,randomUUID());
  const badReason=await http(actor.context,base(customers.active.id),'POST',
    {kind:'compensate',days:1,reason:' '},actor.csrf,randomUUID());
  const roleBefore=bridge('grant',{account:customers.missing.id});
  const roleAfter=bridge('revoke',{account:customers.missing.id});
  const revoked=await http(customers.missing.context,base(customers.active.id),'POST',
    {kind:'compensate',days:1,reason:'Subscription operation revoked'},customers.missing.csrf,randomUUID());
  const afterGuards=bridge('snapshot',{account:customers.active.id});
  const codes=[denied,forged,csrf,originGuard,wrongPair,badKind,badReason,revoked].map(r=>r.status);
  record('AC7 role/foreign-account/CSRF/Origin/strict-input guards',
    'nonoperator and revoked403; actor spoof/body mismatch/reason400; CSRF/Origin403; foreign operation404; no target write',
    {codes,role_cycle:roleBefore.role&&!roleAfter.role,
     unchanged:afterGuards.access_operations===beforeGuards.access_operations&&
      sameNative(beforeGuards.panel,afterGuards.panel,
       ['identity_digest','expiry_ms','limit_ip','traffic_limit_bytes','membership_digest','used_traffic'])},
    codes.join('/')==='403/400/403/403/404/400/400/403'&&roleBefore.role&&!roleAfter.role&&
   afterGuards.access_operations===beforeGuards.access_operations&&
   sameNative(beforeGuards.panel,afterGuards.panel,
      ['identity_digest','expiry_ms','limit_ip','traffic_limit_bytes','membership_digest','used_traffic']));
  }
  if(stage==='expired-recovery'){
   step='expired recovery precondition';
   const before=nativeBefore.expired;
   const priorSub=await http(customers.expired.context,'/api/v1/subscription');
   if(!before.panel.exists||before.panel.enabled!==false||before.panel.expiry_ms>=Date.now()||
      before.panel.traffic_limit_bytes<=0||before.panel.used_traffic>=before.panel.traffic_limit_bytes||
      before.vpn_banned||priorSub.status!==200||priorSub.body?.status!=='needs_review')
    throw Error('Subscription operation disabled finite recovery precondition absent');
   step='expired recovery compensation';
   const key=randomUUID(),input={kind:'compensate',days:1,reason:'Subscription operation restore expired native access'};
   const postedAt=Date.now();
   const accepted=await http(actor.context,base(customers.expired.id),'POST',input,actor.csrf,key);
   const replay=await http(actor.context,base(customers.expired.id),'POST',input,actor.csrf,key);
   const conflict=await http(actor.context,base(customers.expired.id),'POST',
     {...input,days:2},actor.csrf,key);
   if(accepted.status!==202||!accepted.body?.operation_id)
    throw Error('Subscription operation expired recovery not accepted');
   const done=await waitOperation(actor,customers.expired.id,accepted.body.operation_id);
   const after=bridge('snapshot',{account:customers.expired.id});
   const stored=bridge('operation',{operation:accepted.body.operation_id});
   const sub=await http(customers.expired.context,'/api/v1/subscription');
   record('AC1 expired finite recovery enables actual access',
    'expired nonexhausted disabled client +1d from now; same-key replay one effect, native enabled and subscription active; identity/limits/counters/membership preserved',
    {codes:[accepted,replay,conflict].map(r=>r.status),same_operation:accepted.body.operation_id===replay.body?.operation_id,
     applied:done.status==='applied',native_target:stored.panel_matches_target,
     base_from_now:after.panel.expiry_ms>=postedAt+86400000&&
      after.panel.expiry_ms<=Date.now()+86400000,
     enabled:after.panel.enabled,subscription_http:sub.status,subscription_status:sub.body?.status,
     preserved:sameNative(before.panel,after.panel,
       ['identity_digest','limit_ip','traffic_limit_bytes','membership_digest','used_traffic','up','down']),
     operations_delta:after.access_operations-before.access_operations,
     audit:stored.requested_audit+'/'+stored.applied_audit},
    accepted.status===202&&replay.status===202&&conflict.status===409&&
    accepted.body.operation_id===replay.body?.operation_id&&done.status==='applied'&&
    stored.panel_matches_target&&after.panel.expiry_ms>=postedAt+86400000&&
    after.panel.expiry_ms<=Date.now()+86400000&&
    after.panel.enabled===true&&sub.status===200&&sub.body?.status==='active'&&
    sameNative(before.panel,after.panel,
      ['identity_digest','limit_ip','traffic_limit_bytes','membership_digest','used_traffic','up','down'])&&
    after.access_operations-before.access_operations===1&&
    stored.requested_audit===1&&stored.applied_audit===1);
  }
  if(stage==='recovery-expire'){
   step='prepare same expired client for recovery';
   const before=nativeBefore.expired;
   const priorSub=await http(customers.expired.context,'/api/v1/subscription');
   if(!before.panel.exists||before.panel.enabled!==false||before.panel.expiry_ms<=Date.now()||
      before.panel.traffic_limit_bytes<=0||before.panel.used_traffic>=before.panel.traffic_limit_bytes||
      before.vpn_banned||before.trial_grants!==1||before.access_operations!==1||
      priorSub.status!==200||priorSub.body?.status!=='disabled')
    throw Error('Subscription operation recovery-expire source precondition absent');
   const changed=bridge('recovery-expire',{account:customers.expired.id});
   const after=bridge('snapshot',{account:customers.expired.id});
   record('AC1 controlled expired recovery fixture',
    'same owned future-disabled nonexhausted client expiry only becomes past; identity/limits/counters/membership/ban unchanged and no new access operation',
    {expiry_past:changed.after.expiry_ms<Date.now(),disabled:changed.after.enabled===false,
     preserved:sameNative(before.panel,after.panel,
       ['identity_digest','limit_ip','traffic_limit_bytes','membership_digest','used_traffic','up','down']),
     ban_unchanged:after.vpn_banned===before.vpn_banned,
     operations_unchanged:after.access_operations===before.access_operations},
    changed.after.expiry_ms<Date.now()&&changed.after.enabled===false&&
    sameNative(before.panel,after.panel,
      ['identity_digest','limit_ip','traffic_limit_bytes','membership_digest','used_traffic','up','down'])&&
    after.vpn_banned===before.vpn_banned&&after.access_operations===before.access_operations);
  }
  process.stdout.write(JSON.stringify({stage,status:'completed'})+'\n');
 }
 if(stage==='ui-ru'){
  const data=fixture();const actor=await login(browser,data.actor);
  if(actor.id!==data.actor.id)throw Error('Subscription operation UI operator identity changed');
  const customer=data.accounts.active;
  step='UI ru focused';
  await actor.page.goto(origin+'/admin/clients/'+customer.id+'/show?lang=ru');
  const section=actor.page.getByRole('region',{name:'Операции доступа'});
  await expect(section).toBeVisible();
  const focus=section.getByLabel('Действие',{exact:true});
  await focus.focus();await expect(focus).toBeFocused();
  const fit=await actor.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth);
  record('AC7 RU real 375px operator keyboard',
   'Russian access form visible, actual Действие selector keyboard focus works and width fits',
   {visible:true,focused:true,fit},fit);
  process.stdout.write(JSON.stringify({stage:'ui-ru',rows:1})+'\n');
 }
 if(stage==='ui-keyboard'){
  const data=fixture();const actor=await login(browser,data.actor);
  if(actor.id!==data.actor.id)throw Error('Subscription operation UI keyboard operator identity changed');
  for(const [lang,regionName,operationName,daysName,reasonName] of [
   ['en','Access operations','Operation','Days to add','Reason'],
   ['ru','Операции доступа','Действие','Дней добавить','Причина операции']]){
   step='keyboard navigation '+lang;
   await actor.page.goto(origin+'/admin/clients/'+data.accounts.active.id+'/show?lang='+lang);
   const section=actor.page.getByRole('region',{name:regionName});
   await expect(section).toBeVisible();
   const operation=section.getByLabel(operationName,{exact:true});
   await operation.focus();await expect(operation).toBeFocused();
   await actor.page.keyboard.press('Tab');
   const days=section.getByLabel(daysName,{exact:true});
   await expect(days).toBeFocused();
   await actor.page.keyboard.press('Tab');
   const reason=section.getByLabel(reasonName,{exact:true});
   await expect(reason).toBeFocused();
   const fit=await actor.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth);
   record('AC7 '+lang.toUpperCase()+' real keyboard Tab order at 375px',
    'native operation select → keyboard Tab days input → keyboard Tab reason textarea; form visible and width fits',
    {visible:true,tab_days:true,tab_reason:true,fit},fit);
  }
  process.stdout.write(JSON.stringify({stage:'ui-keyboard',rows:2})+'\n');
 }
 if(stage.startsWith('fault-')){
  const data=fixture();step='fault fixture login';
  const actor=await login(browser,data.actor);
  const customer=await login(browser,data.accounts.fault);
  if(actor.id!==data.actor.id||customer.id!==data.accounts.fault.id)throw Error('Subscription operation fault actor mismatch');
  const old=faultState();
  if(stage==='fault-lost-reply'){
   step='masked successful reset reply';
   if(old.reset_operation||!process.env.FAULT_PROOF||!existsSync(process.env.FAULT_PROOF))
    throw Error('Subscription operation fresh fault and root gate proof required');
   const before=bridge('snapshot',{account:customer.id});
   if(before.panel.used_traffic<=0)throw Error('Subscription operation lost-reply counter precondition absent');
   const response=await http(actor.context,base(customer.id),'POST',
     {kind:'reset_traffic',reason:'Subscription operation upstream success reply masked'},actor.csrf,randomUUID());
   if(response.status!==202||!response.body?.operation_id)throw Error('Subscription operation masked reset not accepted');
   const observed=await waitOperation(actor,customer.id,response.body.operation_id,['needs_review']);
   const stored=bridge('operation',{operation:response.body.operation_id});
   const after=bridge('snapshot',{account:customer.id});
   privateFault({...old,reset_operation:response.body.operation_id,
    reset_identity_digest:before.panel.identity_digest,reset_before_used:before.panel.used_traffic});
   record('AC4/5 lost upstream success reply remains ambiguous',
    'root after-mode POST reset fault: native counter zero, same operation needs_review despite zero readback, no applied audit',
    {fault_proof_present:true,code:response.status,status:observed.status,
     before_positive:before.panel.used_traffic>0,native_zero:after.panel.used_traffic===0,
     reset_started:stored.reset_started,one_operation:after.access_operations-before.access_operations===1,
     audit:stored.requested_audit+'/'+stored.applied_audit,
     preserved:sameNative(before.panel,after.panel,
       ['identity_digest','expiry_ms','limit_ip','traffic_limit_bytes','membership_digest'])},
    response.status===202&&observed.status==='needs_review'&&before.panel.used_traffic>0&&
    after.panel.used_traffic===0&&stored.reset_started&&
    after.access_operations-before.access_operations===1&&
    stored.requested_audit===1&&stored.applied_audit===0&&
    sameNative(before.panel,after.panel,
      ['identity_digest','expiry_ms','limit_ip','traffic_limit_bytes','membership_digest']));
  }
  if(stage==='fault-unavailable'){
   step='read fault refusal';
   const before=bridge('snapshot',{account:customer.id});
   const result=await http(actor.context,base(customer.id),'POST',
     {kind:'compensate',days:1,reason:'Subscription operation unavailable read'},actor.csrf,randomUUID());
   const after=bridge('snapshot',{account:customer.id});
   record('AC2 unavailable panel target guard','root exact-key GET before-fault rejects action without operation or native change',
     {code:result.status,operations_delta:after.access_operations-before.access_operations,
      native_equal:sameNative(before.panel,after.panel,
       ['identity_digest','expiry_ms','limit_ip','traffic_limit_bytes','used_traffic','membership_digest'])},
     result.status===409&&after.access_operations===before.access_operations&&
     sameNative(before.panel,after.panel,
       ['identity_digest','expiry_ms','limit_ip','traffic_limit_bytes','used_traffic','membership_digest']));
  }
  if(stage==='fault-restart-check'){
   step='restart no blind retry';
   if(!old.reset_operation||!process.env.RESTART_PROOF||!existsSync(process.env.RESTART_PROOF))
    throw Error('Subscription operation root restart proof required');
   const observed=await http(actor.context,opPath(customer.id,old.reset_operation));
   const stored=bridge('operation',{operation:old.reset_operation});
   const after=bridge('snapshot',{account:customer.id});
   record('AC4/5 restarted worker does not repeat ambiguous reset',
    'after root fault-off, nonzero VPN probe and backend restart, same needs_review operation and traffic survive',
    {restart_proof_present:true,status:observed.body?.status,
     traffic_positive:after.panel.used_traffic>0,
     identity:after.panel.identity_digest===old.reset_identity_digest,
     reset_started:stored.reset_started,applied_audit:stored.applied_audit},
    observed.status===200&&observed.body?.status==='needs_review'&&after.panel.used_traffic>0&&
    after.panel.identity_digest===old.reset_identity_digest&&stored.reset_started&&stored.applied_audit===0);
   privateFault({...old,post_restart_used:after.panel.used_traffic});
  }
  if(stage==='fault-noack'){
   step='safe reconcile without destructive acknowledgement';
   if(!old.reset_operation||!old.post_restart_used)throw Error('Subscription operation post-restart state required');
   const body={reason:'Subscription operation readback without reset cost',acknowledge_reset_cost:false};
   const response=await http(actor.context,opPath(customer.id,old.reset_operation)+'/reconcile',
     'POST',body,actor.csrf,randomUUID());
   if(response.status!==202)throw Error('Subscription operation no-ack reconcile not accepted');
   const observed=await waitOperation(actor,customer.id,old.reset_operation,['needs_review']);
   const after=bridge('snapshot',{account:customer.id});
   record('AC4/6 no-ack reconcile preserves newly accumulated traffic',
    'API accepts safe readback (202) and remains needs_review with nonzero counter unchanged; no reset is repeated',
    {code:response.status,status:observed.status,counter_unchanged:after.panel.used_traffic===old.post_restart_used},
    response.status===202&&observed.status==='needs_review'&&
    after.panel.used_traffic===old.post_restart_used);
  }
  if(stage==='fault-ack'){
   step='explicit destructive reset reconciliation';
   if(!old.reset_operation||!old.post_restart_used)throw Error('Subscription operation reset operation absent');
   const before=bridge('snapshot',{account:customer.id});
   const response=await http(actor.context,opPath(customer.id,old.reset_operation)+'/reconcile',
     'POST',{reason:'Subscription operation explicit reset cost accepted',acknowledge_reset_cost:true},actor.csrf,randomUUID());
   if(response.status!==202)throw Error('Subscription operation explicit reconcile not accepted');
   const observed=await waitOperation(actor,customer.id,old.reset_operation);
   const after=bridge('snapshot',{account:customer.id});
   record('AC4/6 explicit reset-cost acknowledgement applies same operation',
    'true ack202 applies same operation; nonzero counters zeroed, identity/expiry/limits preserved',
    {code:response.status,status:observed.status,counter_zero:after.panel.used_traffic===0,
     same_identity:before.panel.identity_digest===after.panel.identity_digest,
     operations_unchanged:before.access_operations===after.access_operations,
     native_target:bridge('operation',{operation:old.reset_operation}).panel_matches_target},
    response.status===202&&observed.status==='applied'&&after.panel.used_traffic===0&&
    before.panel.identity_digest===after.panel.identity_digest&&
    sameNative(before.panel,after.panel,['expiry_ms','limit_ip','traffic_limit_bytes','membership_digest'])&&
    before.access_operations===after.access_operations);
  }
  if(stage==='fault-partial-start'){
   step='partial managed membership';
   if(!old.reset_operation)throw Error('Subscription operation reset fixture absent');
   const before=bridge('snapshot',{account:customer.id});
   const plan=data.plans.euru;
   const response=await http(actor.context,base(customer.id),'POST',
     {kind:'assign_plan',plan_id:plan.id,revision:plan.revision,period_days:30,
      reason:'Subscription operation partial euru membership'},actor.csrf,randomUUID());
   if(response.status!==202||!response.body?.operation_id)throw Error('Subscription operation euru assignment not accepted');
   const observed=await waitOperation(actor,customer.id,response.body.operation_id,['needs_review']);
   const stored=bridge('operation',{operation:response.body.operation_id});
   const conflict=await http(actor.context,base(customer.id),'POST',
     {kind:'compensate',days:1,reason:'Subscription operation conflicting write'},actor.csrf,randomUUID());
   privateFault({...old,partial_operation:response.body.operation_id,
     partial_identity_digest:before.panel.identity_digest});
   record('AC3/5/6 partial EURU assignment owns account',
    'root exact-key detach before-fault leaves needs_review, unmatched native target and rejects conflicting mutation',
    {codes:[response,conflict].map(r=>r.status),status:observed.status,
     target_mismatch:!stored.panel_matches_target,write_started:stored.write_started},
    response.status===202&&conflict.status===409&&observed.status==='needs_review'&&
    !stored.panel_matches_target&&stored.write_started);
  }
  if(stage==='fault-partial-reconcile'){
   step='complete partial membership after fault off';
   if(!old.partial_operation)throw Error('Subscription operation partial operation absent');
   const unmatched=bridge('operation',{operation:old.partial_operation});
   if(unmatched.panel_matches_target)throw Error('Subscription operation partial fault did not leave unmatched membership');
   const response=await http(actor.context,opPath(customer.id,old.partial_operation)+'/reconcile',
     'POST',{reason:'Subscription operation membership readback resolved',acknowledge_reset_cost:false},actor.csrf,randomUUID());
   if(response.status!==202)throw Error('Subscription operation partial reconcile not accepted');
   const observed=await waitOperation(actor,customer.id,old.partial_operation);
   const stored=bridge('operation',{operation:old.partial_operation});
   const after=bridge('snapshot',{account:customer.id});
   record('AC3/6 explicit EURU membership reconcile',
    'same operation applies saved EURU revision/period, native target and identity match after gate cleared',
    {native_mismatch_before:true,code:response.status,status:observed.status,profile:observed.desired?.profile,
     plan_revision:observed.desired?.revision,native_target:stored.panel_matches_target,
     identity:after.panel.identity_digest===old.partial_identity_digest},
    response.status===202&&observed.status==='applied'&&observed.desired?.profile==='euru'&&
    observed.desired?.revision===data.plans.euru.revision&&stored.panel_matches_target&&
    after.panel.identity_digest===old.partial_identity_digest);
  }
  process.stdout.write(JSON.stringify({stage,rows:1})+'\n');
 }
}catch(error){
 blocked('Subscription operation '+stage+' interruption','stage completes against root-approved runtime',
  {step,error_class:error?.name??'Error'});
 throw Error('Subscription operation '+stage+' acceptance stopped at '+step);
}finally{if(browser)await browser.close();closeSync(fd);}
