import {test,expect} from '@playwright/test';
import {readFileSync,existsSync} from 'node:fs';
import {execFileSync} from 'node:child_process';
import type {components} from '../src/api/schema.gen';
test('Web trial real HTTP + River + Python actor + browser',async({page})=>{
 await page.goto('/register?lang=en');await page.getByLabel('Email',{exact:true}).fill('browser@example.test');await page.getByRole('checkbox',{name:/terms of use/}).check();await page.getByRole('checkbox',{name:/privacy policy/}).check();await page.getByRole('button',{name:'Continue',exact:true}).click();await expect(page.getByText('If the address is available, we will send an email.')).toBeVisible();
 const file=process.env.TEST_MAIL_FILE!;await expect.poll(()=>existsSync(file)).toBe(true);const {token}=JSON.parse(readFileSync(file,'utf8'));
 await page.goto('/verify-email?lang=en#token='+token);expect(new URL(page.url()).hash).toBe('');await page.getByLabel('Password',{exact:true}).fill('browser fixture password with ✨');await page.getByRole('button',{name:'Verify email',exact:true}).click();await expect(page.getByText('Email verified. You can now sign in.')).toBeVisible();
 await page.getByRole('link',{name:'Sign in',exact:true}).first().click();await page.getByLabel('Email',{exact:true}).fill('browser@example.test');await page.getByLabel('Password',{exact:true}).fill('browser fixture password with ✨');await page.getByRole('button',{name:'Sign in',exact:true}).click();await expect(page).toHaveURL(/cabinet/);
 await page.getByRole('button',{name:'Request trial',exact:true}).click();await expect(page.getByText('Request sent. Waiting for support')).toBeVisible();const response=await page.request.get('/api/v1/trial-requests/current');const current:components['schemas']['CurrentTrialRequest']=await response.json();
 execFileSync('poetry',['run','python','-m','unittest','tests.test_web_trial_contract','-v'],{cwd:'..',env:{...process.env,TRIAL_CONTRACT_REQUEST_ID:current.request!.request_id},stdio:'pipe',timeout:20000});
 await expect(page.getByText('Trial active',{exact:true})).toBeVisible({timeout:15000});const keyResponse=page.waitForResponse(r=>r.url().endsWith('/subscription/key'));await page.getByRole('button',{name:'Show subscription link'}).click();expect((await keyResponse).headers()['cache-control']).toBe('no-store');await expect(page.getByLabel('Subscription link',{exact:true})).toHaveValue(/^https:\/\/subscriptions\.example\.test\/sub\/[0-9a-z]{16}$/);
 expect(await page.evaluate(()=>JSON.stringify(localStorage)+JSON.stringify(sessionStorage))).not.toContain(token);const cookie=(await page.context().cookies()).find(c=>c.name==='__Host-session');expect(cookie?.httpOnly&&cookie.secure).toBe(true);
 const internal=await page.request.post('/internal/v1/telegram/jobs/claim',{data:{limit:1}});expect(internal.status()).toBe(404);
 await page.getByRole('button',{name:'Sign out'}).click();await expect(page).toHaveURL(/login/);expect((await page.request.get('/api/v1/subscription/key')).status()).toBe(401);
});

