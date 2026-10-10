import {readFileSync} from 'node:fs';
import {randomUUID} from 'node:crypto';
import {chromium,expect} from '@playwright/test';

let checkpoint='fixture',browser;
try{
 const origin=process.env.TEST_ORIGIN;
 const fixture=JSON.parse(readFileSync(process.env.TEST_NATIVE_PROMOCODES_FILE,'utf8'));
 const phase=process.env.TEST_NATIVE_PROMOCODES_PHASE;
 if(!origin?.startsWith('https://')||!fixture.operator_cookie||!fixture.client_cookie||!fixture.operator_csrf||!['core','used'].includes(phase))throw new Error('fixture');
 browser=await chromium.launch();
 const context=async cookie=>{const value=await browser.newContext({ignoreHTTPSErrors:true});await value.addCookies([{name:'__Host-session',value:cookie,url:origin,httpOnly:true,secure:true,sameSite:'Lax'}]);return value;};
 const operator=await context(fixture.operator_cookie),admin=await operator.newPage();
 if(phase==='core'){
  checkpoint='empty and language';
  await admin.goto(origin+'/admin/promocodes?lang=en');
  await expect(admin.getByRole('heading',{name:'Promocodes',exact:true})).toBeVisible();
  await expect(admin.getByText('No promocodes yet.',{exact:true})).toBeVisible();
  await admin.getByRole('button',{name:'RU',exact:true}).click();
  await expect(admin.getByRole('heading',{name:'Промокоды',exact:true})).toBeVisible();
  await admin.getByRole('button',{name:'Новый промокод'}).focus();await admin.keyboard.press('Enter');
  await expect(admin.getByLabel('Срок в днях')).toBeVisible();
  await admin.getByRole('button',{name:'EN',exact:true}).click();

  checkpoint='create and history';
  await admin.getByRole('button',{name:'Create promocode'}).click();
  await expect(admin.getByRole('alert')).toBeFocused();
  await admin.getByLabel('Duration in days').fill('30');
  await admin.getByLabel('Reason').fill('Owned browser creation');
  await admin.getByRole('button',{name:'Create promocode'}).click();
  const card=admin.getByRole('region',{name:'Promocode details'});
  await expect(card.getByText('Revision 1',{exact:true})).toBeVisible();
  await expect(card.getByText('Owned browser creation')).toBeVisible();
  const search=await admin.request.post(origin+'/api/v1/operator/promocodes/search',{headers:{Origin:origin,'X-CSRF-Token':fixture.operator_csrf},data:{page:1,per_page:50}});
  expect(search.status()).toBe(200);
  const list=await search.json();expect(list.promocodes).toHaveLength(1);
  const id=list.promocodes[0].promocode_id;

  checkpoint='edit and stale refresh';
  await admin.getByLabel('Duration in days').fill('45');await admin.getByLabel('Reason').fill('Owned browser edit');
  await admin.getByRole('button',{name:'Save duration'}).focus();await admin.keyboard.press('Enter');
  await expect(card.getByText('Revision 2',{exact:true})).toBeVisible();
  const external=await admin.request.post(origin+'/api/v1/operator/promocodes/'+encodeURIComponent(id)+'/edit',{headers:{Origin:origin,'X-CSRF-Token':fixture.operator_csrf,'Idempotency-Key':randomUUID()},data:{duration_days:46,expected_revision:2,reason:'Owned concurrent edit'}});
  expect(external.status()).toBe(200);
  await admin.getByLabel('Duration in days').fill('50');await admin.getByLabel('Reason').fill('Owned stale edit');
  await admin.getByRole('button',{name:'Save duration'}).click();
  await expect(admin.getByRole('alert').filter({hasText:'The promocode changed.'})).toBeFocused();
  await expect(admin.getByLabel('Reason')).toHaveValue('Owned stale edit');
  await admin.getByRole('button',{name:'Refresh promocode'}).click();
  await expect(card.getByText('Revision 3',{exact:true})).toBeVisible();
  await admin.getByRole('button',{name:'Save duration'}).click();
  await expect(card.getByText('Revision 4',{exact:true})).toBeVisible();

  checkpoint='confirmed deletion';
  await admin.getByLabel('Reason').fill('Owned browser deletion');
  await admin.getByRole('button',{name:'Delete promocode'}).click();
  await expect(admin.getByRole('button',{name:'Confirm deletion'})).toBeVisible();
  await admin.getByRole('button',{name:'Confirm deletion'}).click();
  await expect(card.getByText('Deleted',{exact:true})).toBeVisible();
  await expect(admin.getByRole('button',{name:'Save duration'})).toHaveCount(0);
  await expect(card.getByText('Owned browser deletion')).toBeVisible();

  checkpoint='ordinary client denied';
  const client=await context(fixture.client_cookie),denied=await client.newPage();
  await denied.goto(origin+'/admin/promocodes?lang=en');
  await expect(denied.getByRole('heading',{name:'Operator access required'})).toBeVisible();
  await expect(denied.getByRole('button',{name:'New promocode'})).toHaveCount(0);
 }else{
  checkpoint='used legacy view';
  await admin.goto(origin+'/admin/promocodes?lang=en');
  const row=admin.locator('article.admin-client').filter({hasText:fixture.used_code});
  checkpoint='used row open';
  await row.getByRole('button',{name:'Open promocode'}).click();
  const card=admin.getByRole('region',{name:'Promocode details'});
  checkpoint='used state';
  await expect(card.getByText('Activated',{exact:true})).toBeVisible();
  await expect(card.getByText(fixture.used_account_id,{exact:true})).toBeVisible();
  checkpoint='legacy bigint';
  await expect(card.getByText('9223372036854775807',{exact:true}).first()).toBeVisible();
  checkpoint='nullable activation timestamp';
  await expect(card.locator('dt').filter({hasText:'Activated at'}).locator('..').locator('dd')).toHaveText('No data');
  checkpoint='used actions hidden';
  await expect(admin.getByRole('button',{name:'Save duration'})).toHaveCount(0);
  await expect(admin.getByRole('button',{name:'Delete promocode'})).toHaveCount(0);
 }
 console.log('PASS: native promocodes '+phase);
}catch{console.error('FAIL: native promocodes checkpoint '+checkpoint);process.exitCode=1;}finally{await browser?.close();}
