import {readFileSync} from 'node:fs';
import {chromium,expect} from '@playwright/test';

let browser,checkpoint='fixture';
try{
 const origin=process.env.TEST_ORIGIN;
 const fixture=JSON.parse(readFileSync(process.env.TEST_NATIVE_PROMO_ACTIVATION_FILE,'utf8'));
 if(!origin?.startsWith('https://')||!fixture.client_cookie||!fixture.client_csrf||!fixture.code||!Number.isInteger(fixture.days))throw new Error('fixture unavailable');
 browser=await chromium.launch();
 const context=await browser.newContext({ignoreHTTPSErrors:true});
 await context.addCookies([{name:'__Host-session',value:fixture.client_cookie,url:origin,httpOnly:true,secure:true,sameSite:'Lax'}]);
 const page=await context.newPage();
 checkpoint='Russian empty and keyboard';
 await page.goto(origin+'/cabinet?lang=ru');
 const form=page.getByRole('region',{name:'Активировать промокод'});
 await expect(form.getByText('Пока нет промокода? Обратитесь в поддержку.')).toBeVisible();
 const input=form.getByLabel('Промокод');await input.fill(fixture.code);await input.focus();
 await page.keyboard.press('Tab');await expect(form.getByRole('button',{name:'Активировать'})).toBeFocused();
 checkpoint='activation';
 const post=page.waitForRequest(request=>new URL(request.url()).pathname==='/api/v1/promocodes/activate'&&request.method()==='POST');
 await page.keyboard.press('Enter');
 const request=await post;
 expect(request.headers()['x-csrf-token']).toBe(fixture.client_csrf);
 expect(request.headers()['idempotency-key']).toMatch(/^[0-9a-f-]{36}$/i);
 expect(request.postDataJSON()).toEqual({code:fixture.code.trim()});
 await expect(form.getByRole('status')).toContainText('Дней: '+fixture.days);
 await expect(input).toHaveValue('');
 expect(page.url()).not.toContain(fixture.code);
 expect(await page.evaluate(()=>JSON.stringify(localStorage)+JSON.stringify(sessionStorage))).not.toContain(fixture.code);
 checkpoint='English and fresh status';
 await page.getByRole('button',{name:'EN',exact:true}).click();
 const english=page.getByRole('region',{name:'Activate promocode'});
 await expect(english.getByRole('status')).toContainText('Days: '+fixture.days);
 const get=page.waitForResponse(response=>new URL(response.url()).pathname.startsWith('/api/v1/promocodes/activations/'));
 await english.getByRole('button',{name:'Refresh status'}).click();
 expect((await get).status()).toBe(200);
 await expect(english.getByRole('status')).toContainText('Days: '+fixture.days);
 console.log('PASS: native client promocode activation, keyboard, RU/EN, status');
}catch{console.error('FAIL: native client promocode checkpoint '+checkpoint);process.exitCode=1;}finally{await browser?.close();}
