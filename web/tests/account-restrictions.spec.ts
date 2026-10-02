import {test,expect,type Page,type Route} from '@playwright/test';

const operatorId='10000000-0000-4000-8000-000000000001';
const clientId='20000000-0000-4000-8000-000000000002';
const otherId='20000000-0000-4000-8000-000000000003';
const client={account_id:clientId,kind:'telegram',display_name:'Exact Person',email:null,telegram_id:'9223372036854775807',locale:'ru',created_at:null,restricted:false,vpn_banned:false,had_subscription:false};
const snapshot={source_legacy_user_id:'9223372036854775807',source_tg_id:'9223372036854775807',status:'rejected',requested_at:null,decided_at:null,decided_by:null};
const event={source_id:'9223372036854775806',target_tg_id:'9223372036854775807',created_at:'2026-10-02T08:00:00Z',action:'approval.reject',actor_type:null,actor_id:null,actor_name:null,source:null};
const card=(account=client)=>({client:account,subscription:{status:'none',expires_at:null,devices:0,traffic_limit_bytes:0,traffic_used_bytes:0,observed_at:null,data_stale:false,access_profile:'unknown',vpn_banned:account.vpn_banned,access_operation_id:null,access_operation_status:null},server:null,support:null,trial_requests:[],trial_has_more:false,audit_events:[],audit_has_more:false,legacy_approval:snapshot,legacy_events:[event],legacy_has_more:true});

async function routes(page:Page,extra?:(route:Route,path:string)=>Promise<boolean>){
 await page.route('**/api/v1/**',async route=>{const path=new URL(route.request().url()).pathname;
  if(extra&&await extra(route,path))return;
  if(path.endsWith('/operator/session'))return route.fulfill({json:{account:{account_id:operatorId,email:'operator@example.test',email_verified:true,locale:'en',telegram_linked:false},csrf_token:'s'.repeat(43)}});
  if(path.endsWith('/operator/clients/'+clientId))return route.fulfill({json:card()});
  if(path.endsWith('/operator/clients/'+otherId))return route.fulfill({json:card({...client,account_id:otherId,display_name:'Other Person'})});
  if(path.endsWith('/support'))return route.fulfill({json:{conversation:null,messages:[],has_more:false,oldest_sequence:null}});
  return route.fulfill({json:{}});
 });
}

test('restriction requires a reason and confirmation, keeps one key on retry, and clears a revealed link on success',async({page})=>{
 const bodies:unknown[]=[],keys:string[]=[];let calls=0,restricted=false,release!:()=>void;
 const pending=new Promise<void>(resolve=>release=resolve);
 await routes(page,async(route,path)=>{
  if(path.endsWith('/operator/clients/'+clientId)){await route.fulfill({json:card({...client,restricted})});return true;}
  if(path.endsWith('/key')){await route.fulfill({json:{subscription_url:'https://subscriptions.example.test/private'}});return true;}
  if(path.endsWith('/restriction')){calls++;bodies.push(route.request().postDataJSON());keys.push(route.request().headers()['idempotency-key']);if(calls===1){await route.abort();return true;}await pending;restricted=(route.request().postDataJSON() as {restricted:boolean}).restricted;await route.fulfill({json:{restricted,changed_at:'2026-10-02T09:00:00Z',operator_account_id:operatorId}});return true;}
  return false;
 });
 await page.setViewportSize({width:375,height:812});await page.goto('/admin/clients/'+clientId+'/show?lang=en');
 await page.getByRole('button',{name:'Show subscription link'}).click();await expect(page.getByLabel('Subscription link')).toBeVisible();
 const reason=page.getByLabel('Account restriction reason');await page.getByRole('button',{name:'Restrict account'}).click();expect(calls).toBe(0);
 await reason.fill('Operator review');await page.getByRole('button',{name:'Restrict account'}).click();await expect(page.getByText(/sessions|access/i).last()).toBeVisible();expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);expect(calls).toBe(0);
 await page.getByRole('button',{name:'Confirm restriction'}).click();await expect(page.getByRole('alert')).toBeVisible();await expect(reason).toHaveValue('Operator review');await expect(reason).toBeFocused();
 await page.getByRole('button',{name:'Restrict account'}).click();await page.getByRole('button',{name:'Confirm restriction'}).click();await expect.poll(()=>calls).toBe(2);
 try{await expect(reason).toBeDisabled();await expect(page.getByRole('button',{name:'Confirm restriction'})).toBeDisabled();}finally{release();}
 await expect(page.getByText('Account restricted')).toBeVisible();await expect(page.getByLabel('Subscription link')).toHaveCount(0);
 expect(bodies).toEqual([{restricted:true,reason:'Operator review'},{restricted:true,reason:'Operator review'}]);expect(keys[0]).toBe(keys[1]);expect(keys[0]).toMatch(/^[0-9a-f-]{36}$/i);
});

