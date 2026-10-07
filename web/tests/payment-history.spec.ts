import {test,expect,type Page,type Route} from '@playwright/test';
import type {components} from '../src/api/schema.gen';

type Model<K extends keyof components['schemas']>=components['schemas'][K];
type Kind=Model<'PaymentHistoryInput'>['kind'];
const clientId='20000000-0000-4000-8000-000000000001';
const otherId='20000000-0000-4000-8000-000000000002';
const orderId='80000000-0000-4000-8000-000000000001';
const created='2026-10-03T10:00:00.123456Z';
const order:Model<'PaymentHistoryOrder'>={order_id:orderId,action:'renew',quote:{plan_id:'70000000-0000-4000-8000-000000000001',revision:3,devices:2,period_days:30,traffic_gb:20,profile:'regular',amount_minor:'9007199254740993',currency:'RUB'},payment_method:'manual',payment_type:'MANUAL',payment_status:'paid',fulfillment_status:'queued',created_at:created,expires_at:'2026-10-03T11:00:00Z',review_required:false,access_operation_id:null,review_reason:null};
const receipt:Model<'PaymentHistoryReceipt'>={operation_id:'bank-confirmation',order_id:orderId,payment_method:'manual',created_at:created,occurred_at:'2026-10-03T09:59:59Z',gross_minor:'9007199254740993',net_minor:null,currency:'RUB',raw_currency:'643',source:'operator',funds_order:true,review_required:false,review_reason:null,codepro:false,unaccepted:false,crypto_amounts:null};
const legacy:Model<'LegacyPaymentHistoryItem'>={source_id:'9007199254740993',created_at:created,updated_at:created,payment_status:'completed',fulfillment_status:'unknown',payment_method:'telegram_stars',quote:{action:'purchase',amount_minor:'99',currency:'XTR',devices:'9007199254740993',period_days:'30',traffic_gb:'0'}};
const empty=(kind:Kind):Model<'PaymentHistoryPage'>=>({kind,orders:[],receipts:[],legacy_transactions:[],has_more:false});
const history=(kind:Kind):Model<'PaymentHistoryPage'>=>({...empty(kind),orders:kind==='orders'?[order]:[],receipts:kind==='receipts'?[receipt]:[],legacy_transactions:kind==='legacy'?[legacy,{...legacy,source_id:'9007199254740992',payment_status:'refunded',quote:null,payment_method:null}]:[]});
const client=(id:string):Model<'OperatorClient'>=>({account_id:id,kind:'telegram',display_name:id===clientId?'Client one':'Client two',email:null,telegram_id:id===clientId?'123456789':'123456788',locale:'en',created_at:null,restricted:true,vpn_banned:false,had_subscription:false});
const card=(id:string):Model<'OperatorClientCard'>=>({client:client(id),subscription:{status:'none',access_profile:'regular',vpn_banned:false,devices:0,traffic_limit_bytes:0,expires_at:null,traffic_used_bytes:null,observed_at:null,data_stale:false,access_operation_id:null,access_operation_status:null},server:null,support:null,trial_requests:[],trial_has_more:false,audit_events:[],audit_has_more:false,legacy_approval:null,legacy_events:[],legacy_has_more:false});
const calls:{path:string;input:Model<'PaymentHistoryInput'>;csrf:string;method:string}[]=[];
async function routes(page:Page,extra?:(route:Route,path:string)=>Promise<boolean>){
 calls.length=0;
 await page.route('**/api/v1/**',async route=>{const request=route.request(),path=new URL(request.url()).pathname;
  if(path.endsWith('/payment-history'))calls.push({path,input:request.postDataJSON(),csrf:request.headers()['x-csrf-token'],method:request.method()});
  if(extra&&await extra(route,path))return;
  if(path.endsWith('/auth/session'))return route.fulfill({json:{csrf_token:'s'.repeat(43)}});
  if(path.endsWith('/operator/session'))return route.fulfill({json:{account:{account_id:'10000000-0000-4000-8000-000000000001',email:'operator@example.test',locale:'en'},role:'operator',csrf_token:'s'.repeat(43)}});
  if(path.endsWith('/operator/clients/search'))return route.fulfill({json:{clients:[client(clientId),client(otherId)],total:2,page:1,per_page:50}});
  if(path.endsWith('/operator/clients/'+clientId))return route.fulfill({json:card(clientId)});
  if(path.endsWith('/operator/clients/'+otherId))return route.fulfill({json:card(otherId)});
  if(path.endsWith('/support'))return route.fulfill({json:{conversation:null,messages:[],has_more:false,oldest_sequence:null}});
  if(path.endsWith('/orders/current'))return route.fulfill({json:{order:null}});
  if(path.endsWith('/payment-history'))return route.fulfill({json:history(request.postDataJSON().kind)});
  return route.fulfill({json:{}});
 });
}

