import {test,expect,type Page,type Route} from '@playwright/test';
import type {components} from '../src/api/schema.gen';
type Model<K extends keyof components['schemas']>=components['schemas'][K];
const id='20000000-0000-4000-8000-000000000001',noticeID='30000000-0000-4000-8000-000000000001',previewID='40000000-0000-4000-8000-000000000001';
const profile={account:{account_id:id,email:'notices@example.test',email_verified:true,locale:'en',telegram_linked:false},csrf_token:'s'.repeat(43),capabilities:{trial_available:false}};
const operator={...profile,account:{...profile.account,account_id:'10000000-0000-4000-8000-000000000001',email:'operator@example.test'}};
const client:Model<'OperatorClient'>={account_id:id,kind:'web',display_name:'',email:profile.account.email,telegram_id:null,locale:'en',created_at:null,restricted:false,vpn_banned:false,had_subscription:false};
const none:Model<'Subscription'>={status:'none',devices:0,traffic_limit_bytes:0,traffic_used_bytes:null,observed_at:null,data_stale:true,expires_at:null,access_profile:'unknown',vpn_banned:false,access_operation_id:null,access_operation_status:null};
const counts:Model<'NoticeChannelCounts'>={pending:0,succeeded:0,failed:0,skipped:0,unknown:0,unchanged:0};
const preview=(input:Partial<Model<'NoticePreviewInput'>>={}):Model<'NoticePreview'>=>({id:previewID,notice_id:noticeID,mode:input.mode??'send',revision:(input.expected_revision??0)+1,html:'<b>Owned notice</b>',text:'Owned notice',reason:input.reason??'Owned reason',expires_at:new Date(Date.now()+15*60000).toISOString(),recipient_count:1,cabinet_count:1,telegram_count:1,email_count:0});
const batch=(p:Model<'NoticePreview'>|null=null):Model<'NoticeBatchResult'>=>({version:'operator-notices-v1',notice:p,revision:p?.revision??0,cabinet:{...counts,succeeded:p?1:0},telegram:{...counts,unknown:p?1:0},email:{...counts,skipped:p?1:0}});
const inbox:Model<'NoticeResult'>={version:'operator-notices-v1',email_enabled:false,email_available:true,page:1,per_page:20,total:1,notices:[{id:noticeID,revision:1,html:'<b>Owned notice</b>',text:'Owned notice',created_at:'2026-10-09T00:00:00Z'}]};
type Options={result?:Model<'NoticeResult'>;extra?:(r:Route,path:string)=>Promise<boolean>;profile?:()=>typeof profile;mini?:boolean;subscription?:Model<'Subscription'>};
async function fixture(page:Page,options:Options={}){
 let value=structuredClone(options.result??inbox),last=batch();
 const writes:{path:string;body:unknown;csrf:string|undefined;bearer:string|undefined}[]=[];
 const miniProfile={...profile,account:{...profile.account,display_name:'Owned notice client',telegram_id:701,telegram_linked:true}};
 if(options.mini)await page.route('https://telegram.org/js/telegram-web-app.js',r=>r.fulfill({contentType:'application/javascript',body:'window.Telegram={WebApp:{initData:"owned-notice-launch",ready(){},expand(){}}};'}));
 await page.route('**/api/v1/**',async r=>{
  const req=r.request(),path=new URL(req.url()).pathname;
  if(options.extra&&await options.extra(r,path))return;
  if(path==='/api/v1/telegram/mini-app/session')return r.fulfill({json:{...miniProfile,session_token:'mini_'+'b'.repeat(43)}});
  if(path==='/api/v1/telegram/mini-app/account')return r.fulfill({json:miniProfile});
  if(path==='/api/v1/me')return r.fulfill({json:options.profile?.()??profile});
  if(path.endsWith('/operator/session')||path.endsWith('/auth/session'))return r.fulfill({json:operator});
  if(path==='/api/v1/subscription')return r.fulfill({json:options.subscription??none});
  if(path==='/api/v1/trial-requests/current')return r.fulfill({json:{request:null}});
  if(path==='/api/v1/orders/current')return r.fulfill({json:{order:null,can_purchase:false}});
  if(path==='/api/v1/reminders')return r.fulfill({json:{version:'reminders-v1',email_enabled:false,email_available:true,reminders:[]}});
  if(path==='/api/v1/stars-subscription')return r.fulfill({json:{state:'none',order_id:null,provider_state:null,control_state:null,paid_until:null,period_phase:'none',can_cancel:false,can_resume:false,external_billing_blocked:false,needs_review:false}});
  if(path.endsWith('/auth/logout')||path.endsWith('/telegram/mini-app/logout'))return r.fulfill({status:204});
  if(path.endsWith('/operator/clients/search'))return r.fulfill({json:{clients:[client],page:req.postDataJSON().page,per_page:50,total:1}});
  if(path==='/api/v1/operator/notices/last')return r.fulfill({json:last});
  if(path==='/api/v1/notices')return r.fulfill({json:{...value,page:Number(new URL(req.url()).searchParams.get('page')??1)}});
  writes.push({path,body:req.postData()?req.postDataJSON():undefined,csrf:req.headers()['x-csrf-token'],bearer:req.headers().authorization});
  if(path==='/api/v1/operator/notices/previews')return r.fulfill({status:201,json:preview(req.postDataJSON())});
  if(path.endsWith('/confirm')){last=batch(preview());return r.fulfill({json:last});}
  if(path==='/api/v1/notices/preferences'){value={...value,email_enabled:req.postDataJSON().email_enabled};return r.fulfill({json:value});}
  if(path.endsWith('/dismiss')){value={...value,total:value.total-1,notices:value.notices.filter(n=>!path.includes(n.id))};return r.fulfill({status:204});}
  return r.fulfill({status:404,json:{error:{code:'NOT_FOUND',request_id:''}}});
 });
 return {writes};
}
for(const lang of ['ru','en'])test('operator personal preview requires explicit confirmation '+lang,async({page})=>{
 const f=await fixture(page);await page.goto('/admin/notices?lang='+lang);
 await expect(page.getByRole('heading',{name:lang==='ru'?'Уведомления':'Notices',exact:true})).toBeVisible();
 await page.getByLabel(lang==='ru'?'Поиск клиентов':'Search clients',{exact:true}).fill('notices@example.test');
 await page.getByRole('button',{name:lang==='ru'?'Найти':'Find',exact:true}).click();
 await page.getByRole('button',{name:(lang==='ru'?'Выбрать ':'Select ')+profile.account.email,exact:true}).click();
 await page.getByLabel(lang==='ru'?'Текст сообщения':'Message text',{exact:true}).fill('<b>Owned notice</b>');
 await page.getByLabel(lang==='ru'?'Комментарий операции':'Operation reason',{exact:true}).fill('Owned reason');
 await page.getByRole('button',{name:lang==='ru'?'Подготовить предпросмотр':'Prepare preview',exact:true}).click();
 const section=page.getByRole('region',{name:lang==='ru'?'Предпросмотр':'Preview',exact:true});await expect(section.getByText('Owned notice',{exact:true})).toBeVisible();
 expect(f.writes.filter(w=>w.path.endsWith('/confirm'))).toHaveLength(0);
 await section.getByRole('button',{name:lang==='ru'?'Подтвердить отправку':'Confirm send',exact:true}).click();
 await expect(page.getByRole('region',{name:lang==='ru'?'Результат операции':'Operation result',exact:true})).toBeVisible();
 expect(f.writes[0].body).toEqual({mode:'send',audience:'personal',account_id:id,body:'<b>Owned notice</b>',reason:'Owned reason'});expect(f.writes[1].body).toEqual({confirmed:true});expect(f.writes[1].csrf).toBe(operator.csrf_token);
});

