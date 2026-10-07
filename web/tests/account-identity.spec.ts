import {test,expect,type Page,type Route} from '@playwright/test';
import type {components} from '../src/api/schema.gen';
type Model<K extends keyof components['schemas']>=components['schemas'][K];
const id='11111111-1111-4111-8111-111111111111',csrf='c'.repeat(43),miniToken='mini_'+'b'.repeat(43),linkToken='k'.repeat(43),launch='owned-signed-identity';
const webProfile:Model<'AccountResult'>={account:{account_id:id,email:'owner@example.test',email_verified:true,locale:'en',telegram_linked:false},csrf_token:csrf,capabilities:{trial_available:false}};
const miniProfile:Model<'MiniAppAccountResult'>={account:{account_id:id,email:null,email_verified:false,display_name:'Mini owner',telegram_id:444,telegram_linked:true,locale:'en'},csrf_token:csrf,capabilities:{trial_available:false}};
const none:Model<'Subscription'>={status:'none',devices:0,traffic_limit_bytes:0,traffic_used_bytes:null,observed_at:null,data_stale:true,expires_at:null,access_profile:'unknown',vpn_banned:false,access_operation_id:null,access_operation_status:null};
type Call={path:string;method:string;body:any;authorization?:string;csrf?:string};
async function identityFixture(page:Page,options:{mini?:boolean;first?:boolean;retired?:boolean;linked?:boolean;extra?:(r:Route,path:string)=>Promise<boolean>}={}){
 const calls:Call[]=[];let linked=!!options.linked,pending:Model<'IdentityEmailPending'>|null=null;
 await page.route('https://telegram.org/js/telegram-web-app.js',r=>r.fulfill({contentType:'application/javascript',body:`window.Telegram={WebApp:{initData:new URLSearchParams(location.hash.slice(1)).get('tgWebAppData')||${JSON.stringify(launch)},ready(){},expand(){}}};`}));
 await page.route('**/api/v1/**',async r=>{
  const req=r.request(),path=new URL(req.url()).pathname;let body:any;try{body=req.postDataJSON()}catch{}
  calls.push({path,method:req.method(),body,authorization:req.headers().authorization,csrf:req.headers()['x-csrf-token']});
  if(options.extra&&await options.extra(r,path))return;
  if(path==='/api/v1/telegram/mini-app/session'){
   if((options.first||options.retired)&&!linked)return r.fulfill({status:409,json:{error:{code:options.retired?'TELEGRAM_UNLINKED':'CONSENT_REQUIRED',message:'pending',request_id:''}}});
   return r.fulfill({json:{...miniProfile,...(linked?{account:{...miniProfile.account,email:webProfile.account.email,email_verified:true}}:{}),session_token:miniToken}});
  }
  if(path==='/api/v1/telegram/link'){linked=true;return r.fulfill({json:{linked:true}});}
  if(path==='/api/v1/me')return r.fulfill({json:webProfile});
  if(path==='/api/v1/telegram/mini-app/account')return r.fulfill({json:miniProfile});
  if(path==='/api/v1/auth/session')return r.fulfill({json:{csrf_token:csrf}});
  if(path==='/api/v1/me/identity'){
   const identity:Model<'IdentityContext'>={email:options.mini?null:webProfile.account.email,source_kind:options.mini?'telegram':'web',independent_login:!options.mini,telegram_linked:!!options.mini||linked,can_unlink:!options.mini&&linked,unlink_blocked_reason:options.mini?'INDEPENDENT_LOGIN_REQUIRED':null,pending_initial_email:pending};
   return r.fulfill({json:identity});
  }
  if(path==='/api/v1/telegram/initial-email'){pending={challenge_id:id,email:body.email,expires_at:new Date(Date.now()+600000).toISOString()};return r.fulfill({status:202,json:{challenge_id:id,resend_after:60}});}
  if(path==='/api/v1/telegram/initial-email/confirm')return r.fulfill({json:{verified:true}});
  if(path==='/api/v1/me/telegram/link')return r.fulfill({json:{link_token:linkToken,expires_at:new Date(Date.now()+600000).toISOString()}});
  if(path==='/api/v1/me/telegram/unlink'){linked=false;return r.fulfill({json:{changed:true}});}
  if(path==='/api/v1/auth/logout'||path==='/api/v1/telegram/mini-app/logout')return r.fulfill({status:204});
  if(path==='/api/v1/subscription')return r.fulfill({json:none});
  if(path==='/api/v1/orders/current')return r.fulfill({json:{order:null,can_purchase:false}});
  if(path==='/api/v1/trial-requests/current')return r.fulfill({json:{request:null}});
  return r.fulfill({status:404,json:{error:{code:'INVALID_INPUT',message:'missing',request_id:''}}});
 });
 return calls;
}
const storage=(page:Page)=>page.evaluate(()=>JSON.stringify(localStorage)+JSON.stringify(sessionStorage));

