import {test,expect,type Page,type Route} from '@playwright/test';

const csrf='s'.repeat(43);
const legacyId='legacy/edge id?x#1';
const session=(canManage:boolean)=>({account:{account_id:'10000000-0000-4000-8000-000000000001',email:'operator@example.test',email_verified:true,locale:'en',telegram_linked:false},csrf_token:csrf,can_manage_servers:canManage});
const offline={id:legacyId,name:'Edge North',host:'https://edge.example.test',max_clients:null,online:false,observed_at:null,assigned_clients:3,reserved_clients:1,retired:false};
const detail={...offline,assigned_clients:0,reserved_clients:0,can_ping:true,can_delete:false};
const deletable={...detail,online:true,observed_at:'2026-10-09T10:00:00Z',can_delete:true};

async function routes(page:Page,canManage=true,extra?:(route:Route,path:string)=>Promise<boolean>){
 await page.route('**/api/v1/**',async route=>{
  const path=new URL(route.request().url()).pathname;
  if(extra&&await extra(route,path))return;
  if(path.endsWith('/operator/session')||path.endsWith('/auth/session'))return route.fulfill({json:session(canManage)});
  if(path==='/api/v1/operator/servers')return route.fulfill({json:{servers:[offline]}});
  if(path==='/api/v1/operator/servers/'+encodeURIComponent(legacyId))return route.fulfill({json:detail});
  return route.fulfill({json:{}});
 });
}

test('ordinary operators have no server navigation and make no server requests',async({page})=>{
 let serverCalls=0;await routes(page,false,async(_route,path)=>{if(path.includes('/operator/servers'))serverCalls++;return false;});
 await page.goto('/admin?lang=en');await expect(page.getByRole('menuitem',{name:'Servers',exact:true})).toHaveCount(0);expect(serverCalls).toBe(0);
});

test('keyboard opens a safely encoded legacy server card and creates a server',async({page})=>{
 let detailPath='',created=false;const writes:{body:unknown;key:string;csrf:string}[]=[];
 await routes(page,true,async(route,path)=>{
  if(path==='/api/v1/operator/servers'&&route.request().method()==='GET'){await route.fulfill({json:{servers:created?[offline,{...offline,id:'created',name:'Edge South',assigned_clients:0,reserved_clients:0}]:[offline]}});return true;}
  if(path==='/api/v1/operator/servers'&&route.request().method()==='POST'){writes.push({body:route.request().postDataJSON(),key:route.request().headers()['idempotency-key'],csrf:route.request().headers()['x-csrf-token']});created=true;await route.fulfill({status:201,json:{...detail,id:'created',name:'Edge South',max_clients:0,assigned_clients:0,reserved_clients:0}});return true;}
  if(path.startsWith('/api/v1/operator/servers/')){detailPath=path;await route.fulfill({json:detail});return true;}return false;
 });
 await page.setViewportSize({width:375,height:812});await page.goto('/admin/servers?lang=en');
 const open=page.getByRole('button',{name:'Open server Edge North'});await open.focus();await page.keyboard.press('Enter');await expect(page.getByRole('region',{name:'Server details'})).toBeVisible();expect(detailPath).toBe('/api/v1/operator/servers/legacy%2Fedge%20id%3Fx%231');
 await expect(page.getByText('Offline',{exact:true}).first()).toBeVisible();await expect(page.getByText('No configured limit',{exact:true}).first()).toBeVisible();await expect(page.getByText('Deletion is unavailable while the server is in use or its state cannot be checked safely.')).toBeVisible();await expect(page.getByRole('button',{name:'Delete server',exact:true})).toHaveCount(0);await page.getByLabel('Server name').fill('Edge South');await page.getByLabel('HTTPS host').fill('https://south.example.test/panel');await page.getByLabel('Maximum clients').fill('0');
 const create=page.getByRole('button',{name:'Add server',exact:true});await create.focus();await page.keyboard.press('Enter');await expect(page.getByRole('status')).toContainText('Server added.');
 expect(writes).toHaveLength(1);expect(writes[0].body).toEqual({name:'Edge South',host:'https://south.example.test/panel',max_clients:0});expect(writes[0].csrf).toBe(csrf);expect(writes[0].key).toMatch(/^[0-9a-f-]{36}$/i);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await expect(page.getByRole('region',{name:'Server details'}).locator('dt').filter({hasText:/^Maximum clients$/}).locator('..').locator('dd')).toHaveText('0');
});

