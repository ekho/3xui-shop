import {test,expect} from '@playwright/test';
import {readFileSync,existsSync} from 'node:fs';
import {execFileSync} from 'node:child_process';
import type {components} from '../src/api/schema.gen';
test('S01 real HTTP + River + Python actor + browser',async({page})=>{
 await page.goto('/register?lang=en');await page.getByLabel('Email',{exact:true}).fill('browser@example.test');await page.getByRole('checkbox',{name:/terms of use/}).check();await page.getByRole('checkbox',{name:/privacy policy/}).check();await page.getByRole('button',{name:'Continue',exact:true}).click();await expect(page.getByText('If the address is available, an email has been sent.')).toBeVisible();
 const file=process.env.S01_TEST_MAIL_FILE!;await expect.poll(()=>existsSync(file)).toBe(true);const {token}=JSON.parse(readFileSync(file,'utf8'));
 await page.goto('/verify-email?lang=en#token='+token);expect(new URL(page.url()).hash).toBe('');await page.getByLabel('Password',{exact:true}).fill('browser fixture password with ✨');await page.getByRole('button',{name:'Verify email',exact:true}).click();await expect(page.getByText('Email verified. You can now sign in.')).toBeVisible();
 await page.getByRole('link',{name:'Sign in',exact:true}).first().click();await page.getByLabel('Email',{exact:true}).fill('browser@example.test');await page.getByLabel('Password',{exact:true}).fill('browser fixture password with ✨');await page.getByRole('button',{name:'Sign in',exact:true}).click();await expect(page).toHaveURL(/cabinet/);
 await page.getByRole('button',{name:'Request trial',exact:true}).click();await expect(page.getByText('Request sent. Waiting for support')).toBeVisible();const response=await page.request.get('/api/v1/trial-requests/current');const current:components['schemas']['CurrentTrialRequest']=await response.json();
 execFileSync('poetry',['run','python','-m','unittest','tests.test_web_trial_contract','-v'],{cwd:'..',env:{...process.env,S01_CONTRACT_REQUEST_ID:current.request!.request_id},stdio:'pipe',timeout:20000});
 await expect(page.getByText('Trial active',{exact:true})).toBeVisible({timeout:15000});const keyResponse=page.waitForResponse(r=>r.url().endsWith('/subscription/key'));await page.getByRole('button',{name:'Show subscription link'}).click();expect((await keyResponse).headers()['cache-control']).toBe('no-store');await expect(page.getByLabel('Subscription link',{exact:true})).toHaveValue(/^https:\/\/subscriptions\.example\.test\/sub\/[0-9a-z]{16}$/);
 expect(await page.evaluate(()=>JSON.stringify(localStorage)+JSON.stringify(sessionStorage))).not.toContain(token);const cookie=(await page.context().cookies()).find(c=>c.name==='__Host-session');expect(cookie?.httpOnly&&cookie.secure).toBe(true);
 const internal=await page.request.post('/internal/v1/telegram/jobs/claim',{data:{limit:1}});expect(internal.status()).toBe(404);
 await page.getByRole('button',{name:'Sign out'}).click();await expect(page).toHaveURL(/login/);expect((await page.request.get('/api/v1/subscription/key')).status()).toBe(401);
});