test('Mini first launch links an existing cabinet before creating an account',async({page})=>{
 const calls=await identityFixture(page,{mini:true,first:true});await page.goto('/mini-app?lang=en#tgWebAppData='+encodeURIComponent(launch));
 await page.getByRole('button',{name:'I already have a cabinet',exact:true}).click();
 await page.getByLabel('Link code',{exact:true}).fill(linkToken);
 const submit=page.getByRole('button',{name:'Link Telegram',exact:true});await expect(submit).toBeDisabled();
 await page.getByRole('checkbox',{name:/terms of use/i}).check();await page.getByRole('checkbox',{name:/privacy/i}).check();await submit.click();
 await expect(page.getByRole('heading',{name:'Mini owner'})).toBeVisible();
 const link=calls.filter(c=>c.path==='/api/v1/telegram/link');expect(link).toHaveLength(1);expect(link[0].authorization).toBeUndefined();expect(link[0].body).toEqual({init_data:launch,link_token:linkToken,accepted_terms_version:'1',accepted_privacy_version:'1'});
 expect(calls.filter(c=>c.path.endsWith('/mini-app/session')).every(c=>!c.body.accepted_terms_version)).toBe(true);
 expect(page.url()).not.toMatch(/owned-signed|kkkk|mini_/);expect(await storage(page)).not.toMatch(/owned-signed|kkkk|mini_|csrf/);
});
test('Mini identity adds independent credentials with signed-session CSRF and explicit consent',async({page})=>{
 const calls=await identityFixture(page,{mini:true});await page.goto('/mini-app/cabinet/identity?lang=en');
 await expect(page.getByRole('heading',{name:'Sign-in methods',exact:true})).toBeVisible();
 await page.getByLabel('Email',{exact:true}).fill('new@example.test');await page.getByRole('button',{name:'Send confirmation code',exact:true}).click();
 await page.getByLabel('Confirmation code',{exact:true}).fill('12345678');await page.getByLabel('New password',{exact:true}).fill('independent long password ✨');await page.getByLabel('Repeat password',{exact:true}).fill('independent long password ✨');
 const submit=page.getByRole('button',{name:'Enable email sign-in',exact:true});await expect(submit).toBeDisabled();
 await page.getByRole('checkbox',{name:/terms of use/i}).check();await page.getByRole('checkbox',{name:/privacy/i}).check();await submit.click();
 await expect(page.getByRole('alert')).toContainText('Email sign-in enabled');
 const sent=calls.find(c=>c.path.endsWith('/initial-email/confirm'))!;expect(sent.authorization).toBe('Bearer '+miniToken);expect(sent.csrf).toBe(csrf);expect(sent.body.challenge_id).toBe(id);expect(sent.body.new_password).toBe('independent long password ✨');
 expect(await storage(page)).not.toMatch(/12345678|independent long|mini_|csrf/);
});
test('Browser identity issues only a memory link code after the current password',async({page})=>{
 const calls=await identityFixture(page);await page.goto('/cabinet/identity?lang=en');
 await expect(page.getByRole('heading',{name:'Sign-in methods',exact:true})).toBeVisible();
 await page.getByLabel('Current password',{exact:true}).fill('current long password ✨');await page.getByRole('button',{name:'Get link code',exact:true}).click();
 await expect(page.getByLabel('Link code',{exact:true})).toHaveValue(linkToken);expect(calls.find(c=>c.path==='/api/v1/me/telegram/link')?.csrf).toBe(csrf);
 await expect(page.getByLabel('Current password',{exact:true})).toHaveValue('');expect(page.url()).not.toContain(linkToken);expect(await storage(page)).not.toMatch(/kkkk|current long|csrf/);
 await page.getByRole('link',{name:'Back to cabinet',exact:true}).click();await expect(page.getByLabel('Link code',{exact:true})).toHaveCount(0);
});
test('Browser unlink requires independent credentials and ends the session',async({page})=>{
 const calls=await identityFixture(page,{linked:true});await page.goto('/cabinet/identity?lang=en');
 await page.getByLabel('Current password',{exact:true}).fill('current long password ✨');await page.getByRole('button',{name:'Unlink Telegram',exact:true}).click();
 await expect(page).toHaveURL(/\/login\?lang=en$/);expect(calls.find(c=>c.path==='/api/v1/me/telegram/unlink')?.csrf).toBe(csrf);expect(await storage(page)).not.toMatch(/current long|csrf/);
});
test('Retired Telegram identity offers linking instead of silent new registration',async({page})=>{
 const calls=await identityFixture(page,{mini:true,retired:true});await page.goto('/mini-app?lang=en');await expect(page.getByLabel('Link code',{exact:true})).toBeVisible();
 await expect(page.getByRole('button',{name:'Create a new account',exact:true})).toHaveCount(0);expect(calls).toHaveLength(1);
});
test('Identity load can be retried and Russian controls stay keyboard accessible',async({page})=>{
 let failed=false;await identityFixture(page,{extra:async(r,path)=>{if(path==='/api/v1/me/identity'&&!failed){failed=true;await r.fulfill({status:503,json:{error:{code:'SERVICE_UNAVAILABLE',message:'retry',request_id:''}}});return true}return false}});
 await page.goto('/cabinet/identity');await expect(page.getByRole('alert')).toBeVisible();await page.getByRole('button',{name:'Повторить',exact:true}).click();
 await expect(page.getByRole('heading',{name:'Способы входа',exact:true})).toBeVisible();await page.getByLabel('Текущий пароль',{exact:true}).focus();await page.keyboard.type('current long password');await page.keyboard.press('Tab');await expect(page.getByRole('button',{name:'Получить код привязки',exact:true})).toBeFocused();
});

