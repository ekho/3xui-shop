import {test,expect,type Page,type Route} from '@playwright/test';
import type {components} from '../src/api/schema.gen';

type Model<K extends keyof components['schemas']>=components['schemas'][K];
const account:Model<'AccountResult'>={account:{account_id:'11111111-1111-4111-8111-111111111111',email:'referrals@example.test',email_verified:true,locale:'en',telegram_linked:false},csrf_token:'c'.repeat(43),capabilities:{trial_available:false}};
const miniAccount:Model<'MiniAppAccountResult'>={...account,account:{...account.account,email:null,email_verified:false,display_name:'Referral fixture',telegram_id:701,telegram_linked:true}};
const token='mini_'+'b'.repeat(43);
const result:Model<'ReferralsResult'>={version:'2026-10-10-referrals-v1',web_url:'https://cabinet.example.test/register?invite=r_'+ 'a'.repeat(32),levels:[
 {level:1,invited:3,granted_days:'9007199254740993',pending_days:'9007199254740995',granted_rewards:2,pending_rewards:1,money_records:5,pending_money_records:4},
 {level:2,invited:2,granted_days:'9223372036854775807',pending_days:'0',granted_rewards:1,pending_rewards:0,money_records:1,pending_money_records:0}
],unclassified_records:2};
const empty:Model<'ReferralsResult'>={...result,levels:result.levels.map(level=>({...level,invited:0,granted_days:'0',pending_days:'0',granted_rewards:0,pending_rewards:0,money_records:0,pending_money_records:0})),unclassified_records:0};
const none:Model<'Subscription'>={status:'none',devices:0,traffic_limit_bytes:0,traffic_used_bytes:null,observed_at:null,data_stale:true,expires_at:null,access_profile:'unknown',vpn_banned:false,access_operation_id:null,access_operation_status:null};
type Call={path:string;search:string;method:string;authorization:string|undefined;cookie:boolean};
async function fixture(page:Page,options:{result?:Model<'ReferralsResult'>;extra?:(route:Route,path:string)=>Promise<boolean>}={}){
 const calls:Call[]=[];
 await page.route('https://telegram.org/js/telegram-web-app.js',route=>route.fulfill({contentType:'application/javascript',body:'window.Telegram={WebApp:{initData:"owned-referral-launch",ready(){},expand(){}}};'}));
 await page.route('**/api/v1/**',async route=>{
  const request=route.request(),url=new URL(request.url()),path=url.pathname;
  calls.push({path,search:url.search,method:request.method(),authorization:request.headers().authorization,cookie:!!request.headers().cookie});
  if(options.extra&&await options.extra(route,path))return;
  if(path==='/api/v1/maintenance')return route.fulfill({json:{enabled:false,revision:0,changed_at:null}});
  if(path==='/api/v1/telegram/mini-app/session')return route.fulfill({json:{...miniAccount,session_token:token}});
  if(path==='/api/v1/telegram/mini-app/account')return route.fulfill({json:miniAccount});
  if(path==='/api/v1/me')return route.fulfill({json:account});
  if(path==='/api/v1/subscription')return route.fulfill({json:none});
  if(path==='/api/v1/trial-requests/current')return route.fulfill({json:{request:null}});
  if(path==='/api/v1/orders/current')return route.fulfill({json:{order:null,can_purchase:false}});
  if(path==='/api/v1/notices')return route.fulfill({json:{version:'operator-notices-v1',email_enabled:false,email_available:true,page:1,per_page:20,total:0,notices:[]}});
  if(path==='/api/v1/reminders')return route.fulfill({json:{version:'reminders-v1',email_enabled:false,email_available:true,reminders:[]}});
  if(path==='/api/v1/stars-subscription')return route.fulfill({json:{state:'none',order_id:null,provider_state:null,control_state:null,paid_until:null,period_phase:'none',can_cancel:false,can_resume:false,external_billing_blocked:false,needs_review:false}});
  if(path==='/api/v1/referrals')return route.fulfill({json:options.result??result});
  return route.fulfill({status:404,json:{error:{code:'INVALID_INPUT',message:'Owned referral fixture'}}});
 });
 return calls;
}