test('all recipients remain a preview snapshot and lost confirmation retries the same operation',async({page})=>{
 const attempts:{path:string;body:unknown;csrf:string}[]=[];let pending=preview();
 const f=await fixture(page,{extra:async(r,path)=>{
  if(path==='/api/v1/operator/notices/previews'){pending={...preview(r.request().postDataJSON()),recipient_count:2,cabinet_count:2};await r.fulfill({status:201,json:pending});return true;}
  if(path.endsWith('/confirm')){attempts.push({path,body:r.request().postDataJSON(),csrf:r.request().headers()['x-csrf-token']});if(attempts.length===1)await r.abort();else await r.fulfill({json:{...batch(pending),cabinet:{...counts,succeeded:2},telegram:{...counts,unknown:2},email:{...counts,skipped:2}}});return true;}return false;
 }});
 await page.goto('/admin/notices?lang=en');await page.getByLabel('Recipients',{exact:true}).selectOption('all');await page.getByLabel('Message text').fill('Owned notice');await page.getByLabel('Operation reason').fill('Owned all');await page.getByRole('button',{name:'Prepare preview'}).click();
 const region=page.getByRole('region',{name:'Preview',exact:true});await expect(region).toContainText('New registrations will not be added');await expect(region).toContainText('A sent email cannot be edited or recalled');await expect(region.locator('dd').first()).toHaveText('2');
 await region.getByRole('button',{name:'Confirm send'}).click();await expect(page.getByRole('alert')).toBeVisible();await region.getByRole('button',{name:'Confirm send'}).click();
 await expect(page.getByRole('region',{name:'Operation result'})).toContainText('Unknown result: 2');expect(attempts).toHaveLength(2);expect(attempts[0]).toEqual(attempts[1]);expect(attempts[0].body).toEqual({confirmed:true});expect(f.writes.some(w=>w.path.includes('/orders'))).toBe(false);
});

