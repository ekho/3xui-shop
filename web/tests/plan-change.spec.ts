import {test,expect,type Page,type Route} from '@playwright/test';
import type {components} from '../src/api/schema.gen';

type Model<K extends keyof components['schemas']>=components['schemas'][K];
const planId='70000000-0000-4000-8000-000000000002';
const orderId='80000000-0000-4000-8000-000000000001';
const sourceId='90000000-0000-4000-8000-000000000001';
const context:Model<'PlanChangeContext'>={current_plan_id:'70000000-0000-4000-8000-000000000001',source_access_operation_id:sourceId};
const plan:Model<'CataloguePlanSnapshot'>={plan_id:planId,revision:3,devices:4,traffic_gb:120,profile:'euru',hidden:false,periods:[30,90],prices:[{period_days:30,currency:'RUB',amount_minor:'12345'},{period_days:90,currency:'RUB',amount_minor:'33345'},{period_days:30,currency:'USD',amount_minor:'200'},{period_days:30,currency:'XTR',amount_minor:'0'}]};
const order:Model<'PurchaseOrder'>={order_id:orderId,action:'change_plan',quote:{plan_id:planId,revision:3,devices:4,period_days:30,traffic_gb:120,profile:'euru',amount_minor:'12345',currency:'RUB',source_access_operation_id:sourceId},payment_method:'yoomoney',payment_type:'AC',payment_status:'pending',fulfillment_status:'not_started',created_at:'2026-10-03T10:00:00Z',expires_at:'2026-10-03T10:30:00Z',expired:false,can_pay:true,can_cancel:true,review_required:false,access_operation_id:null,checkout:{action:'https://yoomoney.ru/quickpay/confirm',method:'POST',fields:{receiver:'410011111111111','quickpay-form':'button',paymentType:'AC',sum:'123.45',label:orderId,successURL:'http://127.0.0.1:4173/orders/'+orderId}}};
const history:Model<'PurchaseOrder'>={...order,order_id:'80000000-0000-4000-8000-000000000002',action:'purchase',payment_status:'paid',fulfillment_status:'applied',can_pay:false,can_cancel:false,checkout:null,quote:{...order.quote,source_access_operation_id:undefined}};
test.beforeEach(async({page})=>{await page.clock.install({time:new Date('2026-10-03T10:05:00Z')});});

async function routes(page:Page,extra?:(route:Route,path:string)=>Promise<boolean>){
 await page.route('https://yoomoney.ru/quickpay/confirm',route=>route.abort());
 await page.route('**/api/v1/**',async route=>{const path=new URL(route.request().url()).pathname;if(extra&&await extra(route,path))return;
  if(path.endsWith('/auth/session'))return route.fulfill({json:{csrf_token:'s'.repeat(43)}});
  if(path.endsWith('/subscription/plan-change'))return route.fulfill({json:context});
  if(path.endsWith('/subscription/renewal'))return route.fulfill({json:plan});
  if(path.endsWith('/catalogue'))return route.fulfill({json:{plans:[plan]}});
  if(path.endsWith('/payment-methods'))return route.fulfill({json:{methods:[{id:'manual',currency:'RUB'},{id:'yoomoney',currency:'RUB'},{id:'yookassa',currency:'RUB'},{id:'cryptomus',currency:'USD'},{id:'heleket',currency:'USD'}]}});
  if(path.endsWith('/orders/current'))return route.fulfill({json:{order:history}});
  if(path.endsWith('/orders/'+orderId))return route.fulfill({json:order});
  if(path.endsWith('/support'))return route.fulfill({json:{conversation:null,messages:[],has_more:false,oldest_sequence:null}});
  return route.fulfill({json:{}});
 });
}

