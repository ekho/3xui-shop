import {test,expect,type Page} from '@playwright/test';
import type {components} from '../src/api/schema.gen';
type Model<K extends keyof components['schemas']> = components['schemas'][K];
const account:Model<'AccountResult'>={account:{account_id:'b496e45c-4e80-47d6-a868-e3c1da4e4f35',email:'client@example.test',email_verified:true,locale:'ru',telegram_linked:false},csrf_token:'x'.repeat(43),capabilities:{trial_available:true}};
const trial:Model<'TrialRequest'>={request_id:'425e3641-912b-4e50-b4d2-6b4a21598890',status:'pending',created_at:'2026-10-01T00:00:00Z',decided_at:null,operation_id:null,previous_request_id:null};
const none:Model<'Subscription'>={status:'none',devices:0,traffic_limit_bytes:0,traffic_used_bytes:null,observed_at:null,data_stale:true,expires_at:null,access_operation_id:null,access_operation_status:null};
async function mock(page:Page,current:Model<'CurrentTrialRequest'>={request:null},sub:Model<'Subscription'>=none){
 await page.route('**/api/v1/**',async route=>{
  const p=new URL(route.request().url()).pathname;const body=p.endsWith('/me')?account:p.endsWith('/current')?current:p.endsWith('/subscription')?sub:p.endsWith('/key')?{subscription_url:'https://subscriptions.example.test/sub/fixtureprivate000'}:{};
  await route.fulfill({json:body});
 });
}
test.describe('S01',()=>{
 test('registration keyboard and equal confirmation',async({page})=>{
  let body:unknown;await page.route('**/api/v1/auth/register',async r=>{body=r.request().postDataJSON();await r.fulfill({status:202,json:{challenge_id:trial.request_id,resend_after:60}})});
  await page.goto('/register');await expect(page.getByRole('main')).toBeVisible();if(process.env.S01_SCREENSHOT_DIR)await page.screenshot({path:process.env.S01_SCREENSHOT_DIR+'/desktop.png'});await expect(page.getByLabel('Email',{exact:true})).toBeVisible();await page.getByLabel('Email',{exact:true}).fill('client@example.test');
  await page.keyboard.press('Tab');await expect(page.getByRole('checkbox',{name:/условия/})).toBeFocused();await page.keyboard.press('Space');await page.keyboard.press('Tab');await page.keyboard.press('Tab');await expect(page.getByRole('checkbox',{name:/данных/})).toBeFocused();await page.keyboard.press('Space');await page.keyboard.press('Tab');await page.keyboard.press('Tab');await expect(page.getByRole('button',{name:'Продолжить'})).toBeFocused();await page.keyboard.press('Enter');
  await expect(page.getByText('Если адрес доступен, мы отправим письмо.')).toBeVisible();expect(body).toEqual({email:'client@example.test',locale:'ru',accepted_terms_version:'1',accepted_privacy_version:'1'});
 });
 test('fragment cleared before requests; explicit Unicode password submit',async({page})=>{
  let posts=0;await page.route('**/api/v1/auth/verify-email',async r=>{posts++;expect(r.request().postDataJSON().new_password).toBe('я'.repeat(15));await r.fulfill({json:{verified:true}})});
  await page.goto('/verify-email#token='+'x'.repeat(43));expect(new URL(page.url()).hash).toBe('');expect(posts).toBe(0);
  await page.getByLabel('Пароль',{exact:true}).fill('я'.repeat(129));await page.getByRole('button',{name:'Подтвердить email'}).click();await expect(page.getByRole('alert')).toContainText('15–128');expect(posts).toBe(0);await expect(page.getByLabel('Пароль',{exact:true})).toBeFocused();
  expect(await page.getByLabel('Пароль',{exact:true}).getAttribute('aria-describedby')).toContain('form-error');
  await page.getByLabel('Пароль',{exact:true}).fill('я'.repeat(15));await page.getByRole('button',{name:'Подтвердить email'}).click();await expect(page.getByText('Email подтверждён. Теперь войдите.')).toBeVisible();expect(posts).toBe(1);
  expect(await page.evaluate(()=>Object.keys(window).filter(k=>k==='__emailToken'))).toEqual([]);
 });
 test('pending poll exact spacing; terminal stop; private key and clipboard fallback',async({page})=>{
  await page.clock.install({time:new Date('2026-10-01T00:00:00Z')});await page.clock.pauseAt(new Date('2026-10-01T00:00:01Z'));let state:Model<'Subscription'>={...none,status:'provisioning'};const times:number[]=[];let keyReads=0;
  await mock(page,{request:trial});await page.route('**/api/v1/subscription',async r=>{times.push(await page.evaluate(()=>Date.now()));await r.fulfill({json:state})});await page.route('**/api/v1/subscription/key',async r=>{keyReads++;await r.fulfill({json:{subscription_url:'https://subscriptions.example.test/sub/fixtureprivate000'},headers:{'Cache-Control':'no-store'}})});
  await page.goto('/cabinet');await expect(page.getByText('Заявка отправлена. Ожидаем решения поддержки')).toBeVisible();expect(keyReads).toBe(0);
  await page.clock.runFor(5000);await expect.poll(()=>times.length).toBe(2);state={...none,status:'active',devices:1,traffic_limit_bytes:15*1024**3,expires_at:'2026-10-04T00:00:00Z',traffic_used_bytes:1234,observed_at:'2026-10-01T00:00:10Z',data_stale:false};
  await page.clock.runFor(5000);await expect(page.getByText('Триал активен')).toBeVisible();expect(times.slice(1).map((t,i)=>t-times[i])).toEqual([5000,5000]);await page.clock.runFor(20000);expect(times.length).toBe(3);
  await page.getByRole('button',{name:'Показать ссылку'}).click();await expect(page.getByRole('button',{name:'Скопировать'})).toBeVisible();expect(keyReads).toBe(1);
  await page.evaluate(()=>Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:()=>Promise.reject(new Error('fixture'))}}));await page.getByRole('button',{name:'Скопировать'}).click();await expect(page.getByText('Выделите ссылку и скопируйте вручную.')).toBeVisible();
  expect(await page.evaluate(()=>[JSON.stringify(localStorage),JSON.stringify(sessionStorage)].join(''))).not.toMatch(/fixtureprivate000|csrf_token|__Host-session/);
 });
 test('request retries preserve idempotency and CSRF',async({page})=>{
  const keys:string[]=[];await mock(page);await page.route('**/api/v1/trial-requests',async r=>{keys.push(r.request().headers()['idempotency-key']);expect(r.request().headers()['x-csrf-token']).toBe(account.csrf_token);if(keys.length===1)await r.fulfill({status:503,json:{error:{code:'SERVICE_UNAVAILABLE',message:'safe',request_id:trial.request_id}}});else await r.fulfill({status:201,json:trial})});
  await page.goto('/cabinet');await page.getByRole('button',{name:'Запросить триал'}).click();await expect(page.getByRole('alert')).toBeVisible();await page.getByRole('button',{name:'Запросить триал'}).click();expect(keys.length).toBe(2);expect(keys[0]).toBe(keys[1]);
 });
 test('rejection and support contact; mobile English',async({page})=>{
  await page.setViewportSize({width:375,height:812});await mock(page,{request:{...trial,status:'rejected',decided_at:'2026-10-01T00:00:01Z'}},none);await page.goto('/cabinet');await expect(page.getByText('В триале отказано. Повторное рассмотрение — через поддержку.')).toBeVisible();await expect(page.getByRole('link',{name:'Связаться с поддержкой'})).toHaveAttribute('href','mailto:support@example.test');await expect(page.getByRole('button',{name:'Запросить триал'})).toHaveCount(0);
  await page.getByRole('button',{name:'EN',exact:true}).click();await expect(page.getByText('Trial declined. Contact support for reconsideration.')).toBeVisible();expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);if(process.env.S01_SCREENSHOT_DIR)await page.screenshot({path:process.env.S01_SCREENSHOT_DIR+'/mobile.png'});
 });
 test('needs review uses safe error copy; expired session clears key',async({page})=>{
  await mock(page,{request:{...trial,status:'approved',operation_id:account.account.account_id}},{...none,status:'needs_review'});await page.goto('/cabinet');await expect(page.getByText('Нужна проверка поддержки.')).toBeVisible();await expect(page.getByRole('button',{name:'Показать ссылку'})).toHaveCount(0);
  await page.unroute('**/api/v1/**');await page.route('**/api/v1/**',r=>r.fulfill({status:401,json:{error:{code:'INVALID_CREDENTIALS',message:'provider stacktrace private',request_id:trial.request_id}}}));await page.goto('/cabinet');await expect(page).toHaveURL(/\/login/);await expect(page.getByText('provider stacktrace private')).toHaveCount(0);
 });
 test('slow polls do not overlap; leaving cancels polling',async({page})=>{
  await page.clock.install({time:new Date('2026-10-01T00:00:00Z')});await page.clock.pauseAt(new Date('2026-10-01T00:00:01Z'));
  let calls=0;let release!:()=>void;const wait=new Promise<void>(resolve=>{release=resolve});
  await mock(page,{request:trial},{...none,status:'provisioning'});await page.route('**/api/v1/subscription',async r=>{calls++;if(calls===1)await wait;await r.fulfill({json:{...none,status:'provisioning'}})});
  await page.goto('/cabinet');await expect.poll(()=>calls).toBe(1);await page.clock.runFor(10000);expect(calls).toBe(1);release();await expect(page.getByText('Заявка отправлена. Ожидаем решения поддержки')).toBeVisible();await page.clock.runFor(4999);expect(calls).toBe(1);await page.clock.runFor(1);await expect.poll(()=>calls).toBe(2);
  await page.goto('/login');await page.clock.runFor(20000);expect(calls).toBe(2);
 });
 test('pending without an operation has one status message',async({page})=>{await mock(page,{request:trial},none);await page.goto('/cabinet');await expect(page.getByText('Заявка отправлена. Ожидаем решения поддержки')).toHaveCount(1);});
 test('temporary read error resumes pending polling',async({page})=>{
  await page.clock.install({time:new Date('2026-10-01T00:00:00Z')});await page.clock.pauseAt(new Date('2026-10-01T00:00:01Z'));
  let calls=0;await mock(page,{request:trial});await page.route('**/api/v1/subscription',async r=>{calls++;if(calls===1)await r.fulfill({status:503,json:{error:{code:'SERVICE_UNAVAILABLE',message:'private panel error',request_id:trial.request_id}}});else await r.fulfill({json:{...none,status:'active',devices:1,traffic_limit_bytes:15*1024**3,expires_at:'2026-10-04T00:00:00Z'}})});
  await page.goto('/cabinet');await expect(page.getByRole('alert')).toContainText('временно недоступен');await page.clock.runFor(5000);await expect(page.getByText('Триал активен')).toBeVisible();expect(calls).toBe(2);await expect(page.getByText('private panel error')).toHaveCount(0);
 });
 test('login rate limit respects Retry-After',async({page})=>{
  await page.clock.install();let calls=0;await page.route('**/api/v1/auth/login',async r=>{calls++;await r.fulfill({status:429,headers:{'Retry-After':'30'},json:{error:{code:'RATE_LIMITED',message:'private provider message',request_id:trial.request_id}}})});await page.goto('/login');await page.getByLabel('Email',{exact:true}).fill('client@example.test');await page.getByLabel('Пароль',{exact:true}).fill('fixture long password');await page.getByRole('button',{name:'Войти',exact:true}).click();await expect(page.getByRole('alert')).toContainText('30');await expect(page.getByRole('button',{name:'Войти',exact:true})).toBeDisabled();await page.clock.runFor(30000);await expect(page.getByRole('button',{name:'Войти',exact:true})).toBeEnabled();expect(calls).toBe(1);
 });
});
