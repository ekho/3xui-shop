import {test,expect,type Page,type Route} from '@playwright/test';
import type {components} from '../src/api/schema.gen';
type Model<K extends keyof components['schemas']>=components['schemas'][K];
const profile:Model<'MiniAppAccountResult'>={account:{account_id:'11111111-1111-4111-8111-111111111111',email:null,email_verified:false,display_name:'Mini client',telegram_id:444,telegram_linked:true,locale:'en'},csrf_token:'c'.repeat(43),capabilities:{trial_available:false}};
const token='mini_'+'b'.repeat(43),launch='owned-signed-fixture';
const none:Model<'Subscription'>={status:'none',devices:0,traffic_limit_bytes:0,traffic_used_bytes:null,observed_at:null,data_stale:true,expires_at:null,access_profile:'unknown',vpn_banned:false,access_operation_id:null,access_operation_status:null};
const emptySupport:Model<'SupportResult'>={conversation:null,messages:[],has_more:false,oldest_sequence:null};
const emptyHistory:Model<'PaymentHistoryPage'>={kind:'orders',orders:[],receipts:[],legacy_transactions:[],refunds:[],has_more:false};

type Options={sdk?:'ready'|'empty'|'fail'|'timeout'|'broken-methods';consent?:boolean;sub?:Model<'Subscription'>;extra?:(r:Route,path:string)=>Promise<boolean>};
async function fixture(page:Page,options:Options={}){
 const calls:{path:string;authorization:boolean;cookie:boolean;body:any}[]=[];let sessions=0;let sdkMode=options.sdk;
 await page.route('https://telegram.org/js/telegram-web-app.js',async r=>{
  if(sdkMode==='fail')return r.abort();if(sdkMode==='timeout')return;
  const raw=sdkMode==='empty'?'':launch;
  await r.fulfill({contentType:'application/javascript',body:`
  window.__sdk={opened:[],handlers:{},back:null,stale:sessionStorage.getItem('__telegram__initParams')};
  window.Telegram={WebApp:{initData:${JSON.stringify(raw)},initDataUnsafe:{user:{id:999999}},version:'9.6',platform:'web',themeParams:{bg_color:'#17212b',text_color:'#ffffff',secondary_bg_color:'#242f3d',button_color:'#2481cc',button_text_color:'#ffffff',link_color:'#6ab3f3'},viewportStableHeight:640,safeAreaInset:{top:12,bottom:10,left:0,right:0},contentSafeAreaInset:{top:8,bottom:6,left:0,right:0},ready(){${sdkMode==='broken-methods'?'throw new Error("owned SDK method failure")':''}},expand(){},onEvent(n,f){window.__sdk.handlers[n]=f},offEvent(n){delete window.__sdk.handlers[n]},BackButton:{show(){},hide(){},onClick(f){window.__sdk.back=f},offClick(){window.__sdk.back=null}},openLink(url){window.__sdk.opened.push(url)}}};
  sessionStorage.setItem('__telegram__initParams',JSON.stringify({tgWebAppData:${JSON.stringify(raw)}}));`});
 });
 await page.route('**/api/v1/**',async r=>{
  const req=r.request(),path=new URL(req.url()).pathname;
  let body:any;try{body=req.postDataJSON()}catch{}
  calls.push({path,authorization:req.headers().authorization===('Bearer '+token),cookie:!!req.headers().cookie,body});
  if(options.extra&&await options.extra(r,path))return;
  if(path==='/api/v1/telegram/mini-app/session'){
   sessions++;if(options.consent&&!body?.accepted_terms_version)return r.fulfill({status:409,json:{error:{code:'CONSENT_REQUIRED',message:'CONSENT_REQUIRED',request_id:'00000000-0000-4000-8000-000000000001'}}});
   return r.fulfill({json:{...profile,session_token:token},headers:{'Cache-Control':'no-store'}});
  }
  if(path==='/api/v1/telegram/mini-app/account')return r.fulfill({json:profile});
  if(path==='/api/v1/auth/session')return r.fulfill({json:{csrf_token:profile.csrf_token}});
  if(path==='/api/v1/telegram/mini-app/logout'||path==='/api/v1/support/read')return r.fulfill({status:204});
  if(path==='/api/v1/subscription')return r.fulfill({json:options.sub??none});
  if(path==='/api/v1/subscription/key')return r.fulfill({json:{subscription_url:'https://subscriptions.example.test/sub/owned-private-link'}});
  if(path==='/api/v1/trial-requests/current')return r.fulfill({json:{request:null}});
  if(path==='/api/v1/orders/current')return r.fulfill({json:{order:null,can_purchase:false}});
  if(path==='/api/v1/catalogue')return r.fulfill({json:{plans:[]}});
  if(path==='/api/v1/support')return r.fulfill({json:emptySupport});
  if(path==='/api/v1/payment-history')return r.fulfill({json:emptyHistory});
  return r.fulfill({status:404,json:{error:{code:'INVALID_INPUT',message:'INVALID_INPUT',request_id:''}}});
 });
 return {calls,sessions:()=>sessions,sdk:(mode:Options['sdk'])=>{sdkMode=mode}};
}
const storage=async(page:Page)=>page.evaluate(()=>JSON.stringify(localStorage)+JSON.stringify(sessionStorage));

