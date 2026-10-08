import {test,expect,type Page} from '@playwright/test';
import type {components} from '../src/api/schema.gen';
type Model<K extends keyof components['schemas']>=components['schemas'][K];
const accountId='11111111-1111-4111-8111-111111111111',orderId='80000000-0000-4000-8000-000000000001',planId='70000000-0000-4000-8000-000000000001';
const token='mini_'+'b'.repeat(43),csrf='c'.repeat(43),charge='stars:'+'a'.repeat(64);
const profile:Model<'MiniAppAccountResult'>={account:{account_id:accountId,email:null,email_verified:false,display_name:'Stars client',telegram_id:701,telegram_linked:true,locale:'en'},csrf_token:csrf,capabilities:{trial_available:false}};
const plan:Model<'CataloguePlanSnapshot'>={plan_id:planId,revision:1,devices:2,traffic_gb:10,profile:'regular',hidden:false,periods:[30],prices:[{period_days:30,currency:'XTR',amount_minor:'100'}]};
const order:Model<'PurchaseOrder'>={order_id:orderId,action:'purchase',quote:{plan_id:planId,revision:1,devices:2,period_days:30,traffic_gb:10,profile:'regular',amount_minor:'100',currency:'XTR'},payment_method:'telegram_stars',payment_type:'STARS',payment_status:'pending',fulfillment_status:'not_started',created_at:'2026-10-08T00:00:00Z',expires_at:new Date(Date.now()+30*60_000).toISOString(),expired:false,can_pay:true,can_cancel:false,review_required:false,access_operation_id:null,checkout:null,stars_checkout:{state:'preparing',url:null}};
const receipt:Model<'PaymentHistoryReceipt'>={operation_id:charge,order_id:orderId,payment_method:'telegram_stars',created_at:order.created_at,occurred_at:order.created_at,gross_minor:'100',net_minor:null,currency:'XTR',raw_currency:'XTR',source:'provider',funds_order:true,review_required:false,review_reason:null,codepro:false,unaccepted:false,crypto_amounts:null};
const refund:Model<'PaymentRefund'>={refund_id:'60000000-0000-4000-8000-000000000001',order_id:orderId,receipt_operation_id:charge,payment_method:'telegram_stars',created_at:order.created_at,receipt_gross_minor:'100',receipt_currency:'XTR',returned_amount:'100',returned_currency:'XTR',reference:charge,reason:'Owned refund proof',operator_account_id:null,source:'telegram'};
const none:Model<'StarsSubscription'>={state:'none',order_id:null,provider_state:null,control_state:null,paid_until:null,period_phase:'none',can_cancel:false,can_resume:false,external_billing_blocked:false,needs_review:false};
const active:Model<'StarsSubscription'>={...none,state:'active',order_id:orderId,provider_state:'active',control_state:'none',paid_until:'2026-11-08T00:00:00Z',period_phase:'current',can_cancel:true,external_billing_blocked:true};

