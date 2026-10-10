import {readFileSync} from 'node:fs';
import {chromium,expect} from '@playwright/test';

const origin=process.env.TEST_ORIGIN;
const fixture=JSON.parse(readFileSync(process.env.TEST_NATIVE_REFERRALS_FILE,'utf8'));
if(!origin?.startsWith('https://')||!fixture.client_cookie||!fixture.mini_init_data||!fixture.web_url||!fixture.registration_email)throw new Error('native fixture unavailable');
const browser=await chromium.launch();
let checkpoint='web facts';
const field=(page,section,label)=>section.locator('dl div').filter({has:page.getByText(label,{exact:true})}).locator('dd');
try{
 const webContext=await browser.newContext({ignoreHTTPSErrors:true});
 await webContext.addCookies([{name:'__Host-session',value:fixture.client_cookie,url:origin,httpOnly:true,secure:true,sameSite:'Lax'}]);
 const web=await webContext.newPage();
 await web.goto(origin+'/cabinet?lang=ru');
 await web.getByRole('link',{name:'Приглашения',exact:true}).focus();await web.keyboard.press('Enter');
 const screen=web.getByRole('region',{name:'Приглашения',exact:true});
 await expect(screen.getByRole('heading',{level:1})).toBeVisible();
 const first=screen.getByRole('region',{name:'Первая степень'}),second=screen.getByRole('region',{name:'Вторая степень'});
 await expect(field(web,first,'Приглашённые')).toHaveText('1');
 await expect(field(web,first,'Выданные дни')).toHaveText('9007199254740993');
 await expect(field(web,first,'Ожидающие дни')).toHaveText('4');
 await expect(field(web,second,'Выданные дни')).toHaveText('3');
 await expect(field(web,second,'Ожидающие дни')).toHaveText('2');
 await expect(screen).toContainText('Записи наград без сохранённой степени: 1');
 const share=screen.getByRole('textbox',{name:'Персональная ссылка для приглашения'});
 await screen.getByRole('link',{name:'Вернуться в кабинет'}).focus();await web.keyboard.press('Tab');
 await expect(share).toBeFocused();await expect(share).toHaveValue(fixture.web_url);await web.keyboard.press('ControlOrMeta+A');
 expect(await share.evaluate(input=>input.selectionStart===0&&input.selectionEnd===input.value.length)).toBe(true);
 await web.getByRole('button',{name:'EN',exact:true}).click();
 await expect(web.getByRole('region',{name:'Invitations',exact:true})).toContainText('do not confirm an available balance or payout');

 checkpoint='signed Mini facts';
 const miniContext=await browser.newContext({ignoreHTTPSErrors:true});
 const mini=await miniContext.newPage();
 await mini.route('https://telegram.org/js/telegram-web-app.js',route=>route.fulfill({contentType:'application/javascript',body:`window.Telegram={WebApp:{initData:${JSON.stringify(fixture.mini_init_data)},version:'9.6',platform:'web',themeParams:{},ready(){},expand(){},onEvent(){},offEvent(){},BackButton:{show(){},hide(){},onClick(){},offClick(){}}}};`}));
 await mini.goto(origin+'/mini-app/cabinet?lang=en');
 await mini.getByRole('link',{name:'Invitations',exact:true}).click();
 const miniScreen=mini.getByRole('region',{name:'Invitations',exact:true});
 await expect(miniScreen.getByRole('textbox',{name:'Personal invitation link'})).toHaveValue(fixture.web_url);
 await expect(field(mini,miniScreen.getByRole('region',{name:'First level'}),'Granted days')).toHaveText('9007199254740993');
 await expect(field(mini,miniScreen.getByRole('region',{name:'First level'}),'Pending days')).toHaveText('4');
 await expect(miniScreen.getByRole('link',{name:'Back to cabinet'})).toHaveAttribute('href','/mini-app/cabinet?lang=en');
 await expect(mini.getByRole('alert')).toHaveCount(0);

 checkpoint='real invitation registration';
 const guestContext=await browser.newContext({ignoreHTTPSErrors:true});
 const guest=await guestContext.newPage();
 await guest.goto(fixture.web_url);
 await guest.getByRole('button',{name:'EN',exact:true}).click();
 await guest.getByRole('textbox',{name:'Email',exact:true}).fill(fixture.registration_email);
 await guest.getByRole('checkbox',{name:/terms of use/i}).check();await guest.getByRole('checkbox',{name:/privacy/i}).check();
 await guest.getByRole('button',{name:'Continue',exact:true}).click();
 await expect(guest.getByRole('status').filter({hasText:'If the address is available'})).toBeVisible();
 await expect(guest.getByRole('alert')).toHaveCount(0);
 console.log('PASS: live TLS web, signed Mini, exact reward facts, ru/en, keyboard share, invitation registration');
}catch{console.error('FAIL: native referrals browser checkpoint '+checkpoint);process.exitCode=1;}finally{await browser.close();}