for(const lang of ['en','ru'] as const)test(`self history is readable with exact money and keyboard at mobile width ${lang}`,async({page})=>{
 await routes(page);await page.setViewportSize({width:375,height:812});await page.goto('/cabinet/history?lang='+lang);
 await expect(page.getByRole('heading',{name:lang==='en'?'Payment history':'История платежей',exact:true})).toBeVisible();
 const kind=page.getByLabel(lang==='en'?'History type':'Вид истории');await kind.focus();await expect(kind).toBeFocused();
 await expect(page.getByText(lang==='en'?'90,071,992,547,409.93 RUB':'90\u00a0071\u00a0992\u00a0547\u00a0409,93 RUB',{exact:true})).toBeVisible();
 await expect(page.getByText(lang==='en'?'Payment confirmed':'Оплата подтверждена',{exact:true})).toBeVisible();
 await expect(page.getByText(lang==='en'?'Access queued':'Доступ ожидает подготовки',{exact:true})).toBeVisible();
 await expect(page.getByRole('link',{name:lang==='en'?'Open order':'Открыть заказ',exact:true})).toHaveAttribute('href','/orders/'+orderId+(lang==='en'?'?lang=en':''));
 await expect(page.getByRole('button',{name:/Continue to payment|Я оплатил|Отменить заказ/})).toHaveCount(0);
 expect(calls[0]).toMatchObject({input:{kind:'orders'},csrf:'s'.repeat(43),method:'POST'});
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await page.keyboard.press('Tab');const refresh=page.getByRole('button',{name:lang==='en'?'Refresh history':'Обновить историю'});await expect(refresh).toBeFocused();expect(await refresh.evaluate(element=>getComputedStyle(element).outlineStyle)).not.toBe('none');await page.keyboard.press('Enter');await expect.poll(()=>calls.length).toBe(2);
});

test('receipts distinguish operator confirmation, unknown net/currency and exact crypto facts',async({page})=>{
 await routes(page,async(route,path)=>{if(!path.endsWith('/payment-history')||route.request().postDataJSON().kind!=='receipts')return false;await route.fulfill({json:{...empty('receipts'),receipts:[receipt,{...receipt,operation_id:'protected-incoming',gross_minor:'1200',net_minor:null,currency:null,raw_currency:'XYZ',payment_method:'yoomoney',source:'provider',funds_order:false,review_required:true,review_reason:'currency_mismatch',codepro:true,unaccepted:true},{...receipt,operation_id:'crypto-exact',gross_minor:'123',net_minor:null,currency:'USD',payment_method:'heleket',source:'provider',crypto_amounts:{payment_amount:'0.000000000123',payer_amount:'0.000000000124',merchant_amount:'0.000000000121',payer_currency:'BTC'},provider_data:{private_token:'PRIVATE_PROVIDER_PAYLOAD'}}]}});return true;});
 await page.goto('/cabinet/history?lang=en');await page.getByLabel('History type').selectOption('receipts');const entries=page.locator('.payment-history article');await expect(entries).toHaveCount(3);
 await expect(entries.first()).toContainText('Confirmed by operator');await expect(entries.first().locator('dl div').filter({has:page.getByText('Net amount',{exact:true})})).toContainText('No data');
 await expect(entries.nth(1)).toContainText('1200 minor units');await expect(entries.nth(1)).toContainText('XYZ');await expect(entries.nth(1)).toContainText('Protected payment');await expect(entries.nth(1)).toContainText('Unaccepted payment');await expect(entries.nth(1)).toContainText('currency_mismatch');
 for(const value of ['0.000000000123','0.000000000124','0.000000000121','BTC'])await expect(entries.last().getByText(value,{exact:true})).toBeVisible();
 await expect(page.locator('body')).not.toContainText('PRIVATE_PROVIDER_PAYLOAD');expect(await page.evaluate(()=>JSON.stringify([localStorage,sessionStorage]))).not.toContain('bank-confirmation');
 expect(calls.at(-1)?.input).toEqual({kind:'receipts'});await expect(page.locator('.payment-history form')).toHaveCount(0);
});