test('plan change warns and freezes source through a lost response',async({page})=>{
 let contextReads=0,catalogueReads=0;const calls:{body:Model<'PurchaseOrderInput'>;key:string;csrf:string}[]=[];
 const changed:Model<'PurchaseOrder'>={...order,payment_type:'PC',quote:{...order.quote,period_days:90,amount_minor:'33345'},checkout:{...order.checkout!,fields:{...order.checkout!.fields,paymentType:'PC',sum:'333.45'}}};
 await routes(page,async(route,path)=>{
  if(path.endsWith('/subscription/plan-change')){contextReads++;await route.fulfill({json:contextReads===1?context:{...context,source_access_operation_id:'90000000-0000-4000-8000-000000000009'}});return true;}
  if(path.endsWith('/catalogue')){catalogueReads++;await route.fulfill({json:{plans:[catalogueReads===1?plan:{...plan,revision:4,periods:[30],prices:[{period_days:30,currency:'RUB',amount_minor:'99999'}]}]}});return true;}
  if(path.endsWith('/orders')&&route.request().method()==='POST'){calls.push({body:route.request().postDataJSON(),key:route.request().headers()['idempotency-key'],csrf:route.request().headers()['x-csrf-token']});if(calls.length===1)await route.abort();else await route.fulfill({status:201,json:changed});return true;}
  if(path.endsWith('/orders/'+orderId)){await route.fulfill({json:changed});return true;}return false;
 });
 await page.setViewportSize({width:375,height:812});await page.goto('/cabinet/change-plan?lang=en');
 await expect(page.getByRole('heading',{name:'Change plan',exact:true})).toBeVisible();
 await expect(page.getByText('Remaining days are discarded. The new period starts when access is prepared.',{exact:true})).toBeVisible();
 await page.getByRole('button',{name:'Select plan',exact:true}).click();
 await page.getByRole('combobox',{name:'Period',exact:true}).selectOption('90');await page.getByRole('radio',{name:'Wallet',exact:true}).check();
 await page.getByRole('button',{name:'Change plan',exact:true}).click();expect(calls).toHaveLength(0);
 await expect(page.getByText('Remaining days are discarded. The new period starts when access is prepared.',{exact:true})).toBeVisible();
 await page.getByRole('button',{name:'Confirm plan change',exact:true}).click();await expect(page.getByRole('alert')).toBeVisible();
 await page.getByRole('button',{name:'RU',exact:true}).click();await expect(page.getByRole('heading',{name:'Сменить тариф',exact:true})).toBeVisible();
 await expect(page.getByRole('combobox',{name:'Период',exact:true})).toHaveValue('90');await expect(page.getByRole('radio',{name:'Кошелёк YooMoney',exact:true})).toBeChecked();
 await expect(page.getByText('Цена: 333,45 RUB',{exact:true})).toBeVisible();
 await page.getByRole('button',{name:'Подтвердить смену тарифа',exact:true}).click();await expect(page).toHaveURL(/\/orders\/80000000/);
 expect(contextReads).toBe(1);expect(catalogueReads).toBe(1);expect(calls).toHaveLength(2);expect(calls[0]).toEqual(calls[1]);expect(calls[0].key).toMatch(/^[0-9a-f-]{36}$/i);expect(calls[0].csrf).toBe('s'.repeat(43));
 expect(calls[0].body).toEqual({action:'change_plan',plan_id:planId,revision:3,period_days:90,payment_method:'yoomoney',payment_type:'PC',source_access_operation_id:sourceId});
 await expect(page.getByText('Смена тарифа',{exact:true})).toBeVisible();await expect(page.getByText('333,45 RUB',{exact:true})).toBeVisible();
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});

function methodOrder(method:Model<'PurchaseOrder'>['payment_method']):Model<'PurchaseOrder'>{
 const payment_type={yoomoney:'AC',manual:'MANUAL',yookassa:'YOOKASSA',cryptomus:'CRYPTOMUS',heleket:'HELEKET'} as const;
 const crypto=method==='cryptomus'||method==='heleket';
 return {...order,payment_method:method,payment_type:payment_type[method],quote:{...order.quote,currency:crypto?'USD':'RUB',amount_minor:crypto?'200':'12345'},checkout:method==='yoomoney'?order.checkout:null,
  manual_payment:method==='manual'?{state:'not_reported',instructions:'Synthetic transfer details',can_report:true,reported_at:null,decided_at:null,reason:null}:null,
  yookassa_checkout:method==='yookassa'?{state:'ready',url:'https://yoomoney.ru/checkout/payments/v2/contract?orderId='+orderId}:null,
  cryptomus_checkout:method==='cryptomus'?{state:'ready',url:'https://pay.cryptomus.com/pay/'+orderId}:null,
  heleket_checkout:method==='heleket'?{state:'ready',url:'https://pay.heleket.com/pay/'+orderId}:null};
}

