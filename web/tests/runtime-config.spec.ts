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
 ['invalid-name',{json:{...config,productName:'invalid\nname'}}],
 ['oversized-name',{json:{...config,productName:'x'.repeat(129)}}],
 ['nontext-name',{json:{...config,productName:42}}],
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

for(const lang of ['ru','en'] as const){
 test(`deployment product name is plain accessible text (${lang})`,async({page})=>{
  let active={...config,productName:'<Fixture & Product>'};
  await page.route('**/config.json',route=>route.fulfill({json:active}));
  await page.goto('/login?lang='+lang);
  const cabinet=lang==='ru'?'Кабинет':'Account';
  await expect(page).toHaveTitle(cabinet+' · '+active.productName);
  const brand=page.getByRole('banner').getByRole('link',{name:active.productName+' · '+cabinet});
  await expect(brand).toBeVisible();
  await page.keyboard.press('Tab');await expect(brand).toBeFocused();
  expect(await brand.locator('span *').count()).toBe(0);
  await expect(brand.locator('span')).toHaveText(active.productName+' · '+cabinet);
  active={...active,productName:'Second deployment'};
  await page.reload();
  await expect(page).toHaveTitle(cabinet+' · '+active.productName);
 });
}