test('last edit and deletion have separate previews and retain email uncertainty',async({page})=>{
 let last=batch(preview()),pending=preview();const inputs:Model<'NoticePreviewInput'>[]=[];
 await fixture(page,{extra:async(r,path)=>{
  if(path.endsWith('/notices/last')){await r.fulfill({json:last});return true;}
  if(path.endsWith('/notices/previews')){const input=r.request().postDataJSON();inputs.push(input);pending={...preview(input),id:'40000000-0000-4000-8000-00000000000'+inputs.length,html:input.mode==='edit'?input.body:last.notice!.html,text:input.mode==='edit'?'Revised':'Owned notice'};await r.fulfill({status:201,json:pending});return true;}
  if(path.endsWith('/confirm')){last={...batch(pending),email:{...counts,unchanged:1}};await r.fulfill({json:last});return true;}return false;
 }});
 await page.goto('/admin/notices?lang=en');await page.getByRole('button',{name:'Edit last notice',exact:true}).click();await page.getByLabel('Message text').fill('<i>Revised</i>');await page.getByLabel('Operation reason').fill('Owned edit');await page.getByRole('button',{name:'Prepare preview'}).click();
 await expect(page.getByRole('region',{name:'Preview',exact:true}).locator('i')).toHaveText('Revised');await page.getByRole('button',{name:'Confirm edit',exact:true}).click();await expect(page.getByRole('region',{name:'Last notice'})).toContainText('Revision 2');await expect(page.getByRole('region',{name:'Operation result'})).toContainText('Unchanged: 1');
 await page.getByRole('button',{name:'Delete last notice',exact:true}).click();await page.getByLabel('Operation reason').fill('Owned deletion');await page.getByRole('button',{name:'Prepare preview'}).click();await page.getByRole('button',{name:'Confirm deletion',exact:true}).click();
 await expect(page.getByRole('region',{name:'Last notice'})).toContainText('The notice was withdrawn');await expect(page.getByRole('button',{name:'Edit last notice'})).toHaveCount(0);expect(inputs).toEqual([{mode:'edit',body:'<i>Revised</i>',expected_revision:1,reason:'Owned edit'},{mode:'delete',expected_revision:2,reason:'Owned deletion'}]);
});

for(const variant of ['expired','superseded'])test('unusable preview requires a new explicit preview '+variant,async({page})=>{
 let confirms=0;
 await fixture(page,{extra:async(r,path)=>{
  if(variant==='expired'&&path.endsWith('/notices/previews')){await r.fulfill({status:201,json:{...preview(r.request().postDataJSON()),expires_at:new Date(Date.now()-1000).toISOString()}});return true;}
  if(path.endsWith('/confirm')){confirms++;await r.fulfill({status:409,json:{error:{code:'REQUEST_STATE_CONFLICT',message:'private state'}}});return true;}return false;
 }});
 await page.goto('/admin/notices?account_id='+id+'&lang=en');await page.getByLabel('Message text').fill('Owned notice');await page.getByLabel('Operation reason').fill('Owned reason');await page.getByRole('button',{name:'Prepare preview'}).click();
 if(variant==='expired'){await expect(page.getByText('The preview expired. Prepare a new one.')).toBeVisible();await expect(page.getByRole('button',{name:'Confirm send'})).toBeDisabled();expect(confirms).toBe(0);}
 else{await page.getByRole('button',{name:'Confirm send'}).click();await expect(page.getByRole('alert')).toContainText('stale or superseded');await expect(page.getByRole('region',{name:'Preview',exact:true})).toHaveCount(0);await expect(page.getByText('private state')).toHaveCount(0);}
});