test('lost create response reuses its key while edited input starts a new action',async({page})=>{
 const writes:{body:unknown;key:string}[]=[];await routes(page,true,async(route,path)=>{if(path==='/api/v1/operator/servers'&&route.request().method()==='POST'){writes.push({body:route.request().postDataJSON(),key:route.request().headers()['idempotency-key']});if(writes.length<3)await route.abort();else await route.fulfill({status:201,json:{...detail,id:'created',name:'Changed'}});return true;}return false;});
 await page.goto('/admin/servers?lang=en');await page.getByLabel('Server name').fill('New server');await page.getByLabel('HTTPS host').fill('https://new.example.test');await page.getByLabel('Maximum clients').fill('10');
 await page.getByRole('button',{name:'Add server',exact:true}).click();await expect(page.getByRole('alert')).toBeVisible();await page.getByRole('button',{name:'Add server',exact:true}).click();await expect.poll(()=>writes.length).toBe(2);expect(writes[1]).toEqual(writes[0]);
 await page.getByLabel('Server name').fill('Changed');await page.getByRole('button',{name:'Add server',exact:true}).click();await expect.poll(()=>writes.length).toBe(3);expect(writes[2].key).not.toBe(writes[1].key);expect(writes[2].body).toEqual({name:'Changed',host:'https://new.example.test',max_clients:10});
});

test('sync and ping are explicit; deletion needs confirmation and service failure is never retried automatically',async({page})=>{
 const actions:{path:string;key:string;body:unknown}[]=[];let syncCalls=0,deleteCalls=0;
 await routes(page,true,async(route,path)=>{
  if(path==='/api/v1/operator/servers/sync'){syncCalls++;actions.push({path,key:route.request().headers()['idempotency-key'],body:route.request().postDataJSON()});if(syncCalls===1)await route.abort();else await route.fulfill({json:{servers:[offline]}});return true;}
  if(path==='/api/v1/operator/servers/'+encodeURIComponent(legacyId)&&route.request().method()==='GET'){await route.fulfill({json:deletable});return true;}
  if(path.endsWith('/ping')){actions.push({path,key:route.request().headers()['idempotency-key'],body:route.request().postDataJSON()});await route.fulfill({json:deletable});return true;}
  if(path.endsWith('/delete')){deleteCalls++;actions.push({path,key:route.request().headers()['idempotency-key'],body:route.request().postDataJSON()});if(deleteCalls===1)await route.fulfill({status:503,json:{error:{code:'SERVICE_UNAVAILABLE',message:'private',request_id:'70000000-0000-4000-8000-000000000001'}}});else await route.fulfill({status:409,json:{error:{code:'SERVER_IN_USE',message:'private',request_id:'70000000-0000-4000-8000-000000000001'}}});return true;}
  return false;
 });
 await page.goto('/admin/servers?lang=en');await page.getByRole('button',{name:'Sync servers',exact:true}).click();await expect(page.getByRole('alert')).toBeVisible();await expect(page.getByLabel('Server name')).toHaveAttribute('aria-invalid','false');await expect(page.getByLabel('Server name')).not.toHaveAttribute('aria-describedby',/./);expect(syncCalls).toBe(1);await page.waitForTimeout(50);expect(syncCalls).toBe(1);await page.getByRole('button',{name:'Sync servers',exact:true}).click();await expect.poll(()=>syncCalls).toBe(2);expect(actions[1].key).toBe(actions[0].key);
 await page.getByRole('button',{name:'Open server Edge North'}).click();await page.getByRole('button',{name:'Ping server',exact:true}).click();await expect(page.getByRole('region',{name:'Server details'}).getByText('Online',{exact:true})).toBeVisible();
 await page.getByRole('button',{name:'Delete server',exact:true}).click();expect(deleteCalls).toBe(0);await expect(page.getByText('Delete Edge North? This action cannot be undone.')).toBeVisible();await page.getByRole('button',{name:'Confirm server deletion',exact:true}).click();await expect(page.getByRole('alert')).toContainText('Service temporarily unavailable.');expect(deleteCalls).toBe(1);await page.waitForTimeout(50);expect(deleteCalls).toBe(1);await page.getByRole('button',{name:'Confirm server deletion',exact:true}).click();await expect(page.getByRole('alert')).toContainText('Server is still in use and cannot be deleted.');expect(deleteCalls).toBe(2);const deletes=actions.filter(action=>action.path.endsWith('/delete'));expect(deletes[1].key).toBe(deletes[0].key);expect(deletes[1].body).toEqual({confirmation:true});
});

test('sync refreshes selected detail when an offline server becomes deletable',async({page})=>{
 let detailReads=0;
 await routes(page,true,async(route,path)=>{
  if(path==='/api/v1/operator/servers/sync'){await route.fulfill({json:{servers:[{...offline,online:true,assigned_clients:0,reserved_clients:0}]}});return true;}
  if(path==='/api/v1/operator/servers/'+encodeURIComponent(legacyId)&&route.request().method()==='GET'){detailReads++;await route.fulfill({json:detailReads===1?detail:deletable});return true;}
  return false;
 });
 await page.goto('/admin/servers?lang=en');await page.getByRole('button',{name:'Open server Edge North'}).click();await expect(page.getByRole('button',{name:'Delete server',exact:true})).toHaveCount(0);
 await page.getByRole('button',{name:'Sync servers',exact:true}).click();await expect(page.getByRole('button',{name:'Delete server',exact:true})).toBeVisible();expect(detailReads).toBe(2);
 await page.getByRole('button',{name:'Delete server',exact:true}).click();await expect(page.getByRole('button',{name:'Confirm server deletion',exact:true})).toBeVisible();
});

