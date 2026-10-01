import {test,expect} from '@playwright/test';
const id='425e3641-912b-4e50-b4d2-6b4a21598890',token='z'.repeat(43),password='A new long password';
test.describe('S02 reset',()=>{
 for(const lang of ['ru','en'])test('request accepted and cooldown '+lang,async({page})=>{
  await page.clock.install();let calls=0;
  await page.route('**/api/v1/auth/password-reset',async r=>{calls++;expect(r.request().postDataJSON()).toEqual({email:'client@example.test',locale:lang});await r.fulfill({status:202,json:{challenge_id:id,resend_after:60}})});
  await page.goto('/forgot-password?lang='+lang);await page.getByLabel('Email',{exact:true}).fill('client@example.test');await page.getByRole('button',{name:lang==='ru'?'Продолжить':'Continue',exact:true}).click();
  await expect(page.getByText(lang==='ru'?'Если аккаунт существует, мы отправим инструкцию.':'If the account exists, we will send instructions.')).toBeVisible();
  await expect(page.getByRole('button',{name:lang==='ru'?'Продолжить':'Continue',exact:true})).toBeDisabled();await page.clock.runFor(60000);expect(calls).toBe(1);
 });
 test('explicit link confirmation, repeat validation and double click',async({page})=>{
  let posts=0;await page.route('**/api/v1/auth/password-reset/complete',async r=>{posts++;expect(r.request().postDataJSON()).toEqual({token,new_password:password});await r.fulfill({status:204})});
  await page.goto('/reset-password#token='+token);expect(new URL(page.url()).hash).toBe('');expect(posts).toBe(0);await page.getByLabel('Пароль',{exact:true}).fill(password);await page.getByLabel('Повторите пароль').fill('different');await page.getByRole('button',{name:'Сохранить пароль'}).click();await expect(page.getByRole('alert')).toContainText('не совпадают');expect(posts).toBe(0);
  await page.getByLabel('Повторите пароль').fill(password);await page.getByRole('button',{name:'Сохранить пароль'}).dblclick();await expect(page.getByText('Пароль изменён. Теперь войдите.')).toBeVisible();expect(posts).toBe(1);expect(await page.evaluate(()=>JSON.stringify([localStorage,sessionStorage]))).not.toContain(token);
 });
 test('manual code at mobile width; no automatic login or account switch',async({page})=>{
  await page.setViewportSize({width:375,height:812});let posts=0,logins=0;
  await page.route('**/api/v1/auth/login',async r=>{logins++;await r.abort()});
  await page.route('**/api/v1/me',r=>r.fulfill({json:{account:{account_id:id,email:'account-b@example.test'}}}));
  await page.route('**/api/v1/auth/password-reset/complete',async r=>{posts++;expect(r.request().postDataJSON()).toEqual({challenge_id:id,code:'12345678',new_password:password});await r.fulfill({status:204})});
  await page.goto('/reset-password?lang=en');await page.getByLabel('Challenge ID from the email').fill(id);await page.getByLabel('Code from the email').fill('12345678');await page.getByLabel('Password',{exact:true}).fill(password);await page.getByLabel('Repeat password').fill(password);await page.getByRole('button',{name:'Save password'}).click();await expect(page.getByText('Password changed. You can now sign in.')).toBeVisible();expect(posts).toBe(1);expect(logins).toBe(0);
  expect(await page.evaluate(async()=>(await(await fetch('/api/v1/me')).json()).account.email)).toBe('account-b@example.test');expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 });
 for(const failure of ['429','503','lost'])test('failure without automatic retry '+failure,async({page})=>{
  await page.clock.install();let posts=0;await page.route('**/api/v1/auth/password-reset/complete',async r=>{posts++;if(failure==='lost')await r.abort();else await r.fulfill({status:Number(failure),headers:{'Retry-After':'30'},json:{error:{code:'SERVICE_UNAVAILABLE'}}})});
  await page.goto('/reset-password#token='+token);await page.getByLabel('Пароль',{exact:true}).fill(password);await page.getByLabel('Повторите пароль').fill(password);await page.getByRole('button',{name:'Сохранить пароль'}).click();await expect(page.getByRole('alert')).toBeVisible();await page.clock.runFor(30000);expect(posts).toBe(1);await expect(page.getByRole('link',{name:'Запросить новую инструкцию'})).toBeVisible();
 });
});