test('late preview after a field edit cannot restore confirmation',async({page})=>{
 let reads=0,release!:()=>void;const hold=new Promise<void>(r=>release=r);
 const f=await fixture(page,{extra:async(r,path)=>{if(!path.endsWith('/notices/previews'))return false;reads++;const input=r.request().postDataJSON();if(reads===1)await hold;await r.fulfill({status:201,json:preview(input)});return true;}});
 await page.goto('/admin/notices?account_id='+id+'&lang=en');await page.getByLabel('Message text').fill('Old private body');await page.getByLabel('Operation reason').fill('Owned reason');await page.getByRole('button',{name:'Prepare preview'}).click();await expect.poll(()=>reads).toBe(1);
 await page.getByLabel('Message text').fill('Current body');release();await expect(page.getByRole('region',{name:'Preview',exact:true})).toHaveCount(0);await expect(page.getByRole('button',{name:'Confirm send'})).toHaveCount(0);expect(f.writes).toHaveLength(0);
 await page.getByRole('button',{name:'Prepare preview'}).click();await expect(page.getByRole('region',{name:'Preview',exact:true})).toBeVisible();expect(reads).toBe(2);
});

test('old confirmation arriving after a newer edit cannot roll back last notice',async({page})=>{
 let last=batch(),pending=preview(),prepares=0,confirms=0,release!:()=>void;const hold=new Promise<void>(r=>release=r);
 await fixture(page,{extra:async(r,path)=>{
  if(path.endsWith('/notices/last')){await r.fulfill({json:last});return true;}
  if(path.endsWith('/notices/previews')){prepares++;const input=r.request().postDataJSON();pending={...preview(input),id:prepares===1?previewID:'40000000-0000-4000-8000-000000000002',html:input.body,text:input.body};await r.fulfill({status:201,json:pending});return true;}
  if(path.endsWith('/confirm')){confirms++;const out=batch(pending);last=out;if(confirms===1)await hold;await r.fulfill({json:out});return true;}return false;
 }});
 await page.goto('/admin/notices?account_id='+id+'&lang=en');await page.getByLabel('Message text').fill('First body');await page.getByLabel('Operation reason').fill('Owned first');await page.getByRole('button',{name:'Prepare preview'}).click();await page.getByRole('button',{name:'Confirm send'}).click();await expect.poll(()=>confirms).toBe(1);
 await page.getByLabel('Operation reason').fill('Current reason');await page.getByRole('button',{name:'Refresh result'}).click();await page.getByRole('button',{name:'Edit last notice'}).click();await page.getByLabel('Message text').fill('Newer edited body');await page.getByLabel('Operation reason').fill('Owned edit');await page.getByRole('button',{name:'Prepare preview'}).click();await page.getByRole('button',{name:'Confirm edit'}).click();
 await expect(page.getByRole('region',{name:'Last notice'})).toContainText('Revision 2');release();await expect(page.getByRole('region',{name:'Last notice'})).toContainText('Newer edited body');await expect(page.getByRole('region',{name:'Operation result'})).toContainText('Revision 2');await expect(page.getByText('First body',{exact:true})).toHaveCount(0);
});

test('normalized formatting renders safe nodes, accessible spoiler and no overflow',async({page})=>{
 const html='<b>Bold</b> <i>Italic</i> <u>Underlined</u> <s>Strike</s>\n<pre><code class="language-go">fmt.Println(&#34;x&#34;)</code></pre><blockquote expandable>Quote</blockquote><a href="https://example.test/path?q=1&amp;x=2">Link</a><tg-spoiler>Hidden body</tg-spoiler>🙂 &amp;&lt;';
 await fixture(page,{result:{...inbox,notices:[{...inbox.notices[0],html}]}});await page.setViewportSize({width:375,height:812});await page.goto('/cabinet?lang=en');const region=page.getByRole('region',{name:'Messages',exact:true});
 await expect(region.locator('b')).toHaveText('Bold');await expect(region.locator('i')).toHaveText('Italic');await expect(region.locator('pre code')).toHaveText('fmt.Println("x")');await expect(region.locator('blockquote')).toHaveText('Quote');await expect(region.getByRole('link',{name:'Link',exact:true})).toHaveAttribute('href','https://example.test/path?q=1&x=2');await expect(region.getByRole('link',{name:'Link'})).toHaveAttribute('rel','noopener noreferrer');
  await expect(region.getByText('Hidden body',{exact:true})).toHaveCount(1);await expect(region.getByText('Hidden body',{exact:true})).not.toBeVisible();await region.getByText('Show hidden text',{exact:true}).focus();await page.keyboard.press('Enter');await expect(region.getByText('Hidden body',{exact:true})).toBeVisible();await expect(region).toContainText('🙂 &<');expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});

