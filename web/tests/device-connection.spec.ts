import {test,expect,type Page} from '@playwright/test';
import type {components} from '../src/api/schema.gen';

type Subscription=components['schemas']['Subscription'];
const ownURL='https://subscriptions.example.test/sub/private-fixture';
const account={account:{account_id:'b496e45c-4e80-47d6-a868-e3c1da4e4f35',email:'client@example.test',email_verified:true,locale:'en',telegram_linked:false},csrf_token:'x'.repeat(43),capabilities:{trial_available:true}};
const active:Subscription={status:'active',devices:1,traffic_limit_bytes:1024,traffic_used_bytes:0,observed_at:'2026-10-02T10:00:00Z',data_stale:false,expires_at:'2026-10-05T10:00:00Z',connection_available:true,access_profile:'regular',vpn_banned:false,access_operation_id:null,access_operation_status:null};

async function mock(page:Page,getSub:()=>Subscription=()=>active,key:()=>Promise<string>|string=()=>ownURL){
 await page.route('**/api/v1/**',async route=>{
  const path=new URL(route.request().url()).pathname;
  if(path.endsWith('/me'))return route.fulfill({json:account});
  if(path==='/api/v1/notices')return route.fulfill({json:{version:'operator-notices-v1',email_enabled:false,email_available:true,page:1,per_page:20,total:0,notices:[]}});
  if(path==='/api/v1/reminders')return route.fulfill({json:{version:'reminders-v1',email_enabled:false,email_available:true,reminders:[]}});
  if(path.endsWith('/trial-requests/current'))return route.fulfill({json:{request:null}});
  if(path.endsWith('/subscription/key'))return route.fulfill({json:{subscription_url:await key()}}).catch(()=>{});
  if(path.endsWith('/subscription'))return route.fulfill({json:getSub()});
  if(path.endsWith('/auth/logout'))return route.fulfill({status:204});
  return route.fulfill({json:{}});
 });
}

test('five platforms, iOS region and install fallbacks work at mobile width',async({page})=>{
 await page.setViewportSize({width:375,height:812});await mock(page);await page.goto('/cabinet?lang=en');
 await expect(page.getByRole('heading',{name:'Connect a device'})).toBeVisible();const platform=page.getByLabel('Platform');await expect(platform.locator('option')).toHaveText(['iOS','Android','macOS','Windows','Other']);
 await expect(page.getByRole('link',{name:'Install Happ'})).toHaveAttribute('href','https://apps.apple.com/us/app/happ-proxy-utility/id6504287215');await page.getByRole('button',{name:'RU',exact:true}).click();await expect(page.getByRole('heading',{name:'Подключить устройство'})).toBeVisible();await expect(page.getByLabel('Регион App Store')).toHaveValue('global');await page.getByRole('button',{name:'EN',exact:true}).click();await page.getByLabel('App Store region').selectOption('ru');await expect(page.getByRole('link',{name:'Install Happ'})).toHaveAttribute('href','https://apps.apple.com/ru/app/happ-lite/id6799917773');await expect(page.getByRole('link',{name:'Global App Store'})).toBeVisible();await expect(page.getByRole('link',{name:'Developer catalog'})).toBeVisible();
 await platform.focus();await page.keyboard.press('a');await expect(platform).toHaveValue('android');await expect(page.getByRole('link',{name:'Install Happ'})).toHaveAttribute('href','https://play.google.com/store/apps/details?id=com.happproxy');
 await platform.selectOption('macos');await expect(page.getByRole('link',{name:'Install Happ'})).toHaveAttribute('href','https://github.com/Happ-proxy/happ-desktop/releases/latest/download/Happ.macOS.universal.dmg');await platform.selectOption('windows');await expect(page.getByRole('link',{name:'Install Happ'})).toHaveAttribute('href','https://github.com/Happ-proxy/happ-desktop/releases/latest/download/setup-Happ.x64.exe');await platform.selectOption('other');await expect(page.getByRole('link',{name:'Open developer catalog'})).toHaveAttribute('href','https://www.happ.su/main');await expect(page.getByText('Import the HTTPS subscription manually into a compatible client.')).toBeVisible();
 await expect(page.getByRole('list',{name:'Connection steps'}).getByRole('listitem')).toHaveCount(3);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});

test('copy, local QR and intercepted macOS deep link use the same own URL',async({page})=>{
 const requests:string[]=[];let keyReads=0;page.on('request',request=>requests.push(request.url()));await mock(page,()=>active,()=>{keyReads++;return ownURL});await page.goto('/cabinet?lang=en');await page.getByLabel('Platform').selectOption('macos');await page.getByRole('button',{name:'Show subscription link'}).click();expect(keyReads).toBe(1);
 await expect(page.getByLabel('Subscription link')).toHaveValue(ownURL);const deep=page.getByRole('link',{name:'Open in Happ'});await expect(deep).toHaveAttribute('href','happ://add/'+ownURL);
 await page.evaluate(()=>Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:(value:string)=>{(window as Window&{copied?:string}).copied=value;return Promise.resolve();}}}));await page.getByRole('button',{name:'Copy'}).click();expect(await page.evaluate(()=>(window as Window&{copied?:string}).copied)).toBe(ownURL);await page.getByRole('button',{name:'Show QR code'}).click();await expect(page.getByRole('img',{name:'Subscription QR code'})).toBeVisible();expect(requests.filter(url=>url.includes('private-fixture'))).toHaveLength(0);
 await page.evaluate(()=>document.addEventListener('click',event=>{const target=event.target;if(target instanceof Element&&target.closest('a[href^="happ:"]'))event.preventDefault();},{capture:true}));const before=page.url();await deep.click();await expect(page).toHaveURL(before);
});

