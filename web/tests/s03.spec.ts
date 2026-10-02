import {test,expect,type Page} from '@playwright/test';
import type {components} from '../src/api/schema.gen';

type Model<K extends keyof components['schemas']> = components['schemas'][K];
const account:Model<'AccountResult'>={account:{account_id:'b496e45c-4e80-47d6-a868-e3c1da4e4f35',email:'client@example.test',email_verified:true,locale:'en',telegram_linked:false},csrf_token:'x'.repeat(43),capabilities:{trial_available:true}};
const base:Model<'Subscription'>={status:'active',devices:2,traffic_limit_bytes:1024,traffic_used_bytes:300,traffic_upload_bytes:100,traffic_download_bytes:200,traffic_remaining_bytes:724,unlimited_traffic:false,unlimited_devices:false,connection_available:true,access_profile:'regular',panel_error:null,observed_at:'2026-10-02T10:00:00Z',data_stale:false,expires_at:'2026-10-05T10:00:00Z',access_operation_id:null,access_operation_status:null};

async function mock(page:Page,getSub:()=>Model<'Subscription'>=()=>base){
 await page.route('**/api/v1/**',route=>{
  const path=new URL(route.request().url()).pathname;
  if(path.endsWith('/me'))return route.fulfill({json:account});
  if(path.endsWith('/trial-requests/current'))return route.fulfill({json:{request:null}});
  if(path.endsWith('/subscription/key'))return route.fulfill({json:{subscription_url:'https://subscriptions.example.test/sub/private-fixture'}});
  if(path.endsWith('/subscription'))return route.fulfill({json:getSub()});
  return route.fulfill({json:{}});
 });
}
const value=(page:Page,label:string)=>page.locator('dt').filter({hasText:new RegExp('^'+label+'$')}).locator('xpath=following-sibling::dd[1]');

test('finite limits remain finite for an unlimited access profile',async({page})=>{
 await mock(page,()=>({...base,access_profile:'unlimited'}));await page.goto('/cabinet?lang=en');
 await expect(page.getByRole('heading',{name:'client@example.test'})).toBeVisible();
 await expect(value(page,'Email')).toHaveText('client@example.test');await expect(value(page,'Language')).toHaveText('English');
 await expect(page.getByText('Trial active',{exact:true})).toBeVisible();await expect(value(page,'Access profile')).toHaveText('Unlimited');
 await expect(value(page,'Upload')).toHaveText('100 B');await expect(value(page,'Download')).toHaveText('200 B');await expect(value(page,'Used')).toHaveText('300 B');await expect(value(page,'Remaining')).toHaveText('724 B');await expect(value(page,'Traffic limit')).toHaveText('1 KiB');
});

test('unlimited zeroes remain observed while omitted counters stay unknown',async({page})=>{
 let sub:Model<'Subscription'>={...base,devices:0,traffic_limit_bytes:0,traffic_used_bytes:0,traffic_upload_bytes:0,traffic_download_bytes:0,traffic_remaining_bytes:null,unlimited_traffic:true,unlimited_devices:true,access_profile:'unlimited',expires_at:null};
 await mock(page,()=>sub);await page.goto('/cabinet?lang=en');
 await expect(value(page,'Devices')).toHaveText('Unlimited');await expect(value(page,'Traffic limit')).toHaveText('Unlimited');await expect(value(page,'Remaining')).toHaveText('Unlimited');await expect(value(page,'Expires')).toHaveText('No expiry');
 await expect(value(page,'Upload')).toHaveText('0 B');await expect(value(page,'Download')).toHaveText('0 B');await expect(value(page,'Used')).toHaveText('0 B');
 sub={status:'active',devices:1,traffic_limit_bytes:1024,traffic_used_bytes:null,observed_at:null,data_stale:true,expires_at:null,panel_error:'unavailable',access_operation_id:null,access_operation_status:null};
 await page.reload();await expect(value(page,'Upload')).toHaveText('No data');await expect(value(page,'Download')).toHaveText('No data');await expect(value(page,'Used')).toHaveText('No data');await expect(value(page,'Remaining')).toHaveText('No data');
 await expect(page.getByRole('alert')).toContainText('panel is unavailable');await expect(page.getByRole('button',{name:'Retry'})).toBeVisible();await expect(page.getByRole('button',{name:'Show subscription link'})).toBeVisible();
});

test('Russian cabinet distinguishes restricted subscription states',async({page})=>{
 let sub:Model<'Subscription'>={...base};await mock(page,()=>sub);
 const states:[Model<'Subscription'>['status'],string][]=[['none','Доступ ещё не активирован'],['provisioning','Подключаем доступ'],['needs_review','Нужна проверка поддержки'],['expired','Доступ истёк'],['banned','Доступ заблокирован'],['disabled','Доступ отключён'],['exhausted','Трафик исчерпан']];
 for(const [status,label] of states){sub={...base,status,connection_available:false};await page.goto('/cabinet');await expect(page.getByText(label,{exact:status==='none'||status==='expired'||status==='banned'||status==='disabled'||status==='exhausted'})).toBeVisible();await expect(page.getByRole('button',{name:'Показать ссылку'})).toHaveCount(0);}
});

test('retry clears a revealed key when refreshed access is restricted',async({page})=>{
 await page.setViewportSize({width:375,height:812});let sub:Model<'Subscription'>={...base,data_stale:true,panel_error:'unavailable'};await mock(page,()=>sub);await page.goto('/cabinet?lang=en');
 await page.getByRole('button',{name:'Show subscription link'}).click();await expect(page.getByLabel('Subscription link')).toBeVisible();
 sub={...base,status:'disabled',connection_available:false,data_stale:false};await page.getByRole('button',{name:'Retry'}).focus();await expect(page.getByRole('button',{name:'Retry'})).toBeFocused();await page.keyboard.press('Enter');
 await expect(page.getByText('Access disabled',{exact:true})).toBeVisible();await expect(page.getByLabel('Subscription link')).toHaveCount(0);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});