for(const [index,html] of ['<script>window.compromised=true</script>','<img src="https://attacker.example.test/private"/>','<img xmlns="http://www.w3.org/1999/xhtml" src="https://attacker.example.test/private"/>','<b onclick="alert(1)">Unsafe</b>','<a href="javascript:alert(1)">Unsafe</a>','<a href="https://u:p@attacker.example.test/">Unsafe</a>','<b><i>Bad</b></i>','<b>&unsupported;</b>','<!DOCTYPE x><b>Unsafe</b>'].entries())test('unsafe notice reply fails closed '+index,async({page})=>{
 let external=0;await page.route('https://attacker.example.test/**',async r=>{external++;await r.abort();});await fixture(page,{result:{...inbox,notices:[{...inbox.notices[0],html}]}});await page.goto('/cabinet?lang=en');const region=page.getByRole('region',{name:'Messages',exact:true});
 await expect(region.getByRole('alert')).toBeVisible();await expect(region.getByRole('listitem')).toHaveCount(0);await expect(region.getByRole('checkbox')).toHaveCount(0);await expect(region.getByRole('link')).toHaveCount(0);expect(external).toBe(0);expect(await page.evaluate(()=>(window as unknown as {compromised?:boolean}).compromised)).toBeUndefined();
});

test('non-string preview expiry fails closed before confirmation',async({page})=>{
 await fixture(page,{extra:async(r,path)=>{if(!path.endsWith('/notices/previews'))return false;await r.fulfill({status:201,json:{...preview(r.request().postDataJSON()),expires_at:2030}});return true;}});await page.goto('/admin/notices?account_id='+id+'&lang=en');await page.getByLabel('Message text').fill('Owned notice');await page.getByLabel('Operation reason').fill('Owned reason');await page.getByRole('button',{name:'Prepare preview'}).click();await expect(page.getByRole('alert')).toBeVisible();await expect(page.getByRole('button',{name:'Confirm send'})).toHaveCount(0);
});

for(const variant of ['version','page','count','id','revision','date','duplicate'])test('malformed notice result is rejected '+variant,async({page})=>{
 const bad:any=structuredClone(inbox);if(variant==='version')bad.version='unknown';if(variant==='page')bad.page=0;if(variant==='count')bad.total=-1;if(variant==='id')bad.notices[0].id='00000000-0000-0000-0000-000000000000';if(variant==='revision')bad.notices[0].revision=0;if(variant==='date')bad.notices[0].created_at='bad';if(variant==='duplicate'){bad.notices.push(bad.notices[0]);bad.total=2;}
 await fixture(page,{extra:async(r,path)=>{if(path!=='/api/v1/notices')return false;await r.fulfill({json:bad});return true;}});await page.goto('/cabinet?lang=en');const region=page.getByRole('region',{name:'Messages',exact:true});await expect(region.getByRole('alert')).toBeVisible();await expect(region.getByRole('checkbox')).toHaveCount(0);await expect(region.getByRole('listitem')).toHaveCount(0);
});

test('twenty records per page and dismissal refresh the current page',async({page})=>{
 const rows=Array.from({length:21},(_,i)=>({...inbox.notices[0],id:'50000000-0000-4000-8000-'+String(i+1).padStart(12,'0'),html:'Message '+(i+1),text:'Message '+(i+1)}));let records=rows;const pages:number[]=[];
 await fixture(page,{extra:async(r,path)=>{
  if(path==='/api/v1/notices'){const p=Number(new URL(r.request().url()).searchParams.get('page'));pages.push(p);await r.fulfill({json:{...inbox,page:p,total:records.length,notices:records.slice((p-1)*20,p*20)}});return true;}
  if(path.endsWith('/dismiss')){records=records.filter(n=>!path.includes(n.id));await r.fulfill({status:204});return true;}return false;
 }});
 await page.goto('/cabinet?lang=en');const region=page.getByRole('region',{name:'Messages',exact:true});await expect(region.getByRole('listitem')).toHaveCount(20);await region.getByRole('button',{name:'Next page'}).click();await expect(region.getByRole('listitem')).toHaveCount(1);await expect(region).toContainText('Message 21');await region.getByRole('button',{name:'Close message'}).click();await expect(region.getByText('No messages.')).toBeVisible();await expect(region.getByRole('heading',{name:'Messages',exact:true})).toBeFocused();await region.getByRole('button',{name:'Previous page'}).click();await expect(region.getByRole('listitem')).toHaveCount(20);expect(pages).toEqual([1,2,2,1]);
});

test('loading and safe error retry reach an empty inbox',async({page})=>{
 let reads=0,release!:()=>void;const hold=new Promise<void>(r=>release=r);
 await fixture(page,{extra:async(r,path)=>{if(path!=='/api/v1/notices')return false;reads++;if(reads===1){await hold;await r.fulfill({status:503,json:{error:{code:'SERVICE_UNAVAILABLE',message:'private raw body'}}});}else await r.fulfill({json:{...inbox,total:0,notices:[]}});return true;}});
 await page.goto('/cabinet?lang=en');const region=page.getByRole('region',{name:'Messages',exact:true});await expect(region.getByRole('status')).toContainText('Loading');release();await expect(region.getByRole('alert')).toBeVisible();await expect(page.getByText('private raw body')).toHaveCount(0);await region.getByRole('button',{name:'Retry'}).click();await expect(region.getByText('No messages.')).toBeVisible();expect(reads).toBe(2);
});

