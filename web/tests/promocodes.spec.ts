import {test,expect,type Page,type Route} from '@playwright/test';

const id='70000000-0000-4000-8000-000000000048';
const actor='10000000-0000-4000-8000-000000000001';
const session={account:{account_id:actor,email:'operator@example.test',email_verified:true,locale:'en',telegram_linked:false},csrf_token:'s'.repeat(43)};
const row={promocode_id:id,code:'PROMO48',duration_days:30,revision:1,state:'available',created_at:'2026-10-10T10:00:00Z',activated_at:null,activated_account_id:null,activated_by_tg_id:null,legacy_source:null,legacy_promocode_id:null} as {promocode_id:string;code:string;duration_days:number;revision:number;state:'available'|'activated'|'deleted';created_at:string;activated_at:string|null;activated_account_id:string|null;activated_by_tg_id:string|null;legacy_source:string|null;legacy_promocode_id:string|null};
const metadata=(item= row)=>({promocode_id:item.promocode_id,duration_days:item.duration_days,revision:item.revision,state:item.state});
const detail=(item=row)=>({promocode:item,events:[{event_id:'70000000-0000-4000-8000-000000000049',actor_account_id:actor,action:'create',created_at:item.created_at,reason:'Created by operator',before:null,after:metadata(item)}],events_has_more:true});

async function routes(page:Page,extra?:(route:Route,path:string)=>Promise<boolean>){await page.route('**/api/v1/**',async route=>{
 const path=new URL(route.request().url()).pathname;
 if(extra&&await extra(route,path))return;
 if(path.endsWith('/operator/session')||path.endsWith('/auth/session'))return route.fulfill({json:session});
 if(path.endsWith('/operator/promocodes/search'))return route.fulfill({json:{promocodes:[row],page:1,per_page:50,total:1}});
 if(path==='/api/v1/operator/promocodes/'+id)return route.fulfill({json:detail()});
 return route.fulfill({json:{}});
});}

test('empty and failed lists have focusable errors, retry, and RU/EN labels',async({page})=>{
 let reads=0;
 await routes(page,async(route,path)=>{if(path.endsWith('/operator/promocodes/search')){reads++;await route.fulfill(reads===1?{status:503,json:{error:{code:'SERVICE_UNAVAILABLE'}}}:{json:{promocodes:[],page:1,per_page:50,total:0}});return true;}return false;});
 await page.goto('/admin/promocodes?lang=en');
 await expect(page.getByRole('heading',{name:'Promocodes',exact:true})).toBeVisible();
 await expect(page.getByRole('alert')).toBeFocused();
 await page.getByRole('button',{name:'Retry',exact:true}).click();
 await expect(page.getByText('No promocodes yet.',{exact:true})).toBeVisible();
 await page.getByRole('button',{name:'RU',exact:true}).click();
 await expect(page.getByRole('heading',{name:'Промокоды',exact:true})).toBeVisible();
 await page.getByRole('button',{name:'Новый промокод'}).focus();await page.keyboard.press('Enter');
 await expect(page.getByLabel('Срок в днях')).toBeVisible();
 expect(reads).toBe(2);
});

test('create validates input and retries an uncertain result with the same key, body and CSRF',async({page})=>{
 const writes:{body:unknown;key:string;csrf:string}[]=[];let current=row;
 await routes(page,async(route,path)=>{
  if(path.endsWith('/operator/promocodes/search')){await route.fulfill({json:{promocodes:writes.length>1?[current]:[],page:1,per_page:50,total:writes.length>1?1:0}});return true;}
  if(path==='/api/v1/operator/promocodes'){writes.push({body:route.request().postDataJSON(),key:route.request().headers()['idempotency-key'],csrf:route.request().headers()['x-csrf-token']});if(writes.length===1)await route.abort();else await route.fulfill({status:201,json:current});return true;}
  if(path==='/api/v1/operator/promocodes/'+id){await route.fulfill({json:detail(current)});return true;}return false;
 });
 await page.goto('/admin/promocodes?lang=en');await page.getByRole('button',{name:'New promocode'}).click();
 await page.getByRole('button',{name:'Create promocode'}).click();await expect(page.getByRole('alert')).toBeFocused();expect(writes).toHaveLength(0);
 await page.getByLabel('Duration in days').fill('30');await page.getByLabel('Reason').fill('Created by operator');
 await page.getByRole('button',{name:'Create promocode'}).click();await expect(page.getByRole('alert')).toBeVisible();
 await page.getByRole('button',{name:'Create promocode'}).click();
 await expect(page.getByRole('region',{name:'Promocode details'}).getByText('PROMO48')).toBeVisible();
 expect(writes).toHaveLength(2);expect(writes[0]).toEqual(writes[1]);expect(writes[0].body).toEqual({duration_days:30,reason:'Created by operator'});expect(writes[0].csrf).toBe('s'.repeat(43));expect(writes[0].key).toMatch(/^[0-9a-f-]{36}$/i);
});

