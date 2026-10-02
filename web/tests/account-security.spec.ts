import {test,expect} from '@playwright/test';
const id='425e3641-912b-4e50-b4d2-6b4a21598890',token='z'.repeat(43),password='A new long password';
test.describe('Password reset',()=>{
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

test.describe('Account sessions',()=>{
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

test.describe('Email change',()=>{
 const account={account:{account_id:id,email:'old@example.test',locale:'en',email_verified:true,telegram_linked:false},csrf_token:'x'.repeat(43),capabilities:{trial_available:true}};
 const initial={new_email:'new@example.test',expires_at:'2026-10-02T12:30:00Z',current_email_confirmed:true,new_email_confirmed:false};
 for(const order of ['old','new'])test('both orders '+order,async({page})=>{
  let posts=0;await page.route('**/api/v1/me',r=>r.fulfill({json:account}));await page.route('**/api/v1/auth/email-change/confirm',async r=>{posts++;expect(r.request().postDataJSON()).toEqual({token:posts===1?token:'q'.repeat(43)});await r.fulfill({json:{completed:posts===2}})});
  await page.goto('/confirm-email-change?lang=en#token='+token);await expect(page.getByRole('heading',{name:'Confirm email change'})).toBeVisible({timeout:2000});expect(new URL(page.url()).hash).toBe('');expect(posts).toBe(0);
  await page.getByRole('button',{name:'Confirm mailbox',exact:true}).dblclick();await expect(page.getByText('Confirm the other mailbox to finish the change.')).toBeVisible();expect(posts).toBe(1);
  await page.goto('/confirm-email-change?lang=en#token='+'q'.repeat(43));await page.getByRole('button',{name:'Confirm mailbox',exact:true}).click();await expect(page.getByText('Email changed. Sign in with the new address.')).toBeVisible();expect(posts).toBe(2);
  expect(await page.evaluate(async()=>(await(await fetch('/api/v1/me')).json()).account.email)).toBe('old@example.test');expect(await page.evaluate(()=>JSON.stringify([localStorage,sessionStorage]))).not.toContain(token);
 });
 test('manual code and invalid proof; no retry on lost reply',async({page})=>{
  let calls=0;await page.route('**/api/v1/auth/email-change/confirm',async r=>{calls++;expect(r.request().postDataJSON()).toEqual({challenge_id:id,code:'12345678'});if(calls===1)await r.fulfill({status:400,json:{error:{code:'INVALID_VERIFICATION'}}});else await r.abort()});
  await page.goto('/confirm-email-change');await expect(page.getByRole('heading',{name:'Подтверждение смены email'})).toBeVisible({timeout:2000});await page.getByLabel('Номер заявки из письма').fill(id);await page.getByLabel('Код из письма').fill('12345678');await page.getByRole('button',{name:'Подтвердить почту',exact:true}).click();await expect(page.getByRole('alert')).toContainText('последнее письмо');await page.getByRole('button',{name:'Подтвердить почту',exact:true}).click();await expect(page.getByRole('alert')).toContainText('Проверьте состояние');await expect(page.getByRole('button',{name:'Подтвердить почту',exact:true})).toBeDisabled();expect(calls).toBe(2);
 });
 test('cancel and replace; pending refresh, keyboard and mobile',async({page})=>{
  await page.clock.install();await page.setViewportSize({width:375,height:812});const mobileAccount={...account,account:{...account.account,email:'x'.repeat(64)+'@example.test'}};let pending:typeof initial|null=initial,requests=0,cancels=0;
  await page.route('**/api/v1/me',r=>r.fulfill({json:mobileAccount}));await page.route('**/api/v1/me/security',r=>r.fulfill({json:{email:mobileAccount.account.email,has_other_sessions:false,pending_email_change:pending}}));
  await page.route('**/api/v1/me/email-change',async r=>{requests++;expect(r.request().postDataJSON()).toEqual({new_email:'new@example.test',current_password:'Old long password'});expect(r.request().headers()['x-csrf-token']).toBe(account.csrf_token);pending={...initial,current_email_confirmed:false};await r.fulfill({status:202,json:{change_id:id,expires_at:initial.expires_at,resend_after:60}})});
  await page.route('**/api/v1/me/email-change/cancel',async r=>{cancels++;pending=null;await r.fulfill({status:204})});
  await page.goto('/cabinet/security?lang=en');await expect(page.getByLabel('New email')).toBeVisible({timeout:2000});await expect(page.getByText('Resending replaces both previous instructions and confirmations.')).toBeVisible();await page.getByLabel('Current password',{exact:true}).fill('Old long password');await page.getByLabel('New email').focus();await page.keyboard.press('Tab');await expect(page.getByRole('button',{name:'Resend both instructions'})).toBeFocused();await page.keyboard.press('Enter');await expect(page.getByText('Current email: awaiting confirmation')).toBeVisible();expect(requests).toBe(1);
  pending={...initial,new_email_confirmed:true,current_email_confirmed:false};await page.clock.runFor(5000);await expect(page.getByText('New email: confirmed')).toBeVisible();expect(requests).toBe(1);await page.getByRole('button',{name:'Cancel email change'}).click();await expect(page.getByRole('button',{name:'Change email',exact:true})).toBeVisible();expect(cancels).toBe(1);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 });
 for(const code of [409,429,503])test('errors '+code,async({page})=>{
  await page.clock.install();await page.route('**/api/v1/me',r=>r.fulfill({json:account}));await page.route('**/api/v1/me/security',r=>r.fulfill({json:{email:account.account.email,has_other_sessions:false,pending_email_change:null}}));let calls=0;await page.route('**/api/v1/me/email-change',async r=>{calls++;await r.fulfill({status:code,headers:{'Retry-After':'30'},json:{error:{code:code===409?'EMAIL_CHANGE_UNAVAILABLE':'SERVICE_UNAVAILABLE'}}})});
  await page.goto('/cabinet/security?lang=en');await expect(page.getByLabel('New email')).toBeVisible({timeout:2000});await page.getByLabel('New email').fill('new@example.test');await page.getByRole('button',{name:'Change email',exact:true}).click();expect(calls).toBe(0);await page.getByLabel('Current password',{exact:true}).fill('Old long password');await page.getByRole('button',{name:'Change email',exact:true}).click();await expect(page.getByRole('alert')).toContainText(code===409?'unavailable':code===429?'30':'temporarily');await page.clock.runFor(30000);expect(calls).toBe(1);await expect(page.getByText('If you cannot access your current mailbox, contact support.')).toBeVisible();
 });
});

test('password reset second proof on same path remains explicit',async({page})=>{
 const nextToken='q'.repeat(43);let posts=0;await page.route('**/api/v1/auth/password-reset/complete',async r=>{posts++;expect(r.request().postDataJSON().token).toBe(posts===1?token:nextToken);await r.fulfill({status:204})});
 await page.goto('/reset-password#token='+token);await page.getByLabel('Пароль',{exact:true}).fill(password);await page.getByLabel('Повторите пароль').fill(password);await page.getByRole('button',{name:'Сохранить пароль'}).click();await expect(page.getByText('Пароль изменён. Теперь войдите.')).toBeVisible();
 await page.goto('/reset-password#token='+nextToken);await expect(page.getByLabel('Пароль',{exact:true})).toBeVisible({timeout:2000});expect(new URL(page.url()).hash).toBe('');expect(posts).toBe(1);await page.getByLabel('Пароль',{exact:true}).fill(password);await page.getByLabel('Повторите пароль').fill(password);await page.getByRole('button',{name:'Сохранить пароль'}).click();await expect(page.getByText('Пароль изменён. Теперь войдите.')).toBeVisible();expect(posts).toBe(2);
});

test('password change cancels pending poll before cookie rotation',async({page})=>{
 await page.clock.install();let reads=0,meReads=0;let oldRelease!:()=>void,newRelease!:()=>void;const oldWait=new Promise<void>(r=>oldRelease=r),newWait=new Promise<void>(r=>newRelease=r);
 const pending={new_email:'new@example.test',expires_at:'2026-10-02T12:30:00Z',current_email_confirmed:false,new_email_confirmed:false};
 await page.route('**/api/v1/me',async r=>{meReads++;await r.fulfill({json:{csrf_token:(meReads===1?'x':'y').repeat(43)}})});
 await page.route('**/api/v1/me/security',async r=>{reads++;if(reads===2){await oldWait;await r.fulfill({status:401,json:{error:{code:'INVALID_CREDENTIALS'}}}).catch(()=>{});}else{if(reads===3)await newWait;await r.fulfill({json:{email:'old@example.test',has_other_sessions:false,pending_email_change:reads===1?pending:null}})}});
 await page.route('**/api/v1/me/password-change',r=>r.fulfill({status:204}));await page.route('**/api/v1/me/sessions/revoke-others',async r=>{expect(r.request().headers()['x-csrf-token']).toBe('y'.repeat(43));await r.fulfill({status:204})});
 await page.goto('/cabinet/security?lang=en');await expect(page.getByText('Current email: awaiting confirmation')).toBeVisible();await page.clock.runFor(5000);await expect.poll(()=>reads).toBe(2);
 await page.getByLabel('Current password',{exact:true}).fill('Old long password');await page.getByLabel('New password',{exact:true}).fill(password);await page.getByLabel('Repeat password').fill(password);await page.getByRole('button',{name:'Change password',exact:true}).click();await expect.poll(()=>reads).toBe(3);
 oldRelease();await page.clock.runFor(1);newRelease();await expect(page.getByText('Saved.',{exact:true})).toBeVisible();await expect(page.getByRole('button',{name:'End other sessions'})).toBeEnabled({timeout:2000});await page.getByLabel('Current password',{exact:true}).fill(password);await page.getByRole('button',{name:'End other sessions'}).click();await expect(page.getByText('Saved.',{exact:true})).toBeVisible();
});