for(const enabled of [false,true])test('unavailable email prevents opt-in and still allows opt-out '+enabled,async({page})=>{
 const f=await fixture(page,{result:{...inbox,email_available:false,email_enabled:enabled}});await page.goto('/cabinet?lang=en');const region=page.getByRole('region',{name:'Messages',exact:true}),checkbox=region.getByRole('checkbox');await expect(region.getByRole('link',{name:'Set up email sign-in'})).toHaveAttribute('href','/cabinet/identity?lang=en');
 if(enabled){await checkbox.uncheck();await region.getByRole('button',{name:'Save message settings'}).click();await expect(checkbox).not.toBeChecked();expect(f.writes[0].body).toEqual({email_enabled:false});}await expect(checkbox).toBeDisabled();
});

for(const status of [400,401,403])test('write failure preserves only authorized client content '+status,async({page})=>{
 await fixture(page,{extra:async(r,path)=>{if(path!=='/api/v1/notices/preferences')return false;await r.fulfill({status,json:{error:{code:status===403?'ACCOUNT_RESTRICTED':'INVALID_INPUT',message:'private provider value'}}});return true;}});await page.goto('/cabinet?lang=en');const region=page.getByRole('region',{name:'Messages',exact:true});await region.getByRole('checkbox').check();await region.getByRole('button',{name:'Save message settings'}).click();
 if(status===401)await expect(page).toHaveURL(/\/login/);else{await expect(region.getByRole('alert')).toBeVisible();await expect(region.getByRole('listitem')).toHaveCount(status===403?0:1);}await expect(page.getByText('private provider value')).toHaveCount(0);
});

for(const status of [401,403])test('operator authorization loss clears preview and private results '+status,async({page})=>{
 await fixture(page,{extra:async(r,path)=>{if(!path.endsWith('/confirm'))return false;await r.fulfill({status,json:{error:{code:'INVALID_CREDENTIALS',message:'private result'}}});return true;}});await page.goto('/admin/notices?account_id='+id+'&lang=en');await page.getByLabel('Message text').fill('Owned notice');await page.getByLabel('Operation reason').fill('Owned reason');await page.getByRole('button',{name:'Prepare preview'}).click();await page.getByRole('button',{name:'Confirm send'}).click();await expect(page.getByRole('heading',{name:'Operator access required'})).toBeVisible();await expect(page.getByRole('region',{name:'Preview',exact:true})).toHaveCount(0);await expect(page.getByText('Owned notice')).toHaveCount(0);
});

for(const action of ['read','save'])test('old language reply cannot restore client data '+action,async({page})=>{
 let reads=0,writes=0,release!:()=>void;const hold=new Promise<void>(r=>release=r);
 await fixture(page,{extra:async(r,path)=>{
  if(path==='/api/v1/notices'){reads++;if(reads===1&&action==='read')await hold;await r.fulfill({json:reads===1?inbox:{...inbox,total:0,notices:[]}});return true;}
  if(path==='/api/v1/notices/preferences'){writes++;await hold;await r.fulfill({json:{...inbox,email_enabled:true}});return true;}return false;
 }});
 await page.goto('/cabinet?lang=en');const old=page.getByRole('region',{name:'Messages',exact:true});if(action==='save'){await old.getByRole('checkbox').check();await old.getByRole('button',{name:'Save message settings'}).click();await expect.poll(()=>writes).toBe(1);}else await expect.poll(()=>reads).toBe(1);
 await page.getByRole('button',{name:'RU',exact:true}).click();const current=page.getByRole('region',{name:'Сообщения',exact:true});await expect(current.getByText('Сообщений нет.')).toBeVisible();release();await expect(current.getByRole('checkbox')).not.toBeChecked();await expect(current.getByRole('listitem')).toHaveCount(0);
});

