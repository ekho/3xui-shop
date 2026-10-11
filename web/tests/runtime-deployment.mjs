import assert from 'node:assert/strict';
import {chromium} from '@playwright/test';

const origin=process.env.TEST_ORIGIN;
assert.match(origin??'',/^https:\/\/cabinet\.example\.test:[0-9]{1,5}$/,'Only the owned loopback Caddy fixture is allowed');
const browser=await chromium.launch({args:['--host-resolver-rules=MAP cabinet.example.test 127.0.0.1']});
try{
 for(const lang of ['ru','en']){
  const context=await browser.newContext({ignoreHTTPSErrors:true});
  try{
   const page=await context.newPage();
   await page.goto(origin+'/register?lang='+lang);
   const response=await page.evaluate(async()=>{const result=await fetch('/config.json',{cache:'no-store'});return {status:result.status,cache:result.headers.get('cache-control'),config:await result.json()};});
   assert.equal(response.status,200);
   assert.equal(response.cache,'no-store');
   const config=response.config;
   assert.deepEqual(Object.keys(config).sort(),['productName','termsVersion','privacyVersion','termsURL','privacyURL','supportURL'].sort());
   assert.equal(config.productName,process.env.EXPECTED_PRODUCT_NAME);
   const cabinet=lang==='ru'?'Кабинет':'Account';
   const brand=page.getByRole('banner').getByRole('link',{name:config.productName+' · '+cabinet});
   await brand.waitFor();
   assert.equal(await page.title(),cabinet+' · '+config.productName);
   assert.equal(await brand.locator('span *').count(),0);
   assert.equal(await brand.locator('span').textContent(),config.productName+' · '+cabinet);
   await page.keyboard.press('Tab');
   assert.equal(await brand.evaluate(element=>document.activeElement===element),true);
   assert.equal(await page.getByRole('link',{name:lang==='ru'?'Условия':'Terms'}).first().getAttribute('href'),config.termsURL);
   assert.equal(await page.getByRole('link',{name:lang==='ru'?'Связаться с поддержкой':'Contact support'}).getAttribute('href'),config.supportURL);
   assert.equal(await page.getByRole('alert').count(),0);
  }finally{await context.close();}
 }
 console.log('PASS: real Caddy runtime config rendered in ru/en with plain branding, documents and keyboard access');
}finally{await browser.close();}
