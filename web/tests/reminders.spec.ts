import {test,expect,type Page,type Route} from '@playwright/test';
import type {components} from '../src/api/schema.gen';
type Model<K extends keyof components['schemas']>=components['schemas'][K];
const account:Model<'AccountResult'>={account:{account_id:'11111111-1111-4111-8111-111111111111',email:'reminders@example.test',email_verified:true,locale:'ru',telegram_linked:false},csrf_token:'c'.repeat(43),capabilities:{trial_available:false}};
const none:Model<'Subscription'>={status:'none',devices:0,traffic_limit_bytes:0,traffic_used_bytes:null,observed_at:null,data_stale:true,expires_at:null,access_profile:'unknown',vpn_banned:false,access_operation_id:null,access_operation_status:null};
const observed='2030-01-01T10:00:00Z';
const expiry:Model<'Reminder'>={id:'22222222-2222-4222-8222-222222222222',kind:'expiry',threshold:1,observed_at:observed,expires_at:'2030-01-02T10:00:00Z',traffic_used_bytes:null,traffic_limit_bytes:null,paid_until:null,route:'cabinet'};
const traffic:Model<'Reminder'>={...expiry,id:'33333333-3333-4333-8333-333333333333',kind:'traffic',threshold:80,expires_at:null,traffic_used_bytes:'9007199254740993',traffic_limit_bytes:'9223372036854775807'};
const lapse:Model<'Reminder'>={...expiry,id:'44444444-4444-4444-8444-444444444444',kind:'stars_lapsed',threshold:0,expires_at:null,paid_until:'2029-12-30T10:00:00Z',route:'cabinet'};
const reminders:Model<'ReminderResult'>={version:'reminders-v1',email_enabled:false,email_available:true,reminders:[expiry,traffic,lapse]};
const empty:Model<'ReminderResult'>={...reminders,reminders:[]};
type Options={result?:Model<'ReminderResult'>;extra?:(r:Route,path:string)=>Promise<boolean>;profile?:()=>Model<'AccountResult'>|Model<'MiniAppAccountResult'>;subscription?:Model<'Subscription'>};
async function fixture(page:Page,options:Options={}){
 let result=structuredClone(options.result??reminders);
 const writes:{path:string;body:unknown;csrf:string|undefined}[]=[];
 await page.route('**/api/v1/**',async r=>{
  const req=r.request(),path=new URL(req.url()).pathname;
  if(options.extra&&await options.extra(r,path))return;
  if(path==='/api/v1/me')return r.fulfill({json:options.profile?.()??account});
  if(path==='/api/v1/notices')return r.fulfill({json:{version:'operator-notices-v1',email_enabled:false,email_available:options.profile?.().account.email_verified??account.account.email_verified,page:1,per_page:20,total:0,notices:[]}});
  if(path==='/api/v1/subscription')return r.fulfill({json:options.subscription??none});
  if(path==='/api/v1/trial-requests/current')return r.fulfill({json:{request:null}});
  if(path==='/api/v1/orders/current')return r.fulfill({json:{order:null,can_purchase:false}});
  if(path==='/api/v1/auth/logout')return r.fulfill({status:204});
  if(path==='/api/v1/reminders')return r.fulfill({json:result});
  if(path==='/api/v1/reminders/preferences'){
   writes.push({path,body:req.postDataJSON(),csrf:req.headers()['x-csrf-token']});
   result={...result,email_enabled:req.postDataJSON().email_enabled};return r.fulfill({json:result});
  }
  if(path.endsWith('/dismiss')){
   writes.push({path,body:req.postData(),csrf:req.headers()['x-csrf-token']});
   result={...result,reminders:result.reminders.filter(v=>!path.includes(v.id))};return r.fulfill({status:204});
  }
  return r.fulfill({status:404,json:{error:{code:'INVALID_INPUT',message:'owned fixture',request_id:''}}});
 });
 return {writes};
}
for(const lang of ['ru','en'])test('dated reminders, explicit email preference and own close '+lang,async({page})=>{
 const f=await fixture(page);if(lang==='en')await page.setViewportSize({width:375,height:812});await page.goto('/cabinet'+(lang==='en'?'?lang=en':''));
 const section=page.getByRole('region',{name:lang==='ru'?'Предупреждения о подписке':'Subscription reminders'});
 await expect(section).toBeVisible();await expect(section.getByRole('listitem')).toHaveCount(3);
 await expect(section.locator('time[datetime="'+observed+'"]')).toHaveCount(3);
 await expect(section.locator('time[datetime="'+expiry.expires_at+'"]')).toBeVisible();await expect(section.locator('time[datetime="'+lapse.paid_until+'"]')).toBeVisible();
 await expect(section).toContainText('9007199254740993');await expect(section).toContainText('9223372036854775807');
 await expect(section).toContainText(lang==='ru'?'Проверьте состояние автопродления':'Check the auto-renewal status');
 const open=section.getByRole('link',{name:lang==='ru'?'Открыть кабинет':'Open cabinet'}).first();await expect(open).toHaveAttribute('href','/cabinet'+(lang==='en'?'?lang=en':''));
 await expect(section.getByRole('link',{name:lang==='ru'?'Проверить Stars':'Check Stars'})).toHaveAttribute('href','/cabinet'+(lang==='en'?'?lang=en':''));
 const consent=section.getByRole('checkbox',{name:lang==='ru'?'Получать предупреждения по email':'Receive reminders by email'});await expect(consent).not.toBeChecked();
 await expect(consent).toHaveAttribute('aria-describedby','reminder-email-help');
 await consent.focus();await page.keyboard.press('Space');await expect(consent).toBeChecked();expect(f.writes).toHaveLength(0);
 const save=section.getByRole('button',{name:lang==='ru'?'Сохранить настройки уведомлений':'Save reminder settings'});await page.keyboard.press('Tab');await expect(save).toBeFocused();await page.keyboard.press('Enter');
 await expect(section.getByRole('status')).toContainText(lang==='ru'?'Настройки сохранены':'Settings saved');expect(f.writes[0]).toEqual({path:'/api/v1/reminders/preferences',body:{email_enabled:true},csrf:account.csrf_token});
 const close=section.getByRole('button',{name:lang==='ru'?'Закрыть предупреждение: Срок подписки':'Dismiss reminder: Subscription expiry'});await close.focus();await page.keyboard.press('Enter');
 await expect(section.getByRole('listitem')).toHaveCount(2);await expect(section.getByRole('heading',{name:lang==='ru'?'Предупреждения о подписке':'Subscription reminders',exact:true})).toBeFocused();
 expect(f.writes[1]).toEqual({path:'/api/v1/reminders/'+expiry.id+'/dismiss',body:null,csrf:account.csrf_token});
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 expect(await page.evaluate(()=>JSON.stringify(localStorage)+JSON.stringify(sessionStorage))).not.toMatch(/9007199254740993|csrf_token|22222222/);
});

