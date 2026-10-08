import {test,expect,type Page,type Route} from '@playwright/test';
import type {components} from '../src/api/schema.gen';
type Model<K extends keyof components['schemas']>=components['schemas'][K];
const id='70000000-0000-4000-8000-000000000001',otherId='70000000-0000-4000-8000-000000000002';
const actor='10000000-0000-4000-8000-000000000001';
const session={account:{account_id:actor,email:'operator@example.test',email_verified:true,locale:'en',telegram_linked:false},csrf_token:'s'.repeat(43)};
const row:Model<'Campaign'>={campaign_id:id,name:'<Campaign>',code:'c_owned',state:'active',revision:1,web_visits:17,legacy_clicks:null,legacy_invite_id:null,created_at:'2026-10-08T10:00:00Z',source:'operator'};
const statistics:Model<'CampaignStatistics'>={users:4,web_registrations:2,telegram_registrations:1,legacy_name_users:1,legacy_trial_used:1,trials:{trial_users:1},payments:{paid_orders:6,paid_users:3,repeat_users:2,money:[{currency:'RUB',gross_minor:'18446744073709551614',known_net_minor:'18446744073709551614',unknown_net_receipts:0},{currency:'USD',gross_minor:'12345',known_net_minor:'0',unknown_net_receipts:1},{currency:'XTR',gross_minor:'300',known_net_minor:'0',unknown_net_receipts:3}],refunds:[{currency:'USDT',returned_amount:'0.000000000000000000000001'}],legacy:{completed_transactions:3,paid_users:1,repeat_users:1,unknown_quote_count:2,money:[{currency:'RUB',quoted_minor:'1025'}]}}};
const detail=(campaign=row):Model<'CampaignDetail'>=>({campaign,statistics,events:[{event_id:otherId,actor_account_id:actor,action:'create',created_at:'2026-10-08T10:00:00Z',reason:'Owned creation',before:null,after:campaign}],events_has_more:true});
async function routes(page:Page,extra?:(route:Route,path:string)=>Promise<boolean>){await page.route('**/api/v1/**',async route=>{
 const path=new URL(route.request().url()).pathname;if(extra&&await extra(route,path))return;
 if(path.endsWith('/operator/session')||path.endsWith('/auth/session'))return route.fulfill({json:session});
 if(path.endsWith('/operator/campaigns/search'))return route.fulfill({json:{campaigns:[row],page:1,per_page:50,total:1}});
 if(path==='/api/v1/operator/campaigns/'+id)return route.fulfill({json:detail()});
 if(path.endsWith('/campaign-visits'))return route.fulfill({status:204});
 return route.fulfill({json:{}});
});}

test('campaign card escapes names and shows exact native money, archive uncertainty and public link',async({page})=>{
 await page.addInitScript(()=>Object.defineProperty(navigator,'clipboard',{value:{writeText:async(value:string)=>{(window as unknown as {copied:string}).copied=value;}}}));
 await routes(page);await page.setViewportSize({width:375,height:812});await page.goto('/admin/campaigns?lang=en');
 await expect(page.getByRole('heading',{name:'Campaigns',exact:true})).toBeVisible();
 const open=page.getByRole('button',{name:'Open campaign',exact:true});await open.focus();await page.keyboard.press('Enter');
 const card=page.getByRole('region',{name:'Campaign details'});
 await expect(card.getByRole('heading',{name:'<Campaign>',exact:true})).toBeVisible();
 await expect(card.getByText('184,467,440,737,095,516.14 RUB',{exact:true}).first()).toBeVisible();
 await expect(card.getByText('0.000000000000000000000001 USDT',{exact:true})).toBeVisible();
 await expect(card.getByText('A legacy name does not prove the original invitation link.')).toBeVisible();
 await expect(card.getByText('Only the latest 20 changes are shown.')).toBeVisible();
 await card.getByRole('button',{name:'Copy registration link'}).click();
 expect(await page.evaluate(()=>(window as unknown as {copied:string}).copied)).toBe('http://127.0.0.1:4173/register?invite=c_owned&lang=en');
 expect(await page.locator('script').evaluateAll(nodes=>nodes.some(node=>node.textContent?.includes('<Campaign>')))).toBe(false);
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await page.getByRole('button',{name:'RU',exact:true}).click();await expect(page.getByRole('heading',{name:'Кампании',exact:true})).toBeVisible();await expect(page.getByRole('button',{name:'Приостановить кампанию'})).toBeVisible();
});