async function fixture(page:Page,options:{sdk?:'missing'|'throw';url?:string;current?:Model<'PurchaseOrder'>;plan?:Model<'CataloguePlanSnapshot'>;subscription?:Model<'StarsSubscription'>;control?:Model<'StarsSubscription'>;managing?:boolean}={}){
 let current=options.current??structuredClone(order),invoiceReads=0,reads=0,refundState:Model<'StarsRefund'>['state']|undefined;
 let subscription=options.subscription??none;
 const calls:{path:string;body:any;key?:string;auth?:string;csrf?:string}[]=[];
 await page.route('https://telegram.org/js/telegram-web-app.js',r=>r.fulfill({contentType:'application/javascript',body:`window.__stars={opened:[],done:null};window.Telegram={WebApp:{initData:'owned-stars-launch',ready(){},expand(){},openInvoice:${options.sdk==='missing'?'undefined':`function(url,done){${options.sdk==='throw'?'throw new Error("owned SDK error");':'window.__stars.opened.push(url);window.__stars.done=done;'}}`}}};`}));
 await page.route('**/api/v1/**',async r=>{
  const req=r.request(),path=new URL(req.url()).pathname;let body:any;try{body=req.postDataJSON()}catch{}
  calls.push({path,body,key:req.headers()['idempotency-key'],auth:req.headers().authorization,csrf:req.headers()['x-csrf-token']});
  if(path.endsWith('/telegram/mini-app/session'))return r.fulfill({json:{...profile,session_token:token}});
  if(path.endsWith('/telegram/mini-app/account'))return r.fulfill({json:profile});
  if(path.endsWith('/auth/session'))return r.fulfill({json:{csrf_token:csrf}});
  if(path.endsWith('/catalogue'))return r.fulfill({json:{plans:[options.plan??plan]}});
  if(path==='/api/v1/stars-subscription')return r.fulfill({json:subscription});
  if(path==='/api/v1/stars-subscription/control'){subscription=options.control??subscription;return r.fulfill({json:subscription});}
  if(path==='/api/v1/subscription/renewal')return r.fulfill(options.managing?{json:options.plan??plan}:{status:409,json:{error:{code:'RENEWAL_NOT_ELIGIBLE'}}});
  if(path==='/api/v1/subscription/plan-change')return r.fulfill(options.managing?{json:{current_plan_id:'70000000-0000-4000-8000-000000000002',source_access_operation_id:'90000000-0000-4000-8000-000000000001'}}:{status:409,json:{error:{code:'PLAN_CHANGE_NOT_ELIGIBLE'}}});
  if(path==='/api/v1/subscription')return r.fulfill({json:{status:'none',devices:0,traffic_limit_bytes:0,traffic_used_bytes:null,observed_at:null,data_stale:false,expires_at:null,access_profile:'unknown',vpn_banned:false,access_operation_id:null,access_operation_status:null}});
  if(path==='/api/v1/trial-requests/current')return r.fulfill({json:{request:null}});
  if(path.endsWith('/payment-methods'))return r.fulfill({json:{methods:[{id:'telegram_stars',currency:'XTR'}]}});
  if(path==='/api/v1/orders/current')return r.fulfill({json:{order:null,can_purchase:true}});
  if(path==='/api/v1/orders'){current={...current,action:body.action,quote:{...current.quote,...(body.stars_recurring?{stars_recurring:true}:{})}};return r.fulfill({status:201,json:current});}
  if(path.endsWith('/stars-invoice')){invoiceReads++;current={...current,stars_checkout:{state:'ready',url:options.url??'https://t.me/$Owned_invoice'}};return r.fulfill({json:current});}
  if(path==='/api/v1/orders/'+orderId){reads++;return r.fulfill({json:current});}
  if(path.endsWith('/operator/session'))return r.fulfill({json:{account:{account_id:'10000000-0000-4000-8000-000000000001',email:'operator@example.test',locale:'en'},role:'operator',csrf_token:csrf}});
  if(path==='/api/v1/operator/clients/'+accountId)return r.fulfill({json:{client:{account_id:accountId,kind:'telegram',display_name:'Stars client',email:null,telegram_id:'701',locale:'en',created_at:null,restricted:false,vpn_banned:false,had_subscription:false},subscription:{status:'none',access_profile:'unknown',vpn_banned:false,devices:0,traffic_limit_bytes:0,expires_at:null,traffic_used_bytes:null,observed_at:null,data_stale:true,access_operation_id:null,access_operation_status:null},server:null,support:null,trial_requests:[],trial_has_more:false,audit_events:[],audit_has_more:false}});
  if(path.endsWith('/support'))return r.fulfill({json:{conversation:null,messages:[],has_more:false,oldest_sequence:null}});
  if(path.endsWith('/orders/current'))return r.fulfill({json:{order:null}});
  if(path.endsWith('/payment-history'))return r.fulfill({json:{kind:body.kind,orders:body.kind==='orders'?[{...order,review_reason:null}]:[],receipts:body.kind==='receipts'?[receipt]:[],refunds:body.kind==='refunds'?[refund]:[],legacy_transactions:[],has_more:false}});
  if(path.endsWith('/payment-case'))return r.fulfill({json:{order:{...current,can_pay:false},receipt,refund:null,financial_review_open:true,can_confirm_refund:false,can_refund_stars:!refundState,stars_refund_state:refundState??null}});
  if(path.endsWith('/stars-refund')){refundState='uncertain';return r.fulfill({json:{state:refundState,refund:null}});}
  return r.fulfill({json:{}});
 });
 return {calls,invoiceReads:()=>invoiceReads,reads:()=>reads,set:(v:Model<'PurchaseOrder'>)=>{current=v;},setSubscription:(v:Model<'StarsSubscription'>)=>{subscription=v;}};
}