for(const failure of [{status:409,code:'IDENTITY_CONFLICT',copy:'already belongs'},{status:400,code:'INVALID_VERIFICATION',copy:'expired'}])test('Mini link '+failure.code+' keeps a retryable existing-account flow',async({page})=>{
 let deny=true;const calls=await identityFixture(page,{mini:true,first:true,extra:async(r,path)=>{if(path==='/api/v1/telegram/link'&&deny){await r.fulfill({status:failure.status,json:{error:{code:failure.code,message:'denied',request_id:''}}});return true}return false}});
 await page.goto('/mini-app?lang=en');await page.getByRole('button',{name:'I already have a cabinet',exact:true}).click();await page.getByLabel('Link code',{exact:true}).fill(linkToken);
 await page.getByRole('checkbox',{name:/terms of use/i}).check();await page.getByRole('checkbox',{name:/privacy/i}).check();await page.getByRole('button',{name:'Link Telegram',exact:true}).click();
 await expect(page.getByRole('alert')).toContainText(failure.copy);expect(calls.filter(c=>c.path.endsWith('/mini-app/session'))).toHaveLength(1);expect(await storage(page)).not.toMatch(/kkkk|owned-signed|mini_/);
 deny=false;await page.getByRole('button',{name:'Link Telegram',exact:true}).click();await expect(page.getByRole('heading',{name:'Mini owner'})).toBeVisible();
});
test('Browser link expires from memory and a rejected current password is cleared',async({page})=>{
 await page.clock.install();let deny=true;await identityFixture(page,{extra:async(r,path)=>{if(path==='/api/v1/me/telegram/link'&&deny){await r.fulfill({status:400,json:{error:{code:'CURRENT_PASSWORD_INVALID',message:'denied',request_id:''}}});return true}return false}});
 await page.goto('/cabinet/identity?lang=en');await page.getByLabel('Current password',{exact:true}).fill('wrong current long password');await page.getByRole('button',{name:'Get link code',exact:true}).click();await expect(page.getByRole('alert')).toBeVisible();await expect(page.getByLabel('Current password',{exact:true})).toHaveValue('');
 deny=false;await page.getByLabel('Current password',{exact:true}).fill('correct current long password');await page.getByRole('button',{name:'Get link code',exact:true}).click();await expect(page.getByLabel('Link code',{exact:true})).toHaveValue(linkToken);await page.clock.fastForward(600001);await expect(page.getByLabel('Link code',{exact:true})).toHaveCount(0);await expect(page.getByRole('status')).toContainText('expired');
});
test('Closing Mini during an initial-email request discards its late private response',async({page})=>{
 let release:()=>void=()=>{};let waiting=false;await identityFixture(page,{mini:true,extra:async(r,path)=>{if(path==='/api/v1/telegram/initial-email'){waiting=true;await new Promise<void>(resolve=>{release=resolve});await r.fulfill({status:202,json:{challenge_id:id,resend_after:60}}).catch(()=>{});return true}return false}});
 await page.goto('/mini-app/cabinet/identity?lang=en');await page.getByLabel('Email',{exact:true}).fill('private@example.test');await page.getByRole('button',{name:'Send confirmation code',exact:true}).click();await expect.poll(()=>waiting).toBe(true);await page.evaluate(()=>window.dispatchEvent(new Event('pagehide')));release();
 await expect(page.getByRole('alert')).toContainText('Session ended');await expect(page.getByLabel('Confirmation code',{exact:true})).toHaveCount(0);expect(await storage(page)).not.toMatch(/private@example|mini_|csrf/);
});