for(const method of ['yoomoney','manual','yookassa'] as const)for(const state of ['401','403','404','foreign','purpose','source'] as const)
 test('fresh '+state+' closes '+method+' plan change checkout',async({page})=>{
  const initial=methodOrder(method);let reads=0,external=0,reports=0;
  await routes(page,async(route,path)=>{
   if(path.endsWith('/manual-report')){reports++;await route.fulfill({json:{...initial,manual_payment:{...initial.manual_payment!,can_report:false,state:'pending'}}});return true;}
   if(path.endsWith('/orders/'+orderId)){reads++;if(reads===1)await route.fulfill({json:initial});else if(/^\d+$/.test(state))await route.fulfill({status:Number(state),json:{error:{code:'NOT_FOUND'}}});else await route.fulfill({json:state==='foreign'?{...initial,order_id:'80000000-0000-4000-8000-000000000009'}:state==='purpose'?{...initial,action:'purchase'}:{...initial,quote:{...initial.quote,source_access_operation_id:'90000000-0000-4000-8000-000000000009'}}});return true;}
   return false;
  });
  await page.route('https://yoomoney.ru/**',route=>{external++;return route.abort();});await page.goto('/orders/'+orderId+'?lang=en');
  const pay=page.getByRole('button',{name:method==='manual'?'I have paid':'Continue to payment',exact:true});await expect(pay).toBeVisible();await pay.click({noWaitAfter:true});
  await expect.poll(()=>reads).toBe(2);if(state==='401')await expect(page).toHaveURL(/\/login\?lang=en/);else await expect(pay).toHaveCount(0);
  await expect(page.locator('form[action="https://yoomoney.ru/quickpay/confirm"]')).toHaveCount(0);await expect(page.getByText('80000000-0000-4000-8000-000000000009',{exact:true})).toHaveCount(0);
  expect(external).toBe(0);expect(reports).toBe(0);
 });

const account:Model<'AccountResult'>={account:{account_id:'20000000-0000-4000-8000-000000000001',email:'client@example.test',email_verified:true,locale:'en',telegram_linked:false},csrf_token:'s'.repeat(43),capabilities:{trial_available:false}};
const subscription:Model<'Subscription'>={status:'active',devices:2,traffic_limit_bytes:1024,traffic_used_bytes:0,observed_at:'2026-10-03T10:00:00Z',data_stale:false,expires_at:'2026-10-05T10:00:00Z',connection_available:false,access_profile:'regular',vpn_banned:false,access_operation_id:null,access_operation_status:null};

test('operator sees plan change purpose, USD amount and the current access operation',async({page})=>{
 const clientId=account.account.account_id;const client:Model<'OperatorClient'>={account_id:clientId,kind:'web',display_name:'',email:'client@example.test',telegram_id:null,locale:'en',created_at:null,restricted:false,vpn_banned:false,had_subscription:true};
 const card:Model<'OperatorClientCard'>={client,subscription,server:null,support:null,trial_requests:[],trial_has_more:false,audit_events:[],audit_has_more:false,legacy_approval:null,legacy_events:[],legacy_has_more:false};
 const current:Model<'PurchaseOrder'>={...methodOrder('cryptomus'),payment_status:'paid',fulfillment_status:'queued',can_pay:false,can_cancel:false,cryptomus_checkout:null,access_operation_id:'90000000-0000-4000-8000-000000000002'};
 const operation:Model<'AccessOperation'>={operation_id:current.access_operation_id!,account_id:clientId,kind:'purchase',status:'pending',created_at:order.created_at,updated_at:order.created_at,reason:'',operator_account_id:null,desired:{expires_at:'2026-11-02T10:00:00Z',devices:4,traffic_limit_bytes:120*1024**3,profile:'euru',plan_id:planId,revision:3,period_days:30,reset_traffic:true,vpn_banned:false},completed_steps:[],review_reason:null};
 await routes(page,async(route,path)=>{if(path.endsWith('/operator/session')){await route.fulfill({json:{account:account.account,csrf_token:'s'.repeat(43)}});return true;}if(path.endsWith('/operator/clients/'+clientId)){await route.fulfill({json:card});return true;}if(path.endsWith('/operator/clients/'+clientId+'/orders/current')){await route.fulfill({json:{order:current}});return true;}if(path.endsWith('/access-operations/'+current.access_operation_id)){await route.fulfill({json:operation});return true;}return false;});
 await page.goto('/admin/clients/'+clientId+'/show?lang=en');const section=page.getByRole('region',{name:'Plan change',exact:true});await expect(section.getByText('Order amount: 2.00 USD',{exact:true})).toBeVisible();await expect(section.getByText('Payment received. Preparing access.')).toBeVisible();await expect(section.getByRole('link',{name:'Access operations: '+current.access_operation_id})).toBeVisible();await expect(section.getByRole('button',{name:'Retry preparation'})).toHaveCount(0);
});

