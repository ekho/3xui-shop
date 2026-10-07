import {test,expect,type Page,type Route} from '@playwright/test';
import type {components} from '../src/api/schema.gen';

type Model<K extends keyof components['schemas']>=components['schemas'][K];
const planId='70000000-0000-4000-8000-000000000001';
const orderId='80000000-0000-4000-8000-000000000001';
const plan:Model<'CataloguePlanSnapshot'>={plan_id:planId,revision:3,devices:2,traffic_gb:20,profile:'regular',hidden:true,periods:[30,90],prices:[{period_days:30,currency:'RUB',amount_minor:'12345'},{period_days:90,currency:'RUB',amount_minor:'33345'},{period_days:30,currency:'USD',amount_minor:'200'},{period_days:30,currency:'XTR',amount_minor:'0'}]};
const order:Model<'PurchaseOrder'>={order_id:orderId,action:'renew',quote:{plan_id:planId,revision:3,devices:2,period_days:30,traffic_gb:20,profile:'regular',amount_minor:'12345',currency:'RUB'},payment_method:'yoomoney',payment_type:'AC',payment_status:'pending',fulfillment_status:'not_started',created_at:'2026-10-03T10:00:00Z',expires_at:'2026-10-03T10:30:00Z',expired:false,can_pay:true,can_cancel:true,review_required:false,access_operation_id:null,checkout:{action:'https://yoomoney.ru/quickpay/confirm',method:'POST',fields:{receiver:'410011111111111','quickpay-form':'button',paymentType:'AC',sum:'123.45',label:orderId,successURL:'http://127.0.0.1:4173/orders/'+orderId}}};
const history:Model<'PurchaseOrder'>={...order,order_id:'80000000-0000-4000-8000-000000000002',action:'purchase',payment_status:'paid',fulfillment_status:'applied',can_pay:false,can_cancel:false,checkout:null};
test.beforeEach(async({page})=>{await page.clock.install({time:new Date('2026-10-03T10:05:00Z')});});

async function routes(page:Page,extra?:(route:Route,path:string)=>Promise<boolean>){
 await page.route('https://yoomoney.ru/quickpay/confirm',route=>route.abort());
 await page.route('**/api/v1/**',async route=>{const path=new URL(route.request().url()).pathname;if(extra&&await extra(route,path))return;
  if(path.endsWith('/auth/session'))return route.fulfill({json:{csrf_token:'s'.repeat(43)}});
  if(path.endsWith('/subscription/renewal'))return route.fulfill({json:plan});
  if(path.endsWith('/payment-methods'))return route.fulfill({json:{methods:[{id:'manual',currency:'RUB'},{id:'yoomoney',currency:'RUB'},{id:'yookassa',currency:'RUB'},{id:'cryptomus',currency:'USD'},{id:'heleket',currency:'USD'}]}});
  if(path.endsWith('/orders/current'))return route.fulfill({json:{order:history}});
  if(path.endsWith('/orders/'+orderId))return route.fulfill({json:order});
  if(path.endsWith('/support'))return route.fulfill({json:{conversation:null,messages:[],has_more:false,oldest_sequence:null}});
  return route.fulfill({json:{}});
 });
}

test('renewal uses one own plan and a frozen retry',async({page})=>{
 const calls:{body:Model<'PurchaseOrderInput'>;key:string;csrf:string}[]=[];
 const renewed:Model<'PurchaseOrder'>={...order,payment_type:'PC',quote:{...order.quote,period_days:90,amount_minor:'33345'},checkout:{...order.checkout!,fields:{...order.checkout!.fields,paymentType:'PC',sum:'333.45'}}};
 await routes(page,async(route,path)=>{if(path.endsWith('/orders')&&route.request().method()==='POST'){calls.push({body:route.request().postDataJSON(),key:route.request().headers()['idempotency-key'],csrf:route.request().headers()['x-csrf-token']});if(calls.length===1)await route.abort();else await route.fulfill({status:201,json:renewed});return true;}if(path.endsWith('/orders/'+orderId)){await route.fulfill({json:renewed});return true;}return false;});
 await page.setViewportSize({width:375,height:812});await page.goto('/cabinet/renew?lang=en');
 await expect(page.getByRole('heading',{name:'Renew subscription'})).toBeVisible();
 await expect(page.getByRole('button',{name:'Select plan'})).toHaveCount(0);
 await expect(page.getByLabel('Currency')).toHaveValue('RUB');expect(await page.getByLabel('Currency').locator('option').count()).toBe(2);
 await page.getByRole('combobox',{name:'Period',exact:true}).selectOption('90');await page.getByLabel('Wallet', {exact:true}).check();
 await expect(page.getByLabel('Manual transfer', {exact:true})).toBeVisible();await expect(page.getByLabel('Cryptomus', {exact:true})).toBeVisible();
 await page.getByRole('button',{name:'Renew subscription',exact:true}).click();expect(calls).toHaveLength(0);
 await page.getByRole('button',{name:'Confirm renewal'}).click();await expect(page.getByRole('alert')).toBeVisible();
 await page.getByRole('button',{name:'Confirm renewal'}).click();await expect(page).toHaveURL(/\/orders\/80000000/);
 expect(calls).toHaveLength(2);expect(calls[0]).toEqual(calls[1]);expect(calls[0].key).toMatch(/^[0-9a-f-]{36}$/i);expect(calls[0].csrf).toBe('s'.repeat(43));
 expect(calls[0].body).toEqual({action:'renew',plan_id:planId,revision:3,period_days:90,payment_method:'yoomoney',payment_type:'PC'});
 await expect(page.getByText('Subscription renewal',{exact:true})).toBeVisible();await expect(page.getByText('333.45 RUB',{exact:true})).toBeVisible();
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});