for(const lang of ['en','ru'])test('Recovery fragment needs password and explicit consent '+lang,async({page})=>{
 const body:any[]=[];await page.route('**/api/v1/auth/identity-recovery',r=>{body.push(r.request().postDataJSON());return r.fulfill({json:{verified:true}})});
 await page.goto('/recover-account?lang='+lang+'#token='+linkToken);expect(new URL(page.url()).hash).toBe('');expect(body).toHaveLength(0);
 const en=lang==='en';await expect(page.getByRole('heading',{name:en?'Recover account access':'Восстановление доступа',exact:true})).toBeVisible();await page.getByLabel(en?'New password':'Новый пароль',{exact:true}).fill('independent recovered password');await page.getByLabel(en?'Repeat password':'Повторите пароль',{exact:true}).fill('independent recovered password');
 const submit=page.getByRole('button',{name:en?'Restore access':'Восстановить доступ',exact:true});await expect(submit).toBeDisabled();await page.getByRole('checkbox',{name:en?/terms of use/i:/условия/i}).check();await page.getByRole('checkbox',{name:en?/privacy/i:/данных/i}).check();await submit.click();
 await expect(page.getByRole('status')).toContainText(en?'Recovery completed':'Доступ восстановлен');expect(body).toEqual([{token:linkToken,new_password:'independent recovered password',accepted_terms_version:'1',accepted_privacy_version:'1'}]);expect(await storage(page)).not.toMatch(/kkkk|independent recovered/);
});
test('Manual recovery code handles an expired proof and retry at mobile width',async({page})=>{
 await page.setViewportSize({width:375,height:812});let requests=0;
 await page.route('**/api/v1/auth/identity-recovery',r=>{requests++;const body=r.request().postDataJSON();expect(body.challenge_id).toBe(id);expect(body.code).toBe('12345678');return requests===1?r.fulfill({status:400,json:{error:{code:'INVALID_VERIFICATION'}}}):r.fulfill({json:{verified:true}})});
 await page.goto('/recover-account?lang=en');await page.getByLabel('Challenge ID from the email',{exact:true}).fill(id);await page.getByLabel('Code from the email',{exact:true}).fill('12345678');await page.getByLabel('New password',{exact:true}).fill('independent recovered password');await page.getByLabel('Repeat password',{exact:true}).fill('independent recovered password');await page.getByRole('checkbox',{name:/terms of use/i}).check();await page.getByRole('checkbox',{name:/privacy/i}).check();await page.getByRole('button',{name:'Restore access',exact:true}).click();await expect(page.getByRole('alert')).toContainText('expired');
 await page.getByLabel('New password',{exact:true}).fill('independent recovered password');await page.getByLabel('Repeat password',{exact:true}).fill('independent recovered password');await page.getByRole('button',{name:'Restore access',exact:true}).click();await expect(page.getByRole('status')).toContainText('Recovery completed');expect(requests).toBe(2);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});