for(const mini of [false,true])for(const lang of ['ru','en'] as const)test('own persisted referral facts, exact days and keyboard in '+(mini?'Mini App ':'web ')+lang,async({page,context,baseURL})=>{
 await context.addCookies([{name:'owned_browser_cookie',value:'synthetic',url:baseURL!}]);
 const calls=await fixture(page),prefix=mini?'/mini-app':'';
 await page.setViewportSize({width:375,height:812});await page.goto(prefix+'/cabinet?lang='+lang);
 const title=lang==='ru'?'Приглашения':'Invitations',open=page.getByRole('link',{name:title,exact:true});
 await expect(open).toHaveAttribute('href',prefix+'/cabinet/referrals'+(lang==='en'?'?lang=en':''));
 await open.focus();await page.keyboard.press('Enter');await expect(page).toHaveURL(new RegExp(prefix+'/cabinet/referrals'+(lang==='en'?'\\?lang=en':'')+'$'));
 const screen=page.getByRole('region',{name:title,exact:true});await expect(screen.getByRole('heading',{level:1,name:title})).toBeVisible();await expect(page).toHaveTitle(new RegExp(title));
 const first=screen.getByRole('region',{name:lang==='ru'?'Первая степень':'First level'}),second=screen.getByRole('region',{name:lang==='ru'?'Вторая степень':'Second level'});
 const field=(section:typeof first,label:string)=>section.locator('dl div').filter({has:page.getByText(label,{exact:true})}).locator('dd');
 await expect(field(first,lang==='ru'?'Приглашённые':'Invited accounts')).toHaveText('3');await expect(field(second,lang==='ru'?'Приглашённые':'Invited accounts')).toHaveText('2');
 await expect(field(first,lang==='ru'?'Выданные дни':'Granted days')).toHaveText(result.levels[0].granted_days);
 await expect(field(first,lang==='ru'?'Ожидающие дни':'Pending days')).toHaveText(result.levels[0].pending_days);
 await expect(field(second,lang==='ru'?'Выданные дни':'Granted days')).toHaveText(result.levels[1].granted_days);
 await expect(field(first,lang==='ru'?'Выданные награды днями (записи)':'Granted day rewards (records)')).toHaveText('2');
 await expect(field(first,lang==='ru'?'Ожидающие награды днями (записи)':'Pending day rewards (records)')).toHaveText('1');
 await expect(field(first,lang==='ru'?'Расчётные денежные записи':'Calculated money records')).toHaveText('5');
 await expect(field(first,lang==='ru'?'Ожидающие расчётные денежные записи':'Pending calculated money records')).toHaveText('4');
 await expect(screen).toContainText(lang==='ru'?'не подтверждают доступный баланс или выплату':'do not confirm an available balance or payout');
 await expect(screen).toContainText((lang==='ru'?'Записи наград без сохранённой степени':'Reward records without a saved level')+': 2');
 await expect(first).not.toContainText('without a saved level');await expect(second).not.toContainText('без сохранённой степени');
 const back=screen.getByRole('link',{name:lang==='ru'?'Вернуться в кабинет':'Back to cabinet'}),share=screen.getByRole('textbox',{name:lang==='ru'?'Персональная ссылка для приглашения':'Personal invitation link'});
 await expect(back).toHaveAttribute('href',prefix+'/cabinet'+(lang==='en'?'?lang=en':''));await back.focus();await page.keyboard.press('Tab');await expect(share).toBeFocused();
 await expect(share).toHaveValue(result.web_url);await expect(share).toHaveAttribute('readonly','');await page.keyboard.type('do not change');await expect(share).toHaveValue(result.web_url);
 await page.keyboard.press('ControlOrMeta+A');expect(await share.evaluate(element=>{const input=element as HTMLInputElement;return input.selectionStart===0&&input.selectionEnd===input.value.length;})).toBe(true);
 expect(await share.evaluate(element=>getComputedStyle(element).outlineStyle)).not.toBe('none');expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 expect(calls.filter(call=>call.path==='/api/v1/referrals')).toEqual([{path:'/api/v1/referrals',search:'',method:'GET',authorization:mini?'Bearer '+token:undefined,cookie:!mini}]);
 expect(await page.evaluate(()=>JSON.stringify(localStorage)+JSON.stringify(sessionStorage))).not.toMatch(/r_aaaaaaaa|mini_bbbb|referrals@example/);
 await expect(screen.getByRole('link')).toHaveCount(1);await expect(page.getByRole('alert')).toHaveCount(0);
});

for(const mini of [false,true])test('loading, safe error, keyboard retry and empty levels in '+(mini?'Mini App':'web'),async({page})=>{
 let reads=0,release!:()=>void;const hold=new Promise<void>(resolve=>release=resolve);
 await fixture(page,{result:empty,extra:async(route,path)=>{if(path!=='/api/v1/referrals')return false;reads++;if(reads===1){await hold;await route.fulfill({status:503,json:{error:{code:'SERVICE_UNAVAILABLE',message:'PRIVATE_PROVIDER_DETAIL'}}});return true;}return false;}});
 await page.goto((mini?'/mini-app':'')+'/cabinet/referrals?lang=en');const screen=page.getByRole('region',{name:'Invitations',exact:true});
 await expect(screen.getByRole('status')).toContainText('Loading');await expect(screen.getByRole('textbox')).toHaveCount(0);
 release();await expect(screen.getByRole('alert')).toBeVisible();await expect(page.locator('body')).not.toContainText('PRIVATE_PROVIDER_DETAIL');
 await screen.getByRole('button',{name:'Retry'}).focus();await page.keyboard.press('Enter');
 await expect(screen.getByRole('status')).toHaveText('No invitations or reward records yet.');await expect(screen.getByRole('heading',{level:1})).toBeFocused();
 await expect(screen.getByRole('textbox',{name:'Personal invitation link'})).toHaveValue(result.web_url);await expect(screen.locator('dd')).toHaveCount(14);
 for(const cell of await screen.locator('dd').all())await expect(cell).toHaveText('0');
 await expect(screen.getByRole('region')).toHaveCount(2);await expect(screen.getByRole('alert')).toHaveCount(0);expect(reads).toBe(2);
});