test('revision conflict requires refresh; success re-reads a card that became used',async({page})=>{
 let current={...row};const writes:{body:any;key:string}[]=[];
 await routes(page,async(route,path)=>{
  if(path==='/api/v1/operator/promocodes/'+id){await route.fulfill({json:detail(current)});return true;}
  if(path.endsWith('/edit')){const body=route.request().postDataJSON();writes.push({body,key:route.request().headers()['idempotency-key']});if(writes.length===1){current={...current,revision:2};await route.fulfill({status:409,json:{error:{code:'PROMOCODE_REVISION_CONFLICT'}}});}else{current={...current,duration_days:45,revision:3,state:'activated',activated_at:'2026-10-10T11:00:00Z',activated_account_id:actor};await route.fulfill({json:{...row,duration_days:45,revision:3}});}return true;}return false;
 });
 await page.goto('/admin/promocodes?lang=en');await page.getByRole('button',{name:'Open promocode'}).click();
 await page.getByLabel('Duration in days').fill('45');await page.getByLabel('Reason').fill('Operator correction');
 await page.getByRole('button',{name:'Save duration'}).click();await expect(page.getByRole('alert')).toBeFocused();
 await expect(page.getByLabel('Reason')).toHaveValue('Operator correction');
 await expect(page.getByRole('button',{name:'Save duration'})).toBeDisabled();
 await page.getByRole('button',{name:'Refresh promocode'}).click();await expect(page.getByText('Revision 2',{exact:true})).toBeVisible();
 await page.getByRole('button',{name:'Save duration'}).click();
 await expect(page.getByRole('region',{name:'Promocode details'}).getByText('Activated',{exact:true})).toBeVisible();
 await expect(page.getByRole('button',{name:'Save duration'})).toHaveCount(0);
 expect(writes.map(item=>item.body.expected_revision)).toEqual([1,2]);
});

test('delete needs confirmation; used and deleted cards are read only with metadata history',async({page})=>{
 let current={...row};const writes:unknown[]=[];
 await routes(page,async(route,path)=>{
  if(path==='/api/v1/operator/promocodes/'+id){await route.fulfill({json:detail(current)});return true;}
  if(path.endsWith('/delete')){writes.push(route.request().postDataJSON());current={...current,state:'deleted',revision:2};await route.fulfill({json:current});return true;}return false;
 });
 await page.goto('/admin/promocodes?lang=en');await page.getByRole('button',{name:'Open promocode'}).click();
 const card=page.getByRole('region',{name:'Promocode details'});
 await expect(card.getByText('Created by operator')).toBeVisible();await expect(card.getByText('Only the latest 50 changes are shown.')).toBeVisible();
 await page.getByLabel('Reason').fill('Remove unused code');await page.getByRole('button',{name:'Delete promocode'}).click();expect(writes).toHaveLength(0);
 await page.getByRole('button',{name:'Confirm deletion'}).click();
 expect(writes).toEqual([{expected_revision:1,reason:'Remove unused code'}]);
 await expect(card.getByText('Deleted',{exact:true})).toBeVisible();await expect(page.getByRole('button',{name:'Delete promocode'})).toHaveCount(0);
});