test('create validates inputs and retries a lost response with identical body, key and CSRF',async({page})=>{
 const writes:{body:unknown;key:string;csrf:string}[]=[];let created=false;
 await routes(page,async(route,path)=>{
  if(path.endsWith('/operator/campaigns/search')){await route.fulfill({json:{campaigns:created?[row]:[],page:1,per_page:50,total:created?1:0}});return true;}
  if(path==='/api/v1/operator/campaigns'){writes.push({body:route.request().postDataJSON(),key:route.request().headers()['idempotency-key'],csrf:route.request().headers()['x-csrf-token']});if(writes.length===1)await route.abort();else{created=true;await route.fulfill({status:201,json:row});}return true;}return false;
 });
 await page.goto('/admin/campaigns?lang=en');await page.getByRole('button',{name:'New campaign'}).click();await page.getByRole('button',{name:'Create campaign',exact:true}).click();await expect(page.getByRole('alert')).toBeFocused();expect(writes).toHaveLength(0);
 await page.getByLabel('Campaign name').fill('<Campaign>');await page.getByLabel('Reason',{exact:true}).fill('Owned creation');
 await page.getByRole('button',{name:'Create campaign',exact:true}).click();await expect(page.getByRole('alert')).toBeVisible();
 await page.getByRole('button',{name:'Create campaign',exact:true}).click();await expect(page.getByText('Campaign saved.',{exact:true})).toBeVisible();
 expect(writes).toHaveLength(2);expect(writes[0]).toEqual(writes[1]);expect(writes[0].body).toEqual({name:'<Campaign>',reason:'Owned creation'});expect(writes[0].csrf).toBe('s'.repeat(43));expect(writes[0].key).toMatch(/^[0-9a-f-]{36}$/i);
});

test('stale state needs explicit refresh, then pause, enable and confirmed terminal deletion',async({page})=>{
 let current=row;const writes:{body:Model<'CampaignStateInput'>;key:string}[]=[];
 await routes(page,async(route,path)=>{
  if(path==='/api/v1/operator/campaigns/'+id){await route.fulfill({json:detail(current)});return true;}
  if(path.endsWith('/state')){const body=route.request().postDataJSON();writes.push({body,key:route.request().headers()['idempotency-key']});if(writes.length===1){current={...current,revision:2};await route.fulfill({status:409,json:{error:{code:'CAMPAIGN_REVISION_CONFLICT'}}});}else{current={...current,state:body.state,revision:current.revision+1};await route.fulfill({json:current});}return true;}return false;
 });
 await page.goto('/admin/campaigns?lang=en');await page.getByRole('button',{name:'Open campaign',exact:true}).click();await page.getByLabel('Reason',{exact:true}).fill('Owned change');
 await page.getByRole('button',{name:'Pause campaign',exact:true}).click();await expect(page.getByRole('alert')).toContainText('changed');expect(writes[0].body.expected_revision).toBe(1);await expect(page.getByLabel('Reason',{exact:true})).toHaveValue('Owned change');
 await page.getByRole('button',{name:'Refresh campaign',exact:true}).click();await expect(page.getByText('Revision 2',{exact:true})).toBeVisible();
 await page.getByRole('button',{name:'Pause campaign',exact:true}).click();await expect(page.getByRole('button',{name:'Enable campaign',exact:true})).toBeVisible();expect(writes[1].body.expected_revision).toBe(2);
 await page.getByRole('button',{name:'Enable campaign',exact:true}).click();await expect(page.getByRole('button',{name:'Pause campaign',exact:true})).toBeVisible();
 await page.getByRole('button',{name:'Delete campaign',exact:true}).click();expect(writes).toHaveLength(3);await page.getByRole('button',{name:'Confirm campaign deletion',exact:true}).click();await expect(page.getByRole('button',{name:'Enable campaign',exact:true})).toHaveCount(0);await expect(page.getByRole('button',{name:'Delete campaign',exact:true})).toHaveCount(0);expect(writes[3].body.state).toBe('deleted');
});

test('a successful HTTP response with the wrong campaign state cannot claim success',async({page})=>{
 const writes:{body:unknown;key:string}[]=[];let current=row;
 await routes(page,async(route,path)=>{
  if(path==='/api/v1/operator/campaigns/'+id){await route.fulfill({json:detail(current)});return true;}
  if(path.endsWith('/state')){const body=route.request().postDataJSON();writes.push({body,key:route.request().headers()['idempotency-key']});if(writes.length>1)current={...current,state:body.state,revision:current.revision+1};await route.fulfill({json:current});return true;}return false;
 });
 await page.goto('/admin/campaigns?lang=en');await page.getByRole('button',{name:'Open campaign',exact:true}).click();await page.getByLabel('Reason',{exact:true}).fill('Owned uncertain state');await page.getByRole('button',{name:'Pause campaign',exact:true}).click();
 await expect(page.getByRole('alert')).toBeVisible();await expect(page.getByText('Campaign saved.',{exact:true})).toHaveCount(0);
 await page.getByRole('button',{name:'Pause campaign',exact:true}).click();await expect(page.getByRole('button',{name:'Enable campaign',exact:true})).toBeVisible();expect(writes).toHaveLength(2);expect(writes[0]).toEqual(writes[1]);
});