test('sync closes open deletion confirmation when current detail refuses deletion',async({page})=>{
 let detailReads=0;const refused={...detail,assigned_clients:2,reserved_clients:1,online:false,can_delete:false};
 await routes(page,true,async(route,path)=>{
  if(path==='/api/v1/operator/servers'&&route.request().method()==='GET'){await route.fulfill({json:{servers:[{...offline,online:true,assigned_clients:0,reserved_clients:0}]}});return true;}
  if(path==='/api/v1/operator/servers/sync'){await route.fulfill({json:{servers:[offline]}});return true;}
  if(path==='/api/v1/operator/servers/'+encodeURIComponent(legacyId)&&route.request().method()==='GET'){detailReads++;await route.fulfill({json:detailReads===1?deletable:refused});return true;}
  return false;
 });
 await page.goto('/admin/servers?lang=en');await page.getByRole('button',{name:'Open server Edge North'}).click();await page.getByRole('button',{name:'Delete server',exact:true}).click();await expect(page.getByRole('button',{name:'Confirm server deletion',exact:true})).toBeVisible();
 await page.getByRole('button',{name:'Sync servers',exact:true}).click();await expect(page.getByRole('button',{name:'Confirm server deletion',exact:true})).toHaveCount(0);await expect(page.getByRole('button',{name:'Delete server',exact:true})).toHaveCount(0);await expect(page.getByText('Deletion is unavailable while the server is in use or its state cannot be checked safely.')).toBeVisible();expect(detailReads).toBe(2);
});

for(const status of [401,403])test(`delete ${status} revocation removes infrastructure actions`,async({page})=>{
 let deletes=0;await routes(page,true,async(route,path)=>{if(path==='/api/v1/operator/servers/'+encodeURIComponent(legacyId)&&route.request().method()==='GET'){await route.fulfill({json:deletable});return true;}if(path.endsWith('/delete')){deletes++;await route.fulfill({status,json:{error:{code:'INVALID_CREDENTIALS'}}});return true;}return false;});
 await page.goto('/admin/servers?lang=en');await page.getByRole('button',{name:'Open server Edge North'}).click();await page.getByRole('button',{name:'Delete server',exact:true}).click();await page.getByRole('button',{name:'Confirm server deletion',exact:true}).click();await expect(page.getByRole('heading',{name:'Operator access required'})).toBeVisible();expect(deletes).toBe(1);
});

test('confirmed deletion retires an empty server and removes its actions',async({page})=>{
 let deletes=0;await routes(page,true,async(route,path)=>{if(path==='/api/v1/operator/servers/'+encodeURIComponent(legacyId)&&route.request().method()==='GET'){await route.fulfill({json:deletable});return true;}if(path.endsWith('/delete')){deletes++;await route.fulfill({json:{...deletable,retired:true,online:false,can_ping:false,can_delete:false}});return true;}return false;});
 await page.goto('/admin/servers?lang=en');await page.getByRole('button',{name:'Open server Edge North'}).click();await page.getByRole('button',{name:'Delete server',exact:true}).click();await page.getByRole('button',{name:'Confirm server deletion',exact:true}).click();await expect(page.getByRole('status')).toContainText('Server retired.');await expect(page.getByRole('region',{name:'Server details'})).toHaveCount(0);await expect(page.getByText('No servers yet.',{exact:true})).toBeVisible();expect(deletes).toBe(1);
});

test('list failure retries to a Russian empty state and offline refusal has accessible errors',async({page})=>{
 let reads=0;await routes(page,true,async(route,path)=>{if(path==='/api/v1/operator/servers'&&route.request().method()==='GET'){reads++;if(reads===1)await route.fulfill({status:503,json:{error:{code:'SERVICE_UNAVAILABLE'}}});else await route.fulfill({json:{servers:[]}});return true;}return false;});
 await page.goto('/admin/servers?lang=ru');await expect(page.getByRole('alert')).toBeFocused();await page.getByRole('button',{name:'Повторить',exact:true}).click();await expect(page.getByText('Серверов пока нет.',{exact:true})).toBeVisible();
 await page.getByRole('button',{name:'Добавить сервер',exact:true}).click();const error=page.getByRole('alert');await expect(error).toBeFocused();expect(await page.getByLabel('Название сервера').getAttribute('aria-describedby')).toContain('server-form-error');expect(await page.getByLabel('HTTPS-адрес').getAttribute('aria-describedby')).toContain('server-form-error');
});