for(const mini of [false,true])test('unauthorized referrals end the existing '+(mini?'Mini App session':'web session'),async({page})=>{
 const calls=await fixture(page,{extra:async(route,path)=>{if(path!=='/api/v1/referrals')return false;await route.fulfill({status:401,json:{error:{code:'AUTH_REQUIRED',message:'PRIVATE_AUTH_DETAIL'}}});return true;}});
 await page.goto((mini?'/mini-app':'')+'/cabinet/referrals?lang=en');
 if(mini){await expect(page.getByRole('alert')).toContainText('Session ended. Reopen the cabinet from Telegram.');expect(calls.filter(call=>call.path==='/api/v1/telegram/mini-app/session')).toHaveLength(1);}
 else await expect(page).toHaveURL(/\/login\?lang=en$/);
 await expect(page.getByRole('textbox',{name:'Personal invitation link'})).toHaveCount(0);await expect(page.locator('body')).not.toContainText(result.levels[0].granted_days);await expect(page.locator('body')).not.toContainText('PRIVATE_AUTH_DETAIL');
});

test('a restricted refresh removes previously displayed facts',async({page})=>{
 let restricted=false;
 await fixture(page,{extra:async(route,path)=>{if(path!=='/api/v1/referrals'||!restricted)return false;await route.fulfill({status:403,json:{error:{code:'ACCOUNT_RESTRICTED',message:'PRIVATE_RESTRICTION_DETAIL'}}});return true;}});
 await page.goto('/cabinet/referrals?lang=en');await expect(page.getByRole('textbox',{name:'Personal invitation link'})).toHaveValue(result.web_url);
 restricted=true;await page.getByRole('button',{name:'RU',exact:true}).click();
 await expect(page.getByRole('alert')).toBeVisible();await expect(page.getByRole('textbox')).toHaveCount(0);await expect(page.locator('body')).not.toContainText(result.levels[0].granted_days);await expect(page.locator('body')).not.toContainText('PRIVATE_RESTRICTION_DETAIL');
});

test('a late referral reply cannot revive an ended Mini App session',async({page})=>{
 let release!:()=>void;const hold=new Promise<void>(resolve=>release=resolve);
 const calls=await fixture(page,{extra:async(route,path)=>{if(path!=='/api/v1/referrals')return false;await hold;await route.fulfill({json:result}).catch(()=>{});return true;}});
 await page.goto('/mini-app/cabinet/referrals?lang=en');await expect(page.getByRole('region',{name:'Invitations',exact:true}).getByRole('status')).toContainText('Loading');
 await page.evaluate(()=>window.dispatchEvent(new CustomEvent('mini-session-ended')));await expect(page.getByRole('alert')).toContainText('Session ended');
 release();await page.unrouteAll({behavior:'wait'});await expect(page.getByRole('textbox')).toHaveCount(0);await expect(page.locator('body')).not.toContainText(result.levels[0].granted_days);expect(calls.filter(call=>call.path==='/api/v1/telegram/mini-app/session')).toHaveLength(1);
});

for(const variant of ['days','fractional-days','negative-days','levels'])test('malformed referral '+variant+' is a safe error',async({page})=>{
 const bad:any=structuredClone(result);if(variant==='days')bad.levels[0].granted_days=9007199254740992;else if(variant==='fractional-days')bad.levels[0].granted_days='1.5';else if(variant==='negative-days')bad.levels[0].pending_days='-1';else bad.levels[1].level=1;
 await fixture(page,{extra:async(route,path)=>{if(path!=='/api/v1/referrals')return false;await route.fulfill({json:bad});return true;}});
 await page.goto('/cabinet/referrals?lang=en');const screen=page.getByRole('region',{name:'Invitations',exact:true});
 await expect(screen.getByRole('alert')).toBeVisible();await expect(screen.getByRole('textbox')).toHaveCount(0);await expect(screen.getByRole('heading',{level:2})).toHaveCount(0);await expect(screen.getByRole('button',{name:'Retry'})).toBeEnabled();
});

test('unclassified legacy records remain outside both zero levels',async({page})=>{
 await fixture(page,{result:{...empty,unclassified_records:3}});await page.goto('/cabinet/referrals?lang=en');const screen=page.getByRole('region',{name:'Invitations',exact:true});
 await expect(screen).toContainText('Reward records without a saved level: 3');await expect(screen.getByRole('status')).toHaveCount(0);
 for(const section of await screen.getByRole('region').all()){await expect(section).not.toContainText('without a saved level');for(const cell of await section.locator('dd').all())await expect(cell).toHaveText('0');}
});