for(const action of ['change_plan','renew'] as const)for(const [method,label] of [['yoomoney','Bank card'],['manual','Manual transfer'],['yookassa','YooKassa'],['cryptomus','Cryptomus'],['heleket','Heleket']] as const)
 test(action+' creates an exact '+method+' order',async({page})=>{
  const current:Model<'PurchaseOrder'>={...methodOrder(method),action,quote:{...methodOrder(method).quote,source_access_operation_id:action==='change_plan'?sourceId:undefined}};const calls:Model<'PurchaseOrderInput'>[]=[];
  await routes(page,async(route,path)=>{if(path.endsWith('/orders')&&route.request().method()==='POST'){calls.push(route.request().postDataJSON());await route.fulfill({status:201,json:current});return true;}if(path.endsWith('/orders/'+orderId)){await route.fulfill({json:current});return true;}return false;});
  await page.setViewportSize({width:375,height:812});await page.goto('/cabinet/'+(action==='renew'?'renew':'change-plan')+'?lang=en');if(action==='change_plan')await page.getByRole('button',{name:'Select plan',exact:true}).click();
  await page.getByRole('radio',{name:label,exact:true}).check();await expect(page.getByRole('combobox',{name:'Currency',exact:true})).toHaveValue(current.quote.currency);await expect(page.getByText('Price: '+(current.quote.currency==='USD'?'2.00 USD':'123.45 RUB'),{exact:true})).toBeVisible();
  await page.getByRole('button',{name:action==='renew'?'Renew subscription':'Change plan',exact:true}).click();await page.getByRole('button',{name:action==='renew'?'Confirm renewal':'Confirm plan change',exact:true}).click();await expect(page).toHaveURL(/\/orders\//);
  expect(calls).toEqual([{action,plan_id:planId,revision:3,period_days:30,payment_method:method,payment_type:current.payment_type,...(action==='change_plan'?{source_access_operation_id:sourceId}:{})}]);await expect(page.getByText(action==='renew'?'Subscription renewal':'Plan change',{exact:true})).toBeVisible();expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 });

for(const lang of ['ru','en'] as const)test('mobile cabinet change link and keyboard controls '+lang,async({page})=>{
 await routes(page,async(route,path)=>{if(path.endsWith('/me')){await route.fulfill({json:account});return true;}if(path.endsWith('/subscription')){await route.fulfill({json:subscription});return true;}if(path.endsWith('/trial-requests/current')){await route.fulfill({json:{request:null}});return true;}return false;});
 const title=lang==='ru'?'Сменить тариф':'Change plan';await page.setViewportSize({width:375,height:812});await page.goto('/cabinet'+(lang==='en'?'?lang=en':''));const change=page.getByRole('link',{name:title,exact:true});await change.focus();await expect(change).toBeFocused();await page.keyboard.press('Enter');await expect(page.getByRole('heading',{name:title,exact:true})).toBeVisible();
 const select=page.getByRole('button',{name:lang==='ru'?'Выбрать тариф':'Select plan',exact:true});await select.focus();await page.keyboard.press('Enter');const period=page.getByRole('combobox',{name:lang==='ru'?'Период':'Period',exact:true});await period.focus();await page.keyboard.press('9');await expect(period).toHaveValue('90');
 const wallet=page.getByRole('radio',{name:lang==='ru'?'Кошелёк YooMoney':'Wallet',exact:true});await wallet.focus();await page.keyboard.press('Space');await expect(wallet).toBeChecked();await page.getByRole('button',{name:title,exact:true}).focus();await page.keyboard.press('Enter');await expect(page.getByRole('button',{name:lang==='ru'?'Подтвердить смену тарифа':'Confirm plan change',exact:true})).toBeVisible();await expect(page.getByText(lang==='ru'?'Оставшиеся дни старого тарифа пропадут. Новый срок начнётся при подготовке доступа.':'Remaining days are discarded. The new period starts when access is prepared.',{exact:true})).toBeVisible();expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});

for(const [code,status,message] of [['PLAN_CHANGE_NOT_ELIGIBLE',409,'Plan change is currently unavailable.'],['EXTERNAL_BILLING_UNVERIFIED',409,'Automatic payments could not be verified.'],['ACCOUNT_RESTRICTED',403,''],['SERVICE_UNAVAILABLE',503,'']] as const)
 test('change context '+code+' hides selection and retries without an order',async({page})=>{
  let failed=true,writes=0;await routes(page,async(route,path)=>{if(path.endsWith('/subscription/plan-change')&&failed){await route.fulfill({status,json:{error:{code}}});return true;}if(path.endsWith('/orders')&&route.request().method()==='POST'){writes++;await route.abort();return true;}return false;});await page.goto('/cabinet/change-plan?lang=en');await expect(page.getByRole('alert')).toBeVisible();if(message)await expect(page.getByRole('alert')).toContainText(message);await expect(page.getByRole('button',{name:'Select plan',exact:true})).toHaveCount(0);await expect(page.getByRole('link',{name:'Current order'})).toBeVisible();failed=false;await page.getByRole('button',{name:'Retry',exact:true}).click();await expect(page.getByRole('button',{name:'Select plan',exact:true})).toBeVisible();expect(writes).toBe(0);
 });

test('anonymous change context redirects to login without selecting a plan',async({page})=>{
 await routes(page,async(route,path)=>{if(path.endsWith('/subscription/plan-change')){await route.fulfill({status:401,json:{error:{code:'INVALID_CREDENTIALS'}}});return true;}return false;});await page.goto('/cabinet/change-plan?lang=en');await expect(page).toHaveURL(/\/login\?lang=en/);await expect(page.getByRole('button',{name:'Select plan',exact:true})).toHaveCount(0);
});

test('the same visible plan is selectable and hidden targets stay absent',async({page})=>{
 await routes(page,async(route,path)=>{if(path.endsWith('/subscription/plan-change')){await route.fulfill({json:{...context,current_plan_id:planId}});return true;}if(path.endsWith('/catalogue')){await route.fulfill({json:{plans:[plan,{...plan,plan_id:'70000000-0000-4000-8000-000000000009',devices:9,hidden:true}]}});return true;}return false;});await page.goto('/cabinet/change-plan?lang=en');await expect(page.getByRole('button',{name:'Select plan',exact:true})).toHaveCount(1);await page.getByRole('button',{name:'Select plan',exact:true}).click();await expect(page.getByRole('button',{name:'Change plan',exact:true})).toBeEnabled();await expect(page.getByRole('heading',{name:'9 devices',exact:true})).toHaveCount(0);
});

for(const plans of [[],[{...plan,hidden:true}]])test('empty visible change catalogue '+plans.length+' cannot confirm',async({page})=>{
 await routes(page,async(route,path)=>{if(path.endsWith('/catalogue')){await route.fulfill({json:{plans}});return true;}return false;});await page.goto('/cabinet/change-plan?lang=en');await expect(page.getByText('No plans yet.',{exact:true})).toBeVisible();await expect(page.getByRole('button',{name:'Select plan',exact:true})).toHaveCount(0);await expect(page.getByRole('button',{name:'Confirm plan change',exact:true})).toHaveCount(0);
});

for(const state of ['zero','disabled'] as const)test(state+' payment cannot create a change order',async({page})=>{
 let writes=0;await routes(page,async(route,path)=>{if(state==='zero'&&path.endsWith('/catalogue')){await route.fulfill({json:{plans:[{...plan,prices:plan.prices.map(price=>({...price,amount_minor:'0'}))}]}});return true;}if(state==='disabled'&&path.endsWith('/payment-methods')){await route.fulfill({json:{methods:[]}});return true;}if(path.endsWith('/orders')&&route.request().method()==='POST'){writes++;await route.abort();return true;}return false;});await page.goto('/cabinet/change-plan?lang=en');await page.getByRole('button',{name:'Select plan',exact:true}).click();if(state==='zero')await expect(page.getByRole('button',{name:'Change plan',exact:true})).toBeDisabled();else{await expect(page.getByRole('radio')).toHaveCount(0);await expect(page.getByRole('button',{name:'Change plan',exact:true})).toHaveCount(0);}expect(writes).toBe(0);
});

for(const previous of [order,{...order,payment_status:'paid' as const,fulfillment_status:'needs_review' as const,review_required:true,can_pay:false,checkout:null}])test('current '+previous.payment_status+' blocks another plan change',async({page})=>{
 await routes(page,async(route,path)=>{if(path.endsWith('/orders/current')){await route.fulfill({json:{order:previous}});return true;}return false;});await page.goto('/cabinet/change-plan?lang=en');await page.getByRole('button',{name:'Select plan',exact:true}).click();await expect(page.getByRole('link',{name:'Current order'})).toHaveAttribute('href','/orders/'+orderId+'?lang=en');await expect(page.getByRole('button',{name:'Change plan',exact:true})).toHaveCount(0);
});

for(const code of ['CATALOGUE_REVISION_CONFLICT','PURCHASE_PLAN_CONFLICT','PLAN_CHANGE_NOT_ELIGIBLE'] as const)test('explicit reload after '+code+' changes source, revision and key',async({page})=>{
 const calls:{body:Model<'PurchaseOrderInput'>;key:string}[]=[];let changed=false;const nextSource='90000000-0000-4000-8000-000000000009';
 await routes(page,async(route,path)=>{if(path.endsWith('/subscription/plan-change')&&changed){await route.fulfill({json:{...context,source_access_operation_id:nextSource}});return true;}if(path.endsWith('/catalogue')&&changed){await route.fulfill({json:{plans:[{...plan,revision:4}]}});return true;}if(path.endsWith('/orders')&&route.request().method()==='POST'){calls.push({body:route.request().postDataJSON(),key:route.request().headers()['idempotency-key']});changed=true;if(calls.length===1)await route.fulfill({status:409,json:{error:{code}}});else await route.fulfill({status:201,json:{...order,quote:{...order.quote,revision:4,source_access_operation_id:nextSource}}});return true;}return false;});
 await page.goto('/cabinet/change-plan?lang=en');await page.getByRole('button',{name:'Select plan',exact:true}).click();await page.getByRole('button',{name:'Change plan',exact:true}).click();await page.getByRole('button',{name:'Confirm plan change',exact:true}).click();await expect(page.getByRole('alert')).toBeVisible();await page.getByRole('button',{name:code==='PLAN_CHANGE_NOT_ELIGIBLE'?'Retry':'Reload plans',exact:true}).click();await page.getByRole('button',{name:'Select plan',exact:true}).click();await page.getByRole('button',{name:'Change plan',exact:true}).click();await page.getByRole('button',{name:'Confirm plan change',exact:true}).click();await expect(page).toHaveURL(/\/orders\//);
 expect(calls).toHaveLength(2);expect(calls[0].key).not.toBe(calls[1].key);expect(calls[0].body.revision).toBe(3);expect(calls[1].body.revision).toBe(4);expect(calls[0].body.source_access_operation_id).toBe(sourceId);expect(calls[1].body.source_access_operation_id).toBe(nextSource);
});
