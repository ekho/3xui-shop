// Access profile owned local acceptance. Every stage needs a root-reviewed new runtime manifest.
// Private fixture files contain credentials; JSONL/stdout contain only safe facts.
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
const dir=root+'.superpowers/acceptance/access-profiles';
const oldFixture=root+'.superpowers/acceptance/subscription-operations/fixtures.json';
const fixturePath=dir+'/fixtures.json';
const stage=process.argv[2];
if(!['readiness','setup','finite','unlimited','ban','intent-trial','intent-bonus','guards','ui'].includes(stage))
 throw Error('Access profile implemented stage required');
const manifestPath=process.env.ACCEPTANCE_RUNTIME_MANIFEST;
if(!manifestPath||!existsSync(manifestPath))throw Error('root-reviewed new Access profile runtime manifest required');
const manifest=JSON.parse(readFileSync(manifestPath));
if(!manifest||manifest.project!==ownedProject||
   typeof manifest.revision!=='string'||manifest.revision.length!==40||
   !manifest.images?.backend?.startsWith('sha256:')||
   !manifest.images?.gateway?.startsWith('sha256:')||
   !manifest.images?.panel?.startsWith('sha256:')||
   manifest.health!==true||manifest.vpn_unchanged!==true||
   manifest.bot_stopped!==true||manifest.fault_mode!=='off')
 throw Error('root-reviewed Access profile runtime manifest incomplete');