test('Failed web logout retains CSRF and retries successfully',async({page})=>{
 let attempts=0;const calls=await identityFixture(page,{extra:async(r,path)=>{if(path==='/api/v1/auth/logout'){attempts++;const matching=r.request().headers()['x-csrf-token']===csrf;await r.fulfill({status:attempts===1?503:matching?204:403,...(attempts===1||!matching?{json:{error:{code:'SERVICE_UNAVAILABLE'}}}:{})});return true}return false}});
 await page.goto('/cabinet/identity?lang=en');await page.getByRole('button',{name:'Sign out',exact:true}).click();await expect(page.getByRole('alert')).toBeVisible();await page.getByRole('button',{name:'Sign out',exact:true}).click();await expect(page).toHaveURL(/\/login\?lang=en$/);expect(calls.filter(c=>c.path==='/api/v1/auth/logout').map(c=>c.csrf)).toEqual([csrf,csrf]);
});

const recoveryClient:Model<'OperatorClient'>={account_id:id,kind:'telegram',display_name:'Recovery client A',email:null,telegram_id:'9501',locale:'en',created_at:null,restricted:false,vpn_banned:false,had_subscription:true};
async function operatorRecoveryFixture(page:Page,extra?:(r:Route,path:string)=>Promise<boolean>){
 const calls:Call[]=[];await page.route('**/api/v1/**',async r=>{
  const path=new URL(r.request().url()).pathname;let body:any;try{body=r.request().postDataJSON()}catch{}calls.push({path,body,method:r.request().method(),csrf:r.request().headers()['x-csrf-token']});if(extra&&await extra(r,path))return;
  if(path.endsWith('/operator/session'))return r.fulfill({json:{account:{...webProfile.account,account_id:'33333333-3333-4333-8333-333333333333'},csrf_token:csrf}});
  if(/\/operator\/clients\/[\da-f-]{36}$/.test(path)){const client=path.endsWith(id)?recoveryClient:{...recoveryClient,account_id:'22222222-2222-4222-8222-222222222222',display_name:'Recovery client B'};const card:Model<'OperatorClientCard'>={client,subscription:none,server:null,support:null,trial_requests:[],trial_has_more:false,audit_events:[],audit_has_more:false,legacy_approval:null,legacy_events:[],legacy_has_more:false};return r.fulfill({json:card});}
  if(path.endsWith('/identity-recovery'))return r.fulfill({status:202,json:{challenge_id:id,expires_at:new Date(Date.now()+1800000).toISOString(),resend_after:60}});
  if(path.endsWith('/support'))return r.fulfill({json:{conversation:null,messages:[],has_more:false,oldest_sequence:null}});
  if(path.endsWith('/payment-history'))return r.fulfill({json:{kind:body.kind,orders:[],receipts:[],legacy_transactions:[],has_more:false}});
  return r.fulfill({json:{}});
 });return calls;
}
test('Selected-client recovery requires operator password reason and confirmation',async({page})=>{
 const calls=await operatorRecoveryFixture(page);await page.goto('/admin/clients/'+id+'/show?lang=en');const section=page.getByRole('region',{name:'Recover Telegram access',exact:true});await expect(section).toBeVisible();
 await section.getByLabel('Email',{exact:true}).fill('recover@example.test');await section.getByLabel('Current password',{exact:true}).fill('current operator long password');await section.getByLabel('Recovery reason',{exact:true}).fill('Support proof');const submit=section.getByRole('button',{name:'Send recovery email',exact:true});await expect(submit).toBeDisabled();await section.getByRole('checkbox',{name:'Disable Telegram sign-in until recovery is completed',exact:true}).check();await submit.click();await expect(section.getByRole('status')).toContainText('Recovery email queued');await expect(section.getByLabel('Current password',{exact:true})).toHaveValue('');
 const sent=calls.find(c=>c.path.endsWith('/identity-recovery'))!;expect(sent.body).toEqual({email:'recover@example.test',current_password:'current operator long password',reason:'Support proof',confirmed:true});expect(sent.csrf).toBe(csrf);expect(await storage(page)).not.toMatch(/recover@example|current operator|Support proof/);
});
test('Late recovery response cannot affect another selected client',async({page})=>{
 let waiting=false,release:()=>void=()=>{};await operatorRecoveryFixture(page,async(r,path)=>{if(path.endsWith('/identity-recovery')){waiting=true;await new Promise<void>(resolve=>release=resolve);await r.fulfill({status:202,json:{challenge_id:id,expires_at:new Date(Date.now()+1800000).toISOString(),resend_after:60}}).catch(()=>{});return true}return false});
 await page.goto('/admin/clients/'+id+'/show?lang=en');const section=page.getByRole('region',{name:'Recover Telegram access',exact:true});await section.getByLabel('Email',{exact:true}).fill('client-a@example.test');await section.getByLabel('Current password',{exact:true}).fill('current operator long password');await section.getByLabel('Recovery reason',{exact:true}).fill('Support proof A');await section.getByRole('checkbox').check();await section.getByRole('button',{name:'Send recovery email',exact:true}).click();await expect.poll(()=>waiting).toBe(true);
 await page.evaluate(()=>{history.pushState(null,'','/admin/clients/22222222-2222-4222-8222-222222222222/show?lang=en');dispatchEvent(new PopStateEvent('popstate'))});await expect(page.getByRole('heading',{name:'Recovery client B',exact:true})).toBeVisible();release();await expect(section.getByLabel('Email',{exact:true})).toHaveValue('');await expect(section.getByLabel('Current password',{exact:true})).toHaveValue('');await expect(section.getByRole('status')).toHaveCount(0);expect(await storage(page)).not.toMatch(/client-a|Support proof A/);
});