test('unrestricting is explicit, keeps VPN status separate, and works by keyboard at 375px in Russian',async({page})=>{
 let changed=false;const bodies:unknown[]=[];await page.setViewportSize({width:375,height:812});
 await routes(page,async(route,path)=>{if(path.endsWith('/operator/clients/'+clientId)){await route.fulfill({json:card({...client,restricted:!changed,vpn_banned:true})});return true;}if(path.endsWith('/restriction')){bodies.push(route.request().postDataJSON());changed=true;await route.fulfill({json:{restricted:false,changed_at:'2026-10-02T09:00:00Z',operator_account_id:operatorId}});return true;}return false;});
 await page.goto('/admin/clients/'+clientId+'/show?lang=ru');await expect(page.getByText('VPN заблокирован')).toBeVisible();
 const reason=page.getByLabel('Причина ограничения аккаунта');await reason.focus();await expect(reason).toBeFocused();await reason.fill('Проверка завершена');
 await page.getByRole('button',{name:'Снять ограничение аккаунта'}).focus();await page.keyboard.press('Enter');
 await expect(page.getByText('Аккаунт без ограничений')).toBeVisible();await expect(page.getByText('VPN заблокирован')).toBeVisible();expect(bodies).toEqual([{restricted:false,reason:'Проверка завершена'}]);
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});

test('legacy snapshot renders unknown dates and actors, and pages using decimal source ID',async({page})=>{
 const inputs:unknown[]=[];await routes(page,async(route,path)=>{if(path.endsWith('/history')){inputs.push(route.request().postDataJSON());await route.fulfill({json:{kind:'legacy',legacy_events:[{...event,source_id:'9223372036854775805',actor_type:'telegram',actor_id:'123456789',actor_name:'Old operator'}],has_more:false}});return true;}return false;});
 await page.goto('/admin/clients/'+clientId+'/show?lang=en');const history=page.getByRole('region',{name:'Legacy registration history'});
 await expect(history.getByText('rejected',{exact:true})).toBeVisible();await expect(history.getByText('Unknown',{exact:true}).first()).toBeVisible();await expect(history.getByText(event.source_id,{exact:true})).toBeVisible();
 await expect(history.getByRole('button',{name:/Approve|Reject/})).toHaveCount(0);await history.getByRole('button',{name:'Load older registration events'}).click();
 expect(inputs).toEqual([{kind:'legacy',before_created_at:event.created_at,before_source_id:event.source_id}]);await expect(history.getByText('telegram · Old operator · 123456789')).toBeVisible();await expect(history.getByRole('button',{name:'Load older registration events'})).toHaveCount(0);
});