for(const lang of ['en','ru'] as const)test('Mini Stars catalogue and invoice '+lang,async({page})=>{
 const f=await fixture(page);await page.setViewportSize({width:375,height:812});await page.goto('/mini-app/catalogue?lang='+lang);
 await page.getByRole('button',{name:lang==='en'?'Select plan':'Выбрать тариф'}).click();
 const buy=page.getByRole('button',{name:lang==='en'?'Buy plan':'Купить тариф'});await expect(buy).toBeEnabled();await buy.focus();await page.keyboard.press('Enter');
 await page.getByRole('button',{name:lang==='en'?'Confirm purchase':'Подтвердить покупку'}).click();
 await expect(page).toHaveURL(new RegExp('/mini-app/orders/'+orderId));
 const pay=page.getByRole('button',{name:lang==='en'?'Pay with Stars':'Оплатить Stars'});await expect(pay).toBeEnabled();await pay.click();
 await expect.poll(()=>page.evaluate(()=>(window as any).__stars.opened.length)).toBe(1);
 expect(f.calls.find(c=>c.path==='/api/v1/orders')).toMatchObject({body:{action:'purchase',payment_method:'telegram_stars',payment_type:'STARS',plan_id:planId,revision:1,period_days:30},auth:'Bearer '+token});
 expect(f.calls.find(c=>c.path==='/api/v1/orders')?.body).not.toHaveProperty('stars_recurring');
 expect(f.calls.filter(c=>c.path.endsWith('/mini-app/session'))).toHaveLength(1);expect(f.invoiceReads()).toBe(1);
 await page.evaluate(()=>(window as any).__stars.done('paid'));await expect(page.getByText(lang==='en'?'Waiting for payment confirmation.':'Ожидаем подтверждения оплаты.',{exact:true})).toBeVisible();
 expect(await page.evaluate(()=>JSON.stringify(localStorage)+JSON.stringify(sessionStorage))).not.toMatch(/mini_|csrf_token|owned-stars-launch/);
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});

for(const state of ['cancelled','failed','pending'] as const)test('Stars SDK '+state+' refreshes server facts only',async({page})=>{
 const f=await fixture(page);await page.goto('/mini-app/orders/'+orderId+'?lang=en');await page.getByRole('button',{name:'Pay with Stars'}).click();
 await expect.poll(()=>page.evaluate(()=>typeof (window as any).__stars.done)).toBe('function');const before=f.reads();await page.evaluate(state=>(window as any).__stars.done(state),state);await expect.poll(()=>f.reads()).toBeGreaterThan(before);
 await expect(page.getByText('Waiting for payment confirmation.',{exact:true})).toBeVisible();expect(f.calls.filter(c=>c.path.endsWith('/stars-invoice'))).toHaveLength(1);
 const hint={cancelled:'The Telegram payment window was closed.',failed:'Telegram reported a payment error. Check the order before retrying.',pending:'Telegram is still processing the payment. Wait for confirmation.'}[state];
 await expect(page.getByRole('status').filter({hasText:hint})).toBeVisible();
});

