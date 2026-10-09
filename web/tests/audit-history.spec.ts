import {test,expect,type Page,type Route} from '@playwright/test';
import type {components} from '../src/api/schema.gen';
type Model<K extends keyof components['schemas']>=components['schemas'][K];
const actor='10000000-0000-4000-8000-000000000001',account='20000000-0000-4000-8000-000000000001';
const session={account:{account_id:actor,email:'operator@example.test',email_verified:true,locale:'en',telegram_linked:false},csrf_token:'s'.repeat(43)};
const when='2026-10-09T05:00:00.123456Z';
const event:Model<'OperatorAuditEvent'>={id:'30000000-0000-4000-8000-000000000003',created_at:when,action:'support.message',reason:'<b>Recorded reason</b>',operator_account_id:actor,operator_tg_id:null,system_actor:false,request_id:'40000000-0000-4000-8000-000000000001',operation_id:'50000000-0000-4000-8000-000000000001',support_message_id:'60000000-0000-4000-8000-000000000001',access_operation_id:'70000000-0000-4000-8000-000000000001',monthly_period:'2026-10'};
function report(input:Model<'AuditHistoryInput'>):Model<'AuditHistory'>{
 const out:Model<'AuditHistory'>={version:'audit-history-v1',kind:input.kind,account_id:input.account_id??null,legacy_target_tg_id:input.legacy_target_tg_id??null,native_events:[],legacy_events:[],system_events:[],has_more:false};
 if(input.kind==='native'){
  out.native_events=[{account_id:input.account_id??account,event:{...event}},{account_id:input.account_id??account,event:{...event,id:'30000000-0000-4000-8000-000000000002',action:'actor.unknown',reason:null,operator_account_id:null,system_actor:null,request_id:null,operation_id:null,support_message_id:null,access_operation_id:null,monthly_period:null}}];out.has_more=!input.before_id;
  if(input.before_id)out.native_events=[{account_id:input.account_id??account,event:{...event,id:'30000000-0000-4000-8000-000000000001',action:'older.action'}}];
 }
 if(input.kind==='legacy')out.legacy_events=[{source_id:'9223372036854775807',created_at:when,action:'legacy.support',target_tg_id:'9007199254740993',actor_type:'operator',actor_id:'-9007199254740993',actor_name:'<img src=x onerror="window.auditExecuted=true">',source:'support_bot',account_id:input.account_id??account}];
 if(input.kind==='system')out.system_events=[{id:'80000000-0000-4000-8000-000000000001',created_at:when,action:'audit.pruned',period_day:'2026-10-09',cutoff:'2025-10-09T05:00:00Z',retention_days:365,native_count:3,legacy_count:4,system_count:5}];
 return out;
}
async function routes(page:Page,extra?:(route:Route,path:string)=>Promise<boolean>){
 const reads:{body:Model<'AuditHistoryInput'>;csrf:string|undefined;key:string|undefined}[]=[];
 await page.route('**/api/v1/**',async route=>{
  const req=route.request(),path=new URL(req.url()).pathname;
  if(path.endsWith('/operator/audit/history'))reads.push({body:req.postDataJSON(),csrf:req.headers()['x-csrf-token'],key:req.headers()['idempotency-key']});
  if(extra&&await extra(route,path))return;
  if(path.endsWith('/operator/session')||path.endsWith('/auth/session'))return route.fulfill({json:session});
  if(path.endsWith('/operator/audit/history'))return route.fulfill({json:report(req.postDataJSON())});
  if(path.endsWith('/auth/logout'))return route.fulfill({status:204});
  return route.fulfill({json:{}});
 });
 return reads;
}