test('account change aborts an earlier message preference save',async({page})=>{
 await page.clock.install({time:new Date('2030-01-01T10:00:00Z')});await page.clock.pauseAt(new Date('2030-01-01T10:00:01Z'));let current=profile,saves=0,release!:()=>void;const hold=new Promise<void>(r=>release=r);
 await fixture(page,{profile:()=>current,subscription:{...none,status:'provisioning'},extra:async(r,path)=>{
  if(path==='/api/v1/notices'){await r.fulfill({json:current.account.account_id===id?inbox:{...inbox,total:0,notices:[]}});return true;}
  if(path==='/api/v1/notices/preferences'){saves++;await hold;await r.fulfill({json:{...inbox,email_enabled:true}});return true;}return false;
 }});
 await page.goto('/cabinet?lang=en');const region=page.getByRole('region',{name:'Messages',exact:true});await region.getByRole('checkbox').check();await region.getByRole('button',{name:'Save message settings'}).click();await expect.poll(()=>saves).toBe(1);current={...profile,account:{...profile.account,account_id:'60000000-0000-4000-8000-000000000001',email:'new-owner@example.test'}};await page.clock.runFor(5000);await expect(page.getByRole('heading',{name:'new-owner@example.test'})).toBeVisible();await expect(region.getByText('No messages.')).toBeVisible();release();await expect(region.getByRole('checkbox')).not.toBeChecked();await expect(region.getByRole('listitem')).toHaveCount(0);
});

test('logout clears the inbox while a prior save and logout reply are pending',async({page})=>{
 let saves=0,release!:()=>void,logout!:()=>void;const hold=new Promise<void>(r=>release=r),holdLogout=new Promise<void>(r=>logout=r);
 await fixture(page,{extra:async(r,path)=>{if(path==='/api/v1/notices/preferences'){saves++;await hold;await r.fulfill({json:{...inbox,email_enabled:true}});return true;}if(path.endsWith('/auth/logout')){await holdLogout;await r.fulfill({status:204});return true;}return false;}});
 await page.goto('/cabinet?lang=en');const region=page.getByRole('region',{name:'Messages',exact:true});await region.getByRole('checkbox').check();await region.getByRole('button',{name:'Save message settings'}).click();await expect.poll(()=>saves).toBe(1);await page.getByRole('button',{name:'Sign out',exact:true}).click();await expect(region).toHaveCount(0);release();await expect(region).toHaveCount(0);logout();await expect(page).toHaveURL(/\/login/);
});

for(const action of ['locale','unmount'])test('operator scope change drops a late preview '+action,async({page})=>{
 let previews=0,release!:()=>void;const hold=new Promise<void>(r=>release=r);
 const f=await fixture(page,{extra:async(r,path)=>{if(!path.endsWith('/notices/previews'))return false;previews++;await hold;await r.fulfill({status:201,json:preview(r.request().postDataJSON())});return true;}});await page.goto('/admin/notices?account_id='+id+'&lang=en');await page.getByLabel('Message text').fill('Owned notice');await page.getByLabel('Operation reason').fill('Owned reason');await page.getByRole('button',{name:'Prepare preview'}).click();await expect.poll(()=>previews).toBe(1);
  if(action==='locale'){await page.getByRole('button',{name:'RU',exact:true}).click();await expect(page.getByRole('heading',{name:'Уведомления',exact:true})).toBeVisible();}else{await page.getByRole('menuitem',{name:'Clients',exact:true}).click();await expect(page.getByRole('heading',{name:'Clients',exact:true})).toBeVisible();}release();await expect(page.getByRole('region',{name:/^(Preview|Предпросмотр)$/})).toHaveCount(0);expect(f.writes.filter(w=>w.path.endsWith('/confirm'))).toHaveLength(0);
});

test('raw Unicode character limit never silently truncates the message',async({page})=>{
 const inputs:Model<'NoticePreviewInput'>[]=[];await fixture(page,{extra:async(r,path)=>{if(!path.endsWith('/notices/previews'))return false;const input=r.request().postDataJSON();inputs.push(input);await r.fulfill({status:201,json:{...preview(input),html:input.body,text:input.body}});return true;}});await page.goto('/admin/notices?account_id='+id+'&lang=en');const body='я'.repeat(4095)+'🙂';await page.getByLabel('Message text').fill(body);await page.getByLabel('Operation reason').fill('Owned Unicode');await page.getByRole('button',{name:'Prepare preview'}).click();await expect(page.getByRole('region',{name:'Preview',exact:true})).toContainText(body);expect(inputs[0].body).toBe(body);await page.getByLabel('Message text').fill(body+'a');await page.getByRole('button',{name:'Prepare preview'}).click();await expect(page.getByRole('alert')).toBeFocused();await expect(page.getByLabel('Message text')).toHaveValue(body+'a');expect(inputs).toHaveLength(1);
});
for(const mini of [false,true])test('client inbox has separate consent and own close '+(mini?'Mini':'web'),async({page})=>{
 const f=await fixture(page,{mini});await page.goto((mini?'/mini-app':'')+'/cabinet?lang=en');
 const section=page.getByRole('region',{name:'Messages',exact:true});await expect(section.getByText('Owned notice',{exact:true})).toBeVisible();
 await section.getByRole('checkbox',{name:'Receive operator messages by email'}).check();await section.getByRole('button',{name:'Save message settings'}).click();await expect(section.getByRole('status')).toContainText('Settings saved');
 await section.getByRole('button',{name:'Close message'}).click();await expect(section.getByText('No messages.',{exact:true})).toBeVisible();
 expect(f.writes.map(w=>w.path)).toEqual(['/api/v1/notices/preferences','/api/v1/notices/'+noticeID+'/dismiss']);expect(f.writes.every(w=>w.csrf===profile.csrf_token)).toBe(true);expect(f.writes.every(w=>mini?!!w.bearer:!w.bearer)).toBe(true);
});