mkdirSync(dir,{recursive:true,mode:0o700});chmodSync(dir,0o700);
const evidencePath=dir+'/'+stage+'.jsonl';
const fd=openSync(evidencePath,'wx',0o600);
const command='ACCEPTANCE_RUNTIME_MANIFEST='+manifestPath+' node deploy/access-profiles/browser.mjs '+stage;
const target=`${ownedProject} HTTPS localhost:58443, own PostgreSQL/Mailpit and pinned 3X-UI 3.7.0`;
let step='preflight',browser;
function evidence(criterion,expected,actual,verdict){
 writeSync(fd,JSON.stringify({criterion,target,command,expected,actual,verdict,artifacts:[evidencePath]})+'\n');
 if(verdict==='FAIL')throw Error(criterion);
}
function check(criterion,expected,actual,ok){evidence(criterion,expected,actual,ok?'PASS':'FAIL');}
function bridge(action,data={}){
 return JSON.parse(execFileSync('python3',['deploy/access-profiles/local.py'],
  {cwd:root,input:JSON.stringify({action,...data}),stdio:['pipe','pipe','pipe'],timeout:90000}).toString());
}
function old(){
 if(!existsSync(oldFixture))throw Error('Subscription operation private fixture absent');
 const value=JSON.parse(readFileSync(oldFixture));
 if(value.setup_complete!==true||!value.actor?.id||!value.accounts?.active?.id)
  throw Error('Subscription operation private fixture incomplete');
 return value;
}
function fixture(){
 if(!existsSync(fixturePath))throw Error('Access profile private checkpoint absent');
 const value=JSON.parse(readFileSync(fixturePath));
 if(value.runtime_revision!==manifest.revision)throw Error('Access profile checkpoint runtime mismatch');
 return value;
}
function writeFixture(value,initial=false){
 const path=initial?fixturePath:fixturePath+'.next-'+randomUUID();
 const out=openSync(path,'wx',0o600);
 try{writeSync(out,JSON.stringify(value));}finally{closeSync(out);}
 chmodSync(path,0o600);
 if(!initial)renameSync(path,fixturePath);
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
 throw Error('Access profile mail token timeout');
}
async function login(credentials,context,page){
 context??=await browser.newContext({viewport:{width:375,height:812}});
 page??=await context.newPage();
 await page.goto(origin+'/login?lang=en');
 await page.getByLabel('Email',{exact:true}).fill(credentials.email);
 await page.getByLabel('Password',{exact:true}).fill(credentials.password);
 await page.getByRole('button',{name:'Sign in',exact:true}).click();
 await expect(page).toHaveURL(/cabinet/);
 const me=await page.evaluate(async()=>await(await fetch('/api/v1/me')).json());
 if(credentials.id&&me.account.account_id!==credentials.id)throw Error('Access profile login identity mismatch');
 return {context,page,id:me.account.account_id,csrf:me.csrf_token};
}
async function register(credentials){
 const context=await browser.newContext({viewport:{width:375,height:812}}),page=await context.newPage();
 await page.goto(origin+'/register?lang=en');
 await page.getByLabel('Email',{exact:true}).fill(credentials.email);
 await page.getByRole('checkbox').nth(0).check();await page.getByRole('checkbox').nth(1).check();
 await page.getByRole('button',{name:'Continue',exact:true}).click();
 const token=await mailToken(credentials.email);
 await page.goto(origin+'/verify-email?lang=en#token='+token);
 await page.getByLabel('Password',{exact:true}).fill(credentials.password);
 await page.getByRole('button',{name:'Verify email',exact:true}).click();
 await expect(page.getByText('Email verified. You can now sign in.')).toBeVisible();
 return login(credentials,context,page);
}
async function http(context,path,method='GET',body,csrf,key,extra={}){
 const cookie=(await context.cookies(origin)).find(item=>item.name==='__Host-session');
 const payload=body===undefined?null:Buffer.from(JSON.stringify(body));
 const headers={Cookie:cookie?'__Host-session='+cookie.value:'',...extra};
 if(method!=='GET'&&!Object.keys(headers).some(name=>name.toLowerCase()==='origin'))headers.Origin=origin;
 if(payload){headers['Content-Type']='application/json';headers['Content-Length']=String(payload.length);}
 if(csrf)headers['X-CSRF-Token']=csrf;
 if(key)headers['Idempotency-Key']=key;
 return new Promise((resolve,reject)=>{
  const req=https.request(origin+path,{method,ca:cert,headers,timeout:30000},response=>{
   const chunks=[];response.on('data',chunk=>chunks.push(chunk));response.on('end',()=>{
    try{const bytes=Buffer.concat(chunks),type=response.headers['content-type']??'';
     resolve({status:response.statusCode,body:type.includes('json')?JSON.parse(bytes.toString()):null});}
    catch(error){reject(error);}
   });
  });req.on('timeout',()=>req.destroy(Error('Access profile HTTPS timeout')));req.on('error',reject);req.end(payload??undefined);
 });
}
const base=id=>'/api/v1/operator/clients/'+id+'/access-operations';
const sleep=ms=>new Promise(resolve=>setTimeout(resolve,ms));
async function waitOperation(actor,id,operation){
 const until=Date.now()+90000;
 do{
  const response=await http(actor.context,base(id)+'/'+operation);
  if(response.status!==200)throw Error('Access profile operation read unavailable');
  if(response.body?.status==='applied')return response.body;
  if(response.body?.status==='needs_review')throw Error('Access profile operation needs review');
  await sleep(400);
 }while(Date.now()<until);
 throw Error('Access profile operation pending timeout');
}
async function createApplied(actor,id,input){
 const accepted=await http(actor.context,base(id),'POST',input,actor.csrf,randomUUID());
 if(accepted.status!==202||!accepted.body?.operation_id)
  throw Error('Access profile expected 202 operation not accepted');
 return {accepted,done:await waitOperation(actor,id,accepted.body.operation_id)};
}
async function approveTrial(actor,customer){
 const request=await http(customer.context,'/api/v1/trial-requests','POST',
  {comment:'Access profile owned first trial'},customer.csrf,randomUUID());
 if(request.status!==201||!request.body?.request_id)throw Error('Access profile trial request rejected');
 const decision=await http(actor.context,'/api/v1/operator/trial-requests/'+request.body.request_id+'/decision',
  'POST',{decision:'approve',reason:''},actor.csrf,randomUUID());
 if(decision.status!==200)throw Error('Access profile trial decision rejected');
 const until=Date.now()+90000;
 do{
  const sub=await http(customer.context,'/api/v1/subscription');
  if(sub.status===200&&sub.body?.status==='banned'){
   const state=bridge('state',{account:customer.id});
   if(state.trial_grants===1&&state.assigned)return {request,decision,state};
  }
  await sleep(400);
 }while(Date.now()<until);
 throw Error('Access profile first trial did not provision');
}
async function catalogue(actor){
 const plans=[];let page=1,total=Infinity;
 while(plans.length<total){
  if(page>100)throw Error('Access profile catalogue pagination unexpected');
  const response=await http(actor.context,'/api/v1/operator/catalogue?page='+page+'&per_page=50');
  if(response.status!==200||response.body?.page!==page||response.body?.per_page!==50||
     !Array.isArray(response.body?.plans)||!Number.isInteger(response.body?.total))
   throw Error('Access profile operator catalogue contract mismatch');
  total=response.body.total;plans.push(...response.body.plans);page++;
 }
 if(plans.length!==total)throw Error('Access profile catalogue cardinality mismatch');
 return plans;
}
function same(nativeA,nativeB,fields){return fields.every(field=>nativeA[field]===nativeB[field]);}
function safeTransport(){
 const t=bridge('transport');
 check('AC8 Access profile root runtime transport guard','owned project, stopped bot/reconcile, empty Telegram operators and native Telegram disabled, connected and unchanged primary VPN',
  {project_match:t.project===manifest.project,bot_stopped:t.bot_stopped,reconcile_stopped:t.reconcile_stopped,
   telegram_empty:t.no_telegram_operators,telegram_disabled:t.telegram_disabled,vpn_connected:t.vpn_connected,
   primary_digest_match:t.vpn_config_digest===manifest.vpn_config_sha256},
  t.project===manifest.project&&t.bot_stopped&&t.reconcile_stopped&&t.no_telegram_operators&&t.telegram_disabled&&
  t.vpn_connected&&t.vpn_config_digest===manifest.vpn_config_sha256);
}
try{
 safeTransport();
 browser=await chromium.launch({args:['--ignore-certificate-errors-spki-list='+pin]});
 if(stage==='readiness'){
  step='read-only owned native and catalogue readiness';
  if(existsSync(fixturePath))throw Error('Access profile private checkpoint already exists');
  const source=old(),actor=await login(source.actor),finite=source.accounts.active;
  const [native,registry]=[bridge('snapshot',{account:finite.id}),bridge('registry')];
  const customer=await login(finite);
  const sub=await http(customer.context,'/api/v1/subscription');
  const all=await catalogue(actor);
  const unlimited=all.filter(plan=>plan.profile==='unlimited'&&plan.hidden&&
   plan.archived===false);
  const wanted={'local-regular-vless':1,'local-probe-vless':2,
   'local-euru-vless':3,'local-unlimited-vless':4};
  const confirmedRegular=native.access_profile==='regular'||
   native.access_profile===null&&native.latest_applied_profile==='regular';
  const ready=sub.status===200&&sub.body?.access_profile==='regular'&&
   sub.body?.status==='active'&&sub.body?.vpn_banned===false&&
   confirmedRegular&&native.unresolved_access===0&&
   native.panel.exists&&native.panel.identity_matches&&native.panel.managed_tags.join(',')==='local-regular-vless'&&
   native.panel.enabled&&native.panel.expiry_ms>Date.now()&&native.panel.used_traffic>0&&
   !native.vpn_banned&&!native.restricted&&unlimited.length===1&&
   unlimited[0].source==='unlimited_seed'&&
   Object.entries(wanted).every(([tag,id])=>registry[tag]===id);
  check('AC1/2/8 eligible existing finite and unlimited plan',
   'Subscription operation owned active finite regular, real native identity/counter, no pending write, one current hidden unlimited plan and exact four inbounds',
   {subscription_status:sub.status===200?sub.body?.status:'unavailable',
    known_regular:sub.body?.access_profile==='regular'&&confirmedRegular,
    confirmed_from_applied_target:native.access_profile===null&&native.latest_applied_profile==='regular',
    native_identity:native.panel.identity_matches,native_active:native.panel.enabled,
    nonzero_counter:native.panel.used_traffic>0,pending:native.unresolved_access,
    unlimited_plan_count:unlimited.length,
    seed_source:unlimited.length===1&&unlimited[0].source==='unlimited_seed',
    inbound_count:Object.keys(registry).length,
    exact_owned_inbound_ids:Object.entries(wanted).every(([tag,id])=>registry[tag]===id)},ready);
  const checkpoint={runtime_revision:manifest.revision,finite:{...finite},actor:{...source.actor},
   no_client:{trial:{email:'access-profiles-'+randomUUID()+'@example.test',password:'Local Access profile '+randomUUID()},
              bonus:{email:'access-profiles-'+randomUUID()+'@example.test',password:'Local Access profile '+randomUUID()}},
   unlimited_plan:{id:unlimited[0].plan_id,revision:unlimited[0].revision,
                   devices:unlimited[0].devices,traffic_gb:unlimited[0].traffic_gb},
   expected_inbounds:registry,setup_complete:false};
  writeFixture(checkpoint,true);
  check('AC4 Access profile pre-mutation private checkpoint',
   'existing owned actor/finite metadata plus two fresh account credentials persisted before registration/native writes',
   {checkpoint_exists:existsSync(fixturePath),new_account_slots:2,existing_finite:true},
   existsSync(fixturePath));
 }
 if(stage==='setup'){
  step='register exactly two checkpointed no-client accounts';
  let f=fixture();
  if(f.setup_complete)throw Error('Access profile setup already completed');
  for(const name of ['trial','bonus']){
   if(f.no_client[name].id)continue;
   const account=await register(f.no_client[name]);
   f.no_client[name].id=account.id;writeFixture(f);f=fixture();
  }
  const states=Object.fromEntries(['trial','bonus'].map(name=>[name,bridge('state',{account:f.no_client[name].id})]));
  const ok=Object.values(states).every(state=>state.access_profile==='regular'&&
   !state.vpn_banned&&!state.assigned&&!state.had_subscription&&!state.operator&&
   state.trial_grants===0&&state.access_operations===0&&state.unresolved_access===0);
  check('AC4 exactly two fresh no-client fixtures',
   'two distinct own web accounts with regular intent, no panel assignment/Grant/access write; credentials checkpointed before registration',
   {distinct:f.no_client.trial.id!==f.no_client.bonus.id,accounts:Object.keys(states).length,
    both_no_client:ok},ok&&f.no_client.trial.id!==f.no_client.bonus.id);
  f.setup_complete=true;writeFixture(f);
 }
 if(stage==='finite'){
  step='finite profile transition precondition';
  const f=fixture();if(!f.setup_complete)throw Error('Access profile setup incomplete');
  const actor=await login(f.actor),customer=await login(f.finite);
  const before=bridge('snapshot',{account:customer.id});
  const confirmedRegular=before.access_profile==='regular'||
   before.access_profile===null&&before.latest_applied_profile==='regular';
  if(!confirmedRegular||before.unresolved_access!==0||
     before.panel.managed_tags.join(',')!=='local-regular-vless'||
     !before.panel.unmanaged_present||!before.panel.enabled||before.panel.used_traffic<=0)
   throw Error('Access profile finite native precondition absent; root must attach unmanaged probe and confirm counter');
  const preserved=['identity_digest','assigned_server_digest','expiry_ms','limit_ip',
    'traffic_limit_bytes','used_traffic','up','down','unmanaged_membership_digest'];
  step='regular to EURU';
  const firstBody={kind:'set_profile',profile:'euru',reason:'Access profile owned finite profile'};
  const key=randomUUID();
  const accepted=await http(actor.context,base(customer.id),'POST',firstBody,actor.csrf,key);
  if(accepted.status!==202||!accepted.body?.operation_id)throw Error('Access profile EURU POST not accepted');
  const applied=await waitOperation(actor,customer.id,accepted.body.operation_id);
  const euru=bridge('snapshot',{account:customer.id});
  const replay=await http(actor.context,base(customer.id),'POST',firstBody,actor.csrf,key);
  const conflict=await http(actor.context,base(customer.id),'POST',
   {...firstBody,reason:'Access profile conflicting payload'},actor.csrf,key);
  check('AC1/5 regular to EURU and replay',
   'one applied EURU operation, managed membership changed, all native identity/limits/counters/ban/unmanaged retained; same key replay202 and conflict409',
   {post:accepted.status,operation_status:applied.status,profile:euru.access_profile,
    managed_tags:euru.panel.managed_tags,unmanaged:euru.panel.unmanaged_present,
    preserved:same(before.panel,euru.panel,preserved),replay:replay.status,
    replay_same:replay.body?.operation_id===accepted.body.operation_id,conflict:conflict.status},
   applied.desired?.profile==='euru'&&euru.access_profile==='euru'&&
   euru.panel.managed_tags.join(',')==='local-euru-vless'&&
   euru.panel.unmanaged_present&&same(before.panel,euru.panel,preserved)&&
   euru.vpn_banned===before.vpn_banned&&
   replay.status===202&&replay.body?.operation_id===accepted.body.operation_id&&conflict.status===409);
  step='EURU to regular';
  const second=await http(actor.context,base(customer.id),'POST',
   {kind:'set_profile',profile:'regular',reason:'Access profile return regular'},actor.csrf,randomUUID());
  if(second.status!==202||!second.body?.operation_id)throw Error('Access profile regular POST not accepted');
  const regularOp=await waitOperation(actor,customer.id,second.body.operation_id);
  const back=bridge('snapshot',{account:customer.id});
  const sub=await http(customer.context,'/api/v1/subscription');
  check('AC1 EURU to regular native and client state',
   'return to regular managed membership and subscription while identity/limits/counters/ban/unmanaged remain exactly preserved',
   {post:second.status,operation_status:regularOp.status,profile:back.access_profile,
    managed_tags:back.panel.managed_tags,unmanaged:back.panel.unmanaged_present,
    preserved:same(before.panel,back.panel,preserved),subscription_status:sub.status,
    subscription_profile:sub.body?.access_profile,ban_unchanged:back.vpn_banned===before.vpn_banned},
   regularOp.desired?.profile==='regular'&&back.access_profile==='regular'&&
   back.panel.managed_tags.join(',')==='local-regular-vless'&&back.panel.unmanaged_present&&
   same(before.panel,back.panel,preserved)&&back.vpn_banned===before.vpn_banned&&
  sub.status===200&&sub.body?.access_profile==='regular');
 }
 if(stage==='unlimited'){
  step='unlimited baseline and current hidden revision';
  const f=fixture();if(!f.setup_complete)throw Error('Access profile setup incomplete');
  const actor=await login(f.actor),customer=await login(f.finite);
  const before=bridge('snapshot',{account:customer.id});
  const plans=(await catalogue(actor)).filter(plan=>plan.profile==='unlimited'&&plan.hidden&&
   !plan.archived);
  if(before.access_profile!=='regular'||before.unresolved_access!==0||
     before.panel.managed_tags.join(',')!=='local-regular-vless'||
     !before.panel.unmanaged_present||!before.panel.enabled||plans.length!==1||
     plans[0].source!=='unlimited_seed'||
     plans[0].plan_id!==f.unlimited_plan.id||plans[0].revision!==f.unlimited_plan.revision)
   throw Error('Access profile unlimited native/catalogue precondition changed');
  const preserved=['identity_digest','assigned_server_digest','unmanaged_membership_digest'];
  step='assign hidden unlimited';
  const assigned=await createApplied(actor,customer.id,
   {kind:'set_profile',profile:'unlimited',reason:'Access profile own hidden unlimited'});
  const unlimited=bridge('snapshot',{account:customer.id});
  const clientUnlimited=await http(customer.context,'/api/v1/subscription');
  check('AC2 hidden unlimited applied',
   'exact current hidden revision; expiry0, inherited regular+unlimited, plan devices/traffic, current counters, native identity/unmanaged and ban preserved',
   {post:assigned.accepted.status,status:assigned.done.status,
    desired_plan_match:assigned.done.desired?.plan_id===f.unlimited_plan.id&&
      assigned.done.desired?.revision===f.unlimited_plan.revision,
    profile:unlimited.access_profile,expiry_zero:unlimited.panel.expiry_ms===0,
    managed_tags:unlimited.panel.managed_tags,
    counters_preserved:same(before.panel,unlimited.panel,['used_traffic','up','down']),
    limits_match:unlimited.panel.limit_ip===f.unlimited_plan.devices+1&&
      unlimited.panel.traffic_limit_bytes===f.unlimited_plan.traffic_gb*1024**3,
    native_identity_and_unmanaged:same(before.panel,unlimited.panel,preserved),
    ban_unchanged:unlimited.vpn_banned===before.vpn_banned,
    client_profile:clientUnlimited.body?.access_profile},
   assigned.done.desired?.plan_id===f.unlimited_plan.id&&
   assigned.done.desired?.revision===f.unlimited_plan.revision&&
   assigned.done.desired?.reset_traffic===false&&
   unlimited.access_profile==='unlimited'&&unlimited.panel.expiry_ms===0&&
   unlimited.panel.managed_tags.join(',')==='local-regular-vless,local-unlimited-vless'&&
   same(before.panel,unlimited.panel,['used_traffic','up','down'])&&
   unlimited.panel.limit_ip===f.unlimited_plan.devices+1&&
   unlimited.panel.traffic_limit_bytes===f.unlimited_plan.traffic_gb*1024**3&&
   same(before.panel,unlimited.panel,preserved)&&unlimited.vpn_banned===before.vpn_banned&&
   clientUnlimited.status===200&&clientUnlimited.body?.access_profile==='unlimited');
  step='revoke unlimited to EURU starter';
  const started=Date.now();
  const revoked=await createApplied(actor,customer.id,
   {kind:'set_profile',profile:'euru',reason:'Access profile revoke to selected EURU starter'});
  const after=bridge('snapshot',{account:customer.id});
  const clientFinite=await http(customer.context,'/api/v1/subscription');
  check('AC2 unlimited revoke to selected finite starter',
   'new finite starter expiry, reset0, EURU managed only, existing ban/identity/unmanaged retained',
   {post:revoked.accepted.status,status:revoked.done.status,profile:after.access_profile,
    expiry_future:after.panel.expiry_ms>started,managed_tags:after.panel.managed_tags,
    reset_zero:after.panel.used_traffic===0,
    identity_unmanaged:same(before.panel,after.panel,preserved),
    ban_unchanged:after.vpn_banned===before.vpn_banned,
    subscription_profile:clientFinite.body?.access_profile},
   revoked.done.desired?.profile==='euru'&&revoked.done.desired?.reset_traffic===true&&
   after.access_profile==='euru'&&after.panel.expiry_ms>started&&
   after.panel.managed_tags.join(',')==='local-euru-vless'&&after.panel.used_traffic===0&&
   same(before.panel,after.panel,preserved)&&after.vpn_banned===before.vpn_banned&&
   clientFinite.status===200&&clientFinite.body?.access_profile==='euru');
 }
 if(stage==='ban'){
  step='ban requires root-controlled nonzero traffic';
  const f=fixture();if(!f.setup_complete)throw Error('Access profile setup incomplete');
  const actor=await login(f.actor),customer=await login(f.finite);
  const before=bridge('snapshot',{account:customer.id});
  if(before.access_profile!=='euru'||before.unresolved_access!==0||
     before.vpn_banned||!before.panel.enabled||before.panel.used_traffic<=0)
   throw Error('Access profile ban native counter precondition absent');
  const unchanged=['identity_digest','assigned_server_digest','expiry_ms','limit_ip',
    'traffic_limit_bytes','membership_digest','unmanaged_membership_digest'];
  step='apply independent VPN ban';
  const banned=await createApplied(actor,customer.id,
   {kind:'set_vpn_ban',vpn_banned:true,reason:'Access profile own VPN ban'});
  const during=bridge('snapshot',{account:customer.id});
  const bannedSub=await http(customer.context,'/api/v1/subscription');
  check('AC3 native VPN ban overlay',
   'native disabled, ban true, subscription banned; account/support restriction and profile/limits/memberships/identity retained',
   {post:banned.accepted.status,status:banned.done.status,disabled:during.panel.enabled===false,
    profile:during.access_profile,ban:during.vpn_banned,subscription_status:bannedSub.body?.status,
    preserved:same(before.panel,during.panel,unchanged),
    account_restriction_unchanged:during.restricted===before.restricted,
    support_ban_unchanged:during.support_banned===before.support_banned},
   during.vpn_banned&&during.panel.enabled===false&&during.access_profile===before.access_profile&&
   same(before.panel,during.panel,unchanged)&&
   during.restricted===before.restricted&&during.support_banned===before.support_banned&&
   bannedSub.status===200&&bannedSub.body?.status==='banned');
  step='manual reset while banned';
  const reset=await createApplied(actor,customer.id,
   {kind:'reset_traffic',reason:'Access profile manual reset with ban retained'});
  const afterReset=bridge('snapshot',{account:customer.id});
  check('AC3 reset does not unban',
   'nonzero traffic becomes zero; native disabled and profile/limits/identity/memberships/ban remain',
   {post:reset.accepted.status,status:reset.done.status,
    counter_zero:afterReset.panel.used_traffic===0,disabled:afterReset.panel.enabled===false,
    vpn_banned:afterReset.vpn_banned,preserved:same(before.panel,afterReset.panel,unchanged)},
   afterReset.panel.used_traffic===0&&afterReset.panel.enabled===false&&
   afterReset.vpn_banned&&same(before.panel,afterReset.panel,unchanged));
  step='explicit unban';
  const unbanned=await createApplied(actor,customer.id,
   {kind:'set_vpn_ban',vpn_banned:false,reason:'Access profile explicit own unban'});
  const after=bridge('snapshot',{account:customer.id});
  const sub=await http(customer.context,'/api/v1/subscription');
  check('AC3 explicit unban',
   'native enabled only after explicit unban; finite subscription active and account/support state unchanged',
   {post:unbanned.accepted.status,status:unbanned.done.status,enabled:after.panel.enabled,
    ban_cleared:after.vpn_banned===false,profile:after.access_profile,
    subscription_status:sub.body?.status,
    account_restriction_unchanged:after.restricted===before.restricted,
    support_ban_unchanged:after.support_banned===before.support_banned},
   after.panel.enabled&&after.vpn_banned===false&&after.access_profile==='euru'&&
   sub.status===200&&sub.body?.status==='active'&&
   after.restricted===before.restricted&&after.support_banned===before.support_banned);
 }
 if(stage==='intent-trial'||stage==='intent-bonus'){
  const f=fixture();if(!f.setup_complete)throw Error('Access profile setup incomplete');
  const name=stage==='intent-trial'?'trial':'bonus';
  const actor=await login(f.actor),customer=await login(f.no_client[name]);
  step='no-client initial intent guard';
  const before=bridge('snapshot',{account:customer.id});
  if(before.panel.exists||before.access_profile!=='regular'||before.vpn_banned||
     before.had_subscription||before.trial_grants||before.unresolved_access)
   throw Error('Access profile no-client fresh fixture precondition absent');
  step='persist EURU and ban future intent';
  const profile=await createApplied(actor,customer.id,
   {kind:'set_profile',profile:'euru',reason:'Access profile no-client EURU intent'});
  const ban=await createApplied(actor,customer.id,
   {kind:'set_vpn_ban',vpn_banned:true,reason:'Access profile no-client ban overlay'});
  const intended=bridge('snapshot',{account:customer.id});
  const noClientSub=await http(customer.context,'/api/v1/subscription');
  check('AC4 no-client profile and ban intent',
   'both applied intent_saved, no panel client/Grant/subscription; confirmed future EURU and ban visible',
   {profile_status:profile.done.status,ban_status:ban.done.status,
    profile_step:profile.done.completed_steps?.includes('intent_saved'),
    ban_step:ban.done.completed_steps?.includes('intent_saved'),
    profile:intended.access_profile,ban:intended.vpn_banned,
    no_client:!intended.panel.exists,no_grant:intended.trial_grants===0,
    subscription_status:noClientSub.body?.status,
    subscription_profile:noClientSub.body?.access_profile,
    subscription_ban:noClientSub.body?.vpn_banned},
   profile.done.completed_steps?.includes('intent_saved')&&
   ban.done.completed_steps?.includes('intent_saved')&&
   intended.access_profile==='euru'&&intended.vpn_banned&&
   !intended.panel.exists&&intended.trial_grants===0&&
   noClientSub.status===200&&noClientSub.body?.status==='none'&&
   noClientSub.body?.access_profile==='euru'&&noClientSub.body?.vpn_banned===true);
  if(stage==='intent-trial'){
   step='first trial consumes banned profile';
   await approveTrial(actor,customer);
   const after=bridge('snapshot',{account:customer.id});
   const sub=await http(customer.context,'/api/v1/subscription');
   check('AC4 first trial consumes profile and ban once',
    'one first Grant, owned native EURU disabled by ban, identity valid and no extra access write',
    {grants:after.trial_grants,native_exists:after.panel.exists,
     native_identity:after.panel.identity_matches,managed_tags:after.panel.managed_tags,
     disabled:after.panel.enabled===false,ban:after.vpn_banned,
     subscription_status:sub.body?.status,access_writes:after.access_operations},
    after.trial_grants===1&&after.panel.exists&&after.panel.identity_matches&&
    after.panel.managed_tags.join(',')==='local-euru-vless'&&
    after.panel.enabled===false&&after.vpn_banned&&
    sub.status===200&&sub.body?.status==='banned'&&
    after.access_operations===intended.access_operations);
  }else{
   step='banned no-client compensation guard';
   const rejected=await http(actor.context,base(customer.id),'POST',
    {kind:'compensate',days:1,reason:'Access profile banned no-client guard'},actor.csrf,randomUUID());
   const denied=bridge('snapshot',{account:customer.id});
   check('AC4 banned no-client compensation guard',
    '409 before any native client/Grant/access write, future EURU/ban intent retained',
    {status:rejected.status,no_client:!denied.panel.exists,grants:denied.trial_grants,
     writes_unchanged:denied.access_operations===intended.access_operations,
     profile:denied.access_profile,ban:denied.vpn_banned},
    rejected.status===409&&!denied.panel.exists&&denied.trial_grants===0&&
    denied.access_operations===intended.access_operations&&
    denied.access_profile==='euru'&&denied.vpn_banned);
   step='explicit unban then first bonus';
   const unban=await createApplied(actor,customer.id,
    {kind:'set_vpn_ban',vpn_banned:false,reason:'Access profile explicit unban before bonus'});
   const bonus=await createApplied(actor,customer.id,
    {kind:'compensate',days:1,reason:'Access profile first bonus consumes EURU intent'});
   const after=bridge('snapshot',{account:customer.id});
   const sub=await http(customer.context,'/api/v1/subscription');
   check('AC4 unban then bonus consumes profile without Grant',
    'explicit unban applied; one bonus creates enabled EURU client using allocated identity, no first-trial Grant',
    {unban_status:unban.done.status,bonus_status:bonus.done.status,
     native_exists:after.panel.exists,native_identity:after.panel.identity_matches,
     enabled:after.panel.enabled,managed_tags:after.panel.managed_tags,
     profile:after.access_profile,ban:after.vpn_banned,grants:after.trial_grants,
     subscription_profile:sub.body?.access_profile},
    after.panel.exists&&after.panel.identity_matches&&after.panel.enabled&&
    after.panel.managed_tags.join(',')==='local-euru-vless'&&
    after.access_profile==='euru'&&!after.vpn_banned&&after.trial_grants===0&&
    sub.status===200&&sub.body?.access_profile==='euru');
  }
 }
 if(stage==='guards'){
  const f=fixture();if(!f.setup_complete)throw Error('Access profile setup incomplete');
  const actor=await login(f.actor),customer=await login(f.finite),outsider=await login(f.no_client.bonus);
  step='strict auth, origin, CSRF and body guards';
  const before=bridge('snapshot',{account:customer.id});
  if(before.unresolved_access||!before.panel.exists||before.access_profile!=='euru')
   throw Error('Access profile guard baseline requires settled EURU fixture');
  const input={kind:'set_profile',profile:'euru',reason:'Access profile same-state no-op'};
  const rejected=[
   await http(outsider.context,base(customer.id),'POST',input,outsider.csrf,randomUUID()),
   await http(actor.context,base(customer.id),'POST',{...input,operator_account_id:outsider.id},actor.csrf,randomUUID()),
   await http(actor.context,base(customer.id),'POST',input,'wrong',randomUUID()),
   await http(actor.context,base(customer.id),'POST',input,actor.csrf,randomUUID(),{Origin:'https://foreign.example.test'}),
   await http(actor.context,base(customer.id),'POST',{...input,days:1},actor.csrf,randomUUID()),
   await http(actor.context,base(customer.id),'POST',{...input,reason:' '},actor.csrf,randomUUID()),
   await http(actor.context,base(customer.id),'POST',{kind:'monthly_reset',reason:'Access profile forbidden system kind'},actor.csrf,randomUUID())
  ];
  const afterReject=bridge('snapshot',{account:customer.id});
  const codes=rejected.map(value=>value.status);
  check('AC5/7 strict actor, Origin, CSRF and body guards',
   'nonoperator403; actor spoof400; CSRF/foreign Origin403; extra field/blank reason/system kind400; no native/access mutation',
   {codes,access_write_unchanged:afterReject.access_operations===before.access_operations,
    native_unchanged:same(before.panel,afterReject.panel,
     ['identity_digest','expiry_ms','limit_ip','traffic_limit_bytes','membership_digest','used_traffic'])},
   codes.join('/')==='403/400/403/403/400/400/400'&&
   afterReject.access_operations===before.access_operations&&
   same(before.panel,afterReject.panel,
    ['identity_digest','expiry_ms','limit_ip','traffic_limit_bytes','membership_digest','used_traffic']));
  step='new-key state unchanged operation';
  const key=randomUUID(),noop=await http(actor.context,base(customer.id),'POST',input,actor.csrf,key);
  if(noop.status!==202||!noop.body?.operation_id)throw Error('Access profile no-op not accepted');
  const replay=await http(actor.context,base(customer.id),'POST',input,actor.csrf,key);
  const conflict=await http(actor.context,base(customer.id),'POST',
   {...input,reason:'Access profile conflicting same key'},actor.csrf,key);
  const after=bridge('snapshot',{account:customer.id});
  const stored=bridge('operation',{operation:noop.body.operation_id});
  check('AC5 no-op, replay and body conflict',
   'new key applied state_unchanged with zero River jobs/native write; replay same ID202, changed body409',
   {status:noop.body?.status,steps:noop.body?.completed_steps,
    job_count:stored.jobs,replay_status:replay.status,
    same_operation:replay.body?.operation_id===noop.body.operation_id,
    conflict_status:conflict.status,
    native_unchanged:same(before.panel,after.panel,
     ['identity_digest','expiry_ms','limit_ip','traffic_limit_bytes','membership_digest','used_traffic'])},
   noop.body?.status==='applied'&&noop.body?.completed_steps?.includes('state_unchanged')&&
   stored.jobs===0&&replay.status===202&&replay.body?.operation_id===noop.body.operation_id&&
   conflict.status===409&&same(before.panel,after.panel,
   ['identity_digest','expiry_ms','limit_ip','traffic_limit_bytes','membership_digest','used_traffic']));
 }
 if(stage==='ui'){
  const f=fixture();if(!f.setup_complete)throw Error('Access profile setup incomplete');
  const actor=await login(f.actor),customer=await login(f.finite);
  const state=bridge('state',{account:customer.id});
  if(state.access_profile!=='euru'||state.vpn_banned||state.unresolved_access)
   throw Error('Access profile UI current-state precondition absent');
  for(const labels of [
   {lang:'en',region:'Access operations',operation:'Operation',profile:'Profile',
    reason:'Reason',changeProfile:'Change profile',confirmProfile:'Confirm profile change',
    changeBan:'Change VPN ban',ban:'VPN ban',confirmBan:'Confirm VPN ban',
    reset:'Reset traffic',confirmReset:'Confirm reset',access:'Access profile',vpn:'VPN access',
    allowed:'VPN access allowed',profileWarning:/hidden plan.*monthly reset.*traffic counters.*VPN ban/i,
    banWarning:/Account and support restrictions stay separate/i,
    resetWarning:/VPN ban remains/i},
   {lang:'ru',region:'Операции доступа',operation:'Действие',profile:'Профиль',
    reason:'Причина операции',changeProfile:'Сменить профиль',confirmProfile:'Подтвердить смену профиля',
    changeBan:'Изменить VPN-блокировку',ban:'VPN-блокировка',confirmBan:'Подтвердить VPN-блокировку',
    reset:'Сбросить трафик',confirmReset:'Подтвердить сброс',access:'Профиль доступа',vpn:'VPN-доступ',
    allowed:'VPN-доступ разрешён',profileWarning:/скрытого тарифа.*ежемесячный сброс.*счётчики.*VPN-блокировка/i,
    banWarning:/Ограничения аккаунта и поддержки останутся отдельными/i,
    resetWarning:/VPN-блокировка сохранится/i}]){
   step='operator RU/EN 375px keyboard and confirmation '+labels.lang;
   await actor.page.goto(origin+'/admin/clients/'+customer.id+'/show?lang='+labels.lang);
   const section=actor.page.getByRole('region',{name:labels.region});
   await expect(section).toBeVisible();
   const operation=section.getByLabel(labels.operation,{exact:true});
   await operation.selectOption('set_profile');
   await operation.focus();await expect(operation).toBeFocused();
   await actor.page.keyboard.press('Tab');
   const profile=section.getByLabel(labels.profile,{exact:true});
   await expect(profile).toBeFocused();
   await profile.selectOption('unlimited');
   await actor.page.keyboard.press('Tab');
   const reason=section.getByLabel(labels.reason,{exact:true});
   await expect(reason).toBeFocused();
   await reason.fill('Access profile read-only confirmation check');
   await section.getByRole('button',{name:labels.changeProfile,exact:true}).click();
   await expect(section.getByRole('button',{name:labels.confirmProfile,exact:true})).toBeVisible();
   await expect(section.locator('.access-create > .warning')).toContainText(labels.profileWarning);
   await section.getByRole('button',{name:labels.lang==='ru'?'Отмена':'Cancel',exact:true}).click();
   await operation.selectOption('set_vpn_ban');
   await section.getByLabel(labels.ban,{exact:true}).selectOption('true');
   await section.getByRole('button',{name:labels.changeBan,exact:true}).click();
   await expect(section.getByRole('button',{name:labels.confirmBan,exact:true})).toBeVisible();
   await expect(section.locator('.access-create > .warning')).toContainText(labels.banWarning);
   await section.getByRole('button',{name:labels.lang==='ru'?'Отмена':'Cancel',exact:true}).click();
   await operation.selectOption('reset_traffic');
   await section.getByRole('button',{name:labels.reset,exact:true}).click();
   await expect(section.getByRole('button',{name:labels.confirmReset,exact:true})).toBeVisible();
   await expect(section.locator('.access-create > .warning')).toContainText(labels.resetWarning);
   const operatorFits=await actor.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth);
   step='client RU/EN profile and independent ban '+labels.lang;
   await customer.page.goto(origin+'/cabinet?lang='+labels.lang);
   const clientProfile=await customer.page.locator('dt').filter({hasText:labels.access}).first()
    .locator('..').locator('dd').textContent();
   const clientBan=await customer.page.locator('dt').filter({hasText:labels.vpn}).first()
    .locator('..').locator('dd').textContent();
   const clientFits=await customer.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth);
   check('AC7 '+labels.lang.toUpperCase()+' real UI and keyboard',
    'real operator form at 375px: Tab operation→profile→reason, profile/ban/reset confirmations; client current EURU and VPN allowed shown separately, no write',
    {region_visible:true,tab_profile:true,tab_reason:true,
     profile_confirmation:true,ban_confirmation:true,reset_confirmation:true,
     operator_fits:operatorFits,client_profile_euru:clientProfile?.trim()==='EURU',
     client_ban_allowed:clientBan?.trim()===labels.allowed,client_fits:clientFits},
    operatorFits&&clientProfile?.trim()==='EURU'&&clientBan?.trim()===labels.allowed&&clientFits);
  }
 }
 process.stdout.write(JSON.stringify({stage,result:'completed',evidence:evidencePath})+'\n');
}catch(error){
 evidence('Access profile stage stopped at '+step,'approved precondition or checked native/API result',
  {error_class:error?.name??'Error',step},'BLOCKED');
 process.stderr.write('Access profile '+stage+' BLOCKED at '+step+'\n');
 process.exitCode=1;
}finally{
 if(browser)await browser.close();closeSync(fd);
}