test('archive preserves exact IDs and quantities and never claims completed means access',async({page})=>{
 await routes(page);await page.setViewportSize({width:375,height:812});await page.goto('/cabinet/history?lang=en');await page.getByLabel('History type').selectOption('legacy');const entries=page.locator('.payment-history article');await expect(entries).toHaveCount(2);
 await expect(entries.first()).toContainText('Completed in old bot');await expect(entries.first()).toContainText('Access unknown');await expect(entries.first().getByText('99 XTR',{exact:true})).toBeVisible();await expect(entries.first().locator('dl div').filter({has:page.getByText('Devices',{exact:true})})).toContainText('9007199254740993');
 await expect(entries.last()).toContainText('Refunded in old bot');await expect(entries.last()).toContainText('No data');await expect(entries.last()).toContainText('9007199254740992');await expect(entries.getByRole('link')).toHaveCount(0);await expect(entries.getByRole('button')).toHaveCount(0);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await page.getByRole('button',{name:'RU',exact:true}).click();await expect(page.getByRole('heading',{name:'История платежей',exact:true})).toBeVisible();await expect(page.getByText('Выдача доступа неизвестна').first()).toBeVisible();
});

test('failed older page retains the cursor and rows; refresh starts from the first page',async({page})=>{
 const older={...order,order_id:'70000000-0000-4000-8000-000000000001'};const first=Array.from({length:50},(_,i)=>({...order,order_id:'80000000-0000-4000-8000-'+String(50-i).padStart(12,'0')}));let attempts=0;
 await routes(page,async(route,path)=>{if(!path.endsWith('/payment-history'))return false;const input=route.request().postDataJSON();if(input.before_id){attempts++;if(attempts===1){await route.fulfill({status:503,json:{error:{code:'SERVICE_UNAVAILABLE'}}});return true;}await route.fulfill({json:{...empty('orders'),orders:[older]}});return true;}await route.fulfill({json:{...empty('orders'),orders:first,has_more:true}});return true;});
 await page.goto('/cabinet/history?lang=en');await expect(page.locator('.payment-history article')).toHaveCount(50);await page.getByRole('button',{name:'Load older records'}).click();await expect(page.getByRole('alert')).toBeVisible();await expect(page.locator('.payment-history article')).toHaveCount(50);await page.getByRole('button',{name:'Retry',exact:true}).click();await expect(page.locator('.payment-history article')).toHaveCount(51);await expect(page.getByRole('button',{name:'Load older records'})).toHaveCount(0);
 expect(calls.slice(1).map(call=>call.input)).toEqual([{kind:'orders',before_created_at:created,before_id:orderId},{kind:'orders',before_created_at:created,before_id:orderId}]);
 await page.getByRole('button',{name:'Refresh history'}).click();await expect(page.locator('.payment-history article')).toHaveCount(50);expect(calls.at(-1)?.input).toEqual({kind:'orders'});
});

test('loading, initial error, retry and empty state stay visible and usable',async({page})=>{
 let attempts=0,release!:()=>void;const held=new Promise<void>(resolve=>release=resolve);
 await routes(page,async(route,path)=>{if(!path.endsWith('/payment-history'))return false;attempts++;if(attempts===1){await held;await route.fulfill({status:503,json:{error:{code:'SERVICE_UNAVAILABLE'}}});}else await route.fulfill({json:empty(route.request().postDataJSON().kind)});return true;});
 await page.goto('/cabinet/history?lang=en');await expect(page.getByRole('status')).toContainText('Loading');await expect(page.getByRole('button',{name:'Refresh history'})).toBeDisabled();release();await expect(page.getByRole('alert')).toBeVisible();await page.getByRole('button',{name:'Retry',exact:true}).click();await expect(page.getByRole('status')).toContainText('No records of this type.');await expect(page.getByRole('button',{name:'Load older records'})).toHaveCount(0);
});

