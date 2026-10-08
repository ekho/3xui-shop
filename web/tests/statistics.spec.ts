import {test,expect,type Page,type Route} from '@playwright/test';
import {statisticsReport} from './statistics-fixture';
const actor='10000000-0000-4000-8000-000000000001';
const session={account:{account_id:actor,email:'operator@example.test',email_verified:true,locale:'en',telegram_linked:false},csrf_token:'s'.repeat(43)};
async function routes(page:Page,extra?:(route:Route,path:string)=>Promise<boolean>){
 await page.route('**/api/v1/**',async route=>{
  const path=new URL(route.request().url()).pathname;
  if(extra&&await extra(route,path))return;
  if(path.endsWith('/operator/session')||path.endsWith('/auth/session'))return route.fulfill({json:session});
  if(path.endsWith('/operator/reports/statistics'))return route.fulfill({json:statisticsReport(route.request().postDataJSON().campaign_id)});
  return route.fulfill({json:{}});
 });
}

test('report errors focus retry, panel outage stays unknown and revoked role removes all statistics',async({page})=>{
 let calls=0;
 await routes(page,async(route,path)=>{
  if(!path.endsWith('/operator/reports/statistics'))return false;
  calls++;
  if(calls===1){await route.fulfill({status:503,json:{error:{code:'SERVICE_UNAVAILABLE'}}});return true;}
  if(calls===3){await route.fulfill({status:403,json:{error:{code:'INVALID_CREDENTIALS'}}});return true;}
  const report=statisticsReport();report.activity={...report.activity,known_active_users:0,known_inactive_users:2,unknown_users:2};report.servers[0]={...report.servers[0],availability:'unavailable',clients:null,inbounds:null,enabled_inbounds:null,error_code:'PANEL_UNAVAILABLE'};report.groups=report.groups.map(group=>({...group,inbound_references:null,enabled_inbound_references:null}));
  await route.fulfill({json:report});return true;
 });
 await page.goto('/admin/statistics?lang=en');await expect(page.getByRole('alert')).toBeFocused();await expect(page.getByText('184,467,440,737,095,516.14 RUB',{exact:true})).toHaveCount(0);
 await page.getByRole('button',{name:'Retry',exact:true}).click();await expect(metric(page,'Panel clients')).toHaveText('No data');await expect(metric(page,'Active accounts')).toHaveText('No data');await expect(metric(page,'Accounts with unconfirmed access')).toHaveText('2');await expect(metric(page,'Confirmed active accounts')).toHaveText('0');await expect(page.getByText('Panel unavailable. Its counters are unknown; account facts remain available.')).toBeVisible();
 await page.getByRole('button',{name:'Refresh statistics',exact:true}).click();await expect(page.getByRole('heading',{name:'Operator access required',exact:true})).toBeVisible();await expect(page.getByRole('region',{name:'Statistics',exact:true})).toHaveCount(0);await expect(page.getByText('184,467,440,737,095,516.14 RUB',{exact:true})).toHaveCount(0);expect(calls).toBe(3);
});

test('held English snapshot cannot replace the refreshed Russian report',async({page})=>{
 let release!:()=>void;const pending=new Promise<void>(resolve=>release=resolve);let calls=0;
 await routes(page,async(route,path)=>{
  if(!path.endsWith('/operator/reports/statistics'))return false;
  calls++;if(calls===1){await pending;const stale=statisticsReport();stale.users=999;try{await route.fulfill({json:stale});}catch{}return true;}return false;
 });
 try{
  await page.goto('/admin/statistics?lang=en');await expect.poll(()=>calls).toBe(1);await page.getByRole('button',{name:'RU',exact:true}).click();await expect(page.getByRole('region',{name:'Статистика',exact:true})).toBeVisible();await expect(metric(page,'Аккаунты')).toHaveText('4');release();await expect(metric(page,'Аккаунты')).toHaveText('4');await expect(page.getByText('999',{exact:true})).toHaveCount(0);expect(calls).toBe(2);
 }finally{release();}
});