// A direct renewal warning blocks trial's first purchase and active Stars traffic management.
for(const mini of [false,true])for(const source of ['trial','paid','stars'] as const)test('warning reaches available '+source+' action in '+(mini?'Mini':'web'),async({page})=>{
 let paymentWrites=0;
 const plan:Model<'CataloguePlanSnapshot'>={plan_id:'70000000-0000-4000-8000-000000000001',revision:1,devices:2,traffic_gb:10,profile:'regular',hidden:false,periods:[30],prices:[{period_days:30,currency:mini?'XTR':'RUB',amount_minor:'100'}]};
 const stars:Model<'StarsSubscription'>={state:source==='stars'?'active':'none',order_id:source==='stars'?'80000000-0000-4000-8000-000000000001':null,provider_state:source==='stars'?'active':null,control_state:source==='stars'?'none':null,paid_until:source==='stars'?'2030-02-01T10:00:00Z':null,period_phase:source==='stars'?'current':'none',can_cancel:source==='stars',can_resume:false,external_billing_blocked:source==='stars',needs_review:false};
 const profile:Model<'MiniAppAccountResult'>={account:{account_id:account.account.account_id,email:account.account.email,email_verified:true,display_name:'Owned reminder client',telegram_id:701,telegram_linked:true,locale:'en'},csrf_token:account.csrf_token,capabilities:{trial_available:false}};
 if(mini)await page.route('https://telegram.org/js/telegram-web-app.js',r=>r.fulfill({contentType:'application/javascript',body:'window.Telegram={WebApp:{initData:"owned-reminder-launch",ready(){},expand(){}}};'}));
 await fixture(page,{profile:()=>source==='stars'?profile:account,result:{...reminders,reminders:[source==='stars'?traffic:expiry]},subscription:{...none,status:'active',access_profile:'regular',devices:2,data_stale:false,expires_at:expiry.expires_at,traffic_used_bytes:80,traffic_limit_bytes:100},extra:async(r,path)=>{
  if(path.endsWith('/telegram/mini-app/session')){await r.fulfill({json:{...profile,session_token:'mini_'+'b'.repeat(43)}});return true;}
  if(path.endsWith('/telegram/mini-app/account')){await r.fulfill({json:profile});return true;}
  if(path.endsWith('/auth/session')){await r.fulfill({json:{csrf_token:account.csrf_token}});return true;}
  if(path==='/api/v1/stars-subscription'){await r.fulfill({json:stars});return true;}
  if(path==='/api/v1/catalogue'){await r.fulfill({json:{plans:[plan]}});return true;}
  if(path==='/api/v1/payment-methods'){await r.fulfill({json:{methods:[{id:mini?'telegram_stars':'yoomoney',currency:mini?'XTR':'RUB'}]}});return true;}
  if(path==='/api/v1/orders/current'){await r.fulfill({json:{order:null,can_purchase:source==='trial'}});return true;}
  if(path==='/api/v1/subscription/renewal'){await r.fulfill(source==='paid'?{json:plan}:{status:409,json:{error:{code:source==='stars'?'EXTERNAL_BILLING_UNVERIFIED':'RENEWAL_NOT_ELIGIBLE'}}});return true;}
  if(path==='/api/v1/orders'&&r.request().method()==='POST'){paymentWrites++;await r.abort();return true;}
  return false;
 }});
 const prefix=mini?'/mini-app':'';
 await page.goto(prefix+'/cabinet?lang=en');const warning=page.getByRole('region',{name:'Subscription reminders'});
 const open=warning.getByRole('link',{name:'Open cabinet'});await expect(open).toHaveAttribute('href',prefix+'/cabinet?lang=en');await open.click();
 await expect(page).toHaveURL(new RegExp(prefix+'/cabinet\\?lang=en$'));
 if(source==='stars'){
  const controls=page.getByRole('region',{name:'Stars auto-renewal'});await expect(controls.getByText('Auto-renewal is active.',{exact:true})).toBeVisible();await expect(controls.getByRole('button',{name:'Cancel auto-renewal',exact:true})).toBeEnabled();
 }else if(source==='trial'){
  await page.getByRole('link',{name:'Plans',exact:true}).click();await page.getByRole('button',{name:'Select plan'}).click();await expect(page.getByRole('button',{name:'Buy plan',exact:true})).toBeEnabled();
 }else{
  await page.getByRole('link',{name:'Renew subscription',exact:true}).click();await expect(page.getByRole('button',{name:'Renew subscription',exact:true})).toBeEnabled();
 }
 await expect(page.getByRole('alert')).toHaveCount(0);expect(paymentWrites).toBe(0);
});

