import {test,expect} from '@playwright/test';
import {readFileSync,existsSync} from 'node:fs';
import type {components} from '../src/api/schema.gen';
test('Web trial real HTTP + River + Go decision + browser',async({page})=>{
 await page.goto('/register?lang=en');await page.getByLabel('Email',{exact:true}).fill('browser@example.test');await page.getByRole('checkbox',{name:/terms of use/}).check();await page.getByRole('checkbox',{name:/privacy policy/}).check();await page.getByRole('button',{name:'Continue',exact:true}).click();await expect(page.getByText('If the address is available, we will send an email.')).toBeVisible();
 const file=process.env.TEST_MAIL_FILE!;await expect.poll(()=>existsSync(file)).toBe(true);const {token}=JSON.parse(readFileSync(file,'utf8'));
 await page.goto('/verify-email?lang=en#token='+token);expect(new URL(page.url()).hash).toBe('');await page.getByLabel('Password',{exact:true}).fill('browser fixture password with ✨');await page.getByRole('button',{name:'Verify email',exact:true}).click();await expect(page.getByText('Email verified. You can now sign in.')).toBeVisible();
 await page.getByRole('link',{name:'Sign in',exact:true}).first().click();await page.getByLabel('Email',{exact:true}).fill('browser@example.test');await page.getByLabel('Password',{exact:true}).fill('browser fixture password with ✨');await page.getByRole('button',{name:'Sign in',exact:true}).click();await expect(page).toHaveURL(/cabinet/);
 await page.getByRole('button',{name:'Request trial',exact:true}).click();await expect(page.getByText('Request sent. Waiting for support')).toBeVisible();const response=await page.request.get('/api/v1/trial-requests/current');const current:components['schemas']['CurrentTrialRequest']=await response.json();
 expect(response.status()).toBe(200);expect(current.request?.request_id).toMatch(/^[0-9a-f-]{36}$/);
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
  const activate=page.getByRole('button',{name:'Activate trial',exact:true});await expect(activate).toBeVisible();await expect(page.getByRole('textbox',{name:'Comment for support',exact:true})).toHaveCount(0);await expect(page.getByRole('textbox',{name:'Promocode',exact:true})).toBeVisible();
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
  const {init_data,control_url,recurring}=JSON.parse(readFileSync(process.env.TEST_STARS_INIT_FILE!,'utf8'));
  await page.route('https://telegram.org/js/telegram-web-app.js',r=>r.fulfill({contentType:'application/javascript',body:`window.__ownedStars={opened:[],done:null};window.Telegram={WebApp:{initData:${JSON.stringify(init_data)},version:'9.6',platform:'web',themeParams:{},ready(){},expand(){},onEvent(){},offEvent(){},BackButton:{show(){},hide(){},onClick(){},offClick(){}},openInvoice(url,done){window.__ownedStars.opened.push(url);window.__ownedStars.done=done;}}};`}));
  const keys:string[]=[];page.on('request',r=>{if(new URL(r.url()).pathname==='/api/v1/orders')keys.push(r.headers()['idempotency-key']);});
  await page.goto('/mini-app/catalogue?lang=en');checkpoint='consent';await page.getByRole('checkbox',{name:/terms of use/i}).check();await page.getByRole('checkbox',{name:/privacy/i}).check();await page.getByRole('button',{name:'Continue',exact:true}).click();checkpoint='order';
  await page.getByRole('button',{name:'Select plan'}).click();const automatic=page.getByRole('checkbox',{name:'Automatically renew every 30 days'});await expect(automatic).not.toBeChecked();if(recurring){await automatic.focus();await page.keyboard.press('Space');await expect(automatic).toBeChecked();}await page.getByRole('button',{name:'Buy plan'}).click();await page.getByRole('button',{name:'Confirm purchase'}).click();
  await expect(page).toHaveURL(/\/mini-app\/orders\/[0-9a-f-]{36}/);const orderPath=new URL(page.url()).pathname;
  checkpoint='invoice';const pay=page.getByRole('button',{name:'Pay with Stars'});await pay.focus();await page.keyboard.press('Enter');
  await expect.poll(()=>page.evaluate(()=>(window as any).__ownedStars.opened)).toEqual(['https://t.me/$Owned_native_invoice']);
  await page.evaluate(()=>(window as any).__ownedStars.done('paid'));await expect(page.getByText('Waiting for payment confirmation.',{exact:true})).toBeVisible();
  checkpoint='money';expect((await page.request.post(control_url+'/complete')).status()).toBe(204);checkpoint='provision';
  await expect(page.getByText('Payment received. Access is ready.',{exact:true})).toBeVisible({timeout:20000});expect(keys).toHaveLength(1);expect(keys[0]).toMatch(/^[0-9a-f-]{36}$/);
  checkpoint='replay';await expect(pay).toHaveCount(0);await page.getByRole('button',{name:'Refresh status'}).click();await expect(page.getByText('Payment received. Access is ready.',{exact:true})).toBeVisible();expect(new URL(page.url()).pathname).toBe(orderPath);
  if(recurring){checkpoint='cancel';const region=page.getByRole('region',{name:'Stars auto-renewal'});await expect(region.getByText('Auto-renewal is active.',{exact:true})).toBeVisible();await region.getByRole('button',{name:'Cancel auto-renewal',exact:true}).click();await region.getByRole('button',{name:'Confirm cancellation'}).click();await expect(region.getByText('Telegram has confirmed cancellation. Paid access is retained.',{exact:true})).toBeVisible();checkpoint='resume';await region.getByRole('button',{name:'Allow renewal in Telegram',exact:true}).click();await region.getByRole('button',{name:'Confirm renewal'}).click();await expect(region.getByText('You can enable renewal in Telegram. No new payment is confirmed.',{exact:true})).toBeVisible();await expect(page.getByText('Payment received. Access is ready.',{exact:true})).toBeVisible();}
  expect(await page.evaluate(()=>JSON.stringify(localStorage)+JSON.stringify(sessionStorage))).not.toMatch(/mini_|init_data|tgWebAppData|csrf_token|subscription_url/);
  }finally{process.stdout.write('TG_STARS_CHECKPOINT:'+checkpoint+'\n');}
 });
}