test('Recovery operator retries an uncertain request with the same key and a fresh password',async({page})=>{
 const keys:string[]=[];await operatorRecoveryFixture(page,async(r,path)=>{if(path.endsWith('/identity-recovery')){keys.push(r.request().headers()['idempotency-key']);if(keys.length===1)await r.abort();else await r.fulfill({status:202,json:{challenge_id:id,expires_at:new Date(Date.now()+1800000).toISOString(),resend_after:60}});return true}return false});
 await page.goto('/admin/clients/'+id+'/show?lang=en');const section=page.getByRole('region',{name:'Recover Telegram access',exact:true});await section.getByLabel('Email',{exact:true}).fill('retry@example.test');await section.getByLabel('Current password',{exact:true}).fill('current operator long password');await section.getByLabel('Recovery reason',{exact:true}).fill('Support proof');await section.getByRole('checkbox').check();await section.getByRole('button',{name:'Send recovery email',exact:true}).click();await expect(section.getByRole('alert')).toBeVisible();await expect(section.getByLabel('Current password',{exact:true})).toHaveValue('');await section.getByLabel('Current password',{exact:true}).fill('current operator long password');await section.getByRole('button',{name:'Send recovery email',exact:true}).click();await expect(section.getByRole('status')).toContainText('Recovery email queued');expect(keys).toHaveLength(2);expect(keys[1]).toBe(keys[0]);
});
test('Pagehide discards a late anonymous recovery result and all entered secrets',async({page})=>{
 let waiting=false,release:()=>void=()=>{};await page.route('**/api/v1/auth/identity-recovery',async r=>{waiting=true;await new Promise<void>(resolve=>release=resolve);await r.fulfill({json:{verified:true}}).catch(()=>{});});
 await page.goto('/recover-account?lang=en#token='+linkToken);await page.getByLabel('New password',{exact:true}).fill('independent recovered password');await page.getByLabel('Repeat password',{exact:true}).fill('independent recovered password');await page.getByRole('checkbox',{name:/terms of use/i}).check();await page.getByRole('checkbox',{name:/privacy/i}).check();await page.getByRole('button',{name:'Restore access',exact:true}).click();await expect.poll(()=>waiting).toBe(true);await page.evaluate(()=>window.dispatchEvent(new Event('pagehide')));release();await expect(page.getByRole('alert')).toContainText('form was closed');await expect(page.getByRole('status')).toHaveCount(0);await expect(page.getByLabel('New password',{exact:true})).toHaveCount(0);expect(await storage(page)).not.toMatch(/kkkk|independent recovered/);
});
test('Failed Mini logout still destroys its private session without automatic login',async({page})=>{
 const calls=await identityFixture(page,{mini:true,extra:async(r,path)=>{if(path==='/api/v1/telegram/mini-app/logout'){await r.fulfill({status:503,json:{error:{code:'SERVICE_UNAVAILABLE'}}});return true}return false}});await page.goto('/mini-app/cabinet/identity?lang=en');await page.getByRole('button',{name:'Sign out',exact:true}).click();await expect(page.getByRole('alert')).toContainText('Session ended');await expect(page.getByLabel('Email',{exact:true})).toHaveCount(0);expect(calls.filter(c=>c.path.endsWith('/mini-app/session'))).toHaveLength(1);expect(await storage(page)).not.toMatch(/mini_|csrf/);
});
