import {test,expect,type Page, type Route} from '@playwright/test';
import type {components} from '../src/api/schema.gen';

type Model<K extends keyof components['schemas']> = components['schemas'][K];
type SupportResult=Model<'SupportResult'>;
const csrf='x'.repeat(43);
const account={account:{account_id:'11111111-1111-4111-8111-111111111111',email:'client@example.test',email_verified:true,locale:'en',telegram_linked:false},csrf_token:csrf,capabilities:{trial_available:true}};
const conversation:Model<'SupportConversation'>={id:'22222222-2222-4222-8222-222222222222',status:'open',support_banned:false,created_at:'2026-10-02T10:00:00Z',updated_at:'2026-10-02T10:05:00Z',customer_received_sequence:0,operator_received_sequence:0};
const message=(sequence:number,sender:'customer'|'operator'='customer',text='Message '+sequence,delivery:'stored'|'delivered'='stored',attachment:Model<'SupportAttachment'>|null=null):Model<'SupportMessage'>=>({id:`00000000-0000-4000-8000-${String(sequence).padStart(12,'0')}`,sequence,sender,text,created_at:'2026-10-02T10:00:00Z',attachment,delivery});
const empty:SupportResult={conversation:null,messages:[],has_more:false,oldest_sequence:null};

async function baseRoutes(page:Page,getSupport:()=>Promise<SupportResult>|SupportResult=()=>empty,extra?:(route:Route,path:string)=>Promise<boolean>){
 await page.route('**/api/v1/**',async route=>{
  const request=route.request(),path=new URL(request.url()).pathname;
  if(extra&&await extra(route,path))return;
  if(path.endsWith('/me'))return route.fulfill({json:account});
  if(path.endsWith('/support')&&request.method()==='GET')return route.fulfill({json:await getSupport()}).catch(()=>{});
  if(path.endsWith('/support/read'))return route.fulfill({status:204});
  if(path.endsWith('/auth/logout'))return route.fulfill({status:204});
  return route.fulfill({json:{}});
 });
}

test('read acknowledgement uses the highest displayed operator sequence',async({page})=>{
 let state={...conversation},readSequence=0;await baseRoutes(page,()=>({conversation:state,messages:[message(3,'operator','Reply'),message(4,'customer','Follow-up')],has_more:false,oldest_sequence:3}),async(route,path)=>{if(path.endsWith('/support/read')){const body=route.request().postDataJSON() as {sequence:number};readSequence=body.sequence;if(body.sequence!==3){await route.fulfill({status:409,json:{error:{code:'INVALID_SEQUENCE'}}});return true;}state={...state,customer_received_sequence:body.sequence};await route.fulfill({status:204});return true;}return false;});
 await page.goto('/cabinet/support?lang=en');await expect(page.getByText('Reply')).toBeVisible();await expect.poll(()=>readSequence).toBe(3);expect(state.customer_received_sequence).toBe(3);
});

test('history is sorted and deduplicated with delivery and protected attachment labels',async({page})=>{
 const latest:SupportResult={conversation,messages:[message(52,'operator','<img src=external.example.test>','stored',{name:'notes.txt',size_bytes:12}),message(51,'customer','Question','delivered')],has_more:true,oldest_sequence:51};let historyBody:unknown,ackBody:unknown;
 await baseRoutes(page,()=>latest,async(route,path)=>{
  if(path.endsWith('/support/history')){historyBody=route.request().postDataJSON();await route.fulfill({json:{conversation,messages:[message(51,'customer','Question','delivered'),message(49,'operator','Old reply','delivered'),message(50)],has_more:false,oldest_sequence:49}});return true;}
  if(path.endsWith('/support/read')){ackBody=route.request().postDataJSON();expect(route.request().headers()['x-csrf-token']).toBe(csrf);await route.fulfill({status:204});return true;}return false;
 });
 await page.goto('/cabinet/support?lang=en');await expect(page.getByRole('heading',{name:'Support messages'})).toBeVisible();await expect.poll(()=>ackBody).toEqual({sequence:52});
 expect(await page.locator('[data-sequence]').evaluateAll(items=>items.map(item=>item.getAttribute('data-sequence')))).toEqual(['51','52']);await expect(page.getByText('Stored in support',{exact:true})).toBeVisible();await expect(page.getByText('Delivered to recipient cabinet',{exact:true})).toBeVisible();
 const attachment=page.getByRole('link',{name:/Download notes.txt/});await expect(attachment).toHaveAttribute('download','notes.txt');await expect(attachment).toHaveAttribute('href','/api/v1/support/messages/00000000-0000-4000-8000-000000000052/attachment');expect(await page.locator('img,object,iframe').count()).toBe(0);
 await page.getByRole('button',{name:'Load older messages'}).click();expect(historyBody).toEqual({before_sequence:51});await expect(page.locator('[data-sequence]')).toHaveCount(4);expect(await page.locator('[data-sequence]').evaluateAll(items=>items.map(item=>item.getAttribute('data-sequence')))).toEqual(['49','50','51','52']);await expect(page.getByRole('button',{name:'Load older messages'})).toHaveCount(0);
});