const account:Model<'AccountResult'>={account:{account_id:'20000000-0000-4000-8000-000000000001',email:'client@example.test',email_verified:true,locale:'en',telegram_linked:false},csrf_token:'s'.repeat(43),capabilities:{trial_available:false}};
const subscription:Model<'Subscription'>={status:'active',devices:2,traffic_limit_bytes:1024,traffic_used_bytes:0,observed_at:'2026-10-03T10:00:00Z',data_stale:false,expires_at:'2026-10-05T10:00:00Z',connection_available:false,access_profile:'regular',vpn_banned:false,access_operation_id:null,access_operation_status:null};

for(const lang of ['ru','en'] as const)test('mobile cabinet renewal link and keyboard controls '+lang,async({page})=>{
 await routes(page,async(route,path)=>{if(path.endsWith('/me')){await route.fulfill({json:account});return true;}if(path.endsWith('/subscription')){await route.fulfill({json:subscription});return true;}if(path.endsWith('/trial-requests/current')){await route.fulfill({json:{request:null}});return true;}return false;});
 const title=lang==='ru'?'Продлить подписку':'Renew subscription';await page.setViewportSize({width:375,height:812});await page.goto('/cabinet'+(lang==='en'?'?lang=en':''));
 const renewal=page.getByRole('link',{name:title,exact:true});await renewal.focus();await expect(renewal).toBeFocused();await page.keyboard.press('Enter');
 await expect(page.getByRole('heading',{name:title,exact:true})).toBeVisible();expect(await page.evaluate(()=>document.documentElement.lang)).toBe(lang);
 const period=page.getByRole('combobox',{name:lang==='ru'?'Период':'Period',exact:true});await period.focus();await page.keyboard.press('9');await expect(period).toHaveValue('90');
 const wallet=page.getByRole('radio',{name:lang==='ru'?'Кошелёк YooMoney':'Wallet',exact:true});await wallet.focus();await page.keyboard.press('Space');await expect(wallet).toBeChecked();
 await page.getByRole('button',{name:title,exact:true}).focus();await page.keyboard.press('Enter');await expect(page.getByRole('button',{name:lang==='ru'?'Подтвердить продление':'Confirm renewal'})).toBeVisible();expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});

for(const [code,status,message] of [
 ['RENEWAL_NOT_ELIGIBLE',409,'Renewal is currently unavailable.'],
 ['EXTERNAL_BILLING_UNVERIFIED',409,'Automatic payments could not be verified.'],
 ['SERVICE_UNAVAILABLE',503,'Something went wrong.'],
] as const)test('unavailable offer '+code+' can be retried without creating an order',async({page})=>{
 let failed=true,writes=0;await routes(page,async(route,path)=>{if(path.endsWith('/subscription/renewal')&&failed){await route.fulfill({status,json:{error:{code}}});return true;}if(path.endsWith('/orders')&&route.request().method()==='POST'){writes++;await route.abort();return true;}return false;});
 await page.goto('/cabinet/renew?lang=en');const alert=page.getByRole('alert');await expect(alert).toBeVisible();if(code!=='SERVICE_UNAVAILABLE')await expect(alert).toContainText(message);await expect(page.getByRole('button',{name:'Renew subscription',exact:true})).toHaveCount(0);await expect(page.getByRole('link',{name:'Current order'})).toBeVisible();failed=false;await page.getByRole('button',{name:'Retry',exact:true}).click();await expect(page.getByRole('button',{name:'Renew subscription',exact:true})).toBeEnabled();expect(writes).toBe(0);
});