test('denied states hide connection while expired access can import',async({page})=>{
 let sub:Subscription={...active};await mock(page,()=>sub);for(const status of ['none','provisioning','needs_review','banned','disabled','exhausted'] as const){sub={...active,status,connection_available:false};await page.goto('/cabinet?lang=en');await expect(page.getByRole('heading',{name:'Connect a device'})).toHaveCount(0);}
 sub={...active,status:'expired',connection_available:true};await page.goto('/cabinet?lang=en');await expect(page.getByRole('heading',{name:'Connect a device'})).toBeVisible();await expect(page.getByRole('button',{name:'Show subscription link'})).toBeVisible();
});

test('TTL, visibility and pagehide clear key and require a fresh request',async({page})=>{
 await page.clock.install();let reads=0;await mock(page,()=>active,()=>{reads++;return ownURL});await page.goto('/cabinet?lang=en');const reveal=page.getByRole('button',{name:'Show subscription link'});await reveal.click();await expect(page.getByLabel('Subscription link')).toBeVisible();await page.clock.runFor(60000);await expect(page.getByLabel('Subscription link')).toHaveCount(0);await reveal.click();expect(reads).toBe(2);
 await page.evaluate(()=>{Object.defineProperty(document,'visibilityState',{configurable:true,value:'hidden'});document.dispatchEvent(new Event('visibilitychange'));});await expect(page.getByLabel('Subscription link')).toHaveCount(0);await reveal.click();expect(reads).toBe(3);await page.getByRole('button',{name:'Show QR code'}).click();await page.getByRole('button',{name:'Hide connection details'}).click();await page.waitForTimeout(10);await expect(page.getByRole('img',{name:'Subscription QR code'})).toHaveCount(0);await reveal.click();expect(reads).toBe(4);await page.evaluate(()=>dispatchEvent(new PageTransitionEvent('pagehide')));await expect(page.getByLabel('Subscription link')).toHaveCount(0);
});

test('hide suppresses a late key response and logout clears before completion',async({page})=>{
 let release!:()=>void;const wait=new Promise<void>(resolve=>release=resolve);await mock(page,()=>active,async()=>{await wait;return ownURL});await page.goto('/cabinet?lang=en');await page.getByRole('button',{name:'Show subscription link'}).click({noWaitAfter:true});await page.getByRole('button',{name:'Hide connection details'}).click();release();await page.waitForTimeout(10);await expect(page.getByLabel('Subscription link')).toHaveCount(0);
 await page.unroute('**/api/v1/**');let finishLogout!:()=>void;const logoutWait=new Promise<void>(resolve=>finishLogout=resolve);await mock(page);await page.route('**/api/v1/auth/logout',async route=>{await logoutWait;await route.fulfill({status:204})});await page.getByRole('button',{name:'Show subscription link'}).click();await expect(page.getByLabel('Subscription link')).toBeVisible();await page.getByRole('button',{name:'Sign out'}).click({noWaitAfter:true});await expect(page.getByLabel('Subscription link')).toHaveCount(0);finishLogout();
});

test('invalid key and clipboard or QR failures retain a manual text fallback',async({page})=>{
 await page.addInitScript(()=>{Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:()=>Promise.reject(new Error('fixture'))}});HTMLCanvasElement.prototype.getContext=()=>null;});let reads=0;await mock(page,()=>active,()=>++reads===1?'http://invalid.example.test/sub/key':ownURL);await page.goto('/cabinet?lang=en');const reveal=page.getByRole('button',{name:'Show subscription link'});await reveal.click();await expect(page.getByLabel('Subscription link')).toHaveCount(0);await expect(page.getByRole('alert')).toBeVisible();await reveal.click();await expect(page.getByLabel('Subscription link')).toHaveValue(ownURL);
 await page.getByRole('button',{name:'Copy'}).click();await expect(page.getByText('Select the link and copy it manually.')).toBeVisible();await expect(page.getByLabel('Subscription link')).toBeFocused();await page.getByRole('button',{name:'Show QR code'}).click();await expect(page.getByText('QR code is unavailable. Use the subscription link instead.')).toBeVisible();await expect(page.getByLabel('Subscription link')).toHaveValue(ownURL);
 await page.getByRole('button',{name:'Hide connection details'}).click();await page.route('**/api/v1/subscription/key',route=>route.fulfill({status:403,json:{error:{code:'ACCOUNT_RESTRICTED'}}}));await reveal.click();await expect(page.getByLabel('Subscription link')).toHaveCount(0);await expect(page.getByRole('alert')).toContainText('restricted');
});