test('text send keeps explicit stored meaning and closed conversation can reopen',async({page})=>{
 let state=conversation;const states:string[]=[],keys:string[]=[];await baseRoutes(page,()=>({conversation:state,messages:[],has_more:false,oldest_sequence:null}),async(route,path)=>{
  if(path.endsWith('/support/messages')){expect(route.request().headers()['content-type']).toContain('application/json');expect(route.request().headers()['x-csrf-token']).toBe(csrf);keys.push(route.request().headers()['idempotency-key']);expect(route.request().postDataJSON()).toEqual({text:'Need help'});state={...state,status:'open'};await route.fulfill({status:201,json:message(1,'customer','Need help','stored')});return true;}
  if(path.endsWith('/support/state')){const body=route.request().postDataJSON() as {status:string};states.push(body.status);state={...state,status:body.status as 'open'|'closed'};await route.fulfill({status:204});return true;}return false;
 });
 await page.goto('/cabinet/support?lang=en');await page.getByRole('button',{name:'Close conversation'}).click();await expect(page.getByText('Conversation closed.')).toBeVisible();await page.getByLabel('Message').fill('Need help');await page.getByRole('button',{name:'Reopen and send'}).click();await expect(page.getByText('Need help')).toBeVisible();await expect(page.getByText('Stored in support',{exact:true})).toBeVisible();expect(states).toEqual(['closed']);expect(keys).toHaveLength(1);
 await page.getByRole('button',{name:'Close conversation'}).click();await page.getByRole('button',{name:'Reopen conversation'}).click();expect(states).toEqual(['closed','closed','open']);
});

test('file uses one multipart request and client limits are accessible',async({page})=>{
 let contentType='',body='';await page.setViewportSize({width:375,height:812});await baseRoutes(page,()=>empty,async(route,path)=>{if(path.endsWith('/support/messages')){contentType=route.request().headers()['content-type'];body=route.request().postData()??'';await route.fulfill({status:201,json:message(1,'customer','', 'stored',{name:'report.txt',size_bytes:7})});return true;}return false;});
 await page.goto('/cabinet/support');const text=page.getByLabel('Сообщение');await text.fill('я'.repeat(4001));await page.getByRole('button',{name:'Отправить'}).click();await expect(page.getByRole('alert')).toContainText('4000');await text.fill('');await page.getByLabel('Файл').setInputFiles({name:'report.txt',mimeType:'text/plain',buffer:Buffer.from('fixture')});
 await page.getByRole('button',{name:'Отправить'}).focus();await page.keyboard.press('Enter');await expect(page.getByText('report.txt')).toBeVisible();expect(contentType).toContain('multipart/form-data; boundary=');expect(body).toContain('report.txt');expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await page.getByLabel('Файл').setInputFiles({name:'large.bin',mimeType:'application/octet-stream',buffer:Buffer.alloc(10*1024*1024+1)});await page.getByRole('button',{name:'Отправить'}).click();await expect(page.getByRole('alert')).toContainText('10 MiB');
});