for(const sdk of ['missing','throw'] as const)test('Stars unavailable SDK '+sdk+' has a visible error',async({page})=>{
 await fixture(page,{sdk});await page.goto('/mini-app/orders/'+orderId+'?lang=en');await page.getByRole('button',{name:'Pay with Stars'}).click();await expect(page.getByRole('alert')).toBeVisible();await expect(page).toHaveURL(/\/mini-app\/orders\//);
});
for(const url of ['https://evil.example/$invoice','https://t.me/$owned?secret=1','http://t.me/$owned'])test('Stars refuses unsafe invoice '+new URL(url).protocol+new URL(url).host+new URL(url).search,async({page})=>{
 await fixture(page,{url});await page.goto('/mini-app/orders/'+orderId+'?lang=en');await page.getByRole('button',{name:'Pay with Stars'}).click();await expect(page.getByRole('alert')).toBeVisible();expect(await page.evaluate(()=>(window as any).__stars.opened)).toEqual([]);
});
test('Stars rechecks current CanPay and browser never opens a native invoice',async({page})=>{
 const f=await fixture(page);await page.goto('/mini-app/orders/'+orderId+'?lang=en');const pay=page.getByRole('button',{name:'Pay with Stars'});await expect(pay).toBeVisible();f.set({...order,can_pay:false});await pay.click();expect(f.invoiceReads()).toBe(0);expect(await page.evaluate(()=>(window as any).__stars.opened)).toEqual([]);await expect(pay).toHaveCount(0);
 await page.goto('/orders/'+orderId+'?lang=en');await expect(page.getByRole('button',{name:'Pay with Stars'})).toHaveCount(0);
});
for(const lang of ['en','ru'] as const)test('native Stars refund uncertainty and real provider source '+lang,async({page})=>{
 const f=await fixture(page);await page.goto('/admin/clients/'+accountId+'/show?lang='+lang);await page.getByRole('region',{name:lang==='en'?'Payment history':'История платежей'}).getByRole('button',{name:lang==='en'?'Open payment case':'Разобрать платёж'}).click();
 const area=page.getByRole('region',{name:lang==='en'?'Payment case':'Разбор платежа',exact:true});await area.getByLabel(lang==='en'?'Reason':'Причина',{exact:true}).fill('Owned operator request');await expect(area.getByLabel(lang==='en'?'External operation reference':'Номер внешней операции')).toHaveCount(0);
 await area.getByLabel(lang==='en'?'Return the full Stars payment':'Вернуть весь платёж Stars').check();await area.getByLabel(lang==='en'?'Keep the current access unchanged':'Сохранить текущий доступ без изменений').check();await area.getByRole('button',{name:lang==='en'?'Refund Stars':'Вернуть Stars'}).click();
 await expect(area.getByText(lang==='en'?'Refund result is uncertain. Refresh the case; do not send another payout.':'Результат возврата неизвестен. Обновите разбор; не отправляйте повторный перевод.',{exact:true})).toBeVisible();await expect(area.getByRole('button',{name:lang==='en'?'Refund Stars':'Вернуть Stars'})).toHaveCount(0);
 expect(f.calls.filter(c=>c.path.endsWith('/stars-refund'))).toHaveLength(1);expect(f.calls.find(c=>c.path.endsWith('/stars-refund'))?.body).toEqual({receipt_operation_id:charge,reason:'Owned operator request',confirm_full:true,keep_access:true});
 await page.goto('/cabinet/history?lang='+lang);await page.getByLabel(lang==='en'?'History type':'Вид истории').selectOption('refunds');await expect(page.getByText(lang==='en'?'Confirmed by Telegram':'Подтверждено Telegram',{exact:true})).toBeVisible();await expect(page.locator('.payment-history')).not.toContainText(lang==='en'?'Confirmed by operator':'Подтверждено оператором');
});

for(const lang of ['en','ru'] as const)test('explicit recurring Stars purchase '+lang,async({page})=>{
 const f=await fixture(page);await page.goto('/mini-app/catalogue?lang='+lang);await page.getByRole('button',{name:lang==='en'?'Select plan':'Выбрать тариф'}).click();
 const choice=page.getByRole('checkbox',{name:lang==='en'?'Automatically renew every 30 days':'Автопродление каждые 30 дней'});await expect(choice).not.toBeChecked();await choice.focus();await page.keyboard.press('Space');await expect(choice).toBeChecked();
 await expect(page.getByText(lang==='en'?'100 XTR every 30 days':'100 XTR каждые 30 дней',{exact:true})).toBeVisible();
 await page.getByRole('button',{name:lang==='en'?'Buy plan':'Купить тариф'}).click();await page.getByRole('button',{name:lang==='en'?'Confirm purchase':'Подтвердить покупку'}).click();await expect(page).toHaveURL(/\/mini-app\/orders\//);
 expect(f.calls.find(c=>c.path==='/api/v1/orders')?.body.stars_recurring).toBe(true);
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});
test('recurring choice resets for unsupported period and price',async({page})=>{
 const offered={...plan,periods:[30,60],prices:[...plan.prices,{period_days:60,currency:'XTR' as const,amount_minor:'190'}]};await fixture(page,{plan:offered});await page.goto('/mini-app/catalogue?lang=en');await page.getByRole('button',{name:'Select plan'}).click();
 const choice=page.getByRole('checkbox',{name:'Automatically renew every 30 days'});await choice.check();await page.getByRole('combobox',{name:'Period',exact:true}).selectOption('60');await expect(choice).toHaveCount(0);await page.getByRole('combobox',{name:'Period',exact:true}).selectOption('30');await expect(choice).not.toBeChecked();
 await fixture(page,{plan:{...plan,prices:[{period_days:30,currency:'XTR',amount_minor:'10001'}]}});await page.reload();await page.getByRole('button',{name:'Select plan'}).click();await expect(choice).toHaveCount(0);
});

for(const lang of ['en','ru'] as const)test('own recurring cancellation confirmation '+lang,async({page})=>{
 const f=await fixture(page,{subscription:active,control:{...active,state:'canceled',control_state:'confirmed',can_cancel:false,can_resume:true,external_billing_blocked:false}});await page.goto('/mini-app/cabinet?lang='+lang);
 const region=page.getByRole('region',{name:lang==='en'?'Stars auto-renewal':'Автопродление Stars'}),cancel=region.getByRole('button',{name:lang==='en'?'Cancel auto-renewal':'Отменить автопродление',exact:true});
 await expect(region.getByText(lang==='en'?'Auto-renewal is active.':'Автопродление активно.',{exact:true})).toBeVisible();await cancel.focus();await page.keyboard.press('Enter');
 const confirm=region.getByRole('button',{name:lang==='en'?'Confirm cancellation':'Подтвердить отмену',exact:true});await expect(confirm).toBeFocused();expect(f.calls.filter(c=>c.path.endsWith('/stars-subscription/control'))).toHaveLength(0);
 await region.getByRole('button',{name:lang==='en'?'Cancel confirmation':'Отменить подтверждение'}).click();await expect(cancel).toBeFocused();await cancel.click();await confirm.click();
 await expect(region.getByText(lang==='en'?'Telegram has confirmed cancellation. Paid access is retained.':'Telegram подтвердил отмену. Оплаченный доступ сохраняется.',{exact:true})).toBeVisible();
 await expect(region.getByRole('heading',{name:lang==='en'?'Stars auto-renewal':'Автопродление Stars'})).toBeFocused();
 const sent=f.calls.filter(c=>c.path.endsWith('/stars-subscription/control'));expect(sent).toHaveLength(1);expect(sent[0]).toMatchObject({body:{action:'cancel',confirmed:true},auth:'Bearer '+token,csrf});expect(sent[0].key).toMatch(/^[0-9a-f-]{36}$/);
 expect(await page.evaluate(()=>JSON.stringify(localStorage)+JSON.stringify(sessionStorage))).not.toMatch(/mini_|csrf_token|owned-stars-launch/);
});
for(const lang of ['en','ru'] as const)test('resume only permits Telegram user reenabling '+lang,async({page})=>{
 await fixture(page,{subscription:{...active,state:'canceled',can_cancel:false,can_resume:true,external_billing_blocked:false},control:{...active,state:'resume_allowed',control_state:'confirmed'}});await page.goto('/mini-app/orders/'+orderId+'?lang='+lang);
 const region=page.getByRole('region',{name:lang==='en'?'Stars auto-renewal':'Автопродление Stars'});await region.getByRole('button',{name:lang==='en'?'Allow renewal in Telegram':'Разрешить продление в Telegram',exact:true}).click();await region.getByRole('button',{name:lang==='en'?'Confirm renewal':'Подтвердить продление',exact:true}).click();
 await expect(region.getByText(lang==='en'?'You can enable renewal in Telegram. No new payment is confirmed.':'Можно включить продление в Telegram. Новый платёж не подтверждён.',{exact:true})).toBeVisible();await expect(page.getByText(lang==='en'?'Waiting for payment confirmation.':'Ожидаем подтверждения оплаты.',{exact:true})).toBeVisible();
});
test('uncertain cancellation keeps billing closed and status refresh is read only',async({page})=>{
 const f=await fixture(page,{subscription:active,control:{...active,state:'cancel_uncertain',control_state:'uncertain',can_resume:false,needs_review:true}});await page.goto('/mini-app/cabinet?lang=en');const region=page.getByRole('region',{name:'Stars auto-renewal'});
 await region.getByRole('button',{name:'Cancel auto-renewal',exact:true}).click();await region.getByRole('button',{name:'Confirm cancellation'}).click();await expect(region.getByText('Cancellation result is uncertain. Refresh status or contact support.',{exact:true})).toBeVisible();await expect(region.getByRole('button',{name:'Allow renewal in Telegram',exact:true})).toHaveCount(0);await expect(region.getByText('Other payment methods remain unavailable.',{exact:true})).toBeVisible();
 await region.getByRole('button',{name:'Refresh auto-renewal status'}).click();expect(f.calls.filter(c=>c.path.endsWith('/stars-subscription/control'))).toHaveLength(1);
});
test('failed own status is visible and retry recovers without a financial action',async({page})=>{
 const f=await fixture(page,{subscription:active});let unavailable=true;await page.route('**/api/v1/stars-subscription',r=>unavailable?r.fulfill({status:503,json:{error:{code:'SERVICE_UNAVAILABLE'}}}):r.fallback());await page.goto('/mini-app/cabinet?lang=en');const region=page.getByRole('region',{name:'Stars auto-renewal'});await expect(region.getByRole('alert')).toBeVisible();await expect(region.getByRole('button',{name:'Cancel auto-renewal',exact:true})).toHaveCount(0);unavailable=false;await region.getByRole('button',{name:'Retry',exact:true}).click();await expect(region.getByText('Auto-renewal is active.',{exact:true})).toBeVisible();expect(f.calls.filter(c=>c.path.endsWith('/stars-subscription/control'))).toHaveLength(0);
});
for(const status of [401,403,404])test('own recurring status refuses unauthorized scope '+status,async({page})=>{
 await fixture(page,{subscription:active});await page.route('**/api/v1/stars-subscription',r=>r.fulfill({status,json:{error:{code:status===401?'INVALID_CREDENTIALS':'NOT_FOUND'}}}));await page.goto('/mini-app/cabinet?lang=en');await expect(page.getByRole('alert')).toBeVisible();await expect(page.getByText('Auto-renewal is active.',{exact:true})).toHaveCount(0);await expect(page.getByRole('button',{name:'Cancel auto-renewal',exact:true})).toHaveCount(0);
});
test('recurring control retry retains the idempotency key',async({page})=>{
 const f=await fixture(page,{subscription:active,control:{...active,state:'canceled',can_cancel:false,can_resume:true,external_billing_blocked:false}});let fail=true;await page.route('**/api/v1/stars-subscription/control',async r=>{if(!fail)return r.fallback();fail=false;f.calls.push({path:'/api/v1/stars-subscription/control',body:r.request().postDataJSON(),key:r.request().headers()['idempotency-key']});return r.fulfill({status:503,json:{error:{code:'SERVICE_UNAVAILABLE'}}});});await page.goto('/mini-app/cabinet?lang=en');const region=page.getByRole('region',{name:'Stars auto-renewal'});await region.getByRole('button',{name:'Cancel auto-renewal',exact:true}).click();const confirm=region.getByRole('button',{name:'Confirm cancellation'});await confirm.click();await expect(region.getByRole('alert')).toBeVisible();await confirm.click();await expect(region.getByText('Telegram has confirmed cancellation. Paid access is retained.',{exact:true})).toBeVisible();const sent=f.calls.filter(c=>c.path.endsWith('/stars-subscription/control'));expect(sent).toHaveLength(2);expect(sent[0].key).toBe(sent[1].key);
});
test('leaving the cabinet aborts an outstanding recurring control',async({page})=>{
 await fixture(page,{subscription:active});let release!:()=>void,entered=false;const held=new Promise<void>(resolve=>release=resolve);await page.route('**/api/v1/stars-subscription/control',async r=>{entered=true;await held;await r.fulfill({json:{...active,state:'canceled'}}).catch(()=>{});});await page.goto('/mini-app/cabinet?lang=en');const region=page.getByRole('region',{name:'Stars auto-renewal'});await region.getByRole('button',{name:'Cancel auto-renewal',exact:true}).click();await region.getByRole('button',{name:'Confirm cancellation'}).click();await expect.poll(()=>entered).toBe(true);await page.getByRole('link',{name:'Plans',exact:true}).click();await expect(page.getByRole('heading',{name:'Plans',exact:true})).toBeVisible();release();await expect(page.getByText('Telegram has confirmed cancellation. Paid access is retained.',{exact:true})).toHaveCount(0);
});
for(const action of ['renew','change_plan'] as const)for(const allowed of [false,true])test('Mini managing '+action+' uses owner eligibility '+allowed,async({page})=>{
 const f=await fixture(page,{managing:allowed});await page.goto('/mini-app/cabinet/'+(action==='renew'?'renew':'change-plan')+'?lang=en');const buy=page.getByRole('button',{name:action==='renew'?'Renew subscription':'Change plan',exact:true});
 if(!allowed){await expect(page.getByRole('alert')).toBeVisible();await expect(buy).toHaveCount(0);return;}
 if(action==='change_plan')await page.getByRole('button',{name:'Select plan'}).click();await expect(buy).toBeEnabled();await expect(page.getByRole('checkbox',{name:'Automatically renew every 30 days'})).toHaveCount(0);await buy.click();await page.getByRole('button',{name:action==='renew'?'Confirm renewal':'Confirm plan change',exact:true}).click();await expect(page).toHaveURL(/\/mini-app\/orders\//);const sent=f.calls.find(c=>c.path==='/api/v1/orders');expect(sent?.body).toMatchObject({action,payment_method:'telegram_stars',payment_type:'STARS'});expect(sent?.body).not.toHaveProperty('stars_recurring');if(action==='change_plan')expect(sent?.body.source_access_operation_id).toBe('90000000-0000-4000-8000-000000000001');
});