test('successful HTTP with the wrong report scope or version is rejected before showing metrics',async({page})=>{
 let calls=0;
 await routes(page,async(route,path)=>{
  if(!path.endsWith('/operator/reports/statistics'))return false;
  calls++;const report=statisticsReport(calls===1?'70000000-0000-4000-8000-000000000001':null);
  if(calls===2)(report as unknown as {version:string}).version='future-report-version';
  await route.fulfill({json:report});return true;
 });
 await page.goto('/admin/statistics?lang=en');await expect(page.getByRole('alert')).toBeFocused();await expect(metric(page,'Accounts')).toHaveCount(0);await page.getByRole('button',{name:'Retry',exact:true}).click();await expect.poll(()=>calls).toBe(2);await expect(page.getByRole('alert')).toBeFocused();await expect(metric(page,'Accounts')).toHaveCount(0);await page.getByRole('button',{name:'Retry',exact:true}).click();await expect(metric(page,'Accounts')).toHaveText('4');expect(calls).toBe(3);
});

test('a pending report cannot restore content after another operator request loses the role',async({page})=>{
 let release!:()=>void;const pending=new Promise<void>(resolve=>release=resolve);let started=false;
 await routes(page,async(route,path)=>{
  if(path.endsWith('/operator/reports/statistics')){started=true;await pending;try{await route.fulfill({json:statisticsReport()});}catch{}return true;}
  if(path.endsWith('/operator/campaigns/search')){await route.fulfill({status:403,json:{error:{code:'INVALID_CREDENTIALS'}}});return true;}return false;
 });
 try{
  await page.setViewportSize({width:1024,height:900});await page.goto('/admin/statistics?lang=en');await expect.poll(()=>started).toBe(true);await page.getByRole('menuitem',{name:'Campaigns',exact:true}).click();await expect(page.getByRole('heading',{name:'Operator access required',exact:true})).toBeVisible();release();await expect(page.getByRole('region',{name:'Statistics',exact:true})).toHaveCount(0);await expect(page.getByText('184,467,440,737,095,516.14 RUB',{exact:true})).toHaveCount(0);
 }finally{release();}
});
const metric=(page:Page,label:string)=>page.locator('dt').filter({hasText:new RegExp('^'+label+'$')}).locator('..').locator('dd').first();

for(const lang of ['en','ru']){
 test('global statistics exact money, partial activity, references and keyboard '+lang,async({page})=>{
  const reads:{body:unknown;csrf:string;key:string|undefined}[]=[];
  await routes(page,async(route,path)=>{if(path.endsWith('/operator/reports/statistics')){reads.push({body:route.request().postDataJSON(),csrf:route.request().headers()['x-csrf-token'],key:route.request().headers()['idempotency-key']});return false;}return false;});
  await page.setViewportSize({width:375,height:812});await page.goto('/admin/statistics?lang='+lang);
  const report=page.getByRole('region',{name:lang==='en'?'Statistics':'Статистика',exact:true});
  await expect(report).toBeVisible();
  await expect(report.getByText(lang==='en'?'184,467,440,737,095,516.14 RUB':'184 467 440 737 095 516,14 RUB',{exact:true}).first()).toBeVisible();
  await expect(report.getByText('0.000000000000000000000001 USDT',{exact:true})).toBeVisible();
  await expect(metric(page,lang==='en'?'Confirmed active accounts':'Подтверждённые активные аккаунты')).toHaveText('1');
  await expect(metric(page,lang==='en'?'Active accounts':'Активные аккаунты')).toHaveText(lang==='en'?'No data':'Нет данных');
  await expect(metric(page,lang==='en'?'Accounts':'Аккаунты')).toHaveText('4');
  await expect(metric(page,lang==='en'?'Trial percentage':'Доля аккаунтов с триалом')).toHaveText(lang==='en'?'25.00%':'25,00%');
  await expect(report.getByText(lang==='en'?'Includes hidden and archived plans.':'Включая скрытые и архивные тарифы.')).toBeVisible();
  await expect(report.locator('time')).toHaveCount(3);
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  const refresh=report.getByRole('button',{name:lang==='en'?'Refresh statistics':'Обновить статистику',exact:true});await refresh.focus();await page.keyboard.press('Enter');await expect.poll(()=>reads.length).toBe(2);
  expect(reads.every(v=>JSON.stringify(v.body)==='{"campaign_id":null}'&&v.csrf==='s'.repeat(43)&&v.key===undefined)).toBe(true);
  expect(await report.textContent()).not.toMatch(/password|subscription_url|panel_key|providerSecret/);
 });
}