for(const failure of ['lost','429','503'] as const)test(failure+' response retains draft file and idempotency key without browser persistence',async({page})=>{
 const keys:string[]=[],logs:string[]=[];let calls=0;const privateDraft='Private fixture draft';page.on('console',entry=>logs.push(entry.text()));await baseRoutes(page,()=>empty,async(route,path)=>{if(path.endsWith('/support/messages')){calls++;keys.push(route.request().headers()['idempotency-key']);if(calls===1){if(failure==='lost')await route.abort();else await route.fulfill({status:Number(failure),headers:failure==='429'?{'Retry-After':'30'}:{},json:{error:{code:failure==='429'?'RATE_LIMITED':'SERVICE_UNAVAILABLE'}}});}else await route.fulfill({status:201,json:message(1,'customer',privateDraft)});return true;}return false;});
 await page.goto('/cabinet/support?lang=en');await page.getByLabel('Message').fill(privateDraft);await page.getByLabel('File').setInputFiles({name:'draft.txt',mimeType:'text/plain',buffer:Buffer.from('draft-file')});await page.getByRole('button',{name:'Send'}).click();await expect(page.getByRole('alert')).toBeVisible();await expect(page.getByLabel('Message')).toHaveValue(privateDraft);await expect(page.getByLabel('File')).toHaveValue(/draft.txt/);
 await page.getByRole('button',{name:'Send'}).click();await expect(page.getByText(privateDraft)).toBeVisible();expect(keys[0]).toBe(keys[1]);expect(await page.evaluate(()=>JSON.stringify([localStorage,sessionStorage]))).not.toContain(privateDraft);expect(logs.join('')).not.toContain(privateDraft);
});

test('support ban preserves history and blocks new message or reopen',async({page})=>{
 const banned={...conversation,status:'closed' as const,support_banned:true};await baseRoutes(page,()=>({conversation:banned,messages:[message(1,'operator','Policy explanation','delivered')],has_more:false,oldest_sequence:1}));await page.goto('/cabinet/support?lang=en');
 await expect(page.getByText('Policy explanation')).toBeVisible();await expect(page.getByRole('alert')).toContainText('blocked new messages');await expect(page.getByLabel('Message')).toHaveCount(0);await expect(page.getByRole('button',{name:/Reopen/})).toHaveCount(0);await expect(page.getByRole('link',{name:'Back to account'})).toBeVisible();await expect(page.getByRole('link',{name:'Account security'})).toBeVisible();
});

test('composer and logout wait for a confirmed session context',async({page})=>{
 await baseRoutes(page,()=>empty,async(route,path)=>{if(path.endsWith('/me')){await route.fulfill({status:503,json:{error:{code:'SERVICE_UNAVAILABLE'}}});return true;}return false;});await page.goto('/cabinet/support?lang=en');await expect(page.getByRole('alert')).toBeVisible();await expect(page.getByLabel('Message')).toHaveCount(0);await expect(page.getByRole('button',{name:'Sign out'})).toBeDisabled();
});

test('polling waits for the response, pauses when hidden and aborts on logout',async({page})=>{
 await page.clock.install();let reads=0,release!:()=>void;const wait=new Promise<void>(resolve=>release=resolve);await baseRoutes(page,async()=>{reads++;if(reads===2)await wait;return empty;});await page.goto('/cabinet/support?lang=en');await expect.poll(()=>reads).toBe(1);await page.clock.runFor(5000);await expect.poll(()=>reads).toBe(2);await page.clock.runFor(10000);expect(reads).toBe(2);
 await page.evaluate(()=>{Object.defineProperty(document,'visibilityState',{configurable:true,value:'hidden'});document.dispatchEvent(new Event('visibilitychange'));});release();await page.clock.runFor(10000);expect(reads).toBe(2);await page.getByRole('button',{name:'Sign out'}).click();await expect(page).toHaveURL(/\/login/);await page.clock.runFor(10000);expect(reads).toBe(2);
});
