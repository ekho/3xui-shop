import {test,expect,type Page,type Route} from '@playwright/test';
import type {components} from '../src/api/schema.gen';

type Model<K extends keyof components['schemas']>=components['schemas'][K];
const planId='70000000-0000-4000-8000-000000000001';
const orderId='80000000-0000-4000-8000-000000000001';
const checkoutURL='https://pay.cryptomus.com/checkout/payments/local-fixture';
const plan:Model<'CataloguePlanSnapshot'>={plan_id:planId,revision:3,devices:2,traffic_gb:20,profile:'regular',hidden:false,periods:[30],prices:[{period_days:30,currency:'RUB',amount_minor:'55555'},{period_days:30,currency:'USD',amount_minor:'12345'}]};
const order:Model<'PurchaseOrder'>={order_id:orderId,action:'purchase',quote:{plan_id:planId,revision:3,devices:2,period_days:30,traffic_gb:20,profile:'regular',amount_minor:'12345',currency:'USD'},payment_method:'cryptomus',payment_type:'CRYPTOMUS',payment_status:'pending',fulfillment_status:'not_started',created_at:'2026-10-03T10:00:00Z',expires_at:'2026-10-03T10:30:00Z',expired:false,can_pay:true,can_cancel:true,review_required:false,access_operation_id:null,checkout:null,cryptomus_checkout:{state:'ready',url:checkoutURL}};
test.beforeEach(async({page})=>{await page.clock.install({time:new Date('2026-10-03T10:05:00Z')});});
async function routes(page:Page,extra:(route:Route,path:string)=>Promise<boolean>){
 await page.route('**/api/v1/**',async route=>{
  const path=new URL(route.request().url()).pathname;if(await extra(route,path))return;
  if(path.endsWith('/auth/session'))return route.fulfill({json:{csrf_token:'s'.repeat(43)}});
  if(path.endsWith('/support'))return route.fulfill({json:{conversation:null,messages:[],has_more:false,oldest_sequence:null}});
  if(path.endsWith('/payment-methods'))return route.fulfill({json:{methods:[{id:'cryptomus',currency:'USD'}]}});
  if(path.endsWith('/catalogue'))return route.fulfill({json:{plans:[plan]}});
  if(path.endsWith('/orders/current'))return route.fulfill({json:{order:null}});
  if(path.endsWith('/orders/'+orderId))return route.fulfill({json:order});
  return route.fulfill({json:{}});
 });
}
for(const lang of ['en','ru'] as const)test(`keyboard ${lang}: preparing then verified payment navigation`,async({page})=>{
 const words=lang==='en'?{select:'Select plan',buy:'Buy plan',confirm:'Confirm purchase',preparing:'Preparing payment. Please wait.',pay:'Continue to payment'}:{select:'Выбрать тариф',buy:'Купить тариф',confirm:'Подтвердить покупку',preparing:'Подготавливаем оплату. Подождите.',pay:'Перейти к оплате'};
 const preparing={...order,can_pay:false,cryptomus_checkout:{state:'preparing',url:null}};
 let reads=0,navigations=0;const calls:{body:Model<'PurchaseOrderInput'>;key:string}[]=[];
 await page.route('https://pay.cryptomus.com/**',route=>{navigations++;return route.abort();});
 await routes(page,async(route,path)=>{
  if(path.endsWith('/orders')&&route.request().method()==='POST'){calls.push({body:route.request().postDataJSON(),key:route.request().headers()['idempotency-key']});await route.fulfill({status:201,json:preparing});return true;}
  if(path.endsWith('/orders/'+orderId)){reads++;await route.fulfill({json:reads===1?preparing:order});return true;}return false;
 });
 await page.setViewportSize({width:375,height:812});await page.goto('/catalogue?lang='+lang);
 await page.getByRole('button',{name:words.select}).focus();await page.keyboard.press('Enter');
 const choice=page.getByRole('radio',{name:'Cryptomus'});await choice.focus();await page.keyboard.press('Space');await expect(choice).toBeChecked();
 await expect(page.getByLabel(lang==='en'?'Currency':'Валюта')).toHaveValue('USD');await expect(page.getByText(lang==='en'?'123.45 USD':'123,45 USD')).toBeVisible();
 await page.getByRole('button',{name:words.buy}).focus();await page.keyboard.press('Enter');await page.getByRole('button',{name:words.confirm}).focus();await page.keyboard.press('Enter');
 await expect(page).toHaveURL(new RegExp('/orders/'+orderId));expect(calls[0].body).toEqual({action:'purchase',plan_id:planId,revision:3,period_days:30,payment_method:'cryptomus',payment_type:'CRYPTOMUS'});expect(calls[0].key).toMatch(/^[0-9a-f-]{36}$/i);
 await expect(page.getByText(words.preparing,{exact:true})).toBeVisible();await expect(page.getByRole('button',{name:words.pay})).toHaveCount(0);
 await page.clock.runFor(5000);const pay=page.getByRole('button',{name:words.pay});await expect(pay).toBeVisible();expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await pay.focus();await page.keyboard.press('Enter');await expect.poll(()=>navigations).toBe(1);expect(reads).toBe(3);
});
test('lost Cryptomus creation response preserves body and key',async({page})=>{
 const calls:{body:unknown;key:string}[]=[];await routes(page,async(route,path)=>{if(path.endsWith('/orders')&&route.request().method()==='POST'){calls.push({body:route.request().postDataJSON(),key:route.request().headers()['idempotency-key']});if(calls.length===1)await route.abort();else await route.fulfill({status:201,json:order});return true;}return false;});
 await page.goto('/catalogue?lang=en');await page.getByRole('button',{name:'Select plan'}).click();await page.getByRole('button',{name:'Buy plan'}).click();await page.getByRole('button',{name:'Confirm purchase'}).click();await expect(page.getByRole('alert')).toBeVisible();await page.getByRole('button',{name:'Confirm purchase'}).click();await expect.poll(()=>calls.length).toBe(2);expect(calls[1]).toEqual(calls[0]);
});
for(const url of ['http://pay.cryptomus.com/pay','javascript:alert(1)','https://attacker.example.test/pay','https://pay.cryptomus.com.attacker.example.test/pay','https://user@pay.cryptomus.com/pay','https://pay.cryptomus.com:444/pay'])test(`unsafe checkout ${url} is hidden`,async({page})=>{
 await routes(page,async(route,path)=>{if(path.endsWith('/orders/'+orderId)){await route.fulfill({json:{...order,cryptomus_checkout:{state:'ready',url}}});return true;}return false;});await page.goto('/orders/'+orderId+'?lang=en');await expect(page.getByRole('heading',{name:'Order',exact:true})).toBeVisible();await expect(page.getByRole('button',{name:'Continue to payment'})).toHaveCount(0);await expect(page.getByRole('alert')).toContainText('Purchase is unavailable');
});
for(const [name,latest] of [
 ['expired',{...order,expired:true,can_pay:false}],
 ['disabled',{...order,can_pay:false}],
 ['review',{...order,review_required:true}],
 ['foreign-id',{...order,order_id:'80000000-0000-4000-8000-000000000002'}],
 ['changed-quote',{...order,quote:{...order.quote,amount_minor:'12346'}}],
 ['changed-currency',{...order,quote:{...order.quote,currency:'RUB'}}],
 ['changed-method',{...order,payment_method:'yookassa',payment_type:'YOOKASSA',yookassa_checkout:{state:'ready',url:'https://yoomoney.ru/pay'}}],
 ['unsafe-url',{...order,cryptomus_checkout:{state:'ready',url:'https://attacker.example.test/pay'}}],
] as const)test(`fresh ${name} prevents payment navigation`,async({page})=>{
 let reads=0,navigations=0;await page.route('https://pay.cryptomus.com/**',route=>{navigations++;return route.abort();});await routes(page,async(route,path)=>{if(path.endsWith('/orders/'+orderId)){reads++;await route.fulfill({json:reads===1?order:latest});return true;}return false;});await page.goto('/orders/'+orderId+'?lang=en');await page.getByRole('button',{name:'Continue to payment'}).click();await expect.poll(()=>reads).toBe(2);await expect(page.getByRole('alert').filter({has:page.getByRole('button',{name:'Retry',exact:true})})).toBeVisible();expect(navigations).toBe(0);
});
test('revoked ownership before click hides payment and does not navigate',async({page})=>{
 let reads=0,navigations=0;await page.route('https://pay.cryptomus.com/**',route=>{navigations++;return route.abort();});await routes(page,async(route,path)=>{if(path.endsWith('/orders/'+orderId)){reads++;if(reads===1)await route.fulfill({json:order});else await route.fulfill({status:403,json:{error:{code:'ACCOUNT_RESTRICTED'}}});return true;}return false;});await page.goto('/orders/'+orderId+'?lang=en');await page.getByRole('button',{name:'Continue to payment'}).click();await expect(page.getByRole('alert')).toBeVisible();await expect(page.getByRole('button',{name:'Continue to payment'})).toHaveCount(0);expect(navigations).toBe(0);
});
test('provider return cannot confirm money; GET failure, retry and review remain visible',async({page})=>{
 let reads=0;await routes(page,async(route,path)=>{if(path.endsWith('/orders/'+orderId)){reads++;if(reads===1)await route.fulfill({status:503,json:{error:{code:'SERVICE_UNAVAILABLE'}}});else await route.fulfill({json:reads===2?order:{...order,can_pay:false,review_required:true,cryptomus_checkout:{state:'unavailable',url:null}}});return true;}return false;});
 await page.goto('/orders/'+orderId+'?lang=en&payment_status=succeeded');await expect(page.getByRole('alert')).toBeVisible();await page.getByRole('button',{name:'Retry',exact:true}).click();await expect(page.getByRole('status')).toContainText('Waiting for payment confirmation.');await page.getByRole('button',{name:'Refresh status'}).click();await expect(page.getByRole('status')).toContainText('Payment needs support review.');await expect(page.getByRole('button',{name:'Continue to payment'})).toHaveCount(0);
});

