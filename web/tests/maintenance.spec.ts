import {test,expect,type Page,type Route} from '@playwright/test';
import type {components} from '../src/api/schema.gen';
type Model<K extends keyof components['schemas']>=components['schemas'][K];
const operator={account:{account_id:'10000000-0000-4000-8000-000000000001',email:'operator@example.test',email_verified:true,locale:'en',telegram_linked:false},csrf_token:'s'.repeat(43)};
const account:Model<'AccountResult'>={account:{account_id:'b496e45c-4e80-47d6-a868-e3c1da4e4f35',email:'client@example.test',email_verified:true,locale:'en',telegram_linked:false},csrf_token:'x'.repeat(43),capabilities:{trial_available:true}};
const none:Model<'Subscription'>={status:'none',devices:0,traffic_limit_bytes:0,traffic_used_bytes:null,observed_at:null,data_stale:false,expires_at:null,access_profile:'unknown',vpn_banned:false,access_operation_id:null,access_operation_status:null};
const maintenance=(enabled:boolean,revision:number):Model<'MaintenanceStatus'>=>({enabled,revision,changed_at:revision?'2026-10-09T10:00:00Z':null});
const failure=(code:string,status=503)=>({status,json:{error:{code,message:code,request_id:''}}});
async function operatorFixture(page:Page,extra:(route:Route,state:{enabled:boolean;revision:number})=>Promise<boolean>,state={enabled:false,revision:0}){
 await page.route('**/api/v1/**',async route=>{const path=new URL(route.request().url()).pathname;
  if(path.endsWith('/operator/session'))return route.fulfill({json:operator});
  if(path.endsWith('/operator/maintenance')){if(await extra(route,state))return;return route.fulfill({json:maintenance(state.enabled,state.revision)});}
  return route.fulfill({json:{}});
 });return state;
}
for(const lang of ['en','ru'] as const)test('operator toggle, unknown reply replay and opposite transition '+lang,async({page})=>{
 const labels=lang==='en'?{heading:'Maintenance',on:'Turn maintenance on',off:'Turn maintenance off',reason:'Reason for change',confirm:'I confirm this maintenance change',saved:'Current status updated.'}:{heading:'Обслуживание',on:'Включить обслуживание',off:'Выключить обслуживание',reason:'Причина изменения',confirm:'Подтверждаю изменение режима обслуживания',saved:'Текущее состояние обновлено.'};
 const attempts:{key:string;body:Model<'MaintenanceInput'>;csrf:string}[]=[];const state=await operatorFixture(page,async(route,current)=>{
  if(route.request().method()!=='POST')return false;
  const body=route.request().postDataJSON() as Model<'MaintenanceInput'>;attempts.push({key:route.request().headers()['idempotency-key'],body,csrf:route.request().headers()['x-csrf-token']});
  if(attempts.length===1)return route.fulfill(failure('SERVICE_UNAVAILABLE')).then(()=>true);
  if(attempts.length===2){current.enabled=true;current.revision=1;return route.fulfill({json:maintenance(true,1)}).then(()=>true);}
  current.enabled=false;current.revision=2;return route.fulfill({json:maintenance(false,2)}).then(()=>true);
 });
 await page.goto('/admin/maintenance?lang='+lang);await expect(page.getByRole('heading',{name:labels.heading,exact:true})).toBeVisible();
 const reason=page.getByRole('textbox',{name:labels.reason});await reason.fill('Planned update');const check=page.getByRole('checkbox',{name:labels.confirm});await check.check();
 const button=page.getByRole('button',{name:labels.on});await button.focus();await page.keyboard.press('Enter');await expect.poll(()=>attempts.length).toBe(1);await expect(page.getByRole('alert')).toBeVisible();await expect(reason).toBeDisabled();await expect(check).toBeDisabled();
 await button.click();await expect.poll(()=>attempts.length).toBe(2);await expect(page.getByRole('status').filter({hasText:labels.saved})).toBeVisible();await expect(page.getByText(lang==='en'?'Maintenance is on':'Обслуживание включено',{exact:true})).toBeFocused();expect(state.enabled).toBe(true);
 expect(attempts[0].key).toMatch(/^[0-9a-f-]{36}$/);expect(attempts[0]).toEqual(attempts[1]);expect(attempts[0].csrf).toBe('s'.repeat(43));expect(attempts[0].body).toEqual({enabled:true,expected_revision:0,reason:'Planned update',confirmed:true});
 await reason.fill('Back online');await check.check();await page.getByRole('button',{name:labels.off}).click();await expect.poll(()=>attempts.length).toBe(3);expect(attempts[2].key).not.toBe(attempts[0].key);expect(attempts[2].body.expected_revision).toBe(1);expect(state.enabled).toBe(false);
});
test('operator conflict refetches current state and revoked role removes resource',async({page})=>{
 let posts=0;let deny=false;const state=await operatorFixture(page,async(route,current)=>{
  if(route.request().method()!=='POST')return false;posts++;
  if(deny)return route.fulfill(failure('ACCOUNT_RESTRICTED',403)).then(()=>true);
  current.enabled=true;current.revision=1;return route.fulfill(failure('MAINTENANCE_CONFLICT',409)).then(()=>true);
 });
 await page.goto('/admin/maintenance?lang=en');await page.getByRole('textbox',{name:'Reason for change'}).fill('Concurrent update');await page.getByRole('checkbox',{name:'I confirm this maintenance change'}).check();await page.getByRole('button',{name:'Turn maintenance on'}).click();await expect(page.getByRole('alert')).toContainText('Maintenance changed');await expect(page.getByText('Maintenance is on',{exact:true})).toBeFocused();expect(posts).toBe(1);expect(state.revision).toBe(1);
 deny=true;await page.getByRole('textbox',{name:'Reason for change'}).fill('Return online');await page.getByRole('checkbox',{name:'I confirm this maintenance change'}).check();await page.getByRole('button',{name:'Turn maintenance off'}).click();await expect(page.getByRole('heading',{name:'Operator access required'})).toBeVisible();
});
async function clientFixture(page:Page,status:Model<'MaintenanceStatus'>|null,mini=false){
 if(mini)await page.route('https://telegram.org/js/telegram-web-app.js',route=>route.fulfill({contentType:'application/javascript',body:"window.Telegram={WebApp:{initData:'signed-test',ready(){},expand(){},themeParams:{},BackButton:{show(){},hide(){},onClick(){},offClick(){}}}}"}));
 const writes:string[]=[];
 await page.route('**/api/v1/**',async route=>{const req=route.request(),path=new URL(req.url()).pathname;if(req.method()==='POST'&&path.includes('/orders'))writes.push(path);
  if(path==='/api/v1/maintenance')return status?route.fulfill({json:status}):route.fulfill(failure('SERVICE_UNAVAILABLE'));
  if(path==='/api/v1/me'||path==='/api/v1/telegram/mini-app/account')return route.fulfill({json:mini?{...account,account:{...account.account,email:null,display_name:'Mini client',telegram_id:444},session_token:'mini_'+'b'.repeat(43)}:{...account,account:{...account.account,telegram_linked:true}}});
  if(path==='/api/v1/telegram/mini-app/session')return route.fulfill({json:{...account,account:{...account.account,email:null,display_name:'Mini client',telegram_id:444},session_token:'mini_'+'b'.repeat(43)}});
  if(path==='/api/v1/subscription')return route.fulfill({json:none});
  if(path==='/api/v1/trial-requests/current')return route.fulfill({json:{request:null}});
  if(path==='/api/v1/orders/current')return route.fulfill({json:{order:null,can_purchase:true}});
  if(path==='/api/v1/catalogue')return route.fulfill({json:{plans:[{plan_id:'70000000-0000-4000-8000-000000000001',revision:1,devices:2,traffic_gb:10,profile:'regular',hidden:false,periods:[30],prices:[{period_days:30,currency:mini?'XTR':'RUB',amount_minor:'100'}]}]}});
  if(path==='/api/v1/payment-methods')return route.fulfill({json:{methods:[{id:mini?'telegram_stars':'manual',currency:mini?'XTR':'RUB'}]}});
  if(path==='/api/v1/stars-subscription')return route.fulfill({json:{state:'resume_allowed',order_id:null,provider_state:'canceled',control_state:'none',paid_until:null,period_phase:'lapsed',can_cancel:true,can_resume:true,external_billing_blocked:false,needs_review:false}});
  if(path==='/api/v1/reminders')return route.fulfill({json:{version:'reminders-v1',email_enabled:false,email_available:false,reminders:[]}});
  if(path==='/api/v1/notices')return route.fulfill({json:{version:'operator-notices-v1',email_enabled:false,email_available:false,page:1,per_page:20,total:0,notices:[]}});
  return route.fulfill({json:{}});
 });return writes;
}
test('client status on disables trial, purchase and Stars resume, while cancel remains',async({page})=>{
 const writes=await clientFixture(page,maintenance(true,1));await page.goto('/cabinet?lang=en');await expect(page.getByText('Maintenance is on',{exact:true})).toBeVisible();await expect(page.getByRole('button',{name:'Request trial'})).toBeDisabled();await expect(page.getByRole('button',{name:'Allow renewal in Telegram'})).toBeDisabled();await expect(page.getByRole('button',{name:'Cancel auto-renewal'})).toBeEnabled();
 await page.goto('/catalogue?lang=en');await page.getByRole('button',{name:'Select plan'}).click();await expect(page.getByRole('button',{name:'Buy plan'})).toBeDisabled();expect(writes).toEqual([]);
});
test('failed status is shown as unknown, not off, and Mini reads maintenance with bearer',async({page})=>{
 await clientFixture(page,null);await page.goto('/cabinet?lang=en');await expect(page.getByText('Maintenance status is unknown',{exact:true})).toBeVisible();await expect(page.getByText('Maintenance is off')).toHaveCount(0);
});
test('Mini client sees maintenance state and blocked trial',async({page})=>{
 let bearer='';await clientFixture(page,maintenance(true,1),true);await page.route('**/api/v1/maintenance',async route=>{bearer=route.request().headers().authorization??'';await route.fulfill({json:maintenance(true,1)});});
 await page.goto('/mini-app/cabinet?lang=en');await expect(page.getByRole('heading',{name:'Mini client'})).toBeVisible();await expect(page.getByText('Maintenance is on',{exact:true})).toBeVisible();await expect(page.getByRole('button',{name:'Request trial'})).toBeDisabled();expect(bearer).toMatch(/^Bearer mini_/);
});
test('off status keeps trial available and read failure has retry',async({page})=>{
 let reads=0;await clientFixture(page,maintenance(false,0));await page.route('**/api/v1/maintenance',route=>{reads++;return reads===1?route.fulfill(failure('SERVICE_UNAVAILABLE')):route.fulfill({json:maintenance(false,0)});});
 await page.goto('/cabinet?lang=en');await expect(page.getByText('Maintenance status is unknown',{exact:true})).toBeVisible();await page.getByRole('button',{name:'Check status'}).click();await expect(page.getByText('Maintenance status is unknown',{exact:true})).toHaveCount(0);await expect(page.getByRole('button',{name:'Request trial'})).toBeEnabled();expect(reads).toBe(2);
});
test('Mini existing Stars invoice remains payable while new invoice is paused',async({page})=>{
 const id='80000000-0000-4000-8000-000000000001';let ready=false;const order:Model<'PurchaseOrder'>={order_id:id,action:'purchase',quote:{plan_id:'70000000-0000-4000-8000-000000000001',revision:1,devices:2,period_days:30,traffic_gb:10,profile:'regular',amount_minor:'100',currency:'XTR'},payment_method:'telegram_stars',payment_type:'STARS',payment_status:'pending',fulfillment_status:'not_started',created_at:'2026-10-09T00:00:00Z',expires_at:new Date(Date.now()+30*60_000).toISOString(),expired:false,can_pay:true,can_cancel:true,review_required:false,access_operation_id:null,checkout:null,stars_checkout:{state:'preparing',url:null}};
 await clientFixture(page,maintenance(true,1),true);await page.route('**/api/v1/orders/'+id,route=>route.fulfill({json:{...order,stars_checkout:ready?{state:'ready',url:'https://t.me/$owned-invoice'}:order.stars_checkout}}));
 await page.goto('/mini-app/orders/'+id+'?lang=en');await expect(page.getByRole('button',{name:'Pay with Stars'})).toBeDisabled();
 ready=true;await page.reload();await expect(page.getByRole('button',{name:'Pay with Stars'})).toBeEnabled();await expect(page.getByRole('button',{name:'Cancel order'})).toBeEnabled();
});