test('late legacy page from one client cannot appear on another card',async({page})=>{
 let release!:()=>void;const pending=new Promise<void>(resolve=>release=resolve);
 await routes(page,async(route,path)=>{if(path.endsWith('/operator/clients/'+otherId)){await route.fulfill({json:card({...client,account_id:otherId,display_name:'Other Person',restricted:true})});return true;}if(path.endsWith('/history')){await pending;await route.fulfill({json:{kind:'legacy',legacy_events:[{...event,source_id:'1',actor_name:'STALE OLD CLIENT'}],has_more:false}}).catch(()=>{});return true;}return false;});
 await page.goto('/admin/clients/'+clientId+'/show?lang=en');await page.getByRole('button',{name:'Load older registration events'}).click();
 await page.evaluate(id=>{history.pushState({},'',`/admin/clients/${id}/show?lang=en`);dispatchEvent(new PopStateEvent('popstate'));},otherId);
 await expect(page.getByRole('heading',{name:'Other Person'})).toBeVisible();release();await expect(page.getByText('STALE OLD CLIENT')).toHaveCount(0);
});

test('late restriction success cannot clear a different client link after in-app navigation',async({page})=>{
 let release!:()=>void,received=0;const pending=new Promise<void>(resolve=>release=resolve);
 await routes(page,async(route,path)=>{if(path.endsWith('/restriction')){received++;await pending;await route.fulfill({json:{restricted:true,changed_at:'2026-10-02T09:00:00Z',operator_account_id:operatorId}}).catch(()=>{});return true;}if(path.endsWith('/key')){await route.fulfill({json:{subscription_url:'https://subscriptions.example.test/'+(path.includes(otherId)?'other':'first')}});return true;}return false;});
 await page.goto('/admin/clients/'+clientId+'/show?lang=en');await page.getByLabel('Account restriction reason').fill('Old client');await page.getByRole('button',{name:'Restrict account'}).click();await page.getByRole('button',{name:'Confirm restriction'}).click();await expect.poll(()=>received).toBe(1);
 await page.evaluate(id=>{history.pushState({},'',`/admin/clients/${id}/show?lang=en`);dispatchEvent(new PopStateEvent('popstate'));},otherId);
 await expect(page.getByRole('heading',{name:'Other Person'})).toBeVisible();await page.getByRole('button',{name:'Show subscription link'}).click();await expect(page.getByLabel('Subscription link')).toHaveValue('https://subscriptions.example.test/other');
 release();await page.waitForTimeout(100);await expect(page.getByLabel('Subscription link')).toHaveValue('https://subscriptions.example.test/other');
});

test('protected operator rejection preserves the valid operator session, reason and account state',async({page})=>{
 let calls=0;await routes(page,async(route,path)=>{if(path.endsWith('/restriction')){calls++;await route.fulfill({status:403,json:{error:{code:'OPERATOR_ACCOUNT_PROTECTED'}}});return true;}return false;});
 await page.goto('/admin/clients/'+clientId+'/show?lang=en');const reason=page.getByLabel('Account restriction reason');await reason.fill('Wrong target');
 await page.getByRole('button',{name:'Restrict account'}).click();await page.getByRole('button',{name:'Confirm restriction'}).click();
 await expect(page.getByRole('alert')).toContainText('active operator');await expect(reason).toHaveValue('Wrong target');await expect(page.getByText('Account unrestricted')).toBeVisible();await expect(page.getByRole('heading',{name:'Operator access required'})).toHaveCount(0);expect(calls).toBe(1);
});

test('a thousand Unicode characters are accepted as a reason without UTF-16 truncation',async({page})=>{
 const bodies:{restricted:boolean;reason:string}[]=[];await routes(page,async(route,path)=>{if(path.endsWith('/restriction')){bodies.push(route.request().postDataJSON());await route.fulfill({json:{restricted:true,changed_at:'2026-10-02T09:00:00Z',operator_account_id:operatorId}});return true;}return false;});
 await page.goto('/admin/clients/'+clientId+'/show?lang=en');const reason='🟢'.repeat(1000);await page.getByLabel('Account restriction reason').fill(reason);
 await page.getByRole('button',{name:'Restrict account'}).click();await page.getByRole('button',{name:'Confirm restriction'}).click();
 await expect.poll(()=>bodies.length).toBe(1);expect(bodies[0]).toEqual({restricted:true,reason});
});