test('disabled YooMoney preserves enabled manual and USD renewal methods',async({page})=>{
 await routes(page,async(route,path)=>{if(path.endsWith('/payment-methods')){await route.fulfill({json:{methods:[{id:'manual',currency:'RUB'},{id:'cryptomus',currency:'USD'}]}});return true;}return false;});await page.goto('/cabinet/renew?lang=en');await expect(page.getByText('Price: 123.45 RUB')).toBeVisible();await expect(page.getByRole('radio',{name:'Bank card',exact:true})).toHaveCount(0);await expect(page.getByRole('radio',{name:'Manual transfer',exact:true})).toBeChecked();await page.getByRole('radio',{name:'Cryptomus',exact:true}).check();await expect(page.getByRole('combobox',{name:'Currency',exact:true})).toHaveValue('USD');await expect(page.getByText('Price: 2.00 USD',{exact:true})).toBeVisible();await expect(page.getByRole('button',{name:'Renew subscription',exact:true})).toBeEnabled();
});

for(const [method,label,paymentType] of [['cryptomus','Cryptomus','CRYPTOMUS'],['heleket','Heleket','HELEKET']] as const)
 for(const methodsFirst of [true,false])test('sole '+method+' renewal keeps USD when '+(methodsFirst?'methods resolve first':'offer resolves first'),async({page})=>{
 const calls:Model<'PurchaseOrderInput'>[]=[];let release!:()=>void;const gate=new Promise<void>(resolve=>{release=resolve;});
 const renewed:Model<'PurchaseOrder'>={...order,payment_method:method,payment_type:paymentType,quote:{...order.quote,currency:'USD',amount_minor:'200'},checkout:null};
 await routes(page,async(route,path)=>{
  if(path.endsWith('/subscription/renewal')){if(methodsFirst)await gate;await route.fulfill({json:plan});return true;}
  if(path.endsWith('/payment-methods')){if(!methodsFirst)await gate;await route.fulfill({json:{methods:[{id:method,currency:'USD'}]}});return true;}
  if(path.endsWith('/orders')&&route.request().method()==='POST'){calls.push(route.request().postDataJSON());await route.fulfill({status:201,json:renewed});return true;}
  if(path.endsWith('/orders/'+orderId)){await route.fulfill({json:renewed});return true;}return false;
 });
 try{
  await page.goto('/cabinet/renew?lang=en');
  if(methodsFirst)await expect(page.getByRole('link',{name:'Current order',exact:true})).toBeVisible();
  else await expect(page.getByText('Price: 123.45 RUB',{exact:true})).toBeVisible();
  release();await expect(page.getByRole('radio',{name:label,exact:true})).toBeChecked();
  await expect(page.getByRole('combobox',{name:'Currency',exact:true})).toHaveValue('USD');
  await expect(page.getByText('Price: 2.00 USD',{exact:true})).toBeVisible();
  await expect(page.getByRole('button',{name:'Renew subscription',exact:true})).toBeEnabled();
  await page.getByRole('button',{name:'Renew subscription',exact:true}).click();await page.getByRole('button',{name:'Confirm renewal'}).click();await expect(page).toHaveURL(/\/orders\/80000000/);
  expect(calls).toEqual([{action:'renew',plan_id:planId,revision:3,period_days:30,payment_method:method,payment_type:paymentType}]);
 }finally{release();}
});

for(const previous of [order,{...order,payment_status:'paid' as const,fulfillment_status:'needs_review' as const,review_required:true,can_pay:false,checkout:null}])
 test('current '+previous.payment_status+' renewal stays linked and blocks another order',async({page})=>{
 let writes=0;await routes(page,async(route,path)=>{if(path.endsWith('/orders/current')){await route.fulfill({json:{order:previous}});return true;}if(path.endsWith('/orders')&&route.request().method()==='POST'){writes++;await route.abort();return true;}return false;});await page.goto('/cabinet/renew?lang=en');await expect(page.getByRole('link',{name:'Current order'})).toHaveAttribute('href','/orders/'+orderId+'?lang=en');await expect(page.getByRole('button',{name:'Renew subscription',exact:true})).toHaveCount(0);expect(writes).toBe(0);
});