test('aborted old card cannot replace a newly selected campaign',async({page})=>{
 let release!:()=>void;const pending=new Promise<void>(resolve=>release=resolve);let first=false;
 const other={...row,campaign_id:otherId,name:'Second campaign',code:'c_second'};
 await routes(page,async(route,path)=>{
  if(path.endsWith('/search')){await route.fulfill({json:{campaigns:[row,other],page:1,per_page:50,total:2}});return true;}
  if(path==='/api/v1/operator/campaigns/'+id){first=true;await pending;try{await route.fulfill({json:detail(row)});}catch{}return true;}
  if(path==='/api/v1/operator/campaigns/'+otherId){await route.fulfill({json:detail(other)});return true;}return false;
 });
 await page.goto('/admin/campaigns?lang=en');await page.getByRole('button',{name:'Open campaign',exact:true}).first().click();await expect.poll(()=>first).toBe(true);await page.getByRole('button',{name:'Open campaign',exact:true}).last().click();const card=page.getByRole('region',{name:'Campaign details'});await expect(card.getByRole('heading',{name:'Second campaign',exact:true})).toBeVisible();release();await expect(card.getByRole('heading',{name:'<Campaign>',exact:true})).toHaveCount(0);
});

test('list error retries to empty, and revoked role hides campaign actions',async({page})=>{
 let reads=0;
 await routes(page,async(route,path)=>{if(path.endsWith('/search')){reads++;await route.fulfill(reads===1?{status:503,json:{error:{code:'SERVICE_UNAVAILABLE'}}}:{json:{campaigns:[],page:1,per_page:50,total:0}});return true;}return false;});
 await page.goto('/admin/campaigns?lang=en');await expect(page.getByRole('alert')).toBeVisible();await page.getByRole('button',{name:'Retry',exact:true}).click();await expect(page.getByText('No campaigns yet.',{exact:true})).toBeVisible();expect(reads).toBe(2);
 await page.unroute('**/api/v1/**');let campaignCalls=0;
 await routes(page,async(route,path)=>{if(path.endsWith('/operator/session')){await route.fulfill({status:403,json:{error:{code:'INVALID_CREDENTIALS'}}});return true;}if(path.includes('/campaigns'))campaignCalls++;return false;});
 await page.reload();await expect(page.getByRole('heading',{name:'Operator access required'})).toBeVisible();await expect(page.getByRole('button',{name:'New campaign',exact:true})).toHaveCount(0);expect(campaignCalls).toBe(0);
});

for(const query of ['invite=c_owned','invite=c_owned&invite=c_other','invite=bad%20code','invite=','invite='+('a'.repeat(65))]){
 test('registration captures exactly one valid invite in memory: '+query,async({page})=>{
  const registrations:Model<'RegisterInput'>[]=[],visits:unknown[]=[];
  await routes(page,async(route,path)=>{if(path.endsWith('/campaign-visits')){visits.push(route.request().postDataJSON());await route.fulfill({status:503,json:{error:{code:'SERVICE_UNAVAILABLE'}}});return true;}if(path.endsWith('/auth/register')){registrations.push(route.request().postDataJSON());await route.fulfill({status:202,json:{challenge_id:id,resend_after:60}});return true;}return false;});
  await page.goto('/register?lang=en&'+query);await page.getByLabel('Email',{exact:true}).fill('owned-registration@example.test');await page.getByRole('checkbox').nth(0).check();await page.getByRole('checkbox').nth(1).check();await page.getByRole('button',{name:'Continue',exact:true}).click();await expect(page.getByRole('status').filter({hasText:'email'}).first()).toBeVisible();
  expect(registrations).toHaveLength(1);if(query==='invite=c_owned'){expect(registrations[0].source_code).toBe('c_owned');expect(visits).toEqual([{code:'c_owned'}]);}else{expect(registrations[0]).not.toHaveProperty('source_code');expect(visits).toHaveLength(0);}
  expect(await page.evaluate(()=>Object.keys(localStorage).some(key=>/campaign|invite|source/i.test(key)))).toBe(false);
 });
}

test('login and reset requests never capture a campaign source or count a visit',async({page})=>{
 const bodies:unknown[]=[],visits:unknown[]=[];
 await routes(page,async(route,path)=>{if(path.endsWith('/campaign-visits'))visits.push(route.request().postDataJSON());if(path.endsWith('/auth/login')){bodies.push(route.request().postDataJSON());await route.fulfill({status:401,json:{error:{code:'INVALID_CREDENTIALS'}}});return true;}if(path.endsWith('/auth/password-reset')){bodies.push(route.request().postDataJSON());await route.fulfill({status:202,json:{challenge_id:id,resend_after:60}});return true;}return false;});
 await page.goto('/login?lang=en&invite=c_owned');await page.getByLabel('Email',{exact:true}).fill('owned-login@example.test');await page.getByLabel('Password',{exact:true}).fill('owned-demo-password');await page.getByRole('button',{name:'Sign in',exact:true}).click();await expect(page.getByRole('alert')).toBeVisible();
 await page.goto('/forgot-password?lang=en&invite=c_owned');await page.getByLabel('Email',{exact:true}).fill('owned-login@example.test');await page.getByRole('button',{name:'Continue',exact:true}).click();await expect.poll(()=>bodies.length).toBe(2);
 for(const body of bodies)expect(body).not.toHaveProperty('source_code');expect(visits).toHaveLength(0);
});
