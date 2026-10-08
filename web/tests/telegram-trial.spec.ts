import {test,expect,type Page} from '@playwright/test';
import type {components} from '../src/api/schema.gen';
type Model<K extends keyof components['schemas']>=components['schemas'][K];
const account:Model<'AccountResult'>={account:{account_id:'11111111-1111-4111-8111-111111111111',email:'owned@example.test',email_verified:true,locale:'ru',telegram_linked:false},csrf_token:'c'.repeat(43),capabilities:{trial_available:true,trial_mode:'activate'}};
const trial:Model<'TrialRequest'>={request_id:'22222222-2222-4222-8222-222222222222',status:'approved',created_at:'2026-10-08T00:00:00Z',decided_at:'2026-10-08T00:00:00Z',operation_id:'33333333-3333-4333-8333-333333333333',previous_request_id:null};
const none:Model<'Subscription'>={status:'none',devices:0,traffic_limit_bytes:0,traffic_used_bytes:null,observed_at:null,data_stale:false,expires_at:null,access_profile:'unknown',vpn_banned:false,access_operation_id:null,access_operation_status:null};
async function fixture(page:Page,profile=account,request:Model<'TrialRequest'>|null=null,sub=none){
 await page.route('**/api/v1/**',r=>{const p=new URL(r.request().url()).pathname;return r.fulfill({json:p.endsWith('/me')?profile:p.endsWith('/trial-requests/current')?{request}:p.endsWith('/orders/current')?{order:null,can_purchase:false}:p.endsWith('/subscription')?sub:{}})});
}

for(const [lang,activate,setting] of [['ru','Активировать триал','Подключаем доступ'],['en','Activate trial','Setting up access']] as const){
 test(`ordinary Telegram trial ${lang}`,async({page})=>{
  await fixture(page);let calls=0;
  await page.route('**/api/v1/trials/activate',async r=>{calls++;expect(r.request().postDataJSON()).toEqual({});expect(r.request().headers()['x-csrf-token']).toBe(account.csrf_token);expect(r.request().headers()['idempotency-key']).toMatch(/^[a-f0-9-]{36}$/);await r.fulfill({status:201,json:trial});});
  await page.goto('/cabinet?lang='+lang);const button=page.getByRole('button',{name:activate,exact:true});await expect(button).toBeVisible();await expect(page.getByRole('textbox')).toHaveCount(0);await button.focus();await page.keyboard.press('Enter');await expect.poll(()=>calls).toBe(1);
  await page.unroute('**/api/v1/**');await fixture(page,{...account,capabilities:{trial_available:false,trial_mode:'activate'}},trial,{...none,status:'provisioning'});await page.reload();await expect(page.getByRole('status').filter({hasText:setting})).toBeVisible();await expect(button).toHaveCount(0);await expect(page.getByRole('button',{name:/Показать ссылку|Show subscription link/})).toHaveCount(0);
 });
}

test('lost activation response retries the same endpoint and key',async({page})=>{
 await fixture(page);const keys:string[]=[];let reads=0;
 await page.route('**/api/v1/me',r=>{reads++;return r.fulfill({json:reads===1?account:{...account,capabilities:{trial_available:true}}})});
 await page.route('**/api/v1/trials/activate',async r=>{keys.push(r.request().headers()['idempotency-key']);expect(r.request().postDataJSON()).toEqual({});await r.fulfill(keys.length===1?{status:503,json:{error:{code:'SERVICE_UNAVAILABLE',message:'private provider error',request_id:trial.request_id}}}:{status:200,json:trial});});
 let manual=0;await page.route('**/api/v1/trial-requests',r=>{manual++;return r.fulfill({status:500,json:{}})});
 await page.goto('/cabinet');await page.getByRole('button',{name:'Активировать триал',exact:true}).click();const alert=page.getByRole('alert');await expect(alert).toContainText('временно недоступен');await expect(page.getByText('private provider error')).toHaveCount(0);
 await alert.getByRole('button',{name:'Повторить'}).click();await expect.poll(()=>reads).toBeGreaterThan(1);await page.getByRole('button',{name:'Активировать триал',exact:true}).click();await expect.poll(()=>keys.length).toBe(2);expect(keys[0]).toBe(keys[1]);expect(manual).toBe(0);
});

test('activation is disabled while its response is pending',async({page})=>{
 await fixture(page);let calls=0;let release!:()=>void;const wait=new Promise<void>(r=>release=r);
 await page.route('**/api/v1/trials/activate',async r=>{calls++;await wait;await r.fulfill({status:201,json:trial})});
 await page.goto('/cabinet');await page.getByRole('button',{name:'Активировать триал',exact:true}).click();await expect(page.getByRole('button',{name:'Отправляем…'})).toBeDisabled();expect(calls).toBe(1);release();await expect(page.getByRole('button',{name:'Отправляем…'})).toHaveCount(0);
});

test('missing mode preserves the manual form',async({page})=>{
 await fixture(page,{...account,capabilities:{trial_available:true}});let received:unknown;
 await page.route('**/api/v1/trial-requests',async r=>{received=r.request().postDataJSON();await r.fulfill({status:201,json:{...trial,status:'pending',operation_id:null,decided_at:null}})});
 await page.goto('/cabinet');await expect(page.getByRole('button',{name:'Активировать триал',exact:true})).toHaveCount(0);await page.getByLabel('Комментарий для поддержки').fill('Owned manual request');await page.getByRole('button',{name:'Запросить триал'}).click();await expect.poll(()=>received).toEqual({comment:'Owned manual request'});
});

test('reserved uncertain trial offers support without reactivation',async({page})=>{
 await fixture(page,{...account,capabilities:{trial_available:false,trial_mode:'activate'}},trial,{...none,status:'needs_review'});await page.goto('/cabinet');await expect(page.getByRole('status').filter({hasText:'Нужна проверка поддержки'})).toBeVisible();await expect(page.getByRole('button',{name:'Активировать триал',exact:true})).toHaveCount(0);await expect(page.getByRole('link',{name:'Связаться с поддержкой'})).toHaveAttribute('href','mailto:support@example.test');
});