for(const archived of [false,true])test('stale quote reload '+(archived?'rejects archived plan':'uses new revision and key'),async({page})=>{
 const calls:{body:Model<'PurchaseOrderInput'>;key:string}[]=[];let changed=false;
 await routes(page,async(route,path)=>{if(path.endsWith('/subscription/renewal')&&changed){if(archived)await route.fulfill({status:409,json:{error:{code:'RENEWAL_NOT_ELIGIBLE'}}});else await route.fulfill({json:{...plan,revision:4}});return true;}if(path.endsWith('/orders')&&route.request().method()==='POST'){calls.push({body:route.request().postDataJSON(),key:route.request().headers()['idempotency-key']});changed=true;if(calls.length===1)await route.fulfill({status:409,json:{error:{code:'PURCHASE_PLAN_CONFLICT'}}});else await route.fulfill({status:201,json:order});return true;}return false;});
 await page.goto('/cabinet/renew?lang=en');await page.getByRole('button',{name:'Renew subscription',exact:true}).click();await page.getByRole('button',{name:'Confirm renewal'}).click();await expect(page.getByRole('alert')).toContainText('The plan changed.');await page.getByRole('button',{name:'Reload plans',exact:true}).click();
 if(archived){await expect(page.getByRole('alert')).toContainText('Renewal is currently unavailable.');await expect(page.getByRole('button',{name:'Confirm renewal'})).toHaveCount(0);expect(calls).toHaveLength(1);}else{await page.getByRole('button',{name:'Renew subscription',exact:true}).click();await page.getByRole('button',{name:'Confirm renewal'}).click();await expect(page).toHaveURL(/\/orders\//);expect(calls).toHaveLength(2);expect(calls[0].key).not.toBe(calls[1].key);expect(calls[0].body.revision).toBe(3);expect(calls[1].body.revision).toBe(4);}
});

test('locale change after a lost response preserves the shown terms, key and body',async({page})=>{
 let offerReads=0;const calls:{body:Model<'PurchaseOrderInput'>;key:string}[]=[];
 await routes(page,async(route,path)=>{if(path.endsWith('/subscription/renewal')){offerReads++;await route.fulfill({json:offerReads===1?plan:{...plan,revision:4,periods:[30],prices:[{period_days:30,currency:'RUB',amount_minor:'99999'}]}});return true;}if(path.endsWith('/orders')&&route.request().method()==='POST'){calls.push({body:route.request().postDataJSON(),key:route.request().headers()['idempotency-key']});if(calls.length===1)await route.abort();else await route.fulfill({status:201,json:order});return true;}return false;});
 await page.goto('/cabinet/renew?lang=en');await page.getByRole('combobox',{name:'Period',exact:true}).selectOption('90');await page.getByLabel('Wallet',{exact:true}).check();await page.getByRole('button',{name:'Renew subscription',exact:true}).click();await page.getByRole('button',{name:'Confirm renewal'}).click();await expect(page.getByRole('alert')).toBeVisible();
 await page.getByRole('button',{name:'RU',exact:true}).click();await expect(page.getByRole('heading',{name:'Продлить подписку'})).toBeVisible();await expect(page.getByRole('combobox',{name:'Период',exact:true})).toHaveValue('90');await expect(page.getByRole('radio',{name:'Кошелёк YooMoney',exact:true})).toBeChecked();await page.getByRole('button',{name:'Подтвердить продление'}).click();await expect(page).toHaveURL(/\/orders\//);expect(offerReads).toBe(1);expect(calls).toHaveLength(2);expect(calls[0]).toEqual(calls[1]);expect(calls[1].body).toEqual({action:'renew',plan_id:planId,revision:3,period_days:90,payment_method:'yoomoney',payment_type:'PC'});
});

for(const [name,latest] of [
 ['billing',{...order,can_pay:false,checkout:null}],
 ['plan',{...order,review_required:true,can_pay:false,checkout:null}],
 ['expired',{...order,expired:true,can_pay:false,checkout:null}],
] as const)test('fresh '+name+' check closes renewal checkout',async({page})=>{
 let reads=0,posts=0;await routes(page,async(route,path)=>{if(path.endsWith('/orders/'+orderId)){reads++;await route.fulfill({json:reads===1?order:latest});return true;}return false;});await page.route('https://yoomoney.ru/quickpay/confirm',route=>{posts++;return route.abort();});await page.goto('/orders/'+orderId+'?lang=en');await expect(page.getByText('Subscription renewal',{exact:true})).toBeVisible();await page.getByRole('button',{name:'Continue to payment'}).click({noWaitAfter:true});await expect.poll(()=>reads).toBe(2);await expect(page.locator('form[action="https://yoomoney.ru/quickpay/confirm"]')).toHaveCount(0);expect(posts).toBe(0);
});

for(const status of [401,404])test('fresh '+status+' cannot send a renewal payment',async({page})=>{
 let reads=0,posts=0;await routes(page,async(route,path)=>{if(path.endsWith('/orders/'+orderId)){reads++;if(reads===1)await route.fulfill({json:order});else await route.fulfill({status,json:{error:{code:status===401?'INVALID_CREDENTIALS':'NOT_FOUND'}}});return true;}return false;});await page.route('https://yoomoney.ru/quickpay/confirm',route=>{posts++;return route.abort();});await page.goto('/orders/'+orderId+'?lang=en');await page.getByRole('button',{name:'Continue to payment'}).click({noWaitAfter:true});await expect.poll(()=>reads).toBe(2);if(status===401)await expect(page).toHaveURL(/\/login\?lang=en/);else await expect(page.locator('form')).toHaveCount(0,{timeout:1000});expect(posts).toBe(0);
});

test('foreign fresh order is discarded before renewal payment',async({page})=>{
 let reads=0,posts=0;await routes(page,async(route,path)=>{if(path.endsWith('/orders/'+orderId)){reads++;await route.fulfill({json:reads===1?order:{...order,order_id:'80000000-0000-4000-8000-000000000009'}});return true;}return false;});await page.route('https://yoomoney.ru/quickpay/confirm',route=>{posts++;return route.abort();});await page.goto('/orders/'+orderId+'?lang=en');await page.getByRole('button',{name:'Continue to payment'}).click({noWaitAfter:true});await expect(page.locator('form')).toHaveCount(0);await expect(page.getByText('80000000-0000-4000-8000-000000000009',{exact:true})).toHaveCount(0);expect(posts).toBe(0);
});

test('a changed purpose in a fresh order cannot keep the payment form',async({page})=>{
 let reads=0,posts=0;await routes(page,async(route,path)=>{if(path.endsWith('/orders/'+orderId)){reads++;await route.fulfill({json:reads===1?order:{...order,action:'purchase'}});return true;}return false;});await page.route('https://yoomoney.ru/quickpay/confirm',route=>{posts++;return route.abort();});await page.goto('/orders/'+orderId+'?lang=en');await page.getByRole('button',{name:'Continue to payment'}).click({noWaitAfter:true});await expect(page.locator('form')).toHaveCount(0);expect(reads).toBe(2);expect(posts).toBe(0);
});

test('operator sees renewal purpose and the current preparation operation',async({page})=>{
 const clientId=account.account.account_id;const client:Model<'OperatorClient'>={account_id:clientId,kind:'web',display_name:'',email:'client@example.test',telegram_id:null,locale:'en',created_at:null,restricted:false,vpn_banned:false,had_subscription:true};
 const card:Model<'OperatorClientCard'>={client,subscription,server:null,support:null,trial_requests:[],trial_has_more:false,audit_events:[],audit_has_more:false,legacy_approval:null,legacy_events:[],legacy_has_more:false};const current:Model<'PurchaseOrder'>={...order,payment_status:'paid',fulfillment_status:'queued',can_pay:false,can_cancel:false,checkout:null,access_operation_id:'90000000-0000-4000-8000-000000000001'};
 const operation:Model<'AccessOperation'>={operation_id:current.access_operation_id!,account_id:clientId,kind:'purchase',status:'pending',created_at:order.created_at,updated_at:order.created_at,reason:'',operator_account_id:null,desired:{expires_at:'2026-11-02T10:00:00Z',devices:2,traffic_limit_bytes:20*1024**3,profile:'regular',plan_id:planId,revision:3,period_days:30,reset_traffic:true,vpn_banned:false},completed_steps:[],review_reason:null};
 await routes(page,async(route,path)=>{if(path.endsWith('/operator/session')){await route.fulfill({json:{account:account.account,csrf_token:'s'.repeat(43)}});return true;}if(path.endsWith('/operator/clients/'+clientId)){await route.fulfill({json:card});return true;}if(path.endsWith('/operator/clients/'+clientId+'/orders/current')){await route.fulfill({json:{order:current}});return true;}if(path.endsWith('/access-operations/'+current.access_operation_id)){await route.fulfill({json:operation});return true;}return false;});await page.goto('/admin/clients/'+clientId+'/show?lang=en');const section=page.getByRole('region',{name:'Subscription renewal'});await expect(section.getByText('Payment received. Preparing access.')).toBeVisible();await expect(section.getByRole('link',{name:'Access operations: '+current.access_operation_id})).toBeVisible();await expect(section.getByRole('button',{name:'Retry preparation'})).toHaveCount(0);
});