test('Mini App signed login preserves independent cookie and memory-only transport',async({page})=>{
 const f=await fixture(page);await page.addInitScript(()=>sessionStorage.setItem('__telegram__initParams','stale-owned-launch'));await page.context().addCookies([{name:'browser-marker',value:'independent',url:'http://127.0.0.1:4173'}]);
 await page.goto('/mini-app#tgWebAppData='+encodeURIComponent(launch));
 await expect(page.getByRole('heading',{name:'Mini client'})).toBeVisible();
 expect(await page.evaluate(()=>(window as any).__sdk.stale)).toBeNull();expect(f.sessions()).toBe(1);expect(f.calls[0]?.body?.init_data===launch).toBe(true);
 expect(f.calls.filter(c=>c.path!=='/api/v1/telegram/mini-app/session').every(c=>c.authorization&&!c.cookie)).toBe(true);
 expect(new URL(page.url()).hash).toBe('');expect(await storage(page)).not.toMatch(/owned-signed|mini_|csrf_token|__telegram__initParams/);
 expect((await page.context().cookies()).some(c=>c.name==='browser-marker')).toBe(true);
 await expect(page.getByRole('link',{name:'Безопасность аккаунта'})).toHaveCount(0);
});
test('Mini App requires both explicit document consents',async({page})=>{
 const f=await fixture(page,{consent:true});await page.goto('/mini-app?lang=en');
 await expect(page.getByRole('heading',{name:'Cabinet in Telegram'})).toBeVisible();
 const submit=page.getByRole('button',{name:'Continue'});await expect(submit).toBeDisabled();
 await page.getByRole('checkbox',{name:/terms of use/i}).check();await expect(submit).toBeDisabled();
 await page.getByRole('checkbox',{name:/privacy/i}).check();await submit.click();await expect(page.getByRole('heading',{name:'Mini client'})).toBeVisible();
 const post=f.calls.filter(c=>c.path.endsWith('/mini-app/session')).at(-1)!;expect(post.body.accepted_terms_version).toBe('1');expect(post.body.accepted_privacy_version).toBe('1');
});
for(const mode of ['empty','fail','timeout'] as const)test('Mini App SDK '+mode+' stays explicit and does not trust initDataUnsafe',async({page})=>{
 const f=await fixture(page,{sdk:mode});await page.goto('/mini-app?lang=en');
 await expect(page.getByRole('heading',{name:'Cabinet in Telegram'})).toBeVisible();
 await expect(page.getByRole('alert')).toBeVisible();expect(f.sessions()).toBe(0);
 await expect(page.getByLabel('Email',{exact:true})).toHaveCount(0);
 await expect(page.getByRole('link',{name:'Open cabinet in browser'})).toBeVisible();
});
test('Mini App SDK theme viewport back and internal navigation use shared screens',async({page})=>{
 const f=await fixture(page);await page.goto('/mini-app?lang=en');await expect(page.getByRole('heading',{name:'Mini client'})).toBeVisible();
 await page.getByRole('link',{name:'Payment history',exact:true}).click();await expect(page.getByRole('heading',{name:'Payment history'})).toBeVisible();
 expect(page.url()).toContain('/mini-app/cabinet/history');expect(f.sessions()).toBe(1);
 await page.evaluate(()=>{(window as any).__sdk.back()});await expect(page.getByRole('heading',{name:'Mini client'})).toBeVisible();
 expect(await page.evaluate(()=>getComputedStyle(document.querySelector('.mini-app')!).getPropertyValue('--mini-bg').trim())).toBe('#17212b');
 expect(await page.evaluate(()=>getComputedStyle(document.querySelector('.mini-app')!).getPropertyValue('--mini-safe-top').trim())).toBe('12px');
 await page.evaluate(()=>{(window as any).Telegram.WebApp.themeParams.bg_color='url(https://attacker.example.test)';(window as any).__sdk.handlers.themeChanged()});
 expect(await page.evaluate(()=>getComputedStyle(document.querySelector('.mini-app')!).getPropertyValue('--mini-bg').trim())).not.toContain('url(');
 await page.getByRole('button',{name:'RU',exact:true}).click();await expect(page.getByText('Доступ ещё не активирован')).toBeVisible();
});
test('Mini App handles older/broken optional SDK methods without losing login',async({page})=>{
 await fixture(page,{sdk:'broken-methods'});await page.goto('/mini-app?lang=en');await expect(page.getByRole('heading',{name:'Mini client'})).toBeVisible();
});
test('Mini App browser-open action forwards only the public cabinet URL',async({page})=>{
 await fixture(page);await page.goto('/mini-app?lang=en');await expect(page.getByRole('heading',{name:'Mini client'})).toBeVisible();
 await page.getByRole('link',{name:'Open cabinet in browser',exact:true}).click();
 const opened=await page.evaluate(()=>(window as any).__sdk.opened);expect(opened).toEqual(['http://127.0.0.1:4173/cabinet?lang=en']);
 expect(JSON.stringify(opened)).not.toMatch(/owned-signed|mini_|csrf|11111111|sub\//);
});
test('Mini App logout destroys private key and does not sign in automatically',async({page})=>{
 const f=await fixture(page,{sub:{...none,status:'active',devices:1,access_profile:'regular',traffic_limit_bytes:1024,expires_at:'2030-01-01T00:00:00Z'}});
 await page.goto('/mini-app?lang=en');await expect(page.getByRole('heading',{name:'Mini client'})).toBeVisible();
 await page.getByRole('button',{name:'Show subscription link',exact:true}).click();await expect(page.getByRole('textbox',{name:'Subscription link',exact:true})).toBeVisible();
 await page.getByRole('button',{name:'Sign out',exact:true}).click();await expect(page.getByRole('alert')).toContainText('Session ended');
 await expect(page.getByRole('textbox',{name:'Subscription link',exact:true})).toHaveCount(0);expect(f.sessions()).toBe(1);
 expect(await page.locator('body').textContent()).not.toContain('owned-private-link');expect(await storage(page)).not.toMatch(/owned-private|mini_|csrf_token/);
});
test('Mini App expired response destroys the screen without cookie fallback',async({page})=>{
 const f=await fixture(page,{extra:async(r,path)=>{if(path==='/api/v1/subscription'){await r.fulfill({status:401,json:{error:{code:'INVALID_CREDENTIALS',message:'INVALID_CREDENTIALS',request_id:''}}});return true}return false}});
 await page.goto('/mini-app?lang=en');await expect(page.getByRole('alert')).toContainText('Session ended');
 expect(f.sessions()).toBe(1);await expect(page.getByRole('heading',{name:'Mini client'})).toHaveCount(0);await expect(page.getByLabel('Email',{exact:true})).toHaveCount(0);
});
test('Mini App support attachment uses authenticated fetch without tokens in its URL',async({page})=>{
 const id='00000000-0000-4000-8000-000000000001';let authorized=false;
 await fixture(page,{extra:async(r,path)=>{
  if(path==='/api/v1/support'){await r.fulfill({json:{conversation:{id:'22222222-2222-4222-8222-222222222222',status:'open',support_banned:false,created_at:'2026-10-02T00:00:00Z',updated_at:'2026-10-02T00:00:00Z',customer_received_sequence:0,operator_received_sequence:0},messages:[{id,sequence:1,sender:'operator',text:'Owned attachment',created_at:'2026-10-02T00:00:00Z',attachment:{name:'owned.txt',size_bytes:5},delivery:'delivered'}],has_more:false,oldest_sequence:1}});return true}
  if(path.endsWith('/attachment')){authorized=r.request().headers().authorization===('Bearer '+token);await r.fulfill({contentType:'application/octet-stream',body:'owned'});return true}return false
 }});
 await page.goto('/mini-app/cabinet/support?lang=en');await expect(page.getByText('Owned attachment',{exact:true})).toBeVisible();
 const download=page.waitForEvent('download');await page.getByRole('button',{name:/owned.txt/}).click();const file=await download;expect(file.suggestedFilename()).toBe('owned.txt');expect(authorized).toBe(true);
 expect(page.url()).not.toMatch(/mini_|owned-signed/);
});
test('Normal browser cabinet does not load Telegram SDK',async({page})=>{
 let sdk=0;await page.route('https://telegram.org/**',async r=>{sdk++;await r.abort()});
 await page.route('**/api/v1/**',async r=>{const path=new URL(r.request().url()).pathname;await r.fulfill({json:path==='/api/v1/me'?{...profile,account:{account_id:profile.account.account_id,email:'browser@example.test',email_verified:true,locale:'en',telegram_linked:false}}:path==='/api/v1/subscription'?none:{request:null,order:null}})});
 await page.goto('/cabinet?lang=en');await expect(page.getByRole('heading',{name:'browser@example.test'})).toBeVisible();expect(sdk).toBe(0);
});

test('Mini App rejected signed launch asks to reopen without an email-password error',async({page})=>{
 const f=await fixture(page,{extra:async(r,path)=>{if(path==='/api/v1/telegram/mini-app/session'){await r.fulfill({status:401,json:{error:{code:'INVALID_CREDENTIALS',message:'INVALID_CREDENTIALS',request_id:''}}});return true}return false}});
 await page.goto('/mini-app?lang=en');await expect(page.getByRole('alert')).toContainText('Session ended');
 await expect(page.getByRole('button',{name:'Retry'})).toHaveCount(0);expect(f.calls).toHaveLength(1);
});

test('Mini App SDK failed load can be retried once it becomes available',async({page})=>{
 const f=await fixture(page,{sdk:'fail'});await page.goto('/mini-app?lang=en');await expect(page.getByRole('alert')).toBeVisible();
 f.sdk('ready');await page.getByRole('button',{name:'Retry',exact:true}).click();await expect(page.getByRole('heading',{name:'Mini client'})).toBeVisible();expect(f.sessions()).toBe(1);
});
const plan:Model<'CataloguePlanSnapshot'>={plan_id:'70000000-0000-4000-8000-000000000001',revision:3,devices:2,traffic_gb:20,profile:'regular',hidden:false,periods:[30],prices:[{period_days:30,currency:'RUB',amount_minor:'12345'},{period_days:30,currency:'USD',amount_minor:'200'},{period_days:30,currency:'XTR',amount_minor:'1'}]};
test('Mini App catalogue shows common prices without external checkout requests',async({page})=>{
 const f=await fixture(page,{extra:async(r,path)=>{if(path==='/api/v1/catalogue'){await r.fulfill({json:{plans:[plan]}});return true}return false}});
 await page.setViewportSize({width:375,height:812});await page.goto('/mini-app/catalogue?lang=en');
 await page.getByRole('button',{name:'Select plan'}).click();await expect(page.getByText('Price: 123.45 RUB')).toBeVisible();
 await expect(page.getByRole('button',{name:'Buy plan'})).toHaveCount(0);expect(f.calls.some(c=>c.path.endsWith('/payment-methods')||c.path==='/api/v1/orders')).toBe(false);
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});
for(const manual of [false,true])test('Mini App order hides external checkout even when response permits '+(manual?'manual report':'YooMoney'),async({page})=>{
 const id='80000000-0000-4000-8000-000000000001';
 const order:Model<'PurchaseOrder'>={order_id:id,action:'purchase',quote:{plan_id:plan.plan_id,revision:3,devices:2,period_days:30,traffic_gb:20,profile:'regular',amount_minor:'12345',currency:'RUB'},payment_method:manual?'manual':'yoomoney',payment_type:manual?'MANUAL':'AC',payment_status:'pending',fulfillment_status:'not_started',created_at:'2026-10-03T10:00:00Z',expires_at:'2030-01-01T00:00:00Z',expired:false,can_pay:true,can_cancel:false,review_required:false,access_operation_id:null,...(manual?{checkout:null,manual_payment:{state:'not_reported',reported_at:null,decided_at:null,instructions:'Owned manual instructions',reason:null,can_report:true}}:{checkout:{action:'https://yoomoney.ru/quickpay/confirm',method:'POST',fields:{receiver:'410011111111111','quickpay-form':'button',paymentType:'AC',sum:'123.45',label:id,successURL:'http://127.0.0.1:4173/orders/'+id}}})};
 await fixture(page,{extra:async(r,path)=>{if(path==='/api/v1/orders/'+id){await r.fulfill({json:order});return true}return false}});
 await page.goto('/mini-app/orders/'+id+'?lang=en');await expect(page.getByRole('heading',{name:'Order',exact:true})).toBeVisible();await expect(page.getByText(id,{exact:true})).toBeVisible();
 await expect(page.locator('form[action*="yoomoney"]')).toHaveCount(0);await expect(page.getByRole('button',{name:/payment|paid/i})).toHaveCount(0);await expect(page.getByText('Owned manual instructions',{exact:true})).toHaveCount(0);
});
test('Mini App admin URL falls back to the customer cabinet',async({page})=>{
 const f=await fixture(page);await page.goto('/mini-app/admin/clients?lang=en');await expect(page.getByRole('heading',{name:'Mini client'})).toBeVisible();
 expect(f.calls.some(c=>c.path.includes('/operator/')||c.path.includes('/me/security'))).toBe(false);await expect(page.getByLabel('Email',{exact:true})).toHaveCount(0);
});