test('loading, safe error, manual retry and empty reminders',async({page})=>{
 let reads=0,release!:()=>void;const hold=new Promise<void>(r=>release=r);
 await fixture(page,{extra:async(r,path)=>{if(path!=='/api/v1/reminders')return false;reads++;if(reads===1){await hold;await r.fulfill({status:503,json:{error:{code:'SERVICE_UNAVAILABLE',message:'private provider raw response',request_id:''}}});}else await r.fulfill({json:empty});return true;}});
 await page.goto('/cabinet?lang=en');const section=page.getByRole('region',{name:'Subscription reminders'});
 await expect(section.getByRole('status')).toContainText('Loading');await expect(section.getByRole('checkbox')).toHaveCount(0);
 release();await expect(section.getByRole('alert')).toBeVisible();await expect(page.getByText('private provider raw response')).toHaveCount(0);
 await section.getByRole('button',{name:'Retry'}).click();await expect(section.getByRole('status')).toContainText('No current reminders');expect(reads).toBe(2);await expect(section.getByRole('checkbox')).not.toBeChecked();
});
for(const variant of ['version','bytes','date','route','duplicate'])test('rejects malformed reminder reply '+variant,async({page})=>{
 const bad:any=structuredClone(reminders);
 if(variant==='version')bad.version='unrecognized';if(variant==='bytes')bad.reminders[1].traffic_used_bytes=9007199254740992;if(variant==='date')bad.reminders[0].expires_at='invalid';if(variant==='route')bad.reminders[0].route='https://attacker.example.test';if(variant==='duplicate')bad.reminders[1].id=expiry.id;
 await fixture(page,{extra:async(r,path)=>{if(path!=='/api/v1/reminders')return false;await r.fulfill({json:bad});return true;}});
 await page.goto('/cabinet?lang=en');const section=page.getByRole('region',{name:'Subscription reminders'});await expect(section.getByRole('alert')).toBeVisible();await expect(section.getByRole('checkbox')).toHaveCount(0);await expect(section.getByRole('listitem')).toHaveCount(0);await expect(section.getByRole('link')).toHaveCount(0);
});
test('email unavailable prevents opt-in but permits withdrawing prior consent',async({page})=>{
 const f=await fixture(page,{result:{...empty,email_available:false,email_enabled:true}});await page.goto('/cabinet?lang=en');const section=page.getByRole('region',{name:'Subscription reminders'}),checkbox=section.getByRole('checkbox');
 await expect(checkbox).toBeChecked();await expect(section).toContainText('verified email and password');await expect(section.getByRole('link',{name:'Set up email sign-in'})).toHaveAttribute('href','/cabinet/identity?lang=en');
 await checkbox.uncheck();await section.getByRole('button',{name:'Save reminder settings'}).click();await expect(checkbox).not.toBeChecked();await expect(checkbox).toBeDisabled();expect(f.writes[0].body).toEqual({email_enabled:false});
});
for(const status of [400,401,403])test('write error keeps only authorized reminder state '+status,async({page})=>{
 await fixture(page,{extra:async(r,path)=>{if(path!=='/api/v1/reminders/preferences')return false;await r.fulfill({status,json:{error:{code:status===403?'ACCOUNT_RESTRICTED':'INVALID_INPUT',message:'private recipient details',request_id:''}}});return true;}});
 await page.goto('/cabinet?lang=en');const section=page.getByRole('region',{name:'Subscription reminders'});await section.getByRole('checkbox').check();await section.getByRole('button',{name:'Save reminder settings'}).click();
 if(status===401){await expect(page).toHaveURL(/\/login\?lang=en$/);await expect(section).toHaveCount(0);}else{await expect(section.getByRole('alert')).toBeVisible();await expect(section.getByRole('listitem')).toHaveCount(status===403?0:3);await expect(section.getByRole('checkbox')).toHaveCount(status===403?0:1);}
 await expect(page.getByText('private recipient details')).toHaveCount(0);
});
test('old-language delayed read cannot restore private reminders',async({page})=>{
 let reads=0,release!:()=>void;const hold=new Promise<void>(r=>release=r);
 await fixture(page,{extra:async(r,path)=>{if(path!=='/api/v1/reminders')return false;reads++;if(reads===1){await hold;await r.fulfill({json:{...reminders,email_enabled:true}});}else await r.fulfill({json:empty});return true;}});
 await page.goto('/cabinet');await expect.poll(()=>reads).toBe(1);await page.getByRole('button',{name:'EN',exact:true}).click();const section=page.getByRole('region',{name:'Subscription reminders'});
 await expect(section.getByRole('status')).toContainText('No current reminders');release();await expect(section.getByRole('checkbox')).not.toBeChecked();await expect(section.getByRole('listitem')).toHaveCount(0);await expect(page.getByText('9007199254740993')).toHaveCount(0);
});
test('old-language delayed save cannot replace new consent',async({page})=>{
 let saves=0,release!:()=>void;const hold=new Promise<void>(r=>release=r);
 await fixture(page,{result:empty,extra:async(r,path)=>{if(path!=='/api/v1/reminders/preferences')return false;saves++;await hold;await r.fulfill({json:{...reminders,email_enabled:true}});return true;}});
 await page.goto('/cabinet');await page.getByRole('region',{name:'Предупреждения о подписке'}).getByRole('checkbox').check();await page.getByRole('button',{name:'Сохранить настройки уведомлений'}).click();await expect.poll(()=>saves).toBe(1);
 await page.getByRole('button',{name:'EN',exact:true}).click();const section=page.getByRole('region',{name:'Subscription reminders'});await expect(section.getByRole('status')).toContainText('No current reminders');release();await expect(section.getByRole('checkbox')).not.toBeChecked();await expect(section.getByRole('listitem')).toHaveCount(0);
});
test('changing account aborts the prior account save',async({page})=>{
 await page.clock.install({time:new Date(observed)});await page.clock.pauseAt(new Date('2030-01-01T10:00:01Z'));
 let current=account,saves=0,reads=0,release!:()=>void;const hold=new Promise<void>(r=>release=r);
 await fixture(page,{profile:()=>current,subscription:{...none,status:'provisioning'},extra:async(r,path)=>{
  if(path==='/api/v1/reminders/preferences'){saves++;await hold;await r.fulfill({json:{...reminders,email_enabled:true}});return true;}
  if(path==='/api/v1/reminders'){reads++;await r.fulfill({json:empty});return true;}return false;
 }});
 await page.goto('/cabinet?lang=en');const section=page.getByRole('region',{name:'Subscription reminders'});await section.getByRole('checkbox').check();await section.getByRole('button',{name:'Save reminder settings'}).click();await expect.poll(()=>saves).toBe(1);
 current={...account,account:{...account.account,account_id:'55555555-5555-4555-8555-555555555555',email:'new-account@example.test'}};await page.clock.runFor(5000);await expect(page.getByRole('heading',{name:'new-account@example.test'})).toBeVisible();await expect.poll(()=>reads).toBe(2);release();await expect(section.getByRole('checkbox')).not.toBeChecked();await expect(section.getByRole('listitem')).toHaveCount(0);
});
test('logout removes reminders while an earlier save is pending',async({page})=>{
 let saves=0,release!:()=>void,logout!:()=>void;const hold=new Promise<void>(r=>release=r),holdLogout=new Promise<void>(r=>logout=r);
 await fixture(page,{extra:async(r,path)=>{
  if(path==='/api/v1/reminders/preferences'){saves++;await hold;await r.fulfill({json:{...reminders,email_enabled:true}});return true;}
  if(path==='/api/v1/auth/logout'){await holdLogout;await r.fulfill({status:204});return true;}return false;
 }});
 await page.goto('/cabinet?lang=en');const section=page.getByRole('region',{name:'Subscription reminders'});await section.getByRole('checkbox').check();await section.getByRole('button',{name:'Save reminder settings'}).click();await expect.poll(()=>saves).toBe(1);await page.getByRole('button',{name:'Sign out',exact:true}).click();await expect(section).toHaveCount(0);release();await expect(section).toHaveCount(0);logout();await expect(page).toHaveURL(/\/login/);
});