test('global journal renders recorded actors, unknowns, references and exact tuple pages',async({page})=>{
 const reads=await routes(page);await page.setViewportSize({width:375,height:812});await page.goto('/admin/audit?lang=en');
 const region=page.getByRole('region',{name:'Action journal',exact:true});await expect(region).toBeVisible();
 await expect(region.getByRole('heading',{name:'support.message',exact:true})).toBeVisible();await expect(region.getByText('<b>Recorded reason</b>',{exact:true})).toBeVisible();await expect(region.locator('b')).toHaveCount(0);
 await expect(region.getByText('Unknown actor',{exact:true})).toBeVisible();await expect(region.getByText('No reason recorded',{exact:true})).toBeVisible();
 for(const id of [event.request_id,event.operation_id,event.support_message_id,event.access_operation_id])await expect(region.getByText(id!,{exact:true})).toBeVisible();
 await expect(region.getByRole('link',{name:account,exact:true}).first()).toHaveAttribute('href','/admin/clients/'+account+'/show?lang=en');
 const next=region.getByRole('button',{name:'Next page',exact:true});await next.focus();await page.keyboard.press('Enter');await expect(region.getByRole('heading',{name:'older.action',exact:true})).toBeVisible();
 expect(reads[1].body).toEqual({kind:'native',before_created_at:when,before_id:'30000000-0000-4000-8000-000000000002'});
 await region.getByRole('button',{name:'Previous page',exact:true}).click();await expect(region.getByRole('heading',{name:'support.message',exact:true})).toBeVisible();
 const refresh=region.getByRole('button',{name:'Refresh journal',exact:true});await refresh.focus();await page.keyboard.press('Enter');await expect.poll(()=>reads.length).toBe(4);
 expect(reads.every(x=>x.csrf==='s'.repeat(43)&&x.key===undefined)).toBe(true);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});

for(const lang of ['en','ru'])test('legacy exact identifiers/private payload and system receipt '+lang,async({page})=>{
 const reads=await routes(page,async(route,path)=>{
  if(!path.endsWith('/operator/audit/history'))return false;
  const input=route.request().postDataJSON(),value=report(input);
  if(input.kind==='legacy')(value.legacy_events[0] as Model<'LegacyAuditHistoryEvent'>&{payload_json:string}).payload_json='private-dm-do-not-show';
  await route.fulfill({json:value});return true;
 });
 await page.goto('/admin/audit?lang='+lang);const region=page.getByRole('region',{name:lang==='en'?'Action journal':'Журнал действий',exact:true});
 const select=region.getByLabel(lang==='en'?'Journal source':'Источник журнала',{exact:true});await select.selectOption('legacy');
 await expect(region.getByText('9223372036854775807',{exact:true})).toBeVisible();await expect(region.getByText('-9007199254740993',{exact:true})).toBeVisible();
 await expect(region.getByText('<img src=x onerror="window.auditExecuted=true">',{exact:true})).toBeVisible();await expect(region.locator('img')).toHaveCount(0);await expect(region.getByText('private-dm-do-not-show')).toHaveCount(0);expect(await page.evaluate(()=>Object.hasOwn(window,'auditExecuted'))).toBe(false);
 await region.getByLabel(lang==='en'?'Legacy target Telegram ID':'Исходный Telegram ID клиента',{exact:true}).fill('9007199254740993');
 await region.getByRole('button',{name:lang==='en'?'Apply filters':'Применить фильтры',exact:true}).click();await expect.poll(()=>reads.at(-1)?.body.legacy_target_tg_id).toBe('9007199254740993');await expect(region.getByText('9223372036854775807',{exact:true})).toBeVisible();
 await select.selectOption('system');await expect(region.getByRole('heading',{name:'audit.pruned',exact:true})).toBeVisible();
 for(const value of ['365','3','4','5'])await expect(region.getByText(value,{exact:true})).toBeVisible();expect(reads.at(-1)?.body).toEqual({kind:'system'});
});

