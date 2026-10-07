import {test,expect,type Page,type Route} from '@playwright/test';
const source='9007199254740993';
const created='2026-10-07T10:00:00Z';
const row=(id:string)=>({source_id:id,created_at:created,updated_at:created,payment_status:'completed',fulfillment_status:'unknown',payment_method:null,quote:null});
const history=(rows:string[])=>({kind:'legacy',orders:[],receipts:[],legacy_transactions:rows.map(row),has_more:false});
async function setup(page:Page,extra?:(route:Route)=>Promise<boolean>){
 const calls:Record<string,unknown>[]=[];
 await page.route('**/api/v1/**',async route=>{
  const path=new URL(route.request().url()).pathname;
  if(path.endsWith('/auth/session'))return route.fulfill({json:{csrf_token:'s'.repeat(43)}});
  if(path.endsWith('/payment-history')){
   const input=route.request().postDataJSON();calls.push(input);
   if(extra&&await extra(route))return;
   return route.fulfill({json:input.kind==='legacy'?history(input.legacy_source_id?[String(input.legacy_source_id)]:[source,'11']):{...history([]),kind:input.kind}});
  }
  return route.fulfill({json:{}});
 });return calls;
}
for(const lang of ['ru','en'] as const)test(`legacy link keeps exact record, keyboard and full archive ${lang}`,async({page})=>{
 const calls=await setup(page);await page.setViewportSize({width:375,height:812});await page.goto('/cabinet/history?kind=legacy&legacy_source_id='+source+'&lang='+lang);
 await expect(page.locator('.payment-history article')).toHaveCount(1);await expect(page.getByLabel(lang==='ru'?'Вид истории':'History type')).toHaveValue('legacy');
 expect(calls[0]).toEqual({kind:'legacy',legacy_source_id:source});await expect(page.locator('.payment-history article').getByText(source,{exact:true})).toBeVisible();
 const full=page.getByRole('button',{name:lang==='ru'?'Вся архивная история':'Full archive history',exact:true});await full.focus();await expect(full).toBeFocused();await page.keyboard.press('Enter');
 await expect(page.locator('.payment-history article')).toHaveCount(2);expect(calls.at(-1)).toEqual({kind:'legacy'});expect(new URL(page.url()).searchParams.has('legacy_source_id')).toBe(false);
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await expect(page.locator('.payment-history article').getByRole('button')).toHaveCount(0);
});
test('unknown archive link offers empty state and full history',async({page})=>{
 const calls=await setup(page,async route=>{if(!route.request().postDataJSON().legacy_source_id)return false;await route.fulfill({json:history([])});return true;});await page.goto('/cabinet/history?kind=legacy&legacy_source_id=999&lang=en');
 await expect(page.getByRole('status')).toContainText('No records of this type.');await page.getByRole('button',{name:'Full archive history',exact:true}).click();await expect(page.locator('.payment-history article')).toHaveCount(2);expect(calls.at(-1)).toEqual({kind:'legacy'});
});
test('retry retains exact archive reference and switching kind removes it',async({page})=>{
 let attempts=0;const calls=await setup(page,async route=>{if(++attempts!==1)return false;await route.fulfill({status:503,json:{error:{code:'SERVICE_UNAVAILABLE'}}});return true;});await page.goto('/cabinet/history?kind=legacy&legacy_source_id='+source+'&lang=en');
 await expect(page.getByRole('alert')).toBeVisible();await page.getByRole('button',{name:'Retry',exact:true}).click();await expect(page.locator('.payment-history article')).toHaveCount(1);expect(calls.slice(0,2)).toEqual([{kind:'legacy',legacy_source_id:source},{kind:'legacy',legacy_source_id:source}]);
 await page.getByLabel('History type').selectOption('orders');await expect.poll(()=>calls.at(-1)?.kind).toBe('orders');expect(calls.at(-1)).toEqual({kind:'orders'});
 await page.getByLabel('History type').selectOption('legacy');await expect(page.locator('.payment-history article')).toHaveCount(2);expect(calls.at(-1)).toEqual({kind:'legacy'});
});
for(const status of [401,403])test(`archive access denial clears shown record ${status}`,async({page})=>{
 let denied=false;await setup(page,async route=>{if(!denied)return false;await route.fulfill({status,json:{error:{code:status===403?'ACCOUNT_RESTRICTED':'UNAUTHORIZED'}}});return true;});await page.goto('/cabinet/history?kind=legacy&legacy_source_id='+source+'&lang=en');await expect(page.locator('.payment-history article')).toHaveCount(1);
 denied=true;await page.getByRole('button',{name:'Refresh history',exact:true}).click();await expect(page.locator('.payment-history article')).toHaveCount(0);if(status===401)await expect(page).toHaveURL(/\/login/);else {await expect(page.getByRole('alert')).toBeVisible();await expect(page.getByRole('button',{name:'Full archive history',exact:true})).toBeDisabled();}
});
for(const bad of ['0','01','9223372036854775808','1e3'])test(`invalid archive reference falls back to full archive ${bad}`,async({page})=>{
 const calls=await setup(page);await page.goto('/cabinet/history?kind=legacy&legacy_source_id='+bad+'&lang=en');await expect(page.locator('.payment-history article')).toHaveCount(2);expect(calls[0]).toEqual({kind:'legacy'});
});
test('legacy platform link selects existing instructions without revealing VPN credentials',async({page})=>{
 let secretReads=0;
 await page.route('**/api/v1/**',async route=>{
  const path=new URL(route.request().url()).pathname;
  if(path.endsWith('/me'))return route.fulfill({json:{account:{account_id:'20000000-0000-4000-8000-000000000001',email:'owned@example.test',locale:'en'},capabilities:{trial_available:false},csrf_token:'s'.repeat(43)}});
  if(path.endsWith('/subscription/key')){secretReads++;return route.fulfill({status:403,json:{error:{code:'SUBSCRIPTION_UNAVAILABLE'}}});}
  if(path.endsWith('/subscription'))return route.fulfill({json:{status:'active',access_profile:'regular',devices:1,expires_at:'2099-01-01T00:00:00Z',traffic_limit_bytes:0,traffic_used_bytes:0,data_stale:false,observed_at:created,connection_available:true}});
  if(path.endsWith('/trial-requests/current'))return route.fulfill({json:{request:null}});
  if(path.endsWith('/orders/current'))return route.fulfill({json:{order:null}});
  return route.fulfill({json:{}});
 });
 await page.goto('/cabinet?lang=en&platform=macos#connection-title');await expect(page.getByLabel('Platform')).toHaveValue('macos');expect(secretReads).toBe(0);await expect(page.locator('#subscription-key')).toHaveCount(0);
});