test('currency and provider selection stay aligned when switching back to RUB',async({page})=>{
 await routes(page,async(route,path)=>{if(path.endsWith('/payment-methods')){await route.fulfill({json:{methods:[{id:'yoomoney',currency:'RUB'},{id:'cryptomus',currency:'USD'}]}});return true;}return false;});
 await page.goto('/catalogue?lang=en');await page.getByRole('button',{name:'Select plan'}).click();
 await page.getByRole('radio',{name:'Cryptomus'}).check();await expect(page.getByLabel('Currency')).toHaveValue('USD');await page.getByRole('button',{name:'Buy plan'}).click();
 await expect(page.getByText(/Review the terms.*123\.45 USD/)).toBeVisible();await page.getByRole('button',{name:'Back to plans'}).click();
 await page.getByRole('radio',{name:'Bank card'}).check();await expect(page.getByLabel('Currency')).toHaveValue('RUB');await expect(page.getByText('555.55 RUB')).toBeVisible();
 await page.getByLabel('Currency').selectOption('USD');await expect(page.getByRole('radio',{name:'Cryptomus'})).toBeChecked();
 await page.getByLabel('Currency').selectOption('XTR');await expect(page.getByRole('button',{name:'Buy plan'})).toBeDisabled();
});

for(const [name,method,type] of [['Manual transfer','manual','MANUAL'],['YooKassa','yookassa','YOOKASSA']] as const)test(`changing period preserves ${method} selection and order`,async({page})=>{
 const calls:Model<'PurchaseOrderInput'>[]=[];
 await routes(page,async(route,path)=>{
  if(path.endsWith('/payment-methods')){await route.fulfill({json:{methods:[{id:'yoomoney',currency:'RUB'},{id:'manual',currency:'RUB'},{id:'yookassa',currency:'RUB'},{id:'cryptomus',currency:'USD'}]}});return true;}
  if(path.endsWith('/catalogue')){await route.fulfill({json:{plans:[{...plan,periods:[30,60],prices:[...plan.prices,{period_days:60,currency:'RUB',amount_minor:'88888'}]}]}});return true;}
  if(path.endsWith('/orders')&&route.request().method()==='POST'){calls.push(route.request().postDataJSON());await route.abort();return true;}return false;
 });
 await page.goto('/catalogue?lang=en');await page.getByRole('button',{name:'Select plan'}).click();
 const selected=page.getByRole('radio',{name});await selected.check();await page.getByLabel('Period').selectOption('60');
 await expect(selected).toBeChecked();await expect(page.getByLabel('Currency')).toHaveValue('RUB');
 await page.getByRole('button',{name:'Buy plan'}).click();await expect(page.getByText(/Review the terms.*888\.88 RUB/)).toBeVisible();
 await page.getByRole('button',{name:'Confirm purchase'}).click();await expect.poll(()=>calls.length).toBe(1);
 expect(calls[0]).toEqual({action:'purchase',plan_id:planId,revision:3,period_days:60,payment_method:method,payment_type:type});
});