test('loading then failed read focuses a safe retry and revocation clears the journal',async({page})=>{
 let calls=0,release!:()=>void;const hold=new Promise<void>(resolve=>release=resolve);
 await routes(page,async(route,path)=>{
  if(!path.endsWith('/operator/audit/history'))return false;calls++;
  if(calls===1){await hold;await route.fulfill({status:503,json:{error:{code:'SERVICE_UNAVAILABLE',message:'private-provider-error'}}});return true;}
  if(calls===3){await route.fulfill({status:403,json:{error:{code:'INVALID_CREDENTIALS'}}});return true;}return false;
 });
 try{
  await page.goto('/admin/audit?lang=en');const region=page.getByRole('region',{name:'Action journal',exact:true});await expect(region.getByRole('status')).toContainText('Loading');release();await expect(region.getByRole('alert')).toBeFocused();await expect(page.getByText('private-provider-error')).toHaveCount(0);
  await region.getByRole('button',{name:'Retry',exact:true}).click();await expect(region.getByRole('heading',{name:'support.message',exact:true})).toBeVisible();
  await region.getByRole('button',{name:'Refresh journal',exact:true}).click();await expect(page.getByRole('heading',{name:'Operator access required',exact:true})).toBeVisible();await expect(region).toHaveCount(0);await expect(page.getByText('<b>Recorded reason</b>',{exact:true})).toHaveCount(0);expect(calls).toBe(3);
 }finally{release();}
});

test('wrong successful version, filter or stream is rejected before rendering',async({page})=>{
 let calls=0;
 await routes(page,async(route,path)=>{
  if(!path.endsWith('/operator/audit/history'))return false;calls++;const value=report(route.request().postDataJSON());
  if(calls===1)(value as unknown as {version:string}).version='future-audit';
  if(calls===2)value.account_id=account;
  if(calls===3)value.legacy_events=report({kind:'legacy'}).legacy_events;
  if(calls===4){value.native_events=[];value.has_more=false;}
  await route.fulfill({json:value});return true;
 });
 await page.goto('/admin/audit?lang=en');const region=page.getByRole('region',{name:'Action journal',exact:true});
 for(let n=1;n<=3;n++){await expect(region.getByRole('alert')).toBeFocused();await expect(region.getByRole('heading',{name:'support.message',exact:true})).toHaveCount(0);await region.getByRole('button',{name:'Retry',exact:true}).click();}
 await expect(region.getByRole('status')).toHaveText('No events.');await expect(region.getByRole('button',{name:'Next page',exact:true})).toBeDisabled();expect(calls).toBe(4);
});

for(const change of ['language','filter','source'])test('held old reply cannot replace changed journal '+change,async({page})=>{
 let calls=0,release!:()=>void;const hold=new Promise<void>(resolve=>release=resolve);
 const reads=await routes(page,async(route,path)=>{
  if(!path.endsWith('/operator/audit/history'))return false;calls++;
  if(calls!==1)return false;await hold;const value=report({kind:'native'});value.native_events[0].event.action='stale-private-action';try{await route.fulfill({json:value});}catch{}return true;
 });
 try{
  await page.goto('/admin/audit?lang=en');await expect.poll(()=>calls).toBe(1);let region=page.getByRole('region',{name:'Action journal',exact:true});
  if(change==='language'){await page.getByRole('button',{name:'RU',exact:true}).click();region=page.getByRole('region',{name:'Журнал действий',exact:true});}
  if(change==='filter'){await region.getByLabel('Account ID',{exact:true}).fill(account);await region.getByRole('button',{name:'Apply filters',exact:true}).click();}
  if(change==='source')await region.getByLabel('Journal source',{exact:true}).selectOption('legacy');
  await expect(region.getByRole('heading',{name:change==='source'?'legacy.support':'support.message',exact:true})).toBeVisible();release();await expect(page.getByRole('heading',{name:'stale-private-action',exact:true})).toHaveCount(0);expect(calls).toBe(2);if(change==='filter')expect(reads[1].body).toEqual({kind:'native',account_id:account});
 }finally{release();}
});