test('switching kind and locale discards held replies from the former view',async({page})=>{
 let release!:()=>void,heldCount=0;const held=new Promise<void>(resolve=>release=resolve);const old={...order,order_id:'80000000-0000-4000-8000-000000000099'};
 await routes(page,async(route,path)=>{if(!path.endsWith('/payment-history')||route.request().postDataJSON().kind!=='orders')return false;heldCount++;await held;await route.fulfill({json:{...empty('orders'),orders:[old]}}).catch(()=>{});return true;});
 await page.goto('/cabinet/history?lang=en');await expect.poll(()=>heldCount).toBe(1);await page.getByLabel('History type').selectOption('receipts');await expect(page.getByText('bank-confirmation',{exact:true})).toBeVisible();await page.getByRole('button',{name:'RU',exact:true}).click();await expect(page.getByText('Подтверждено оператором',{exact:true})).toBeVisible();release();await expect(page.getByText(old.order_id,{exact:true})).toHaveCount(0);await expect(page.getByLabel('Вид истории')).toHaveValue('receipts');await expect(page.getByText('bank-confirmation',{exact:true})).toBeVisible();
});

for(const status of [401,403])test(`self clears shown history after late access denial ${status}`,async({page})=>{
 let denied=false;await routes(page,async(route,path)=>{if(!path.endsWith('/payment-history')||!denied)return false;await route.fulfill({status,json:{error:{code:status===403?'ACCOUNT_RESTRICTED':'UNAUTHORIZED'}}});return true;});await page.goto('/cabinet/history?lang=en');await expect(page.getByText(orderId,{exact:true})).toBeVisible();denied=true;await page.getByRole('button',{name:'Refresh history'}).click();await expect(page.getByText(orderId,{exact:true})).toHaveCount(0);if(status===401)await expect(page).toHaveURL(/\/login/);else{await expect(page.getByRole('alert')).toBeVisible();await expect(page.getByLabel('History type')).toBeDisabled();}
});

test('operator target switch discards held client history and a role denial clears the card',async({page})=>{
 let release!:()=>void,heldCount=0,denied=false;const held=new Promise<void>(resolve=>release=resolve);
 await routes(page,async(route,path)=>{if(!path.endsWith('/payment-history'))return false;if(path.includes(clientId)){heldCount++;await held;await route.fulfill({json:history('orders')}).catch(()=>{});return true;}if(denied){await route.fulfill({status:403,json:{error:{code:'OPERATOR_REQUIRED'}}});return true;}await route.fulfill({json:{...empty('orders'),orders:[{...order,order_id:'80000000-0000-4000-8000-000000000002'}]}});return true;});
 await page.goto('/admin/clients/'+clientId+'/show?lang=en');await expect.poll(()=>heldCount).toBe(1);await page.evaluate(id=>{window.history.pushState(null,'','/admin/clients/'+id+'/show?lang=en');window.dispatchEvent(new PopStateEvent('popstate'));},otherId);await expect(page.getByRole('heading',{name:'Client two',exact:true})).toBeVisible();const area=page.getByRole('region',{name:'Payment history'});await expect(area.getByText('80000000-0000-4000-8000-000000000002',{exact:true})).toBeVisible();release();await expect(area.getByText(orderId,{exact:true})).toHaveCount(0);await expect(area.getByRole('link',{name:'Open order'})).toHaveCount(0);
 denied=true;await area.getByRole('button',{name:'Refresh history'}).click();await expect(page.getByRole('heading',{name:'Operator access required'})).toBeVisible();await expect(page.getByRole('heading',{name:'Client two',exact:true})).toHaveCount(0);await expect(page.getByText('80000000-0000-4000-8000-000000000002',{exact:true})).toHaveCount(0);
});
