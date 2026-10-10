import {readFileSync} from 'node:fs';
import {randomUUID} from 'node:crypto';
import {chromium,expect} from '@playwright/test';

const origin=process.env.TEST_ORIGIN;
const fixture=JSON.parse(readFileSync(process.env.TEST_NATIVE_MAINTENANCE_FILE,'utf8'));
if(!origin?.startsWith('https://')||!fixture.operator_cookie||!fixture.client_cookie||!fixture.mini_init_data)throw new Error('native fixture unavailable');
const browser=await chromium.launch();
let checkpoint='operator';
const context=async cookie=>{
 const value=await browser.newContext({ignoreHTTPSErrors:true});
 await value.addCookies([{name:'__Host-session',value:cookie,url:origin,httpOnly:true,secure:true,sameSite:'Lax'}]);
 return value;
};
try{
 const operator=await context(fixture.operator_cookie);
 const admin=await operator.newPage();
 await admin.goto(origin+'/admin/maintenance?lang=en');
 await expect(admin.getByRole('heading',{name:'Maintenance',exact:true})).toBeVisible();
 await expect(admin.getByRole('status').filter({hasText:'Maintenance is off'})).toBeVisible();
 await admin.getByLabel('Reason for change').fill('Owned rendered browser acceptance');
 await admin.getByRole('checkbox',{name:'I confirm this maintenance change'}).check();
 const enable=admin.getByRole('button',{name:'Turn maintenance on'});
 await admin.getByRole('checkbox',{name:'I confirm this maintenance change'}).focus();
 await admin.keyboard.press('Tab');await expect(enable).toBeFocused();await expect(enable).toHaveCSS('outline-style','solid');
 await admin.keyboard.press('Enter');
 await expect(admin.getByRole('status').filter({hasText:'Maintenance is on'})).toBeVisible();

 checkpoint='web client';
 const clientContext=await context(fixture.client_cookie);
 const client=await clientContext.newPage();
 await client.goto(origin+'/cabinet?lang=ru');
 await expect(client.locator('aside[role="status"]').filter({hasText:'Обслуживание включено'})).toBeVisible();
 await expect(client.getByRole('button',{name:'Запросить триал'})).toBeDisabled();
 await client.getByRole('button',{name:'EN',exact:true}).click();
 await expect(client.locator('aside[role="status"]').filter({hasText:'Maintenance is on'})).toBeVisible();

 checkpoint='signed Mini';
 const miniContext=await browser.newContext({ignoreHTTPSErrors:true});
 const mini=await miniContext.newPage();
 await mini.route('https://telegram.org/js/telegram-web-app.js',route=>route.fulfill({contentType:'application/javascript',body:`window.Telegram={WebApp:{initData:${JSON.stringify(fixture.mini_init_data)},version:'9.6',platform:'web',themeParams:{},ready(){},expand(){},onEvent(){},offEvent(){},BackButton:{show(){},hide(){},onClick(){},offClick(){}}}};`}));
 await mini.goto(origin+'/mini-app/cabinet?lang=en');
 const consent=mini.getByRole('button',{name:'Continue',exact:true});
 await expect(consent).toBeVisible();
 {
  await mini.getByRole('checkbox',{name:/terms of use/i}).check();
  await mini.getByRole('checkbox',{name:/privacy/i}).check();
  await consent.click();
 }
 await expect(mini.locator('aside[role="status"]').filter({hasText:'Maintenance is on'})).toBeVisible();
 await expect(mini.getByRole('button',{name:'Activate trial'})).toBeDisabled();

 // A real second operator request changes the revision while the page is stale.
 checkpoint='operator conflict';
 const external=await admin.request.post(origin+'/api/v1/operator/maintenance',{headers:{Origin:origin,'X-CSRF-Token':fixture.operator_csrf,'Idempotency-Key':randomUUID()},data:{enabled:false,expected_revision:1,reason:'Owned concurrent operator',confirmed:true}});
 expect(external.status()).toBe(200);
 await admin.getByLabel('Reason for change').fill('Owned stale browser decision');
 await admin.getByRole('checkbox',{name:'I confirm this maintenance change'}).check();
 await admin.getByRole('button',{name:'Turn maintenance off'}).click();
 await expect(admin.getByRole('alert').filter({hasText:'Maintenance changed.'})).toBeVisible();
 await expect(admin.getByRole('status').filter({hasText:'Maintenance is off'})).toBeVisible();

 checkpoint='operator recovery';
 await admin.getByLabel('Reason for change').fill('Owned browser recovery');
 await admin.getByRole('checkbox',{name:'I confirm this maintenance change'}).check();
 await admin.getByRole('button',{name:'Turn maintenance on'}).click();
 await expect(admin.getByRole('status').filter({hasText:'Maintenance is on'})).toBeVisible();
 checkpoint='operator disable';
 await admin.getByLabel('Reason for change').fill('Owned browser disable');
 await admin.getByRole('checkbox',{name:'I confirm this maintenance change'}).check();
 const disable=admin.getByRole('button',{name:'Turn maintenance off'});
 await admin.getByRole('checkbox',{name:'I confirm this maintenance change'}).focus();
 await admin.keyboard.press('Tab');await expect(disable).toBeFocused();await expect(disable).toHaveCSS('outline-style','solid');
 await admin.keyboard.press('Enter');
 await expect(admin.getByRole('status').filter({hasText:'Maintenance is off'})).toBeVisible();
 await client.reload();
 await expect(client.locator('aside.maintenance-banner')).toHaveCount(0);
 await mini.reload();
 await expect(mini.locator('aside.maintenance-banner')).toHaveCount(0);
 console.log('PASS: live TLS operator, web, signed Mini, conflict, keyboard, and disable');
}catch{console.error('FAIL: native maintenance browser checkpoint '+checkpoint);process.exitCode=1;}finally{await browser.close();}