test('invalid or mixed filters do not read global data',async({page})=>{
 const reads=await routes(page);await page.goto('/admin/audit?lang=en');const region=page.getByRole('region',{name:'Action journal',exact:true});await expect(region.getByRole('heading',{name:'support.message',exact:true})).toBeVisible();
 await region.getByLabel('Account ID',{exact:true}).fill('NOT-A-UUID');await region.getByRole('button',{name:'Apply filters',exact:true}).click();await expect(region.getByRole('alert')).toBeFocused();expect(reads).toHaveLength(1);
 await region.getByLabel('Journal source',{exact:true}).selectOption('legacy');await expect(region.getByRole('heading',{name:'legacy.support',exact:true})).toBeVisible();await region.getByLabel('Account ID',{exact:true}).fill(account);await region.getByLabel('Legacy target Telegram ID',{exact:true}).fill('9007199254740993');await region.getByRole('button',{name:'Apply filters',exact:true}).click();await expect(region.getByRole('alert')).toBeFocused();expect(reads).toHaveLength(2);
 await region.getByLabel('Account ID',{exact:true}).fill('');await region.getByLabel('Legacy target Telegram ID',{exact:true}).fill('9223372036854775808');await region.getByRole('button',{name:'Apply filters',exact:true}).click();await expect(region.getByRole('alert')).toBeFocused();expect(reads).toHaveLength(2);
});

test('legacy page cursor keeps its source namespace and clears on source change',async({page})=>{
 const reads=await routes(page,async(route,path)=>{
  if(!path.endsWith('/operator/audit/history'))return false;
  const input=route.request().postDataJSON();if(input.kind!=='legacy')return false;const value=report(input);value.has_more=!input.before_source_id;
  if(input.before_source_id){value.legacy_events[0].source_id='9223372036854775806';value.legacy_events[0].action='older.legacy';}
  await route.fulfill({json:value});return true;
 });
 await page.goto('/admin/audit?lang=en');const region=page.getByRole('region',{name:'Action journal',exact:true});await region.getByLabel('Journal source',{exact:true}).selectOption('legacy');await expect(region.getByRole('heading',{name:'legacy.support',exact:true})).toBeVisible();
 await region.getByRole('button',{name:'Next page',exact:true}).click();await expect(region.getByRole('heading',{name:'older.legacy',exact:true})).toBeVisible();expect(reads.at(-1)?.body).toEqual({kind:'legacy',before_created_at:when,before_source_id:'9223372036854775807'});
 await region.getByLabel('Journal source',{exact:true}).selectOption('native');await expect(region.getByRole('heading',{name:'support.message',exact:true})).toBeVisible();expect(reads.at(-1)?.body).toEqual({kind:'native'});
});

test('another denied operator request cannot restore a held journal',async({page})=>{
 let started=false,release!:()=>void;const hold=new Promise<void>(resolve=>release=resolve);
 await routes(page,async(route,path)=>{
  if(path.endsWith('/operator/audit/history')){started=true;await hold;try{await route.fulfill({json:report({kind:'native'})});}catch{}return true;}
  if(path.endsWith('/operator/clients/search')){await route.fulfill({status:403,json:{error:{code:'INVALID_CREDENTIALS'}}});return true;}return false;
 });
 try{await page.setViewportSize({width:1024,height:900});await page.goto('/admin/audit?lang=en');await expect.poll(()=>started).toBe(true);await page.getByRole('menuitem',{name:'Clients',exact:true}).click();await expect(page.getByRole('heading',{name:'Operator access required',exact:true})).toBeVisible();release();await expect(page.getByRole('region',{name:'Action journal',exact:true})).toHaveCount(0);await expect(page.getByText('<b>Recorded reason</b>',{exact:true})).toHaveCount(0);}finally{release();}
});