test.describe('S02 sessions',()=>{
 const account={account:{account_id:id,email:'client@example.test',locale:'en',email_verified:true,telegram_linked:false},csrf_token:'x'.repeat(43),capabilities:{trial_available:true}};
 test('password change refreshes CSRF; lost reply has no retry',async({page})=>{
  let reads=0,posts=0;await page.route('**/api/v1/me',async r=>{reads++;await r.fulfill({json:{...account,csrf_token:(reads>1?'y':'x').repeat(43)}})});
  await page.route('**/api/v1/me/security',r=>r.fulfill({json:{email:account.account.email,has_other_sessions:true,pending_email_change:null}}));
  await page.route('**/api/v1/me/password-change',async r=>{posts++;expect(r.request().headers()['x-csrf-token']).toBe('x'.repeat(43));expect(r.request().postDataJSON()).toEqual({current_password:'Old long password',new_password:password});await r.fulfill({status:204})});
  await page.goto('/cabinet/security?lang=en');await expect(page.getByRole('heading',{name:'Account security'})).toBeVisible({timeout:2000});
  await page.getByLabel('Current password',{exact:true}).fill('Old long password');await page.getByLabel('New password',{exact:true}).fill(password);await page.getByLabel('Repeat password',{exact:true}).fill('different');await page.getByRole('button',{name:'Change password',exact:true}).click();expect(posts).toBe(0);await page.getByLabel('Repeat password',{exact:true}).fill(password);await page.getByRole('button',{name:'Change password',exact:true}).click();await expect(page.getByRole('status')).toContainText('Saved');expect(reads).toBe(2);
  await page.route('**/api/v1/me/password-change',async r=>{posts++;await r.abort()});await page.getByLabel('Current password',{exact:true}).fill(password);await page.getByLabel('New password',{exact:true}).fill('Another safe new password');await page.getByLabel('Repeat password',{exact:true}).fill('Another safe new password');await page.getByRole('button',{name:'Change password',exact:true}).click();await expect(page.getByRole('alert')).toContainText('Sign in again');expect(posts).toBe(2);await expect(page.getByRole('button',{name:'Change password',exact:true})).toBeDisabled();
 });
 test('other sessions uses rotated CSRF',async({page})=>{
  await page.route('**/api/v1/me',r=>r.fulfill({json:account}));await page.route('**/api/v1/me/security',r=>r.fulfill({json:{email:account.account.email,has_other_sessions:true,pending_email_change:null}}));let calls=0;
  await page.route('**/api/v1/me/sessions/revoke-others',async r=>{calls++;expect(r.request().headers()['x-csrf-token']).toBe(account.csrf_token);expect(r.request().postDataJSON()).toEqual({current_password:'Old long password'});await r.fulfill({status:204})});
  await page.goto('/cabinet/security?lang=en');await expect(page.getByRole('heading',{name:'Account security'})).toBeVisible({timeout:2000});await page.getByLabel('Current password',{exact:true}).fill('Old long password');await page.getByRole('button',{name:'End other sessions'}).click();await expect(page.getByRole('status')).toContainText('Saved');expect(calls).toBe(1);
 });
 test('restricted logout after reload',async({page})=>{
  await page.route('**/api/v1/me',r=>r.fulfill({status:403,json:{error:{code:'ACCOUNT_RESTRICTED'}}}));await page.route('**/api/v1/auth/session',r=>r.fulfill({json:{csrf_token:account.csrf_token}}));let calls=0;
  await page.route('**/api/v1/auth/logout',async r=>{calls++;expect(r.request().headers()['x-csrf-token']).toBe(account.csrf_token);await r.fulfill({status:204})});
  await page.goto('/cabinet?lang=en');await expect(page.getByRole('alert')).toContainText('restricted');await expect(page.getByRole('button',{name:'Sign out'})).toBeEnabled({timeout:2000});await page.getByRole('button',{name:'Sign out'}).click();await expect(page).toHaveURL(/login/);expect(calls).toBe(1);
 });
});