for(const from of ['list','card'])test('existing client '+from+' opens a personal notice target',async({page})=>{
 const searches:string[]=[];
 await fixture(page,{extra:async(r,path)=>{
  if(path.endsWith('/operator/clients/search')){searches.push(r.request().postDataJSON().q);await r.fulfill({json:{clients:[client],total:1,page:1,per_page:50}});return true;}
  if(path==='/api/v1/operator/clients/'+id){await r.fulfill({json:{client,subscription:none,server:{panel_id:'configured-panel',enabled:true},support:null,trial_requests:[],trial_has_more:false,audit_events:[],audit_has_more:false,legacy_approval:null,legacy_events:[],legacy_has_more:false}});return true;}
  if(path.endsWith('/support')&&r.request().method()==='GET'){await r.fulfill({json:{conversation:null,messages:[],has_more:false,oldest_sequence:null}});return true;}return false;
 }});
 await page.goto(from==='list'?'/admin/clients?lang=en':'/admin/clients/'+id+'/show?lang=en');await page.getByRole('link',{name:'Send message',exact:true}).click();await expect(page).toHaveURL('/admin/notices?account_id='+id+'&lang=en');await expect(page.getByRole('heading',{name:'Notices',exact:true})).toBeVisible();await expect(page.getByText('Selected client: '+profile.account.email,{exact:true})).toBeVisible();expect(searches).toContain(id);
});

test('Mini does not offer operator notices',async({page})=>{
 let operators=0;await fixture(page,{mini:true,extra:async(r,path)=>{if(!path.includes('/operator/'))return false;operators++;await r.fulfill({status:403,json:{error:{code:'INVALID_CREDENTIALS'}}});return true;}});await page.goto('/mini-app/admin/notices?lang=en');await expect(page.getByRole('button',{name:'Prepare preview'})).toHaveCount(0);await expect(page.getByRole('link',{name:'Notices',exact:true})).toHaveCount(0);expect(operators).toBe(0);
});

test('Russian inbox has its own consent label and message close',async({page})=>{
 const f=await fixture(page);await page.goto('/cabinet?lang=ru');const region=page.getByRole('region',{name:'Сообщения',exact:true});await region.getByRole('checkbox',{name:'Получать сообщения оператора по email'}).check();await region.getByRole('button',{name:'Сохранить настройки сообщений'}).click();await expect(region.getByText('Настройки сохранены',{exact:true})).toBeVisible();await region.getByRole('button',{name:'Закрыть сообщение'}).click();await expect(region.getByText('Сообщений нет.')).toBeVisible();expect(f.writes).toHaveLength(2);
});

test('operator logout drops a late private preview while logout is pending',async({page})=>{
 let previews=0,release!:()=>void,logout!:()=>void;const hold=new Promise<void>(r=>release=r),holdLogout=new Promise<void>(r=>logout=r);
 await fixture(page,{extra:async(r,path)=>{if(path.endsWith('/notices/previews')){previews++;await hold;await r.fulfill({status:201,json:preview(r.request().postDataJSON())});return true;}if(path.endsWith('/auth/logout')){await holdLogout;await r.fulfill({status:204});return true;}return false;}});
 await page.goto('/admin/notices?account_id='+id+'&lang=en');await page.getByLabel('Message text').fill('Owned notice');await page.getByLabel('Operation reason').fill('Owned reason');await page.getByRole('button',{name:'Prepare preview'}).click();await expect.poll(()=>previews).toBe(1);await page.getByRole('button',{name:'Profile',exact:true}).click();await page.getByRole('menuitem',{name:'Logout'}).click();release();await expect(page.getByRole('region',{name:'Preview',exact:true})).toHaveCount(0);logout();
});