const otherAccount='20000000-0000-4000-8000-000000000002';
const client=(id:string):Model<'OperatorClient'>=>({account_id:id,kind:'web',display_name:id===account?'Client one':'Client two',email:'client@example.test',telegram_id:null,locale:'en',created_at:null,restricted:true,vpn_banned:false,had_subscription:false});
const card=(id:string):Model<'OperatorClientCard'>=>({client:client(id),subscription:{status:'none',access_profile:'regular',vpn_banned:false,devices:0,traffic_limit_bytes:0,expires_at:null,traffic_used_bytes:null,observed_at:null,data_stale:false,access_operation_id:null,access_operation_status:null},server:null,support:null,trial_requests:[],trial_has_more:false,audit_events:[],audit_has_more:false,legacy_approval:null,legacy_events:[],legacy_has_more:false});
async function cardRoutes(route:Route,path:string){
 for(const id of [account,otherAccount])if(path.endsWith('/operator/clients/'+id)){await route.fulfill({json:card(id)});return true;}
 if(path.endsWith('/support')){await route.fulfill({json:{conversation:null,messages:[],has_more:false,oldest_sequence:null}});return true;}
 if(path.endsWith('/orders/current')){await route.fulfill({json:{order:null}});return true;}
 if(path.endsWith('/payment-history')){await route.fulfill({json:{kind:route.request().postDataJSON().kind,orders:[],receipts:[],legacy_transactions:[],has_more:false}});return true;}
 return false;
}

test('client card lazily reads the same journal with an immutable account filter',async({page})=>{
 const reads=await routes(page,cardRoutes);await page.goto('/admin/clients/'+account+'/show?lang=en');await expect(page.getByRole('heading',{name:'Client one',exact:true})).toBeVisible();expect(reads).toHaveLength(0);
 const summary=page.locator('summary').filter({hasText:/^Action journal$/});await summary.focus();await page.keyboard.press('Enter');
 const region=page.getByRole('region',{name:'Action journal',exact:true});await expect(region.getByRole('heading',{name:'support.message',exact:true})).toBeVisible();expect(reads[0].body).toEqual({kind:'native',account_id:account});
 await expect(region.getByRole('textbox')).toHaveCount(0);await expect(region.getByLabel('Journal source',{exact:true}).locator('option[value="system"]')).toHaveCount(0);
 await region.getByLabel('Journal source',{exact:true}).selectOption('legacy');await expect(region.getByRole('heading',{name:'legacy.support',exact:true})).toBeVisible();expect(reads[1].body).toEqual({kind:'legacy',account_id:account});await expect(region.getByRole('link',{name:account,exact:true})).toBeVisible();
});

test('client switch aborts the old account journal without restoring its events',async({page})=>{
 let calls=0,release!:()=>void;const hold=new Promise<void>(resolve=>release=resolve);
 const reads=await routes(page,async(route,path)=>{
  if(!path.endsWith('/operator/audit/history'))return cardRoutes(route,path);calls++;
  if(calls!==1)return false;await hold;const value=report({kind:'native',account_id:account});value.native_events[0].event.action='old-account-private-action';await route.fulfill({json:value}).catch(()=>{});return true;
 });
 try{
  await page.goto('/admin/clients/'+account+'/show?lang=en');await expect(page.getByRole('heading',{name:'Client one',exact:true})).toBeVisible();await page.locator('summary').filter({hasText:/^Action journal$/}).click();await expect.poll(()=>calls).toBe(1);
  await page.evaluate(id=>{window.history.pushState(null,'','/admin/clients/'+id+'/show?lang=en');window.dispatchEvent(new PopStateEvent('popstate'));},otherAccount);await expect(page.getByRole('heading',{name:'Client two',exact:true})).toBeVisible();
  await expect(page.getByRole('region',{name:'Action journal',exact:true})).toHaveCount(0);await page.locator('summary').filter({hasText:/^Action journal$/}).click();const region=page.getByRole('region',{name:'Action journal',exact:true});await expect(region.getByRole('heading',{name:'support.message',exact:true})).toBeVisible();expect(reads[1].body).toEqual({kind:'native',account_id:otherAccount});
  release();await expect(region.getByRole('heading',{name:'old-account-private-action',exact:true})).toHaveCount(0);await expect(region.getByRole('link',{name:otherAccount,exact:true}).first()).toBeVisible();expect(calls).toBe(2);
 }finally{release();}
});