if(process.env.TEST_MINI_INIT_FILE){
 test('Telegram trial real signed SDK + HTTP + River + panel',async({page})=>{
  let checkpoint='navigation',sessionStatus=0;
  page.on('response',r=>{if(new URL(r.url()).pathname==='/api/v1/telegram/mini-app/session')sessionStatus=r.status();});
  try{
  const {init_data}=JSON.parse(readFileSync(process.env.TEST_MINI_INIT_FILE!,'utf8'));
  await page.route('https://telegram.org/js/telegram-web-app.js',r=>r.fulfill({contentType:'application/javascript',body:`window.Telegram={WebApp:{initData:${JSON.stringify(init_data)},version:'9.6',platform:'web',themeParams:{},viewportStableHeight:720,ready(){},expand(){},onEvent(){},offEvent(){},BackButton:{show(){},hide(){},onClick(){},offClick(){}}}};`}));
  const keys:string[]=[];page.on('request',r=>{if(new URL(r.url()).pathname==='/api/v1/trials/activate')keys.push(r.headers()['idempotency-key']);});
  await page.goto('/mini-app/cabinet?lang=en');checkpoint='consent';await page.getByRole('checkbox',{name:/terms of use/i}).check();await page.getByRole('checkbox',{name:/privacy/i}).check();await page.getByRole('button',{name:'Continue',exact:true}).click();checkpoint='capability';
  const activate=page.getByRole('button',{name:'Activate trial',exact:true});await expect(activate).toBeVisible();await expect(page.getByRole('textbox')).toHaveCount(0);
  checkpoint='activation';const response=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/trials/activate');await activate.focus();await page.keyboard.press('Enter');const result=await response;expect(result.status()).toBe(201);expect(result.headers()['cache-control']).toBe('no-store');expect(result.request().postDataJSON()).toEqual({});expect(result.request().headers().authorization).toMatch(/^Bearer mini_/);expect(result.request().headers()['x-csrf-token']).toBeTruthy();checkpoint='provision';
  await expect(page.getByText('Trial active',{exact:true})).toBeVisible({timeout:20000});await expect(activate).toHaveCount(0);expect(keys).toHaveLength(1);
  expect(await page.evaluate(()=>JSON.stringify(localStorage)+JSON.stringify(sessionStorage))).not.toMatch(/mini_|init_data|tgWebAppData|subscription_url/);
  checkpoint='connection';await page.getByRole('button',{name:'Show subscription link'}).click();await expect(page.getByLabel('Subscription link',{exact:true})).toHaveValue(/^https:\/\/.+\/sub\/[0-9a-z]{16}$/);
  await page.getByRole('button',{name:'Hide connection details'}).click();await expect(page.getByLabel('Subscription link',{exact:true})).toHaveCount(0);
  }finally{process.stdout.write('TG_TRIAL_CHECKPOINT:'+checkpoint+'\n');if(sessionStatus)process.stdout.write('TG_TRIAL_CHECKPOINT:session:'+sessionStatus+'\n');}
 });
}

if(process.env.TEST_STARS_INIT_FILE){
 test('Telegram Stars real signed SDK + HTTP + River + panel',async({page})=>{
  let checkpoint='navigation';
  try{
  const {init_data,control_url}=JSON.parse(readFileSync(process.env.TEST_STARS_INIT_FILE!,'utf8'));
  await page.route('https://telegram.org/js/telegram-web-app.js',r=>r.fulfill({contentType:'application/javascript',body:`window.__ownedStars={opened:[],done:null};window.Telegram={WebApp:{initData:${JSON.stringify(init_data)},version:'9.6',platform:'web',themeParams:{},ready(){},expand(){},onEvent(){},offEvent(){},BackButton:{show(){},hide(){},onClick(){},offClick(){}},openInvoice(url,done){window.__ownedStars.opened.push(url);window.__ownedStars.done=done;}}};`}));
  const keys:string[]=[];page.on('request',r=>{if(new URL(r.url()).pathname==='/api/v1/orders')keys.push(r.headers()['idempotency-key']);});
  await page.goto('/mini-app/catalogue?lang=en');checkpoint='consent';await page.getByRole('checkbox',{name:/terms of use/i}).check();await page.getByRole('checkbox',{name:/privacy/i}).check();await page.getByRole('button',{name:'Continue',exact:true}).click();checkpoint='order';
  await page.getByRole('button',{name:'Select plan'}).click();await page.getByRole('button',{name:'Buy plan'}).click();await page.getByRole('button',{name:'Confirm purchase'}).click();
  await expect(page).toHaveURL(/\/mini-app\/orders\/[0-9a-f-]{36}/);const orderPath=new URL(page.url()).pathname;
  checkpoint='invoice';const pay=page.getByRole('button',{name:'Pay with Stars'});await pay.focus();await page.keyboard.press('Enter');
  await expect.poll(()=>page.evaluate(()=>(window as any).__ownedStars.opened)).toEqual(['https://t.me/$Owned_native_invoice']);
  await page.evaluate(()=>(window as any).__ownedStars.done('paid'));await expect(page.getByText('Waiting for payment confirmation.',{exact:true})).toBeVisible();
  checkpoint='money';expect((await page.request.post(control_url+'/complete')).status()).toBe(204);checkpoint='provision';
  await expect(page.getByText('Payment received. Access is ready.',{exact:true})).toBeVisible({timeout:20000});expect(keys).toHaveLength(1);expect(keys[0]).toMatch(/^[0-9a-f-]{36}$/);
  checkpoint='replay';await expect(pay).toHaveCount(0);await page.getByRole('button',{name:'Refresh status'}).click();await expect(page.getByText('Payment received. Access is ready.',{exact:true})).toBeVisible();expect(new URL(page.url()).pathname).toBe(orderPath);
  expect(await page.evaluate(()=>JSON.stringify(localStorage)+JSON.stringify(sessionStorage))).not.toMatch(/mini_|init_data|tgWebAppData|csrf_token|subscription_url/);
  }finally{process.stdout.write('TG_STARS_CHECKPOINT:'+checkpoint+'\n');}
 });
}