if(process.env.TEST_BROWSER_PAYMENTS_FILE){
 test('Telegram browser payments real independent login and pending YooMoney',async({page,browser})=>{
  let checkpoint='navigation';
  const input=JSON.parse(readFileSync(process.env.TEST_BROWSER_PAYMENTS_FILE!,'utf8'));
  const external=await browser.newContext({ignoreHTTPSErrors:true});
  try{
   await page.context().addCookies([input.other_cookie]);await external.addCookies([input.other_cookie]);
   await page.route('https://telegram.org/js/telegram-web-app.js',r=>r.fulfill({contentType:'application/javascript',body:`window.__ownedBrowserPayments={opened:[]};window.Telegram={WebApp:{initData:${JSON.stringify(input.init_data)},ready(){},expand(){},onEvent(){},offEvent(){},BackButton:{show(){},hide(){},onClick(){},offClick(){}},openLink(url){window.__ownedBrowserPayments.opened.push(url)}}};`}));
   await page.goto('/mini-app/cabinet?lang=en');checkpoint='consent';
   await page.getByRole('checkbox',{name:/terms of use/i}).check();await page.getByRole('checkbox',{name:/privacy/i}).check();
   const session=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/telegram/mini-app/session'&&r.status()===200);
   await page.getByRole('button',{name:'Continue',exact:true}).click();const auth=await(await session).json();
   expect(auth.account.account_id).not.toBe(input.other_account_id);checkpoint='trial';
   await page.getByRole('button',{name:'Activate trial',exact:true}).click();await expect(page.getByText('Trial active',{exact:true})).toBeVisible({timeout:20000});
   const miniSubscription=await page.evaluate(async token=>{const r=await fetch('/api/v1/subscription',{credentials:'omit',headers:{Authorization:'Bearer '+token}});return{status:r.status,body:await r.json()};},auth.session_token);
   expect(miniSubscription.status).toBe(200);checkpoint='public_navigation';
   const action=page.getByRole('button',{name:'Other payment methods',exact:true});await action.focus();await page.keyboard.press('Enter');
   const opened=await page.evaluate(()=>(window as any).__ownedBrowserPayments.opened);
   expect(opened).toEqual([new URL(page.url()).origin+'/login?lang=en']);
   const web=await external.newPage();let sdkLoads=0;
   await web.route('https://telegram.org/**',async r=>{sdkLoads++;await r.abort()});
   await web.goto(opened[0]);await expect(web.getByLabel('Email',{exact:true})).toBeVisible();
   expect((await(await web.request.get(new URL('/api/v1/me',opened[0]).href)).json()).account.account_id).toBe(input.other_account_id);
   await page.getByRole('region',{name:'Other payment methods'}).getByRole('link',{name:'Set up email sign-in',exact:true}).click();checkpoint='email';
   await expect(page.getByRole('heading',{name:'Sign-in methods',exact:true})).toBeVisible();await page.getByLabel('Email',{exact:true}).fill(input.email);
   const challenge=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/telegram/initial-email'&&r.request().method()==='POST');
   await page.getByRole('button',{name:'Send confirmation code',exact:true}).click();const sent=await challenge;expect(sent.status()).toBe(202);
   const code=await page.request.post(input.control_url+'/code',{data:{challenge_id:(await sent.json()).challenge_id}});expect(code.status()).toBe(200);
   await page.getByLabel('Confirmation code',{exact:true}).fill((await code.json()).code);
   await page.getByLabel('New password',{exact:true}).fill(input.password);await page.getByLabel(/Repeat.*password/i).fill(input.password);
   await page.getByRole('checkbox',{name:/terms of use/i}).check();await page.getByRole('checkbox',{name:/privacy/i}).check();checkpoint='identity';
   await page.getByRole('button',{name:'Enable email sign-in',exact:true}).click();await expect(page.getByRole('alert')).toContainText('Email sign-in enabled');
   await expect(page.getByRole('button',{name:'Other payment methods',exact:true})).toHaveCount(0);
   expect(await page.evaluate(async token=>(await fetch('/api/v1/telegram/mini-app/account',{credentials:'omit',headers:{Authorization:'Bearer '+token}})).status,auth.session_token)).toBe(401);
   await page.getByRole('link',{name:'Open cabinet in browser',exact:true}).click();
   expect(await page.evaluate(()=>(window as any).__ownedBrowserPayments.opened)).toEqual([opened[0],opened[0]]);
   expect((await page.context().cookies()).find(c=>c.name==='__Host-session')?.value).toBe(input.other_cookie.value);
   expect(await page.evaluate(()=>JSON.stringify(localStorage)+JSON.stringify(sessionStorage))).not.toMatch(/mini_|init_data|tgWebAppData|csrf_token|subscription_url/);
   checkpoint='browser_login';await web.getByLabel('Email',{exact:true}).fill(input.email);await web.getByLabel('Password',{exact:true}).fill(input.password);
   await web.getByRole('button',{name:'Sign in',exact:true}).click();await expect(web.getByRole('heading',{name:input.email,exact:true})).toBeVisible();
   const origin=new URL(web.url()).origin;const me=await(await web.request.get(origin+'/api/v1/me')).json();expect(me.account.account_id).toBe(auth.account.account_id);
   const subscription=await(await web.request.get(origin+'/api/v1/subscription')).json();
   for(const key of ['status','devices','traffic_limit_bytes','expires_at','access_profile','access_operation_id'])expect(subscription[key]).toEqual(miniSubscription.body[key]);
   expect(sdkLoads).toBe(0);checkpoint='checkout';
   await web.getByRole('link',{name:'Plans',exact:true}).click();await web.getByRole('button',{name:'Select plan',exact:true}).click();await web.getByRole('radio',{name:'Wallet',exact:true}).check();
   await expect(web.getByRole('radio',{name:'Telegram Stars',exact:true})).toHaveCount(0);
   await web.getByRole('button',{name:'Buy plan',exact:true}).click();await web.getByRole('button',{name:'Confirm purchase',exact:true}).click();
   await expect(web).toHaveURL(/\/orders\/[0-9a-f-]{36}/);const orderPath=new URL(web.url()).pathname;expect(orderPath).toMatch(/^\/orders\/[0-9a-f-]{36}$/);
   let submitted=false;
   await web.route('https://yoomoney.ru/**',async r=>{
    const fields=new URLSearchParams(r.request().postData()??'');
    expect(r.request().method()).toBe('POST');expect(new URL(r.request().url()).pathname).toBe('/quickpay/confirm');
    expect(fields.get('receiver')).toBe('410000000000000');expect(fields.get('paymentType')).toBe('PC');expect(fields.get('sum')).toBe('123.45');
    expect(fields.get('label')).toBe(orderPath.split('/').at(-1));submitted=true;
    await r.fulfill({status:303,headers:{location:fields.get('successURL')!},body:''});
   });
   const returned=web.waitForURL(url=>url.origin===origin&&url.pathname===orderPath&&submitted);
   await web.getByRole('button',{name:'Continue to payment',exact:true}).click();await returned;checkpoint='provider_return';
   const order=await(await web.request.get(origin+'/api/v1'+orderPath)).json();expect(order.payment_method).toBe('yoomoney');expect(order.payment_status).toBe('pending');expect(order.fulfillment_status).toBe('not_started');expect(order.access_operation_id).toBeNull();
   const after=await(await web.request.get(origin+'/api/v1/subscription')).json();
   for(const key of ['status','devices','traffic_limit_bytes','expires_at','access_profile','access_operation_id'])expect(after[key]).toEqual(miniSubscription.body[key]);
  }finally{await external.close();process.stdout.write('TG_BROWSER_CHECKPOINT:'+checkpoint+'\n');}
 });
}
