import {test,expect} from '@playwright/test';

const config={termsVersion:'2026-a',privacyVersion:'2026-b',termsURL:'https://legal.example.test/terms',privacyURL:'https://legal.example.test/privacy',supportURL:'https://help.example.test/contact'};

test('loads deployment values before registration renders',async({page})=>{
 let accepted:unknown;let active={...config};
 await page.route('**/config.json',route=>route.fulfill({json:active}));
 await page.route('**/api/v1/auth/register',route=>{accepted=route.request().postDataJSON();return route.fulfill({status:202,json:{challenge_id:'fixture',resend_after:60}})});
 await page.goto('/register');
 await expect(page.getByRole('link',{name:'Условия'}).first()).toHaveAttribute('href',config.termsURL);
 await expect(page.getByRole('link',{name:'Связаться с поддержкой'})).toHaveAttribute('href',config.supportURL);
 await page.getByLabel('Email',{exact:true}).fill('client@example.test');
 await page.getByRole('checkbox').first().check();
 await page.getByRole('checkbox').last().check();
 await page.getByRole('button',{name:'Продолжить'}).click();
 expect(accepted).toMatchObject({accepted_terms_version:config.termsVersion,accepted_privacy_version:config.privacyVersion});
 active={...config,termsVersion:'2026-next',termsURL:'https://new.example.test/terms',supportURL:'mailto:new@example.test'};accepted=undefined;
 await page.reload();
 await expect(page.getByRole('link',{name:'Условия'}).first()).toHaveAttribute('href',active.termsURL);
 await expect(page.getByRole('link',{name:'Связаться с поддержкой'})).toHaveAttribute('href',active.supportURL);
 await page.getByLabel('Email',{exact:true}).fill('client@example.test');
 await page.getByRole('checkbox').first().check();await page.getByRole('checkbox').last().check();
 await page.getByRole('button',{name:'Продолжить'}).click();
 expect(accepted).toMatchObject({accepted_terms_version:active.termsVersion});
});

for(const [name,response] of [
 ['missing',{status:404}],
 ['malformed',{body:'{not json',contentType:'application/json'}],
 ['invalid',{json:{...config,termsURL:'javascript:alert(1)'}}],
 ['incomplete',{json:{...config,privacyVersion:''}}],
] as const){
 test(`${name} runtime config blocks registration`,async({page})=>{
  let registration=0;
  await page.route('**/config.json',route=>route.fulfill(response));
  await page.route('**/api/v1/auth/register',route=>{registration++;return route.fulfill({status:202,json:{}})});
  await page.goto('/register');
  await expect(page.getByRole('alert')).toContainText('Service is not configured');
  await expect(page.getByRole('button',{name:'Продолжить'})).toHaveCount(0);
  expect(registration).toBe(0);
 });
}
