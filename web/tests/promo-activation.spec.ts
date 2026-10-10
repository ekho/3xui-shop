import {test,expect,type Page,type Route} from '@playwright/test';

const id='11111111-1111-4111-8111-111111111111';
const operation='22222222-2222-4222-8222-222222222222';
const csrf='x'.repeat(43);
const activation={promocode_id:id,duration_days:30,operation_id:operation,status:'pending'};
const account={account:{account_id:id,email:'client@example.test',email_verified:true,locale:'ru',telegram_linked:false},csrf_token:csrf,capabilities:{trial_available:false}};
const subscription={status:'none',devices:0,traffic_limit_bytes:0,traffic_used_bytes:null,observed_at:null,data_stale:true,expires_at:null,access_profile:'unknown',vpn_banned:false,access_operation_id:null,access_operation_status:null};

async function fixture(page:Page,extra?:(route:Route,path:string)=>Promise<boolean>,mini=false){
 if(mini)await page.route('https://telegram.org/js/telegram-web-app.js',route=>route.fulfill({contentType:'application/javascript',body:"window.Telegram={WebApp:{initData:'owned-init-data',version:'9.6',platform:'web',themeParams:{},ready(){},expand(){},onEvent(){},offEvent(){},BackButton:{show(){},hide(){},onClick(){},offClick(){}}}};"}));
 await page.route('**/api/v1/**',async route=>{
  const path=new URL(route.request().url()).pathname;
  if(extra&&await extra(route,path))return;
  if(path==='/api/v1/telegram/mini-app/session')return route.fulfill({json:{...account,session_token:'mini_'+'b'.repeat(43)}});
  if(path==='/api/v1/me'||path==='/api/v1/telegram/mini-app/account')return route.fulfill({json:account});
  if(path==='/api/v1/subscription')return route.fulfill({json:subscription});
  if(path==='/api/v1/trial-requests/current')return route.fulfill({json:{request:null}});
  if(path==='/api/v1/orders/current')return route.fulfill({json:{order:null,can_purchase:false}});
  if(path==='/api/v1/reminders')return route.fulfill({json:{version:'reminders-v1',email_enabled:false,email_available:false,reminders:[]}});
  if(path==='/api/v1/stars-subscription')return route.fulfill({json:{state:'none',order_id:null,provider_state:null,control_state:null,paid_until:null,period_phase:'none',can_cancel:false,can_resume:false,external_billing_blocked:false,needs_review:false}});
  if(path==='/api/v1/notices')return route.fulfill({json:{notices:[],has_more:false}});
  return route.fulfill({json:{}});
 });
}

test('cabinet keeps the same key after uncertain response, then refreshes operation status',async({page})=>{
 const keys:string[]=[];let refreshed=false;
 await fixture(page,async(route,path)=>{
  if(path==='/api/v1/promocodes/activate'){
   const req=route.request();keys.push(req.headers()['idempotency-key']);expect(req.headers()['x-csrf-token']).toBe(csrf);expect(req.postDataJSON()).toEqual({code:'MiXeD'});
   if(keys.length===1)await route.fulfill({status:503,json:{error:{code:'SERVICE_UNAVAILABLE',message:'private',request_id:''}}});else await route.fulfill({status:201,json:activation});return true;
  }
  if(path==='/api/v1/promocodes/activations/'+operation){refreshed=true;await route.fulfill({json:{...activation,status:'applied'}});return true;}
  return false;
 });
 await page.goto('/cabinet');const form=page.getByRole('region',{name:'Активировать промокод'});
 await expect(form.getByText('Введите код, полученный от поддержки.')).toBeVisible();
 const input=form.getByLabel('Промокод');await input.fill('  MiXeD  ');await input.press('Enter');
 await expect(form.getByRole('alert')).toBeFocused();await expect(form.getByRole('alert')).not.toContainText('private');
 await form.getByRole('button',{name:'Активировать'}).click();await expect(form.getByRole('status')).toContainText('Ожидает выдачи');
 expect(keys).toHaveLength(2);expect(keys[0]).toBeTruthy();expect(keys[1]).toBe(keys[0]);
 await form.getByRole('button',{name:'Обновить статус'}).click();await expect(form.getByRole('status')).toContainText('Применён');expect(refreshed).toBe(true);
 expect(page.url()).not.toContain('MiXeD');expect(await page.evaluate(()=>JSON.stringify(localStorage)+JSON.stringify(sessionStorage))).not.toContain('MiXeD');
});

test('invalid and used codes show specific errors, focus and keyboard flow',async({page})=>{
 const codes=['PROMOCODE_INVALID','PROMOCODE_USED'];let calls=0;
 await fixture(page,async(route,path)=>{if(path!=='/api/v1/promocodes/activate')return false;const code=codes[calls++];await route.fulfill({status:code==='PROMOCODE_INVALID'?404:409,json:{error:{code,message:'private code and stack',request_id:''}}});return true;});
 await page.goto('/cabinet?lang=en');const form=page.getByRole('region',{name:'Activate promocode'}),input=form.getByLabel('Promocode');
 await input.fill('bad');await input.press('Enter');await expect(form.getByRole('alert')).toBeFocused();await expect(form.getByRole('alert')).toContainText('Invalid promocode');
 await input.fill('used');await form.getByRole('button',{name:'Activate',exact:true}).click();await expect(form.getByRole('alert')).toContainText('already used');
 await expect(form.getByRole('alert')).not.toContainText('private code');expect(calls).toBe(2);
});

test('signed MiniApp uses the same form with bearer auth and English copy',async({page})=>{
 let posted=false;
 await fixture(page,async(route,path)=>{if(path!=='/api/v1/promocodes/activate')return false;posted=true;expect(route.request().headers().authorization).toBe('Bearer mini_'+'b'.repeat(43));expect(route.request().headers().cookie).toBeUndefined();expect(route.request().headers()['x-csrf-token']).toBe(csrf);await route.fulfill({status:201,json:{...activation,status:'needs_review'}});return true;},true);
 await page.goto('/mini-app/cabinet?lang=en');const form=page.getByRole('region',{name:'Activate promocode'});await expect(form.getByText('Enter a code from support.')).toBeVisible();
 await form.getByLabel('Promocode').fill('mini-code');await form.getByRole('button',{name:'Activate',exact:true}).click();await expect(form.getByRole('status')).toContainText('Support review needed');expect(posted).toBe(true);
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});
